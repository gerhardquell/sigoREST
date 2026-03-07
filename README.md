# sigoREST

REST-Server für sigoEngine. Einheitliche OpenAI-kompatible API für ~100 parallele Verbindungen.
IP-basierte Zugriffskontrolle, globaler Memory-Block für Prompt-Caching.

## Architektur

```
sigorest/
├── sigoengine/engine.go    # Shared Package (Modelle, API-Call, Session, CircuitBreaker)
├── cmd/sigoE/main.go       # CLI-Wrapper (identisch zu originalem sigoEngine)
└── sigoREST/main.go        # REST-Server
```

## Installation

### System-Weite Installation (Empfohlen)

**sigoREST Server** (als systemd-Service):
```bash
# Binary kompilieren und installieren
go build -o sigoREST/sigoREST ./sigoREST/
sudo cp sigoREST/sigoREST /usr/local/sbin/sigoREST

# Konfiguration anlegen
sudo mkdir -p /usr/local/slib/sigoREST/certs
sudo cp sigoREST/models.csv /usr/local/slib/sigoREST/
sudo cp sigoREST/memory.json /usr/local/slib/sigoREST/

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
| `-v` | `info` | Log-Level: `debug\|info\|warn\|error` |
| `-q` | — | Quiet Mode (nur Fehler) |
| `-j` | — | JSON-Logs |
| `-version` | — | Version anzeigen und beenden |

## CLI Flags (sigoE)

| Flag | Default | Beschreibung |
|------|---------|--------------|
| `-m` | `gpt41` | Modell (Shortcode oder vollständiger Name) |
| `-s` | — | Session-ID für Gesprächsverlauf |
| `-n` | `0` | Max. Tokens (0 = Modell-Default) |
| `-T` | `-1` | Temperatur (-1 = Modell-Default) |
| `-t` | `180` | Timeout in Sekunden |
| `-r` | `3` | Anzahl Wiederholungsversuche |
| `-v` | `info` | Log-Level: `debug\|info\|warn\|error` |
| `-V` | — | **Version anzeigen** |
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

## Konfigurationsdateien

Beide Dateien: Disk hat Vorrang vor eingebetteten Defaults.

### models.csv
Komma-separierte Whitelist der erlaubten Shortcodes:
```
claude-h,gpt41,gemini-p,deepseek-r1,kimi,grok3m
```
Unbekannte Shortcodes werden beim Start mit Warnung ignoriert.

### memory.json
Globaler System-Kontext für alle Anfragen (wird immer zuerst eingefügt):
```json
{
  "content": "Antworte immer auf Deutsch. Du sprichst mit Gerhard.",
  "cache": true
}
```
`cache: true` → Anthropic ephemeral caching. OpenAI cached automatisch ab 1024 Tokens.

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
    "model": "claude-h",
    "messages": [{"role": "user", "content": "Hallo"}],
    "temperature": 0.7,
    "max_tokens": 1024,
    "session_id": "mein-projekt",
    "timeout": 120,
    "retries": 3
  }'
```

`session_id`, `timeout`, `retries` sind sigoREST-Erweiterungen — alle anderen Felder sind Standard-OpenAI.

### GET /v1/models
```bash
curl -s http://localhost:9080/v1/models
```
OpenAI-kompatible Modell-Liste (nur Whitelist).

### GET /api/models
```bash
curl -s http://localhost:9080/api/models
```
Volle Modell-Infos: Preise, Token-Limits, Temperatur-Range.

### GET /ping
```bash
curl -s http://localhost:9080/ping
```
Einfacher Health-Check für Load Balancer. Antwortet mit `pong`.

### GET /api/health
```bash
curl -s http://localhost:9080/api/health
```
Server-Status, Anzahl Modelle, Circuit-Breaker-Zustand.

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
Ändert den Memory-Block zur Laufzeit und schreibt ihn auf Disk.

## Client-Beispiele

### Go
```go
client := openai.NewClient(
    option.WithBaseURL("http://localhost:9080/v1"),
    option.WithAPIKey("dummy"),
)
resp, _ := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
    Model:    openai.F("claude-h"),
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
    model="claude-h",
    messages=[{"role": "user", "content": "Hallo"}],
    extra_body={"session_id": "mein-projekt"},
)
print(resp.choices[0].message.content)
```

## Modelle (Whitelist-Default)

| Shortcode | Modell | Provider | Input $/M | Output $/M |
|-----------|--------|----------|-----------|------------|
| `claude-h` | claude-3-5-haiku-20241022 | Mammoth.ai | $0.80 | $4.00 |
| `gpt41` | gpt-4.1 | Mammoth.ai | $2.00 | $8.00 |
| `gemini-p` | gemini-2.5-pro | Mammoth.ai | $2.50 | $15.00 |
| `deepseek-r1` | deepseek-r1-0528 | Mammoth.ai | $3.00 | $8.00 |
| `kimi` | kimi-k2.5 | Moonshot.ai | $0.60 | $3.00 |
| `grok3m` | grok-3-mini | Mammoth.ai | $0.30 | $0.50 |

Alle verfügbaren Cloud-Shortcodes: `./sigoE -l`

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

Sessions werden als JSON-Dateien gespeichert: `.sessions/<model>-<sessionID>.json`
Max. 20 Nachrichten pro Session (älteste werden automatisch verworfen).

```bash
# Session ansehen
cat .sessions/claude-h-mein-projekt.json

# Session löschen
rm .sessions/claude-h-mein-projekt.json
```

## ENV-Variablen

```bash
export MAMMOUTH_API_KEY=...   # Mammoth.ai (GPT, Claude, Gemini, Grok, DeepSeek, ...)
export MOONSHOT_API_KEY=...   # Moonshot.ai (Kimi)
export ZAI_API_KEY=...        # Z.ai (GLM)
```

## systemd Service

Für Produktiv-Umgebungen wird sigoREST als systemd-Service empfohlen:
- Binary: `/usr/local/sbin/sigoREST`
- Daten/Konfiguration: `/usr/local/slib/sigoREST/`
- CLI Client: `/usr/local/bin/sigoE`

Detaillierte Anleitung: [`docs/systemd-install.md`](docs/systemd-install.md)

Schnellstart:
```bash
sudo systemctl start sigoREST
sudo systemctl enable sigoREST
journalctl -u sigoREST -f
```
