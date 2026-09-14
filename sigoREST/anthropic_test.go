package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestAnthropicRequestToInternal_ToolUseWithoutInput(t *testing.T) {
	// Regression: Client lässt "input" für ein parameterloses Tool weg ->
	// b.Input ist nil/leer. Provider erwarten trotzdem einen gültigen
	// JSON-Objekt-String ("{}"), nicht "".
	req := &AnthropicRequest{
		Model:     "ci-claude-opus-5",
		MaxTokens: 100,
		Messages: []AnthropicMessage{
			{Role: "assistant", Content: json.RawMessage(`[
				{"type":"tool_use","id":"toolu_01","name":"list_files"}
			]`)},
		},
	}

	messages, _, _, err := anthropicRequestToInternal(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("expected 1 message, got %d: %+v", len(messages), messages)
	}
	toolCalls, ok := messages[0]["tool_calls"].([]map[string]interface{})
	if !ok || len(toolCalls) != 1 {
		t.Fatalf("expected 1 tool_call, got %+v", messages[0]["tool_calls"])
	}
	fn, ok := toolCalls[0]["function"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected function map, got %+v", toolCalls[0]["function"])
	}
	if fn["arguments"] != "{}" {
		t.Fatalf("expected arguments '{}', got %q", fn["arguments"])
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

func TestInternalToAnthropicResponse_EmptyContentMarshalsAsEmptyArray(t *testing.T) {
	// Regression: leerer Text + keine Tool-Calls (z.B. Content-Filter-Hit)
	// darf nicht "content": null marshaln — Anthropic-SDK-Clients iterieren
	// response.content und stürzen bei null ab.
	resp := internalToAnthropicResponse("ci-claude-opus-5", "", nil, nil, "stop")

	if resp.Content == nil {
		t.Fatalf("expected non-nil Content slice, got nil")
	}
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("unexpected marshal error: %v", err)
	}
	if strings.Contains(string(b), `"content":null`) {
		t.Fatalf("content marshaled as null, want []: %s", b)
	}
	if !strings.Contains(string(b), `"content":[]`) {
		t.Fatalf("expected \"content\":[] in output, got: %s", b)
	}
}

func TestHandleMessages_ModelNotFound(t *testing.T) {
	srv, _ := newTestServer(t)
	body := `{"model":"does-not-exist","max_tokens":100,"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	rr := httptest.NewRecorder()

	srv.handleMessages(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rr.Code, rr.Body.String())
	}

	// Regression: /v1/messages Fehler müssen im Anthropic-Wire-Format
	// kommen ({"type":"error","error":{"type","message"}}), nicht im
	// OpenAI-Shape ({"error":{"message","type","code"}}) von writeError.
	var envelope struct {
		Type  string `json:"type"`
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if envelope.Type != "error" {
		t.Fatalf("expected top-level type \"error\", got %q (body: %s)", envelope.Type, rr.Body.String())
	}
	if envelope.Error.Type != "not_found_error" {
		t.Fatalf("expected error.type \"not_found_error\", got %q (body: %s)", envelope.Error.Type, rr.Body.String())
	}
	if envelope.Error.Message == "" {
		t.Fatalf("expected non-empty error.message, body: %s", rr.Body.String())
	}
}

func TestWriteAnthropicError_Shape(t *testing.T) {
	rr := httptest.NewRecorder()
	writeAnthropicError(rr, "rate_limit_error", "boom", http.StatusTooManyRequests)

	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rr.Code)
	}
	var envelope map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if envelope["type"] != "error" {
		t.Fatalf("expected top-level type \"error\", got %+v", envelope["type"])
	}
	errObj, ok := envelope["error"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected error object, got %+v", envelope["error"])
	}
	if errObj["type"] != "rate_limit_error" || errObj["message"] != "boom" {
		t.Fatalf("unexpected error object: %+v", errObj)
	}
	// OpenAI-Shape-Felder ("code") dürfen nicht auftauchen.
	if _, hasCode := errObj["code"]; hasCode {
		t.Fatalf("did not expect OpenAI-shaped \"code\" field, got: %s", rr.Body.String())
	}
}

func TestAnthropicErrorType_Mapping(t *testing.T) {
	cases := []struct {
		internal string
		want     string
	}{
		{sigoengine.ErrRateLimit, "rate_limit_error"},
		{sigoengine.ErrAuthFailed, "authentication_error"},
		{sigoengine.ErrClientError, "invalid_request_error"},
		{sigoengine.ErrConfigNotFound, "not_found_error"},
		{sigoengine.ErrCircuitOpen, "overloaded_error"},
		{sigoengine.ErrTimeout, "api_error"},
		{sigoengine.ErrServerError, "api_error"},
		{"something_unmapped", "api_error"},
	}
	for _, c := range cases {
		if got := anthropicErrorType(c.internal); got != c.want {
			t.Errorf("anthropicErrorType(%q) = %q, want %q", c.internal, got, c.want)
		}
	}
}

func TestHandleMessages_StreamingReachesProviderCall(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.models["claude-h"] = ModelInfo{ID: "claude-h", Endpoint: "http://127.0.0.1:1/v1/chat/completions"}

	body := `{"model":"claude-h","max_tokens":100,"stream":true,"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	rr := httptest.NewRecorder()

	srv.handleMessages(rr, req)

	// Kein echter Provider erreichbar -> Verbindung schlägt fehl, aber NICHT
	// mehr mit 501 (das hätte Task 7's Platzhalter noch geliefert).
	if rr.Code == http.StatusNotImplemented {
		t.Fatalf("expected streaming to be wired up, still got 501 not-implemented")
	}
}

func TestStreamAnthropicResponse_TextOnly(t *testing.T) {
	srv, _ := newTestServer(t)

	chunk := func(content, finishReason string) string {
		choice := map[string]interface{}{"delta": map[string]interface{}{"content": content}}
		if finishReason != "" {
			choice["finish_reason"] = finishReason
		}
		payload := map[string]interface{}{"choices": []interface{}{choice}}
		b, _ := json.Marshal(payload)
		return "data: " + string(b) + "\n\n"
	}

	sse := chunk("Hallo", "") + chunk(" Welt", "stop") + "data: [DONE]\n\n"
	stream := io.NopCloser(strings.NewReader(sse))
	rr := httptest.NewRecorder()

	text, err := srv.streamAnthropicResponse(rr, stream, "ci-claude-opus-5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "Hallo Welt" {
		t.Fatalf("expected accumulated text 'Hallo Welt', got %q", text)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"event: message_start",
		"event: content_block_start",
		`"type":"text_delta"`,
		`"text":"Hallo"`,
		"event: content_block_stop",
		"event: message_delta",
		`"stop_reason":"end_turn"`,
		"event: message_stop",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected body to contain %q, got:\n%s", want, body)
		}
	}
}

func TestStreamAnthropicResponse_ToolCall(t *testing.T) {
	srv, _ := newTestServer(t)

	chunk := func(delta map[string]interface{}, finishReason string) string {
		choice := map[string]interface{}{"delta": delta}
		if finishReason != "" {
			choice["finish_reason"] = finishReason
		}
		payload := map[string]interface{}{"choices": []interface{}{choice}}
		b, _ := json.Marshal(payload)
		return "data: " + string(b) + "\n\n"
	}

	sse := chunk(map[string]interface{}{
		"tool_calls": []interface{}{
			map[string]interface{}{
				"index": 0, "id": "call_1", "type": "function",
				"function": map[string]interface{}{"name": "read_file", "arguments": ""},
			},
		},
	}, "")
	sse += chunk(map[string]interface{}{
		"tool_calls": []interface{}{
			map[string]interface{}{"index": 0, "function": map[string]interface{}{"arguments": `{"path":`}},
		},
	}, "")
	sse += chunk(map[string]interface{}{
		"tool_calls": []interface{}{
			map[string]interface{}{"index": 0, "function": map[string]interface{}{"arguments": `"x.txt"}`}},
		},
	}, "tool_calls")
	sse += "data: [DONE]\n\n"

	stream := io.NopCloser(strings.NewReader(sse))
	rr := httptest.NewRecorder()

	_, err := srv.streamAnthropicResponse(rr, stream, "ci-claude-opus-5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`"type":"tool_use"`,
		`"id":"call_1"`,
		`"name":"read_file"`,
		`path`,
		`"stop_reason":"tool_use"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected body to contain %q, got:\n%s", want, body)
		}
	}
}
