# sigoREST

REST-Server für sigoEngine. Einheitliche OpenAI-kompatible API für ~100 parallele Verbindungen.
IP-basierte Zugriffskontrolle, globaler + kanal-spezifischer Memory, Multi-Channel-Support mit Failover.

## Architektur

```
sigorest/
├── sigoengine/
│   ├── engine.go              # Shared Package (API-Call, Session, CircuitBreaker, Errors)
│   ├── models.go              # Model-Struct + CoreModels (CLI-Fallback)
│   ├── models_registry.go     # Registry-Logik (Lookup, Shortcode)
│   ├── provider_fetchers.go   # Provider-Fetcher (Mammouth, Moonshot, ZAI)
│   ├── channel.go             # Channel, ChannelRegistry, Env-Discovery
│   ├── channel_manager.go     # Kanal-Auflösung und Failover
│   ├── channel_health.go      # Hintergrund-Health-Monitor
│   ├── session_memory.go      # Session-/Memory-Pfade pro Kanal
│   ├── env.go                 # Optionale ./.env Datei
│   └── version.go             # Zentrale Versions-Konstante
├── cmd/sigoE/main.go          # CLI-Wrapper
└── sigoREST/
    ├── main.go                # REST-Server
    └── memory.json            # Default globaler Memory-Block (embedded)
```

## Installation

### System-Weite Installation (Empfohlen)

**sigoREST Server** (als systemd-Service):
```bash
# Alle Binaries landen in ./build/ (Makefile)
make build
sudo cp build/sigoREST /usr/local/sbin/sigoREST

# Datenverzeichnis anlegen
sudo mkdir -p /var/sigoREST
sudo chown -R sigorest:sigorest /var/sigoREST

# Als systemd-Service einrichten (siehe docs/systemd-install.md)
```

**sigoE CLI**:
```bash
make build
sudo cp build/sigoE /usr/local/bin/sigoE
```

### Entwicklung (Lokal)

```bash
# Alle Binaries bauen → ./build/ (sigoREST, sigoE, mockprovider)
make build

# REST-Server starten
./build/sigoREST -v debug

# CLI nutzen
./build/sigoE -l

# Tests
make test

# ./build/ aufräumen
make clean
```

## Server-Flags

| Flag | Default | Beschreibung |
|------|---------|--------------|
| `-http-port` | `9080` | HTTP (nur localhost 127.0.0.0/8) |
| `-https-port` | `9443` | HTTPS (privates Netz 192.168.0.0/16, 10.0.0.0/8) |
| `-cert` | `./certs/server.crt` | TLS-Zertifikat (wird beim ersten Start auto-generiert) |
| `-key` | `./certs/server.key` | TLS-Schlüssel |
| `-data-dir` | `/var/sigoREST` | Basisverzeichnis für Memory, System-Prompt, channels.json, Sessions, costs.db |
| `-channel-health-interval` | `30s` | Intervall für Kanal-Health-Checks |
| `-rate-min-interval` | `500ms` | Default Mindest-Abstand zwischen Calls pro Kanal (`0`=deaktiviert) |
| `-rate-max-wait` | `1000ms` | Default max Queue-Wartezeit bis HTTP 429 pro Kanal |
| `-comm-log` | — (aus) | Kommunikationsprotokoll: alle Chat-/Embedding-Calls zum Provider mit vollständigem Request/Response als JSONL, z.B. `/var/log/sigoREST/communication.jsonl`. API-Key-Header werden maskiert, Datei `0600`. Siehe `docs/systemd-install.md` |
| `-v` | `info` | Log-Level: `debug\|info\|warn\|error` |
| `-q` | — | Quiet Mode (nur Fehler) |
| `-j` | — | JSON-Logs |
| `-version` | — | Version anzeigen und beenden |

## CLI Flags (sigoE)

| Flag | Default | Beschreibung |
|------|---------|--------------|
| `-m` | `gpt41` | Modell (Shortcode oder vollständiger Name) |
| `-s` | — | Session-ID für Gesprächsverlauf |
| `-session-dir` | `.sessions/` | Verzeichnis für Session-Dateien |
| `-c` | — | Kanal wählen, z.B. `mammouth-0` |
| `-n` | `0` | Max. Tokens (0 = Modell-Default) |
| `-T` | `-1` | Temperatur (-1 = Modell-Default) |
| `-t` | `180` | Timeout in Sekunden |
| `-r` | `3` | Anzahl Wiederholungsversuche |
| `-v` | `info` | Log-Level: `debug\|info\|warn\|error` |
| `-V` / `-version` | — | Version anzeigen |
| `-j` | — | JSON-Ausgabe |
| `-q` | — | Quiet Mode (nur Fehler) |
| `-l` | — | Alle verfügbaren Modelle anzeigen |
| `-i` | — | Modell-Info anzeigen |
| `-h` | — | Hilfe anzeigen |
| `-sp` | — | System-Prompt |

