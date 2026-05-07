#!/usr/bin/env bash
# e2e-demo.sh — orchestrate the Sentry → Keploy record flow end to end.
#
# What this does (when keploy + a sample app are available):
#   1. Builds the POC binaries.
#   2. Starts the user's app under `keploy record`.
#   3. Runs sentry-replay to fire a Sentry-derived request at the app.
#   4. Stops keploy.
#   5. Prints the captured testcase + the mocks that were recorded
#      alongside it.
#
# Requirements:
#   - keploy installed and on PATH (`keploy --version`)
#   - a sample app whose start command is in $APP_CMD
#   - the app listens on $APP_URL (default http://localhost:8080)
#
# Run:
#   APP_CMD="./my-sample-app" APP_URL="http://localhost:8080" ./scripts/e2e-demo.sh
#
# If keploy or APP_CMD are missing, the script falls back to httpbin.org
# so you can still see sentry-replay produce a testcase.

set -euo pipefail

cd "$(dirname "$0")/.."

EVENT="${EVENT:-examples/sentry-event.json}"
APP_URL="${APP_URL:-http://localhost:8080}"
APP_CMD="${APP_CMD:-}"
BEARER="${BEARER:-}"
OUT_DIR="${OUT_DIR:-./out}"

mkdir -p "$OUT_DIR"

echo "[1/5] Building POC binaries..."
go build -o "$OUT_DIR/sentry-replay" ./cmd/sentry-replay
go build -o "$OUT_DIR/sentry-to-curl" ./cmd/sentry-to-curl

if ! command -v keploy >/dev/null 2>&1; then
    echo "[!] keploy not found on PATH — falling back to httpbin.org demo"
    APP_URL="https://httpbin.org"
    APP_CMD=""
fi

if [[ -n "$APP_CMD" ]]; then
    echo "[2/5] Starting app under keploy record: $APP_CMD"
    keploy record -c "$APP_CMD" --path "$OUT_DIR/keploy" &
    KEPLOY_PID=$!
    trap 'kill $KEPLOY_PID 2>/dev/null || true' EXIT
    echo "    waiting 5s for app to come up..."
    sleep 5
else
    echo "[2/5] No APP_CMD set (or keploy missing) — skipping record step."
fi

echo "[3/5] Firing Sentry-derived request at $APP_URL..."
ARGS=(--event "$EVENT" --base-url "$APP_URL" --out "$OUT_DIR/sentry-replay-tc.yaml")
if [[ -n "$BEARER" ]]; then
    ARGS+=(--bearer "$BEARER")
fi
"$OUT_DIR/sentry-replay" "${ARGS[@]}"

if [[ -n "${KEPLOY_PID:-}" ]]; then
    echo "[4/5] Stopping keploy..."
    kill $KEPLOY_PID 2>/dev/null || true
    wait $KEPLOY_PID 2>/dev/null || true
fi

echo "[5/5] Captured testcase:"
echo "----------------------------------------"
sed -n '1,30p' "$OUT_DIR/sentry-replay-tc.yaml"
echo "..."
echo "----------------------------------------"

if [[ -d "$OUT_DIR/keploy" ]]; then
    echo "Keploy artifacts:"
    find "$OUT_DIR/keploy" -type f | sed 's/^/  /'
fi

echo
echo "Done. To run the captured testcase:"
echo "  keploy test --testcase \$(yq .name $OUT_DIR/sentry-replay-tc.yaml)"
