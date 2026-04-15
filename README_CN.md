# sigoREST

sigoEngine 的 REST 服务器。统一兼容 OpenAI 的 API，支持约 100 个并行连接。
基于 IP 的访问控制，全局记忆块用于提示缓存。

> **关于本项目**：sigoREST 由 Gerhard 使用 **Claude Code** 和 **Kimi** 联合开发。我们特别重视对中国大语言模型的原生支持 — 包括 Moonshot.ai 的 Kimi 和 DeepSeek 等中国模型。

## 架构

```
sigorest/
├── sigoengine/
│   ├── engine.go              # 共享包（API 调用、会话、熔断器）
│   ├── models.go              # 模型结构 + CoreModels（CLI 后备）
│   ├── models_registry.go     # 注册表逻辑（查找、短代码）
│   └── provider_fetchers.go   # 提供商获取器（Mammouth、Moonshot、ZAI）
├── cmd/sigoE/main.go          # CLI 包装器
└── sigoREST/
    ├── main.go                # REST 服务器
    └── memory.json            # 全局记忆块
```

## 安装

### 系统范围安装（推荐）

**sigoREST 服务器**（作为 systemd 服务）：
```bash
# 编译并安装二进制文件
go build -o sigoREST/sigoREST ./sigoREST/
sudo cp sigoREST/sigoREST /usr/local/sbin/sigoREST

# 创建配置
sudo mkdir -p /usr/local/slib/sigoREST/certs
sudo cp sigoREST/memory.json /usr/local/slib/sigoREST/

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

# 运行 REST 服务器
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
| `-v` | `info` | 日志级别：`debug\|info\|warn\|error` |
| `-q` | — | 静默模式（仅错误） |
| `-j` | — | JSON 格式日志 |
| `-version` | — | 显示版本并退出 |

## CLI 参数 (sigoE)

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `-m` | `gpt41` | 模型（短代码或完整名称） |
| `-s` | — | 会话 ID，用于对话历史 |
| `-n` | `0` | 最大令牌数（0 = 模型默认值） |
| `-T` | `-1` | 温度（-1 = 模型默认值） |
| `-t` | `180` | 超时时间（秒） |
| `-r` | `3` | 重试次数 |
| `-v` | `info` | 日志级别：`debug\|info\|warn\|error` |
| `-V` | — | **显示版本** |
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

## 配置文件

两个文件：磁盘文件优先于嵌入式默认值。

### 动态模型发现

服务器启动时，模型自动从以下提供商加载：

| 提供商 | 模型 | 认证 |
|--------|------|------|
| Mammouth | ~67 个模型（GPT、Claude、Gemini、Grok、DeepSeek 等） | `MAMMOUTH_API_KEY` |
| Moonshot | ~13 个模型（Kimi、moonshot-v1-*） | `MOONSHOT_API_KEY` |
| ZAI | ~7 个模型（GLM 系列） | `ZAI_API_KEY` |
| Ollama | 本地可用模型 | — |

如果某个提供商无法访问，服务器仍会使用其余模型启动。

### memory.json
所有请求的全局系统上下文（始终首先插入）：
```json
{
  "content": "Always respond in German. You are speaking with Gerhard.",
  "cache": true
}
```
`cache: true` → Anthropic 临时缓存。OpenAI 从 1024 个令牌开始自动缓存。

## API 端点

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
    "system_prompt": "可选：覆盖全局系统提示词"
  }'
```

`session_id`、`timeout`、`retries`、`system_prompt` 是 sigoREST 扩展 — 所有其他字段均为标准 OpenAI。

### GET /v1/models
```bash
curl -s http://localhost:9080/v1/models
```
兼容 OpenAI 的模型列表（仅白名单）。

### GET /api/models
```bash
curl -s http://localhost:9080/api/models
```
完整的模型信息：价格、令牌限制、温度范围。

### GET /ping
```bash
curl -s http://localhost:9080/ping
```
简单的健康检查，供负载均衡器使用。响应 `pong`。

### GET /api/health
```bash
curl -s http://localhost:9080/api/health
```
服务器状态、模型数量、熔断器状态。

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
在运行时更改记忆块并写入磁盘。

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
设置全局系统提示词并保存到 `system-prompt.txt`。可在每次请求中覆盖。

## 客户端示例

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

## 模型

模型在服务器启动时从提供商动态加载（约 84 个模型）。
当前列表：
```bash
curl -s http://localhost:9080/v1/models | jq '.data[].id'
```

**示例：**

| 短代码 | 模型 | 提供商 |
|--------|------|--------|
| `gpt41` | gpt-4.1 | Mammouth |
| `claude-h` | claude-3-5-haiku-20241022 | Mammouth |
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
  -d '{"model":"ollama-gemma3-4b","messages":[{"role":"user","content":"Hello"}]}'
```

## 会话管理

会话以 JSON 文件形式存储：`.sessions/<模型>-<会话ID>.json`
每个会话最多 20 条消息（最旧的消息自动丢弃）。

```bash
# 查看会话
cat .sessions/claude-h-my-project.json

# 删除会话
rm .sessions/claude-h-my-project.json
```

## 环境变量

```bash
export MAMMOUTH_API_KEY=...   # Mammoth.ai（GPT、Claude、Gemini、Grok、DeepSeek 等）
export MOONSHOT_API_KEY=...   # Moonshot.ai（Kimi）— 中国领先的大语言模型
export ZAI_API_KEY=...        # Z.ai（GLM）— 智谱 AI
```

## systemd 服务

对于生产环境，建议将 sigoREST 作为 systemd 服务运行：
- 二进制文件：`/usr/local/sbin/sigoREST`
- 数据/配置：`/usr/local/slib/sigoREST/`
- CLI 客户端：`/usr/local/bin/sigoE`

服务文件示例（`/etc/systemd/system/sigorest.service`）：
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

详细说明：[`docs/systemd-install.md`](docs/systemd-install.md)

快速开始：
```bash
sudo systemctl start sigoREST
sudo systemctl enable sigoREST
journalctl -u sigoREST -f
```
