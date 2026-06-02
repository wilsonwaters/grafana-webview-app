# Master Plan — Documentation & Release (Path 1) (`docs-release`)

## Goal

Produce complete repository and `docs/` documentation (written incrementally alongside the
code), an example dashboard, and a private-signed v1.0 release for self-hosted users —
completing deployment Path 1. Includes the deferred substantive threat-model document as a
dedicated task (its content is NOT written into the planning files).

## Task list

| # | Task | Size | Depends on |
|---|------|------|-----------|
| DR1 | Repo docs flesh-out: complete README intro, CHANGELOG entries, CONTRIBUTING, CODE_OF_CONDUCT (started as stubs in foundation F1) — incremental, kept current as streams land. | M | foundation F1 |
| DR2 | Developer guide (`docs/developer-guide.md`): prerequisites, repo structure, setup, dev workflow (`npm run dev`, `mage -v build:backend`, `docker compose up`), running tests, lint/format, debugging, multi-version testing, validator usage. | M | foundation F1 |
| DR3 | Configuration guide (`docs/configuration.md`) for authors: adding a panel, Test URL, Direct vs Proxied, viewport positioning, capture-view, refresh, hide-selectors, multi-panel; with screenshots. | M | panel-core, frameability |
| DR4 | Administration guide (`docs/administration.md`) for admins: first-time setup checklist, allowlist management + per-domain options, IP-blocklist explanation, rate-limit/size/timeout tuning, audit logging, metrics + suggested alerts, incident response, `[security.egress]` interaction, upgrade/rollback. | L | proxy, content-rewriting |
| DR5 | Written threat-model + SECURITY.md content (the deferred security documentation task): substantive threat model and layered-controls write-up in `docs/architecture.md` + SECURITY.md, plus architecture/flow diagrams and backend handler reference. Content authored here, not in planning files. | L | proxy, content-rewriting, security-foundation |
| DR6 | Installation + troubleshooting docs: `docs/installation.md` (Path 1 methods — CLI, Docker, Helm, manual; private signing notes; required first-time allowlist config; Path 2 noted as deferred) and `docs/troubleshooting.md`. | M | DR4 |
| DR7 | README polish + example dashboard: hero screenshot, badges, quick-start, doc links, known limitations; example dashboard JSON demonstrating one direct and one proxied URL. (deliverables 2, 3) | M | DR3, testing-cicd TC4 |
| DR8 | Private-signed v1.0 release (Path 1 complete): build, sign with `--rootUrls`, package, publish GitHub release; gates on all feature streams green. | M | testing-cicd TC6; DR1–DR7 |

## Integration points

- Fills in the SECURITY.md stub created in `foundation` F1 (DR5).
- DR5 threat-model document is the artefact `catalog-prep` audits against and references in the
  submission cover letter.
- DR8 release uses the signing workflow verified in `testing-cicd` TC6.
- Screenshots (DR3/DR7) are reused by `plugin.json` info.screenshots (out of scope to edit
  during planning).

## Out of scope

- Catalog submission and submission cover letter (`catalog-prep`).
- Workflow/CI changes (`testing-cicd`).
- Feature implementation (other streams).

## Open questions

- Which Grafana instance `rootUrls` to sign for the initial private release (depends on
  stakeholder's self-hosted target). Blocks DR8. (See OPEN-QUESTIONS.)
- Whether the example dashboard's proxied URL should ship pre-allowlisted in provisioning for
  the demo, given empty-by-default allowlist. Blocks DR7. (See OPEN-QUESTIONS.)

## Changelog

- Initialised at project kickoff (planning). No tasks dispatched yet.