## Zugriffskontrolle

| Port | Protokoll | Erlaubte IPs |
|------|-----------|--------------|
| 9080 | HTTP | 127.0.0.0/8 (localhost) |
| 9443 | HTTPS | 192.168.0.0/16, 10.0.0.0/8 |
| beide | — | IPv6 geblockt (außer ::1) |

## Konfiguration

### Environment / API-Keys

sigoREST liest API-Keys in dieser Reihenfolge:

1. Optionale `.env`-Datei im Startverzeichnis (`./.env`). Eine veraltete
   `./env` wird noch geladen, erzeugt aber eine Warnung (liegen beide vor,
   gewinnt `.env` und `env` wird ignoriert).
2. Echte Environment-Variablen

```bash
MAMMOUTH_API_KEY=sk-...          # Mammoth.ai (GPT, Claude, Gemini, Grok, DeepSeek, ...)
MAMMOUTH_API_KEY_0=sk-...        # Zusätzlicher Kanal 0
MAMMOUTH_API_KEY_1=sk-...        # Zusätzlicher Kanal 1
MOONSHOT_API_KEY=sk-...          # Moonshot.ai (Kimi)
ZAI_API_KEY=sk-...               # Z.ai (GLM)
```

Indizierte Keys (`_0`, `_1`, ...) erzeugen zusätzliche Kanäle. Der unindizierte Key wird zum `default`-Kanal.

### Dynamische Modell-Discovery

Beim Serverstart werden Modelle automatisch von folgenden Providern geladen:

| Provider | Modelle | Auth |
|----------|---------|------|
| Mammouth | ~67 Modelle (GPT, Claude, Gemini, Grok, DeepSeek, ...) | `MAMMOUTH_API_KEY` |
| Moonshot | ~13 Modelle (Kimi, moonshot-v1-*) | `MOONSHOT_API_KEY` |
| ZAI | ~7 Modelle (GLM-Serie) | `ZAI_API_KEY` |
| Ollama | Lokal verfügbare Modelle | — |

Ist ein Provider nicht erreichbar, startet der Server trotzdem mit den übrigen Modellen.

### Datenverzeichnis (`-data-dir`)

Standard: `/var/sigoREST`

```text
/var/sigoREST/
├── channels.json                     # Persistenter Aktivierungs-Status der Kanäle
├── memory.json                       # Globaler Memory-Block
├── system-prompt.txt                 # Globaler System-Prompt
├── costs.db                          # Kosten-Tracking (SQLite, siehe unten)
├── channels/
│   └── <provider>/
│       └── <channel>/
│           ├── memory.json           # Kanal-spezifischer Memory
│           └── system-prompt.txt     # Kanal-spezifischer System-Prompt
└── sessions/
    └── <provider>/
        └── <channel>/
            └── <model>-<session>.json
```

### memory.json (global)

Globaler System-Kontext für alle Anfragen (wird immer zuerst eingefügt):
```json
{
  "content": "Antworte immer auf Deutsch. Du sprichst mit Gerhard, einem erfahrenen Software-Entwickler.",
  "cache": true
}
```
`cache: true` → Anthropic ephemeral caching. OpenAI cached automatisch ab 1024 Tokens.

## Multi-Channel Support

Pro Provider können mehrere API-Key-Kanäle verwaltet werden. Jeder Kanal hat eigenen API-Key, eigenen Memory, eigene Sessions und eigenen Circuit Breaker.

### Standardverhalten

- Nur der unindizierte Key (`MAMMOUTH_API_KEY`) wird als `default`-Kanal aktiv geschaltet.
- Reservekanäle (`_0`, `_1`, ...) sind inaktiv, können aber manuell oder automatisch zugeschaltet werden.

### Kanäle verwalten

```bash
# Alle Kanäle anzeigen
curl -s http://localhost:9080/api/channels

# Einzelkanal anzeigen
curl -s http://localhost:9080/api/channels/mammouth/0

# Kanal aktivieren
curl -s -X POST http://localhost:9080/api/channels/mammouth/0/enable

# Kanal deaktivieren
curl -s -X POST http://localhost:9080/api/channels/mammouth/0/disable

# Kanal-Memory setzen
curl -s -X PUT http://localhost:9080/api/channels/mammouth/0/memory \
  -H "Content-Type: application/json" \
  -d '{"content":"Kanal-spezifischer Kontext","cache":false}'

# Kanal-System-Prompt setzen
curl -s -X PUT http://localhost:9080/api/channels/mammouth/0/system-prompt \
  -H "Content-Type: application/json" \
  -d '{"system_prompt":"Antworte wie ein Pirat."}'
```

