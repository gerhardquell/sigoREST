# sigoREST

sigoEngine 的 REST 服务器。统一兼容 OpenAI 的 API，支持约 100 个并行连接。
基于 IP 的访问控制，全局和渠道特定的记忆块，支持多渠道和故障转移。

> **关于本项目**：sigoREST 由 Gerhard 使用 **Claude Code** 和 **Kimi** 联合开发。我们特别重视对中国大语言模型的原生支持 — 包括 Moonshot.ai 的 Kimi 和 DeepSeek 等中国模型。

## 架构

```
sigorest/
├── sigoengine/
│   ├── engine.go              # 共享包（API 调用、会话、熔断器、错误处理）
│   ├── models.go              # 模型结构 + CoreModels（CLI 后备）
│   ├── models_registry.go     # 注册表逻辑（查找、短代码）
│   ├── provider_fetchers.go   # 提供商获取器（Mammouth、Moonshot、ZAI）
│   ├── channel.go             # 渠道、渠道注册表、环境发现
│   ├── channel_manager.go     # 渠道解析和故障转移
│   ├── channel_health.go      # 后台健康监控
│   ├── session_memory.go      # 每个渠道的会话/记忆路径
│   ├── env.go                 # 可选的 ./env 文件
│   └── version.go             # 中央版本常量
├── cmd/sigoE/main.go          # CLI 包装器
└── sigoREST/
    ├── main.go                # REST 服务器
    └── memory.json            # 默认全局记忆块（嵌入式）
```

## 安装

### 系统范围安装（推荐）

**sigoREST 服务器**（作为 systemd 服务）：
```bash
# 编译并安装二进制文件
go build -o sigoREST/sigoREST ./sigoREST/
sudo cp sigoREST/sigoREST /usr/local/sbin/sigoREST

# 创建数据目录
sudo mkdir -p /var/sigoREST
sudo chown -R sigorest:sigorest /var/sigoREST

# 设置为 systemd 服务（请参阅 docs/systemd-install.md）
```

**sigoE CLI**：
```bash
# 编译并安装二进制文件
go build -o cmd/sigoE/sigoE ./cmd/sigoE/
sudo cp cmd/sigoE/sigoE /usr/local/bin/sigoE
```

### 开发（本地）

```bash
# 构建所有包
go build ./...

# 启动 REST 服务器
./sigoREST/sigoREST -v debug

# 使用 CLI
./cmd/sigoE/sigoE -l
```

## 服务器参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `-http-port` | `9080` | HTTP（仅本地 127.0.0.0/8） |
| `-https-port` | `9443` | HTTPS（私有网络 192.168.0.0/16, 10.0.0.0/8） |
| `-cert` | `./certs/server.crt` | TLS 证书（首次启动时自动生成） |
| `-key` | `./certs/server.key` | TLS 密钥 |
| `-data-dir` | `/var/sigoREST` | 记忆、系统提示词、channels.json、会话的基目录 |
| `-channel-health-interval` | `30s` | 渠道健康检查间隔 |
| `-v` | `info` | 日志级别：`debug\|info\|warn\|error` |
| `-q` | — | 静默模式（仅错误） |
| `-j` | — | JSON 格式日志 |
| `-version` | — | 显示版本并退出 |

## CLI 参数 (sigoE)

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `-m` | `gpt41` | 模型（短代码或完整名称） |
| `-s` | — | 会话 ID，用于对话历史 |
| `-session-dir` | `.sessions/` | 会话文件目录 |
| `-c` | — | 选择渠道，例如 `mammouth-0` |
| `-n` | `0` | 最大令牌数（0 = 模型默认值） |
| `-T` | `-1` | 温度（-1 = 模型默认值） |
| `-t` | `180` | 超时时间（秒） |
| `-r` | `3` | 重试次数 |
| `-v` | `info` | 日志级别：`debug\|info\|warn\|error` |
| `-V` / `-version` | — | 显示版本 |
| `-j` | — | JSON 输出 |
| `-q` | — | 静默模式（仅错误） |
| `-l` | — | 列出所有可用模型 |
| `-i` | — | 显示模型信息 |
| `-h` | — | 显示帮助 |
| `-sp` | — | 系统提示词 |

## 访问控制

| 端口 | 协议 | 允许的 IP |
|------|------|----------|
| 9080 | HTTP | 127.0.0.0/8（本地） |
| 9443 | HTTPS | 192.168.0.0/16, 10.0.0.0/8 |
| 两者 | — | IPv6 被阻止（::1 除外） |

