"""The agent loop: send the question to Claude, execute whichever read-only
tool it asks for, feed the result back, repeat until it answers in plain text.

This is deliberately not a framework. It's one loop. The "teaching stream" --
printing what we're about to check and why, before we check it -- is what
turns a black-box answer into something a human can learn from and verify.
"""
from __future__ import annotations

import json

import anthropic
from rich.console import Console

from kubewhy import tools

console = Console()

MODEL = "claude-sonnet-5"

SYSTEM_PROMPT = """You are kubewhy, a read-only Kubernetes investigator.

You can only observe the cluster (get, describe, logs, events, metrics) -- you
have no ability to change anything, by design. Your job is to investigate the
user's question step by step, using the tools available, and arrive at a
root-cause hypothesis backed by concrete evidence (specific events, log lines,
statuses -- not guesses).

Before each tool call, you will be asked to explain in one short sentence what
you're about to check and why. Keep investigating until you have enough
evidence to give a confident answer, then stop calling tools and give your
final answer as plain text. Your final answer must include:
1. The root cause (or your best hypothesis, clearly labeled as such if unsure)
2. The evidence that supports it
3. The equivalent kubectl commands a human could run to verify it themselves
"""

TOOL_SCHEMAS = [
    {
        "name": "get_resource",
        "description": "Get or list a Kubernetes resource (pod, deployment, replicaset, service, node).",
        "input_schema": {
            "type": "object",
            "properties": {
                "kind": {"type": "string"},
                "namespace": {"type": "string"},
                "name": {"type": "string"},
                "label_selector": {"type": "string"},
            },
            "required": ["kind", "namespace"],
        },
    },
    {
        "name": "describe_pod",
        "description": "Get a pod's full spec/status plus the events involving it (read-only equivalent of `kubectl describe pod`).",
        "input_schema": {
            "type": "object",
            "properties": {"namespace": {"type": "string"}, "name": {"type": "string"}},
            "required": ["namespace", "name"],
        },
    },
    {
        "name": "get_logs",
        "description": "Read a pod's container logs.",
        "input_schema": {
            "type": "object",
            "properties": {
                "namespace": {"type": "string"},
                "pod": {"type": "string"},
                "container": {"type": "string"},
                "previous": {"type": "boolean", "description": "Read the previous (crashed) container's logs"},
                "tail_lines": {"type": "integer"},
            },
            "required": ["namespace", "pod"],
        },
    },
    {
        "name": "get_events",
        "description": "List events in a namespace, optionally filtered to one involved object.",
        "input_schema": {
            "type": "object",
            "properties": {"namespace": {"type": "string"}, "involved_object": {"type": "string"}},
            "required": ["namespace"],
        },
    },
    {
        "name": "top_pods",
        "description": "Get CPU/memory usage for pods in a namespace (requires metrics-server).",
        "input_schema": {
            "type": "object",
            "properties": {"namespace": {"type": "string"}},
            "required": ["namespace"],
        },
    },
]

TOOL_FUNCS = {
    "get_resource": tools.get_resource,
    "describe_pod": tools.describe_pod,
    "get_logs": tools.get_logs,
    "get_events": tools.get_events,
    "top_pods": tools.top_pods,
}


def _explain(tool_name: str, tool_input: dict) -> None:
    ns = tool_input.get("namespace", "")
    target = tool_input.get("name") or tool_input.get("pod") or tool_input.get("involved_object") or ""
    kubectl_equivalents = {
        "get_resource": f"kubectl get {tool_input.get('kind','')} {target} -n {ns}",
        "describe_pod": f"kubectl describe pod {target} -n {ns}",
        "get_logs": f"kubectl logs {target} -n {ns}" + (" --previous" if tool_input.get("previous") else ""),
        "get_events": f"kubectl get events -n {ns}" + (f" --field-selector involvedObject.name={target}" if target else ""),
        "top_pods": f"kubectl top pods -n {ns}",
    }
    console.print(f"[dim]  → checking {tool_name.replace('_',' ')} for {target or ns!r}[/dim]")
    console.print(f"[dim]    equivalent: {kubectl_equivalents.get(tool_name, tool_name)}[/dim]")


def run_tool(tool_name: str, tool_input: dict):
    _explain(tool_name, tool_input)
    func = TOOL_FUNCS[tool_name]
    return func(**tool_input)


def investigate(question: str, api_key: str | None = None) -> str:
    tools.load_kube_client()
    client_ = anthropic.Anthropic(api_key=api_key)

    messages = [{"role": "user", "content": question}]
    console.print(f"[bold cyan]kubewhy[/bold cyan] investigating: [italic]{question}[/italic]\n")

    while True:
        response = client_.messages.create(
            model=MODEL,
            max_tokens=2048,
            system=SYSTEM_PROMPT,
            tools=TOOL_SCHEMAS,
            messages=messages,
        )

        if response.stop_reason != "tool_use":
            final_text = "".join(b.text for b in response.content if b.type == "text")
            console.print("\n[bold green]Answer[/bold green]")
            console.print(final_text)
            return final_text

        messages.append({"role": "assistant", "content": response.content})
        tool_results = []
        for block in response.content:
            if block.type != "tool_use":
                continue
            result = run_tool(block.name, block.input)
            tool_results.append(
                {
                    "type": "tool_result",
                    "tool_use_id": block.id,
                    "content": json.dumps(result, default=str)[:8000],
                }
            )
        messages.append({"role": "user", "content": tool_results})