### Auto-Failover

Wenn ein Kanal während eines Requests fehlschlägt (Rate-Limit, Timeout, Server-Fehler), probiert sigoREST automatisch den nächsten aktiven Kanal. Auth-Fehler deaktivieren den betroffenen Kanal sofort persistent.

### Rate-Limiter (pro Kanal, hybrid)

Jeder Kanal hat einen eigenen Rate-Limiter, der zu schnelle aufeinanderfolgende Calls an denselben API-Key drosselt — Provider-Rate-Limits werden so vermieden statt im Fehlerfall repariert.

- **`min_interval`** (Default `-rate-min-interval 500ms`): Mindest-Abstand zwischen zwei Calls pro Kanal.
- **`max_wait`** (Default `-rate-max-wait 1000ms`): Wie lange ein Request maximal auf den freien Kanal wartet, bevor HTTP 429 + `Retry-After` an den Client geht.

Verhalten (hybrid): ein Request, der innerhalb von `min_interval` nach dem letzten Call ankommt, wartet bis das Intervall verstrichen ist. Reicht die Wartezeit bis `max_wait`, schlägt er mit `ErrRateLimited` fehl → der Auto-Failover probiert den **nächsten Kanal**. Erst wenn alle Kanäle eines Providers erschöpft sind, erhält der Client HTTP 429. So verteilt sich ein Burst automatisch auf freie API-Keys.

Pro-Kanal-Override in `channels.json` (beide Felder optional, `0`/fehlend → Server-Default):
```json
{
  "provider": "mammouth",
  "name": "0",
  "active": true,
  "min_interval_ms": 800,
  "max_wait_ms": 2000
}
```

Deaktivieren serverweit: `-rate-min-interval 0`.

### Health-Monitor

Ein Hintergrund-Prozess prüft alle aktiven Kanäle im `-channel-health-interval`. Sind alle aktiven Kanäle eines Providers unhealthy, wird der nächste inaktive Reservekanal automatisch aktiviert.

## Client Libraries

Offizielle Clients für verschiedene Programmiersprachen:

| Sprache | Pfad | Installation |
|---------|------|--------------|
| **Python** | [`clients/python/`](clients/python/) | `pip install clients/python/` |
| **Go** | [`clients/go/`](clients/go/) | `go get github.com/gquell/sigoclient` |
| **JavaScript** | [`clients/javascript/`](clients/javascript/) | Kopiere `client.js` |
| **Common Lisp** | [`clients/clisp-exp/`](clients/clisp-exp/) | Experimentell |

### Python (v2 – modern)

```python
from sigo_client import SigoClient

client = SigoClient()

# Normal
response = client.chat.completions.create(
    model="cl5-s",
    messages=[{"role": "user", "content": "Hallo"}]
)
print(response.content)

# Streaming (echtes SSE)
stream = client.chat.completions.create(..., stream=True)
for chunk in stream:
    print(chunk.content, end="", flush=True)
```

> Siehe [`clients/python/README.md`](clients/python/README.md) für Async, detaillierte Dokumentation und Beispiele.

### Go-Beispiel
```go
client := sigoclient.New("http://127.0.0.1:9080")
resp, err := client.Chat(ctx, "kimi", "Hello!")
fmt.Println(resp.Content)
```

### JavaScript-Beispiel
```javascript
const client = new SigoClient('http://127.0.0.1:9080');
const response = await client.chat('kimi', 'Hello!');
console.log(response.content);
```

### Common Lisp-Beispiel
```lisp
;; Voraussetzung quickload ist installiert
(ql:quickload :drakma)
(ql:quickload :yason)
(load "clients/clisp-exp/sigoclient.lisp")
(use-package :sigoclient)

;; Ping
(ping)  ; => T

;; Chat
(chat "kimi" "Hallo!")
; => "Hallo! Wie kann ich dir helfen?"
```

## API Endpoints

