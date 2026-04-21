# finish_reason weitergeben Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `finish_reason` in `POST /v1/chat/completions` Response zur Verfuegung stellen.

**Architecture:** `CallAPI` gibt `finish_reason` zusaetzlich zurueck. `ChatChoice` bekommt `FinishReason` Feld. Integration in `sigoREST/main.go`.

**Tech Stack:** Go 1.26

---

### Task 1: `CallAPI` Signatur erweitern + `finish_reason` extrahieren

**Files:**
- Modify: `sigoengine/engine.go:1128-1234`

- [ ] **Step 1: Signatur aendern**

Ersetze Zeile 1128-1129:

```go
func CallAPI(ctx context.Context, cfg *ProviderConfig, request map[string]interface{},
	timeoutSec int) (string, *UsageData, error) {
```

durch:

```go
func CallAPI(ctx context.Context, cfg *ProviderConfig, request map[string]interface{},
	timeoutSec int) (string, *UsageData, string, error) {
```

- [ ] **Step 2: Alle `return` Statements in `CallAPI` anpassen**

Jedes `return` in `CallAPI` muss ein viertes Argument (string) fuer `finish_reason` erhalten:

Suche nach allen `return` Statements in `CallAPI` und fuege `, ""` als drittletztes Argument ein:

- Zeile 1142: `return "", nil, NewError(...)` → `return "", nil, "", NewError(...)`
- Zeile 1159: `return "", nil, NewError(...)` → `return "", nil, "", NewError(...)`
- Zeile 1180: `return "", nil, apiErr` → `return "", nil, "", apiErr`
- Zeile 1192: `return "", nil, NewError(...)` → `return "", nil, "", NewError(...)`
- Zeile 1203: `return "", nil, &APIError{...}` → `return "", nil, "", &APIError{...}`
- Zeile 1209: `return "", nil, NewError(...)` → `return "", nil, "", NewError(...)`
- Zeile 1219: `return text, usage, nil` → `return text, usage, "", nil`
- Zeile 1228: `return content, usage, nil` → `return content, usage, "", nil`
- Zeile 1234: `return "", nil, NewError(...)` → `return "", nil, "", NewError(...)`

- [ ] **Step 3: `finish_reason` extrahieren**

Fuege VOR dem Anthropic-Format-Block (Zeile 1215) ein:

```go
	// finish_reason extrahieren
	finishReason := ""
	if choices, ok := result["choices"].([]interface{}); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]interface{}); ok {
			if fr, ok := choice["finish_reason"].(string); ok {
				finishReason = fr
			}
		}
	}
	// Anthropic fallback: stop_reason
	if finishReason == "" && cfg.Type == "anthropic" {
		if sr, ok := result["stop_reason"].(string); ok {
			finishReason = sr
		}
	}
```

Dann ersetze die `return` Statements fuer erfolgreiche Responses:

Zeile 1219: `return text, usage, "", nil` → `return text, usage, finishReason, nil`
Zeile 1228: `return content, usage, "", nil` → `return content, usage, finishReason, nil`

- [ ] **Step 4: Build pruefen**

Run: `go build ./...`
Expected: Keine Fehler (es gibt noch keine Caller mit 4 Return-Werten)

- [ ] **Step 5: Commit**

```bash
git add sigoengine/engine.go
git commit -m "$(cat <<'EOF'
feat: CallAPI gibt finish_reason zurueck

Signatur erweitert um finish_reason string.
Extraktion aus choices[0].finish_reason (OpenAI)
und stop_reason (Anthropic).

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

### Task 2: ChatChoice erweitern + Integration in main.go

**Files:**
- Modify: `sigoREST/main.go:361-364`
- Modify: `sigoREST/main.go:555-570`
- Modify: `sigoREST/main.go:652-656`

- [ ] **Step 1: `ChatChoice` Struct erweitern**

Ersetze:

```go
type ChatChoice struct {
	Index   int         `json:"index"`
	Message ChatMessage `json:"message"`
}
```

durch:

```go
type ChatChoice struct {
	Index        int         `json:"index"`
	Message      ChatMessage `json:"message"`
	FinishReason string      `json:"finish_reason,omitempty"`
}
```

- [ ] **Step 2: `CallAPI` Aufruf anpassen**

Ersetze:

```go
		var responseText string
		var responseUsage *sigoengine.UsageData

		// Input-Text fuer Fallback-Schaetzung sammeln
```

durch:

```go
		var responseText string
		var responseUsage *sigoengine.UsageData
		var responseFinishReason string

		// Input-Text fuer Fallback-Schaetzung sammeln
```

Ersetze innerhalb des Retry-Blocks:

```go
			text, u, e := sigoengine.CallAPI(ctx, cfg, apiRequest, req.Timeout)
			if e != nil {
				return e
			}
			responseText = text
			responseUsage = u
			if responseUsage == nil {
```

durch:

```go
			text, u, fr, e := sigoengine.CallAPI(ctx, cfg, apiRequest, req.Timeout)
			if e != nil {
				return e
			}
			responseText = text
			responseUsage = u
			responseFinishReason = fr
			if responseUsage == nil {
```

- [ ] **Step 3: `FinishReason` in Response setzen**

Ersetze:

```go
		Choices: []ChatChoice{{
			Index:   0,
			Message: ChatMessage{Role: "assistant", Content: responseText},
		}},
```

durch:

```go
		Choices: []ChatChoice{{
			Index:        0,
			Message:      ChatMessage{Role: "assistant", Content: responseText},
			FinishReason: responseFinishReason,
		}},
```

- [ ] **Step 4: Build pruefen**

Run: `go build ./...`
Expected: Keine Fehler

- [ ] **Step 5: Commit**

```bash
git add sigoREST/main.go
git commit -m "$(cat <<'EOF'
feat: finish_reason in Chat-Response integriert

ChatChoice bekommt FinishReason Feld.
CallAPI liefert finish_reason an Client weiter.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

### Task 3: Manueller Test

**Files:**
- Keine Code-Aenderungen

- [ ] **Step 1: Server bauen und starten**

Run:
```bash
go build -o sigoREST/sigoREST ./sigoREST/
./sigoREST/sigoREST -v debug
```

- [ ] **Step 2: Request senden und finish_reason pruefen**

Run:
```bash
curl -s http://localhost:9080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt41",
    "messages": [{"role": "user", "content": "Hallo"}],
    "max_tokens": 50
  }' | jq '.choices[0].finish_reason'
```

Expected: `"stop"`

- [ ] **Step 3: Request mit max_tokens=1 testen**

Run:
```bash
curl -s http://localhost:9080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt41",
    "messages": [{"role": "user", "content": "Erzaehle eine Geschichte"}],
    "max_tokens": 1
  }' | jq '.choices[0].finish_reason'
```

Expected: `"length"`

---

## Self-Review

**Spec coverage:**
- [x] `finish_reason` Extraktion in `CallAPI` → Task 1
- [x] Anthropic `stop_reason` Fallback → Task 1
- [x] `ChatChoice` Struct Erweiterung → Task 2
- [x] Integration in `main.go` → Task 2
- [x] Manuelle Verifikation → Task 3

**Placeholder scan:** Keine TBD/TODO. Alle Code-Bloecke vollstaendig.

**Type consistency:**
- `CallAPI` Signatur: `(string, *UsageData, string, error)` → konsistent in allen Returns
- `ChatChoice` Feld: `FinishReason string json:"finish_reason,omitempty"` → OpenAI-kompatibel
