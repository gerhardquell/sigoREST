# Usage-Feld robust extrahieren + Fallback Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `usage` Feld in `POST /v1/chat/completions` Response nie mehr `null`.

**Architecture:** `extractUsage` um zusatzliche Provider-Formate erweitern. Neue `EstimateUsage` Funktion als Fallback. Integration in `sigoREST/main.go` nach `CallAPI`.

**Tech Stack:** Go 1.26

---

### Task 1: `extractUsage` erweitern + `EstimateUsage` hinzufugen

**Files:**
- Modify: `sigoengine/engine.go:1237-1262`

- [ ] **Step 1: `extractUsage` ersetzen**

Ersetze:

```go
// extractUsage liest Token-Verbrauch aus Provider-Response
func extractUsage(result map[string]interface{}, providerType string) *UsageData {
	u, ok := result["usage"].(map[string]interface{})
	if !ok {
		return nil
	}
	usage := &UsageData{}
	toInt := func(v interface{}) int {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		}
		return 0
	}
	if providerType == "anthropic" {
		usage.InputTokens = toInt(u["input_tokens"])
		usage.OutputTokens = toInt(u["output_tokens"])
	} else {
		usage.InputTokens = toInt(u["prompt_tokens"])
		usage.OutputTokens = toInt(u["completion_tokens"])
	}
	usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	return usage
}
```

durch:

```go
// extractUsage liest Token-Verbrauch aus Provider-Response
func extractUsage(result map[string]interface{}, providerType string) *UsageData {
	u, ok := result["usage"].(map[string]interface{})
	if !ok {
		return nil
	}
	usage := &UsageData{}
	toInt := func(v interface{}) int {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		}
		return 0
	}

	// Versuche provider-spezifische Feldnamen
	if providerType == "anthropic" {
		usage.InputTokens = toInt(u["input_tokens"])
		usage.OutputTokens = toInt(u["output_tokens"])
	} else {
		usage.InputTokens = toInt(u["prompt_tokens"])
		usage.OutputTokens = toInt(u["completion_tokens"])
	}

	// Fallback fur Gemini-Format
	if usage.InputTokens == 0 {
		usage.InputTokens = toInt(u["promptTokenCount"])
	}
	if usage.OutputTokens == 0 {
		usage.OutputTokens = toInt(u["candidatesTokenCount"])
	}

	// Wenn Input/Output immer noch 0, versuche total_tokens direkt
	if usage.InputTokens == 0 && usage.OutputTokens == 0 {
		usage.TotalTokens = toInt(u["total_tokens"])
	} else {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	}

	return usage
}
```

- [ ] **Step 2: `EstimateUsage` nach `extractUsage` einfugen**

Fuge nach `extractUsage` (Zeile 1262) ein:

```go
// EstimateUsage schatzt Token-Verbrauch heuristisch
// Konservative Naherung: 3 Zeichen ~ 1 Token (westliche Sprachen)
func EstimateUsage(inputText, outputText string) *UsageData {
	inputTokens := len([]rune(inputText)) / 3
	if inputTokens < 1 {
		inputTokens = 1
	}
	outputTokens := len([]rune(outputText)) / 3
	if outputTokens < 1 {
		outputTokens = 1
	}
	return &UsageData{
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		TotalTokens:  inputTokens + outputTokens,
	}
}
```

- [ ] **Step 3: Build prufen**

Run: `go build ./...`
Expected: Keine Fehler

- [ ] **Step 4: Commit**

```bash
git add sigoengine/engine.go
git commit -m "$(cat <<'EOF'
feat: Usage-Extraktion erweitert + EstimateUsage Fallback

extractUsage unterstutzt jetzt Gemini-Format und total_tokens Fallback.
Neue EstimateUsage Funktion fur heuristische Token-Schatzung.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

### Task 2: Fallback-Integration in sigoREST/main.go

**Files:**
- Modify: `sigoREST/main.go:562-572`
- Modify: `sigoREST/main.go:625-644`

- [ ] **Step 1: Input-Text zusammenbauen vor CallAPI**

Fuge VOR dem Retry-Block (Zeile 562) ein:

```go
	// Input-Text fur Fallback-Schatzung sammeln
	var inputBuilder strings.Builder
	for _, msg := range req.Messages {
		inputBuilder.WriteString(msg.Content)
	}
	inputText := inputBuilder.String()
```

- [ ] **Step 2: Fallback nach CallAPI einfugen**

Ersetze innerhalb des Retry-Blocks (Zeile 564-570):

```go
			text, u, e := sigoengine.CallAPI(ctx, cfg, apiRequest, req.Timeout)
			if e != nil {
				return e
			}
			responseText = text
			responseUsage = u
			return nil
```

durch:

```go
			text, u, e := sigoengine.CallAPI(ctx, cfg, apiRequest, req.Timeout)
			if e != nil {
				return e
			}
			responseText = text
			responseUsage = u
			if responseUsage == nil {
				responseUsage = sigoengine.EstimateUsage(inputText, responseText)
				sigoengine.LogDebug("Usage geschatzt", map[string]interface{}{
					"model":        req.Model,
					"input_tokens":  responseUsage.InputTokens,
					"output_tokens": responseUsage.OutputTokens,
				})
			}
			return nil
```

- [ ] **Step 3: `strings` Import prufen**

Stelle sicher, dass `strings` in `sigoREST/main.go` importiert ist. Falls nicht, fuge es zu den Imports hinzu.

- [ ] **Step 4: Build prufen**

Run: `go build ./...`
Expected: Keine Fehler

- [ ] **Step 5: Commit**

```bash
git add sigoREST/main.go
git commit -m "$(cat <<'EOF'
feat: Usage-Fallback in Chat-Handler integriert

Wenn Provider kein usage liefert, wird EstimateUsage als Fallback
verwendet. usage Feld in Response ist nie mehr null.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

### Task 3: Manueller Test

**Files:**
- Keine Code-Anderungen

- [ ] **Step 1: Server bauen und starten**

Run:
```bash
go build -o sigoREST/sigoREST ./sigoREST/
./sigoREST/sigoREST -v debug
```

- [ ] **Step 2: Request senden und usage prufen**

Run:
```bash
curl -s http://localhost:9080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt41",
    "messages": [{"role": "user", "content": "Hallo Welt, das ist ein Test."}]
  }' | jq '.usage'
```

Expected: `usage` Feld ist vorhanden mit `prompt_tokens`, `completion_tokens`, `total_tokens` > 0.

- [ ] **Step 3: Zweiten Request mit anderem Modell testen**

Run:
```bash
curl -s http://localhost:9080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-h",
    "messages": [{"role": "user", "content": "Erklare Go in einem Satz."}]
  }' | jq '.usage'
```

Expected: Gleiches — `usage` Feld immer vorhanden.

---

## Self-Review

**Spec coverage:**
- [x] `extractUsage` erweitert (Gemini, total_tokens) → Task 1
- [x] `EstimateUsage` neu → Task 1
- [x] Fallback in main.go → Task 2
- [x] Manuelle Verifikation → Task 3

**Placeholder scan:** Keine TBD/TODO. Alle Code-Blocke vollstandig.

**Type consistency:**
- `UsageData` Felder: `InputTokens int`, `OutputTokens int`, `TotalTokens int` → stimmt mit `engine.go` uberein
- `ChatUsage` Felder: `PromptTokens int`, `CompletionTokens int`, `TotalTokens int` → stimmt mit `main.go` uberein