## 配置

### 环境变量 / API 密钥

sigoREST 按以下顺序读取 API 密钥：

1. 启动目录中的可选 `env` 文件（`./env`）
2. 真实的环境变量

```bash
MAMMOUTH_API_KEY=sk-...          # Mammoth.ai（GPT、Claude、Gemini、Grok、DeepSeek 等）
MAMMOUTH_API_KEY_0=sk-...        # 额外渠道 0
MAMMOUTH_API_KEY_1=sk-...        # 额外渠道 1
MOONSHOT_API_KEY=sk-...          # Moonshot.ai（Kimi）
ZAI_API_KEY=sk-...               # Z.ai（GLM）
```

带索引的密钥（`_0`、`_1`、...）会创建额外的渠道。无索引的密钥成为 `default` 渠道。

### 动态模型发现

服务器启动时，模型自动从以下提供商加载：

| 提供商 | 模型 | 认证 |
|--------|------|------|
| Mammouth | ~67 个模型（GPT、Claude、Gemini、Grok、DeepSeek 等） | `MAMMOUTH_API_KEY` |
| Moonshot | ~13 个模型（Kimi、moonshot-v1-*） | `MOONSHOT_API_KEY` |
| ZAI | ~7 个模型（GLM 系列） | `ZAI_API_KEY` |
| Ollama | 本地可用模型 | — |

如果某个提供商无法访问，服务器仍会使用其余模型启动。

### 数据目录（`-data-dir`）

默认：`/var/sigoREST`

```text
/var/sigoREST/
├── channels.json                     # 渠道持久化激活状态
├── memory.json                       # 全局记忆块
├── system-prompt.txt                 # 全局系统提示词
├── channels/
│   └── <provider>/
│       └── <channel>/
│           ├── memory.json           # 渠道特定的记忆
│           └── system-prompt.txt     # 渠道特定的系统提示词
└── sessions/
    └── <provider>/
        └── <channel>/
            └── <model>-<session>.json
```

### memory.json（全局）

所有请求的全局系统上下文（始终首先插入）：
```json
{
  "content": "Antworte immer auf Deutsch. Du sprichst mit Gerhard, einem erfahrenen Software-Entwickler.",
  "cache": true
}
```
`cache: true` → Anthropic 临时缓存。OpenAI 从 1024 个令牌开始自动缓存。

## 多渠道支持

每个提供商可以管理多个 API 密钥渠道。每个渠道都有自己的 API 密钥、记忆、会话和熔断器。

### 标准行为

- 只有无索引的密钥（`MAMMOUTH_API_KEY`）作为 `default` 渠道被激活。
- 备用渠道（`_0`、`_1`、...）处于非活动状态，但可以手动或自动激活。

### 渠道管理

```bash
# 显示所有渠道
curl -s http://localhost:9080/api/channels

# 显示单个渠道
curl -s http://localhost:9080/api/channels/mammouth/0

# 激活渠道
curl -s -X POST http://localhost:9080/api/channels/mammouth/0/enable

# 停用渠道
curl -s -X POST http://localhost:9080/api/channels/mammouth/0/disable

# 设置渠道记忆
curl -s -X PUT http://localhost:9080/api/channels/mammouth/0/memory \
  -H "Content-Type: application/json" \
  -d '{"content":"渠道特定的上下文","cache":false}'

# 设置渠道系统提示词
curl -s -X PUT http://localhost:9080/api/channels/mammouth/0/system-prompt \
  -H "Content-Type: application/json" \
  -d '{"system_prompt":"像一个海盗一样回答。"}'
```

### 自动故障转移

如果渠道在请求期间失败（速率限制、超时、服务器错误），sigoREST 会自动尝试下一个活动渠道。身份验证错误会立即持久化停用受影响的渠道。

### 健康监控

后台进程按 `-channel-health-interval` 检查所有活动渠道。如果某个提供商的所有活动渠道都不健康，会自动激活下一个非活动备用渠道。

## 客户端库

各种编程语言的官方客户端：

| 语言 | 路径 | 安装 |
|---------|------|--------------|
| **Python** | [`clients/python/`](clients/python/) | `pip install clients/python/` |
| **Go** | [`clients/go/`](clients/go/) | `go get github.com/gquell/sigoclient` |
| **JavaScript** | [`clients/javascript/`](clients/javascript/) | 复制 `client.js` |
| **Common Lisp** | [`clients/clisp-exp/`](clients/clisp-exp/) | 实验性 |

