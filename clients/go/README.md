# sigoclient - Go Client for sigoREST

A simple, type-safe Go client for the sigoREST OpenAI-compatible API.

## Installation

```bash
go get github.com/gquell/sigoclient
```

Or from local source:
```bash
cd /u/go-projekte/sigoREST/clients/go
go mod init myproject
go mod edit -replace github.com/gquell/sigoclient=.
```

## Quick Start

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/gquell/sigoclient"
)

func main() {
    client := sigoclient.New("http://127.0.0.1:9080")

    resp, err := client.Chat(context.Background(), "kimi", "Hello!")
    if err != nil {
        log.Fatal(err)
    }

    fmt.Println(resp.Content)
}
```

## Features

- ✅ Type-safe API with Go structs
- ✅ Context support for timeouts and cancellation
- ✅ Session management for conversation continuity
- ✅ Functional options pattern for configuration
- ✅ Health checking (`Ping()`, `Health()`)
- ✅ Model listing with pricing info
- ✅ Proper error handling with custom error types

## Examples

### Basic Chat

```go
client := sigoclient.New("http://127.0.0.1:9080")

resp, err := client.Chat(context.Background(), "kimi", "Explain quantum computing")
if err != nil {
    log.Fatal(err)
}
fmt.Println(resp.Content)
```

### With Options

```go
resp, err := client.Chat(ctx, "gpt41", "Explain Go routines",
    sigoclient.WithSession("my-session"),
    sigoclient.WithTemperature(0.7),
    sigoclient.WithMaxTokens(1024),
    sigoclient.WithSystemPrompt("You are a helpful expert."),
)
```

### Session-based Conversation

```go
ctx := context.Background()

// First message
resp, err := client.Chat(ctx, "kimi", "My name is Alice",
    sigoclient.WithSession("my-conversation"),
)

// Context is preserved
resp, err = client.Chat(ctx, "kimi", "What's my name?",
    sigoclient.WithSession("my-conversation"),
)
```

### With Timeout

```go
// Client-level timeout
client := sigoclient.New("http://127.0.0.1:9080",
    sigoclient.WithTimeout(30*time.Second),
)

// Or request-level timeout
ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()
resp, err := client.Chat(ctx, "kimi", "Hello")
```

### Error Handling

```go
resp, err := client.Chat(ctx, "unknown-model", "Hello")
if err != nil {
    if sigoErr, ok := sigoclient.IsError(err); ok {
        fmt.Printf("API Error %d: %s\n", sigoErr.StatusCode, sigoErr.Message)
    } else {
        fmt.Printf("Request failed: %v\n", err)
    }
}
```

### List Available Models

```go
models, err := client.ListModels(ctx)
if err != nil {
    log.Fatal(err)
}

for _, model := range models {
    fmt.Printf("%s: $%.2f/M in, $%.2f/M out\n",
        model.Shortcode, model.InputCost, model.OutputCost)
}
```

### Health Check

```go
// Simple ping
if client.Ping(ctx) {
    fmt.Println("Server is alive!")
}

// Detailed health
health, err := client.Health(ctx)
if err != nil {
    log.Fatal(err)
}
fmt.Printf("Models available: %d\n", health.AvailableModels)
fmt.Printf("Circuit breakers: %d\n", len(health.CircuitBreakers))
```

### Memory Management

```go
// Get global memory
mem, err := client.GetMemory(ctx)
if err != nil {
    log.Fatal(err)
}
fmt.Printf("Current memory: %s\n", mem.Content)

// Set global memory
mem, err = client.SetMemory(ctx, "Always respond in German.", true)
if err != nil {
    log.Fatal(err)
}
```

## API Reference

### Creating a Client

```go
// Default client
client := sigoclient.New("http://127.0.0.1:9080")

// With custom timeout
client := sigoclient.New("http://127.0.0.1:9080",
    sigoclient.WithTimeout(60*time.Second),
)

// With custom HTTP client
client := sigoclient.New("http://127.0.0.1:9080",
    sigoclient.WithHTTPClient(&http.Client{
        Timeout: 30 * time.Second,
        Transport: &http.Transport{
            TLSClientConfig: &tls.Config{...},
        },
    }),
)
```

### Chat Options

| Option | Description |
|--------|-------------|
| `WithSession(id)` | Set session ID for conversation continuity |
| `WithSystemPrompt(prompt)` | Add system prompt |
| `WithTemperature(t)` | Set temperature (0.0-2.0) |
| `WithMaxTokens(n)` | Set max tokens to generate |
| `WithTimeoutSeconds(s)` | Set request timeout |
| `WithRetries(n)` | Set number of retries |

### Methods

- `Ping(ctx) bool` - Check if server is alive
- `Health(ctx) (*HealthResponse, error)` - Get detailed health status
- `ListModels(ctx) ([]ModelInfo, error)` - List all available models
- `Chat(ctx, model, message, opts...) (*ChatResponse, error)` - Send chat request
- `GetMemory(ctx) (*MemoryBlock, error)` - Get global memory
- `SetMemory(ctx, content, cache) (*MemoryBlock, error)` - Set global memory

## Running Examples

Make sure sigoREST server is running:
```bash
sudo systemctl start sigoREST
```

Then run examples:
```bash
cd /u/go-projekte/sigoREST/clients/go

# Basic chat
go run examples/basic/main.go

# Session-based conversation
go run examples/session/main.go

# List models
go run examples/listmodels/main.go
```

## Error Handling

The client returns `*sigoclient.Error` for API errors:

```go
type Error struct {
    Message    string
    StatusCode int
    Response   map[string]interface{}
}
```

Use `IsError()` to check if an error is a sigoREST error:

```go
resp, err := client.Chat(ctx, "model", "message")
if err != nil {
    if sigoErr, ok := sigoclient.IsError(err); ok {
        // Handle API error
        fmt.Printf("Status: %d, Message: %s\n", sigoErr.StatusCode, sigoErr.Message)
    } else {
        // Handle other errors (network, etc.)
        fmt.Printf("Error: %v\n", err)
    }
}
```

## Requirements

- Go 1.26+
- Running sigoREST server

## License

Copyright 2025 Gerhard Quell - SKEQuell
