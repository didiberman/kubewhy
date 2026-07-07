# kubewhy

Ask your cluster "why" in plain English. kubewhy investigates and explains
itself as it goes -- it's read-only by construction, so it's safe to point
at production.

```
$ kubewhy ask "why is checkout crashlooping in the prod namespace?"

kubewhy investigating: why is checkout crashlooping in the prod namespace?

  → checking get resource for 'prod'
    equivalent: kubectl get pod  -n prod
  → checking describe pod for 'checkout-7d9f8-abcde'
    equivalent: kubectl describe pod checkout-7d9f8-abcde -n prod
  → checking get logs for 'checkout-7d9f8-abcde'
    equivalent: kubectl logs checkout-7d9f8-abcde -n prod --previous

Answer
Root cause: OOMKilled. The container's memory limit (256Mi) is lower than
its steady-state usage (~310Mi per the last 3 restarts)...
```

## Why read-only

kagent and similar frameworks let agents *act* on a cluster -- deploy, patch,
scale. kubewhy does the opposite on purpose: it only investigates. That's not
a prompt instruction the model could ignore -- it's enforced by RBAC. Bind
kubewhy to the ServiceAccount in [`deploy/readonly-clusterrole.yaml`](deploy/readonly-clusterrole.yaml),
which only grants `get`/`list`/`watch`, and the Kubernetes API server itself
rejects anything else, no matter what the model tries to call.

This is what makes it safe to run against a live incident: no write access
means no blast radius, so there's nothing to approve before you use it.

## Why it teaches you

Every tool call kubewhy makes is narrated before it runs -- what it's about
to check, why, and the exact `kubectl` command it's equivalent to. The final
answer includes those commands too, so you can reproduce the investigation
by hand next time. The goal is to make you faster at `kubectl`, not to
replace it.

## Install

```bash
git clone https://github.com/didiberman/kubewhy
cd kubewhy
pip install -e .
export OPENROUTER_API_KEY=sk-or-...
kubewhy ask "why is my-pod in namespace default not ready?"
```

kubewhy talks to models through [OpenRouter](https://openrouter.ai) rather
than one provider directly, so the model is a runtime choice, not something
baked into the code:

```bash
kubewhy ask "..." --model openai/gpt-5
kubewhy ask "..." --model google/gemini-3-pro
```

Defaults to `anthropic/claude-sonnet-4.5`.

By default kubewhy uses whatever context your local `~/.kube/config` points
at. To run it with the enforced read-only RBAC role instead of your own
credentials:

```bash
kubectl apply -f deploy/readonly-clusterrole.yaml
# generate a kubeconfig scoped to the kubewhy ServiceAccount, then:
export KUBECONFIG=./kubewhy.kubeconfig
kubewhy ask "..."
```

## What it can investigate today (v0.1)

- Pod crashloops / not-ready state (`get`, `describe`, `logs`, `events`)
- Namespace-level event timelines
- Basic resource pressure via `kubectl top` (requires metrics-server)

More investigation playbooks (cost, network policy, RBAC drift, "what
changed since yesterday") are next -- contributions welcome.

## How it works

One loop: the model picks a read-only tool, kubewhy runs it against the
Kubernetes API, the result goes back to the model, repeat until it's
confident enough to answer. No agent framework, no CRDs, no operator to
install -- just a CLI and your kubeconfig.

## License

MIT
