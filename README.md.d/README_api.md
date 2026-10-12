# API-Referenz

[← README](../README.md)

Alle Beispiele gegen `http://localhost:9080`. Gültige Modell-Kürzel liefert
`GET /api/shortcodes`; die Beispiele nutzen Stand 2026-10-12.

## Übersicht

| Pfad | Methode | Zweck |
|-------|---------|-------|
| `/v1/chat/completions` | POST | OpenAI-kompatible Chat API |
| `/v1/messages` | POST | Anthropic-Messages-API-Bridge (Claude Code o.ä.), Tool-Calling, alle Provider |
| `/v1/models` | GET | Modell-Liste (ID + Shortcode), `owned_by` = kanonischer Provider |
| `/v1/embeddings` | POST | OpenAI-kompatible Embeddings (Proxy auf Ollama, läuft ohne Kanal) |
| `/api/models` | GET | Volle Modell-Infos (Preise, Limits, `provider`/`provider_code`); `?format=csv` → Registry-CSV |
| `/api/shortcodes` | GET | Kompaktes Mapping `{id: shortcode}` (nach ID sortiert) |
| `/api/shortlist` | GET | Shortcode + Provider + 5-Zeichen-Code, sortiert |
| `/api/channels` | GET | Status aller Kanäle (inkl. `min_interval_ms`/`max_wait_ms`, `manually_disabled`) |
| `/api/channels/:provider/:name` | GET | Einzelkanal-Detail |
| `/api/channels/:provider/:name/enable` | POST | Kanal aktivieren |
| `/api/channels/:provider/:name/disable` | POST | Kanal manuell deaktivieren |
| `/api/channels/:provider/:name/memory` | GET/PUT | Kanal-spezifischer Memory-Block |
| `/api/channels/:provider/:name/system-prompt` | GET/PUT | Kanal-spezifischer System-Prompt |
| `/api/health` | GET | Server-Status + Circuit-Breaker |
| `/api/memory` | GET/PUT | Globaler Memory-Block |
| `/api/system-prompt` | GET/PUT | Globaler System-Prompt |
| `/api/usage` | GET | Token-Statistiken (RAM, Reset bei Neustart) |
| `/api/costs` | GET | Token-Statistiken + USD-Kosten, persistent (`costs.db`) |
| `/api/budget` | GET/PUT | Tages-/Monats-Budget + Verbrauchsstatus, optional Hard-Stop |
| `/api/version` | GET | Version + Component-Name |
| `/api/help` | GET | Endpoint-Dokumentation als JSON |
| `/ping` | GET | Load-Balancer Health-Check |

`/api/usage`, `/api/costs` und `/api/budget` sind in
[Kosten & Budget](README_kosten-budget.md) beschrieben, die Kanal-Endpoints
zusätzlich in [Kanäle](README_kanaele.md).

## POST /v1/chat/completions

```bash
curl -s http://localhost:9080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "mam-cl46-s",
    "channel": "mammouth-0",
    "messages": [{"role": "user", "content": "Hallo"}],
    "temperature": 0.7,
    "max_tokens": 1024,
    "session_id": "mein-projekt",
    "timeout": 120,
    "retries": 3,
    "system_prompt": "Optional: überschreibt globale + kanal-spezifische System-Prompts"
  }'
```

### sigoREST-Erweiterungen im Request

| Feld | Bedeutung |
|------|-----------|
| `channel` | Kanal-FullName (z.B. `mammouth-0`). Fehlt er, wird der erste aktive Kanal verwendet. |
| `session_id` | Session-ID für isolierten Gesprächsverlauf pro Kanal |
| `timeout` | Request-Timeout in Sekunden (Default 180) |
| `retries` | Wiederholungsversuche pro Kanal (Default 3) |
| `system_prompt` | Per-Request System-Prompt, höchste Priorität; bei `bare` der einzige Kontext |
| `bare` | `true`: weder globaler/Kanal-Memory noch Server-System-Prompt; nur ein nicht-leerer `system_prompt` zählt |
| `response_format` | Standard-OpenAI, z.B. `{"type": "json_object"}` |

- Ein älteres sigoREST ohne `bare`-Unterstützung ignoriert das Feld (Memory
  und Server-Prompt kommen dann wieder dazu).
- `response_format` wird 1:1 im Request-Body an den Provider durchgereicht,
  nicht validiert — ein ungültiger Wert wird vom Provider abgelehnt. Gilt nur
  für `/v1/chat/completions`; das Anthropic-Wire-Format von `/v1/messages`
  hat kein Äquivalent.

### Modellwahl