### POST /v1/chat/completions
```bash
curl -s http://localhost:9080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "cl46-s",
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

`sigoREST`-Erweiterungen:
- `channel` — Optionaler Kanal-FullName (z.B. `mammouth-0`). Fehlt er, wird der erste aktive Kanal verwendet.
- `session_id` — Session-ID für isolierten Gesprächsverlauf pro Kanal.
- `timeout` — Request-Timeout in Sekunden.
- `retries` — Anzahl Wiederholungsversuche pro Kanal.
- `system_prompt` — Per-Request System-Prompt (höchste Priorität).

`model` akzeptiert ID, Shortcode, oder Shortcode mit Kanal-Suffix
(`zai-glm45-2` → Modell `zai-glm45`, Kanal-Override `2`, überschreibt
`channel` nicht wenn beide gesetzt sind — `channel` gewinnt). Ist das
Modell **retired** (vom Provider dauerhaft verschwunden, siehe
[Persistente Shortcode-Registry](#persistente-shortcode-registry)),
antwortet der Server mit `HTTP 410` und `"model_retired"` statt eines
stillen Fallbacks auf ein anderes Modell.

#### Vision-Unterstützung

sigoREST unterstützt das OpenAI Vision-API-Format. Bilder können als Base64-kodierte Daten-URLs gesendet werden:

```bash
curl -s http://localhost:9080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt4o",
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

**Technische Details:**
- `ChatMessage.Content` ist `json.RawMessage` — Passthrough für String und Vision-Array-Format
- Session-Speicherung extrahiert nur Text (keine Bilddaten in Sessions)
- Empfohlen: JPEG mit quality 75 bei ~100 DPI (ca. 80KB pro Seite)
- Zu große Bilder (PNG 200+ DPI, >1MB) können Proxy-Fehler (413) verursachen

Antwort enthält `usage`-Block (sofern Provider Token-Daten liefert):
```json
{
  "choices": [...],
  "usage": {
    "prompt_tokens": 42,
    "completion_tokens": 18,
    "total_tokens": 60
  }
}
```

### POST /v1/messages

Anthropic-Messages-API-kompatibler Endpoint — macht jedes sigoREST-Modell
(unabhängig vom Provider-Wire-Format) über `ANTHROPIC_BASE_URL` erreichbar,
z.B. für Claude Code:

```bash
unset ANTHROPIC_API_KEY
export ANTHROPIC_BASE_URL="http://localhost:9080"
export ANTHROPIC_AUTH_TOKEN="unused"   # wird ignoriert, claude-CLI verlangt aber einen Wert
export ANTHROPIC_MODEL="cl46-s"
claude "Hallo"
```

Unterstützt Tool-Calling, SSE-Streaming und Extended Thinking. Modellwahl ist 1:1 wie bei
`/v1/chat/completions` (ID/Shortcode, kein Alias). Kein separater
Auth-Mechanismus (gleiche IP-Zugriffskontrolle wie alle anderen Endpunkte),
keine Memory-/System-Prompt-/Session-Injektion — der Client verwaltet
seinen eigenen Kontext. Fehler-Antworten folgen dem Anthropic-Format
(`{"type":"error","error":{"type":"...","message":"..."}}`), nicht dem
OpenAI-Format von `/v1/chat/completions`. `usage` (Input-/Output-Tokens)
wird auch bei Streaming aus den echten Provider-Daten befüllt, nicht
geschätzt.

**Thinking**: Der Anthropic-`thinking`-Parameter (`{"type":"enabled",
"budget_tokens":N}`) wird pro Kanaltyp übersetzt — nativer
`budget_tokens`-Weiterleitung für Anthropic-Kanäle, `thinking`-Objekt für
ZAI (GLM-4.5+) und `reasoning_effort` (low/medium/high, aus dem Budget
gemappt) für alle anderen OpenAI-kompatiblen Provider. Umgekehrt wird
Upstream-`reasoning_content` im Streaming in Anthropic-`thinking`-Blöcke
übersetzt (vor dem text-Block, wie Anthropic-Clients es erwarten);
`thinking`-Blöcke im Request werden ignoriert. Nicht-gestreamte Antworten
liefern kein Thinking zurück (`CallAPI` extrahiert keinen
`reasoning_content`).

### GET /v1/models
```bash
curl -s http://localhost:9080/v1/models
```
OpenAI-kompatible Modell-Liste (ID + Shortcode). `owned_by` ist der
kanonische Provider-Name (z.B. `"mammouth"`, `"zai"`, `"ollama"`) —
nicht `"sigorest"`, das macht den Provider für jeden Client (auch
Hermes) direkt ohne Zusatz-Call sichtbar, analog zu OpenRouters
Konvention.

