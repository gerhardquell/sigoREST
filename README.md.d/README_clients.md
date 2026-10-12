# Clients

[← README](../README.md)

sigoREST ist OpenAI-kompatibel — jede OpenAI-Bibliothek funktioniert mit
`base_url` auf den Server. Zusätzlich gibt es eigene Clients.

## Eigene Client-Bibliotheken

| Sprache | Pfad | Installation |
|---------|------|--------------|
| **Python** | [`clients/python/`](../clients/python/) | `pip install clients/python/` |
| **Go** | [`clients/go/`](../clients/go/) | `go get github.com/gquell/sigoclient` |
| **JavaScript** | [`clients/javascript/`](../clients/javascript/) | Kopiere `client.js` |
| **Common Lisp** | [`clients/clisp-exp/`](../clients/clisp-exp/) | Experimentell |
| **C++ / Qt** | [`clients/cpp/`](../clients/cpp/) | CMake |
| **pi-Extension** | [`clients/pi/`](../clients/pi/) | siehe dortiges README |

### Python (v2)

```python
from sigo_client import SigoClient

client = SigoClient()

# Normal
response = client.chat.completions.create(
    model="mam-cl5-s",
    messages=[{"role": "user", "content": "Hallo"}]
)
print(response.content)

# Streaming (echtes SSE)
stream = client.chat.completions.create(..., stream=True)
for chunk in stream:
    print(chunk.content, end="", flush=True)
```

Async, Details und Beispiele: [`clients/python/README.md`](../clients/python/README.md).

### Go

```go
client := sigoclient.New("http://127.0.0.1:9080")
resp, err := client.Chat(ctx, "moo-kimik3", "Hello!")
fmt.Println(resp.Content)
```

### JavaScript

```javascript
const client = new SigoClient('http://127.0.0.1:9080');
const response = await client.chat('moo-kimik3', 'Hello!');
console.log(response.content);
```

### Common Lisp

```lisp
;; Voraussetzung: quicklisp ist installiert
(ql:quickload :drakma)
(ql:quickload :yason)
(load "clients/clisp-exp/sigoclient.lisp")
(use-package :sigoclient)

;; Ping
(ping)  ; => T

;; Chat
(chat "moo-kimik3" "Hallo!")
; => "Hallo! Wie kann ich dir helfen?"
```

## Mit OpenAI-Bibliotheken

### Go

```go
client := openai.NewClient(
    option.WithBaseURL("http://localhost:9080/v1"),
    option.WithAPIKey("dummy"),
)
resp, _ := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
    Model:    openai.F("mam-cl46-s"),
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
    model="mam-cl46-s",
    messages=[{"role": "user", "content": "Hallo"}],
    extra_body={"session_id": "mein-projekt", "channel": "mammouth-0"},
)
print(resp.choices[0].message.content)
```

### Claude Code / Anthropic-SDKs

Über die Anthropic-Bridge `/v1/messages` mit `ANTHROPIC_BASE_URL`, siehe
[API → POST /v1/messages](README_api.md#post-v1messages-anthropic-bridge).
