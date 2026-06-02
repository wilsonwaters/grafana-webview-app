# OPEN QUESTIONS

Unresolved decisions and what they block. Each is deferred to the task/stream noted; none is
an active blocker on dispatching `foundation`. Resolve before reaching the blocked task.

| # | Question | Blocks | Stream |
|---|----------|--------|--------|
| Q1 | ~~Exact packaging mechanics for a panel nested inside an app plugin (module registration vs a separate panel `plugin.json` under the app).~~ **RESOLVED (F4):** Nested-plugin pattern per Grafana docs — the panel lives at `src/panels/webview/` with its own `plugin.json` (id `wilsonwaters-webview-panel`) + sibling `module.tsx`; the `.config` webpack `getEntries()` discovers it and emits `dist/panels/webview/`, and Grafana registers it as a child plugin (no separate package.json/.config). The app `plugin.json` also declares an `includes` entry of `type: panel`. | F4 | foundation |
| Q2 | ~~Keep or remove the scaffold's example app pages (Page One–Four) once the Web View panel is registered.~~ **RESOLVED (F4):** Removed the demo Page One–Four, their routing (`utils.routing.ts`, `ROUTES`) and tests; kept the admin Configuration page and replaced the app root with a minimal `AppRoot` landing page. | F4 | foundation |
| Q3 | ~~How reliably config-vs-view mode can be detected from the panel editor context across the supported Grafana version range (`>=12.3.0`).~~ **RESOLVED (PC3):** Sidestepped — the interactive viewport positioning is a **custom panel options editor** (`addCustomEditor`, bound to `viewportZoom`; reads full options from `context.options` and writes X/Y/zoom). Grafana renders custom option editors only in the edit pane, so the panel component never needs edit-vs-view detection and view mode stays static/non-interactive. | PC1 | panel-core |
| Q4 | ~~Whether hide-selectors and the debug overlay can apply to cross-origin direct iframes at all (DOM access is blocked), or are only meaningful for same-origin proxied content.~~ **RESOLVED (PC5):** Debug overlay is rendered in our own DOM (not the iframe's) — always cross-origin-safe, implemented in PC5. `hideSelectors` requires CSS injection into the iframe DOM which is browser-blocked for cross-origin frames; the option is kept in the schema and will be applied server-side during HTML rewriting in proxy mode — see CR5. | PC5 / CR5 | panel-core / content-rewriting |
| Q5 | Whether per-domain private-IP opt-in needs an audit-log hook in the security library or only at the consuming endpoint. | SF4 | security-foundation |
| Q6 | Resolver behaviour when a hostname returns multiple A/AAAA records — validate all and dial the first valid, or fail closed if any record is denied. | SF4 | security-foundation |
| Q7 | Whether `check-frameable` should return a `recommendedMode` for sites that error (treat ambiguous as proxy) or surface them strictly as Error. | FR1 | frameability |
| Q8 | What `/health` reports — bare liveness vs backend capability detail (e.g. proxy enabled) — which determines how availability detection interprets it. | FR2 / DF1 | frameability / direct-only-fallback |
| Q9 | Subresource-proxy URL scheme: how `/proxy` rewrites reference `/proxy-resource` — query-encoded absolute URL vs path-embedded. Shared decision. | P1 / CR2 / CR3 | proxy / content-rewriting |
| Q10 | Whether the per-request timeout is one total budget or split into separate connect and total settings. | P4 | proxy |
| Q11 | How aggressively to strip frame-buster JavaScript without breaking legitimate page scripts — needs a bounded, documented pattern set. | CR2 | content-rewriting |
| Q12 | On Grafana Cloud, what `/health` returns when the backend is simply not provisioned vs erroring, and whether backend availability can change mid-session (re-probe vs fixed per session). | DF1 | direct-only-fallback |
| Q13 | Whether the Grafana-version E2E matrix spans the full `>=12.3.0` range or a pinned subset for CI cost, and how to simulate DNS rebinding deterministically in CI for AC 21. | TC1 / TC6 | testing-cicd |
| Q14 | Which Grafana instance `rootUrls` to sign for the initial private release (depends on the stakeholder's self-hosted target). | DR8 | docs-release |
| Q15 | Whether the example dashboard's proxied URL should ship pre-allowlisted in provisioning for the demo, given the empty-by-default allowlist. | DR7 | docs-release |
| Q16 | Current catalog submission URL/review process (confirm at submission time), and whether Grafana Labs will accept a frame-header-stripping proxy on Cloud at all — be prepared to continue with Path 1 if rejected. | CP4 | catalog-prep |

## Notes

- Detailed threat-model / attack-technique content is intentionally NOT recorded here; it is a
  deferred documentation task (docs-release DR5).
