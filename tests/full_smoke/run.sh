#!/usr/bin/env bash
# Smoke test toàn bộ hệ thống: dựng docker-compose.full.yml (9 service, project compose riêng, volume tạm), seed project và client ở auth,
# chạy smoke.py, dọn. Xem smoke.py. Cổng 8005-8011 phải trống (dừng `make up-full` trước nếu đang chạy).
set -euo pipefail
cd "$(dirname "$0")/../.."
PROJECT=trivita-full-smoke
rand() { python3 -c 'import secrets;print(secrets.token_hex(24))'; }
# khóa ký riêng cho lần chạy này (biến môi trường của shell được ưu tiên hơn .env.full)
FHIR_JOB_SIGNING_KEY="$(rand)" FHIR_WS_SIGNING_KEY="$(rand)"
export FHIR_JOB_SIGNING_KEY FHIR_WS_SIGNING_KEY
WORK="$(mktemp -d)"
COMPOSE=(docker compose -p "$PROJECT" -f docker-compose.full.yml -f tests/full_smoke/compose.override.yml)
cleanup() { "${COMPOSE[@]}" down --volumes >/dev/null 2>&1 || true; rm -rf "$WORK"; }
trap cleanup EXIT

go build -o "$WORK/helper" ./tests/full_smoke/helper
"$WORK/helper" dicom "$WORK/smoke.dcm"
"${COMPOSE[@]}" up -d --build --wait --wait-timeout 300
# seed: project, admin và client (clientId/clientSecret in ra stdout dạng "khóa: giá trị"); mật khẩu admin ngẫu nhiên, không dùng tới
echo "Smoke-$(rand)" | "${COMPOSE[@]}" exec -T auth /app seed project -name "Full smoke" \
  -admin-email smoke@example.org -admin-first Smoke -admin-last Test >"$WORK/seed.txt" 2>"$WORK/seed.log" \
  || { cat "$WORK/seed.log"; exit 1; }
python3 tests/full_smoke/smoke.py "$WORK"
