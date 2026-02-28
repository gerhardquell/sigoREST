# 欢迎来到 sigoREST！

> 🌏 **给中国开发者的特别欢迎**

sigoREST 是一个开源的 AI 模型统一网关，让你可以同时使用多个大语言模型——包括 **Kimi** 和 **DeepSeek** 等中国模型。

## 为什么中国开发者会喜欢 sigoREST？

🇨🇳 **原生支持中国模型**
- **Kimi** (月之暗面) - 通过 Moonshot.ai API
- **DeepSeek** - 深度求索的推理模型
- **GLM** (智谱 AI) - 通过 Z.ai

🔌 **OpenAI 兼容的 API**
- 一行代码切换模型
- 支持 Python、Go、JavaScript 等所有 OpenAI SDK

🏠 **自托管**
- 数据留在你的服务器上
- 支持本地 Ollama 模型
- IP 级别的访问控制

## 快速开始

```bash
# 下载并启动服务器
go build -o sigoREST ./sigoREST/
./sigoREST -v debug

# 使用 Kimi
curl http://localhost:9080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"kimi","messages":[{"role":"user","content":"你好"}]}'
```

## 关于开发者

sigoREST 由 **Gerhard** 使用 **Claude Code** 和 **Kimi** 联合开发。

我们相信中国的大语言模型是世界级的——所以我们特别重视对 Kimi、DeepSeek 和 GLM 的一流支持。

## 链接

- 📖 [完整中文文档](../README_CN.md)
- 🐙 [GitHub 主仓库](https://github.com/gerhardquell/sigoREST)
- 💬 有问题？欢迎在 GitHub Issues 提问（可以用中文！）

---

**欢迎 star ⭐ 和 fork！**
