# sigoREST

REST server for sigoEngine. Unified OpenAI-compatible API for ~100 parallel connections.
IP-based access control, global memory block for prompt caching.

> **About this project**: sigoREST was developed by Gerhard using **Claude Code** and **Kimi**. We place special emphasis on native support for Chinese LLMs — including Kimi from Moonshot.ai and DeepSeek.

## Architecture

```
sigorest/
├── sigoengine/
│   ├── engine.go              # Shared Package (API calls, sessions, circuit breaker)
│   ├── models.go              # Model struct + CoreModels (CLI fallback)
│   ├── models_registry.go     # Registry logic (lookup, shortcode)
│   └── provider_fetchers.go   # Provider fetchers (Mammouth, Moonshot, ZAI)
├── cmd/sigoE/main.go          # CLI wrapper
└── sigoREST/
    ├── main.go                # REST server
    └── memory.json            # Global memory block
```

## Installation

### System-Wide Installation (Recommended)

**sigoREST Server** (as systemd service):
```bash
# Build and install binary
go build -o sigoREST/sigoREST ./sigoREST/
sudo cp sigoREST/sigoREST /usr/local/sbin/sigoREST

# Create configuration
sudo mkdir -p /usr/local/slib/sigoREST/certs
sudo cp sigoREST/memory.json /usr/local/slib/sigoREST/

# Setup as systemd service (see docs/systemd-install.md)
```

**sigoE CLI**:
```bash
# Build and install binary
go build -o cmd/sigoE/sigoE ./cmd/sigoE/
sudo cp cmd/sigoE/sigoE /usr/local/bin/sigoE
```

### Development (Local)

```bash
# Build all packages
go build ./...

# Run REST server
./sigoREST/sigoREST -v debug

# Use CLI
./cmd/sigoE/sigoE -l
```

## Server Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-http-port` | `9080` | HTTP (localhost only 127.0.0.0/8) |
| `-https-port` | `9443` | HTTPS (private networks 192.168.0.0/16, 10.0.0.0/8) |
| `-cert` | `./certs/server.crt` | TLS certificate (auto-generated on first start) |
| `-key` | `./certs/server.key` | TLS key |
| `-v` | `info` | Log level: `debug\|info\|warn\|error` |
| `-q` | — | Quiet mode (errors only) |
| `-j` | — | JSON logs |
| `-version` | — | Show version and exit |

## CLI Flags (sigoE)

| Flag | Default | Description |
|------|---------|-------------|
| `-m` | `gpt41` | Model (shortcode or full name) |
| `-s` | — | Session ID for conversation history |
| `-n` | `0` | Max tokens (0 = model default) |
| `-T` | `-1` | Temperature (-1 = model default) |
| `-t` | `180` | Timeout in seconds |
| `-r` | `3` | Number of retries |
| `-v` | `info` | Log level: `debug\|info\|warn\|error` |
| `-V` | — | **Show version** |
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

## Configuration Files

Both files: Disk takes precedence over embedded defaults.

### Dynamic Model Discovery

At server startup, models are automatically loaded from the following providers:

| Provider | Models | Auth |
|----------|--------|------|
| Mammouth | ~67 models (GPT, Claude, Gemini, Grok, DeepSeek, ...) | `MAMMOUTH_API_KEY` |
| Moonshot | ~13 models (Kimi, moonshot-v1-*) | `MOONSHOT_API_KEY` |
| ZAI | ~7 models (GLM series) | `ZAI_API_KEY` |
| Ollama | Locally available models | — |

If a provider is unreachable, the server starts with the remaining models.

### memory.json
Global system context for all requests (always inserted first):
```json
{
  "content": "Always respond in German. You are speaking with Gerhard.",
  "cache": true
}
```
`cache: true` → Anthropic ephemeral caching. OpenAI caches automatically from 1024 tokens.

## API Endpoints

