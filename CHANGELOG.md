# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Project scaffold: Grafana app plugin (`wilsonwaters-webview-app`) with a Go backend, created via `@grafana/create-plugin`.
- Repository hygiene: README, CONTRIBUTING, CODE_OF_CONDUCT, SECURITY policy (stub), issue and pull-request templates.
- Local development environment (Docker Compose + Grafana) and Playwright-based end-to-end test setup.
- Plugin signing wired into the release workflow via the `GRAFANA_ACCESS_POLICY_TOKEN` secret, documented in `docs/signing.md`.
- Project plan and orchestration material under `ai-state/` (brief, stream decomposition, per-stream master plans, runbook).

[Unreleased]: https://github.com/wilsonwaters/grafana-webview-app/commits/main
