# Changelog

## Unreleased

- **BREAKING:** 移除 OpenAI Responses（`/v1/responses`）与 Anthropic Messages（`/v1/messages`）客户端兼容入口；仅保留 Chat Completions。
- Added `socks5_paid_direct`; keyed traffic now uses an active SOCKS5 proxy by default.
- Extended SOCKS5 idle connection lifetime and enabled TCP keepalive.
- Added config path resolution with `OPENCODE2API_CONFIG` and user config directory fallback.
- Added graceful shutdown on SIGINT/SIGTERM with a 10-second drain window.
- Projectized the provided Go program.
- Added Go module metadata, local build targets, and release packaging script.
- Added CI and tag-driven multi-platform release automation.
- Changed release automation to parallel matrix builds with a final publish job.
- Added README, API, configuration, deployment, release, contribution, and security docs.
- Added build metadata and `-version` flag.
