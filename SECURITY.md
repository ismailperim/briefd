# Security Policy

## Supported versions

briefd is pre-1.0. Only the latest release receives security fixes.

## Reporting a vulnerability

Please **do not** open a public issue for security problems.

Report privately via
[GitHub Security Advisories](https://github.com/ismailperim/briefd/security/advisories/new),
or by email to ismailperim@gmail.com if you cannot use GitHub.

Include a description of the issue, steps to reproduce, and the affected
version or commit. You will receive an acknowledgement within 72 hours and a
status update once the report has been triaged. Fixes are released as soon as
practical and credited to the reporter unless anonymity is requested.

## What runs automatically

- `gosec` as part of `golangci-lint` on every push and pull request.
- `govulncheck` against the Go vulnerability database (on push, PRs, and weekly).
- CodeQL analysis for Go and the dashboard's JavaScript.
- Dependency review on pull requests: fails on high-severity advisories and on
  copyleft licenses that are incompatible with Apache-2.0.
- Trivy scan of the container image (HIGH/CRITICAL, fixed vulnerabilities only).
- Dependabot for Go modules and GitHub Actions; releases ship a CycloneDX SBOM per archive.

## Scope notes

briefd is designed to run on a private network behind a single bearer token.
Exposing it directly to the public internet is not a supported configuration.
