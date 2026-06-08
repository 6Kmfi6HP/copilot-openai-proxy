# Contributing

## Development Setup

```bash
git clone https://github.com/6Kmfi6HP/copilot-openai-proxy.git
cd copilot-openai-proxy
go test ./...
make build
```

## Local Checks

```bash
make fmt
make vet
make test
make docker
make compose-config
```

## Release Flow

1. Update code and documentation on `main`.
2. Create a semver tag such as `v0.2.0`.
3. Push the tag to GitHub.
4. GitHub Actions will publish release archives and a multi-arch GHCR image automatically.

## Pull Requests

- Keep changes focused and reviewable.
- Add or update tests for behavior changes.
- Update examples or README when flags, env vars, or deployment steps change.
