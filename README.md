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
│   ├── env.go                 # Optionale ./env Datei
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
# Binary kompilieren und installieren
go build -o sigoREST/sigoREST ./sigoREST/
sudo cp sigoREST/sigoREST /usr/local/sbin/sigoREST

# Datenverzeichnis anlegen
sudo mkdir -p /var/sigoREST
sudo chown -R sigorest:sigorest /var/sigoREST

# Als systemd-Service einrichten (siehe docs/systemd-install.md)
```

**sigoE CLI**:
```bash
# Binary kompilieren und installieren
go build -o cmd/sigoE/sigoE ./cmd/sigoE/
sudo cp cmd/sigoE/sigoE /usr/local/bin/sigoE
```

### Entwicklung (Lokal)

```bash
# Alle Pakete bauen
go build ./...

# REST-Server starten
./sigoREST/sigoREST -v debug

# CLI nutzen
./cmd/sigoE/sigoE -l
```

## Server-Flags

| Flag | Default | Beschreibung |
|------|---------|--------------|
| `-http-port` | `9080` | HTTP (nur localhost 127.0.0.0/8) |
| `-https-port` | `9443` | HTTPS (privates Netz 192.168.0.0/16, 10.0.0.0/8) |
| `-cert` | `./certs/server.crt` | TLS-Zertifikat (wird beim ersten Start auto-generiert) |
| `-key` | `./certs/server.key` | TLS-Schlüssel |
| `-data-dir` | `/var/sigoREST` | Basisverzeichnis für Memory, System-Prompt, channels.json, Sessions |
| `-channel-health-interval` | `30s` | Intervall für Kanal-Health-Checks |
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

1. Optionale `env`-Datei im Startverzeichnis (`./env`)
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
  "content": "Antworte immer auf Deutsch. Du sprichst mit Gerhard.",
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

### Python-Beispiel
```python
from sigoclient import SigoClient

client = SigoClient("http://127.0.0.1:9080")
response = client.chat("kimi", "Hello!")
print(response.content)
```

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

### Common Lisp-Beispiel (experimentell)
```lisp
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

### GET /v1/models
```bash
curl -s http://localhost:9080/v1/models
```
OpenAI-kompatible Modell-Liste (ID + Shortcode).

### GET /api/models
```bash
curl -s http://localhost:9080/api/models
```
Volle Modell-Infos: Preise, Token-Limits, Temperatur-Range.

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

Modelle werden beim Serverstart dynamisch von den Providern geladen (~89 Modelle).
Aktuelle Liste:
```bash
curl -s http://localhost:9080/v1/models | jq '.data[].id'
```

**Beispiele:**

| Shortcode | Modell | Provider |
|-----------|--------|----------|
| `gpt41` | gpt-4.1 | Mammouth |
| `gpt4o` | gpt-4o | Mammouth |
| `cl46-s` | claude-sonnet-4-6 | Mammouth |
| `kimi` | kimi-k2.5 | Moonshot |
| `glm51` | glm-5.1 | ZAI |
| `ollama-gemma3` | gemma3:latest | Ollama (lokal) |

## Ollama (lokale LLMs)

Ollama-Modelle werden beim Serverstart automatisch entdeckt — kein API-Key, keine Konfiguration nötig.

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
- Konfiguration/Env: `/usr/local/slib/sigoREST/env`
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
EnvironmentFile=/usr/local/slib/sigoREST/env

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
