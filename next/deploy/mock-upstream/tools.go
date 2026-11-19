package main

import (
	"bytes"
	"encoding/json"
)

// Tool calls. A Messages or chat completions request that offers tools and
// contains the marker MOCK-TOOL-CALL anywhere in its body is answered with a
// call of the first offered tool, input {"query":"mock"}, until the request
// carries a tool result (an Anthropic tool_result block or an OpenAI "tool"
// message); then the answer is text starting with "TOOL-RESULT-SEEN".
// Streams send the call as the API streams it (input_json_delta /
// tool_calls argument deltas).

const toolCallMarker = "MOCK-TOOL-CALL"

// toolArguments is the call's input, sent in two stream fragments.
var toolArguments = [2]string{`{"query":`, `"mock"}`}

type toolTurn struct {
	name       string
	resultSeen bool
}

// toolTurnOf reports whether body asks for the tool behaviour, and which.
func toolTurnOf(body []byte) (toolTurn, bool) {
	if !bytes.Contains(body, []byte(toolCallMarker)) {
		return toolTurn{}, false
	}
	var req struct {
		Tools []struct {
			Name     string `json:"name"`
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if json.Unmarshal(body, &req) != nil || len(req.Tools) == 0 {
		return toolTurn{}, false
	}
	t := toolTurn{name: req.Tools[0].Name}
	if t.name == "" {
		t.name = req.Tools[0].Function.Name
	}
	for _, m := range req.Messages {
		if m.Role == "tool" || bytes.Contains(m.Content, []byte(`"tool_result"`)) {
			t.resultSeen = true
		}
	}
	return t, t.name != ""
}

func toolInput() string { return toolArguments[0] + toolArguments[1] }

// anthropicToolUse is the tool_use content block of a Messages answer.
func anthropicToolUse(id, name string, input any) map[string]any {
	return map[string]any{"type": "tool_use", "id": id, "name": name, "input": input}
}

// messageToolEvents streams a Messages answer calling the tool.
func messageToolEvents(send func(event string, data any) bool, model, msgID string, b behaviour, t toolTurn) {
	steps := []struct {
		event string
		data  map[string]any
	}{
		{"message_start", map[string]any{"type": "message_start", "message": map[string]any{
			"id": msgID, "type": "message", "role": "assistant", "model": model, "content": []any{},
			"stop_reason": nil, "stop_sequence": nil, "usage": usageJSON(b.usage, 1)}}},
		{"content_block_start", map[string]any{"type": "content_block_start", "index": 0,
			"content_block": anthropicToolUse("toolu_"+msgID, t.name, map[string]any{})}},
		{"content_block_delta", map[string]any{"type": "content_block_delta", "index": 0,
			"delta": map[string]any{"type": "input_json_delta", "partial_json": toolArguments[0]}}},
		{"content_block_delta", map[string]any{"type": "content_block_delta", "index": 0,
			"delta": map[string]any{"type": "input_json_delta", "partial_json": toolArguments[1]}}},
		{"content_block_stop", map[string]any{"type": "content_block_stop", "index": 0}},
		{"message_delta", map[string]any{"type": "message_delta",
			"delta": map[string]any{"stop_reason": "tool_use", "stop_sequence": nil},
			"usage": map[string]any{"output_tokens": b.usage.OutputTokens}}},
		{"message_stop", map[string]any{"type": "message_stop"}},
	}
	for _, s := range steps {
		if !send(s.event, s.data) {
			return
		}
	}
}

// chatToolCall is the tool_calls entry of a chat completion.
func chatToolCall(id, name, arguments string) map[string]any {
	return map[string]any{"id": id, "type": "function", "function": map[string]any{"name": name, "arguments": arguments}}
}
