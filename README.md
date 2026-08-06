# copilot-openai-proxy

[![CI](https://github.com/6Kmfi6HP/copilot-openai-proxy/actions/workflows/ci.yml/badge.svg)](https://github.com/6Kmfi6HP/copilot-openai-proxy/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/6Kmfi6HP/copilot-openai-proxy)](https://github.com/6Kmfi6HP/copilot-openai-proxy/releases)
[![GHCR](https://img.shields.io/badge/GHCR-ready-black)](https://github.com/6Kmfi6HP/copilot-openai-proxy/pkgs/container/copilot-openai-proxy)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)

Convert Microsoft Copilot WebSocket completions into an OpenAI-compatible HTTP API.

This project is useful when you want to point OpenAI SDKs, curl scripts, or existing tooling at a self-hosted proxy endpoint instead of calling Copilot directly.

> 中文说明见下方的 [中文使用说明](#中文使用说明)

## Features

- OpenAI-compatible endpoints: `POST /v1/chat/completions`, `GET /v1/models`
- Streaming SSE responses
- Vision input via OpenAI `image_url` data URIs (PNG/JPEG → Copilot attachments)
- Dynamic `GET /v1/models` from Copilot's frontend conversation-mode catalog (cached)
- Optional bearer-token protection
- Multi-arch Docker image publishing to GHCR
- GitHub Releases with prebuilt binaries
- Docker Compose, Docker Swarm, and K3s deployment examples
- Outbound proxy support through environment variables

## Quick Start

### Run a released binary

Download a release archive from [GitHub Releases](https://github.com/6Kmfi6HP/copilot-openai-proxy/releases), unpack it, then:

```bash
./copilot-openai-proxy -api-key sk-change-me
```

### Build from source

```bash
git clone https://github.com/6Kmfi6HP/copilot-openai-proxy.git
cd copilot-openai-proxy
make build
./copilot-openai-proxy -api-key sk-change-me
```

### Install with Go

```bash
go install github.com/6Kmfi6HP/copilot-openai-proxy/cmd/copilot-openai-proxy@latest
```

### Run with Docker

```bash
docker run --rm -p 8080:8080 \
  -e API_KEY=sk-change-me \
  ghcr.io/6kmfi6hp/copilot-openai-proxy:latest
```

### Run with Docker Compose

```bash
cp .env.example .env
docker compose up -d
```

## Configuration

Flags and environment variables can be mixed. Command-line flags take precedence.

| Purpose | Flag | Environment variable | Default |
| --- | --- | --- | --- |
| Listen host | `-host` | `HOST` | `127.0.0.1` |
| Listen port | `-port` | `PORT` | `8080` |
| API key | `-api-key` | `API_KEY` | empty |
| Max sessions | `-max-sessions` | `MAX_SESSIONS` | `1000` |
| Warm idle sessions | `-warm-sessions` | `WARM_SESSIONS` | `4` |
| Session TTL | `-session-ttl` | `SESSION_TTL` | `1800` |
| Cleanup interval | `-cleanup-interval` | `CLEANUP_INTERVAL` | `300` |
| WebSocket connect timeout | `-conn-timeout` | `CONN_TIMEOUT` | `20` |
| Default acquire/start timeout | `-timeout` | `TIMEOUT` | `120` |
| WebSocket read timeout | `-ws-read-timeout` | `WS_READ_TIMEOUT` | `60` |
| WebSocket write timeout | `-ws-write-timeout` | `WS_WRITE_TIMEOUT` | `10` |
| WebSocket ping interval | `-ws-ping-interval` | `WS_PING_INTERVAL` | `25` |
| Time zone | `-timezone` | `TIMEZONE` | `Asia/Shanghai` |
| Debug logging | `-debug` | `DEBUG` | `false` |
| Explicit outbound proxy | `-proxy-url` | `PROXY_URL` | empty |

`WARM_SESSIONS` is capped at `MAX_SESSIONS`. `TIMEOUT` is used as the default session acquire/start budget when the incoming HTTP request does not already carry a deadline.

## Concurrency Semantics

- `WarmSessions` keeps a bounded pool of idle prewarmed sessions and is capped by `MaxSessions`.
- `WSReadTimeout` and `WSPingInterval` work together so dead peers are detected by read deadlines refreshed on pong frames.
- `WSWriteTimeout` bounds prompt sends, ping writes, and challenge replies on the single WebSocket writer goroutine.
- `SessionTTL` and `CleanupInterval` apply only to idle pooled sessions. Active leased sessions are not evicted by the janitor.
- When the proxy cannot acquire session capacity in time, it returns HTTP `503` with `Retry-After: 1`.
- When upstream start/send/read work exceeds its timeout budget, it returns HTTP `504`.
- During process shutdown, the proxy stops warm-pool refills, closes idle sessions immediately, and waits for leased sessions to drain within the shutdown context budget.

### Proxy Support

The proxy supports two ways to route outbound Copilot traffic:

1. Set `PROXY_URL` when you want to force a specific upstream proxy for both the Copilot HTTP start request and the WebSocket connection.
2. Use standard Go proxy environment variables such as `HTTP_PROXY`, `HTTPS_PROXY`, `ALL_PROXY`, and `NO_PROXY`.

If you want to set a proxy IP directly, use a full proxy URL instead of a bare IP. Supported schemes: `http`, `https`, `socks5`, `socks5h`. Example:

```bash
PROXY_URL=http://192.168.1.10:7890
```

SOCKS5 with username/password:

```bash
PROXY_URL='socks5://user:pass@192.168.1.10:1090'
```

Example:

```bash
API_KEY=sk-change-me \
PROXY_URL=http://192.168.1.10:7890 \
./copilot-openai-proxy -host 0.0.0.0 -port 8080
```

### Advanced QA Overrides

For deterministic local QA only, you can override the upstream start and WebSocket endpoints with environment variables. There are no matching CLI flags for these advanced overrides.

```bash
COPILOT_START_URL=http://127.0.0.1:8081/c/api/start
COPILOT_WS_URL=ws://127.0.0.1:8081/c/api/chat
```

When unset, the proxy uses the normal Microsoft Copilot endpoints.

## API Examples

### Non-streaming completion

```bash
curl http://127.0.0.1:8080/v1/chat/completions \
  -H "Authorization: Bearer sk-change-me" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "smart",
    "messages": [
      {"role": "user", "content": "hello"}
    ]
  }'
```

### Streaming completion

```bash
curl http://127.0.0.1:8080/v1/chat/completions \
  -H "Authorization: Bearer sk-change-me" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "reasoning",
    "stream": true,
    "messages": [
      {"role": "user", "content": "stream a short answer"}
    ]
  }'
```

### List models

Model IDs are discovered automatically from Copilot's public web bundle (conversation modes such as `smart`, `reasoning`, `coco`, `search`, `study`, …) and cached for about an hour. Legacy request aliases `creative` / `balanced` / `precise` still work for chat completions but are not guaranteed to appear in this list.

```bash
curl http://127.0.0.1:8080/v1/models \
  -H "Authorization: Bearer sk-change-me"
```

More examples:

- [examples/curl/chat-completions.sh](examples/curl/chat-completions.sh)
- [examples/curl/stream.sh](examples/curl/stream.sh)
- [examples/curl/models.sh](examples/curl/models.sh)
- [examples/python/openai_client.py](examples/python/openai_client.py)
- [examples/node/openai-client.mjs](examples/node/openai-client.mjs)

## Deployment

The repository includes three deployment formats:

- `docker-compose.yml`: root-level quick-start Compose file
- `deploy/swarm/docker-stack.yml`: single-manager Docker Swarm stack example
- `deploy/k3s/`: single-node K3s manifests

### Docker Compose

```bash
cp .env.example .env
docker compose up -d
docker compose logs -f
```

Compose-specific example files are also available in `deploy/compose/`.

### Docker Swarm

Single-node Swarm deployment:

```bash
docker swarm init
cp deploy/swarm/.env.example deploy/swarm/.env
set -a
source deploy/swarm/.env
set +a
docker stack deploy -c deploy/swarm/docker-stack.yml copilot
docker service ls
```

The stack example pins the service to the manager node and runs a single replica.

### K3s

Single-node K3s deployment:

```bash
curl -sfL https://get.k3s.io | sh -
sudo k3s kubectl apply -k deploy/k3s
sudo k3s kubectl -n copilot-openai-proxy get pods,svc
```

Before applying, edit `deploy/k3s/configmap.yaml` and `deploy/k3s/secret.example.yaml` to set `API_KEY` and the proxy fields you need.

After deployment on a single-node K3s cluster, the service is exposed through `NodePort 30080` by default:

```bash
curl http://<node-ip>:30080/healthz
```

### Health Check

All deployment formats expose the same health endpoint:

```bash
curl http://127.0.0.1:8080/healthz
```

## Release Automation

This repository ships with two GitHub Actions workflows:

- `CI`: runs `go vet`, `go test -race`, `go build`, `docker build`, and `docker compose config`
- `Release`: when you push a tag like `v0.2.0`, GitHub Actions will:
  - build release archives for `linux`, `darwin`, and `windows`
  - publish a GitHub Release with checksums
  - build and push `linux/amd64` and `linux/arm64` images to `ghcr.io`

Release maintainers only need:

```bash
git tag v0.2.0
git push origin v0.2.0
```

## Development

```bash
make fmt
make vet
make test
make docker
make compose-config
```

Contribution guide: [CONTRIBUTING.md](CONTRIBUTING.md)

## Compatibility Notes

- This is an unofficial Copilot proxy. Upstream protocol or anti-abuse changes may require updates in this project.
- Tool calling and function calling are not implemented.
- The proxy focuses on chat completions and model listing, not the full OpenAI API surface.
- `/v1/models` is scraped from Copilot's frontend JS catalog (not a first-party models API). On fetch failure the proxy serves the last successful list, or `smart` as a last resort.
- **Vision input:** OpenAI multimodal `image_url` parts are supported when the URL is a `data:image/png` or `data:image/jpeg` (also `image/jpg`) base64 data URI. Images are uploaded to Copilot `POST /c/api/attachments` with the anonymous session cookie, then attached on the WebSocket `send` as `{type:"image",url:...}` before the text part. Limits: up to 4 images per request, 10 MiB each. External `https://` image URLs are rejected (not fetched).
- When upstream Copilot generates an image, the proxy embeds it as Markdown `![image](url)` inside `message.content` / SSE `delta.content`. There is no `/v1/images/generations` endpoint.

## Project Layout

```text
cmd/copilot-openai-proxy/     CLI entrypoint
internal/config/              configuration loading
internal/copilot/             Copilot start/session/WebSocket logic
internal/openai/              OpenAI-compatible handlers and SSE output
internal/server/              HTTP server wiring and middleware
examples/                     curl, Python, and Node usage samples
.github/workflows/            CI and release automation
deploy/                       compose, swarm, and k3s deployment files
```

## License

[Apache License 2.0](LICENSE)

## 中文使用说明

这个项目把 Microsoft Copilot 的 WebSocket 补全能力转换成 OpenAI 兼容的 HTTP API，适合直接给现有 OpenAI SDK、脚本或网关接入。

### 快速启动

```bash
git clone https://github.com/6Kmfi6HP/copilot-openai-proxy.git
cd copilot-openai-proxy
make build
./copilot-openai-proxy -api-key sk-change-me
```

或者直接使用已发布镜像：

```bash
docker run --rm -p 8080:8080 \
  -e API_KEY=sk-change-me \
  ghcr.io/6kmfi6hp/copilot-openai-proxy:latest
```

### 代理接入

如果你的服务器必须走外部代理访问 Copilot，可以直接配置环境变量：

```bash
PROXY_URL=http://192.168.1.10:7890 \
API_KEY=sk-change-me \
./copilot-openai-proxy
```

也就是说，项目支持设置代理 IP，但要写成完整 URL，而不是只写裸 IP。支持 `http` / `https` / `socks5` / `socks5h`。例如：

```bash
PROXY_URL=http://192.168.1.10:7890
PROXY_URL='socks5://user:pass@192.168.1.10:1090'
```

也支持标准代理变量：

- `HTTP_PROXY`
- `HTTPS_PROXY`
- `ALL_PROXY`
- `NO_PROXY`

### Docker Compose 部署

```bash
cp .env.example .env
docker compose up -d
```

`.env.example` 里已经包含常用部署参数和代理参数，直接填值即可。

另外仓库还提供了两套单机部署示例：

### Docker Swarm 单机部署

```bash
docker swarm init
cp deploy/swarm/.env.example deploy/swarm/.env
set -a
source deploy/swarm/.env
set +a
docker stack deploy -c deploy/swarm/docker-stack.yml copilot
docker service ls
```

### K3s 单机部署

```bash
curl -sfL https://get.k3s.io | sh -
sudo k3s kubectl apply -k deploy/k3s
sudo k3s kubectl -n copilot-openai-proxy get pods,svc
```

部署前请先修改：

- `deploy/k3s/configmap.yaml`
- `deploy/k3s/secret.example.yaml`

这里面已经预留了 `PROXY_URL`、`HTTP_PROXY`、`HTTPS_PROXY`、`ALL_PROXY`、`NO_PROXY` 等参数。

默认通过单机节点的 `30080` 端口访问：

```bash
curl http://<节点IP>:30080/healthz
```

### Release 发布

仓库已经配置好 GitHub Actions：

- 推送 `main/master` 会自动跑 CI
- 推送 `vX.Y.Z` tag 会自动：
  - 编译多平台二进制
  - 发布 GitHub Release
  - 构建并推送 `linux/amd64` 和 `linux/arm64` Docker 镜像到 GHCR

发布命令：

```bash
git tag v0.2.0
git push origin v0.2.0
```
