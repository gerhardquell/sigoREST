# sigoclient - Python Client for sigoREST

A simple, lightweight Python client for the sigoREST OpenAI-compatible API.

## Installation

### From source
```bash
cd /u/go-projekte/sigoREST/clients/python
pip install .
```

### Development mode
```bash
cd /u/go-projekte/sigoREST/clients/python
pip install -e .
```

## Quick Start

```python
from sigoclient import SigoClient

# Create client
client = SigoClient("http://127.0.0.1:9080")

# Simple chat
response = client.chat("claude-h", "Hello!")
print(response.content)
```

## Features

- ✅ Simple, intuitive API
- ✅ Session management for conversation continuity
- ✅ Automatic retry handling
- ✅ Health checking (`ping()`, `health()`)
- ✅ Model listing with pricing info
- ✅ Global memory management
- ✅ Context manager support (`with` statement)
- ✅ Proper error handling with custom exceptions

## Examples

### Basic Chat
```python
from sigoclient import SigoClient

client = SigoClient("http://127.0.0.1:9080")
response = client.chat(
    model="claude-h",
    message="Explain quantum computing"
)
print(response.content)
```

### Session-based Conversation
```python
from sigoclient import SigoClient

client = SigoClient("http://127.0.0.1:9080")

# First message
response = client.chat(
    model="claude-h",
    message="My name is Alice",
    session_id="my-conversation"
)

# Context is preserved
response = client.chat(
    model="claude-h",
    message="What's my name?",  # Will know it's Alice
    session_id="my-conversation"
)
```

### With Context Manager
```python
from sigoclient import SigoClient

with SigoClient("http://localhost:9080") as client:
    response = client.chat("gpt41", "Hello!")
    print(response.content)
# Connection automatically closed
```

### Error Handling
```python
from sigoclient import SigoClient, SigoError, SigoAPIError

client = SigoClient("http://127.0.0.1:9080")

try:
    response = client.chat("unknown-model", "Hello")
except SigoAPIError as e:
    print(f"API Error {e.status_code}: {e.message}")
except SigoError as e:
    print(f"Client Error: {e}")
```

### List Available Models
```python
from sigoclient import SigoClient

client = SigoClient("http://127.0.0.1:9080")
models = client.list_models()

for model in models:
    print(f"{model.shortcode}: ${model.input_cost}/M in, ${model.output_cost}/M out")
```

### Health Check
```python
from sigoclient import SigoClient

client = SigoClient("http://127.0.0.1:9080")

# Simple ping
if client.ping():
    print("Server is alive!")

# Detailed health
health = client.health()
print(f"Models available: {health['available_models']}")
```

### Set Global Memory
```python
from sigoclient import SigoClient

client = SigoClient("http://127.0.0.1:9080")

# Set system context for all requests
client.set_memory(
    content="Always respond in German. Be concise.",
    cache=True
)
```

## API Reference

### SigoClient

#### Constructor
```python
SigoClient(base_url: str = "http://localhost:9080", timeout: int = 180)
```

#### Methods

- `ping() -> bool` - Check if server is alive
- `health() -> Dict` - Get detailed health status
- `list_models() -> List[ModelInfo]` - List all available models
- `chat(model, message, **kwargs) -> ChatResponse` - Send chat request
- `chat_stream(model, message, **kwargs) -> Iterator[str]` - Stream response
- `get_memory() -> Dict` - Get global memory block
- `set_memory(content, cache=True) -> Dict` - Set global memory block
- `close()` - Close HTTP session

#### chat() Parameters

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `model` | str | required | Model shortcode or full ID |
| `message` | str | required | User message |
| `session_id` | str | None | Session ID for continuity |
| `system_prompt` | str | None | System prompt/context |
| `temperature` | float | None | Temperature (0.0-2.0) |
| `max_tokens` | int | None | Max tokens to generate |
| `timeout` | int | None | Request timeout override |
| `retries` | int | 3 | Number of retries |

## Running Examples

Make sure sigoREST server is running:
```bash
./sigoREST/sigoREST -q
```

Then run examples:
```bash
cd /u/go-projekte/sigoREST/clients/python

# Basic chat
python examples/basic_chat.py

# Session-based conversation
python examples/session_chat.py

# List models
python examples/list_models.py
```

## Requirements

- Python 3.7+
- requests 2.25.0+
- Running sigoREST server

## License

Copyright 2025 Gerhard Quell - SKEQuell
