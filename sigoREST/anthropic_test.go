package main

import (
	"encoding/json"
	"testing"

	"sigorest/sigoengine"
)

func TestAnthropicRequestToInternal_PlainText(t *testing.T) {
	req := &AnthropicRequest{
		Model:     "ci-claude-opus-5",
		MaxTokens: 100,
		System:    json.RawMessage(`"You are a helpful assistant."`),
		Messages: []AnthropicMessage{
			{Role: "user", Content: json.RawMessage(`"Hallo"`)},
		},
	}

	messages, tools, toolChoice, err := anthropicRequestToInternal(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("expected 2 messages (system+user), got %d: %+v", len(messages), messages)
	}
	if messages[0]["role"] != "system" || messages[0]["content"] != "You are a helpful assistant." {
		t.Fatalf("unexpected system message: %+v", messages[0])
	}
	if messages[1]["role"] != "user" || messages[1]["content"] != "Hallo" {
		t.Fatalf("unexpected user message: %+v", messages[1])
	}
	if tools != nil || toolChoice != nil {
		t.Fatalf("expected no tools/toolChoice, got %+v / %+v", tools, toolChoice)
	}
}

func TestAnthropicRequestToInternal_ToolsAndChoice(t *testing.T) {
	req := &AnthropicRequest{
		Model:     "ci-claude-opus-5",
		MaxTokens: 100,
		Messages: []AnthropicMessage{
			{Role: "user", Content: json.RawMessage(`"Lies die Datei x.txt"`)},
		},
		Tools: []AnthropicTool{
			{Name: "read_file", Description: "Liest eine Datei", InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)},
		},
		ToolChoice: json.RawMessage(`{"type":"any"}`),
	}

	_, tools, toolChoice, err := anthropicRequestToInternal(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	fn := tools[0]["function"].(map[string]interface{})
	if fn["name"] != "read_file" {
		t.Fatalf("unexpected tool: %+v", tools[0])
	}
	if toolChoice != "required" {
		t.Fatalf("expected tool_choice=required, got %+v", toolChoice)
	}
}

func TestAnthropicRequestToInternal_AssistantToolUse(t *testing.T) {
	req := &AnthropicRequest{
		Model:     "ci-claude-opus-5",
		MaxTokens: 100,
		Messages: []AnthropicMessage{
			{Role: "user", Content: json.RawMessage(`"Lies x.txt"`)},
			{Role: "assistant", Content: json.RawMessage(`[
				{"type":"text","text":"Ich schaue nach."},
				{"type":"tool_use","id":"toolu_01","name":"read_file","input":{"path":"x.txt"}}
			]`)},
			{Role: "user", Content: json.RawMessage(`[
				{"type":"tool_result","tool_use_id":"toolu_01","content":"Dateiinhalt hier"}
			]`)},
		},
	}

	messages, _, _, err := anthropicRequestToInternal(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(messages) != 3 {
		t.Fatalf("expected 3 messages (user, assistant+tool_call, tool-result), got %d: %+v", len(messages), messages)
	}
	assistantMsg := messages[1]
	if assistantMsg["role"] != "assistant" || assistantMsg["content"] != "Ich schaue nach." {
		t.Fatalf("unexpected assistant message: %+v", assistantMsg)
	}
	toolCalls, ok := assistantMsg["tool_calls"].([]map[string]interface{})
	if !ok || len(toolCalls) != 1 {
		t.Fatalf("expected 1 tool_call on assistant message, got %+v", assistantMsg["tool_calls"])
	}
	toolMsg := messages[2]
	if toolMsg["role"] != "tool" || toolMsg["tool_call_id"] != "toolu_01" || toolMsg["content"] != "Dateiinhalt hier" {
		t.Fatalf("unexpected tool-result message: %+v", toolMsg)
	}
}

func TestFinishReasonToStopReason(t *testing.T) {
	cases := []struct {
		finishReason string
		hasToolCalls bool
		want         string
	}{
		{"stop", false, "end_turn"},
		{"length", false, "max_tokens"},
		{"tool_calls", false, "tool_use"},
		{"stop", true, "tool_use"},
		{"", false, "end_turn"},
	}
	for _, c := range cases {
		got := finishReasonToStopReason(c.finishReason, c.hasToolCalls)
		if got != c.want {
			t.Errorf("finishReasonToStopReason(%q, %v) = %q, want %q", c.finishReason, c.hasToolCalls, got, c.want)
		}
	}
}

func TestInternalToAnthropicResponse_TextOnly(t *testing.T) {
	usage := &sigoengine.UsageData{InputTokens: 10, OutputTokens: 5, TotalTokens: 15}
	resp := internalToAnthropicResponse("ci-claude-opus-5", "Hallo zurück", nil, usage, "stop")

	if len(resp.Content) != 1 || resp.Content[0].Type != "text" || resp.Content[0].Text != "Hallo zurück" {
		t.Fatalf("unexpected content: %+v", resp.Content)
	}
	if resp.StopReason != "end_turn" {
		t.Fatalf("expected end_turn, got %s", resp.StopReason)
	}
	if resp.Usage.InputTokens != 10 || resp.Usage.OutputTokens != 5 {
		t.Fatalf("unexpected usage: %+v", resp.Usage)
	}
}

func TestInternalToAnthropicResponse_ToolUse(t *testing.T) {
	toolCalls := []sigoengine.ToolCall{
		{ID: "call_1", Type: "function", Function: sigoengine.ToolCallFunction{Name: "read_file", Arguments: `{"path":"x.txt"}`}},
	}
	resp := internalToAnthropicResponse("ci-claude-opus-5", "", toolCalls, nil, "tool_calls")

	if len(resp.Content) != 1 || resp.Content[0].Type != "tool_use" || resp.Content[0].Name != "read_file" {
		t.Fatalf("unexpected content: %+v", resp.Content)
	}
	if resp.Content[0].ID != "call_1" {
		t.Fatalf("expected tool_use id=call_1, got %s", resp.Content[0].ID)
	}
	if string(resp.Content[0].Input) != `{"path":"x.txt"}` {
		t.Fatalf("unexpected input: %s", resp.Content[0].Input)
	}
	if resp.StopReason != "tool_use" {
		t.Fatalf("expected tool_use, got %s", resp.StopReason)
	}
}
