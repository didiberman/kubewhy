// Package agent implements the tool-calling loop: send the question to a
// model via OpenRouter, execute whichever read-only tool it asks for, feed
// the result back, repeat until it answers in plain text.
//
// This is deliberately not a framework. It's one loop. The "teaching
// stream" -- printing what we're about to check and why, before we check
// it -- is what turns a black-box answer into something a human can learn
// from and verify.
package agent

import (
	"context"
	"encoding/json"
	"fmt"

	openai "github.com/sashabaranov/go-openai"

	"github.com/didiberman/kubewhy/internal/tools"
)

const (
	OpenRouterBaseURL = "https://openrouter.ai/api/v1"
	DefaultModel      = "anthropic/claude-sonnet-4.5"
)

const systemPrompt = `You are kubewhy, a read-only Kubernetes investigator.

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
`

func strPtr(s string) *string { return &s }

func toolDefs() []openai.Tool {
	def := func(name, desc string, params map[string]any) openai.Tool {
		return openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        name,
				Description: desc,
				Parameters:  params,
			},
		}
	}
	return []openai.Tool{
		def("get_resource", "Get or list a Kubernetes resource (pod, deployment, replicaset, service, node).", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"kind":           map[string]any{"type": "string"},
				"namespace":      map[string]any{"type": "string"},
				"name":           map[string]any{"type": "string"},
				"label_selector": map[string]any{"type": "string"},
			},
			"required": []string{"kind", "namespace"},
		}),
		def("describe_pod", "Get a pod's full spec/status plus the events involving it (read-only equivalent of `kubectl describe pod`).", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"namespace": map[string]any{"type": "string"},
				"name":      map[string]any{"type": "string"},
			},
			"required": []string{"namespace", "name"},
		}),
		def("get_logs", "Read a pod's container logs.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"namespace":  map[string]any{"type": "string"},
				"pod":        map[string]any{"type": "string"},
				"container":  map[string]any{"type": "string"},
				"previous":   map[string]any{"type": "boolean", "description": "Read the previous (crashed) container's logs"},
				"tail_lines": map[string]any{"type": "integer"},
			},
			"required": []string{"namespace", "pod"},
		}),
		def("get_events", "List events in a namespace, optionally filtered to one involved object.", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"namespace":       map[string]any{"type": "string"},
				"involved_object": map[string]any{"type": "string"},
			},
			"required": []string{"namespace"},
		}),
		def("top_pods", "Get CPU/memory usage for pods in a namespace (requires metrics-server).", map[string]any{
			"type": "object",
			"properties": map[string]any{
				"namespace": map[string]any{"type": "string"},
			},
			"required": []string{"namespace"},
		}),
	}
}

func explain(name string, input map[string]any) string {
	ns, _ := input["namespace"].(string)
	target, _ := input["name"].(string)
	if target == "" {
		target, _ = input["pod"].(string)
	}
	if target == "" {
		target, _ = input["involved_object"].(string)
	}
	var equivalent string
	switch name {
	case "get_resource":
		kind, _ := input["kind"].(string)
		equivalent = fmt.Sprintf("kubectl get %s %s -n %s", kind, target, ns)
	case "describe_pod":
		equivalent = fmt.Sprintf("kubectl describe pod %s -n %s", target, ns)
	case "get_logs":
		equivalent = fmt.Sprintf("kubectl logs %s -n %s", target, ns)
		if prev, _ := input["previous"].(bool); prev {
			equivalent += " --previous"
		}
	case "get_events":
		equivalent = fmt.Sprintf("kubectl get events -n %s", ns)
		if target != "" {
			equivalent += fmt.Sprintf(" --field-selector involvedObject.name=%s", target)
		}
	case "top_pods":
		equivalent = fmt.Sprintf("kubectl top pods -n %s", ns)
	default:
		equivalent = name
	}
	shown := target
	if shown == "" {
		shown = ns
	}
	return fmt.Sprintf("  → checking %s for %q\n    equivalent: %s", name, shown, equivalent)
}

func runTool(ctx context.Context, c *tools.Client, name string, raw json.RawMessage) (any, error) {
	var asMap map[string]any
	if err := json.Unmarshal(raw, &asMap); err != nil {
		return nil, err
	}
	fmt.Println(explain(name, asMap))

	switch name {
	case "get_resource":
		var in tools.GetResourceInput
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, err
		}
		return c.GetResource(ctx, in)
	case "describe_pod":
		var in tools.DescribePodInput
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, err
		}
		return c.DescribePod(ctx, in)
	case "get_logs":
		var in tools.GetLogsInput
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, err
		}
		return c.GetLogs(ctx, in)
	case "get_events":
		var in tools.GetEventsInput
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, err
		}
		return c.GetEvents(ctx, in)
	case "top_pods":
		var in tools.TopPodsInput
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, err
		}
		return c.TopPods(ctx, in)
	default:
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
}

func Investigate(ctx context.Context, question, apiKey, model string) (string, error) {
	client, err := tools.LoadClient()
	if err != nil {
		return "", err
	}

	config := openai.DefaultConfig(apiKey)
	config.BaseURL = OpenRouterBaseURL
	oa := openai.NewClientWithConfig(config)

	messages := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: systemPrompt},
		{Role: openai.ChatMessageRoleUser, Content: question},
	}

	fmt.Printf("kubewhy (%s) investigating: %s\n\n", model, question)

	for {
		resp, err := oa.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
			Model:    model,
			Messages: messages,
			Tools:    toolDefs(),
		})
		if err != nil {
			return "", fmt.Errorf("openrouter request failed: %w", err)
		}

		msg := resp.Choices[0].Message
		if len(msg.ToolCalls) == 0 {
			fmt.Println("\nAnswer")
			fmt.Println(msg.Content)
			return msg.Content, nil
		}

		messages = append(messages, msg)
		for _, call := range msg.ToolCalls {
			result, err := runTool(ctx, client, call.Function.Name, json.RawMessage(call.Function.Arguments))
			var content string
			if err != nil {
				content = fmt.Sprintf(`{"error": %q}`, err.Error())
			} else {
				b, _ := json.Marshal(result)
				content = string(b)
				if len(content) > 8000 {
					content = content[:8000]
				}
			}
			messages = append(messages, openai.ChatCompletionMessage{
				Role:       openai.ChatMessageRoleTool,
				ToolCallID: call.ID,
				Content:    content,
			})
		}
	}
}
