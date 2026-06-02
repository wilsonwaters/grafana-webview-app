# Master Plan — Security Foundation (backend libraries) (`security-foundation`)

## Goal

Build the audited, unit-tested Go building blocks that every proxying endpoint will use,
**before** any endpoint consumes them (per the spec's milestone-3 rule). These are
internal libraries with thorough tests rather than vertical slices — foundational by
nature, no user-visible output. Each task lands with its tests; no consumer code is
written here.

## Task list

| # | Task | Size | Depends on |
|---|------|------|-----------|
| SF1 | Hardcoded IP blocklist library + unit tests: classify a resolved IP as allowed/denied across the mandated private/reserved/loopback/link-local/CGNAT/ULA/multicast ranges and IPv4-mapped IPv6, for both IPv4 and IPv6. Non-configurable. Tests assert each denied class is rejected. (AC 19 partial, 20, 33 partial) | M | foundation F3 |
| SF2 | URL validator + unit tests: scheme allowlist (http/https only), port restriction (80/443 + ports declared per allowed domain), hostname normalization and IDN handling, rejection of non-HTTP schemes. (AC 22, 33 partial) | M | foundation F3 |
| SF3 | Allowlist matcher + unit tests: exact and subdomain matching against the admin allowlist, per-domain options structure (subdomains, private-IP opt-in, per-domain port + rate overrides). Empty allowlist denies everything. (AC 15, 17, 33 partial) | M | foundation F3 |
| SF4 | DNS-resolve-then-dial helper + unit tests: resolve hostname once, validate the resolved IP via SF1, then dial that exact IP using `net.Dialer.Control`, preserving the original `Host` header, so the validated IP is the one connected to. Also block metadata hostnames. (AC 21 foundation, 20 partial) | M | SF1 |
| SF5 | Rate limiter + concurrency cap + unit tests: in-memory token bucket per Grafana instance and per target domain, plus an in-flight concurrent-request cap. Configurable from settings (foundation F3), safe defaults. (AC 25 foundation, 33 partial) | M | foundation F3 |

## Integration points

- All five libraries are consumed by `frameability` (`/check-frameable`) and `proxy`
  (`/proxy`, `/proxy-resource`) so that every endpoint runs the identical security pipeline.
- SF3 per-domain options structure must match the settings schema defined in foundation F3.
- SF4 depends on SF1 for the blocklist check; the consuming endpoints chain SF2 → SF3 →
  SF5 → SF4 in pipeline order.

## Out of scope

- Any HTTP endpoint or resource handler (`frameability`, `proxy`).
- Audit logging and metrics emission (those live with the endpoints in `proxy`).
- Header stripping policy (lives in `proxy`).
- The end-to-end DNS-rebinding test through a live endpoint (`testing-cicd` AC 21).

## Open questions

- Whether per-domain private-IP opt-in (SF3/SF4) needs an audit-log hook here or only at the
  consuming endpoint. Blocks SF4. (See OPEN-QUESTIONS.)
- Resolver behaviour for hostnames returning multiple A/AAAA records — validate all and dial
  the first valid, or fail if any record is denied. Blocks SF4.

## Changelog

- Initialised at project kickoff (planning). No tasks dispatched yet.
