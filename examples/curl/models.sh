#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${OPENAI_BASE_URL:-http://127.0.0.1:8080/v1}"
API_KEY="${OPENAI_API_KEY:-sk-change-me}"

curl "${BASE_URL}/models" \
  -H "Authorization: Bearer ${API_KEY}"
