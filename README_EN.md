# sigoREST

REST server for sigoEngine. Unified OpenAI-compatible API for ~100 parallel connections.
IP-based access control, global + channel-specific memory, Multi-Channel support with failover.

## Architecture

```
sigorest/
├── sigoengine/
│   ├── engine.go              # Shared Package (API-Call, Session, CircuitBreaker, Errors)
│   ├── models.go              # Model-Struct + CoreModels (CLI-Fallback)
│   ├── models_registry.go     # Registry-Logic (Lookup, Shortcode)
│   ├── provider_fetchers.go   # Provider-Fetcher (Mammouth, Moonshot, ZAI)
│   ├── channel.go             # Channel, ChannelRegistry, Env-Discovery
│   ├── channel_manager.go     # Channel resolution and failover
│   ├── channel_health.go      # Background health monitor
│   ├── session_memory.go      # Session/Memory paths per channel
│   ├── env.go                 # Optional ./env file
│   └── version.go             # Central version constant
├── cmd/sigoE/main.go          # CLI-Wrapper
└── sigoREST/
    ├── main.go                # REST server
    └── memory.json            # Default global memory block (embedded)
```

## Installation

### System-Wide Installation (Recommended)

**sigoREST Server** (as systemd service):
```bash
# Compile and install binary
go build -o sigoREST/sigoREST ./sigoREST/
sudo cp sigoREST/sigoREST /usr/local/sbin/sigoREST

# Create data directory
sudo mkdir -p /var/sigoREST
sudo chown -R sigorest:sigorest /var/sigoREST

# Set up as systemd service (see docs/systemd-install.md)
```

**sigoE CLI**:
```bash
# Compile and install binary
go build -o cmd/sigoE/sigoE ./cmd/sigoE/
sudo cp cmd/sigoE/sigoE /usr/local/bin/sigoE
```

### Development (Local)

```bash
# Build all packages
go build ./...

# Start REST server
./sigoREST/sigoREST -v debug

# Use CLI
./cmd/sigoE/sigoE -l
```

## Server Flags

| Flag | Default | Description |
|------|---------|--------------|
| `-http-port` | `9080` | HTTP (only localhost 127.0.0.0/8) |
| `-https-port` | `9443` | HTTPS (private network 192.168.0.0/16, 10.0.0.0/8) |
| `-cert` | `./certs/server.crt` | TLS certificate (auto-generated on first start) |
| `-key` | `./certs/server.key` | TLS key |
| `-data-dir` | `/var/sigoREST` | Base directory for memory, system-prompt, channels.json, sessions |
| `-channel-health-interval` | `30s` | Interval for channel health checks |
| `-v` | `info` | Log level: `debug\|info\|warn\|error` |
| `-q` | — | Quiet mode (errors only) |
| `-j` | — | JSON logs |
| `-version` | — | Show version and exit |

## CLI Flags (sigoE)

| Flag | Default | Description |
|------|---------|--------------|
| `-m` | `gpt41` | Model (shortcode or full name) |
| `-s` | — | Session ID for conversation history |
| `-session-dir` | `.sessions/` | Directory for session files |
| `-c` | — | Select channel, e.g. `mammouth-0` |
| `-n` | `0` | Max. tokens (0 = model default) |
| `-T` | `-1` | Temperature (-1 = model default) |
| `-t` | `180` | Timeout in seconds |
| `-r` | `3` | Number of retry attempts |
| `-v` | `info` | Log level: `debug\|info\|warn\|error` |
| `-V` / `-version` | — | Show version |
| `-j` | — | JSON output |
| `-q` | — | Quiet mode (errors only) |
| `-l` | — | List all available models |
| `-i` | — | Show model info |
| `-h` | — | Show help |
| `-sp` | — | System prompt |

## Access Control

| Port | Protocol | Allowed IPs |
|------|----------|-------------|
| 9080 | HTTP | 127.0.0.0/8 (localhost) |
| 9443 | HTTPS | 192.168.0.0/16, 10.0.0.0/8 |
| both | — | IPv6 blocked (except ::1) |

## Configuration

### Environment / API Keys

sigoREST reads API keys in this order:

1. Optional `env` file in the startup directory (`./env`)
2. Actual environment variables

