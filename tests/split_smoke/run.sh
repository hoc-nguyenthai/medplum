#!/usr/bin/env bash
# Smoke test của bộ service tách: dựng docker-compose.split.yml, chạy smoke.py, dọn. Xem smoke.py.
set -euo pipefail
cd "$(dirname "$0")/../.."
PROJECT=trivita-split-smoke
rand() { python3 -c 'import secrets;print(secrets.token_hex(16))'; }
export SMOKE_WRITE_TOKEN="smoke-write-$(rand)" SMOKE_ADMIN_TOKEN="smoke-admin-$(rand)"
export FHIR_WRITE_TOKENS="ops:${SMOKE_WRITE_TOKEN}" FHIR_ADMIN_TOKENS="root:${SMOKE_ADMIN_TOKEN}"
COMPOSE=(docker compose -p "$PROJECT" -f docker-compose.yml -f docker-compose.split.yml -f tests/split_smoke/compose.override.yml)
cleanup() { "${COMPOSE[@]}" down --volumes >/dev/null 2>&1 || true; }
trap cleanup EXIT
"${COMPOSE[@]}" up -d --build --wait --wait-timeout 300 fhir-server fhir-downloader fhir-notifier fhir-worker
python3 tests/split_smoke/smoke.py
