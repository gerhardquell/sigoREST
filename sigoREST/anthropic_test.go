package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

func TestHandleMessages_MidStreamFailureDoesNotDoubleWriteOrGlueJSON(t *testing.T) {
	// Regression for Finding #3: once streamAnthropicResponse has written
	// headers and flushed at least one event, a later read error from the
	// upstream must not (a) retry the next channel (second stream preamble
	// on the same ResponseWriter) nor (b) fall through to the JSON error
	// writer on the already-open text/event-stream body.
	callCount := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("upstream ResponseWriter does not support flushing")
		}
		fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"delta":{"content":"Hallo"}}]}`)
		flusher.Flush()

		// Verbindung mitten im Stream abrupt kappen (kein "[DONE]", kein
		// sauberes EOF) -> Client bekommt einen echten Lesefehler
		// (unexpected EOF), nicht nur ein normales Stream-Ende.
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("upstream ResponseWriter does not support hijacking")
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			t.Fatalf("hijack failed: %v", err)
		}
		conn.Close()
	}))
	defer upstream.Close()

	srv, _ := newTestServer(t)
	// Zweiten aktiven Kanal für denselben Provider aktivieren, damit ohne
	// den Fix tatsächlich ein Failover-Versuch stattfinden würde.
	if err := srv.channelManager.Registry().SetActive("mammouth", "0", true); err != nil {
		t.Fatalf("failed to activate second channel: %v", err)
	}
	srv.models["claude-h"] = ModelInfo{ID: "claude-h", Endpoint: upstream.URL}

	body := `{"model":"claude-h","max_tokens":100,"stream":true,"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	rr := httptest.NewRecorder()

	srv.handleMessages(rr, req)

	if callCount != 1 {
		t.Fatalf("expected exactly 1 upstream call (no failover retry once the stream had started), got %d", callCount)
	}
	respBody := rr.Body.String()
	if n := strings.Count(respBody, "event: message_start"); n != 1 {
		t.Fatalf("expected exactly 1 message_start event, got %d in body:\n%s", n, respBody)
	}
	if strings.Contains(respBody, `"type":"error"`) {
		t.Fatalf("expected no JSON error glued onto the open SSE stream, got body:\n%s", respBody)
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

	text, _, err := srv.streamAnthropicResponse(rr, stream, "ci-claude-opus-5")
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

func TestStreamAnthropicResponse_UsageCapturedAndIncludesInputTokens(t *testing.T) {
	// Regression: der Provider liefert prompt_tokens/completion_tokens in
	// einem SSE-Chunk mit -> streamAnthropicResponse darf sie weder
	// verwerfen (Rückgabewert) noch im message_delta.usage-Event nur
	// output_tokens ausliefern.
	srv, _ := newTestServer(t)

	chunk := func(content, finishReason string, usage map[string]interface{}) string {
		choice := map[string]interface{}{"delta": map[string]interface{}{"content": content}}
		if finishReason != "" {
			choice["finish_reason"] = finishReason
		}
		payload := map[string]interface{}{"choices": []interface{}{choice}}
		if usage != nil {
			payload["usage"] = usage
		}
		b, _ := json.Marshal(payload)
		return "data: " + string(b) + "\n\n"
	}

	sse := chunk("Hallo", "", nil)
	sse += chunk("", "stop", map[string]interface{}{"prompt_tokens": 42, "completion_tokens": 7})
	sse += "data: [DONE]\n\n"

	stream := io.NopCloser(strings.NewReader(sse))
	rr := httptest.NewRecorder()

	_, usage, err := srv.streamAnthropicResponse(rr, stream, "ci-claude-opus-5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if usage == nil {
		t.Fatalf("expected non-nil usage to be returned")
	}
	if usage.InputTokens != 42 || usage.OutputTokens != 7 {
		t.Fatalf("unexpected usage: %+v", usage)
	}

	body := rr.Body.String()
	if !strings.Contains(body, `"input_tokens":42`) {
		t.Fatalf("expected message_delta.usage to contain input_tokens:42, got:\n%s", body)
	}
	if !strings.Contains(body, `"output_tokens":7`) {
		t.Fatalf("expected message_delta.usage to contain output_tokens:7, got:\n%s", body)
	}
}

// TestStreamAnthropicResponse_UsageIncludesCachedTokens: TODO 20261008 —
// der Bridge-Stream las nur prompt_tokens/completion_tokens, nie
// prompt_tokens_details.cached_tokens. Agent-Loops (Claude Code) mit ~70 %
// Cache-Anteil wurden dadurch voll zum Input-Preis gebucht.
func TestStreamAnthropicResponse_UsageIncludesCachedTokens(t *testing.T) {
	srv, _ := newTestServer(t)

	sse := `data: {"choices":[{"delta":{"content":"Hallo"}}]}` + "\n\n"
	sse += `data: {"choices":[],"usage":{"prompt_tokens":1000,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":800}}}` + "\n\n"
	sse += "data: [DONE]\n\n"

	_, usage, err := srv.streamAnthropicResponse(httptest.NewRecorder(), io.NopCloser(strings.NewReader(sse)), "ci-gpt-6-sol")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if usage == nil {
		t.Fatalf("expected non-nil usage")
	}
	if usage.InputTokens != 1000 || usage.OutputTokens != 5 || usage.CachedTokens != 800 {
		t.Fatalf("unexpected usage: %+v", usage)
	}
}

// TestStreamAnthropicResponse_ExtendsWriteDeadlinePerChunk: TODO-20261003-kosten.md
// Punkt 3 — derselbe 5-Minuten-WriteTimeout-Bug wie bei streamProviderResponse
// (main.go) betrifft auch die Anthropic-Bridge: jedes writeAnthropicSSEEvent
// muss das Write-Deadline erneuern, sonst bricht ein langer Claude-Code-Stream
// nach exakt 300s ab, egal wie regelmäßig Chunks kommen.
func TestStreamAnthropicResponse_ExtendsWriteDeadlinePerChunk(t *testing.T) {
	srv, _ := newTestServer(t)

	chunk := func(content string) string {
		payload := map[string]interface{}{"choices": []interface{}{
			map[string]interface{}{"delta": map[string]interface{}{"content": content}},
		}}
		b, _ := json.Marshal(payload)
		return "data: " + string(b) + "\n\n"
	}
	sse := chunk("A") + chunk("B") + chunk("C") + "data: [DONE]\n\n"

	rec := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	_, _, err := srv.streamAnthropicResponse(rec, io.NopCloser(strings.NewReader(sse)), "ci-claude-opus-5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// message_start + 3x content_block_* (start einmalig + delta pro Chunk) + content_block_stop + message_delta + message_stop
	if len(rec.deadlines) < 4 {
		t.Fatalf("erwartet mind. 4 SetWriteDeadline-Aufrufe (ein SSE-Event pro Flush), bekommen %d", len(rec.deadlines))
	}
	for i := 1; i < len(rec.deadlines); i++ {
		if rec.deadlines[i].Before(rec.deadlines[i-1]) {
			t.Fatalf("Deadline #%d liegt vor Deadline #%d — muss monoton erneuert werden", i, i-1)
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

	_, _, err := srv.streamAnthropicResponse(rr, stream, "ci-claude-opus-5")
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

func TestAnthropicRequest_ThinkingParsed(t *testing.T) {
	// Regression: thinking-Parameter muss geparst werden, damit er nicht
	// still verworfen wird und Thinking-fähige Clients (pi) wirksam sind.
	body := `{"model":"mam-cl46-s","max_tokens":100,
		"thinking":{"type":"enabled","budget_tokens":10240},
		"messages":[{"role":"user","content":"hi"}]}`
	var req AnthropicRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("unexpected decode error: %v", err)
	}
	if req.Thinking == nil || req.Thinking.Type != "enabled" || req.Thinking.BudgetTokens != 10240 {
		t.Fatalf("unexpected thinking: %+v", req.Thinking)
	}

	// Ohne thinking-Feld muss das Feld nil bleiben (kein Zero-Struct).
	var reqNoThinking AnthropicRequest
	if err := json.Unmarshal([]byte(`{"model":"m","max_tokens":1,"messages":[]}`), &reqNoThinking); err != nil {
		t.Fatalf("unexpected decode error: %v", err)
	}
	if reqNoThinking.Thinking != nil {
		t.Fatalf("expected nil thinking, got %+v", reqNoThinking.Thinking)
	}
}

func TestBudgetToEffort(t *testing.T) {
	cases := []struct {
		budget int
		want   string
	}{
		{0, "low"},     // Client ohne Budget
		{1024, "low"},  // pi minimal
		{4096, "low"},  // pi low
		{8191, "low"},
		{10240, "medium"}, // pi medium
		{16383, "medium"},
		{20480, "high"}, // pi high
		{65536, "high"}, // pi xhigh/max
	}
	for _, c := range cases {
		if got := budgetToEffort(c.budget); got != c.want {
			t.Errorf("budgetToEffort(%d) = %q, want %q", c.budget, got, c.want)
		}
	}
}

func TestStreamAnthropicResponse_Thinking(t *testing.T) {
	// delta.reasoning_content muss als thinking-Block vor dem text-Block
	// ausgeliefert werden (Anthropic erwartet thinking zuerst).
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

	sse := chunk(map[string]interface{}{"reasoning_content": "Ich denke "}, "")
	sse += chunk(map[string]interface{}{"reasoning_content": "nach."}, "")
	sse += chunk(map[string]interface{}{"content": "Hallo!"}, "")
	sse += chunk(map[string]interface{}{}, "stop")
	sse += "data: [DONE]\n\n"

	stream := io.NopCloser(strings.NewReader(sse))
	rr := httptest.NewRecorder()

	_, _, err := srv.streamAnthropicResponse(rr, stream, "mam-cl46-s")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`"type":"thinking"`,
		`"type":"thinking_delta"`,
		`"thinking":"Ich denke "`,
		`"thinking":"nach."`,
		`"type":"text_delta"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected body to contain %q, got:\n%s", want, body)
		}
	}
	// thinking-Block (index 0) muss vor dem text-Block (index 1) geöffnet werden.
	// (json.Marshal sortiert Map-Keys alphabetisch — deshalb Position der
	// Block-Start-Merkmale statt fester Key-Reihenfolge prüfen.)
	thinkingPos := strings.Index(body, `"thinking":""`)
	textPos := strings.Index(body, `"text":""`)
	if thinkingPos < 0 || textPos < 0 || thinkingPos > textPos {
		t.Fatalf("expected thinking block before text block, got:\n%s", body)
	}
	// Beide Blöcke müssen geschlossen werden.
	if n := strings.Count(body, "event: content_block_stop"); n != 2 {
		t.Fatalf("expected 2 content_block_stop events, got %d in:\n%s", n, body)
	}
}

func TestHandleMessages_ThinkingForwardedAsReasoningEffort(t *testing.T) {
	// Integration: thinking im /v1/messages-Request muss als
	// reasoning_effort beim OpenAI-kompatiblen Upstream ankommen.
	var gotBody map[string]interface{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &gotBody); err != nil {
			t.Errorf("invalid upstream request body: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}`)
		flusher.Flush()
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer upstream.Close()

	srv, _ := newTestServer(t)
	srv.models["claude-h"] = ModelInfo{ID: "claude-h", Endpoint: upstream.URL}

	body := `{"model":"claude-h","max_tokens":100,"stream":true,
		"thinking":{"type":"enabled","budget_tokens":20480},
		"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	rr := httptest.NewRecorder()

	srv.handleMessages(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if gotBody == nil {
		t.Fatal("upstream received no request")
	}
	if gotBody["reasoning_effort"] != "high" {
		t.Fatalf("expected reasoning_effort=high at upstream, got %v (body: %v)", gotBody["reasoning_effort"], gotBody)
	}
	if _, ok := gotBody["thinking"]; ok {
		t.Fatalf("did not expect thinking object at OpenAI-compatible upstream, got: %v", gotBody)
	}
}

// TestHandleMessages_RespectsHardStopBudget: TODO.md 20261008 — der
// Budget-Hard-Stop lief nur in handleChatCompletions. Claude Code (über
// /v1/messages) überschritt das Tageslimit deshalb ungebremst ($25 bei $10
// Limit). Bei überschrittenem Limit muss auch die Bridge mit HTTP 402 im
// Anthropic-Fehlerformat ablehnen, ohne den Provider anzufragen.
func TestHandleMessages_RespectsHardStopBudget(t *testing.T) {
	upstreamCalls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls++
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	srv, dir := newTestServer(t)
	costDB, err := sigoengine.OpenCostDB(dir)
	if err != nil {
		t.Fatalf("OpenCostDB: %v", err)
	}
	defer costDB.Close()
	srv.costDB = costDB
	if err := costDB.SetBudgetConfig(sigoengine.BudgetConfig{DailyLimitUSD: 5, HardStopEnabled: true}); err != nil {
		t.Fatalf("SetBudgetConfig: %v", err)
	}
	if err := costDB.RecordUsage(sigoengine.UsageEvent{Timestamp: time.Now(), Model: "claude-h", Provider: "mammouth", Channel: "mammouth-default", TotalCostUSD: 6}); err != nil {
		t.Fatalf("RecordUsage: %v", err)
	}
	srv.models["claude-h"] = ModelInfo{ID: "claude-h", Endpoint: upstream.URL}

	body := `{"model":"claude-h","max_tokens":100,"stream":true,"messages":[{"role":"user","content":"hi"}]}`
	rr := httptest.NewRecorder()
	srv.handleMessages(rr, httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body)))

	if rr.Code != http.StatusPaymentRequired {
		t.Fatalf("erwartet 402, bekommen %d: %s", rr.Code, rr.Body.String())
	}
	var env anthropicErrorEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil || env.Type != "error" || env.Error.Type != "billing_error" {
		t.Fatalf("erwartet Anthropic-Fehler billing_error, bekommen: %s", rr.Body.String())
	}
	if upstreamCalls != 0 {
		t.Fatalf("erwartet keinen Provider-Call bei Hard-Stop, bekommen %d", upstreamCalls)
	}
}

// TestHandleMessages_ProviderBudgetExceededNoRetryNoFailover: TODO.md
// 20261008 — Mammouth meldete ein erschöpftes User-Budget als HTTP 429.
// sigoREST hielt das für ein Rate-Limit: 4 Versuche mit Backoff pro Kanal,
// dann Failover über alle Kanäle (alle Keys = derselbe User, also sinnlos).
// Erwartet: genau ein Upstream-Call, 402 billing_error an den Client.
func TestHandleMessages_ProviderBudgetExceededNoRetryNoFailover(t *testing.T) {
	upstreamCalls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"error":{"message":"ExceededBudget: User=93085 over budget. Spend=21.67904175, Budget=20.81356348856591","type":"budget_exceeded","param":null,"code":"429"}}`)
	}))
	defer upstream.Close()

	srv, _ := newTestServer(t)
	if err := srv.channelManager.Registry().SetActive("mammouth", "0", true); err != nil {
		t.Fatalf("failed to activate second channel: %v", err)
	}
	srv.models["claude-h"] = ModelInfo{ID: "claude-h", Endpoint: upstream.URL}

	body := `{"model":"claude-h","max_tokens":100,"messages":[{"role":"user","content":"hi"}]}`
	rr := httptest.NewRecorder()
	srv.handleMessages(rr, httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body)))

	if upstreamCalls != 1 {
		t.Fatalf("erwartet genau 1 Upstream-Call (kein Retry, kein Failover), bekommen %d", upstreamCalls)
	}
	if rr.Code != http.StatusPaymentRequired {
		t.Fatalf("erwartet 402, bekommen %d: %s", rr.Code, rr.Body.String())
	}
	var env anthropicErrorEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil || env.Error.Type != "billing_error" {
		t.Fatalf("erwartet billing_error, bekommen: %s", rr.Body.String())
	}
}
