FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder

ARG TARGETOS
ARG TARGETARCH

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -trimpath -ldflags="-s -w" -o /out/copilot-openai-proxy ./cmd/copilot-openai-proxy

FROM alpine:3.21

RUN apk --no-cache add ca-certificates tzdata && \
    addgroup -S app && \
    adduser -S -G app app

ENV HOST=0.0.0.0
ENV PORT=8080
ENV TIMEZONE=UTC

COPY --from=builder /out/copilot-openai-proxy /usr/local/bin/copilot-openai-proxy

USER app

EXPOSE 8080

ENTRYPOINT ["copilot-openai-proxy"]