`model` akzeptiert ID, Shortcode oder Shortcode mit Kanal-Suffix
(`zai-glm45-2` → Modell `zai-glm45`, Kanal `2`). Sind Suffix und `channel`
beide gesetzt, gewinnt `channel`. Ist das Modell **retired** (vom Provider
dauerhaft verschwunden, siehe [Shortcode-Registry](README_shortcode-registry.md)),
antwortet der Server mit `HTTP 410` und `"model_retired"` statt eines
stillen Fallbacks auf ein anderes Modell.

### Response: `usage`

```json
{
  "choices": [...],
  "usage": {
    "prompt_tokens": 42,
    "completion_tokens": 18,
    "total_tokens": 60,
    "prompt_tokens_details": { "cached_tokens": 0 },
    "completion_tokens_details": { "reasoning_tokens": 0 },
    "cost_usd": 0.0
  }
}
```

`usage` ist gefüllt, sofern der Provider Token-Daten liefert.
`prompt_tokens_details`, `completion_tokens_details` und `cost_usd` sind
sigoREST-Erweiterungen. `cost_usd` ist `null`, wenn für das Modell kein Preis
bekannt ist; bei Streaming kommt `usage` als eigener Chunk vor `[DONE]`.
Berechnung und Cache-Rabatt: [Kosten & Budget](README_kosten-budget.md).

### Vision

sigoREST unterstützt das OpenAI-Vision-Format. Bilder gehen als
Base64-Daten-URLs:

```bash
curl -s http://localhost:9080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "mam-gpt4o",
    "messages": [{
      "role": "user",
      "content": [
        {"type": "text", "text": "Was siehst du auf diesem Bild?"},
        {"type": "image_url", "image_url": {
          "url": "data:image/jpeg;base64,/9j/4AAQ..."
        }}
      ]
    }],
    "max_tokens": 4096
  }'
```

- `ChatMessage.Content` ist `json.RawMessage` — Passthrough für String und
  Vision-Array-Format
- Session-Speicherung extrahiert nur Text (keine Bilddaten in Sessions)
- Empfohlen: JPEG mit Quality 75 bei ~100 DPI (ca. 80 KB pro Seite)
- Zu große Bilder (PNG 200+ DPI, >1 MB) können Proxy-Fehler (413) verursachen

## POST /v1/messages (Anthropic-Bridge)

Übersetzt das Anthropic-Messages-Wire-Format (Request, Response,
SSE-Streaming, Tool-Calling) auf dieselbe interne Engine wie
`/v1/chat/completions`. Damit ist jedes sigoREST-Modell — unabhängig vom
Provider-Wire-Format — über `ANTHROPIC_BASE_URL` erreichbar, z.B. für Claude
Code:

```bash
unset ANTHROPIC_API_KEY
export ANTHROPIC_BASE_URL="http://localhost:9080"
export ANTHROPIC_AUTH_TOKEN="unused"   # wird ignoriert, claude-CLI verlangt aber einen Wert
export ANTHROPIC_MODEL="mam-cl46-s"
claude "Hallo"
```

- Unterstützt Tool-Calling, SSE-Streaming und Extended Thinking.
- Modellwahl 1:1 wie bei `/v1/chat/completions` (ID/Shortcode, kein Alias).
- Kein separater Auth-Mechanismus (gleiche IP-Zugriffskontrolle wie alle
  anderen Endpunkte).
- Keine Memory-/System-Prompt-/Session-Injektion — der Client verwaltet
  seinen eigenen Kontext.
- Fehler-Antworten im Anthropic-Format
  (`{"type":"error","error":{"type":"...","message":"..."}}`), nicht im
  OpenAI-Format. Budget-Stopp → `402 billing_error`.
- `usage` (Input-/Output-Tokens, inkl. `cached_tokens`) wird auch bei
  Streaming aus den echten Provider-Daten befüllt, nicht geschätzt.

**Thinking:** Der Anthropic-`thinking`-Parameter
(`{"type":"enabled","budget_tokens":N}`) wird pro Kanaltyp übersetzt —
native `budget_tokens`-Weiterleitung für Anthropic-Kanäle, `thinking`-Objekt
für ZAI (GLM-4.5+) und `reasoning_effort` (low/medium/high, aus dem Budget
gemappt) für alle anderen OpenAI-kompatiblen Provider. Umgekehrt wird
Upstream-`reasoning_content` im Streaming in Anthropic-`thinking`-Blöcke
übersetzt (vor dem Text-Block, wie Anthropic-Clients es erwarten);
`thinking`-Blöcke im Request werden ignoriert. Nicht-gestreamte Antworten
liefern kein Thinking zurück (`CallAPI` extrahiert keinen `reasoning_content`).

Design: `docs/superpowers/specs/2026-09-12-anthropic-messages-bridge-design.md`.

## GET /v1/models

```bash
curl -s http://localhost:9080/v1/models | jq '.data[].id'
```

