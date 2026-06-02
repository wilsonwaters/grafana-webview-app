# Master Plan — Path 2 Catalog Preparation (`catalog-prep`)

## Goal

Prepare everything needed for Community signing and Grafana catalog submission — validator
run, a self security-audit against the project's own threat model, and a submission cover
letter — while deferring the actual submission pending stakeholder go-ahead. No submission
happens in this phase.

## Task list

| # | Task | Size | Depends on |
|---|------|------|-----------|
| CP1 | Pre-submission validation: run `@grafana/plugin-validator` against the packaged plugin; confirm `plugin.json` metadata completeness (description, links, keywords, screenshots, version), ID convention, and that all required repo/docs files are present. Record and fix any gaps. (AC 32) | M | docs-release DR8 |
| CP2 | Self security audit against the threat model: walk the project's own threat-model document (docs-release DR5) and verify each control is implemented and evidenced (empty allowlist default, non-disableable IP blocklist, rebinding-safe dialler, header stripping both directions, limits, audit logging) — produce an evidence checklist, no new exploit content. | M | docs-release DR5; testing-cicd TC1, TC2 |
| CP3 | Submission cover letter (`docs/publishing.md`): draft the Phase-2 cover letter addressing anticipated reviewer concerns with pre-prepared responses; document the Private→Community signing path and submission process. | M | CP2 |
| CP4 | Deferred submission + feedback iteration (NOT executed this phase): placeholder task to submit to the catalog and iterate on reviewer feedback once stakeholders approve. Explicitly paused. | M | CP3; stakeholder go-ahead |

## Integration points

- Consumes the validator gate from `testing-cicd` TC6 and the threat-model document from
  `docs-release` DR5.
- CP1 metadata checks reference `plugin.json` (not edited during planning).
- CP4 is the project's deferred terminal task; nothing depends on it.

## Out of scope

- Writing the threat model itself (`docs-release` DR5).
- Any code or workflow changes.
- Actually submitting to the catalog (deferred — CP4 is paused pending go-ahead).

## Open questions

- Current catalog submission URL and review process (confirm at submission time per spec).
  Blocks CP4. (See OPEN-QUESTIONS.)
- Whether Grafana Labs will accept a frame-header-stripping proxy on Cloud at all; the spec
  says be prepared for a "no" and continue with Path 1. Blocks CP4. (See OPEN-QUESTIONS.)

## Changelog

- Initialised at project kickoff (planning). No tasks dispatched yet.