### POST /v1/chat/completions
```bash
curl -s http://localhost:9080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-h",
    "messages": [{"role": "user", "content": "Hello"}],
    "temperature": 0.7,
    "max_tokens": 1024,
    "session_id": "my-project",
    "timeout": 120,
    "retries": 3,
    "system_prompt": "Optional: overrides the global system prompt"
  }'
```

`session_id`, `timeout`, `retries`, `system_prompt` are sigoREST extensions — all other fields are standard OpenAI.

### GET /v1/models
```bash
curl -s http://localhost:9080/v1/models
```
OpenAI-compatible model list (whitelist only).

### GET /api/models
```bash
curl -s http://localhost:9080/api/models
```
Full model info: prices, token limits, temperature range.

### GET /ping
```bash
curl -s http://localhost:9080/ping
```
Simple health check for load balancers. Responds with `pong`.

### GET /api/health
```bash
curl -s http://localhost:9080/api/health
```
Server status, number of models, circuit breaker state.

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
Changes the memory block at runtime and writes it to disk.

### GET /api/system-prompt
```bash
curl -s http://localhost:9080/api/system-prompt
```
Read the current global system prompt.

### PUT /api/system-prompt
```bash
curl -s -X PUT http://localhost:9080/api/system-prompt \
  -H "Content-Type: application/json" \
  -d '{"system_prompt":"You are a helpful assistant."}'
```
Set the global system prompt and save it to `system-prompt.txt`. Can be overridden per request.

## Client Examples

### Go
```go
client := openai.NewClient(
    option.WithBaseURL("http://localhost:9080/v1"),
    option.WithAPIKey("dummy"),
)
resp, _ := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
    Model:    openai.F("claude-h"),
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
    model="claude-h",
    messages=[{"role": "user", "content": "Hello"}],
    extra_body={"session_id": "my-project"},
)
print(resp.choices[0].message.content)
```

## Models

Models are dynamically loaded from providers at server startup (~84 models).
Current list:
```bash
curl -s http://localhost:9080/v1/models | jq '.data[].id'
```

**Examples:**

| Shortcode | Model | Provider |
|-----------|-------|----------|
| `gpt41` | gpt-4.1 | Mammouth |
| `claude-h` | claude-3-5-haiku-20241022 | Mammouth |
| `kimi` | kimi-k2.5 | Moonshot |
| `glm51` | glm-5.1 | ZAI |
| `ollama-gemma3` | gemma3:latest | Ollama (local) |

## Ollama (Local LLMs)

Ollama models are automatically discovered at server startup — no API key, no configuration needed.

**Prerequisite:** Ollama running at `http://localhost:11434`

```bash
ollama serve   # if not already running as a service
```

Shortcode schema: `ollama-<modelname>` (`:latest` is trimmed, other tags as suffix)

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

Install and use a new model immediately:
```bash
ollama pull llama3.3
# Restart server — llama3.3 appears automatically as "ollama-llama3.3"
```

Request to local model:
```bash
curl -s http://localhost:9080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"ollama-gemma3-4b","messages":[{"role":"user","content":"Hello"}]}'
```

## Session Management

Sessions are stored as JSON files: `.sessions/<model>-<sessionID>.json`
Max. 20 messages per session (oldest are automatically discarded).

```bash
# View session
cat .sessions/claude-h-my-project.json

# Delete session
rm .sessions/claude-h-my-project.json
```

## Environment Variables

```bash
export MAMMOUTH_API_KEY=...   # Mammoth.ai (GPT, Claude, Gemini, Grok, DeepSeek, ...)
export MOONSHOT_API_KEY=...   # Moonshot.ai (Kimi)
export ZAI_API_KEY=...        # Z.ai (GLM)
```

## systemd Service

For production environments, sigoREST should be run as a systemd service:
- Binary: `/usr/local/sbin/sigoREST`
- Data/Configuration: `/usr/local/slib/sigoREST/`
- CLI Client: `/usr/local/bin/sigoE`

Service file example (`/etc/systemd/system/sigorest.service`):
```ini
[Unit]
Description=sigoREST Server
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/sbin/sigoREST
Restart=on-failure
User=sigorest
Group=sigorest

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