```bash
MAMMOUTH_API_KEY=sk-...          # Mammoth.ai (GPT, Claude, Gemini, Grok, DeepSeek, ...)
MAMMOUTH_API_KEY_0=sk-...        # Additional channel 0
MAMMOUTH_API_KEY_1=sk-...        # Additional channel 1
MOONSHOT_API_KEY=sk-...          # Moonshot.ai (Kimi)
ZAI_API_KEY=sk-...               # Z.ai (GLM)
```

Indexed keys (`_0`, `_1`, ...) create additional channels. The unindexed key becomes the `default` channel.

### Dynamic Model Discovery

On server startup, models are automatically loaded from the following providers:

| Provider | Models | Auth |
|----------|--------|------|
| Mammouth | ~67 models (GPT, Claude, Gemini, Grok, DeepSeek, ...) | `MAMMOUTH_API_KEY` |
| Moonshot | ~13 models (Kimi, moonshot-v1-*) | `MOONSHOT_API_KEY` |
| ZAI | ~7 models (GLM series) | `ZAI_API_KEY` |
| Ollama | Locally available models | — |

If a provider is unreachable, the server starts anyway with the remaining models.

### Data Directory (`-data-dir`)

Default: `/var/sigoREST`

```text
/var/sigoREST/
├── channels.json                     # Persistent activation status of channels
├── memory.json                       # Global memory block
├── system-prompt.txt                 # Global system prompt
├── channels/
│   └── <provider>/
│       └── <channel>/
│           ├── memory.json           # Channel-specific memory
│           └── system-prompt.txt     # Channel-specific system prompt
└── sessions/
    └── <provider>/
        └── <channel>/
            └── <model>-<session>.json
```

### memory.json (global)

Global system context for all requests (always inserted first):
```json
{
  "content": "Antworte immer auf Deutsch. Du sprichst mit Gerhard, einem erfahrenen Software-Entwickler.",
  "cache": true
}
```
`cache: true` → Anthropic ephemeral caching. OpenAI caches automatically from 1024 tokens.

## Multi-Channel Support

Multiple API key channels can be managed per provider. Each channel has its own API key, memory, sessions, and circuit breaker.

### Default Behavior

- Only the unindexed key (`MAMMOUTH_API_KEY`) is activated as `default` channel.
- Reserve channels (`_0`, `_1`, ...) are inactive but can be enabled manually or automatically.

### Managing Channels

```bash
# List all channels
curl -s http://localhost:9080/api/channels

# Show single channel
curl -s http://localhost:9080/api/channels/mammouth/0

# Enable channel
curl -s -X POST http://localhost:9080/api/channels/mammouth/0/enable

# Disable channel
curl -s -X POST http://localhost:9080/api/channels/mammouth/0/disable

# Set channel memory
curl -s -X PUT http://localhost:9080/api/channels/mammouth/0/memory \
  -H "Content-Type: application/json" \
  -d '{"content":"Channel-specific context","cache":false}'

# Set channel system prompt
curl -s -X PUT http://localhost:9080/api/channels/mammouth/0/system-prompt \
  -H "Content-Type: application/json" \
  -d '{"system_prompt":"Answer like a pirate."}'
```

### Auto-Failover

If a channel fails during a request (rate limit, timeout, server error), sigoREST automatically tries the next active channel. Auth errors immediately and persistently disable the affected channel.

### Health Monitor

A background process checks all active channels at the `-channel-health-interval`. If all active channels of a provider are unhealthy, the next inactive reserve channel is automatically activated.

## Client Libraries

Official clients for various programming languages:

| Language | Path | Installation |
|----------|------|--------------|
| **Python** | [`clients/python/`](clients/python/) | `pip install clients/python/` |
| **Go** | [`clients/go/`](clients/go/) | `go get github.com/gquell/sigoclient` |
| **JavaScript** | [`clients/javascript/`](clients/javascript/) | Copy `client.js` |
| **Common Lisp** | [`clients/clisp-exp/`](clients/clisp-exp/) | Experimental |

### Python (v2 — modern)

```python
from sigo_client import SigoClient

client = SigoClient()

# Normal
response = client.chat.completions.create(
    model="cl5-s",
    messages=[{"role": "user", "content": "Hello"}]
)
print(response.content)

# Streaming (real SSE)
stream = client.chat.completions.create(..., stream=True)
for chunk in stream:
    print(chunk.content, end="", flush=True)
```

