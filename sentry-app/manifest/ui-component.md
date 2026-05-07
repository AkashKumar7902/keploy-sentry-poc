# Sentry UI Components

Sentry's Integration Platform exposes two surfaces this POC uses:

## `issue-link` (the "Reproduce in Keploy" button)

Renders on every issue's detail page. When clicked, Sentry POSTs to the
`link.uri` with the issue context (project, event, request data). Our
backend looks up a matching testcase and returns either:

- **Match** — a `keploy test --testcase <id>` command and a deep link to
  the testcase in app.keploy.io.
- **No match** — a curl reconstructed from the Sentry event, plus
  instructions to run it under `keploy record`.

If the user clicks the **Create** action, Sentry POSTs to `create.uri`
with the form values defined in `create.required_fields`. The backend
either enqueues a record job for the user's local agent (V2) or returns
the curl for manual execution (V1).

## OAuth installation

`redirectUrl` receives the install handshake. The backend stores:

- `installation_id` (Sentry's)
- `org_slug` (Sentry org)
- `keploy_workspace_id` (the user picks during install)
- `api_token` (Sentry-issued, stored encrypted)

## Webhooks (future)

`events` listed in the manifest fire `webhookUrl` with payloads. Useful
for syncing testcase pass/fail back to the Sentry issue ("this regression
is now covered by Keploy testcase X").
