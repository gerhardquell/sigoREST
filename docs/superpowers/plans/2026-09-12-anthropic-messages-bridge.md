# Anthropic-Messages-Bridge Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `POST /v1/messages` (Anthropic Messages API, with tool-calling and streaming) to sigoREST, so Claude Code can run against any sigoREST-configured model via `ANTHROPIC_BASE_URL`.

**Architecture:** A new translation layer (`sigoREST/anthropic.go`) sits in front of the existing engine (`sigoengine.CallAPI`/`CallAPIStream`, `ChannelManager`, `RateLimiter`, `EnhancedCircuitBreaker`) — it maps Anthropic wire format to/from the same internal `map[string]interface{}` request shape `/v1/chat/completions` already uses. `sigoengine.CallAPI` is extended to surface tool-calls from provider responses (currently dropped entirely). Two small, low-risk helpers (`ChannelManager.FailoverList`, `Server.recordUsage`) are extracted from the existing handler and reused by both endpoints; the bigger retry/circuit-breaker/error-mapping control flow is deliberately **not** refactored out of the existing, production-critical `handleChatCompletions` — the new handler gets its own copy of that loop instead, to avoid regression risk on the endpoint Gerhard already depends on daily.

**Tech Stack:** Go 1.26, stdlib only (`encoding/json`, `net/http`, `bufio`), existing `sigoengine` package.

**Spec:** `docs/superpowers/specs/2026-09-12-anthropic-messages-bridge-design.md`

## Global Constraints

- No client-auth-token validation on `/v1/messages` — inherits the same IP-based access control as every other endpoint (same `http.ServeMux`). The `x-api-key`/`ANTHROPIC_AUTH_TOKEN` header the client sends is ignored.
- No memory-block / system-prompt-override / `.sessions` persistence in this path — Claude Code manages its own full conversation context per request.
- Model selection is 1:1 ID/shortcode lookup (`s.lookupModel`), identical to `/v1/chat/completions` — no separate alias table.
- Extended-thinking blocks, image content blocks, and `cache_control` hints are out of scope for this plan — parsed defensively (ignored, never a hard error).
- Existing convention (see `CLAUDE.md`): HTTP handlers themselves are not unit-tested against a real upstream — verified manually. Pure mapper/translator functions that don't need real network ARE unit-tested.
- Every task must leave `go build ./...` and `go test ./...` green before committing.

---

### Task 1: Tool-Call type + extraction helper in sigoengine

**Files:**
- Modify: `sigoengine/engine.go` (add types near `UsageData`, add `extractToolCalls` near `extractUsage`, ~line 1348)
- Test: `sigoengine/tool_calls_test.go` (new)

**Interfaces:**
- Produces: `type ToolCallFunction struct { Name string; Arguments string }`, `type ToolCall struct { ID string; Type string; Function ToolCallFunction }`, `func extractToolCalls(result map[string]interface{}, providerType string) []ToolCall`

- [ ] **Step 1: Write the failing tests**

Create `sigoengine/tool_calls_test.go`:

```go
package sigoengine

import "testing"

func TestExtractToolCallsOpenAI(t *testing.T) {
	result := map[string]interface{}{
		"choices": []interface{}{
			map[string]interface{}{
				"message": map[string]interface{}{
					"content": nil,
					"tool_calls": []interface{}{
						map[string]interface{}{
							"id":   "call_abc123",
							"type": "function",
							"function": map[string]interface{}{
								"name":      "read_file",
								"arguments": `{"path":"/tmp/x.txt"}`,
							},
						},
					},
				},
			},
		},
	}
	calls := extractToolCalls(result, "openai")
	if len(calls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(calls))
	}
	if calls[0].ID != "call_abc123" || calls[0].Function.Name != "read_file" {
		t.Fatalf("unexpected tool call: %+v", calls[0])
	}
	if calls[0].Function.Arguments != `{"path":"/tmp/x.txt"}` {
		t.Fatalf("unexpected arguments: %q", calls[0].Function.Arguments)
	}
}

func TestExtractToolCallsOpenAINone(t *testing.T) {
	result := map[string]interface{}{
		"choices": []interface{}{
			map[string]interface{}{
				"message": map[string]interface{}{"content": "Hallo"},
			},
		},
	}
	if calls := extractToolCalls(result, "openai"); calls != nil {
		t.Fatalf("expected nil, got %+v", calls)
	}
}

func TestExtractToolCallsAnthropic(t *testing.T) {
	result := map[string]interface{}{
		"content": []interface{}{
			map[string]interface{}{"type": "text", "text": "Ich schaue nach."},
			map[string]interface{}{
				"type":  "tool_use",
				"id":    "toolu_01",
				"name":  "read_file",
				"input": map[string]interface{}{"path": "/tmp/x.txt"},
			},
		},
	}
	calls := extractToolCalls(result, "anthropic")
	if len(calls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(calls))
	}
	if calls[0].ID != "toolu_01" || calls[0].Function.Name != "read_file" {
		t.Fatalf("unexpected tool call: %+v", calls[0])
	}
	if calls[0].Function.Arguments != `{"path":"/tmp/x.txt"}` {
		t.Fatalf("unexpected arguments: %q", calls[0].Function.Arguments)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./sigoengine/ -run TestExtractToolCalls -v`
Expected: FAIL — `undefined: extractToolCalls`

- [ ] **Step 3: Implement `ToolCall`/`ToolCallFunction` + `extractToolCalls`**

In `sigoengine/engine.go`, add near the `UsageData` type (~line 555):

