# v1.0.0 stabilisation — master plan

**Started:** 2026-10-18. **Branch:** `release/v1.0.0` (single PR into `main`; the maintainer merges).

## Goal

Take the v0.0.1 alpha to a stable v1.0.0:

1. Bring all packages, the scaffold, Grafana versions and CI up to date, with a sensible e2e matrix.
2. Review the code base against current standards and fix the relevant findings.
3. Fix every finding in the 2026-06-10 security review (`ai-state/reference/security-review-2026-06-10.md`, VR-1 to VR-14).
4. Make in-panel proxy mode work on unpatched Grafana instances (FR5 / #102 / Q17). If that is not possible,
   document a Grafana patch and ship v1.0.0 as a closed release with a critical known issue.
5. Add an admin settings UI for the domain allowlist (#129), after the proxy approach is settled.
6. Clean up the GitHub repository (stale PRs and branches).
7. Upgrade the dev server (`graphs.alintech.com.au`, SSH host `pi1.alintech.com.au`) to the latest Grafana
   through apt and install the plugin for testing. Touch nothing on that host except Grafana.

## Process deltas from the original methodology

- **One PR branch.** The maintainer asked for all changes on one branch. Each task is still specified as a
  GitHub issue. Each task is implemented on a task branch (sub-agent worktree), reviewed, and then merged
  into `release/v1.0.0` (not `main`). `ai-state/` updates are committed to `release/v1.0.0` as well.
- **Local dev environment.** Work runs on the maintainer's Windows workstation (Docker Desktop, Go 1.26,
  Node 24, mage). The cloud-sandbox quirks in `RUNBOOK.md` do not apply here.

## Decisions taken at kickoff (orchestrator, 2026-10-18)

| # | Decision | Reason |
|---|----------|--------|
| D1 | `grafanaDependency` becomes `>=12.4.0` (was `>=12.3.0`). | 12.3 had its last patch on 2026-08-18. The patched lines are 12.4, 13.0, 13.1 and 13.2. |
| D2 | `@grafana/*` packages move to the latest 13.2.x with React 19 and React 19 types. The code must stay compatible with React 18. | Follows the Grafana 13.1→13.2 migration guide for plugins that still support versions older than 13.2. The e2e matrix covers 12.4 (React 18) and 13.2 (React 19). |
| D3 | Keep the auto-resolved e2e matrix (`grafana/plugin-actions/e2e-version`). | Q13b decision stands. With D1 it resolves to 12.4.x, 13.0.x, 13.1.x, 13.2.x and nightly. |
| D4 | Revert the temporary Levitate advisory (#124) once the deps are current. | The advisory was a stop-gap until the 13.x bump (#131). |
| D5 | FR5 design (from R1): panel fetches `/proxy` as text and renders `<iframe srcdoc sandbox="allow-scripts" referrerPolicy="no-referrer">`. The rewriter emits absolute upstream URLs for subresources and links, and injects `<meta name="referrer" content="no-referrer">` plus an in-memory cookie/storage shim. No `X-Allow-Embedding`. `/proxy-resource` is removed. Duplicate CORS headers removed. | Sandboxed frames do not send the session cookie (Chromium, Firefox), so `/proxy-resource` cannot work from the frame; it was also broken by the `<base href>` bug (R1 D1). Removing it reduces attack surface. An optional "inline subresources via Grafana" mode is a possible post-v1 feature. |
| D6 | Task merges into `release/v1.0.0` may happen before the independent review finishes; review findings are fixed forward on the branch. | Saves wall-clock; nothing reaches `main` until the maintainer merges #146 after all reviews and CI pass. |

## Key research finding (orchestrator, 2026-10-18) — source of the blocking headers

Checked against Grafana `main` source:

- `X-Frame-Options: deny` comes from `pkg/middleware/middleware.go` `AddDefaultResponseHeaders`. It is
  skipped when `allow_embedding = true` **or when the response already carries `X-Allow-Embedding: allow`**
  (added in Grafana 10.1, PR #69596). The plugin response sender
  (`pkg/plugins/httpresponsesender`) copies plugin headers into the writer before `WriteHeader`, and only
  `Set-Cookie` is filtered (`removeNonAllowedHeaders`). So a plugin can opt its own resource responses out
  of `X-Frame-Options: deny`.
- `Content-Security-Policy: sandbox` comes from `pkg/plugins/manager/client/client.go` `CallResource` →
  `SetCSPHeader`, which calls `Header.Set` on every plugin resource response. A plugin cannot remove or
  relax it. It sandboxes any document that is loaded by navigation (no scripts, opaque origin). It does not
  apply to `fetch()` reads or to subresources (`img`, `script`, `link`).
- Therefore the practical route is: fetch the rewritten HTML, then render it in a frame whose document is
  not delivered with that header (`srcdoc` or a `blob:` URL), with our own `sandbox` attribute. The open
  question is whether subresource requests from that frame are authenticated (session cookie, SameSite)
  in every browser. Task R1 answers that empirically.

## Tasks

Sizes follow the methodology (S/M/L). Issue numbers are in `ai-state/board-map.md`.

| ID | Task | Size | Issue | Depends on | Status |
|----|------|------|-------|------------|--------|
| R1 | Research spike: proxy render on unpatched Grafana | L | #138 | — | done (findings merged) |
| M1 | Scaffold + dependency + CI update | L | #139 | — | done (review APPROVE-WITH-NITS; nits fixed in RV2-be) |
| RV1 | Standards review (read-only findings list) | M | #140 | — | done (43 findings; dispositions in RV1-findings.md) |
| S1 | Frontend security: VR-1, VR-2 | M | #141 | M1 | done (blocking review finding fixed in RV2-fe) |
| FR5-fe | Proxy render in panel (fetch + srcdoc) | L | #102 | R1, S1 | done; runtime-verified in 3 browsers × 2 versions |
| S2 + FR5-be | Backend security VR-3/5/6/7/8/11, #133; rewriter absolute URLs, meta + shim, remove `/proxy-resource`, CORS fix | L | #142, #102 | M1, R1 | done (security review APPROVE-WITH-NITS) |
| AU1 | Settings page (#129), #113, VR-4, VR-13 | L | #129 | M1 | done (review APPROVE-WITH-NITS; nit fixed in RV2-fe) |
| RV2 | Fix the relevant RV1 + review findings (fe, be, nits, hide selectors) | M | #140 | RV1 | done; hide-selector change under review |
| GH1 | Repository hygiene | S | #143 | — | done (closed) |
| DEV1 | pi1: Grafana 13.2.4 via apt; install plugin | M | #144 | final build | Grafana done; plugin install waits for the final build (maintainer choice) |
| DOC1 | Docs refresh (incl. VR-9, VR-12) | M | #145 | features | done (+ README "Which sites work" table, maintainer request) |
| REL1 | v1.0.0 release prep | S | #56 | DOC1 | version 1.0.0 set; tag after #146 merges |
| SYS1 | System verification on the integrated branch | M | — | all | runtime verification passed; final CI pending |

## Integration points

- S1 and FR5 both change the iframe in `WebViewPanel.tsx` and `ViewportEditor.tsx`. S1 lands first.
- AU1 and S3 both touch `AppConfig.tsx` and `provisioning/`. S3 leaves `AppConfig.tsx` to AU1.
- M1 changes `package.json`, `package-lock.json`, `.config/` (through the create-plugin tool only) and the
  workflows. Every other task branches from the branch after M1 merges.

## Changelog

- 2026-10-18: plan created at kickoff.
- 2026-10-19: S3 folded into AU1 (provisioning) and M1 (VR-10, VR-14); S1 + FR5-fe and S2 + FR5-be paired to avoid file conflicts; D5, D6 added.
