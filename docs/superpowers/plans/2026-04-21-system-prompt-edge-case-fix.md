# System-Prompt Edge-Case Fix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Verhindern, dass `system_prompt` im Request-Body UND `role: system` im `messages`-Array gleichzeitig zum API-Provider gesendet werden.

**Architecture:** Eine Bedingung in der Message-Loop in `sigoREST/main.go`. Wenn `req.SystemPrompt` gesetzt ist, werden `role: system` Messages aus `req.Messages` übersprungen und eine Warnung geloggt.

**Tech Stack:** Go 1.26, sigoengine Logger

---

### Task 1: Message-Loop in Chat-Handler anpassen

**Files:**
- Modify: `sigoREST/main.go:504-518`

- [ ] **Step 1: Code-Block ersetzen**

Ersetze:

```go
	// User-Messages aus Request (außer system, die kommt von Memory)
	var userPrompt string
	for _, msg := range req.Messages {
		if msg.Role == "system" {
			// User-definierter system-prompt wird NACH memory eingefügt
			messages = append(messages, map[string]interface{}{
				"role": "system", "content": msg.Content,
			})
		} else {
			messages = append(messages, map[string]interface{}{
				"role": msg.Role, "content": msg.Content,
			})
			if msg.Role == "user" {
				userPrompt = msg.Content
			}
		}
	}
```

durch:

```go
	// User-Messages aus Request
	var userPrompt string
	for _, msg := range req.Messages {
		if msg.Role == "system" {
			if req.SystemPrompt != "" {
				sigoengine.LogWarn("Ignoriere role:system in Messages, da system_prompt im Request gesetzt ist", map[string]interface{}{
					"model": req.Model,
				})
				continue
			}
			messages = append(messages, map[string]interface{}{
				"role": "system", "content": msg.Content,
			})
		} else {
			messages = append(messages, map[string]interface{}{
				"role": msg.Role, "content": msg.Content,
			})
			if msg.Role == "user" {
				userPrompt = msg.Content
			}
		}
	}
```

- [ ] **Step 2: Build prüfen**

Run: `go build ./...`
Expected: Keine Fehler

- [ ] **Step 3: Commit**

```bash
git add sigoREST/main.go
git commit -m "$(cat <<'EOF'
fix: doppelten System-Prompt verhindern

Wenn system_prompt im Request-Body gesetzt ist,
werden role:system Messages aus messages[] ignoriert.
Vermeidet unbeabsichtigte doppelte System-Prompts.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

### Task 2: Manueller Test

**Files:**
- Keine Code-Änderungen

- [ ] **Step 1: Server starten**

Run: `./sigoREST/sigoREST -v debug`

- [ ] **Step 2: Request mit doppeltem System-Prompt senden**

Run:
```bash
curl -s http://localhost:9080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "ollama-gemma3",
    "system_prompt": "Du bist ein Ubersetzer.",
    "messages": [
      {"role": "system", "content": "Du bist ein Experte."},
      {"role": "user", "content": "Hallo"}
    ]
  }' | jq '.choices[0].message.content'
```

Expected: Response kommt zurück. Im Server-Log erscheint:
```
WARN: Ignoriere role:system in Messages, da system_prompt im Request gesetzt ist
```

- [ ] **Step 3: Request mit normalem System-Prompt testen**

Run:
```bash
curl -s http://localhost:9080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "ollama-gemma3",
    "messages": [
      {"role": "system", "content": "Du bist ein Experte."},
      {"role": "user", "content": "Hallo"}
    ]
  }' | jq '.choices[0].message.content'
```

Expected: Response kommt zurück. Keine WARN im Log (da kein `system_prompt` Feld gesetzt).

---

## Self-Review

**Spec coverage:**
- [x] Edge-Case-Fix: doppelter System-Prompt verhindert → Task 1
- [x] Warn-Log bei Ignorierung → Task 1, Step 1
- [x] Manuelle Verifikation → Task 2

**Placeholder scan:** Keine TBD/TODO. Alle Code-Blöcke vollständig. Alle Commands exakt.

**Type consistency:** `sigoengine.LogWarn` Signatur stimmt mit `engine.go:339` überein.
