# Developer Guide

This guide explains how to set up a development environment, build, run, test, and
debug the **Webview** Grafana App plugin (`wilsonwaters-webview-app`) locally.

It is aimed at contributors working on the plugin itself. For user-facing
installation and configuration, see the companion docs listed under
[Related documentation](#related-documentation).

> Every command in this guide maps to a real script in `package.json`, a real
> `mage` target, or a real `docker compose` service. If a command stops working,
> check those files first — they are the source of truth.

---

## Contents

- [Prerequisites](#prerequisites)
- [Repository structure](#repository-structure)
- [First-time setup](#first-time-setup)
- [Development workflow](#development-workflow)
- [Running tests](#running-tests)
- [Lint, format, and type checking](#lint-format-and-type-checking)
- [Debugging](#debugging)
- [Multi-version E2E testing](#multi-version-e2e-testing)
- [Validating the packaged plugin](#validating-the-packaged-plugin)
- [Security test suite](#security-test-suite)
- [The managed `.config/` directory](#the-managed-config-directory)
- [Related documentation](#related-documentation)

---

## Prerequisites

| Tool | Version | Notes |
|------|---------|-------|
| **Node.js** | `>= 22` | Pinned in `.nvmrc` (`22`); also enforced by the `engines` field in `package.json`. Use `nvm use` to match. |
| **npm** | `10.x` | The repo pins `npm@10.9.7` via the `packageManager` field. |
| **Go** | `1.27.2` | The `go` directive in `go.mod`. With the default `GOTOOLCHAIN=auto`, an older local Go downloads 1.27.2 automatically. |
| **Mage** | latest | `go install github.com/magefile/mage@latest`. Ensure `$(go env GOPATH)/bin` is on your `PATH`. |
| **Docker** + **Docker Compose v2** | current | The `docker compose` subcommand (v2) is required by the dev server. |
| **Playwright Chromium** | installed via npm | Needed for end-to-end tests (see [Running tests](#running-tests)). |

This is an **app plugin with a Go backend** (`"backend": true`, `"executable": "gpx_webview"`
in `src/plugin.json`). You therefore need both the Node and Go toolchains to build
it fully.

Toolchain as of v1.0.0: create-plugin scaffold 7.13.0, `@grafana/*` 13.2.4 with
React 19 (the code stays React 18 compatible, because Grafana 12.4–13.1 run
React 18), `@grafana/plugin-e2e` 3.15, grafana-plugin-sdk-go v0.297.0.

### Windows workstations

`@grafana/create-plugin` does not support Windows. On a Windows checkout the
frontend build emits no `module.js` (a glob is built from a backslash path), and
Jest finds no tests when the path contains a dot directory. Run the Node
toolchain in a Linux container (or WSL), with `node_modules` in a named volume
so the Linux binaries do not clash with a Windows install:

```powershell
docker run --rm -v "${PWD}:/work" -v "webview-node-modules:/work/node_modules" -w /work node:22 `
  bash -c "npm ci && npm run typecheck && npm run lint && npm run test:ci && npm run build"
```

- `mage -v buildAll`, `go vet` and `bash scripts/security-suite.sh` work natively.
  `go test -race` needs cgo; run it in the `golang:1.27.2` image.
- For e2e tests, run Playwright in the official image on the Grafana container's
  network:

  ```powershell
  docker run --rm --network container:wilsonwaters-webview-app -v "${PWD}:/work" `
    -v "webview-node-modules:/work/node_modules" -w /work --ipc=host `
    mcr.microsoft.com/playwright:v1.64.1-noble npx playwright test --retries=2
  ```

- `.gitattributes` keeps `*.sh` files at LF line endings so the dev container's
  entrypoint works. If an old checkout still has CRLF, delete the file and run
  `git checkout -- <file>`.

---

## Repository structure

```
.
├── src/                     Frontend (TypeScript/React) source
│   ├── module.tsx           App plugin entry point
│   ├── plugin.json          App plugin manifest (id, backend flag, grafanaDependency)
│   ├── components/          App + AppConfig React components
│   └── panels/webview/      Nested "Web View" panel plugin (wilsonwaters-webview-panel)
├── pkg/                      Backend (Go) source
│   ├── main.go              Backend entry point
│   ├── plugin/              Proxy, resources, config, health, rewrite, metrics
│   └── security/            Allowlist, IP blocklist, rate limiter
├── tests/                   Playwright E2E specs (*.spec.ts) + fixtures
├── docs/                    Project documentation (this file lives here)
├── scripts/                 security-suite.sh (non-skippable security gate)
├── provisioning/            Grafana provisioning for the dev/E2E server
├── ai-state/                Project working notes (RUNBOOK, progress, decisions)
├── .github/workflows/       CI, release, compatibility, bundle-stats workflows
├── .config/                 MANAGED by Grafana plugin tools — do not edit (see below)
├── Magefile.go              Mage entry point (imports the Grafana Go SDK build targets)
├── docker-compose.yaml      Dev Grafana server (extends .config/docker-compose-base.yaml)
├── playwright.config.ts     Playwright/E2E configuration
├── package.json             Frontend scripts and dependencies
└── go.mod / go.sum          Go module definition
```

The plugin ships **two** plugin IDs: the app (`wilsonwaters-webview-app`) and the
nested panel (`wilsonwaters-webview-panel`) bundled under `src/panels/webview/`.
Both are allow-listed for unsigned loading in the dev `docker-compose.yaml`.

---

## First-time setup

From the repository root:

```bash
nvm use                            # match Node to .nvmrc (optional but recommended)
npm install                        # install frontend dependencies
npx playwright install chromium    # install the E2E browser
                                   #   system deps: npx playwright install-deps chromium
npm run build                      # build the frontend into dist/
mage -v build:backend              # build the Go backend for your host OS -> dist/
```

> **Backend architecture must match the container.** The dev Grafana container is
> `linux/amd64`. When you build the backend specifically for the container, use
> `mage -v build:linux` (produces `dist/gpx_webview_linux_amd64`). Use
> `mage -v build:backend` when you only need a binary for your own host during
> pure-frontend work. `mage -v buildAll` builds every supported target (this is
> what CI runs).

---

## Development workflow

### Frontend (watch mode)

```bash
npm run dev        # webpack --watch; rebuilds dist/ on every source change
```

Frontend changes hot-reload in the running Grafana via the webpack livereload
plugin — no container restart needed.

### Backend

```bash
mage -v build:backend     # or build:linux for the dev container
```

The backend is **not** hot-reloaded. After every Go change, rebuild and restart
the container:

```bash
mage -v build:linux
docker compose restart
```

### Run the dev Grafana server

```bash
npm run server       # == docker compose up --build
# or, detached:
docker compose up -d --build
```

- Grafana is served at **http://localhost:3000**. You are signed in
  automatically as an anonymous **Admin**.
- The compose file mounts `./dist` into the container's plugin directory and sets
  `GF_PLUGINS_ALLOW_LOADING_UNSIGNED_PLUGINS=wilsonwaters-webview-app,wilsonwaters-webview-panel`,
  so the **unsigned** dev build loads without signing.
- It mounts `./provisioning`, which allowlists `example.com` for manual testing
  and the e2e tests. Grafana applies it at every start, so allowlist changes you
  save on the Configuration page are lost on restart.
- Tear down with `docker compose down`.

> **Local development only.** The dev container enables anonymous access with the
> **Admin** role, runs Grafana in development mode and loads unsigned plugins.
> Never expose it to an untrusted network, and never copy these settings to a
> production Grafana. To test like CI (anonymous access off, development mode
> off), start it with
> `ANONYMOUS_AUTH_ENABLED=false DEVELOPMENT=false docker compose up -d --build`.

### Loading the unsigned plugin locally

You do not need to sign the plugin for local development. Grafana normally refuses
to load unsigned plugins; the dev `docker-compose.yaml` bypasses this by
allow-listing both plugin IDs (shown above). Build the frontend and backend into
`dist/`, bring the server up, and the plugin appears under
**Administration → Plugins**.

Verify it loaded:

```bash
curl -s http://localhost:3000/api/health
curl -s http://localhost:3000/api/plugins/wilsonwaters-webview-app/settings
# backend resource calls:
#   http://localhost:3000/api/plugins/wilsonwaters-webview-app/resources/<route>
```

> Signing is only required for distribution. See [`docs/signing.md`](./signing.md).

---

## Running tests

### Frontend unit tests (Jest)

```bash
npm run test:ci      # jest --passWithNoTests --maxWorkers 4 (the CI invocation)
npm run test         # jest --watch --onlyChanged (interactive, for local iteration)
```

### Backend unit tests (Go)

```bash
mage test            # the Grafana SDK backend test target (what CI runs)
# or directly, with the race detector:
go test ./... -race
```

### End-to-end tests (Playwright + `@grafana/plugin-e2e`)

E2E tests live in `tests/` as `*.spec.ts` and use the `@grafana/plugin-e2e`
fixtures (imported via `tests/fixtures.ts`), **not** raw `@playwright/test`
navigation. Before writing or changing E2E tests, read
[`.config/AGENTS/e2e-testing.md`](../.config/AGENTS/e2e-testing.md).

Run the dev server in one terminal and the tests in another:

```bash
# terminal 1
npm run server

# terminal 2
npm run e2e          # == playwright test
```

`playwright.config.ts` already sets `retries: 2` under CI (`process.env.CI`).
Locally, retries default to `0`. Some visualization-selection steps
(`setVisualization`) can be flaky under load; if you hit intermittent failures
locally, re-run with retries:

```bash
npm run e2e -- --retries=2
```

The HTML report is written by Playwright's `html` reporter; open it with
`npx playwright show-report`.

---

## Lint, format, and type checking

```bash
npm run typecheck    # tsc --noEmit
npm run lint         # eslint --cache .
npm run lint:fix     # eslint --fix + prettier --write on changed files
```

Backend linting uses `golangci-lint` (CI runs `golangci-lint run ./...`; config in
`.golangci.yml`). Install it locally from https://golangci-lint.run if you want to
reproduce the CI backend lint step.

The frontend production build also runs type checking via the webpack
`fork-ts-checker` plugin:

```bash
npm run build
```

Run the same checks CI requires before opening a PR (see also
[`CONTRIBUTING.md`](../CONTRIBUTING.md)):

```bash
npm run lint
npm run test:ci
npm run build
mage -v build:linux
```

---

## Debugging

### Frontend

- Run `npm run dev` for source maps and livereload.
- Use the browser devtools against http://localhost:3000. Panel and config
  components are plain React, so React DevTools work normally.
- Jest tests can be debugged with `node --inspect-brk node_modules/.bin/jest --runInBand <file>`.

### Backend

- The dev container exposes the **Delve** debugger on port **:2345** (configured in
  `.config/`), so you can attach a Go debugger to the running backend process.
- Add a remote-debug configuration in your editor pointing at `localhost:2345`.
- Rebuild (`mage -v build:linux`) and `docker compose restart` after each change so
  the container picks up the new binary.
- Backend logs surface in `docker compose logs -f grafana`.

---

## Multi-version E2E testing

The plugin supports Grafana **>= 12.4.0** (`grafanaDependency` in `src/plugin.json`).
E2E tests should pass against the supported range.

Locally, pick the Grafana version with environment variables passed to the server:

```bash
# minimum supported version
GRAFANA_VERSION=12.4.0 npm run server

# latest dev image (forward-compatibility check)
GRAFANA_IMAGE=grafana-dev GRAFANA_VERSION=<latest-dev-tag> npm run server

# the way CI runs one matrix entry (anonymous access and development mode off)
ANONYMOUS_AUTH_ENABLED=false DEVELOPMENT=false GRAFANA_VERSION=13.2.4 \
  docker compose up -d --build --force-recreate
```

Then run `npm run e2e` in a second terminal. Use `--force-recreate` between
runs so each run starts from the provisioned settings.

In CI, the version matrix is **auto-resolved** by the official
`grafana/plugin-actions/e2e-version` action (see
[`.github/workflows/ci.yml`](../.github/workflows/ci.yml)) rather than pinned by
hand. It derives the matrix from the plugin's `grafanaDependency`, so the E2E
matrix tracks the supported range (latest patch of every supported minor, plus the
latest dev image) automatically as new Grafana versions ship.

---

## Validating the packaged plugin

Before release — and any time you touch `plugin.json`, assets, or packaging — run
the Grafana plugin validator against a packaged build. The repository provides a
**validate-plugin** skill (`.config/AGENTS/skills/validate-plugin.md`) that wraps
`@grafana/plugin-validator`.

The validator runs on a **zip of `dist/`**, so build first (the companion
**build-plugin** skill at `.config/AGENTS/skills/build-plugin.md` builds frontend +
backend), then package and validate:

```bash
# 1. build frontend + backend into dist/
npm run build
mage -v build:linux
chmod 0755 dist/gpx_*

# 2. package dist/ as the plugin id
PLUGIN_ID=wilsonwaters-webview-app
cp -r dist "${PLUGIN_ID}"
zip -qr "${PLUGIN_ID}.zip" "${PLUGIN_ID}"
rm -rf "${PLUGIN_ID}"

# 3. run the validator (npx preferred, docker as fallback)
npx -y @grafana/plugin-validator@latest -jsonOutput "${PLUGIN_ID}.zip"
```

CI also validates automatically: `Check plugin.json` is a hard gate on
`plugin.json` schema validity, and `Validate plugin (full, advisory)` surfaces the
complete validator output on every PR (advisory). The hard, fail-closed full
validator gate runs in `release.yml` on the signed release package.

---

## Security test suite

The backend has a dedicated security test suite covering SSRF defence, allowlist
and IP-blocklist enforcement, scheme restrictions, redirect handling, response
size limits, rate limiting, header sanitisation, request auditing, and metrics
(acceptance criteria **AC 17–29**). These tests live in:

- `pkg/plugin/proxy_security_ssrf_test.go` (AC 17–22)
- `pkg/plugin/proxy_security_limits_test.go` (AC 23–29)
- supporting tests in `pkg/security/`

### Non-skippable gate

`scripts/security-suite.sh` is a **non-skippable** CI gate (AC 36). Unlike an
ordinary `go test -run <regexp>` — which exits `0` even when its regexp matches
nothing — this script:

1. **Fails if the backend is absent** (no `Magefile.go`), so a missing backend
   cannot be treated as "nothing to test".
2. Runs the security tests with `-count=1` (no cache) and captures the output.
3. Asserts a `--- PASS:` line exists for **every** required AC test function. A
   deleted, renamed, `t.Skip`-ped, or failing test therefore **fails** CI.

Run it locally exactly as CI does:

```bash
./scripts/security-suite.sh
```

It runs from the repository root regardless of where you invoke it. When you add a
new security AC test, add its function name to the `REQUIRED_TESTS` list in the
script so the gate is updated deliberately.

> The GitHub Actions job `Security suite (non-skippable)` runs this script on every
> push and PR, separate from the main build job and **not** gated by any
> `has-backend` condition. Making it a *required* status check (so a PR cannot
> merge while it is red) is a branch-protection setting a repo admin configures; it
> cannot be enforced from the workflow file alone.

---

## The managed `.config/` directory

The entire `.config/` directory is **auto-generated and managed by Grafana plugin
tools**. **Do not edit files inside it** — including the webpack, Jest, ESLint,
TypeScript, and Docker configuration — because your changes will be overwritten
when the configuration is updated.

To customise tooling, extend the base configuration from the **project root**
instead:

- **Webpack** — frontend builds must use the webpack config under `.config/webpack/`
  (the `build`/`dev` scripts already point at it). Extend it from the root per the
  guide below.
- **ESLint** — edit `eslint.config.mjs` in the root.
- **Prettier** — edit `.prettierrc.js` in the root.
- **TypeScript** — `tsconfig.json` in the root extends `.config/tsconfig.json`.
- **Jest** — `jest.config.js` / `jest-setup.js` in the root.

Follow the official guide for extending any of these:
https://grafana.com/developers/plugin-tools/how-to-guides/extend-configurations.md

> Changes to `plugin.json` (including anything in `src/plugin.json`) require a
> **restart of the Grafana server** to take effect. Do not change the plugin ID or
> plugin type.

---

## Related documentation

- [`README.md`](../README.md) — project overview, features, compatibility
- [`CONTRIBUTING.md`](../CONTRIBUTING.md) — branch naming, Conventional Commits, PR process, required checks
- [`docs/architecture.md`](./architecture.md) — system architecture (direct vs. proxy mode, backend design)
- [`docs/administration.md`](./administration.md) — installation and configuration for operators
- [`docs/signing.md`](./signing.md) — plugin signing for distribution
- [`.config/AGENTS/e2e-testing.md`](../.config/AGENTS/e2e-testing.md) — E2E testing conventions
- [`SECURITY.md`](../SECURITY.md) — security policy and vulnerability reporting
- Grafana plugin developer documentation: https://grafana.com/developers/plugin-tools/
