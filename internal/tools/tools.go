// Package tools implements the read-only Kubernetes operations kubewhy can
// call: get, describe, logs, events, top. Every function here issues exactly
// one kind of API call (get/list/watch or a logs subresource read) -- none of
// them can mutate cluster state.
//
// That's not just a convention: kubewhy is meant to run as a ServiceAccount
// whose ClusterRole only grants get/list/watch (see
// deploy/readonly-clusterrole.yaml), so even a prompt-injected or
// hallucinating model gets a 403 from the API server if it tries anything
// else.
package tools

import (
	"context"
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	metricsv "k8s.io/metrics/pkg/client/clientset/versioned"
)

type Client struct {
	core    kubernetes.Interface
	metrics metricsv.Interface
}

// Core exposes the underlying clientset for callers that need lower-level
// read-only access (e.g. the watcher's pod-listing poll loop).
func (c *Client) Core() kubernetes.Interface { return c.core }

func LoadClient() (*Client, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("loading kubeconfig: %w", err)
	}
	cs, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("building clientset: %w", err)
	}
	ms, err := metricsv.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("building metrics clientset: %w", err)
	}
	return &Client{core: cs, metrics: ms}, nil
}

type GetResourceInput struct {
	Kind          string `json:"kind"`
	Namespace     string `json:"namespace"`
	Name          string `json:"name,omitempty"`
	LabelSelector string `json:"label_selector,omitempty"`
}

func (c *Client) GetResource(ctx context.Context, in GetResourceInput) (any, error) {
	opts := metav1.ListOptions{LabelSelector: in.LabelSelector}
	switch in.Kind {
	case "pod":
		if in.Name != "" {
			return c.core.CoreV1().Pods(in.Namespace).Get(ctx, in.Name, metav1.GetOptions{})
		}
		return c.core.CoreV1().Pods(in.Namespace).List(ctx, opts)
	case "deployment":
		if in.Name != "" {
			return c.core.AppsV1().Deployments(in.Namespace).Get(ctx, in.Name, metav1.GetOptions{})
		}
		return c.core.AppsV1().Deployments(in.Namespace).List(ctx, opts)
	case "replicaset":
		return c.core.AppsV1().ReplicaSets(in.Namespace).List(ctx, opts)
	case "service":
		if in.Name != "" {
			return c.core.CoreV1().Services(in.Namespace).Get(ctx, in.Name, metav1.GetOptions{})
		}
		return c.core.CoreV1().Services(in.Namespace).List(ctx, metav1.ListOptions{})
	case "node":
		if in.Name != "" {
			return c.core.CoreV1().Nodes().Get(ctx, in.Name, metav1.GetOptions{})
		}
		return c.core.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	default:
		return nil, fmt.Errorf("unsupported kind for get_resource: %s", in.Kind)
	}
}

type DescribePodInput struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

type DescribePodOutput struct {
	Pod    *corev1.Pod     `json:"pod"`
	Events []corev1.Event  `json:"events"`
}

func (c *Client) DescribePod(ctx context.Context, in DescribePodInput) (*DescribePodOutput, error) {
	pod, err := c.core.CoreV1().Pods(in.Namespace).Get(ctx, in.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	events, err := c.GetEvents(ctx, GetEventsInput{Namespace: in.Namespace, InvolvedObject: in.Name})
	if err != nil {
		return nil, err
	}
	return &DescribePodOutput{Pod: pod, Events: events}, nil
}

type GetLogsInput struct {
	Namespace string `json:"namespace"`
	Pod       string `json:"pod"`
	Container string `json:"container,omitempty"`
	Previous  bool   `json:"previous,omitempty"`
	TailLines *int64 `json:"tail_lines,omitempty"`
}

func (c *Client) GetLogs(ctx context.Context, in GetLogsInput) (string, error) {
	tail := int64(200)
	if in.TailLines != nil {
		tail = *in.TailLines
	}
	opts := &corev1.PodLogOptions{
		Container: in.Container,
		Previous:  in.Previous,
		TailLines: &tail,
	}
	req := c.core.CoreV1().Pods(in.Namespace).GetLogs(in.Pod, opts)
	raw, err := req.DoRaw(ctx)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

type GetEventsInput struct {
	Namespace      string `json:"namespace"`
	InvolvedObject string `json:"involved_object,omitempty"`
}

func (c *Client) GetEvents(ctx context.Context, in GetEventsInput) ([]corev1.Event, error) {
	list, err := c.core.CoreV1().Events(in.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	events := list.Items
	if in.InvolvedObject != "" {
		filtered := events[:0]
		for _, e := range events {
			if e.InvolvedObject.Name == in.InvolvedObject {
				filtered = append(filtered, e)
			}
		}
		events = filtered
	}
	sort.Slice(events, func(i, j int) bool {
		return events[i].LastTimestamp.Time.Before(events[j].LastTimestamp.Time)
	})
	return events, nil
}

type TopPodsInput struct {
	Namespace string `json:"namespace"`
}

func (c *Client) TopPods(ctx context.Context, in TopPodsInput) (any, error) {
	list, err := c.metrics.MetricsV1beta1().PodMetricses(in.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return map[string]string{"error": "metrics-server unavailable or not installed: " + err.Error()}, nil
	}
	return list.Items, nil
}
