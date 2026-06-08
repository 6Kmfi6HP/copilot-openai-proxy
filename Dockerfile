# Builder
FROM golang:1.26-alpine AS builder

WORKDIR /app

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

# Build
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /copilot-openai-proxy ./cmd/copilot-openai-proxy

# Runtime
FROM alpine:3.21

RUN apk --no-cache add ca-certificates

COPY --from=builder /copilot-openai-proxy /usr/local/bin/copilot-openai-proxy

EXPOSE 8080

ENTRYPOINT ["copilot-openai-proxy"]