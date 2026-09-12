# Design: Anthropic-Messages-Bridge für sigoREST (Claude-Code-Anbindung)

**Datum:** 2026-09-12
**Thema:** Neuer `/v1/messages`-Endpoint (Anthropic Messages API) mit Tool-Calling, damit Claude Code über sigoREST läuft — gegen jedes sigoREST-Modell, nicht nur Anthropic-native Provider
**Status:** Vom Benutzer validiert, bereit für Implementierungsplanung
**Version:** 1.0

## Zusammenfassung

sigoREST bekommt einen zweiten, zum bestehenden `/v1/chat/completions` parallelen Client-Endpoint: `POST /v1/messages`, der das Anthropic-Messages-Wire-Format spricht (Request- und Response-Schema, SSE-Event-Format). Claude Code (und jeder andere Anthropic-API-kompatible Client) kann damit `ANTHROPIC_BASE_URL` auf sigoREST zeigen lassen und dabei **jedes** in sigoREST konfigurierte Modell nutzen (Mammouth, Moonshot, ZAI, Longcat, cheaperinference) — unabhängig davon, ob der jeweilige Provider selbst OpenAI- oder Anthropic-Wire-Format spricht.

Der Endpoint ist ein reiner Übersetzer vor der bestehenden Engine: Channel-Resolution, Failover, Rate-Limiting, Circuit-Breaker und Health-Monitor werden 1:1 mitbenutzt. Die sigoREST-eigenen Zusatzfeatures (globaler/Kanal-Memory-Block, System-Prompt-Override-Kette, `.sessions`-Persistenz) werden **nicht** in diesen Pfad eingebaut — Claude Code verwaltet seine Konversationshistorie selbst und schickt sie komplett bei jedem Request mit.

Voraussetzung, die als Nebeneffekt entsteht: `sigoengine.CallAPI` bekommt generisches Tool-Calling (aktuell komplett fehlend), wovon künftig auch `/v1/chat/completions`-Clients profitieren könnten (nicht Teil dieses Specs, aber technisch freigelegt).

## Motivation

- Gerhard nutzt Claude Code aktuell über ein Script (`claude-clomni`), das `ANTHROPIC_BASE_URL` direkt auf cheaperinference zeigen lässt — ohne sigoRESTs Failover, Rate-Limiting, Health-Monitor und Usage-Tracking.
- Ziel: dieselbe Claude-Code-Nutzung, aber über sigoREST geroutet, mit Zugriff auf beliebige sigoREST-Modelle (nicht nur die Anthropic-Modelle bei cheaperinference).
- Claude Code ist ohne Tool-Calling faktisch nicht nutzbar (Dateizugriff, Bash, etc. laufen alle über Tools) — Tool-Calling ist deshalb Teil des ersten Wurfs, nicht nachgelagert.

## Anforderungen

1. **Neuer Endpoint `POST /v1/messages`** im Anthropic-Messages-Schema (Request + Response + SSE-Streaming).
2. **Modellwahl:** `model`-Feld wird 1:1 wie bei `/v1/chat/completions` aufgelöst (ID oder Shortcode, case-insensitiv) — keine separate Alias-Tabelle. Gerhards Script setzt künftig z.B. `ANTHROPIC_MODEL=ci-claude-opus-5` statt eines rohen Anthropic-Modellnamens.
3. **Tool-Calling** vollständig: `tools`/`tool_choice` im Request, `tool_use`-Content-Blocks in der Response, `tool_result`-Blocks in nachfolgenden Requests.
4. **Streaming ist Pflicht** (Claude Code fordert es praktisch immer an) — echte Anthropic-SSE-Event-Sequenz, kein Rohdurchreichen von OpenAI-Chunks.
5. **Alle bestehenden Provider erreichbar**, inkl. solcher, die selbst kein natives Anthropic-Format sprechen (Mammouth/Moonshot/ZAI/Longcat/cheaperinference laufen alle intern über OpenAI-Chat-Completions-Wire-Format).
6. **Kein neuer Auth-Mechanismus** — der Endpoint hängt am selben `http.ServeMux`/IP-Zugriffskontrolle wie alle anderen Endpoints. Der von Claude Code gesendete `x-api-key`/`ANTHROPIC_AUTH_TOKEN`-Header wird ignoriert (wie bisher bei `/v1/chat/completions` üblich).
7. **Keine Memory-/System-Prompt-/Session-Injektion** in diesem Pfad — Claude Code schickt seinen eigenen vollständigen Kontext.
8. **Out of scope für v1** (bewusst zurückgestellt): Extended-Thinking-Blocks (`thinking`-Content-Type), Bild-Content-Blocks, Prompt-Caching-Hints (`cache_control`). Requests mit diesen Feldern sollen nicht crashen, sondern die Felder best-effort ignorieren/droppen.

