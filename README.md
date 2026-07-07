# kubewhy

[![build](https://github.com/didiberman/kubewhy/actions/workflows/build.yml/badge.svg)](https://github.com/didiberman/kubewhy/actions/workflows/build.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/didiberman/kubewhy)](https://goreportcard.com/report/github.com/didiberman/kubewhy)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

**Ask your Kubernetes cluster "why" in plain English.** kubewhy investigates
step by step and narrates its reasoning as it goes -- and it is read-only by
construction, so it's safe to point at a live incident in production.

```
$ kubewhy "why is checkout crashlooping in the prod namespace?"

kubewhy (anthropic/claude-sonnet-4.5) investigating: why is checkout crashlooping in the prod namespace?

  → checking get_resource for "prod"
    equivalent: kubectl get pod  -n prod
  → checking describe_pod for "checkout-7cf7c94d78-7lzxs"
    equivalent: kubectl describe pod checkout-7cf7c94d78-7lzxs -n prod
  → checking get_logs for "checkout-7cf7c94d78-7lzxs"
    equivalent: kubectl logs checkout-7cf7c94d78-7lzxs -n prod --previous
  → checking get_events for "checkout-7cf7c94d78-7lzxs"
    equivalent: kubectl get events -n prod --field-selector involvedObject.name=checkout-7cf7c94d78-7lzxs

Answer
Root cause: the container is being OOMKilled. It's asked to allocate 300Mi
of memory but the pod's memory limit is only 100Mi (exit code 137, restart
count climbing). Fix: raise the limit to >=300Mi or reduce what the process
allocates.

Verify yourself:
  kubectl describe pod -n prod -l app=checkout
  kubectl get events -n prod --sort-by=.lastTimestamp | grep -i oom
```

That transcript is real output from this repo's demo cluster (see
[Demo](#demo) below).

## Table of contents

- [Why read-only](#why-read-only)
- [Why it teaches you](#why-it-teaches-you)
- [How it works](#how-it-works)
- [Install](#install)
- [Usage](#usage)
- [Running with enforced read-only RBAC](#running-with-enforced-read-only-rbac)
- [Demo](#demo)
- [What it can investigate today (v0.1)](#what-it-can-investigate-today-v01)
- [Roadmap](#roadmap)
- [Contributing](#contributing)
- [License](#license)

## Why read-only

Agent frameworks for Kubernetes (kagent and similar) let agents *act* on a
cluster -- deploy, patch, scale, delegate to other agents. kubewhy does the
opposite on purpose: it only investigates, and never mutates anything.

That's not a prompt instruction the model could ignore or a system message
that a jailbreak could bypass -- it's enforced by RBAC. Bind kubewhy to the
ServiceAccount in [`deploy/readonly-clusterrole.yaml`](deploy/readonly-clusterrole.yaml),
which only grants `get`/`list`/`watch`, and the Kubernetes API server itself
rejects anything else, no matter what the model tries to call. Even a
compromised or hallucinating model can't do more than read.

This is what makes it safe to run against a live incident: no write access
means no blast radius, so there's nothing to review or approve before you
use it.

## Why it teaches you

Every tool call kubewhy makes is narrated *before* it runs -- what it's about
to check, why, and the exact `kubectl` command it's equivalent to. The final
answer includes those commands too, so you can reproduce the investigation
by hand next time. The goal is to make you faster at `kubectl` and better at
reading your own cluster, not to replace that skill with a black box.

## How it works

One loop, no agent framework:

```
question ──▶ model picks a read-only tool ──▶ kubewhy calls the k8s API
   ▲                                                      │
   │                                                       ▼
   └──────────── result fed back to the model ◀── narrated to you (teaching stream)
                         │
                         ▼ (once confident)
                  final answer + evidence + kubectl commands to verify
```

No CRDs, no operator to install, no vector memory, no multi-agent
delegation -- a CLI, your kubeconfig, and a handful of typed tool functions
(`get_resource`, `describe_pod`, `get_logs`, `get_events`, `top_pods`).

kubewhy talks to models through [OpenRouter](https://openrouter.ai) rather
than one provider directly, so the model is a runtime choice, not something
baked into the code -- swap in whatever's best or cheapest that week.

## Install

kubewhy ships as a single Go binary -- no runtime to install, no venv.

```bash
git clone https://github.com/didiberman/kubewhy
cd kubewhy
go build -o bin/kubewhy ./cmd/kubewhy
export OPENROUTER_API_KEY=sk-or-...
./bin/kubewhy "why is my-pod in namespace default not ready?"
```

> A Python prototype used to validate the idea before the Go port lives
> under `src/kubewhy` / `pyproject.toml` -- same agent loop and tools, kept
> around for reference. The Go version in `cmd/` and `internal/` is the one
> that's maintained going forward.

## Usage

```bash
kubewhy "why is checkout crashlooping in prod?"
kubewhy "is anything in the prod namespace unhealthy?"
kubewhy "why did the payments deployment lose availability an hour ago?"

# pick any model available on OpenRouter
kubewhy "..." --model openai/gpt-5
kubewhy "..." --model google/gemini-3-pro
```

Defaults to `anthropic/claude-sonnet-4.5`. Uses whatever context your local
`~/.kube/config` currently points at, same as `kubectl`.

## Running with enforced read-only RBAC

By default kubewhy runs with your own kubeconfig credentials, whatever those
allow. To make the read-only guarantee airtight -- so kubewhy *can't* write
even if your own user has cluster-admin -- bind it to a scoped
ServiceAccount instead:

```bash
kubectl apply -f deploy/readonly-clusterrole.yaml
# generate a kubeconfig scoped to the kubewhy ServiceAccount, then:
export KUBECONFIG=./kubewhy.kubeconfig
kubewhy "..."
```

This is the recommended way to run it against a cluster you don't fully
trust yourself around -- prod, a customer's cluster, or during an incident
when you want zero chance of an unintended write.

## Demo

[`deploy/kind-config.yaml`](deploy/kind-config.yaml) and
[`deploy/demo-broken-app.yaml`](deploy/demo-broken-app.yaml) reproduce the
exact scenario in the transcript above: a 1 control-plane / 3 worker kind
cluster with a `checkout` deployment that reliably OOMKills itself.

```bash
kind create cluster --name kubewhy-demo --config deploy/kind-config.yaml
kubectl apply -f deploy/demo-broken-app.yaml
export OPENROUTER_API_KEY=sk-or-...
kubewhy "why is the checkout deployment in the prod namespace crashlooping?"
```

## What it can investigate today (v0.1)

- Pod crashloops / not-ready state (`get`, `describe`, `logs`, `events`)
- Namespace-level event timelines
- Basic resource pressure via `kubectl top` (requires metrics-server)

## Roadmap

More investigation playbooks are next -- cost anomalies, NetworkPolicy /
ingress reachability, RBAC drift, "what changed since yesterday" (git-diff
against manifests). Contributions welcome; open an issue with the
investigation scenario you want covered.

## Contributing

Issues and PRs welcome. If you're adding a new read-only tool, keep it to a
single `get`/`list`/`watch`-shaped API call -- that constraint is the whole
point of the project.

## License

MIT
