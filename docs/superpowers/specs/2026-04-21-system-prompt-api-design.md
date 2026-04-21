# Design: System-Prompt API & Per-Request Override

## Status

Retrospektives Design-Dokument. Feature bereits implementiert in `sigoREST/main.go`. Dieses Dokument dient als Referenz und beschreibt eine beabsichtigte Code-Bereinigung (Edge-Case: doppelter System-Prompt).

## Ziel

System-Prompt über API lesbar und schreibbar machen. Globaler Default + optionaler Per-Request-Override.

## API-Spezifikation

### Globaler System-Prompt

| Methode | Endpoint | Body | Response |
|---------|----------|------|----------|
| `GET` | `/api/system-prompt` | — | `{"system_prompt": "<aktueller Wert>"}` |
| `PUT` | `/api/system-prompt` | `{"system_prompt": "..."}` | `{"status": "ok", "system_prompt": "..."}` |

### Per-Request Override

`POST /v1/chat/completions` akzeptiert zusätzlich zum OpenAI-Standard:

```json
{
  "model": "claude-h",
  "messages": [{"role": "user", "content": "Hallo"}],
  "system_prompt": "Antworte immer auf Deutsch."
}
```

**Priorität (höchste zuerst):**
1. `system_prompt` im Request-Body
2. Globaler System-Prompt (`/api/system-prompt`)
3. Kein System-Prompt

## Architektur & Datenfluss

```
ChatRequest.SystemPrompt
       |
       v (wenn nicht leer)
effectiveSystemPrompt = req.SystemPrompt
       |
       +-- (sonst) --> effectiveSystemPrompt = s.systemPrompt (global)
       |
       v
Als erste Message eingefugt:
  {role: "system", content: effectiveSystemPrompt}
       |
       v
Dann User-Messages aus req.Messages
```

**Wichtig:** `effectiveSystemPrompt` wird VOR allen User-Messages eingefugt. Das ist beabsichtigt — System-Prompt soll Kontext fur die gesamte Konversation setzen.

## Thread-Safety & Persistenz

- `s.systemPrompt` wird durch `s.mu` (`sync.RWMutex`) geschutzt:
  - `GET /api/system-prompt`: `RLock()` / `RUnlock()`
  - `PUT /api/system-prompt`: `Lock()` / `Unlock()`
- Bei PUT wird zusatzlich auf Disk geschrieben: `./system-prompt.txt`
- Beim Serverstart wird `loadSystemPrompt()` aufgerufen, das `system-prompt.txt` liest (falls vorhanden)
- Disk hat Vorrang vor eingebettetem Default

## Edge-Case Fix: Doppelter System-Prompt

### Problem (aktueller Stand)

Der Chat-Handler fuhrt aktuell beide Quellen unabhangig zusammen:

1. `effectiveSystemPrompt` aus Request-Feld oder global → wird als `role: system` eingefugt
2. Alle Messages mit `role: system` aus `req.Messages` → werden ebenfalls eingefugt

**Ergebnis:** Ein Client kann unbeabsichtigt zwei System-Prompts senden:

```json
{
  "system_prompt": "Du bist ein Ubersetzer.",
  "messages": [
    {"role": "system", "content": "Du bist ein Experte."},
    {"role": "user", "content": "Hallo"}
  ]
}
```

Das fuhrt zu:
```
[system: "Du bist ein Ubersetzer.", system: "Du bist ein Experte.", user: "Hallo"]
```

### Loesung

Wenn `req.SystemPrompt` gesetzt ist (nicht leer), werden `role: system` Messages aus `req.Messages` ignoriert. Stattdessen wird eine Warnung ins Log geschrieben:

```
Warn: Ignoriere role:system in Messages, da system_prompt im Request gesetzt ist
```

Wenn `req.SystemPrompt` leer ist, verhalten sich `role: system` Messages wie bisher — sie uberschreiben implizit den globalen Default.

### Code-Change

In `sigoREST/main.go`, Chat-Handler, Message-Loop:

```go
for _, msg := range req.Messages {
    if msg.Role == "system" {
        if req.SystemPrompt != "" {
            sigoengine.LogWarn("Ignoriere role:system in Messages", ...)
            continue
        }
        messages = append(messages, map[string]interface{}{
            "role": "system", "content": msg.Content,
        })
    } else {
        // ... wie bisher
    }
}
```

## Offene Punkte

- [ ] Code-Change implementieren (siehe Loesung oben)
- [ ] Manuell testen: Request mit `system_prompt` + `role: system` in Messages

## Aenderungshistorie

| Datum | Autor | Aenderung |
|-------|-------|-----------|
| 2026-04-21 | kimi | Initiales retrospektives Design-Dokument + Edge-Case-Fix |
