# sentry-spike

Validation spike for the Keploy × Sentry brainstorm.

**Question this answers:** if we canonicalize a Sentry event's request shape
and a Keploy testcase's `HTTPReq` using the same rules (header flaky-list,
key-only matching, content-type media-type comparison), do we get a
precise match?

## Run

```bash
cd sentry-spike
go mod tidy
go run .
```

## What it does

1. Loads `testdata/sentry-event.json` — a synthetic but realistic Sentry
   event for `POST /api/checkout` that returned 500 in production. Includes
   noisy headers (Authorization, X-Request-Id, Traceparent, X-Datadog-Trace-Id)
   that real Sentry events typically carry.
2. Loads `testdata/testcase-*.yaml` — three Keploy-shaped testcases:
   - `tc-checkout-happy` — `POST /api/checkout` exact-shape match
   - `tc-checkout-bad-amount` — same endpoint, different recorded
     trace/request IDs (proves flaky-header filtering works across
     record-time and Sentry-time)
   - `tc-list-users` — `GET /api/users` (control: should not match)
3. For each testcase, prints the canonical fields and computes three tiers
   of signature.
4. Reports the winning testcase and a `keploy test --testcase <name>`
   command.

## Tiers

- **T1** — `(method, path, sorted_query_keys, sorted_non_flaky_header_keys, content_type_media_type)` → SHA-256 prefix
- **T2** — `method + path + sorted query keys`
- **T3** — `method + path`

## What success looks like

- T1 matches one of the `tc-checkout-*` cases (header keys are identical
  after flaky filtering).
- T1 cleanly rejects `tc-list-users` (different method+path).
- The two checkout testcases get the same T1 signature even though their
  recorded `X-Request-Id` and `Traceparent` differ — that's the
  `flakyHeaders` filter doing its job.

## What this does NOT validate

- Body matching (separate cascade in the real matcher).
- Multi-tenant `app_id` scoping.
- Cloud index performance.
- The "no match" record-and-capture flow.
- Sentry's actual event payload shape variations (path templating, etc.).

If the spike's output looks right, the next step (per the brainstorm plan)
is Phase 1: lift the canonicalization into a shared package in keploy and
add a `RequestShapeHash` field to `models.TestCase`.
