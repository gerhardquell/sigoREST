# sigoREST

REST server for sigoEngine. Unified OpenAI-compatible API for ~100 parallel connections.
IP-based access control, global memory block for prompt caching.

> **About this project**: sigoREST was developed by Gerhard using **Claude Code** and **Kimi**. We place special emphasis on native support for Chinese LLMs — including Kimi from Moonshot.ai and DeepSeek.

## Architecture

```
sigorest/
├── sigoengine/engine.go    # Shared Package (models, API calls, sessions, circuit breaker)
├── cmd/sigoE/main.go       # CLI wrapper (identical to original sigoEngine)
└── sigoREST/main.go        # REST server
```

## Build & Run

```bash
# Build all packages
go build ./...

# REST server
go build -o sigoREST/sigoREST ./sigoREST/
./sigoREST/sigoREST -v debug

# CLI (backward compatible with sigoEngine)
go build -o sigoE ./cmd/sigoE/
./sigoE -l
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

## Access Control

| Port | Protocol | Allowed IPs |
|------|----------|-------------|
| 9080 | HTTP | 127.0.0.0/8 (localhost) |
| 9443 | HTTPS | 192.168.0.0/16, 10.0.0.0/8 |
| both | — | IPv6 blocked (except ::1) |

## Configuration Files

Both files: Disk takes precedence over embedded defaults.

### models.csv
Comma-separated whitelist of allowed shortcodes:
```
claude-h,gpt41,gemini-p,deepseek-r1,kimi,grok3m
```
Unknown shortcodes are ignored with a warning at startup.

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
    "retries": 3
  }'
```

`session_id`, `timeout`, `retries` are sigoREST extensions — all other fields are standard OpenAI.

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

## Models (Whitelist Default)

sigoREST provides first-class support for Chinese LLMs, including **Kimi** from Moonshot.ai and **DeepSeek**:

| Shortcode | Model | Provider | Input $/M | Output $/M |
|-----------|-------|----------|-----------|------------|
| `claude-h` | claude-3-5-haiku-20241022 | Mammoth.ai | $0.80 | $4.00 |
| `gpt41` | gpt-4.1 | Mammoth.ai | $2.00 | $8.00 |
| `gemini-p` | gemini-2.5-pro | Mammoth.ai | $2.50 | $15.00 |
| `deepseek-r1` | deepseek-r1-0528 | Mammoth.ai | $3.00 | $8.00 |
| `kimi` | kimi-k2.5 | Moonshot.ai | $0.60 | $3.00 |
| `grok3m` | grok-3-mini | Mammoth.ai | $0.30 | $0.50 |

All available cloud shortcodes: `./sigoE -l`

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

## systemd

See `systemd-install.md`.
