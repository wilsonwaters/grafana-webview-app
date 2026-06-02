# Master Plan — Testing & CI/CD (`testing-cicd`)

## Goal

Provide a non-skippable security test suite, full unit/E2E coverage, and verified CI /
release / E2E-matrix workflows with signing wired in. Most of this stream is foundational
(test infrastructure, CI), with the security suite mapping the spec's mandatory criteria
17–31 to dedicated tests that run on every PR.

## Task list

| # | Task | Size | Depends on |
|---|------|------|-----------|
| TC1 | Security test suite — SSRF/blocklist/allowlist: dedicated Go tests for AC 17 (fresh install fails closed), 18 (allowlist applies to `/proxy`, `/proxy-resource`, `/check-frameable`), 19 (RFC1918 resolution denied), 20 (metadata/link-local denied), 21 (DNS rebinding prevented end-to-end), 22 (non-HTTP schemes → 400). | L | proxy P1–P7; content-rewriting CR3; security-foundation SF1–SF4 |
| TC2 | Security test suite — limits/headers/redirects: AC 23 (redirect into denied dest blocked), 24 (oversize → 413), 25 (rate limit → 429), 26 (outgoing header strip), 27 (incoming header strip), 28 (audit log emitted), 29 (metrics exposed). | L | proxy P1–P7; content-rewriting CR4 |
| TC3 | Frontend unit/component tests: viewport transform math, config-vs-view switching, Test URL flow, load-mode selector, degradation behaviour. (AC 33 frontend) | M | panel-core, frameability, direct-only-fallback |
| TC4 | E2E suite with `@grafana/plugin-e2e` / Playwright: config flow, view-mode rendering, sandbox attribute assertion (AC 30), CSS-selector injection guard (AC 31), light + dark theme rendering (AC 35), security boundary cases. (AC 34, 35) | L | panel-core, frameability, proxy, content-rewriting |
| TC5 | CI verification + non-skippable security gate: confirm `ci.yml` runs frontend + backend unit tests, lint, typecheck on every PR, and wire the security suites (TC1/TC2) so they cannot be skipped. (AC 36) | M | TC1, TC2 |
| TC6 | Release workflow + signing + validator gate: verify/extend `release.yml` to build, sign, and package on tag push; add the `@grafana/plugin-validator` check; confirm/add E2E matrix across Grafana versions (`e2e.yml`). (AC 32) | M | TC4, TC5 |

## Integration points

- Asserts behaviour produced by `security-foundation`, `proxy`, `content-rewriting`,
  `panel-core`, `frameability`, `direct-only-fallback` — this is the cross-stream verification layer.
- TC5/TC6 operate on the existing scaffolded workflows (`ci.yml`, `release.yml`, `e2e.yml`,
  `is-compatible.yml`); changes to workflows happen here, not during planning.
- Validator gate (TC6) feeds `catalog-prep` pre-submission validation.

## Out of scope

- The feature code under test (other streams).
- Writing security documentation / threat model (`docs-release`).
- Actual catalog submission (`catalog-prep`).

## Open questions

- Whether the Grafana-version E2E matrix should span the full `>=12.3.0` range or a pinned
  subset for CI cost. Blocks TC6. (See OPEN-QUESTIONS.)
- How to simulate DNS rebinding deterministically in CI for AC 21. Blocks TC1.
  (See OPEN-QUESTIONS.)

## Changelog

- Initialised at project kickoff (planning). No tasks dispatched yet.
