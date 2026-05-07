# Keploy × Sentry Integration — Brainstorm

> Status: brainstorm / design exploration. Not for commit.
> Owner: Akash
> Date: 2026-05-06

## Pitch

When a developer opens a Sentry issue for an HTTP error in production, give them one button — **"Reproduce in Keploy"** — that either (a) opens a deterministic local repro of that exact request, or (b) records a new testcase by replaying the failing request against their app under Keploy's record mode. The whole experience is **one click, one command**, no mental model the developer has to learn.

## Why Keploy + Sentry

Sentry knows **what broke in production** (the request, the stack trace, the user, the release).
Keploy knows **how to deterministically replay any HTTP request and freeze its dependencies**.
The intersection — *"reproduce this prod failure on my laptop in 10 seconds"* — is currently the worst hour of a developer's debugging week. No other Sentry integration in the directory closes that gap.

## Scope (V1)

- HTTP testcases only. Postgres / MySQL / Mongo / gRPC come later (matching exists per-protocol, but Sentry's data model is HTTP-first).
- Public Sentry integration (third-party on Sentry's Integration Platform). Not pursuing "default" status until adoption proves it out.
- Two user flows: **match** (testcase exists) and **record** (no match, capture one).
- App-to-Keploy-Cloud mapping is per-Sentry-project (1 Sentry project ↔ 1 Keploy app, V1).

Out of scope V1: SDK-side runtime correlation, multi-app fanout, body-content matching beyond shape, write-back from Keploy → Sentry.

---

## Architecture

```
┌─────────────────────┐         ┌───────────────────────┐         ┌────────────────────┐
│   sentry.io         │         │  api.keploy.io        │         │  User's machine    │
│                     │         │  (Keploy Cloud)       │         │                    │
│  ┌──────────────┐   │         │                       │         │  ┌──────────────┐  │
│  │ Issue page   │   │         │  ┌─────────────────┐  │         │  │ Keploy agent │  │
│  │              │   │  HTTPS  │  │ /v1/integrations│  │         │  │ (polls jobs) │  │
│  │ "Reproduce   │───┼────────►│  │  /sentry/match  │  │         │  └──────┬───────┘  │
│  │  in Keploy"  │   │         │  └────────┬────────┘  │         │         │          │
│  │  button      │◄──┼─────────┤           ▼           │         │         ▼          │
│  └──────────────┘   │         │   testcase index      │         │  ┌──────────────┐  │
│                     │         │   (HTTP shape sigs)   │         │  │ App under    │  │
│  Sentry App         │         │                       │         │  │ test         │  │
│  (issue-link UI)    │         │  ┌─────────────────┐  │  poll   │  └──────────────┘  │
│                     │         │  │ /v1/integrations│◄─┼─────────┤                    │
│                     │         │  │  /sentry/record │  │         │                    │
│                     │         │  └─────────────────┘  │         │                    │
│                     │         │                       │         │                    │
└─────────────────────┘         └───────────────────────┘         └────────────────────┘
       Sentry side                      Cloud side                     Local side
```

Three planes:

1. **Sentry side** — public integration manifest, OAuth install, `issue-link` UI component on the issue page. The button calls Keploy Cloud.
2. **Cloud side** (`api.keploy.io`) — two endpoints (`match`, `record`) and a new HTTP-testcase signature index.
3. **Local side** — existing Keploy agent gains a "job poll" loop that picks up record requests from the cloud and executes them against the user's app.

---

## User Flows

### Flow A — match (testcase already exists)

```
1. Dev opens Sentry issue:    POST /api/checkout → 500
2. Clicks "Reproduce in Keploy"
3. Sentry App posts the issue's request shape to api.keploy.io/v1/integrations/sentry/match
   { method, path, query_keys, header_keys, content_type, status_code, app_id }
4. Cloud matches against TestCase.HTTPReq signatures, returns ranked candidates
5. UI renders:
      Match found: tc_a8f3c1
      $ keploy test --testcase tc_a8f3c1 --app checkout-svc
6. Dev runs the command locally → deterministic repro
```

### Flow B — no match (capture a new testcase)

```
1. Same as A through step 3
2. Cloud returns no match
3. UI renders:
      No matching testcase. Record this request:
      [Reconstruct the request as a curl command — show it]
      [Or: click "Run via local agent" if their agent is online]

4a. Manual path: dev starts `keploy record` locally, runs the curl
4b. Auto path: cloud enqueues a job; user's local agent picks it up,
    starts record mode, fires the request via the existing
    PostmanImporter.sendRequest pattern, captures testcase + mocks
5. New testcase tc_xyz789 lands. UI updates with `keploy test --testcase tc_xyz789`.
```

V1 ships **4a only** (manual curl). 4b is V2 — proves the concept before committing to the agent-poll architecture.

---

## What Already Exists (Reuse Map)

| Capability | Where it lives | Reuse for |
|---|---|---|
| Curl serialization (`HTTPReq → curl string`) | `pkg/util.go:2412` `MakeCurlCommand` | Flow B no-match: hand the user a runnable curl |
| Curl parsing (`curl → method/url/headers/body`) | `pkg/matcher/http/absmatch.go:502` `parseCurlString` | Reconstruct HTTPReq from any source |
| Fire request at app + capture testcase | `pkg/service/import/import.go:140` `sendRequest` (Postman importer) | Flow B auto path: same pattern, Sentry-event source |
| Header noise filtering | `pkg/agent/proxy/integrations/http/match.go:39` `flakyHeaders` | Apply same exclusions when computing testcase signatures (auth/tracing/per-request IDs) |
| HTTP request shape comparison | `pkg/agent/proxy/integrations/http/match.go:353` `SchemaMatch` (currently mock-only) | Algorithm to lift — apply to TestCase.HTTPReq |
| Testcase model | `pkg/models/testcase.go:42` | Source for signature computation |
| Postgres-style stored signature precedent | `pkg/models/mock.go:289` `SQLAstHash` | Pattern for adding a `RequestShapeHash` to TestCase |

## What's New (Build List)

### Cloud side (`api.keploy.io`)

1. **HTTP testcase signature** — new field on TestCase persisted in cloud:
   - Compute on upload: canonicalize `(method, path_pattern, sorted_query_keys, sorted_non_flaky_header_keys, content_type)` → SHA256.
   - Store on the testcase record. Index by `(app_id, signature)`.
   - Mirrors the Postgres `SQLAstHash` pattern.
2. **`POST /v1/integrations/sentry/match`**
   - Input: `{ app_id, method, path, query, headers, status_code? }`
   - Compute candidate signature, return ranked matches (exact → method+path → method only).
   - Return: `[{ testcase_id, name, last_run_at, score }]`.
3. **`POST /v1/integrations/sentry/record`** (V2)
   - Input: same as match, plus body if available.
   - Enqueue job for the user's local agent to pick up.
4. **Sentry-project ↔ Keploy-app mapping table** — set during integration install.

### Sentry side (public integration)

1. **Integration manifest** — name, OAuth scopes (`issue:read`, `event:read`), redirect URLs.
2. **Install / OAuth flow** — bind Sentry org → Keploy workspace.
3. **`issue-link` UI component** — renders "Reproduce in Keploy" button on the issue page; calls cloud match endpoint; renders the resulting command (or curl).
4. **Project settings page** — Sentry project ↔ Keploy app picker.

### Keploy side (lifting existing code)

1. **Extract HTTP request canonicalization** from `pkg/agent/proxy/integrations/http/match.go` into a new package, e.g. `pkg/matcher/httpshape/`, callable without proxy context.
   - Input: `models.HTTPReq` (or generic request fields).
   - Output: deterministic signature string.
   - Carry the `flakyHeaders` exclusion list with it.
2. **Hook signature computation into testcase write paths**:
   - `pkg/service/record/record.go:328` (record mode)
   - `pkg/service/import/import.go:375` (Postman importer)
   - `pkg/platform/yaml/testdb/db.go:145` (yaml writer)
3. **Add `RequestShapeHash` field** to `models.TestCase` — written alongside `Curl` field at the same callsites.
4. **(V2) Local agent job loop** — long-poll `api.keploy.io/v1/agents/jobs`, execute record jobs via the existing `sendRequest` pattern, upload result.

---

## Phasing

| Phase | Deliverables | Time estimate | Demo |
|---|---|---|---|
| **0 — Spike** | Manual proof: take a real Sentry event payload, hand-canonicalize, manually grep testcases, find a match | 2-3 days | Show: "yes, the matching idea works" |
| **1 — Cloud signature index** | `RequestShapeHash` on TestCase, computed at write time, indexed in cloud | 1 week | Backfill existing testcases; query by signature |
| **2 — Match endpoint + Sentry app (Flow A)** | `POST /v1/integrations/sentry/match` + Sentry public integration with issue-link button | 2 weeks | Sentry issue → click → get `keploy test` command |
| **3 — Manual record flow (Flow B 4a)** | "No match" branch shows curl reconstructed from Sentry event | 3 days | Click → get curl → run with `keploy record` → testcase saved |
| **4 — Auto record (Flow B 4b)** | Local agent job-poll + cloud `/sentry/record` endpoint + auto-capture | 3 weeks | Click → testcase appears with no manual steps |
| **5 — Polish** | Multi-candidate UX, status code matching, scrubbed-body fallback, telemetry | 2 weeks | |

V1 = phases 0-3. V2 = phase 4. V3 = phase 5.

---

## Open Questions

1. **App identification.** Sentry has projects, Keploy has apps. Is the mapping always 1:1? What about monorepos sending one Sentry project for multiple services? V1 assumes 1:1 — needs validation.
2. **Header reconstruction integrity.** Sentry strips auth/cookies. The reconstructed request will not have valid auth — recording it against a real app will fail at the auth layer. Options: (a) require a "test bearer token" config in the integration, (b) document that auth must be added manually before recording, (c) integrate with Sentry's "additional context" to capture a sanitized auth identifier the user provides.
3. **Body availability.** Many Sentry configs scrub or never send request bodies. Match-by-shape works without bodies; record-with-replay does not. Acceptable failure mode for V1?
4. **Multiple candidate matches.** When 5 testcases match the signature, how does the UI pick? Show a list (Sentry's `issue-link` component is constrained — may not support a picker), or auto-pick most recent / most-passed?
5. **Cloud testcase storage.** Today, are testcases necessarily uploaded to `api.keploy.io`, or can they be local-only? If local-only is common, the match endpoint can't see them — requires either a "sync" prompt or a fallback "your testcase index is local, here's a CLI command to run the match locally."
6. **Auth / tenancy.** OAuth at install gives a Sentry-side token; what's the corresponding Keploy auth model — an org-scoped API key, a per-user token, an SSO bridge?
7. **Pricing / packaging.** Is this a free integration, an enterprise feature, or usage-based? Affects how the install flow gates access.

---

## Risks

- **Sentry's `issue-link` UI surface is limited.** May not be enough to render a multi-candidate picker or a long curl — V1 might need to deep-link to a Keploy-hosted page that shows the result.
- **PII in reconstructed requests.** Even with stripped headers, the path or query string may contain PII (user IDs, emails). The recorded testcase will permanently contain it. Need a scrubbing step before persisting.
- **Drift between mock-matcher and testcase-matcher.** Once the canonicalization is lifted into a shared package, both consumers must stay in sync — risk of subtle behavior divergence over time. Mitigation: shared package with both consumers covered by a single test suite.
- **Adoption gating.** A user installs the Sentry integration but has no Keploy testcases for the failing endpoint → every click returns "no match." First impression is bad. Onboarding flow should set expectations: "this gets better as your Keploy coverage grows."
- **Cloud-to-local agent reliability.** Phase 4 introduces a long-running connection from user's machine to api.keploy.io. Disconnects, NATs, firewalls, etc. — known hard problem. Worth doing only after V1 proves user demand.

---

## What I'd Do First (1-Day Spike)

1. Pull a real Sentry event JSON for an HTTP error.
2. Take three real Keploy testcases from a sample app.
3. Write a 50-line Go script that:
   - Canonicalizes both the Sentry request and the testcase `HTTPReq`s using the same rules
   - Reports which testcase matches
4. Demo it in a meeting. If the matches feel right on real data, build phase 1. If not, the whole design needs to shift to body/content matching — better to know now.

This is the cheapest way to validate the central technical bet (shape matching is precise enough) before committing weeks of build.
