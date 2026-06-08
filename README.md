# copilot-openai-proxy

[![Go Version](https://img.shields.io/badge/Go-1.26+-00ADD8?style=flat&logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)

Convert Microsoft Copilot (copilot.microsoft.com) WebSocket completions into an OpenAI-compatible HTTP API.

> [中文说明](#中文说明)

## Features

- **OpenAI-compatible API** — drop-in replacement for `/v1/chat/completions` and `/v1/models`
- **Streaming support** — SSE (Server-Sent Events) for real-time token delivery
- **Model selection** — `smart` (default), `creative`, `balanced`, `precise`
- **API key auth** — optional `Authorization: Bearer <key>` gating
- **Session pooling** — configurable session TTL and max concurrent sessions
- **Single binary** — zero runtime dependencies, just build and run
- **Docker-ready** — multi-stage Alpine build included

## Quick Start

### Build from Source

```bash
git clone https://github.com/6Kmfi6HP/copilot-openai-proxy.git
cd copilot-openai-proxy
make build
./copilot-openai-proxy
```

Or with Go install:

```bash
go install copilot-openai-proxy/cmd/copilot-openai-proxy@latest
```

### Docker

```bash
docker build -t copilot-openai-proxy .
docker run --rm -p 8080:8080 copilot-openai-proxy
```

### Command Line Flags

```
-api-key string       API key; when set, requests must include Authorization header
-cleanup-interval int session cleanup interval in seconds (default 300)
-conn-timeout int     WebSocket connection timeout in seconds (default 20)
-debug                enable raw protocol logging
-host string          listen host (default "127.0.0.1")
-max-sessions int     maximum in-memory sessions (default 1000)
-port string          listen port (default "8080")
-session-ttl int      session TTL in seconds (default 1800)
-timeout int          request timeout in seconds (default 120)
-timezone string      timezone sent to Copilot (default "Asia/Shanghai")
```

### Example Requests

```bash
# Non-streaming
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer sk-your-key" \
  -H "Content-Type: application/json" \
  -d '{"model":"smart","messages":[{"role":"user","content":"hello"}]}'

# Streaming
curl http://localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer sk-your-key" \
  -H "Content-Type: application/json" \
  -d '{"model":"smart","messages":[{"role":"user","content":"hello"}],"stream":true}'

# List models
curl http://localhost:8080/v1/models \
  -H "Authorization: Bearer sk-your-key"
```

### Supported Models

| Model      | Description         |
|------------|---------------------|
| `smart`    | Default, balanced   |
| `creative` | Creative mode       |
| `balanced` | Balanced mode       |
| `precise`  | Precise mode        |

## Architecture

```
┌──────────┐     OpenAI API      ┌──────────────┐     WebSocket      ┌────────────────────┐
│  Client  │ ──── POST /v1/ ───►│   Proxy      │ ──────────────────►│  copilot.microsoft  │
│ (curl等) │ ◄── SSE / JSON ──── │   Server     │ ◄── appendText ─── │  .com/c/api/chat    │
└──────────┘                     └──────────────┘    done/error ───  └────────────────────┘
```

### Protocol Flow

1. **Anonymous auth**: `POST https://copilot.microsoft.com/c/api/start` → get `__Host-copilot-anon` cookie + `currentConversationId`
2. **Establish WebSocket**: `wss://copilot.microsoft.com/c/api/chat` (with cookie)
3. **Wait for connection**: receive `{"event":"connected",...}`
4. **Set options**: send `{"type":"setOptions","options":{...}}`
5. **Hashcash challenge** (may occur): solve and respond
6. **Send prompt**: `{"type":"send","text":"user prompt","conversationId":"..."}`
7. **Stream response**: `appendText` → `partCompleted` → `done`

## Project Structure

```
cmd/copilot-openai-proxy/main.go     # Entry point
internal/
  config/config.go                    # CLI flag configuration
  copilot/
    client.go                         # WebSocket client + session pool
    errors.go                         # Upstream error types
    protocol.go                       # WebSocket protocol message definitions
    websocket.go                      # WebSocket helpers
  openai/
    handler.go                        # OpenAI API types and route handlers
    models.go                         # Model list and health check
    sse.go                            # SSE writer
  server/
    server.go                         # HTTP server and routing
    middleware.go                      # Auth middleware
  util/
    id.go                             # chatcmpl- UUID generator
```

## Development

```bash
make build    # Build binary
make test     # Run tests
make fmt      # Format code
make vet      # Run go vet
make lint     # Run golangci-lint (requires golangci-lint)
make run      # Build & run
make help     # Show all targets
```

## Known Issues

**Hashcash Challenge**: Copilot recently enabled hashcash anti-abuse protection. The current implementation's response format (`{"type":"answer","answer":"<nonce>"}`) may cause an `invalid-event` error. Workaround: use the reference `copilot-openai-proxy-darwin-arm64` binary which handles this correctly.

## License

[Apache License 2.0](LICENSE)

---

## 中文说明

将 Microsoft Copilot (copilot.microsoft.com) 的 WebSocket 补全接口转换为 OpenAI 兼容的 HTTP API。

### 使用方法

```bash
# 构建
make build

# 运行（默认监听 127.0.0.1:8080）
./copilot-openai-proxy

# 带选项运行
./copilot-openai-proxy -host 0.0.0.0 -port 9090 -api-key sk-your-key -debug
```

### 命令行参数

```
-api-key string       API 密钥；设置后请求需带 Authorization header
-cleanup-interval int 会话清理间隔（秒）(default 300)
-conn-timeout int     WebSocket 连接超时（秒）(default 20)
-debug                打印原始协议日志
-host string          监听主机地址 (default "127.0.0.1")
-max-sessions int     最大内存会话数 (default 1000)
-port string          监听端口 (default "8080")
-session-ttl int      会话过期时间（秒）(default 1800)
-timeout int          请求超时（秒）(default 120)
-timezone string      发送到 Copilot 的时区 (default "Asia/Shanghai")
```

### 已知问题

**Hashcash 挑战**：Copilot 最近启用了 hashcash 反滥用保护。当前实现的 hashcash 答复格式可能导致 `invalid-event` 错误，需要进一步调试正确的答复格式。