```go
// ToolCallFunction beschreibt den aufgerufenen Funktionsnamen + Roh-Argumente
// (JSON-String, wie vom Provider geliefert — Parsing obliegt dem Aufrufer).
type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolCall ist ein normalisierter Tool-Aufruf, unabhängig vom Provider-Format
// (OpenAI choices[0].message.tool_calls oder Anthropic content[].type=="tool_use").
type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"` // "function"
	Function ToolCallFunction `json:"function"`
}
```

Add near `extractUsage` (~line 1348):

```go
// extractToolCalls liest Tool-Calls aus einer Provider-Response.
// OpenAI-Format: choices[0].message.tool_calls (Array von {id, type, function:{name, arguments}}).
// Anthropic-Format: content[] enthält Blocks mit type=="tool_use" ({id, name, input}).
// Liefert nil wenn keine Tool-Calls vorhanden sind.
func extractToolCalls(result map[string]interface{}, providerType string) []ToolCall {
	if providerType == "anthropic" {
		content, ok := result["content"].([]interface{})
		if !ok {
			return nil
		}
		var calls []ToolCall
		for _, item := range content {
			block, ok := item.(map[string]interface{})
			if !ok || block["type"] != "tool_use" {
				continue
			}
			id, _ := block["id"].(string)
			name, _ := block["name"].(string)
			argsJSON := "{}"
			if input, ok := block["input"]; ok {
				if b, err := json.Marshal(input); err == nil {
					argsJSON = string(b)
				}
			}
			calls = append(calls, ToolCall{
				ID:       id,
				Type:     "function",
				Function: ToolCallFunction{Name: name, Arguments: argsJSON},
			})
		}
		return calls
	}

	choices, ok := result["choices"].([]interface{})
	if !ok || len(choices) == 0 {
		return nil
	}
	choice, ok := choices[0].(map[string]interface{})
	if !ok {
		return nil
	}
	msg, ok := choice["message"].(map[string]interface{})
	if !ok {
		return nil
	}
	rawCalls, ok := msg["tool_calls"].([]interface{})
	if !ok {
		return nil
	}
	var calls []ToolCall
	for _, item := range rawCalls {
		tc, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		id, _ := tc["id"].(string)
		tcType, _ := tc["type"].(string)
		if tcType == "" {
			tcType = "function"
		}
		fn, _ := tc["function"].(map[string]interface{})
		name, _ := fn["name"].(string)
		args, _ := fn["arguments"].(string)
		calls = append(calls, ToolCall{
			ID:       id,
			Type:     tcType,
			Function: ToolCallFunction{Name: name, Arguments: args},
		})
	}
	return calls
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./sigoengine/ -run TestExtractToolCalls -v`
Expected: PASS (all 3 tests)

- [ ] **Step 5: Commit**

```bash
git add sigoengine/engine.go sigoengine/tool_calls_test.go
git commit -m "feat: add ToolCall type + extractToolCalls for OpenAI/Anthropic responses"
```

---

### Task 2: Thread tool-calls through `CallAPI` + fix two response-parsing bugs

**Why this task exists:** `CallAPI` currently drops `tool_calls` entirely. Two existing parsing branches also actively **break** on tool-call responses and must be fixed here, not just extended:
1. Anthropic branch only reads `content[0].text` — a tool-only response (`content[0]` is a `tool_use` block, no `text` key) silently falls through to "Unexpected response format".
2. OpenAI branch treats `message.content == nil` as an error ("leere Antwort: max_tokens zu niedrig") — but `content: null` is the **normal** shape of an OpenAI tool-call response. Without this fix, every first tool-call turn would error out.

**Files:**
- Modify: `sigoengine/engine.go:1162-1296` (`CallAPI` function body + signature)
- Modify: `sigoREST/main.go:845` (call site)
- Modify: `cmd/sigoE/main.go:159` (call site)

**Interfaces:**
- Consumes: `ToolCall`, `extractToolCalls` (Task 1)
- Produces: `func CallAPI(ctx context.Context, cfg *ProviderConfig, request map[string]interface{}, timeoutSec int) (text string, usage *UsageData, finishReason string, toolCalls []ToolCall, err error)` — 5 return values instead of 4, `toolCalls` is the new 4th value (before `err`).

- [ ] **Step 1: Replace the `CallAPI` function body**

Replace the entire function in `sigoengine/engine.go` (signature at line 1162 through the closing brace, ~line 1296) with:

```go
func CallAPI(ctx context.Context, cfg *ProviderConfig, request map[string]interface{},
	timeoutSec int) (string, *UsageData, string, []ToolCall, error) {

	start := time.Now()
	logF := map[string]interface{}{"endpoint": cfg.Endpoint, "model": cfg.Model}

	LogDebug("Making API request", logF)

	if timeoutSec > 0 {
		if _, ok := ctx.Deadline(); !ok {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, time.Duration(timeoutSec)*time.Second)
			defer cancel()
		}
	}

	jsonData, _ := json.Marshal(request)

	req, err := http.NewRequestWithContext(ctx, "POST", cfg.Endpoint, bytes.NewBuffer(jsonData))
	if err != nil {
		LogError("Failed to create request", err, logF)
		return "", nil, "", nil, NewError(ErrAPIFailed, "Failed to create HTTP request", err, logF)
	}

	req.Header.Set("Content-Type", "application/json")
	if cfg.Type == "anthropic" {
		req.Header.Set("x-api-key", cfg.APIKey)
		req.Header.Set("anthropic-version", "2023-06-01")
	} else if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}
	for k, v := range cfg.Headers {
		req.Header.Set(k, v)
	}

	resp, err := defaultHTTPClient.Do(req)
	if err != nil {
		LogError("HTTP request failed", err, logF)
		return "", nil, "", nil, NewError(ErrAPIFailed, "HTTP request failed", err, logF)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		logF["status_code"] = resp.StatusCode
		logF["body"] = string(body)
		LogError("HTTP error", nil, logF)

		var retryAfter time.Duration
		if retryHeader := resp.Header.Get("Retry-After"); retryHeader != "" {
			if seconds, err := strconv.Atoi(retryHeader); err == nil {
				retryAfter = time.Duration(seconds) * time.Second
			}
		}

		apiErr := classifyHTTPError(resp.StatusCode, string(body), nil)
		apiErr.RetryAfter = retryAfter
		return "", nil, "", nil, apiErr
	}

	body, _ := io.ReadAll(resp.Body)
	LogDebug("API response", map[string]interface{}{
		"size_bytes":  len(body),
		"duration_ms": time.Since(start).Milliseconds(),
	})

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		LogError("Failed to parse response", err, logF)
		return "", nil, "", nil, NewError(ErrAPIFailed, "Failed to parse JSON response", err, logF)
	}

	if errMsg, ok := result["error"].(map[string]interface{}); ok {
		errText := fmt.Sprintf("%v", errMsg["message"])
		LogError("API error in response", nil, map[string]interface{}{"api_error": errText})

		if isContextLimitError(errText) {
			return "", nil, "", nil, &APIError{
				Type:       ErrClientError,
				StatusCode: 400,
				Message:    errText,
			}
		}

		return "", nil, "", nil, NewError(ErrAPIFailed, errText, nil, logF)
	}

	usage := extractUsage(result, cfg.Type)
	toolCalls := extractToolCalls(result, cfg.Type)

	finishReason := ""
	if choices, ok := result["choices"].([]interface{}); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]interface{}); ok {
			if fr, ok := choice["finish_reason"].(string); ok {
				finishReason = fr
			}
		}
	}
	if finishReason == "" && cfg.Type == "anthropic" {
		if sr, ok := result["stop_reason"].(string); ok {
			finishReason = sr
		}
	}

	// Anthropic-Format: content[] kann mehrere text-Blocks und/oder tool_use-
	// Blocks enthalten (z.B. "Ich schaue nach." + tool_use). Alle text-Blocks
	// werden verkettet; ein reiner Tool-Use-Response (kein text-Block) ist
	// gültig und liefert einen leeren String zurück, kein Fehler.
	if cfg.Type == "anthropic" {
		if content, ok := result["content"].([]interface{}); ok {
			var textParts []string
			for _, item := range content {
				block, ok := item.(map[string]interface{})
				if !ok || block["type"] != "text" {
					continue
				}
				if text, ok := block["text"].(string); ok {
					textParts = append(textParts, text)
				}
			}
			return strings.Join(textParts, ""), usage, finishReason, toolCalls, nil
		}
	}

	// OpenAI-Format: choices[0].message.content. content:null ist normal,
	// wenn stattdessen tool_calls gesetzt sind (Tool-Use-Response) — nur ohne
	// tool_calls ist eine leere Antwort ein echter Fehler (z.B. max_tokens zu
	// niedrig).
	if choices, ok := result["choices"].([]interface{}); ok && len(choices) > 0 {
		if msg, ok := choices[0].(map[string]interface{})["message"].(map[string]interface{}); ok {
			if content, ok := msg["content"].(string); ok {
				return content, usage, finishReason, toolCalls, nil
			}
			if msg["content"] == nil {
				if len(toolCalls) > 0 {
					return "", usage, finishReason, toolCalls, nil
				}
				return "", usage, finishReason, nil, NewError(ErrClientError,
					"leere Antwort: max_tokens zu niedrig oder Modell-Limit erreicht", nil, logF)
			}
		}
	}

	LogError("Unexpected response format", nil, logF)
	return "", nil, "", nil, NewError(ErrUnexpectedFormat, "Unexpected response format", nil, logF)
}
```

- [ ] **Step 2: Update the two call sites**

In `sigoREST/main.go:845`, change:
```go
text, u, fr, e := sigoengine.CallAPI(ctx, cfg, apiRequest, req.Timeout)
```
to:
```go
text, u, fr, _, e := sigoengine.CallAPI(ctx, cfg, apiRequest, req.Timeout)
```
(`/v1/chat/completions` doesn't expose tool-calls in its response yet — out of scope for this plan, ignored with `_`.)

In `cmd/sigoE/main.go:159`, change:
```go
text, _, _, e := sigoengine.CallAPI(ctx, cfg, request, *timeout)
```
to:
```go
text, _, _, _, e := sigoengine.CallAPI(ctx, cfg, request, *timeout)
```

- [ ] **Step 3: Run the full test suite**

Run: `go build ./... && go test ./...`
Expected: build succeeds, all tests pass (Task 1's 3 new tests + the pre-existing 35).

- [ ] **Step 4: Commit**

```bash
git add sigoengine/engine.go sigoREST/main.go cmd/sigoE/main.go
git commit -m "fix: thread tool_calls through CallAPI, fix tool-response parsing bugs"
```

---

### Task 3: Extract `ChannelManager.FailoverList`

**Files:**
- Modify: `sigoengine/channel_manager.go` (add method)
- Modify: `sigoREST/main.go:754-763` (use it)
- Test: `sigoengine/channel_manager_test.go` (append test)

**Interfaces:**
- Produces: `func (m *ChannelManager) FailoverList(provider string, first *Channel) []*Channel`

- [ ] **Step 1: Write the failing test**

Append to `sigoengine/channel_manager_test.go`:

```go
func TestChannelManager_FailoverList(t *testing.T) {
	os.Setenv("MAMMOUTH_API_KEY", "default-key")
	os.Setenv("MAMMOUTH_API_KEY_0", "key-0")
	os.Setenv("MAMMOUTH_API_KEY_1", "key-1")
	defer func() {
		os.Unsetenv("MAMMOUTH_API_KEY")
		os.Unsetenv("MAMMOUTH_API_KEY_0")
		os.Unsetenv("MAMMOUTH_API_KEY_1")
	}()

	reg := NewChannelRegistry("")
	reg.DiscoverFromEnv()
	reg.SetActive("mammouth", "0", true)
	reg.SetActive("mammouth", "1", true)
	mgr := NewChannelManager(reg)

	first, err := mgr.Resolve("mammouth", "")
	if err != nil {
		t.Fatalf("resolve default: %v", err)
	}

	list := mgr.FailoverList("mammouth", first)
	if len(list) != 3 {
		t.Fatalf("expected 3 channels in failover list, got %d: %+v", len(list), list)
	}
	if list[0].Name != "default" {
		t.Fatalf("expected first channel = default, got %s", list[0].Name)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./sigoengine/ -run TestChannelManager_FailoverList -v`
Expected: FAIL — `undefined: FailoverList` (or method not found)

- [ ] **Step 3: Implement `FailoverList`**

In `sigoengine/channel_manager.go`, add after `NextActive`:

```go
// FailoverList returns the ordered list of channels to try for a request:
// the given starting channel followed by the provider's remaining active
// channels in NextActive order. Used by request handlers to build their
// retry/failover loop without re-implementing NextActive traversal.
func (m *ChannelManager) FailoverList(provider string, first *Channel) []*Channel {
	list := []*Channel{first}
	current := first
	for {
		next, ok := m.NextActive(provider, current)
		if !ok {
			break
		}
		list = append(list, next)
		current = next
	}
	return list
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./sigoengine/ -run TestChannelManager_FailoverList -v`
Expected: PASS

- [ ] **Step 5: Use it in `handleChatCompletions`**

In `sigoREST/main.go`, replace lines 754-763:
```go
	// Liste der zu probierenden Kanäle aufbauen (initial + Failover)
	channelsToTry := []*sigoengine.Channel{ch}
	current := ch
	for {
		next, ok := s.channelManager.NextActive(provider, current)
		if !ok {
			break
		}
		channelsToTry = append(channelsToTry, next)
		current = next
	}
```
with:
```go
	// Liste der zu probierenden Kanäle aufbauen (initial + Failover)
	channelsToTry := s.channelManager.FailoverList(provider, ch)
```

- [ ] **Step 6: Run the full test suite**

Run: `go build ./... && go test ./...`
Expected: all tests pass, behavior of `/v1/chat/completions` unchanged (this is a pure, mechanical extraction).

- [ ] **Step 7: Commit**

```bash
git add sigoengine/channel_manager.go sigoengine/channel_manager_test.go sigoREST/main.go
git commit -m "refactor: extract ChannelManager.FailoverList, reuse in handleChatCompletions"
```

---

### Task 4: Extract `Server.recordUsage`

**Files:**
- Modify: `sigoREST/main.go` (add method, use it in `handleChatCompletions`)
- Test: `sigoREST/main_test.go` (append test)

**Interfaces:**
- Consumes: existing `newTestServer(t)` fixture (already in `main_test.go`)
- Produces: `func (s *Server) recordUsage(modelID string, ch *sigoengine.Channel, usage *sigoengine.UsageData)`

- [ ] **Step 1: Write the failing test**

Append to `sigoREST/main_test.go`:

```go
func TestRecordUsage(t *testing.T) {
	srv, _ := newTestServer(t)
	ch := &sigoengine.Channel{Provider: "mammouth", Name: "default"}

	srv.recordUsage("claude-h", ch, &sigoengine.UsageData{InputTokens: 10, OutputTokens: 5, TotalTokens: 15})
	srv.recordUsage("claude-h", ch, &sigoengine.UsageData{InputTokens: 3, OutputTokens: 2, TotalTokens: 5})

	stats := srv.usage["claude-h"]
	if stats == nil || stats.Requests != 2 || stats.TotalTokens != 20 {
		t.Fatalf("unexpected model stats: %+v", stats)
	}

	chStats := srv.usageByChannel["claude-h#mammouth-default"]
	if chStats == nil || chStats.Requests != 2 || chStats.TotalTokens != 20 {
		t.Fatalf("unexpected channel stats: %+v", chStats)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./sigoREST/ -run TestRecordUsage -v`
Expected: FAIL — `srv.recordUsage undefined`

- [ ] **Step 3: Implement `recordUsage`**

In `sigoREST/main.go`, add near `providerForModel`:

```go
// recordUsage aktualisiert die Token-Statistiken für ein Modell und den
// tatsächlich genutzten Kanal. Gemeinsam genutzt von /v1/chat/completions
// und /v1/messages.
func (s *Server) recordUsage(modelID string, ch *sigoengine.Channel, usage *sigoengine.UsageData) {
	s.usageMu.Lock()
	defer s.usageMu.Unlock()

	stats, ok := s.usage[modelID]
	if !ok {
		stats = &ModelUsageStats{}
		s.usage[modelID] = stats
	}
	stats.InputTokens += int64(usage.InputTokens)
	stats.OutputTokens += int64(usage.OutputTokens)
	stats.TotalTokens += int64(usage.TotalTokens)
	stats.Requests++

	channelKey := fmt.Sprintf("%s#%s", modelID, ch.FullName())
	channelStats, ok := s.usageByChannel[channelKey]
	if !ok {
		channelStats = &ModelUsageStats{}
		s.usageByChannel[channelKey] = channelStats
	}
	channelStats.InputTokens += int64(usage.InputTokens)
	channelStats.OutputTokens += int64(usage.OutputTokens)
	channelStats.TotalTokens += int64(usage.TotalTokens)
	channelStats.Requests++
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./sigoREST/ -run TestRecordUsage -v`
Expected: PASS

- [ ] **Step 5: Use it in `handleChatCompletions`**

Replace the "Usage akkumulieren" block (from `s.usageMu.Lock()` through the matching `s.usageMu.Unlock()`, ~24 lines) with:
```go
	// Usage akkumulieren
	s.recordUsage(modelID, successfulCh, responseUsage)
```

- [ ] **Step 6: Run the full test suite**

Run: `go build ./... && go test ./...`
Expected: all tests pass, `/v1/chat/completions` usage-tracking behavior unchanged.

- [ ] **Step 7: Commit**

```bash
git add sigoREST/main.go sigoREST/main_test.go
git commit -m "refactor: extract Server.recordUsage, reuse in handleChatCompletions"
```

---

### Task 5: Anthropic request types + `anthropicRequestToInternal`

**Files:**
- Create: `sigoREST/anthropic.go`
- Test: `sigoREST/anthropic_test.go` (new)

**Interfaces:**
- Produces:
  - `type AnthropicTool struct { Name string; Description string; InputSchema json.RawMessage }`
  - `type AnthropicContentBlock struct { Type, Text, ID, Name string; Input json.RawMessage; ToolUseID string; Content json.RawMessage; IsError bool }`
  - `type AnthropicMessage struct { Role string; Content json.RawMessage }`
  - `type AnthropicRequest struct { Model string; MaxTokens int; Messages []AnthropicMessage; System json.RawMessage; Tools []AnthropicTool; ToolChoice json.RawMessage; Temperature *float64; TopP *float64; StopSequences []string; Stream bool }`
  - `func anthropicRequestToInternal(req *AnthropicRequest) (messages []map[string]interface{}, tools []map[string]interface{}, toolChoice interface{}, err error)`
  - `func anthropicTextFromRaw(raw json.RawMessage) (string, error)` (used again in Task 7)
  - `func anthropicToolChoiceToInternal(raw json.RawMessage) (interface{}, error)`

- [ ] **Step 1: Write the failing tests**

Create `sigoREST/anthropic_test.go`:

```go
package main

import (
	"encoding/json"
	"testing"
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./sigoREST/ -run TestAnthropicRequestToInternal -v`
Expected: FAIL — package doesn't compile (`AnthropicRequest` etc. undefined)

- [ ] **Step 3: Create `sigoREST/anthropic.go` with the types and mapper**

```go
//**********************************************************************
//      sigoREST/anthropic.go
//**********************************************************************
//  Beschreibung: Anthropic-Messages-API-Bridge (POST /v1/messages).
//                Übersetzt Anthropic-Wire-Format <-> internes
//                apiRequest-Format, das sigoengine.CallAPI/CallAPIStream
//                erwarten (dasselbe Format wie /v1/chat/completions nutzt).
//**********************************************************************

package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// AnthropicTool ist ein vom Client angebotenes Tool (Anthropic-Schema).
type AnthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// AnthropicContentBlock deckt die unterstützten Content-Block-Typen ab
// (text, tool_use, tool_result). Andere Typen (image, thinking) werden beim
// Parsen best-effort ignoriert (siehe Spec, "Out of scope").
type AnthropicContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`          // tool_use
	Name      string          `json:"name,omitempty"`        // tool_use
	Input     json.RawMessage `json:"input,omitempty"`       // tool_use
	ToolUseID string          `json:"tool_use_id,omitempty"` // tool_result
	Content   json.RawMessage `json:"content,omitempty"`     // tool_result
	IsError   bool            `json:"is_error,omitempty"`    // tool_result
}

// AnthropicMessage: content ist String ODER Block-Array, deshalb RawMessage.
type AnthropicMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// AnthropicRequest ist der Body von POST /v1/messages.
type AnthropicRequest struct {
	Model         string             `json:"model"`
	MaxTokens     int                `json:"max_tokens"`
	Messages      []AnthropicMessage `json:"messages"`
	System        json.RawMessage    `json:"system,omitempty"`
	Tools         []AnthropicTool    `json:"tools,omitempty"`
	ToolChoice    json.RawMessage    `json:"tool_choice,omitempty"`
	Temperature   *float64           `json:"temperature,omitempty"`
	TopP          *float64           `json:"top_p,omitempty"`
	StopSequences []string           `json:"stop_sequences,omitempty"`
	Stream        bool               `json:"stream,omitempty"`
}

// anthropicRequestToInternal übersetzt einen Anthropic-Messages-Request in
// das interne apiRequest-Format (dasselbe map[string]interface{}, das
// sigoengine.CallAPI/CallAPIStream erwarten). tools/toolChoice werden separat
// zurückgegeben, damit der Aufrufer sie nur bei Bedarf in die apiRequest-Map
// einträgt (leere tools[] soll das Feld nicht erzeugen).
func anthropicRequestToInternal(req *AnthropicRequest) (messages []map[string]interface{}, tools []map[string]interface{}, toolChoice interface{}, err error) {
	if len(req.System) > 0 {
		systemText, sErr := anthropicTextFromRaw(req.System)
		if sErr != nil {
			return nil, nil, nil, fmt.Errorf("system: %w", sErr)
		}
		if systemText != "" {
			messages = append(messages, map[string]interface{}{
				"role": "system", "content": systemText,
			})
		}
	}

	for i, m := range req.Messages {
		var blocks []AnthropicContentBlock
		var plainText string
		if uErr := json.Unmarshal(m.Content, &plainText); uErr == nil {
			blocks = []AnthropicContentBlock{{Type: "text", Text: plainText}}
		} else if uErr := json.Unmarshal(m.Content, &blocks); uErr != nil {
			return nil, nil, nil, fmt.Errorf("messages[%d].content: %w", i, uErr)
		}

		var textParts []string
		var toolCalls []map[string]interface{}
		var toolResultMsgs []map[string]interface{}

		for _, b := range blocks {
			switch b.Type {
			case "text":
				textParts = append(textParts, b.Text)
			case "tool_use":
				toolCalls = append(toolCalls, map[string]interface{}{
					"id":   b.ID,
					"type": "function",
					"function": map[string]interface{}{
						"name":      b.Name,
						"arguments": string(b.Input),
					},
				})
			case "tool_result":
				content, cErr := anthropicTextFromRaw(b.Content)
				if cErr != nil {
					return nil, nil, nil, fmt.Errorf("messages[%d] tool_result: %w", i, cErr)
				}
				toolResultMsgs = append(toolResultMsgs, map[string]interface{}{
					"role":         "tool",
					"tool_call_id": b.ToolUseID,
					"content":      content,
				})
			default:
				// "image", "thinking", unbekannte Typen: bewusst ignoriert (v1-Scope).
				continue
			}
		}

		// tool_result-Blocks zuerst (Provider erwarten die Tool-Antwort direkt
		// im Anschluss an den Assistant-Tool-Call).
		messages = append(messages, toolResultMsgs...)

		if len(textParts) == 0 && len(toolCalls) == 0 {
			continue
		}

		msg := map[string]interface{}{"role": m.Role}
		if len(textParts) > 0 {
			msg["content"] = strings.Join(textParts, "\n")
		} else {
			msg["content"] = nil
		}
		if len(toolCalls) > 0 {
			msg["tool_calls"] = toolCalls
		}
		messages = append(messages, msg)
	}

	for _, t := range req.Tools {
		tools = append(tools, map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  t.InputSchema,
			},
		})
	}

	if len(req.ToolChoice) > 0 {
		toolChoice, err = anthropicToolChoiceToInternal(req.ToolChoice)
		if err != nil {
			return nil, nil, nil, err
		}
	}

	return messages, tools, toolChoice, nil
}

// anthropicTextFromRaw extrahiert Text aus einem Anthropic-"content"-Feld,
// das entweder ein JSON-String oder ein Array von Content-Blocks ist. Bei
// einem Block-Array werden alle text-Blocks verkettet (newline-getrennt).
func anthropicTextFromRaw(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil
	}
	var blocks []AnthropicContentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return "", err
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n"), nil
}

// anthropicToolChoiceToInternal übersetzt Anthropics tool_choice-Schema
// ({"type":"auto"|"any"|"tool"|"none", "name"?}) ins OpenAI-Schema
// ("auto"|"required"|{"type":"function","function":{"name"}}|"none").
func anthropicToolChoiceToInternal(raw json.RawMessage) (interface{}, error) {
	var tc struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &tc); err != nil {
		return nil, fmt.Errorf("tool_choice: %w", err)
	}
	switch tc.Type {
	case "auto":
		return "auto", nil
	case "any":
		return "required", nil
	case "none":
		return "none", nil
	case "tool":
		return map[string]interface{}{
			"type":     "function",
			"function": map[string]interface{}{"name": tc.Name},
		}, nil
	default:
		return "auto", nil
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./sigoREST/ -run TestAnthropicRequestToInternal -v`
Expected: PASS (all 3 tests)

- [ ] **Step 5: Commit**

```bash
git add sigoREST/anthropic.go sigoREST/anthropic_test.go
git commit -m "feat: add Anthropic request types + anthropicRequestToInternal mapper"
```

---

### Task 6: Anthropic response types + `internalToAnthropicResponse` + `finishReasonToStopReason`

**Files:**
- Modify: `sigoREST/anthropic.go` (append types + functions)
- Modify: `sigoREST/anthropic_test.go` (append tests)

**Interfaces:**
- Consumes: `sigoengine.ToolCall`, `sigoengine.ToolCallFunction`, `sigoengine.UsageData` (Task 1)
- Produces:
  - `type AnthropicUsage struct { InputTokens, OutputTokens int }`
  - `type AnthropicResponse struct { ID, Type, Role, Model string; Content []AnthropicContentBlock; StopReason string; Usage AnthropicUsage }`
  - `func finishReasonToStopReason(finishReason string, hasToolCalls bool) string`
  - `func internalToAnthropicResponse(model, text string, toolCalls []sigoengine.ToolCall, usage *sigoengine.UsageData, finishReason string) *AnthropicResponse`

- [ ] **Step 1: Write the failing tests**

Append to `sigoREST/anthropic_test.go` (add `"sigorest/sigoengine"` to imports):

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./sigoREST/ -run "TestFinishReasonToStopReason|TestInternalToAnthropicResponse" -v`
Expected: FAIL — undefined symbols

- [ ] **Step 3: Append the types and functions to `sigoREST/anthropic.go`**

Add `"time"` and `"sigorest/sigoengine"` to the import block, then append:

```go
// AnthropicUsage ist das usage-Objekt der Anthropic-Response.
type AnthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// AnthropicResponse ist der Body einer nicht-gestreamten POST /v1/messages
// Antwort.
type AnthropicResponse struct {
	ID         string                  `json:"id"`
	Type       string                  `json:"type"`
	Role       string                  `json:"role"`
	Model      string                  `json:"model"`
	Content    []AnthropicContentBlock `json:"content"`
	StopReason string                  `json:"stop_reason,omitempty"`
	Usage      AnthropicUsage          `json:"usage"`
}

// finishReasonToStopReason mappt OpenAI/Anthropic finish_reason-Werte auf
// Anthropics stop_reason-Vokabular. hasToolCalls hat Vorrang, weil manche
// Provider bei Tool-Calls trotzdem finish_reason="stop" liefern.
func finishReasonToStopReason(finishReason string, hasToolCalls bool) string {
	if hasToolCalls {
		return "tool_use"
	}
	switch finishReason {
	case "length":
		return "max_tokens"
	case "tool_calls":
		return "tool_use"
	case "stop_sequence":
		return "stop_sequence"
	default:
		return "end_turn"
	}
}

// internalToAnthropicResponse baut die Anthropic-Response aus dem
// normalisierten Ergebnis eines CallAPI-Aufrufs.
func internalToAnthropicResponse(model, text string, toolCalls []sigoengine.ToolCall, usage *sigoengine.UsageData, finishReason string) *AnthropicResponse {
	var content []AnthropicContentBlock
	if text != "" {
		content = append(content, AnthropicContentBlock{Type: "text", Text: text})
	}
	for _, tc := range toolCalls {
		input := json.RawMessage(tc.Function.Arguments)
		if !json.Valid(input) {
			input = json.RawMessage("{}")
		}
		content = append(content, AnthropicContentBlock{
			Type:  "tool_use",
			ID:    tc.ID,
			Name:  tc.Function.Name,
			Input: input,
		})
	}

	resp := &AnthropicResponse{
		ID:         fmt.Sprintf("msg_%d", time.Now().UnixNano()),
		Type:       "message",
		Role:       "assistant",
		Model:      model,
		Content:    content,
		StopReason: finishReasonToStopReason(finishReason, len(toolCalls) > 0),
	}
	if usage != nil {
		resp.Usage = AnthropicUsage{InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens}
	}
	return resp
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./sigoREST/ -run "TestFinishReasonToStopReason|TestInternalToAnthropicResponse" -v`
Expected: PASS (5 test cases across 3 test functions)

- [ ] **Step 5: Commit**

```bash
git add sigoREST/anthropic.go sigoREST/anthropic_test.go
git commit -m "feat: add Anthropic response types + internalToAnthropicResponse mapper"
```

---

### Task 7: `handleMessages` (non-streaming) + shared error-mapping helper + route

**Files:**
- Modify: `sigoREST/anthropic.go` (append `handleMessages`, `writeAPIError`)
- Modify: `sigoREST/main.go` (register route)
- Modify: `sigoREST/anthropic_test.go` (append handler tests)

**Interfaces:**
- Consumes: `anthropicRequestToInternal`, `anthropicTextFromRaw`, `internalToAnthropicResponse` (Tasks 5-6), `s.channelManager.FailoverList` (Task 3), `s.recordUsage` (Task 4), `sigoengine.CallAPI` (Task 2)
- Produces: `func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request)`, `func (s *Server) writeAPIError(w http.ResponseWriter, modelID string, err error)`

Note: this task deliberately does **not** touch `handleChatCompletions`'s existing failover/retry/circuit-breaker/error-mapping block — that code is production-critical and stays as-is. `handleMessages` implements its own copy of the same control flow using the same lower-level primitives (`CallAPI`, `RateLimiter`, `EnhancedCircuitBreaker`, `ClassifyError`).

- [ ] **Step 1: Write the failing tests**

Append to `sigoREST/anthropic_test.go` (add `"net/http"`, `"net/http/httptest"`, `"strings"` to imports):

```go
func TestHandleMessages_ModelNotFound(t *testing.T) {
	srv, _ := newTestServer(t)
	body := `{"model":"does-not-exist","max_tokens":100,"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	rr := httptest.NewRecorder()

	srv.handleMessages(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleMessages_StreamingNotYetSupported(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.models["claude-h"] = ModelInfo{ID: "claude-h", Endpoint: "https://api.mammouth.ai/v1/chat/completions"}

	body := `{"model":"claude-h","max_tokens":100,"stream":true,"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	rr := httptest.NewRecorder()

	srv.handleMessages(rr, req)

	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d: %s", rr.Code, rr.Body.String())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./sigoREST/ -run TestHandleMessages -v`
Expected: FAIL — `srv.handleMessages undefined`

- [ ] **Step 3: Implement `writeAPIError` and `handleMessages`**

Append to `sigoREST/anthropic.go` (add `"context"`, `"net/http"`, `"time"` to imports if not already present from Task 6):

```go
// writeAPIError klassifiziert einen Fehler aus der Provider-Kette und
// schreibt die passende HTTP-Fehlerantwort (Status-Code, Error-Type,
// Retry-After bei Rate-Limits). Eigenständig von handleChatCompletions'
// Fehlerbehandlung, um den bestehenden, produktionskritischen Pfad nicht
// anzufassen.
func (s *Server) writeAPIError(w http.ResponseWriter, modelID string, err error) {
	if err == sigoengine.ErrRateLimited {
		retryAfter := s.rateMaxWait.Seconds()
		if retryAfter < 1 {
			retryAfter = 1
		}
		sigoengine.LogWarn("Alle Kanäle rate-limitiert", map[string]interface{}{
			"model": modelID, "retry_after": retryAfter,
		})
		w.Header().Set("Retry-After", fmt.Sprintf("%.0f", retryAfter))
		writeError(w, "rate limit exceeded: all channels throttled", "rate_limit", http.StatusTooManyRequests)
		return
	}

	apiErr := sigoengine.ClassifyError(err)
	sigoengine.LogError("API-Call fehlgeschlagen", err, map[string]interface{}{
		"model":       modelID,
		"error_type":  apiErr.Type,
		"status_code": apiErr.StatusCode,
	})

	httpStatus := http.StatusBadGateway
	errType := "api_error"
	switch apiErr.Type {
	case sigoengine.ErrRateLimit:
		httpStatus = http.StatusTooManyRequests
		errType = "rate_limit"
		if apiErr.RetryAfter > 0 {
			w.Header().Set("Retry-After", fmt.Sprintf("%.0f", apiErr.RetryAfter.Seconds()))
		}
	case sigoengine.ErrAuthFailed:
		httpStatus = http.StatusUnauthorized
		errType = "auth_failed"
	case sigoengine.ErrTimeout:
		httpStatus = http.StatusGatewayTimeout
		errType = "timeout"
	case sigoengine.ErrServerError:
		httpStatus = http.StatusServiceUnavailable
		errType = "server_error"
	case sigoengine.ErrClientError:
		httpStatus = http.StatusBadRequest
		errType = "client_error"
	case sigoengine.ErrCircuitOpen:
		httpStatus = http.StatusServiceUnavailable
		errType = "circuit_open"
	}
	writeError(w, apiErr.Message, errType, httpStatus)
}

// handleMessages implementiert POST /v1/messages (Anthropic-Messages-API).
// Übersetzt Request/Response und nutzt dieselbe Channel-Resolution/
// Failover/Rate-Limiter/Circuit-Breaker-Maschinerie wie handleChatCompletions.
func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, "Method not allowed", "invalid_request", http.StatusMethodNotAllowed)
		return
	}

	var req AnthropicRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "Invalid JSON: "+err.Error(), "invalid_request", http.StatusBadRequest)
		return
	}

	s.mu.RLock()
	modelInfo, modelID, exists := s.lookupModel(req.Model)
	s.mu.RUnlock()
	if !exists {
		writeError(w, fmt.Sprintf("Model '%s' nicht gefunden", req.Model), "not_found_error", http.StatusNotFound)
		return
	}

	provider := s.providerForModel(modelID)
	ch, err := s.channelManager.Resolve(provider, "")
	if err != nil {
		writeError(w, err.Error(), "api_error", http.StatusServiceUnavailable)
		return
	}

	messages, tools, toolChoice, err := anthropicRequestToInternal(&req)
	if err != nil {
		writeError(w, "Invalid request: "+err.Error(), "invalid_request", http.StatusBadRequest)
		return
	}

	firstCfg, err := sigoengine.LoadConfigWithChannel(modelID, ch)
	if err != nil {
		writeError(w, err.Error(), "config_error", http.StatusInternalServerError)
		return
	}
	wireModel := firstCfg.Model
	if modelInfo.UpstreamID != "" {
		wireModel = modelInfo.UpstreamID
	}

	apiRequest := map[string]interface{}{
		"model":    wireModel,
		"messages": messages,
	}
	if req.Temperature != nil {
		apiRequest["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		apiRequest["top_p"] = *req.TopP
	}
	if len(req.StopSequences) > 0 {
		apiRequest["stop"] = req.StopSequences
	}
	if len(tools) > 0 {
		apiRequest["tools"] = tools
	}
	if toolChoice != nil {
		apiRequest["tool_choice"] = toolChoice
	}
	if modelInfo.RequiresCompletionTokens {
		apiRequest["max_completion_tokens"] = req.MaxTokens
	} else {
		apiRequest["max_tokens"] = req.MaxTokens
	}

	if req.Stream {
		writeError(w, "streaming not yet supported on /v1/messages", "not_implemented", http.StatusNotImplemented)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 180*time.Second)
	defer cancel()

	var inputBuilder strings.Builder
	for _, m := range req.Messages {
		text, _ := anthropicTextFromRaw(m.Content)
		inputBuilder.WriteString(text)
	}
	inputText := inputBuilder.String()

	channelsToTry := s.channelManager.FailoverList(provider, ch)
	retryConfig := sigoengine.DefaultRetryConfig()

	var responseText string
	var responseUsage *sigoengine.UsageData
	var responseFinishReason string
	var responseToolCalls []sigoengine.ToolCall
	var successfulCh *sigoengine.Channel
	var lastErr error

	for _, currentCh := range channelsToTry {
		cfg, err := sigoengine.LoadConfigWithChannel(modelID, currentCh)
		if err != nil {
			lastErr = err
			continue
		}
		cfg.Endpoint = modelInfo.Endpoint
		cfg.Model = wireModel

		minInt := s.rateMinInterval
		if currentCh.MinInterval > 0 {
			minInt = time.Duration(currentCh.MinInterval) * time.Millisecond
		}
		maxW := s.rateMaxWait
		if currentCh.MaxWait > 0 {
			maxW = time.Duration(currentCh.MaxWait) * time.Millisecond
		}
		if minInt > 0 {
			if err := s.rateLimiter.Acquire(ctx, currentCh.FullName(), minInt, maxW); err != nil {
				lastErr = err
				if err == sigoengine.ErrRateLimited {
					continue
				}
				break
			}
			s.rateLimiter.Release(currentCh.FullName())
		}

		cbKey := fmt.Sprintf("%s#%s", modelID, currentCh.FullName())
		s.mu.Lock()
		if _, exists := s.breakers[cbKey]; !exists {
			s.breakers[cbKey] = sigoengine.NewEnhancedCircuitBreaker(&sigoengine.CircuitBreakerConfig{
				Threshold: 5, Window: 60 * time.Second, Cooldown: 10 * time.Second, HalfOpenMax: 3,
			})
		}
		breaker := s.breakers[cbKey]
		s.mu.Unlock()

		lastErr = sigoengine.RetryWithBackoff(ctx, retryConfig, func() error {
			return breaker.Do(func() error {
				text, u, fr, tc, e := sigoengine.CallAPI(ctx, cfg, apiRequest, 180)
				if e != nil {
					apiErr := sigoengine.ClassifyError(e)
					if apiErr.Type == sigoengine.ErrAuthFailed {
						s.channelManager.Registry().SetActive(currentCh.Provider, currentCh.Name, false)
					}
					return e
				}
				responseText = text
				responseUsage = u
				responseFinishReason = fr
				responseToolCalls = tc
				return nil
			})
		})

		if lastErr == nil {
			successfulCh = currentCh
			s.channelManager.Registry().MarkChannelHealth(currentCh.Provider, currentCh.Name, true, "")
			break
		}

		s.channelManager.Registry().MarkChannelHealth(currentCh.Provider, currentCh.Name, false, lastErr.Error())
		if sigoengine.ClassifyError(lastErr).Type == sigoengine.ErrClientError {
			break
		}
	}

	if lastErr != nil {
		s.writeAPIError(w, modelID, lastErr)
		return
	}

	if responseUsage == nil {
		responseUsage = sigoengine.EstimateUsage(inputText, responseText)
	}
	s.recordUsage(modelID, successfulCh, responseUsage)

	resp := internalToAnthropicResponse(req.Model, responseText, responseToolCalls, responseUsage, responseFinishReason)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
```

- [ ] **Step 4: Register the route**

In `sigoREST/main.go`, add next to the other `mux.HandleFunc` calls (~line 1749):
```go
	mux.HandleFunc("/v1/messages", srv.handleMessages)
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./sigoREST/ -run TestHandleMessages -v`
Expected: PASS (both tests)

- [ ] **Step 6: Run the full test suite and build**

Run: `go build ./... && go test ./...`
Expected: all green.

- [ ] **Step 7: Commit**

```bash
git add sigoREST/anthropic.go sigoREST/anthropic_test.go sigoREST/main.go
git commit -m "feat: add POST /v1/messages handler (non-streaming) + route"
```

---

### Task 8: `streamAnthropicResponse` streaming translator

**Files:**
- Modify: `sigoREST/anthropic.go` (append)
- Modify: `sigoREST/anthropic_test.go` (append tests)

**Interfaces:**
- Consumes: `finishReasonToStopReason` (Task 6)
- Produces: `func (s *Server) streamAnthropicResponse(w http.ResponseWriter, stream io.ReadCloser, model string) (string, error)`

- [ ] **Step 1: Write the failing tests**

Append to `sigoREST/anthropic_test.go` (add `"encoding/json"` already present, add `"io"` to imports):

```go
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
		`"path"`,
		`"stop_reason":"tool_use"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected body to contain %q, got:\n%s", want, body)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./sigoREST/ -run TestStreamAnthropicResponse -v`
Expected: FAIL — `srv.streamAnthropicResponse undefined`

- [ ] **Step 3: Implement `streamAnthropicResponse`**

Append to `sigoREST/anthropic.go` (add `"bufio"`, `"io"` to imports):

```go
// writeAnthropicSSEEvent schreibt ein einzelnes Anthropic-SSE-Event
// (event: + data: + Leerzeile) und flusht sofort.
func writeAnthropicSSEEvent(w http.ResponseWriter, flusher http.Flusher, eventType string, payload interface{}) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, data); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

// streamAnthropicResponse liest einen OpenAI-kompatiblen SSE-Stream (wie von
// sigoengine.CallAPIStream geliefert) und übersetzt ihn live in eine
// Anthropic-Messages-Event-Sequenz. Tool-Call-Argument-Fragmente werden
// unverändert als partial_json durchgereicht (Anthropic erwartet ohnehin
// akkumulierbare JSON-Fragmente, keine Neu-Serialisierung nötig).
func (s *Server) streamAnthropicResponse(w http.ResponseWriter, stream io.ReadCloser, model string) (string, error) {
	defer stream.Close()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	flusher, ok := w.(http.Flusher)
	if !ok {
		return "", fmt.Errorf("response writer does not support flushing")
	}

	messageID := fmt.Sprintf("msg_%d", time.Now().UnixNano())
	writeAnthropicSSEEvent(w, flusher, "message_start", map[string]interface{}{
		"type": "message_start",
		"message": map[string]interface{}{
			"id": messageID, "type": "message", "role": "assistant", "model": model,
			"content": []interface{}{}, "stop_reason": nil,
			"usage": map[string]interface{}{"input_tokens": 0, "output_tokens": 0},
		},
	})

	var responseText strings.Builder
	nextBlockIndex := 0
	textBlockIndex := -1
	toolBlockIndexByOpenAIIndex := map[int]int{}
	var openBlockIndices []int
	finishReason := ""
	var usage *sigoengine.UsageData

	scanner := bufio.NewScanner(stream)
	buf := make([]byte, 4096)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		dataStr := strings.TrimPrefix(line, "data: ")
		if dataStr == "" || dataStr == "[DONE]" {
			continue
		}
		var chunk map[string]interface{}
		if err := json.Unmarshal([]byte(dataStr), &chunk); err != nil {
			continue
		}

		if u, ok := chunk["usage"].(map[string]interface{}); ok {
			usage = &sigoengine.UsageData{}
			if v, ok := u["prompt_tokens"].(float64); ok {
				usage.InputTokens = int(v)
			}
			if v, ok := u["completion_tokens"].(float64); ok {
				usage.OutputTokens = int(v)
			}
		}

		choices, ok := chunk["choices"].([]interface{})
		if !ok || len(choices) == 0 {
			continue
		}
		choice, ok := choices[0].(map[string]interface{})
		if !ok {
			continue
		}
		if fr, ok := choice["finish_reason"].(string); ok && fr != "" {
			finishReason = fr
		}
		delta, ok := choice["delta"].(map[string]interface{})
		if !ok {
			continue
		}

		if text, ok := delta["content"].(string); ok && text != "" {
			if textBlockIndex == -1 {
				textBlockIndex = nextBlockIndex
				nextBlockIndex++
				openBlockIndices = append(openBlockIndices, textBlockIndex)
				writeAnthropicSSEEvent(w, flusher, "content_block_start", map[string]interface{}{
					"type": "content_block_start", "index": textBlockIndex,
					"content_block": map[string]interface{}{"type": "text", "text": ""},
				})
			}
			responseText.WriteString(text)
			writeAnthropicSSEEvent(w, flusher, "content_block_delta", map[string]interface{}{
				"type": "content_block_delta", "index": textBlockIndex,
				"delta": map[string]interface{}{"type": "text_delta", "text": text},
			})
		}

		if rawToolCalls, ok := delta["tool_calls"].([]interface{}); ok {
			for _, item := range rawToolCalls {
				tc, ok := item.(map[string]interface{})
				if !ok {
					continue
				}
				openAIIdx := 0
				if v, ok := tc["index"].(float64); ok {
					openAIIdx = int(v)
				}
				anthropicIdx, seen := toolBlockIndexByOpenAIIndex[openAIIdx]
				fn, _ := tc["function"].(map[string]interface{})
				if !seen {
					anthropicIdx = nextBlockIndex
					nextBlockIndex++
					toolBlockIndexByOpenAIIndex[openAIIdx] = anthropicIdx
					openBlockIndices = append(openBlockIndices, anthropicIdx)
					id, _ := tc["id"].(string)
					name := ""
					if fn != nil {
						name, _ = fn["name"].(string)
					}
					writeAnthropicSSEEvent(w, flusher, "content_block_start", map[string]interface{}{
						"type": "content_block_start", "index": anthropicIdx,
						"content_block": map[string]interface{}{
							"type": "tool_use", "id": id, "name": name, "input": map[string]interface{}{},
						},
					})
				}
				if fn != nil {
					if args, ok := fn["arguments"].(string); ok && args != "" {
						writeAnthropicSSEEvent(w, flusher, "content_block_delta", map[string]interface{}{
							"type": "content_block_delta", "index": anthropicIdx,
							"delta": map[string]interface{}{"type": "input_json_delta", "partial_json": args},
						})
					}
				}
			}
		}
	}

	for _, idx := range openBlockIndices {
		writeAnthropicSSEEvent(w, flusher, "content_block_stop", map[string]interface{}{
			"type": "content_block_stop", "index": idx,
		})
	}

	hasToolCalls := len(toolBlockIndexByOpenAIIndex) > 0
	stopReason := finishReasonToStopReason(finishReason, hasToolCalls)
	usagePayload := map[string]interface{}{"output_tokens": 0}
	if usage != nil {
		usagePayload["output_tokens"] = usage.OutputTokens
	}
	writeAnthropicSSEEvent(w, flusher, "message_delta", map[string]interface{}{
		"type":  "message_delta",
		"delta": map[string]interface{}{"stop_reason": stopReason, "stop_sequence": nil},
		"usage": usagePayload,
	})
	writeAnthropicSSEEvent(w, flusher, "message_stop", map[string]interface{}{"type": "message_stop"})

	if err := scanner.Err(); err != nil {
		return responseText.String(), err
	}
	return responseText.String(), nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./sigoREST/ -run TestStreamAnthropicResponse -v`
Expected: PASS (both tests)

- [ ] **Step 5: Run the full test suite and build**

Run: `go build ./... && go test ./...`
Expected: all green.

- [ ] **Step 6: Commit**

```bash
git add sigoREST/anthropic.go sigoREST/anthropic_test.go
git commit -m "feat: add streamAnthropicResponse (OpenAI SSE -> Anthropic SSE translator)"
```

---

### Task 9: Wire streaming into `handleMessages`

**Files:**
- Modify: `sigoREST/anthropic.go` (`handleMessages` body)
- Modify: `sigoREST/anthropic_test.go` (replace the now-obsolete "not yet supported" test)

**Interfaces:**
- Consumes: `streamAnthropicResponse` (Task 8), `sigoengine.CallAPIStream` (existing)

- [ ] **Step 1: Update the test**

In `sigoREST/anthropic_test.go`, replace `TestHandleMessages_StreamingNotYetSupported` with a test proving the streaming branch is reached (it will fail against a fake endpoint with a network error, not a 501 — that's the signal the placeholder is gone):

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./sigoREST/ -run TestHandleMessages_StreamingReachesProviderCall -v`
Expected: FAIL — still gets 501 (placeholder still in place)

- [ ] **Step 3: Replace the streaming placeholder with real branching**

In `sigoREST/anthropic.go`, in `handleMessages`:

Remove:
```go
	if req.Stream {
		writeError(w, "streaming not yet supported on /v1/messages", "not_implemented", http.StatusNotImplemented)
		return
	}
```

Add `var streamed bool` next to the other `var response...` declarations before the failover loop.

Replace the loop's call block:
```go
		lastErr = sigoengine.RetryWithBackoff(ctx, retryConfig, func() error {
			return breaker.Do(func() error {
				text, u, fr, tc, e := sigoengine.CallAPI(ctx, cfg, apiRequest, 180)
				if e != nil {
					apiErr := sigoengine.ClassifyError(e)
					if apiErr.Type == sigoengine.ErrAuthFailed {
						s.channelManager.Registry().SetActive(currentCh.Provider, currentCh.Name, false)
					}
					return e
				}
				responseText = text
				responseUsage = u
				responseFinishReason = fr
				responseToolCalls = tc
				return nil
			})
		})
```
with:
```go
		if req.Stream && cfg.Type != "anthropic" {
			lastErr = breaker.Do(func() error {
				stream, e := sigoengine.CallAPIStream(ctx, cfg, apiRequest)
				if e != nil {
					return e
				}
				text, e := s.streamAnthropicResponse(w, stream, req.Model)
				if e != nil {
					return e
				}
				responseText = text
				streamed = true
				return nil
			})
		} else {
			lastErr = sigoengine.RetryWithBackoff(ctx, retryConfig, func() error {
				return breaker.Do(func() error {
					text, u, fr, tc, e := sigoengine.CallAPI(ctx, cfg, apiRequest, 180)
					if e != nil {
						apiErr := sigoengine.ClassifyError(e)
						if apiErr.Type == sigoengine.ErrAuthFailed {
							s.channelManager.Registry().SetActive(currentCh.Provider, currentCh.Name, false)
						}
						return e
					}
					responseText = text
					responseUsage = u
					responseFinishReason = fr
					responseToolCalls = tc
					return nil
				})
			})
		}
```

And after `if lastErr != nil { s.writeAPIError(...); return }`, before the usage/response-building code, add:
```go
	if streamed {
		return // Anthropic-SSE-Antwort wurde bereits vollständig geschrieben.
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./sigoREST/ -run TestHandleMessages_StreamingReachesProviderCall -v`
Expected: PASS

- [ ] **Step 5: Run the full test suite and build**

Run: `go build ./... && go test ./...`
Expected: all green.

- [ ] **Step 6: Commit**

```bash
git add sigoREST/anthropic.go sigoREST/anthropic_test.go
git commit -m "feat: wire streaming into handleMessages"
```

---

### Task 10: Manual end-to-end verification with the real `claude` CLI

Not automatable — mirrors the manual verification done for the longcat and cheaperinference rollouts (see `RETROSPECTIVE.md`, `feedback_provider_switch_gotcha` memory: fetch-only tests hide wiring bugs that only a real chat call catches).

- [ ] **Step 1: Build and start a local sigoREST instance**

```bash
go build -o /tmp/sigoREST-test ./sigoREST/
/tmp/sigoREST-test -v debug &
```

- [ ] **Step 2: Point the `claude` CLI at sigoREST instead of cheaperinference**

```bash
unset ANTHROPIC_API_KEY
export ANTHROPIC_BASE_URL="http://localhost:9080"
export ANTHROPIC_AUTH_TOKEN="unused"   # sigoREST ignores it, but claude CLI requires it set
export ANTHROPIC_MODEL="ci-claude-opus-5"   # or any other sigoREST model ID/shortcode
claude "List the files in the current directory using your Bash tool, then tell me how many there are."
```

- [ ] **Step 3: Verify**

- Claude Code responds without protocol errors.
- It actually invokes the `Bash`/`ls`-equivalent tool (proves tool-calling round-trips correctly: request→response with `tool_use`→your next turn carries `tool_result`→final answer).
- Check sigoREST's debug log for `provider=<expected provider>` and no `Unexpected response format` warnings.
- Repeat once against a **second** provider (e.g. set `ANTHROPIC_MODEL` to a Moonshot or ZAI model) to catch provider-specific tool-call-streaming quirks (per the spec's "Risiken" section — this is the fragile part).

- [ ] **Step 4: Stop the test server**

```bash
kill %1   # or pkill -f /tmp/sigoREST-test
rm -f /tmp/sigoREST-test
```

- [ ] **Step 5: Note results in the PR/commit description** — no code change in this task, but do not consider the feature done until this manual pass succeeds against at least two providers.

---

### Task 11: Update `CLAUDE.md`

**Files:**
- Modify: `CLAUDE.md`

- [ ] **Step 1: Document the new endpoint**

Add a row to the Endpoints table:
```
| `/v1/messages` | POST | Anthropic-Messages-API-Bridge (Claude Code o.ä.), Tool-Calling, alle Provider |
```

Add a new subsection near "Dynamisches Modell-Laden (Server)" or after "Multi-Channel, Failover, Rate-Limiting, Health-Monitor":

```markdown
### Anthropic-Messages-Bridge (`/v1/messages`)

Übersetzt Anthropic-Messages-Wire-Format (Request, Response, SSE-Streaming,
Tool-Calling) auf dieselbe interne Engine wie `/v1/chat/completions` — jedes
sigoREST-Modell ist damit auch über `ANTHROPIC_BASE_URL` (z.B. Claude Code)
erreichbar, unabhängig vom Provider-Wire-Format. Modellwahl ist 1:1 wie bei
`/v1/chat/completions` (ID/Shortcode, kein Alias). Keine Memory-/System-
Prompt-/Session-Injektion in diesem Pfad — der Client verwaltet seinen
eigenen Kontext. Details: `docs/superpowers/specs/2026-09-12-anthropic-messages-bridge-design.md`.
```

- [ ] **Step 2: Commit**

```bash
git add CLAUDE.md
git commit -m "docs: document /v1/messages Anthropic-Messages-Bridge"
```

---

## Plan Self-Review

**Spec coverage:**
- `/v1/messages` endpoint → Task 7
- Modellwahl 1:1 → Task 7 (`s.lookupModel`, unchanged)
- Tool-Calling (request + response) → Tasks 1, 2, 5, 6
- Streaming Pflicht → Tasks 8, 9
- Alle Provider erreichbar → Task 7 (uses `providerForModel`/`ChannelManager` unchanged, no provider-specific branching added)
- Kein neuer Auth-Mechanismus → Task 7 (no auth check added, inherits mux-level IP control)
- Keine Memory/System-Prompt/Session-Injektion → Task 7 (handler doesn't touch `s.memory`/`s.systemPrompt`/sessions at all)
- Out-of-scope Felder (thinking/image/cache_control) best-effort ignoriert → Task 5 (`default: continue` in the block-type switch)
- Engine-Erweiterung (`ToolCall`, `CallAPI`-Signatur) → Tasks 1, 2
- Code-Organisation (`sigoREST/anthropic.go`, geteilte Helper) → Tasks 3, 4, 5-9
- Testing-Strategie (Mapper-Unit-Tests, Streaming-Unit-Test, manueller E2E) → every task's test step + Task 10
- Risiko "Tool-Call-Streaming-Korrelation gegen ≥2 Provider testen" → Task 10, Step 3

**Placeholder scan:** No "TBD"/"TODO" in any task; the one intentional interim behavior (`req.Stream` → 501) is a real, correct response for a real not-yet-implemented state, not a placeholder — and Task 9 removes it with a test that specifically checks it's gone.

**Type consistency check:**
- `sigoengine.CallAPI` return tuple `(string, *UsageData, string, []ToolCall, error)` used identically in Task 2 (definition), Task 7 and Task 9 (call sites).
- `ToolCall`/`ToolCallFunction` field names (`ID`, `Type`, `Function.Name`, `Function.Arguments`) used identically in Tasks 1, 2, 6, 7, 9.
- `anthropicRequestToInternal` return order `(messages, tools, toolChoice, err)` used identically in Task 5 (definition) and Task 7 (call site).
- `ChannelManager.FailoverList(provider string, first *Channel) []*Channel` used identically in Task 3 (definition) and Task 7 (call site).
- `Server.recordUsage(modelID string, ch *sigoengine.Channel, usage *sigoengine.UsageData)` used identically in Task 4 (definition) and Task 7 (call site).
