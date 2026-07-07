<div align="center">

# kubewhy

### Ask your Kubernetes cluster "why" — in plain English.

[![build](https://github.com/didiberman/kubewhy/actions/workflows/build.yml/badge.svg)](https://github.com/didiberman/kubewhy/actions/workflows/build.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/didiberman/kubewhy)](https://goreportcard.com/report/github.com/didiberman/kubewhy)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

**It investigates like a senior engineer. It can't break anything, ever. And it shows its work.**

</div>

---

## Install

One line, no Python, no virtualenv, no Docker — a single compiled binary.

```bash
curl -sSL https://kubewhy.didibe.dev | bash
```

<details>
<summary>Other ways to install</summary>

**Via Go:**

```bash
go install github.com/didiberman/kubewhy/cmd/kubewhy@latest
```

**Download a prebuilt binary:** grab the archive for your OS/arch from the
[Releases page](https://github.com/didiberman/kubewhy/releases/latest).

**From source:**

```bash
git clone https://github.com/didiberman/kubewhy
cd kubewhy
go build -o bin/kubewhy ./cmd/kubewhy
```

</details>

Then set your model key and point it at a cluster — it uses whatever
context your `kubectl` currently points at:

```bash
export OPENROUTER_API_KEY=sk-or-...
kubewhy "why is my-pod in namespace default not ready?"
```

## Latest: `kubewhy watch` — a live dashboard that finds problems before you ask

This is the newest and most-used way to run kubewhy. Instead of asking one
question at a time, `kubewhy watch` polls your whole cluster continuously
and investigates anything broken automatically, in the background — no
question required:

```
kubewhy watch  ·  read-only  ·  ↑/↓ select  ·  enter expand  ·  q quit

BROKEN
▶ ✗ prod/checkout-7cf7c94d78-7lzxs  (OOMKilled, 17 restarts)
      Root cause: The pod is OOMKilled — it tries to use ~300Mi but the
      container's memory limit is 100Mi.  (enter to expand)

WARNING
  ! staging/worker-9f8c  (2 restarts)
  ! prod/checkout-canary  (no resource requests set (breaks HPA / cluster-autoscaler sizing))

✓ 14 pod(s) healthy
```

The one-line summary is just a preview — broken pods are navigable with
`↑`/`↓`, and `enter` expands the selected one into the full answer
(complete evidence, reasoning, and verification commands), so nothing
gets cut off. Once expanded, `f` opens a follow-up question about that
specific pod — the model keeps everything it already investigated, so a
follow-up like "what memory limit should I set instead?" doesn't restart
the investigation from scratch.

```bash
kubewhy watch                          # all namespaces
kubewhy watch --namespace prod         # just one
kubewhy watch --interval 10s           # poll less often
```

**How it stays cheap:** a plain `get pod` poll (no model calls at all)
classifies every pod as healthy / warning / broken every few seconds. Only
pods that turn broken trigger the actual LLM investigation loop — so
you're not burning a model call per pod per second, only on things
genuinely worth looking at.

**What it catches beyond crashes:** the cheap check also flags things that
never crash but quietly break autoscaling — like a pod with no CPU/memory
requests set, which means the HPA has nothing to compute a percentage
against and Cluster Autoscaler / Karpenter can't size a node for it. Those
show as `WARNING` since nothing is actively failing, just silently
misconfigured.

## The one-shot version: just ask it something

Before `watch` mode existed, this was the whole tool, and it still works
standalone for a one-off question:

```
$ kubewhy "why is checkout crashlooping in the prod namespace?"

kubewhy investigating: why is checkout crashlooping in the prod namespace?

  → checking events for "prod"                    (like: kubectl get events -n prod)
  → checking checkout's pod details                (like: kubectl describe pod ...)
  → checking its crash logs                        (like: kubectl logs ... --previous)

Answer
Root cause: OOMKilled. The app tries to use 300Mi of memory, but the pod's
limit is only 100Mi — so Kubernetes kills it every time it starts.

Fix: raise the memory limit to 300Mi or higher.
Verify it yourself: kubectl describe pod -n prod -l app=checkout
```

Both transcripts above are real output from this repo's own demo cluster
(see [Demo](#see-it-catch-a-real-bug) below). The answer renders as
colorized, formatted markdown (headers, bold text, highlighted code) in
your terminal rather than showing raw `**`/`` ``` `` syntax as literal
text.

After the first answer, kubewhy keeps the conversation open and asks if
you want to follow up — a follow-up like "what should I set the memory
limit to?" reuses everything already gathered instead of starting a new
investigation from scratch.

## Two ideas make this different from a typical AI tool

<table>
<tr>
<td width="50%" valign="top">

### 🔒 It physically cannot break anything

Most AI agents for infrastructure can also *change* things — deploy, scale,
delete. That's powerful, but it means every answer comes with "what if it's
wrong and acts on it?"

kubewhy skips that risk entirely. Think of it as an extremely
capable intern who's allowed to look at absolutely anything in the
building, but has **no hands** — they can open any door, read any file,
but cannot pick anything up.

Concretely: kubewhy connects to Kubernetes using a permission set that
only allows *look, don't touch* (`get`/`list`/`watch` — nothing else).
This isn't a promise the AI makes you — it's enforced by Kubernetes
itself. Even if the model completely lost its mind and tried to delete
your production database, Kubernetes would simply refuse, the same way a
locked door doesn't care how politely (or rudely) you ask.

That's why it's safe to point at production, mid-incident, without asking
anyone for permission first.

</td>
<td width="50%" valign="top">

### 🎓 It shows its work, like a good teacher

Most AI answers arrive as a finished paragraph — you either trust it or
you don't, and you learn nothing about *how* it got there.

kubewhy instead narrates itself in real time: *"checking crash logs
because the pod just restarted"* — followed by the plain old `kubectl`
command that does the same thing. The final answer includes those
commands too, so you could've done it yourself by hand.

The goal isn't to make `kubectl` obsolete. It's to make you faster at
it — every answer doubles as a small lesson.

</td>
</tr>
</table>

## How it works, in one picture

```mermaid
flowchart LR
    Q["🗣️ Your question\n(plain English)"] --> M["🤖 AI model\npicks what to check next"]
    M --> T["🔍 Read-only check\nget · describe · logs · events"]
    T --> K[("☸️ Kubernetes API\n(look-only permissions)")]
    K --> M
    M -->|confident enough| A["✅ Answer\nroot cause + evidence + kubectl commands"]
```

It's one small loop, repeated until the model has enough evidence to give
a confident answer. No installed operator, no custom resources, no
multi-agent framework — just a CLI, your kubeconfig, and a handful of
read-only checks. `watch` mode wraps this same loop with a cheap poller
that decides *when* to trigger it.

Models come from [OpenRouter](https://openrouter.ai), so the brain behind
kubewhy is a runtime choice (`--model openai/gpt-5`, `--model
anthropic/claude-sonnet-4.5`, whatever you like) — never hard-coded.

## Want the ironclad version?

By default kubewhy uses your own kubeconfig, whatever permissions that
carries. To make the "it literally cannot write" guarantee airtight —
useful for prod, a customer's cluster, or anywhere you want zero trust in
the AI's judgment — bind it to a locked-down account instead:

```bash
kubectl apply -f deploy/readonly-clusterrole.yaml
# generate a kubeconfig scoped to that account, then:
export KUBECONFIG=./kubewhy.kubeconfig
kubewhy "..."
```

## See it catch a real bug

This repo includes a script that reproduces a genuine failure so you can
watch kubewhy diagnose it, not take our word for it:

```bash
kind create cluster --name kubewhy-demo --config deploy/kind-config.yaml
kubectl apply -f deploy/demo-broken-app.yaml   # deliberately OOM-kills itself
kubewhy "why is the checkout deployment in the prod namespace crashlooping?"
# or: kubewhy watch
```

## What it can diagnose today (v0.1)

- Crashing / not-ready pods (`get`, `describe`, `logs`, `events`)
- Namespace event timelines ("what's been happening here?")
- Basic CPU/memory pressure (via `kubectl top`, needs metrics-server)
- Pods with no resource requests set — silently breaks HPA and
  Cluster Autoscaler / Karpenter node sizing, caught automatically by
  `kubewhy watch`

More playbooks — cost spikes, network policy issues, "what changed since
yesterday" — are next. Contributions welcome; open an issue with the
scenario you want it to handle.

## FAQ

**Isn't this just kagent?** No — [kagent](https://github.com/kagent-dev/kagent)
is a framework for building agents that *act* on your cluster (deploy,
patch, scale). kubewhy is narrower on purpose: it only ever looks, never
touches, and ships as one binary instead of a platform to operate.

**Do I need to trust the AI model?** Only with information, never with
access. Worst case it gives you a wrong diagnosis — it can't make the
problem worse, because it has no way to change anything.

**Which models work?** Anything available on OpenRouter that supports
tool calling — Claude, GPT, Gemini, and most open-weight models.

## Contributing

Adding a new investigation tool? Keep it to a single
`get`/`list`/`watch`-shaped API call — that constraint is the entire
point of the project.

## License

MIT