### GET /api/models
```bash
curl -s http://localhost:9080/api/models
```
Volle Modell-Infos: Preise, Token-Limits, Temperatur-Range — inkl.
`provider` (kanonischer Name) und `provider_code` (normierter
5-Zeichen-Code, siehe [Provider-Kennzeichnung](#provider-kennzeichnung)
unten).

Als CSV (Semikolon, sortiert nach Provider + Shortcode):
```bash
curl -s 'http://localhost:9080/api/models?format=csv' > models.csv
```
Die ersten 11 Spalten entsprechen dem Registry-Format der CLI
(`id;shortcode;endpoint;apikey;max_input;max_output;input_cost;output_cost;min_temp;max_temp;requires_completion_tokens`),
danach `provider;provider_code;upstream_id`. Die Kopfzeile beginnt mit
`#` (Kommentar für den Parser) — die Datei ist damit direkt als
`models.csv` für `sigoE` verwendbar und bringt die Live-Shortcodes des
Servers mit. `apikey` enthält nur den Namen der ENV-Variable, nie den
Key. Unbekanntes `format` → HTTP 400.

### GET /api/shortcodes
```bash
curl -s http://localhost:9080/api/shortcodes
```
Kompaktes Modell→Shortcode-Mapping: `{"gpt-4.1": "gpt41", ...}`.

### GET /api/shortlist
```bash
curl -s http://localhost:9080/api/shortlist
```
Die schnelle Antwort auf "welcher Provider steckt hinter diesem
Shortcode?" — pro Modell nur `shortcode`, `provider` und `provider_code`,
sortiert nach Provider dann Shortcode:
```json
[
  {"shortcode": "cl-s", "provider": "mammouth", "code": "mammo"},
  {"shortcode": "kimi", "provider": "moonshot", "code": "moons"},
  {"shortcode": "zai-glm51", "provider": "zai", "code": "zai__"}
]
```

### Persistente Shortcode-Registry

sigoREST lädt seine Modelle bei jedem Boot dynamisch von den Provider-APIs
(siehe [Dynamische Modell-Discovery](#dynamische-modell-discovery)) — ohne
weitere Vorkehrung würde sich ein pro-Boot berechneter Shortcode bei jeder
Änderung der Live-Liste verschieben. Deshalb vergibt eine persistente
SQLite-Datenbank (`id_registry.db` im `-data-dir`, gleicher Treiber wie
`costs.db`: `modernc.org/sqlite`, WAL) jedem `(provider, modell)`-Paar
**einmalig** einen Shortcode im Format `{provider3}-{semanticCode}`
(z.B. `zai-glm45`, `mam-cl45-s`) — einmal vergeben, bleibt er für immer bei
seinem Modell, auch über Neustarts und Provider-Ausfälle hinweg, und wird
nie wiederverwendet.

Verschwindet ein Modell 3 aufeinanderfolgende **erfolgreiche** Boots lang
aus der Live-Liste eines Providers, wird es *retired* — der Shortcode
bleibt reserviert (kommt das Modell zurück, bekommt es ihn zurück), Aufrufe
liefern `HTTP 410` statt eines stillen Fallbacks auf ein anderes Modell
(siehe `POST /v1/chat/completions` oben). Ein fehlgeschlagener Provider-Fetch
zählt nie als "Boot ohne Modell" — schützt vor der
[dokumentierten ZAI/Longcat-Fallback-Asymmetrie](#dynamische-modell-discovery).

Schlägt das Öffnen von `id_registry.db` fehl (z.B. Disk voll), läuft der
Server trotzdem weiter — Shortcodes werden dann wie vor diesem Feature pro
Boot neu berechnet (nil-safe, analog zu `costs.db`).

**Breaking Change beim Upgrade:** existierende Shortcodes ändern sich
einmalig beim ersten Boot mit dieser Version (`glm46` wird z.B. zu
`zai-glm46`). Es gibt keine Kompatibilitätsschicht — Clients (Skripte,
`ANTHROPIC_BASE_URL`-Configs, eigene Tools) müssen einmal `/api/shortcodes`
neu abrufen.

### GET /api/version
```bash
curl -s http://localhost:9080/api/version
```
Gibt `{"version":"1.1","component":"sigoREST"}` zurück.

### GET /api/channels
```bash
curl -s http://localhost:9080/api/channels
```
Liste aller Kanäle mit Status.

### GET /api/channels/:provider/:name
```bash
curl -s http://localhost:9080/api/channels/mammouth/0
```
Detail-Status eines Kanals.

### POST /api/channels/:provider/:name/enable|disable
```bash
curl -s -X POST http://localhost:9080/api/channels/mammouth/0/enable
curl -s -X POST http://localhost:9080/api/channels/mammouth/0/disable
```
Ein per API deaktivierter Kanal gilt als **manuell deaktiviert**
(`manually_disabled: true` in `/api/channels` und `channels.json`). Das hat
Vorrang vor der Automatik: Der Health-Monitor aktiviert ihn nie wieder,
auch nach einem Neustart nicht. Eine Reserve desselben Providers darf aber
einspringen. Nur ein manuelles `/enable` hebt das auf. Automatische
Abschaltungen (z.B. Auth-Fehler) setzen das Flag nicht.

### GET/PUT /api/channels/:provider/:name/memory
```bash
curl -s http://localhost:9080/api/channels/mammouth/0/memory

curl -s -X PUT http://localhost:9080/api/channels/mammouth/0/memory \
  -H "Content-Type: application/json" \
  -d '{"content":"Kanal-spezifischer Kontext","cache":false}'
```

### GET/PUT /api/channels/:provider/:name/system-prompt
```bash
curl -s http://localhost:9080/api/channels/mammouth/0/system-prompt

curl -s -X PUT http://localhost:9080/api/channels/mammouth/0/system-prompt \
  -H "Content-Type: application/json" \
  -d '{"system_prompt":"Antworte wie ein Pirat."}'
```

### GET /ping
```bash
curl -s http://localhost:9080/ping
```
Einfacher Health-Check für Load Balancer. Antwortet mit `pong`.

### GET /api/health
```bash
curl -s http://localhost:9080/api/health
```
Server-Status, Anzahl Modelle, Circuit-Breaker-Zustand pro Kanal/Modell.

### GET /api/memory
```bash
curl -s http://localhost:9080/api/memory
```

### PUT /api/memory
```bash
curl -s -X PUT http://localhost:9080/api/memory \
  -H "Content-Type: application/json" \
  -d '{"content":"Neuer Kontext","cache":true}'
```
Ändert den globalen Memory-Block zur Laufzeit und schreibt ihn auf Disk.

### GET /api/system-prompt
```bash
curl -s http://localhost:9080/api/system-prompt
```
Aktuellen globalen System-Prompt lesen.

### PUT /api/system-prompt
```bash
curl -s -X PUT http://localhost:9080/api/system-prompt \
  -H "Content-Type: application/json" \
  -d '{"system_prompt":"Du bist ein hilfreicher Assistent."}'
```
Globalen System-Prompt setzen und in `system-prompt.txt` speichern. Kann per Request oder Kanal überschrieben werden.

### GET /api/usage
```bash
curl -s http://localhost:9080/api/usage
```
Kumulierte Token-Statistiken seit Serverstart — pro Modell, pro Kanal und gesamt.
```json
{
  "by_model": {
    "claude-sonnet-4-6": {
      "input_tokens": 1200,
      "output_tokens": 340,
      "total_tokens": 1540,
      "requests": 5
    }
  },
  "by_channel": {
    "claude-sonnet-4-6#mammouth-0": {
      "input_tokens": 600,
      "output_tokens": 170,
      "total_tokens": 770,
      "requests": 2
    }
  },
  "total": {
    "input_tokens": 1200,
    "output_tokens": 340,
    "total_tokens": 1540,
    "requests": 5
  }
}
```
Hinweis: Nur RAM — Reset bei Neustart.

### Kosten-Tracking (`costs.db`)

Persistentes Kosten-Log, unabhängig von `/api/usage` (das nur RAM ist und
bei jedem Neustart zurückgesetzt wird). Jeder abgeschlossene Chat-Call
(`/v1/chat/completions` und `/v1/messages`) schreibt ein Event nach
`<data-dir>/costs.db` (SQLite, WAL) — Modell, Kanal, Session-ID (falls
gesetzt), Token-Zahlen und daraus berechnete USD-Kosten anhand der
Modell-Preise (`input_cost`/`output_cost`, $/1M Tokens). Modelle ohne
Preisangabe (z.B. `ollama-*`) werden mit $0 geloggt, nicht übersprungen —
Tokens/Requests bleiben so trotzdem sichtbar.

Kosten-Tracking ist best-effort: schlägt das Schreiben fehl, wird nur
gewarnt (Log), der Chat-Response-Pfad bricht nie deswegen ab. Konnte
`costs.db` beim Serverstart gar nicht erst geöffnet werden, läuft der
Server ohne Kosten-Persistenz weiter (`/api/costs`/`/api/budget`
antworten dann mit `503 cost_tracking_disabled`).

#### GET /api/costs
```bash
# Heute (lokale Zeitzone), Default ohne Parameter
curl -s http://localhost:9080/api/costs

# Bequemlichkeits-Shortcuts
curl -s "http://localhost:9080/api/costs?period=today"
curl -s "http://localhost:9080/api/costs?period=month"

# Frei wählbarer Zeitraum (RFC3339 oder YYYY-MM-DD)
curl -s "http://localhost:9080/api/costs?since=2026-09-01&until=2026-09-19"
```
Antwort — Gesamt-Summe plus Aufschlüsselung nach Modell und Kanal:
```json
{
  "since": "2026-09-19T00:00:00+02:00",
  "until": "2026-09-19T18:05:15.869063475+02:00",
  "requests": 12,
  "input_tokens": 4400,
  "output_tokens": 28100,
  "total_tokens": 32500,
  "total_cost_usd": 0.842,
  "by_model": {
    "claude-sonnet-4-6": {"requests": 5, "input_tokens": 2000, "output_tokens": 12000, "total_tokens": 14000, "total_cost_usd": 0.6}
  },
  "by_channel": {
    "mammouth#mammouth-default": {"requests": 5, "input_tokens": 2000, "output_tokens": 12000, "total_tokens": 14000, "total_cost_usd": 0.6}
  }
}
```

#### GET/PUT /api/budget

Einfaches Budget-System: Tages- und/oder Monats-Limit in USD, optional mit
Hard-Stop. Ohne Hard-Stop ist ein überschrittenes Limit reine Information
(`daily_exceeded`/`monthly_exceeded` in der Antwort) — kein Request wird
abgelehnt. Mit `hard_stop_enabled: true` lehnt der Server jeden weiteren
Chat-Call mit `HTTP 402 budget_exceeded` ab, sobald ein gesetztes Limit
erreicht ist (`0` = kein Limit für dieses Feld).

```bash
# Aktuelle Config + Verbrauchsstatus (Tag/Monat) in einer Antwort
curl -s http://localhost:9080/api/budget

# Limits setzen
curl -s -X PUT http://localhost:9080/api/budget \
  -H "Content-Type: application/json" \
  -d '{"daily_limit_usd":5,"monthly_limit_usd":100,"hard_stop_enabled":true}'
```
`GET`-Antwort:
```json
{
  "config": {"daily_limit_usd": 5, "monthly_limit_usd": 100, "hard_stop_enabled": true},
  "status": {
    "daily_spend_usd": 2.3, "monthly_spend_usd": 41.7,
    "daily_limit_usd": 5, "monthly_limit_usd": 100,
    "daily_exceeded": false, "monthly_exceeded": false,
    "hard_stop_enabled": true, "blocked": false
  }
}
```
Limit erreicht + Hard-Stop aktiv → jeder weitere Chat-Call:
```json
{"error": {"message": "Budget überschritten (Tag: $5.10/$5.00, Monat: $41.70/$100.00) — Hard-Stop aktiv", "type": "budget_exceeded", "code": "budget_exceeded"}}
```

### GET /api/help
```bash
curl -s http://localhost:9080/api/help
```
Dokumentation aller Endpunkte als JSON.

## Client-Beispiele

### Go
```go
client := openai.NewClient(
    option.WithBaseURL("http://localhost:9080/v1"),
    option.WithAPIKey("dummy"),
)
resp, _ := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
    Model:    openai.F("cl46-s"),
    Messages: openai.F([]openai.ChatCompletionMessageParamUnion{
        openai.UserMessage("Hallo"),
    }),
})
```

### Python
```python
from openai import OpenAI

client = OpenAI(base_url="http://localhost:9080/v1", api_key="dummy")
resp = client.chat.completions.create(
    model="cl46-s",
    messages=[{"role": "user", "content": "Hallo"}],
    extra_body={"session_id": "mein-projekt", "channel": "mammouth-0"},
)
print(resp.choices[0].message.content)
```

## Modelle

Modelle werden beim Serverstart dynamisch von den Providern geladen (~177 Modelle).
Aktuelle Liste:
```bash
curl -s http://localhost:9080/v1/models | jq '.data[].id'
```

**Beispiele:**

| Shortcode | Modell | Provider | Code |
|-----------|--------|----------|------|
| `gpt41` | gpt-4.1 | mammouth | `mammo` |
| `gpt4o` | gpt-4o | mammouth | `mammo` |
| `cl46-s` | claude-sonnet-4-6 | mammouth | `mammo` |
| `kimi` | kimi-k2.5 | moonshot | `moons` |
| `glm51` | glm-5.1 | zai | `zai__` |
| `ollama-gemma3` | gemma3:latest | ollama | `ollam` |

### Provider-Kennzeichnung

Welcher Provider hinter einem Shortcode steckt, ist oft nicht am Namen
ablesbar (`glm51` → zai, `kimi` → moonshot, `ci-cl45-s` → cheaperinference
liefert eigentlich Claude...). Mehrere Wege, das ohne Rätselraten
herauszufinden:

1. **`sigoE -l`** — CLI-Übersicht, nach Provider gruppiert, jede Gruppe
   mit ihrem Code im Header:
   ```
   --- mammouth [mammo] ---
   Modell               Shortcode      Input$/M  Output$/M
   ...
   --- zai [zai__] ---
   ...
   ```
2. **`sigoE -i <shortcode>`** — Einzel-Lookup, zeigt `Provider: zai [zai__]`
   direkt in der Modell-Detailausgabe.
3. **`GET /api/shortlist`** — dieselbe Info programmatisch für Skripte/Tools
   (siehe oben). `GET /api/models` und `GET /v1/models` führen den
   Provider ebenfalls mit (`provider`/`provider_code` bzw. `owned_by`).

Alle vier Wege nutzen intern dieselbe Erkennung
(`sigoengine.ResolveProvider`, Endpoint zuerst, Namens-Heuristik als
Fallback) — keine Abweichungen zwischen CLI und API.

**5-Zeichen-Provider-Codes** (fest, kürzere mit `_` aufgefüllt, längere
abgeschnitten — Tabellen bleiben so immer exakt ausgerichtet):

| Provider | Code |
|----------|------|
| `mammouth` | `mammo` |
| `moonshot` | `moons` |
| `zai` | `zai__` |
| `longcat` | `longc` |
| `cheaperinference` | `cheap` |
| `ollama` | `ollam` |

## Ollama (lokale LLMs)

Ollama-Modelle werden beim Serverstart automatisch entdeckt — kein API-Key, keine Konfiguration nötig.
Werden Modelle gefunden, legt sigoREST dafür den keylosen Kanal `ollama-default` an
(sichtbar unter `/api/channels`, per `.../ollama/default/disable` abschaltbar).

**Voraussetzung:** Ollama läuft auf `http://localhost:11434`

```bash
ollama serve   # falls nicht bereits als Dienst aktiv
```

Shortcode-Schema: `ollama-<modellname>` (`:latest` wird weggeschnitten, andere Tags als Suffix)

| Ollama-Modell | Shortcode |
|---------------|-----------|
| `gemma3:4b` | `ollama-gemma3-4b` |
| `gemma3:12b` | `ollama-gemma3-12b` |
| `qwen3:latest` | `ollama-qwen3` |
| `qwen3:32b` | `ollama-qwen3-32b` |
| `devstral:latest` | `ollama-devstral` |
| `llama3.2-vision:latest` | `ollama-llama3.2-vision` |

Aktuelle Liste der erkannten Modelle:
```bash
curl -s http://localhost:9080/v1/models | python3 -c \
  "import sys,json; [print(m['id']) for m in json.load(sys.stdin)['data'] if m['id'].startswith('ollama-')]"
```

Neues Modell installieren und sofort nutzen:
```bash
ollama pull llama3.3
# Server neu starten — llama3.3 erscheint automatisch als "ollama-llama3.3"
```

Anfrage an lokales Modell:
```bash
curl -s http://localhost:9080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"ollama-gemma3-4b","messages":[{"role":"user","content":"Hallo"}]}'
```

## Session-Management

Sessions werden als JSON-Dateien gespeichert:

```text
<data-dir>/sessions/<provider>/<channel>/<model>-<sessionID>.json
```

Max. 20 Nachrichten pro Session (älteste werden automatisch verworfen).
Sessions sind pro Kanal isoliert — gleiche `session_id` auf verschiedenen Kanälen = verschiedene Dateien.

```bash
# Session ansehen
cat /var/sigoREST/sessions/mammouth/default/cl46-s-mein-projekt.json

# Session löschen
rm /var/sigoREST/sessions/mammouth/default/cl46-s-mein-projekt.json
```

## systemd Service

Für Produktiv-Umgebungen wird sigoREST als systemd-Service empfohlen:
- Binary: `/usr/local/sbin/sigoREST`
- Daten: `/var/sigoREST/`
- Konfiguration/Env: `/usr/local/slib/sigoREST/.env`
- CLI Client: `/usr/local/bin/sigoE`

Service-File Beispiel (`/etc/systemd/system/sigorest.service`):
```ini
[Unit]
Description=sigoREST Server
# network-online.target wartet, bis DNS verfügbar ist
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/usr/local/sbin/sigoREST -data-dir /var/sigoREST -channel-health-interval 30s
Restart=on-failure
User=sigorest
Group=sigorest
EnvironmentFile=/usr/local/slib/sigoREST/.env

[Install]
WantedBy=multi-user.target
```

Detaillierte Anleitung: [`docs/systemd-install.md`](docs/systemd-install.md)

Schnellstart:
```bash
sudo systemctl start sigoREST
sudo systemctl enable sigoREST
journalctl -u sigoREST -f
```
