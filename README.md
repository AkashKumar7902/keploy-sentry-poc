# Keploy × Sentry Integration — POC

A design sketch + working code that explores adding a "Reproduce in Keploy" button on every Sentry issue page. One click either gives the developer a `keploy test --testcase <id>` command (matching path) or fires the failing request at their app under `keploy record` to capture a brand-new testcase + dependency mocks (recording path).

## Repo layout

| Path | What it is |
|---|---|
| [`sentry-integration-plan.md`](sentry-integration-plan.md) | Full design doc: pitch, architecture, user flows, build list, phasing, open questions, risks. |
| [`spike/`](spike) | 1-day matching POC. Canonicalizes a Sentry event's request shape and a Keploy testcase's `HTTPReq` using the same rules (header flaky-list, key-only matching, content-type media-type). Validates that the same canonical signature falls out for matching pairs. |
| [`cmd/sentry-to-curl/`](cmd/sentry-to-curl) | CLI: Sentry event JSON → runnable curl, with `--bearer` injection and noisy-header stripping. |
| [`cmd/sentry-replay/`](cmd/sentry-replay) | CLI: Sentry event + app URL → fires the request and writes a Keploy-format testcase YAML. Mirrors `pkg/service/import/import.go`'s `sendRequest` pattern from the keploy repo. Tags the testcase with stack-trace + release. |
| [`cmd/agent-poll/`](cmd/agent-poll) | CLI: stub for the cloud-to-local agent bounce. Polls a JSON jobs file, executes pending Sentry-replay jobs, writes captured testcases. |
| [`scripts/e2e-demo.sh`](scripts/e2e-demo.sh) | Orchestrator: builds binaries, starts a sample app under `keploy record`, fires a Sentry-derived request, prints the captured testcase. Falls back to `httpbin.org` if keploy isn't installed. |
| [`scripts/auto-pr.sh`](scripts/auto-pr.sh) | After a record run, branches off main, drops the testcase into a Keploy test-set dir, opens a PR via `gh`. |
| [`sentry-app/`](sentry-app) | Sentry public-integration scaffold: `manifest/integration.json`, UI component spec, runnable Go HTTP backend stub for the OAuth callback / match / record / webhook endpoints. |
| [`internal/sentry/`](internal/sentry) | Shared: Sentry event parsing + provenance string builder. |
| [`internal/keploycase/`](internal/keploycase) | Shared: Keploy testcase YAML emitter (mirrors `pkg/models/testcase.go`). |
| [`examples/`](examples) | Sample Sentry event + jobs.json for kicking the tires. |

## Quick start

```bash
# build everything
go build ./...

# 1. event -> curl
go run ./cmd/sentry-to-curl --event examples/sentry-event.json --bearer "$TOKEN"

# 2. event -> fire at app -> testcase YAML  (works against httpbin out of the box)
go run ./cmd/sentry-replay \
    --event examples/sentry-event.json \
    --base-url https://httpbin.org \
    --out captured-tc.yaml

# 3. polling agent (simulates the cloud-to-local bounce)
go run ./cmd/agent-poll --jobs examples/jobs.json --out-dir ./out --once

# 4. matching POC (the original 1-day spike)
cd spike && go run . && cd ..

# 5. Sentry App stub (manifest + backend)
go run ./sentry-app/src --addr :9090
# in another terminal:
curl -X POST http://localhost:9090/v1/integrations/sentry/match \
    -H 'Content-Type: application/json' \
    --data @examples/sentry-event.json | jq
```

## End-to-end against a real keploy + sample app

```bash
APP_CMD="./my-sample-app" APP_URL="http://localhost:8080" ./scripts/e2e-demo.sh
```

This starts `keploy record` against your app, fires the Sentry-derived request, and prints the resulting testcase + any mocks captured by keploy's proxy alongside it.

## What this POC validates

1. **Matching is feasible.** The spike shows that canonicalizing both the Sentry event and stored testcases with the same flaky-header filter collapses noisy variants to the same signature.
2. **Recording is feasible without new infrastructure.** The CLIs reuse Keploy's existing `sendRequest` pattern (Postman importer) — same idea, different source.
3. **The architecture composes cleanly.** Sentry App stub → cloud endpoint → local agent → testcase on disk works end-to-end as a chain of small, replaceable parts.

## What it doesn't try to do

- No real cloud-side signature index (the spike's algorithm is in-process).
- No real OAuth flow or token persistence in the Sentry App stub.
- No body-content matching beyond shape.
- No PII scrubbing on captured testcases (real risk — see plan).
- Not wired to actually post to sentry.io; the manifest is correct but unused.

## Status

Design + working POC. No production code changes to keploy yet. The plan's recommended next steps (Phase 1: lift canonicalization into a shared keploy package, add `RequestShapeHash` to `models.TestCase`) are unstarted.
