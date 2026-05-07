#!/usr/bin/env bash
# auto-pr.sh — open a PR with a freshly-captured testcase.
#
# Workflow:
#   1. Take a testcase YAML and a Sentry event ID.
#   2. Branch off main: sentry/<event-id>
#   3. Drop the testcase into a Keploy test-set directory.
#   4. Commit, push, open PR via `gh`.
#
# Requirements:
#   - `gh` authenticated (gh auth status)
#   - inside a git repo with a Keploy test-set directory layout
#
# Usage:
#   ./scripts/auto-pr.sh <testcase.yaml> <event-id> [test-set-dir]

set -euo pipefail

TESTCASE="${1:?usage: auto-pr.sh <testcase.yaml> <event-id> [test-set-dir]}"
EVENT_ID="${2:?event id required}"
TEST_SET="${3:-keploy/test-set-0/tests}"

if ! command -v gh >/dev/null 2>&1; then
    echo "auto-pr: gh CLI is required" >&2
    exit 1
fi
if ! gh auth status >/dev/null 2>&1; then
    echo "auto-pr: gh not authenticated. Run 'gh auth login' first." >&2
    exit 1
fi
if [[ ! -f "$TESTCASE" ]]; then
    echo "auto-pr: testcase file not found: $TESTCASE" >&2
    exit 1
fi

BRANCH="sentry/${EVENT_ID}"
DEST="${TEST_SET}/$(basename "$TESTCASE")"

echo "[1/4] Creating branch $BRANCH..."
git checkout -b "$BRANCH"

echo "[2/4] Copying testcase to $DEST..."
mkdir -p "$TEST_SET"
cp "$TESTCASE" "$DEST"
git add "$DEST"

echo "[3/4] Committing..."
git -c user.name="keploy-sentry-bot" -c user.email="bot@keploy.local" commit -m "test: add regression test from Sentry issue $EVENT_ID

Captured automatically by the Keploy x Sentry integration.
See $DEST for the request shape and recorded response.
"

echo "[4/4] Opening PR..."
git push -u origin "$BRANCH"
gh pr create \
    --title "Add regression test for Sentry issue $EVENT_ID" \
    --body "$(cat <<EOF
## Summary
Auto-generated regression test for Sentry issue \`$EVENT_ID\`.

The Keploy x Sentry integration captured the failing request, replayed it
against the app under \`keploy record\`, and stored the result as a testcase.

## Test plan
- [ ] Review the recorded request shape
- [ ] Verify the captured response matches expected behavior
- [ ] Confirm any associated mocks under \`$(dirname "$DEST")/../mocks/\` look reasonable
- [ ] Run \`keploy test --testcase $(basename "$TESTCASE" .yaml)\` locally
EOF
)"
