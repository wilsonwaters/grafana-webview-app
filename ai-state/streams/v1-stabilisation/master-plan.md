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

| ID | Task | Size | Depends on | Status |
|----|------|------|------------|--------|
| R1 | Research spike: proxy render on unpatched Grafana (browser × approach × Grafana version matrix) | L | — | in progress |
| M1 | Scaffold + dependency + CI update (create-plugin update, @grafana 13.2 / React 19, Go SDK, plugin-e2e, e2e matrix, revert #124, `grafanaDependency >=12.4.0`) | L | — | in progress |
| RV1 | Standards review of `src/`, `pkg/`, tests and docs (read-only, findings list) | M | — | in progress |
| S1 | Frontend security: VR-1 URL scheme validation, VR-2 per-mode iframe sandbox | M | M1 | ready |
| S2 | Backend security: VR-3 authz model, VR-5, VR-6, VR-7, VR-8, VR-11 | M | M1 | ready |
| S3 | Config and supply chain: VR-4, VR-9, VR-13 (with AU1), VR-14, VR-10 tracking | S | M1 | ready |
| FR5 | In-panel proxy render, using the approach R1 selects | L | R1, S1 | blocked on R1 |
| AU1 | Allowlist settings UI (#129), remove scaffold `apiKey`/`apiUrl`, investigate #113 | L | M1, R1 | blocked on R1 |
| RV2 | Fix the relevant RV1 findings | M | RV1, M1 | blocked |
| GH1 | Repository hygiene: close stale PRs, delete stale branches (record SHAs) | S | M1 (dependabot PRs superseded) | ready |
| DEV1 | Dev server: upgrade Grafana via apt on pi1, install the plugin build | M | M1 (build) | ready |
| DOC1 | Docs refresh for v1.0.0 (README, configuration, administration, troubleshooting, SECURITY, CHANGELOG) | M | all feature tasks | blocked |
| REL1 | v1.0.0 release prep (version, CHANGELOG, release notes, known issues) | S | DOC1 | blocked |
| SYS1 | System verification on integrated branch (local Docker matrix + pi1) | M | all | blocked |

## Integration points

- S1 and FR5 both change the iframe in `WebViewPanel.tsx` and `ViewportEditor.tsx`. S1 lands first.
- AU1 and S3 both touch `AppConfig.tsx` and `provisioning/`. S3 leaves `AppConfig.tsx` to AU1.
- M1 changes `package.json`, `package-lock.json`, `.config/` (through the create-plugin tool only) and the
  workflows. Every other task branches from the branch after M1 merges.

## Changelog

- 2026-10-18: plan created at kickoff.
