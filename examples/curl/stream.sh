#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${OPENAI_BASE_URL:-http://127.0.0.1:8080/v1}"
API_KEY="${OPENAI_API_KEY:-sk-change-me}"

curl "${BASE_URL}/chat/completions" \
  -H "Authorization: Bearer ${API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "precise",
    "stream": true,
    "messages": [
      {"role": "user", "content": "Explain how reverse proxies help outbound traffic."}
    ]
  }'
