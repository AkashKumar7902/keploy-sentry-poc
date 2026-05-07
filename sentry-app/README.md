# Sentry App scaffold

A minimal public-integration scaffold for the Keploy x Sentry POC.
Demonstrates the three pieces a real Sentry integration needs:

1. **Manifest** (`manifest/integration.json`) — how Sentry registers the
   integration: name, slug, scopes, events, the `issue-link` UI component
   schema, and the action endpoints.
2. **UI Component spec** (`manifest/ui-component.md`) — the structure of
   the `issue-link` element that puts a "Reproduce in Keploy" button on
   every Sentry issue page.
3. **Backend stub** (`src/main.go`) — a runnable HTTP server that
   implements the OAuth callback, issue-link click target, create action,
   webhook handler, and app dropdown lookup.

## Run the stub

```bash
cd ../
go run ./sentry-app/src --addr :9090
```

Then in another shell, simulate a click:

```bash
curl -s -X POST http://localhost:9090/v1/integrations/sentry/match \
  -H 'Content-Type: application/json' \
  --data @../examples/sentry-event.json | jq
```

Expected response: a JSON object with either `action: "match"` (carrying
a `keploy test --testcase ...` command) or `action: "no-match"` (carrying
a reconstructed curl).

## Wiring to a real Sentry org

1. Go to Sentry org → Settings → Custom Integrations → New.
2. Paste `manifest/integration.json` into the form.
3. Set the redirect/webhook URLs to point at this backend.
4. Install on your project.
5. Open any issue → click the "Reproduce in Keploy" button.

## What's NOT real here

- No OAuth token exchange.
- No persistence — every request is stateless.
- The match endpoint returns canned responses based on path heuristics
  rather than calling the cloud signature index from `spike/`.
- No webhook signature verification.
- No rate limiting, retries, or observability.

This is the smallest piece of code that makes the architecture
demonstrable end-to-end. Replace each stub with the real thing as the
plan's phases land.
