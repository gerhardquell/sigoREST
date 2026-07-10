# sigo-client v2.0 — Modern Python Client for sigoREST

**Production-ready**, OpenAI-compatible client with **sync + async** support using `httpx` and Pydantic v2.

## Features

- ✅ Full **OpenAI SDK compatible** interface (`client.chat.completions.create(..., stream=True)`)
- ✅ **Sync + Async** Support (`SigoClient` + `AsyncSigoClient`)
- ✅ Real **Server-Sent Events (SSE)** streaming with proper `text/event-stream` parsing
- ✅ Pydantic v2 models (`ChatCompletion`, `ChatCompletionChunk`, `Model`, ...)
- ✅ Robust error handling with specific exception types
- ✅ Session management, global memory, health checks, model listing
- ✅ Context manager support (`with` / `async with`)
- ✅ Modern packaging (`pyproject.toml`), full type hints, excellent IDE support

## Installation

### From source (recommended during development)

```bash
cd /u/go-projekte/sigoREST/clients/python
pip install -e ".[dev]"
```

Or with uv (faster):
```bash
uv pip install -e ".[dev]"
```

### Build & install

```bash
pip install .
```

## Quick Start

```python
from sigo_client import SigoClient

client = SigoClient("http://127.0.0.1:9080")

# OpenAI-compatible usage
response = client.chat.completions.create(
    model="cl5-s",                    # Claude Sonnet 5
    messages=[{"role": "user", "content": "Erzähl mir einen Witz auf Deutsch"}],
    temperature=0.7,
    session_id="demo-session"
)

print(response.content)
print(f"Model: {response.model}")
```

### Streaming Example (real SSE)

```python
from sigo_client import SigoClient

client = SigoClient()

stream = client.chat.completions.create(
    model="cl5-s",
    messages=[{"role": "user", "content": "Schreibe ein Haiku über KI."}],
    stream=True
)

for chunk in stream:
    if content := chunk.content:
        print(content, end="", flush=True)
```

### Async Streaming

```python
import asyncio
from sigo_client import AsyncSigoClient

async def main():
    async with AsyncSigoClient() as client:
        stream = await client.chat.completions.create(
            model="cl5-s",
            messages=[{"role": "user", "content": "Haiku über Go und Python"}],
            stream=True
        )
        async for chunk in stream:
            if content := getattr(chunk, 'content', ''):
                print(content, end="", flush=True)

asyncio.run(main())
```

## Available Models (Current Shortcodes)

- `cl5-s` — Claude Sonnet 5
- `cl48-o` — Claude Opus 4.8
- `cl45-s` — Claude Sonnet 4.5
- `kimi` / `kimik26` — Moonshot models
- `ollama-*` — Local Ollama models (if running)

See `examples/list_models.py` for full list.

## Examples

```bash
cd clients/python

# List all models with pricing
python examples/list_models.py

# Basic chat (sync)
python examples/basic_chat.py

# Session-based conversation
python examples/session_chat.py

# Async example
python examples/async_chat.py
```

## API Reference

### Main Classes

- `SigoClient(base_url="http://127.0.0.1:9080", timeout=180)`
- `AsyncSigoClient(...)`

### Key Methods

- `.ping()` → `bool`
- `.health()` → `HealthResponse`
- `.models()` / `.list_models()` → `List[Model]`
- `.get_memory()` / `.set_memory(content, cache=True)`
- `.chat.completions.create(...)` → `ChatCompletion` (or async iterator for stream)

### Legacy Compatibility

The old `chat(model, message, session_id=...)` style is still supported via the high-level methods, but the new OpenAI-style interface is recommended.

## Development

```bash
# Run tests (when available)
pytest

# Format & lint
ruff format .
ruff check --fix
mypy src/
```

## License

Copyright © 2026 Gerhard Quell (SKEQuell). MIT License.

---

**This is version 2.0** (Juli 2026) — Komplette Neuentwicklung mit Fokus auf Modernität, Type-Safety, OpenAI-Kompatibilität und **echtem SSE-Streaming**. Vollständig abwärtskompatibel zum bestehenden sigoREST-Server.

Siehe `RETROSPECTIVE.md` (Abschnitt 2026-07-10) für detaillierte Entwicklungsgeschichte.