> See [`clients/python/README.md`](clients/python/README.md) for Async, detailed documentation and examples.

### Go Example
```go
client := sigoclient.New("http://127.0.0.1:9080")
resp, err := client.Chat(ctx, "kimi", "Hello!")
fmt.Println(resp.Content)
```

### JavaScript Example
```javascript
const client = new SigoClient('http://127.0.0.1:9080');
const response = await client.chat('kimi', 'Hello!');
console.log(response.content);
```

### Common Lisp Example
```lisp
;; Prerequisite: quickload is installed
(ql:quickload :drakma)
(ql:quickload :yason)
(load "clients/clisp-exp/sigoclient.lisp")
(use-package :sigoclient)

;; Ping
(ping)  ; => T

;; Chat
(chat "kimi" "Hello!")
; => "Hello! How can I help you?"
```

## API Endpoints

### POST /v1/chat/completions
```bash
curl -s http://localhost:9080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "cl46-s",
    "channel": "mammouth-0",
    "messages": [{"role": "user", "content": "Hello"}],
    "temperature": 0.7,
    "max_tokens": 1024,
    "session_id": "my-project",
    "timeout": 120,
    "retries": 3,
    "system_prompt": "Optional: overrides global + channel-specific system prompts"
  }'
```

`sigoREST` extensions:
- `channel` — Optional channel full name (e.g. `mammouth-0`). If missing, the first active channel is used.
- `session_id` — Session ID for isolated conversation history per channel.
- `timeout` — Request timeout in seconds.
- `retries` — Number of retry attempts per channel.
- `system_prompt` — Per-request system prompt (highest priority).

#### Vision Support

sigoREST supports the OpenAI Vision API format. Images can be sent as Base64-encoded data URLs:

```bash
curl -s http://localhost:9080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt4o",
    "messages": [{
      "role": "user",
      "content": [
        {"type": "text", "text": "What do you see in this image?"},
        {"type": "image_url", "image_url": {
          "url": "data:image/jpeg;base64,/9j/4AAQ..."
        }}
      ]
    }],
    "max_tokens": 4096
  }'
```

**Technical Details:**
- `ChatMessage.Content` is `json.RawMessage` — passthrough for String and Vision array format
- Session storage extracts only text (no image data in sessions)
- Recommended: JPEG with quality 75 at ~100 DPI (~80KB per page)
- Oversized images (PNG 200+ DPI, >1MB) can cause proxy errors (413)

Response contains `usage` block (if provider supplies token data):
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
OpenAI-compatible model list (ID + Shortcode).

### GET /api/models
```bash
curl -s http://localhost:9080/api/models
```
Full model info: prices, token limits, temperature range.

### GET /api/version
```bash
curl -s http://localhost:9080/api/version
```
Returns `{"version":"1.1","component":"sigoREST"}`.

### GET /api/channels
```bash
curl -s http://localhost:9080/api/channels
```
List of all channels with status.

### GET /api/channels/:provider/:name
```bash
curl -s http://localhost:9080/api/channels/mammouth/0
```
Detailed status of a channel.

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
  -d '{"content":"Channel-specific context","cache":false}'
```

### GET/PUT /api/channels/:provider/:name/system-prompt
```bash
curl -s http://localhost:9080/api/channels/mammouth/0/system-prompt

curl -s -X PUT http://localhost:9080/api/channels/mammouth/0/system-prompt \
  -H "Content-Type: application/json" \
  -d '{"system_prompt":"Answer like a pirate."}'
```

### GET /ping
```bash
curl -s http://localhost:9080/ping
```
Simple health check for load balancer. Responds with `pong`.

### GET /api/health
```bash
curl -s http://localhost:9080/api/health
```
Server status, number of models, circuit-breaker state per channel/model.

### GET /api/memory
```bash
curl -s http://localhost:9080/api/memory
```

### PUT /api/memory
```bash
curl -s -X PUT http://localhost:9080/api/memory \
  -H "Content-Type: application/json" \
  -d '{"content":"New context","cache":true}'
```
Changes the global memory block at runtime and writes it to disk.

### GET /api/system-prompt
```bash
curl -s http://localhost:9080/api/system-prompt
```
Read current global system prompt.

### PUT /api/system-prompt
```bash
curl -s -X PUT http://localhost:9080/api/system-prompt \
  -H "Content-Type: application/json" \
  -d '{"system_prompt":"You are a helpful assistant."}'
