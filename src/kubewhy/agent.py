"""The agent loop: send the question to a model via OpenRouter, execute
whichever read-only tool it asks for, feed the result back, repeat until it
answers in plain text.

This is deliberately not a framework. It's one loop. The "teaching stream" --
printing what we're about to check and why, before we check it -- is what
turns a black-box answer into something a human can learn from and verify.

OpenRouter is used instead of calling one provider directly so the model is a
runtime choice (Claude, GPT, Gemini, open-weight models, whatever's best/
cheapest that week) rather than something baked into the code.
"""
from __future__ import annotations

import json

from openai import OpenAI
from rich.console import Console

from kubewhy import tools

console = Console()

OPENROUTER_BASE_URL = "https://openrouter.ai/api/v1"
DEFAULT_MODEL = "anthropic/claude-sonnet-4.5"

SYSTEM_PROMPT = """You are kubewhy, a read-only Kubernetes investigator.

You can only observe the cluster (get, describe, logs, events, metrics) -- you
have no ability to change anything, by design. Your job is to investigate the
user's question step by step, using the tools available, and arrive at a
root-cause hypothesis backed by concrete evidence (specific events, log lines,
statuses -- not guesses).

Keep investigating until you have enough evidence to give a confident answer,
then stop calling tools and give your final answer as plain text. Your final
answer must include:
1. The root cause (or your best hypothesis, clearly labeled as such if unsure)
2. The evidence that supports it
3. The equivalent kubectl commands a human could run to verify it themselves
"""

TOOLS = [
    {
        "type": "function",
        "function": {
            "name": "get_resource",
            "description": "Get or list a Kubernetes resource (pod, deployment, replicaset, service, node).",
            "parameters": {
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
    },
    {
        "type": "function",
        "function": {
            "name": "describe_pod",
            "description": "Get a pod's full spec/status plus the events involving it (read-only equivalent of `kubectl describe pod`).",
            "parameters": {
                "type": "object",
                "properties": {"namespace": {"type": "string"}, "name": {"type": "string"}},
                "required": ["namespace", "name"],
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "get_logs",
            "description": "Read a pod's container logs.",
            "parameters": {
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
    },
    {
        "type": "function",
        "function": {
            "name": "get_events",
            "description": "List events in a namespace, optionally filtered to one involved object.",
            "parameters": {
                "type": "object",
                "properties": {"namespace": {"type": "string"}, "involved_object": {"type": "string"}},
                "required": ["namespace"],
            },
        },
    },
    {
        "type": "function",
        "function": {
            "name": "top_pods",
            "description": "Get CPU/memory usage for pods in a namespace (requires metrics-server).",
            "parameters": {
                "type": "object",
                "properties": {"namespace": {"type": "string"}},
                "required": ["namespace"],
            },
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


def investigate(question: str, api_key: str, model: str = DEFAULT_MODEL) -> str:
    tools.load_kube_client()
    client_ = OpenAI(base_url=OPENROUTER_BASE_URL, api_key=api_key)

    messages = [
        {"role": "system", "content": SYSTEM_PROMPT},
        {"role": "user", "content": question},
    ]
    console.print(f"[bold cyan]kubewhy[/bold cyan] ({model}) investigating: [italic]{question}[/italic]\n")

    while True:
        response = client_.chat.completions.create(
            model=model,
            messages=messages,
            tools=TOOLS,
        )
        message = response.choices[0].message

        if not message.tool_calls:
            final_text = message.content or ""
            console.print("\n[bold green]Answer[/bold green]")
            console.print(final_text)
            return final_text

        messages.append(message.model_dump(exclude_none=True))
        for call in message.tool_calls:
            tool_input = json.loads(call.function.arguments or "{}")
            result = run_tool(call.function.name, tool_input)
            messages.append(
                {
                    "role": "tool",
                    "tool_call_id": call.id,
                    "content": json.dumps(result, default=str)[:8000],
                }
            )
