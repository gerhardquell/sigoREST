# pi-Extension: sigoREST-Provider

Registriert den lokalen sigoREST-Server als Provider für den
[pi Coding-Agent](https://pi.dev) (`@earendil-works/pi-coding-agent`).

## Funktionsweise

- Nutzt den Anthropic-Endpoint `POST /v1/messages` – anders als
  `/v1/chat/completions` injiziert dieser **kein** Memory, keinen
  System-Prompt und keine Session (pi verwaltet seinen eigenen Kontext).
- Lädt beim Start alle Modelle dynamisch von `GET /api/models`
  (Shortcode als Modell-ID, echte Token-Limits und Preise).
- **Thinking**: `THINKING_MODELS` listet Shortcodes mit Reasoning-Fähigkeit
  (abgeleitet durch exaktes Matching gegen pi's Modellkatalog) mit den
  Katalog-`maxTokens`-Werten. pi sendet `budget_tokens`, sigoREST mappt auf
  `reasoning_effort` (OpenAI-kompatibel), `thinking`-Objekt (ZAI) oder
  `budget_tokens` (Anthropic-Kanäle) und übersetzt Upstream-
  `reasoning_content` in thinking-Blöcke zurück.

## Installation

```bash
# Symlink in pis persönlichen Extensions-Ordner (empfohlen):
ln -sf /u/go-projekte/sigoREST/clients/pi/sigorest.ts \
  ~/.pi/agent/extensions/sigorest.ts

# Oder kopieren (dann manuell bei Änderungen aktualisieren):
cp /u/go-projekte/sigoREST/clients/pi/sigorest.ts ~/.pi/agent/extensions/
```

## Konfiguration

ENV-Variable (z.B. `~/.bashrc`):

```bash
# Platzhalter-Key: sigoREST authentifiziert per IP-Zugriffskontrolle,
# pi verlangt aber einen konfigurierten Key.
export SIGOREST_API_KEY="sigo"

# Optional: andere sigoREST-Instanz (Default http://localhost:9080)
# export SIGOREST_URL="http://localhost:9080"
```

In `~/.pi/agent/settings.json` als Standard-Modell eintragen:

```json
{
  "defaultProvider": "sigorest",
  "defaultModel": "mam-cl46-s"
}
```

## Nutzung

- Modellwahl: `/model sigorest/<shortcode>` (z.B. `sigorest/mam-cl46-s`)
- Thinking: `/thinking high` oder `pi --thinking high`
- Fallback, falls sigoREST komplett ausfällt: eingebauter ZAI-Provider
  (`/model zai/glm-5.3`); Provider-Failover innerhalb sigoRESTs übernimmt
  das Kanal-Routing.

## Wartung

- **Neue sigoREST-Modelle** erscheinen automatisch (dynamischer Abruf),
  aber ohne Thinking – Shortcodes dafür manuell in `THINKING_MODELS`
  nachtragen (Quelle: pi-Modellkatalog, nur Modelle mit `reasoning: true`).
- **ZAI-Modelle**: `maxTokens` wird auf 131072 gecappt (ZAI-Fehlercode 1210,
  erlaubter Bereich `[1, 131072]`), da pi-Katalogwerte höher liegen können.
- Embedding-Modelle werden herausgefiltert (keine Chat-Modelle).