### Python（v2 — 现代）

```python
from sigo_client import SigoClient

client = SigoClient()

# 普通
response = client.chat.completions.create(
    model="cl5-s",
    messages=[{"role": "user", "content": "Hallo"}]
)
print(response.content)

# 流式传输（真正的 SSE）
stream = client.chat.completions.create(..., stream=True)
for chunk in stream:
    print(chunk.content, end="", flush=True)
```

> 请参阅 [`clients/python/README.md`](clients/python/README.md) 了解 Async、详细文档和示例。

### Go 示例
```go
client := sigoclient.New("http://127.0.0.1:9080")
resp, err := client.Chat(ctx, "kimi", "Hello!")
fmt.Println(resp.Content)
```

### JavaScript 示例
```javascript
const client = new SigoClient('http://127.0.0.1:9080');
const response = await client.chat('kimi', 'Hello!');
console.log(response.content);
```

### Common Lisp 示例
```lisp
;; 前提条件 quickload 已安装
(ql:quickload :drakma)
(ql:quickload :yason)
(load "clients/clisp-exp/sigoclient.lisp")
(use-package :sigoclient)

;; Ping
(ping)  ; => T

;; Chat
(chat "kimi" "Hallo!")
; => "Hallo! Wie kann ich dir helfen?"
```

## API 端点

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
    "system_prompt": "可选：覆盖全局和渠道特定的系统提示词"
  }'
```

`sigoREST` 扩展：
- `channel` — 可选的渠道 FullName（例如 `mammouth-0`）。如果省略，使用第一个活动渠道。
- `session_id` — 会话 ID，用于每个渠道的隔离对话历史。
- `timeout` — 请求超时（秒）。
- `retries` — 每个渠道的重试次数。
- `system_prompt` — 每个请求的系统提示词（最高优先级）。

#### 视觉支持

sigoREST 支持 OpenAI Vision API 格式。图像可以作为 Base64 编码的数据 URL 发送：

```bash
curl -s http://localhost:9080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt4o",
    "messages": [{
      "role": "user",
      "content": [
        {"type": "text", "text": "在这张图片上你看到了什么？"},
        {"type": "image_url", "image_url": {
          "url": "data:image/jpeg;base64,/9j/4AAQ..."
        }}
      ]
    }],
    "max_tokens": 4096
  }'
```

**技术细节：**
- `ChatMessage.Content` 是 `json.RawMessage` — 字符串和 Vision 数组格式的透传
- 会话存储仅提取文本（会话中无图像数据）
- 推荐：JPEG 质量 75，约 100 DPI（每页约 80KB）
- 过大的图像（PNG 200+ DPI，>1MB）可能导致代理错误（413）

响应包含 `usage` 块（如果提供商提供令牌数据）：
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
兼容 OpenAI 的模型列表（ID + 短代码）。

### GET /api/models
```bash
curl -s http://localhost:9080/api/models
```
完整的模型信息：价格、令牌限制、温度范围。

### GET /api/version
```bash
curl -s http://localhost:9080/api/version
```
返回 `{"version":"1.1","component":"sigoREST"}`。

### GET /api/channels
```bash
curl -s http://localhost:9080/api/channels
```
所有渠道的状态列表。

### GET /api/channels/:provider/:name
```bash
curl -s http://localhost:9080/api/channels/mammouth/0
```
渠道的详细状态。

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
  -d '{"content":"渠道特定的上下文","cache":false}'
```

### GET/PUT /api/channels/:provider/:name/system-prompt
```bash
curl -s http://localhost:9080/api/channels/mammouth/0/system-prompt

curl -s -X PUT http://localhost:9080/api/channels/mammouth/0/system-prompt \
  -H "Content-Type: application/json" \
  -d '{"system_prompt":"像一个海盗一样回答。"}'
```

### GET /ping
```bash
curl -s http://localhost:9080/ping
```
简单的健康检查，供负载均衡器使用。响应 `pong`。

### GET /api/health
```bash
curl -s http://localhost:9080/api/health
```
服务器状态、模型数量、每个渠道/模型的熔断器状态。

### GET /api/memory
```bash
curl -s http://localhost:9080/api/memory
```