## Architektur

```text
┌─────────────────────────────────────────────────────────────┐
│                      Claude Code (CLI)                       │
└──────────────────────┬────────────────────────────────────────┘
                        │ POST /v1/messages  (Anthropic-Schema)
                        ▼
┌─────────────────────────────────────────────────────────────┐
│                      sigoREST Server                         │
│  ┌────────────────────────────────────────────────────────┐  │
│  │  handleMessages (neu, sigoREST/anthropic.go)            │  │
│  │  1. anthropicRequestToInternal()  → apiRequest-Map      │  │
│  │  2. Channel-Resolve + Failover-Liste (wie chat/compl.)  │  │
│  │  3. Rate-Limiter + Circuit-Breaker (wie chat/compl.)    │  │
│  │  4a. Non-Stream: CallAPI() → internalToAnthropicResp()  │  │
│  │  4b. Stream: CallAPIStream() → streamAnthropicResponse()│  │
│  └────────────────────────────────────────────────────────┘  │
│                        │ apiRequest (map[string]interface{})  │
│                        ▼                                      │
│              sigoengine.CallAPI / CallAPIStream               │
│         (unverändert genutzt, wie von /v1/chat/completions)   │
└─────────────────────────────────────────────────────────────┘
```

Der neue Handler dupliziert bewusst NICHT die Memory-/System-Prompt-/Session-Logik aus `handleChatCompletions` — er baut direkt aus dem Anthropic-Request die `messages`/`tools`-Struktur und ruft dieselbe Channel-Resolution + Failover-Schleife wie der bestehende Handler auf (Code dafür wird in eine gemeinsame kleine Hilfsfunktion ausgelagert, um Duplikation zwischen den beiden Handlern zu vermeiden — siehe „Code-Organisation").

## Engine-Erweiterung: Tool-Calls aus der Response

`sigoengine.CallAPI` gibt aktuell `(string, *UsageData, string, error)` zurück (Text, Usage, FinishReason, Fehler) und **verwirft** `message.tool_calls` aus der OpenAI-Response vollständig. Das muss erweitert werden:

```go
type ToolCall struct {
    ID       string
    Type     string // "function"
    Function struct {
        Name      string
        Arguments string // roher JSON-String, wie vom Provider geliefert
    }
}

func CallAPI(ctx context.Context, cfg *ProviderConfig, request map[string]interface{},
    timeoutSec int) (text string, usage *UsageData, finishReason string, toolCalls []ToolCall, err error)
```

Beide bestehenden Call-Sites (`sigoREST/main.go:845`, `cmd/sigoE/main.go:159`) werden auf die neue Signatur angepasst (zusätzlicher Rückgabewert, an den bestehenden Stellen mit `_` ignoriert oder — optional, außerhalb dieses Specs — künftig auch im `/v1/chat/completions`-Response exponiert).

**Anthropic-Format-Parsing** (für den Fall, dass ein Provider tatsächlich natives Anthropic-Format zurückgibt, `cfg.Type == "anthropic"`): `content`-Array kann bereits `tool_use`-Blocks enthalten — die werden ebenfalls in `[]ToolCall` normalisiert, damit die Response-Übersetzung für beide Provider-Familien denselben internen Typ nutzt.

## Request-Übersetzung (Anthropic → intern)

| Anthropic-Feld | Ziel |
|---|---|
| `model` | Modell-Lookup wie bei `/v1/chat/completions` (ID/Shortcode) |
| `system` (String oder Block-Array) | Vorangestellte `{"role":"system","content":...}`-Message |
| `messages[].content` (String oder Block-Array mit `text`/`tool_use`/`tool_result`) | Auf internes `messages`-Array gemappt; `tool_use`-Blocks (in einer Assistant-Message) → `tool_calls`-Feld dieser Message (OpenAI-Schema); `tool_result`-Blocks (bei Anthropic eingebettet in einer User-Message) werden herausgelöst und je Block zu einer eigenen `role:"tool"`-Message mit `tool_call_id = tool_use_id` und `content` = Block-Inhalt (String oder verkettete Text-Teile bei Block-Array) |
| `tools[].{name,description,input_schema}` | `{"type":"function","function":{"name","description","parameters":input_schema}}` |
| `tool_choice` | `{type:"auto"}→"auto"`, `{type:"any"}→"required"`, `{type:"tool",name}→{"type":"function","function":{"name"}}`, `{type:"none"}→"none"` |
| `max_tokens` (Pflichtfeld bei Anthropic) | `max_tokens` bzw. `max_completion_tokens` (wie bestehende `RequiresCompletionTokens`-Logik) |
| `temperature`, `top_p`, `stop_sequences` | direkt durchgereicht (`stop_sequences→stop`) |
| `stream` | steuert non-stream- vs. Stream-Pfad |
| `thinking`, Bild-Blocks, `cache_control` | ignoriert (siehe „Out of scope") |

## Response-Übersetzung (intern → Anthropic)

Nicht-Streaming:

```json
{
  "id": "msg_...",
  "type": "message",
  "role": "assistant",
  "model": "<angefragtes model>",
  "content": [
    {"type": "text", "text": "..."},
    {"type": "tool_use", "id": "toolu_...", "name": "...", "input": {...}}
  ],
  "stop_reason": "end_turn|max_tokens|tool_use|stop_sequence",
  "usage": {"input_tokens": N, "output_tokens": N}
}
```

`finish_reason`-Mapping: `stop→end_turn`, `length→max_tokens`, `tool_calls→tool_use`, sonst `end_turn` als sicherer Default. `tool_calls[].function.arguments` (JSON-String) wird zu `input` (Objekt) geparst; Parse-Fehler → leeres Objekt + Warn-Log statt Absturz.

## Streaming-Übersetzung

`CallAPIStream` liefert weiterhin den rohen OpenAI-SSE-Body. Neue Funktion `streamAnthropicResponse` (Pendant zu `streamProviderResponse`) liest diese Chunks und baut daraus die Anthropic-Event-Sequenz:

1. `event: message_start` (sobald der erste Chunk da ist, mit leerem `content`)
2. Für Text-Deltas: `content_block_start` (type text, einmalig) → laufend `content_block_delta` (`text_delta`)
3. Für Tool-Call-Deltas: OpenAI liefert `tool_calls[].function.arguments` inkrementell als JSON-String-Fragmente, korreliert über `tool_calls[].index`. Pro neuem `index` → eigener `content_block_start` (type `tool_use`, `name` aus dem ersten Fragment mit `function.name`), danach `content_block_delta` (`input_json_delta`, `partial_json` = das rohe Fragment unverändert durchgereicht — Anthropic erwartet ohnehin akkumulierbare JSON-Fragmente, keine Neu-Serialisierung nötig)
4. Am Ende jedes offenen Blocks: `content_block_stop`
5. `message_delta` mit `stop_reason` (aus dem letzten `finish_reason`-Chunk) + finalem `usage`
6. `message_stop`

Verworfene/Parse-Fehler-Zeilen werden übersprungen (Log-Warn), nicht als harter Fehler behandelt — Streaming darf nicht mitten im Response abbrechen, nur weil ein einzelner Chunk unerwartet aussieht.

## Code-Organisation

- **Neue Datei `sigoREST/anthropic.go`**: `handleMessages`, `anthropicRequestToInternal`, `internalToAnthropicResponse`, `streamAnthropicResponse`, alle Anthropic-Wire-Format-Typen (`AnthropicRequest`, `AnthropicMessage`, `AnthropicContentBlock`, `AnthropicTool`, ...).
- **`sigoengine/engine.go`**: `CallAPI`-Signatur erweitert um `[]ToolCall`, neuer Typ `ToolCall`, Response-Parsing erweitert (Anthropic- und OpenAI-Zweig).
- **Gemeinsame Hilfsfunktion** für Channel-Resolution + Failover-Kanalliste (aktuell inline in `handleChatCompletions`, main.go:580-620 sinngemäß) wird herausgezogen, damit `handleMessages` sie mitbenutzt statt zu duplizieren.
- **`sigoREST/main.go`**: Route-Eintrag `mux.HandleFunc("/v1/messages", srv.handleMessages)`; angepasster Call-Site für `CallAPI` (5. Rückgabewert).
- **`cmd/sigoE/main.go`**: angepasster Call-Site (5. Rückgabewert mit `_` verworfen, CLI bekommt in diesem Schritt kein Tool-Calling).

## Testing

- **Unit-Tests** für die reinen Mapper-Funktionen (`anthropicRequestToInternal`, `internalToAnthropicResponse`, `finishReasonToStopReason`) — hier lohnt sich TDD, da Format-Übersetzung fehleranfällig und gut isolierbar ist. Fixtures: einfache Text-Konversation, Tool-Use-Response, Multi-Turn mit `tool_result`.
- **Streaming-Übersetzung** (`streamAnthropicResponse`): Unit-Test mit einem simulierten OpenAI-SSE-Byte-Stream (Text-Deltas + Tool-Call-Deltas über mehrere Chunks verteilt) → erwartete Anthropic-Event-Sequenz.
- **Kein Unit-Test für den HTTP-Handler selbst** (Konvention im Projekt: Server/CLI-Ebene wird manuell getestet, siehe CLAUDE.md).
- **Manueller End-to-End-Test** (Pflicht vor Abschluss, analog zum cheaperinference-Rollout): echte `claude`-CLI gegen den laufenden sigoREST-Server, `ANTHROPIC_BASE_URL` auf sigoREST zeigend, mindestens ein Turn mit Tool-Use (z.B. Datei lesen).

## Risiken und offene Punkte

- **Tool-Call-Streaming-Korrelation** ist der fehleranfälligste Teil (Index-basierte Fragment-Zuordnung, verschiedene Provider könnten sich hier leicht unterschiedlich verhalten trotz „OpenAI-kompatibel"). Muss gegen mindestens zwei verschiedene Provider (z.B. cheaperinference + Mammouth) real getestet werden, nicht nur gegen einen.
- **Extended Thinking** wird für v1 ignoriert — falls Claude Code das standardmäßig anfordert und ein Provider damit nicht umgehen kann, könnte das zu schlechteren (nicht falschen) Antworten führen. Kein Blocker, aber im Auge behalten.
- **`providerForModel()` + Channel-Resolution** werden unverändert wiederverwendet — keine neue Logik nötig, da `/v1/messages` dieselben Modell-IDs/Shortcodes wie `/v1/chat/completions` nutzt.
- Die geplante Erweiterung von `CallAPI`s Signatur ist ein Breaking Change für Code außerhalb dieses Repos, falls `sigoengine` extern importiert wird (aktuell nicht der Fall, `sigorest` ist ein eigenständiges Modul ohne bekannte externe Importer).

## Entscheidungsprotokoll

- **Scope: alle Provider, nicht nur cheaperinference** — Gerhard, 2026-09-12: volle Protokoll-Übersetzung statt schlankem Reverse-Proxy zu einem einzelnen Anthropic-nativen Upstream.
- **Tool-Calling von Anfang an** — Gerhard, 2026-09-12: Claude Code ohne Tools ist praktisch nutzlos, kein zweistufiger Rollout.
- **Keine Memory-/Session-Injektion in diesem Pfad** — Design-Entscheidung (nicht explizit erfragt, aber aus „Claude Code verwaltet eigenen Kontext" abgeleitet); bei Bedarf später revidierbar.
