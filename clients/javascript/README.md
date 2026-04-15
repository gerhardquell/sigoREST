# sigoclient - JavaScript Client for sigoREST

A simple, lightweight JavaScript client for the sigoREST OpenAI-compatible API.
Works in Node.js (18+) and modern browsers.

## Installation

### From source
```bash
cd /u/go-projekte/sigoREST/clients/javascript
npm install
```

### In your project
```bash
# Copy client.js to your project
cp /u/go-projekte/sigoREST/clients/javascript/client.js ./src/

# Or use directly via import
import { SigoClient } from './client.js';
```

## Quick Start

```javascript
import { SigoClient } from './client.js';

// Create client
const client = new SigoClient('http://127.0.0.1:9080');

// Simple chat
const response = await client.chat('kimi', 'Hello!');
console.log(response.content);
```

## Features

- ✅ Simple, intuitive API
- ✅ Works in Node.js and browsers
- ✅ Native fetch() - no dependencies
- ✅ Session management for conversation continuity
- ✅ Health checking (`ping()`, `health()`)
- ✅ Model listing with pricing info
- ✅ Global memory management
- ✅ Proper error handling with custom error classes
- ✅ AbortController support for timeouts

## Examples

### Basic Chat

```javascript
import { SigoClient } from './client.js';

const client = new SigoClient('http://127.0.0.1:9080');
const response = await client.chat(
  'kimi',
  'Explain quantum computing'
);
console.log(response.content);
```

### Session-based Conversation

```javascript
import { SigoClient } from './client.js';

const client = new SigoClient('http://127.0.0.1:9080');

// First message
const response1 = await client.chat(
  'kimi',
  'My name is Alice',
  { sessionId: 'my-conversation' }
);

// Context is preserved
const response2 = await client.chat(
  'kimi',
  "What's my name?",
  { sessionId: 'my-conversation' }
);
```

### With Options

```javascript
const response = await client.chat('gpt41', 'Explain async/await', {
  sessionId: 'my-session',
  temperature: 0.7,
  maxTokens: 1024,
  systemPrompt: 'You are a helpful expert.',
  timeout: 30000,  // 30 seconds
  retries: 3
});
```

### Error Handling

```javascript
import { SigoClient, SigoError, SigoAPIError } from './client.js';

const client = new SigoClient('http://127.0.0.1:9080');

try {
  const response = await client.chat('unknown-model', 'Hello');
} catch (error) {
  if (error instanceof SigoAPIError) {
    console.log(`API Error ${error.statusCode}: ${error.message}`);
  } else if (error instanceof SigoError) {
    console.log(`Client Error: ${error.message}`);
  } else {
    console.log(`Unexpected error: ${error}`);
  }
}
```

### List Available Models

```javascript
import { SigoClient } from './client.js';

const client = new SigoClient('http://127.0.0.1:9080');
const models = await client.listModels();

for (const model of models) {
  console.log(`${model.shortcode}: $${model.input_cost}/M in, $${model.output_cost}/M out`);
}
```

### Health Check

```javascript
import { SigoClient } from './client.js';

const client = new SigoClient('http://127.0.0.1:9080');

// Simple ping
if (await client.ping()) {
  console.log('Server is alive!');
}

// Detailed health
const health = await client.health();
console.log(`Models available: ${health.available_models}`);
console.log(`Circuit breakers: ${health.circuit_breakers.length}`);
```

### Set Global Memory

```javascript
import { SigoClient } from './client.js';

const client = new SigoClient('http://127.0.0.1:9080');

// Set system context for all requests
await client.setMemory(
  'Always respond in German. Be concise.',
  true  // cache
);
```

## Browser Usage

```html
<!DOCTYPE html>
<html>
<head>
  <title>sigoREST Client</title>
</head>
<body>
  <div id="response"></div>
  <script type="module">
    import { SigoClient } from './client.js';

    const client = new SigoClient('http://127.0.0.1:9080');

    async function chat() {
      try {
        const response = await client.chat('kimi', 'Hello!');
        document.getElementById('response').textContent = response.content;
      } catch (error) {
        document.getElementById('response').textContent = `Error: ${error.message}`;
      }
    }

    chat();
  </script>
</body>
</html>
```

## API Reference

### SigoClient

#### Constructor
```javascript
new SigoClient(baseUrl = 'http://127.0.0.1:9080', options = {})
```

Options:
- `timeout` - Default request timeout in milliseconds (default: 180000)

#### Methods

- `ping() → Promise<boolean>` - Check if server is alive
- `health() → Promise<object>` - Get detailed health status
- `listModels() → Promise<ModelInfo[]>` - List all available models
- `chat(model, message, options) → Promise<ChatResponse>` - Send chat request
- `getMemory() → Promise<MemoryBlock>` - Get global memory block
- `setMemory(content, cache) → Promise<MemoryBlock>` - Set global memory block

#### chat() Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `sessionId` | string | undefined | Session ID for continuity |
| `systemPrompt` | string | undefined | System prompt/context |
| `temperature` | number | undefined | Temperature (0.0-2.0) |
| `maxTokens` | number | undefined | Max tokens to generate |
| `timeout` | number | undefined | Request timeout in ms |
| `retries` | number | 3 | Number of retries |

## Error Classes

### SigoError
Base error class for all sigoREST errors.

### SigoAPIError
API error with additional properties:
- `statusCode` - HTTP status code
- `response` - Parsed error response (if available)

## Running Examples

Make sure sigoREST server is running:
```bash
sudo systemctl start sigoREST
```

Then run examples:
```bash
cd /u/go-projekte/sigoREST/clients/javascript

# Basic chat
node examples/basic.js

# Session-based conversation
node examples/session.js

# List models
node examples/listmodels.js
```

## Requirements

- Node.js 18+ (for native fetch() support)
- Or a modern browser with fetch() support
- Running sigoREST server

## License

Copyright 2025 Gerhard Quell - SKEQuell