### PUT /api/memory
```bash
curl -s -X PUT http://localhost:9080/api/memory \
  -H "Content-Type: application/json" \
  -d '{"content":"新上下文","cache":true}'
```
在运行时更改全局记忆块并写入磁盘。

### GET /api/system-prompt
```bash
curl -s http://localhost:9080/api/system-prompt
```
读取当前全局系统提示词。

### PUT /api/system-prompt
```bash
curl -s -X PUT http://localhost:9080/api/system-prompt \
  -H "Content-Type: application/json" \
  -d '{"system_prompt":"你是一个有用的助手。"}'
```
设置全局系统提示词并保存到 `system-prompt.txt`。可以在每次请求或渠道中覆盖。

### GET /api/usage
```bash
curl -s http://localhost:9080/api/usage
```
自服务器启动以来的累计令牌统计 — 按模型、按渠道和总计。
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
注意：仅 RAM — 重启时重置。

### GET /api/help
```bash
curl -s http://localhost:9080/api/help
```
所有端点的文档（JSON 格式）。

## 客户端示例

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

## 模型

模型在服务器启动时从提供商动态加载（~89 个模型）。
当前列表：
```bash
curl -s http://localhost:9080/v1/models | jq '.data[].id'
```

**示例：**

| 短代码 | 模型 | 提供商 |
|--------|------|--------|
| `gpt41` | gpt-4.1 | Mammouth |
| `gpt4o` | gpt-4o | Mammouth |
| `cl46-s` | claude-sonnet-4-6 | Mammouth |
| `kimi` | kimi-k2.5 | Moonshot |
| `glm51` | glm-5.1 | ZAI |
| `ollama-gemma3` | gemma3:latest | Ollama（本地） |

## Ollama（本地大语言模型）

Ollama 模型在服务器启动时自动发现 — 无需 API 密钥，无需配置。

**前提条件：** Ollama 在 `http://localhost:11434` 运行

```bash
ollama serve   # 如果尚未作为服务运行
```

短代码格式：`ollama-<模型名>`（`:latest` 被截断，其他标签作为后缀）

| Ollama 模型 | 短代码 |
|------------|--------|
| `gemma3:4b` | `ollama-gemma3-4b` |
| `gemma3:12b` | `ollama-gemma3-12b` |
| `qwen3:latest` | `ollama-qwen3` |
| `qwen3:32b` | `ollama-qwen3-32b` |
| `devstral:latest` | `ollama-devstral` |
| `llama3.2-vision:latest` | `ollama-llama3.2-vision` |

当前检测到的模型列表：
```bash
curl -s http://localhost:9080/v1/models | python3 -c \
  "import sys,json; [print(m['id']) for m in json.load(sys.stdin)['data'] if m['id'].startswith('ollama-')]"
```

立即安装并使用新模型：
```bash
ollama pull llama3.3
# 重启服务器 — llama3.3 自动显示为 "ollama-llama3.3"
```

向本地模型发送请求：
```bash
curl -s http://localhost:9080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"ollama-gemma3-4b","messages":[{"role":"user","content":"Hallo"}]}'
```

## 会话管理

会话以 JSON 文件形式存储：

```text
<data-dir>/sessions/<provider>/<channel>/<model>-<sessionID>.json
```

每个会话最多 20 条消息（最旧的消息自动丢弃）。
会话在每个渠道隔离 — 不同渠道上相同的 `session_id` = 不同的文件。

```bash
# 查看会话
cat /var/sigoREST/sessions/mammouth/default/cl46-s-mein-projekt.json

# 删除会话
rm /var/sigoREST/sessions/mammouth/default/cl46-s-mein-projekt.json
```

## systemd 服务

对于生产环境，建议将 sigoREST 作为 systemd 服务运行：
- 二进制文件：`/usr/local/sbin/sigoREST`
- 数据：`/var/sigoREST/`
- 配置/环境变量：`/usr/local/slib/sigoREST/env`
- CLI 客户端：`/usr/local/bin/sigoE`

服务文件示例（`/etc/systemd/system/sigorest.service`）：
```ini
[Unit]
Description=sigoREST Server
# network-online.target 等待 DNS 可用
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

详细说明：[`docs/systemd-install.md`](docs/systemd-install.md)

快速开始：
```bash
sudo systemctl start sigoREST
sudo systemctl enable sigoREST
journalctl -u sigoREST -f
```