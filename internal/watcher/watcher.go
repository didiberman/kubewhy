// Package watcher continuously polls pod health across the cluster using
// only get/list calls (no watch streams, to keep the implementation simple
// and the RBAC footprint identical to the rest of kubewhy). It never
// diagnoses anything itself -- it just decides *whether* a pod looks broken,
// cheaply, so the expensive LLM investigation only runs on things that
// actually need it.
package watcher

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type Status string

const (
	StatusHealthy Status = "healthy"
	StatusWarning Status = "warning" // e.g. still starting up, pending
	StatusBroken  Status = "broken"  // crashlooping, OOMKilled, failed
)

// PodHealth is a point-in-time read of one pod's health.
type PodHealth struct {
	Namespace    string
	Name         string
	Phase        string
	Restarts     int32
	Status       Status
	Reason       string // e.g. "OOMKilled", "CrashLoopBackOff", "Pending too long"
	LastObserved time.Time
}

func (p PodHealth) Key() string { return p.Namespace + "/" + p.Name }

// Snapshot polls every pod (optionally scoped to one namespace) and
// classifies each one. It's a single `list pods` call -- read-only, cheap.
func Snapshot(ctx context.Context, client kubernetes.Interface, namespace string) ([]PodHealth, error) {
	ns := namespace
	list, err := client.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing pods: %w", err)
	}

	now := time.Now()
	out := make([]PodHealth, 0, len(list.Items))
	for _, pod := range list.Items {
		out = append(out, classify(pod, now))
	}
	return out, nil
}

func classify(pod corev1.Pod, now time.Time) PodHealth {
	h := PodHealth{
		Namespace:    pod.Namespace,
		Name:         pod.Name,
		Phase:        string(pod.Status.Phase),
		Status:       StatusHealthy,
		LastObserved: now,
	}

	var maxRestarts int32
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.RestartCount > maxRestarts {
			maxRestarts = cs.RestartCount
		}
		if cs.State.Waiting != nil {
			switch cs.State.Waiting.Reason {
			case "CrashLoopBackOff", "ImagePullBackOff", "ErrImagePull", "CreateContainerConfigError":
				h.Status = StatusBroken
				h.Reason = cs.State.Waiting.Reason
			}
		}
		if cs.LastTerminationState.Terminated != nil && cs.LastTerminationState.Terminated.Reason == "OOMKilled" {
			h.Status = StatusBroken
			h.Reason = "OOMKilled"
		}
	}
	h.Restarts = maxRestarts

	if pod.Status.Phase == corev1.PodFailed {
		h.Status = StatusBroken
		if h.Reason == "" {
			h.Reason = "Failed"
		}
	}

	if pod.Status.Phase == corev1.PodPending && now.Sub(pod.CreationTimestamp.Time) > 60*time.Second {
		if h.Status == StatusHealthy {
			h.Status = StatusWarning
			h.Reason = "stuck Pending"
		}
	}

	// Missing resource requests isn't a crash, but it silently breaks
	// autoscaling: HPA can't compute a percentage against zero, and Cluster
	// Autoscaler / Karpenter can't size nodes for a pod with no declared
	// footprint. Worth surfacing even though nothing is actively failing.
	if h.Status == StatusHealthy {
		for _, c := range pod.Spec.Containers {
			if c.Resources.Requests.Cpu().IsZero() && c.Resources.Requests.Memory().IsZero() {
				h.Status = StatusWarning
				h.Reason = "no resource requests set (breaks HPA / cluster-autoscaler sizing)"
				break
			}
		}
	}

	if h.Status == StatusHealthy && maxRestarts > 0 {
		h.Status = StatusWarning
		h.Reason = fmt.Sprintf("%d restart(s)", maxRestarts)
	}

	return h
}
