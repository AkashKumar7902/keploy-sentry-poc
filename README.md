# Keploy × Sentry Integration — POC

A design sketch + 1-day validation spike for a [Keploy](https://github.com/keploy/keploy) integration with Sentry that lets developers click "Reproduce in Keploy" on a Sentry issue and either get a `keploy test --testcase <id>` command (if a matching testcase exists) or a curl to fire under `keploy record` (if not).

## Contents

- [`sentry-integration-plan.md`](sentry-integration-plan.md) — the full design: pitch, architecture, user flows, what-to-build, phasing, open questions, risks.
- [`spike/`](spike) — a self-contained Go program that validates the central technical bet (HTTP request-shape matching is precise enough). Synthetic Sentry event + three Keploy testcases + a canonicalization function that mirrors Keploy's real proxy matcher (`flakyHeaders` exclusion list, header-key matching, content-type media-type comparison).

## Run the spike

```bash
cd spike
go mod tidy
go run .
```

Expected: the `POST /api/checkout` Sentry event collapses to the same canonical signature as the matching testcases (despite different `Authorization` / `Traceparent` / `X-Request-Id` values), and the `GET /api/users` testcase is cleanly rejected.

## Status

POC + design only. No production code changes yet.
