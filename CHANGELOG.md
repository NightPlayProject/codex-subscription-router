# Changelog

All notable changes follow [Keep a Changelog](https://keepachangelog.com/) and
this project uses [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- One-command installer with safe source updates, prerequisite checks, signed
  rebuilds, recoverable upgrades, and automatic launch.
- Reset-aware routing that prioritizes weekly quota at risk of expiring and
  gives a bounded boost to subscriptions with banked usage resets.
- A fail-closed Windows x64 staging path for the Microsoft Store Codex app,
  including an isolated profile, Windows process handling, account controls,
  and exact official package/ASAR verification.

## [0.1.0] - 2026-08-15

### Added

- Multi-subscription routing with quota-aware balancing and sticky threads.
- Account isolation, device-code sign-in, pooled usage, and quota failover.
- Native account menu, masked emails, plan labels, and profile photos.
- Combined Profile statistics with per-account selection.
- Account-scoped Apps and MCP connection state in Settings → Plugins.
- Per-account rate-limit reset selection and pooled depletion handling.
- Independently signed Appshots and Computer Use support.
- Fail-closed upstream compatibility checks and deepest-first nested helper signing.
- Loopback-only, token-authenticated diagnostic UI states.
- Source-only CI, draft release automation, security documentation, and smoke tests.

[Unreleased]: https://github.com/b-nnett/codex-subscription-router/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/b-nnett/codex-subscription-router/releases/tag/v0.1.0

## Windows history, goal routing, and window isolation

- Transfer per-task paginated history indexes and goal counters alongside rollout files; validate matching database schemas and preserve unrelated tasks.
- Pause goal continuation during migration and activate it after ownership commits. Route goal start/resume requests and usage-limited goal notifications through subscription selection, with bounded retries.
- Apply manual subscription changes at turn boundaries, including autonomous goals.
- Mount subscription controls only in the main workspace; hide them behind native dialogs and menus, and exclude pet windows.
- Add opt-in native app-server migration verification with disposable profiles, repeated account moves, changed paginated heads, and goal counter checks. Set CODEX_ROUTER_TEST_EXECUTABLE to run it.

Native migration has been verified without account credentials or generated replies. Authenticated quota failover, live goal continuation, and full native feature parity still require end-to-end verification. Unknown database schema combinations are rejected rather than migrated speculatively.
