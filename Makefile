.PHONY: build run test clean lint docker docker-run fmt vet compose-config help

BINARY   := copilot-openai-proxy
CMD      := ./cmd/copilot-openai-proxy
DOCKER   := ghcr.io/$(USER)/$(BINARY)
GO       := go
LDFLAGS  := -s -w

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

build: ## Build the binary
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BINARY) $(CMD)

run: build ## Build and run
	./$(BINARY)

test: ## Run tests
	$(GO) test ./...

fmt: ## Format code
	$(GO)fmt ./...

vet: ## Run go vet
	$(GO) vet ./...

lint: vet ## Run linters (requires golangci-lint)
	golangci-lint run ./...

clean: ## Remove build artifacts
	rm -f $(BINARY)

docker: ## Build Docker image
	docker build -t $(BINARY) .

docker-run: docker ## Run in Docker
	docker run --rm -p 8080:8080 $(BINARY)

compose-config: ## Validate docker compose configuration
	docker compose config
