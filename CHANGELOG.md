# Changelog

## Unreleased

- **BREAKING:** 移除 OpenAI Responses（`/v1/responses`）与 Anthropic Messages（`/v1/messages`）客户端兼容入口；仅保留 Chat Completions。
- Projectized the provided Go program.
- Added Go module metadata, local build targets, and release packaging script.
- Added CI and tag-driven multi-platform release automation.
- Changed release automation to parallel matrix builds with a final publish job.
- Added README, API, configuration, deployment, release, contribution, and security docs.
- Added build metadata and `-version` flag.
