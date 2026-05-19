# Changelog

## [1.0.1](https://github.com/kuddy-ai/tabby-sync/compare/tabby-sync-v1.0.0...tabby-sync-1.0.1) (2026-05-19)


### Bug Fixes

* release name shows version only without software name or v prefix ([#33](https://github.com/kuddy-ai/tabby-sync/issues/33)) ([ba88cb4](https://github.com/kuddy-ai/tabby-sync/commit/ba88cb400fbcd84272e0358a02565b850597ecb9)), closes [#32](https://github.com/kuddy-ai/tabby-sync/issues/32)

## 1.0.0 (2026-05-19)


### Features

* **api:** add Tabby-compatible config sync API with monotonic modified_at ([#23](https://github.com/kuddy-ai/tabby-sync/issues/23)) ([83b627b](https://github.com/kuddy-ai/tabby-sync/commit/83b627b859d1b30a2316f1e8c5ad810c2d9dcf39))
* **auth:** add users.yml loader and Bearer-token middleware ([#21](https://github.com/kuddy-ai/tabby-sync/issues/21)) ([bdfe26e](https://github.com/kuddy-ai/tabby-sync/commit/bdfe26ef94e2a672939e8d8313bf8ddcd1234bb0))
* **crypto:** encrypt config content at rest with AES-256-GCM ([#22](https://github.com/kuddy-ai/tabby-sync/issues/22)) ([0c88154](https://github.com/kuddy-ai/tabby-sync/commit/0c881549d012e7d06524d531b5aa679c145110d5))
* **server:** add HTTP middleware stack and security hardening ([#19](https://github.com/kuddy-ai/tabby-sync/issues/19)) ([fa652eb](https://github.com/kuddy-ai/tabby-sync/commit/fa652eb31e9a4f545222d0760677ac2e89a79267))
* **skeleton:** initialize Go project skeleton with single-binary entry ([#18](https://github.com/kuddy-ai/tabby-sync/issues/18)) ([4705fde](https://github.com/kuddy-ai/tabby-sync/commit/4705fde0386499f09ff78a714e877e1c55afd633)), closes [#4](https://github.com/kuddy-ai/tabby-sync/issues/4)
* **store:** add SQLite-backed Store with embedded migrations ([#20](https://github.com/kuddy-ai/tabby-sync/issues/20)) ([5345354](https://github.com/kuddy-ai/tabby-sync/commit/5345354c8762313533f00c2cc7a9196beb7af6a9))


### Maintenance

* configure release-please GitHub Action for automated releases ([#27](https://github.com/kuddy-ai/tabby-sync/issues/27)) ([c2039ec](https://github.com/kuddy-ai/tabby-sync/commit/c2039ec0d6cb606db6bdd9172ed5460d39fbc212)), closes [#25](https://github.com/kuddy-ai/tabby-sync/issues/25)
* **init:** initialize repository ([7ed5bdc](https://github.com/kuddy-ai/tabby-sync/commit/7ed5bdc41b1fa4c19da3cf331b90a1daf0468ad4)), closes [#1](https://github.com/kuddy-ai/tabby-sync/issues/1)
* **init:** 将仓库基线适配到 Go 技术栈 ([#2](https://github.com/kuddy-ai/tabby-sync/issues/2)) ([29bd46e](https://github.com/kuddy-ai/tabby-sync/commit/29bd46e14d8e2d0d00161a02dfffd21c9029b18d)), closes [#1](https://github.com/kuddy-ai/tabby-sync/issues/1)


### Documentation

* add v0.1 scope and roadmap documentation ([#29](https://github.com/kuddy-ai/tabby-sync/issues/29)) ([880e59a](https://github.com/kuddy-ai/tabby-sync/commit/880e59adf2eee393ed66f9bfe07c7e082792041d)), closes [#17](https://github.com/kuddy-ai/tabby-sync/issues/17)

## Changelog

All notable changes to this project will be documented in this file.

This file is automatically managed by [release-please](https://github.com/googleapis/release-please).
Do not edit manually.