```
Set global system prompt and save to `system-prompt.txt`. Can be overridden per request or channel.

### GET /api/usage
```bash
curl -s http://localhost:9080/api/usage
```
Cumulative token statistics since server start — per model, per channel, and total.
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
Note: RAM only — reset on restart.

### GET /api/help
```bash
curl -s http://localhost:9080/api/help
```
Documentation of all endpoints as JSON.

## Client Examples

### Go
```go
client := openai.NewClient(
    option.WithBaseURL("http://localhost:9080/v1"),
    option.WithAPIKey("dummy"),
)
resp, _ := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
    Model:    openai.F("cl46-s"),
    Messages: openai.F([]openai.ChatCompletionMessageParamUnion{
        openai.UserMessage("Hello"),
    }),
})
```

### Python
```python
from openai import OpenAI

client = OpenAI(base_url="http://localhost:9080/v1", api_key="dummy")
resp = client.chat.completions.create(
    model="cl46-s",
    messages=[{"role": "user", "content": "Hello"}],
    extra_body={"session_id": "my-project", "channel": "mammouth-0"},
)
print(resp.choices[0].message.content)
```

## Models

Models are loaded dynamically from providers on server startup (~89 models).
Current list:
```bash
curl -s http://localhost:9080/v1/models | jq '.data[].id'
```

**Examples:**

| Shortcode | Model | Provider |
|-----------|-------|----------|
| `gpt41` | gpt-4.1 | Mammouth |
| `gpt4o` | gpt-4o | Mammouth |
| `cl46-s` | claude-sonnet-4-6 | Mammouth |
| `kimi` | kimi-k2.5 | Moonshot |
| `glm51` | glm-5.1 | ZAI |
| `ollama-gemma3` | gemma3:latest | Ollama (local) |

## Ollama (local LLMs)

Ollama models are automatically discovered on server startup — no API key, no configuration needed.

**Prerequisite:** Ollama running on `http://localhost:11434`

```bash
ollama serve   # if not already active as service
```

Shortcode schema: `ollama-<modelname>` (`:latest` is stripped, other tags as suffix)

| Ollama Model | Shortcode |
|--------------|-----------|
| `gemma3:4b` | `ollama-gemma3-4b` |
| `gemma3:12b` | `ollama-gemma3-12b` |
| `qwen3:latest` | `ollama-qwen3` |
| `qwen3:32b` | `ollama-qwen3-32b` |
| `devstral:latest` | `ollama-devstral` |
| `llama3.2-vision:latest` | `ollama-llama3.2-vision` |

Current list of detected models:
```bash
curl -s http://localhost:9080/v1/models | python3 -c \
  "import sys,json; [print(m['id']) for m in json.load(sys.stdin)['data'] if m['id'].startswith('ollama-')]"
```

Install new model and use immediately:
```bash
ollama pull llama3.3
# Restart server — llama3.3 automatically appears as "ollama-llama3.3"
```

Request to local model:
```bash
curl -s http://localhost:9080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"ollama-gemma3-4b","messages":[{"role":"user","content":"Hello"}]}'
```

## Session Management

Sessions are stored as JSON files:

```text
<data-dir>/sessions/<provider>/<channel>/<model>-<sessionID>.json
```

Max. 20 messages per session (oldest are automatically discarded).
Sessions are isolated per channel — same `session_id` on different channels = different files.

```bash
# View session
cat /var/sigoREST/sessions/mammouth/default/cl46-s-my-project.json

# Delete session
rm /var/sigoREST/sessions/mammouth/default/cl46-s-my-project.json
```

## systemd Service

For production environments, sigoREST is recommended as a systemd service:
- Binary: `/usr/local/sbin/sigoREST`
- Data: `/var/sigoREST/`
- Configuration/Env: `/usr/local/slib/sigoREST/env`
- CLI client: `/usr/local/bin/sigoE`

Service file example (`/etc/systemd/system/sigorest.service`):
```ini
[Unit]
Description=sigoREST Server
# network-online.target waits until DNS is available
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

Detailed instructions: [`docs/systemd-install.md`](docs/systemd-install.md)

Quick start:
```bash
sudo systemctl start sigoREST
sudo systemctl enable sigoREST
journalctl -u sigoREST -f
```