OpenAI-kompatible Modell-Liste (ID + Shortcode). `owned_by` ist der
kanonische Provider-Name (z.B. `"mammouth"`, `"zai"`, `"ollama"`) — so ist
der Provider für jeden Client (auch Hermes) ohne Zusatz-Call sichtbar, analog
zu OpenRouters Konvention.

## GET /api/models

```bash
curl -s http://localhost:9080/api/models
```

Volle Modell-Infos: Preise, Token-Limits, Temperatur-Range — inkl.
`provider` (kanonischer Name) und `provider_code` (5-Zeichen-Code, siehe
[Provider-Kennzeichnung](README_provider-modelle.md#provider-kennzeichnung-sigoengineprovider_idgo)).

Als CSV (Semikolon, sortiert nach Provider + Shortcode):

```bash
curl -s 'http://localhost:9080/api/models?format=csv' > models.csv
```

Die ersten 11 Spalten entsprechen dem Registry-Format der CLI, danach
`provider;provider_code;upstream_id;cached_input_cost`. Die Kopfzeile beginnt
mit `#` (Kommentar für den Parser) — die Datei ist damit direkt als
`models.csv` für `sigoE` verwendbar und bringt die Live-Shortcodes des
Servers mit. `apikey` enthält nur den Namen der ENV-Variable, nie den Key.
Unbekanntes `format` → HTTP 400. Format-Details:
[CSV/Registry-Format](README_provider-modelle.md#csvregistry-format-cli).

## GET /api/shortcodes

```bash
curl -s http://localhost:9080/api/shortcodes
```

Kompaktes Modell→Shortcode-Mapping, nach ID sortiert:
`{"gpt-4.1": "mam-gpt41", ...}`.

## GET /api/shortlist

```bash
curl -s http://localhost:9080/api/shortlist
```

Die schnelle Antwort auf "welcher Provider steckt hinter diesem Shortcode?" —
pro Modell nur `shortcode`, `provider` und Code, sortiert nach Provider, dann
Shortcode:

```json
[
  {"shortcode": "mam-cl46-s", "provider": "mammouth", "code": "mammo"},
  {"shortcode": "moo-kimik3", "provider": "moonshot", "code": "moons"},
  {"shortcode": "zai-glm51", "provider": "zai", "code": "zai__"}
]
```

## Kanäle

```bash
# Liste aller Kanäle mit Status
curl -s http://localhost:9080/api/channels

# Detail-Status eines Kanals
curl -s http://localhost:9080/api/channels/mammouth/0

# Aktivieren / deaktivieren
curl -s -X POST http://localhost:9080/api/channels/mammouth/0/enable
curl -s -X POST http://localhost:9080/api/channels/mammouth/0/disable

# Kanal-Memory lesen / setzen
curl -s http://localhost:9080/api/channels/mammouth/0/memory
curl -s -X PUT http://localhost:9080/api/channels/mammouth/0/memory \
  -H "Content-Type: application/json" \
  -d '{"content":"Kanal-spezifischer Kontext","cache":false}'

# Kanal-System-Prompt lesen / setzen
curl -s http://localhost:9080/api/channels/mammouth/0/system-prompt
curl -s -X PUT http://localhost:9080/api/channels/mammouth/0/system-prompt \
  -H "Content-Type: application/json" \
  -d '{"system_prompt":"Antworte wie ein Pirat."}'
```

Ein per API deaktivierter Kanal gilt als **manuell deaktiviert**
(`manually_disabled: true`) und wird vom Health-Monitor nie wieder
aktiviert, auch nach einem Neustart nicht; nur `/enable` hebt das auf.
Details: [Kanäle](README_kanaele.md#manuelles-deaktivieren-hat-vorrang).

## Globaler Memory & System-Prompt

```bash
# Memory lesen / zur Laufzeit ändern (wird auf Disk geschrieben)
curl -s http://localhost:9080/api/memory
curl -s -X PUT http://localhost:9080/api/memory \
  -H "Content-Type: application/json" \
  -d '{"content":"Neuer Kontext","cache":true}'

# System-Prompt lesen / setzen (gespeichert in system-prompt.txt)
curl -s http://localhost:9080/api/system-prompt
curl -s -X PUT http://localhost:9080/api/system-prompt \
  -H "Content-Type: application/json" \
  -d '{"system_prompt":"Du bist ein hilfreicher Assistent."}'
```

Der globale System-Prompt kann per Kanal oder per Request überschrieben
werden.

## Status

```bash
curl -s http://localhost:9080/ping            # "pong" — für Load Balancer
curl -s http://localhost:9080/api/health      # Server-Status, Anzahl Modelle, Circuit Breaker pro Kanal/Modell
curl -s http://localhost:9080/api/version     # {"version":"1.1","component":"sigoREST"}
curl -s http://localhost:9080/api/help        # Dokumentation aller Endpunkte als JSON
```
