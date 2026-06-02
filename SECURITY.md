# Security Policy

> **Status:** This is the initial reporting policy. A full security-architecture
> and threat-model section will be added in a dedicated security-documentation
> milestone, and operational guidance will live in `docs/administration.md`.

## Reporting a vulnerability

**Please do not report security vulnerabilities through public GitHub issues,
discussions, or pull requests.**

Instead, report them privately through GitHub Security Advisories:

- https://github.com/wilsonwaters/grafana-webview-app/security/advisories/new

Use the "Report a vulnerability" flow. This creates a private channel visible
only to the reporter and the maintainers.

When reporting, please include as much of the following as you can:

- A description of the issue and its potential impact
- The plugin version and Grafana version affected
- Steps to reproduce, or a proof of concept
- Any relevant configuration (e.g. deployment method)
- Suggested remediation, if you have one

Please give us a reasonable opportunity to investigate and remediate before any
public disclosure.

## Scope

**In scope** (high level): the plugin's frontend code, the Go backend, and the
backend's request-handling endpoints. Because this plugin includes a backend
component that can fetch external URLs on behalf of a dashboard author, the
backend's request-validation and access-control behaviour is of particular
interest.

**Out of scope:** vulnerabilities in Grafana itself, in third-party sites the
plugin is configured to display, or in the deployment environment. Report those
to the respective projects or vendors.

## Supported versions

The plugin is in active development and has not yet had a stable release. Until
`1.0.0` is published, security fixes are applied to the default development
branch only.

| Version            | Supported          |
| ------------------ | ------------------ |
| `main` (`develop`) | :white_check_mark: |
| Pre-release builds | :warning: best effort |

This table will be updated once tagged releases exist.

## Response expectations

As a community project we aim to:

- Acknowledge a report within **5 business days**
- Provide an initial assessment within **10 business days**
- Keep the reporter informed of remediation progress

These are targets, not guarantees, and may vary with maintainer availability.

## Security design (deferred)

This plugin's backend is designed with security as a first-class concern
(fail-closed defaults, strict request validation, and conservative
header handling). The detailed threat model, the controls that mitigate each
risk, and operator guidance are intentionally documented separately and will be
published in:

- `SECURITY.md` (this file — expanded threat-model section), and
- `docs/administration.md` (operator-facing security guidance), and
- `docs/architecture.md` (architecture and decision log)

as part of the security-documentation milestone.
