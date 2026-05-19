# Changelog

## [1.4.0](https://github.com/kuddy-ai/tabby-sync/compare/1.3.0...1.4.0) (2026-05-19)


### Features

* use GitHub App token and fix version format in release-please ([21331a4](https://github.com/kuddy-ai/tabby-sync/commit/21331a48281822207c674bb820386db6aef52ff9))
* use GitHub App token and fix version format in release-please ([bab09da](https://github.com/kuddy-ai/tabby-sync/commit/bab09da76b5e329829c8c76d5b15cf1fb97f1fd7))


### Bug Fixes

* use squash instead of rebase for release PR merge ([5847457](https://github.com/kuddy-ai/tabby-sync/commit/5847457ab74c26c9af597b1ef34f1f9d7c21f6e4))

## [1.3.0](https://github.com/kuddy-ai/tabby-sync/compare/1.2.1...1.3.0) (2026-05-19)


### Features

* add auto-merge workflow for release-please PRs ([#36](https://github.com/kuddy-ai/tabby-sync/issues/36)) ([bffb451](https://github.com/kuddy-ai/tabby-sync/commit/bffb451ef0c48c00d396aa3510dc273d0de1abc4)), closes [#30](https://github.com/kuddy-ai/tabby-sync/issues/30)
* add rate limiting, quotas, Docker deployment, and security docs ([#31](https://github.com/kuddy-ai/tabby-sync/issues/31)) ([3475b6a](https://github.com/kuddy-ai/tabby-sync/commit/3475b6a238c2d25562e762529b69d78962492b36))
* **api:** add Tabby-compatible config sync API with monotonic modified_at ([#23](https://github.com/kuddy-ai/tabby-sync/issues/23)) ([83b627b](https://github.com/kuddy-ai/tabby-sync/commit/83b627b859d1b30a2316f1e8c5ad810c2d9dcf39))
* **auth:** add users.yml loader and Bearer-token middleware ([#21](https://github.com/kuddy-ai/tabby-sync/issues/21)) ([bdfe26e](https://github.com/kuddy-ai/tabby-sync/commit/bdfe26ef94e2a672939e8d8313bf8ddcd1234bb0))
* complete rewrite of release-please auto-publish workflow ([d0462d2](https://github.com/kuddy-ai/tabby-sync/commit/d0462d2ec399a8bd856b7dc3854a8b2faf67a278))
* **crypto:** encrypt config content at rest with AES-256-GCM ([#22](https://github.com/kuddy-ai/tabby-sync/issues/22)) ([0c88154](https://github.com/kuddy-ai/tabby-sync/commit/0c881549d012e7d06524d531b5aa679c145110d5))
* implement init, user add/rm/rotate, and doctor CLI commands ([#39](https://github.com/kuddy-ai/tabby-sync/issues/39)) ([464adc9](https://github.com/kuddy-ai/tabby-sync/commit/464adc9885f3267ed87677a1f3187da047705317))
* **server:** add HTTP middleware stack and security hardening ([#19](https://github.com/kuddy-ai/tabby-sync/issues/19)) ([fa652eb](https://github.com/kuddy-ai/tabby-sync/commit/fa652eb31e9a4f545222d0760677ac2e89a79267))
* **skeleton:** initialize Go project skeleton with single-binary entry ([#18](https://github.com/kuddy-ai/tabby-sync/issues/18)) ([4705fde](https://github.com/kuddy-ai/tabby-sync/commit/4705fde0386499f09ff78a714e877e1c55afd633)), closes [#4](https://github.com/kuddy-ai/tabby-sync/issues/4)
* **store:** add SQLite-backed Store with embedded migrations ([#20](https://github.com/kuddy-ai/tabby-sync/issues/20)) ([5345354](https://github.com/kuddy-ai/tabby-sync/commit/5345354c8762313533f00c2cc7a9196beb7af6a9))


### Bug Fixes

* move auto-merge into release-please workflow ([80fe577](https://github.com/kuddy-ai/tabby-sync/commit/80fe577245d2f727e8b82f5b47e1c77b6d8d9ddb))
* move auto-merge into release-please workflow to fix GITHUB_TOKEN limitation ([7102c85](https://github.com/kuddy-ai/tabby-sync/commit/7102c8511f31e4a55b3891f614129dd010523c0b)), closes [#30](https://github.com/kuddy-ai/tabby-sync/issues/30)
* release name shows version only without software name or v prefix ([#33](https://github.com/kuddy-ai/tabby-sync/issues/33)) ([ba88cb4](https://github.com/kuddy-ai/tabby-sync/commit/ba88cb400fbcd84272e0358a02565b850597ecb9)), closes [#32](https://github.com/kuddy-ai/tabby-sync/issues/32)
* remove self-approve and use correct PR output in release-please workflow ([6897d8b](https://github.com/kuddy-ai/tabby-sync/commit/6897d8be0be1217d689a905f5343f929f02af69f))
* remove self-approve step and use correct PR output ([527f2a7](https://github.com/kuddy-ai/tabby-sync/commit/527f2a7e5d92beed0beec0d41ab45d333e308a6b)), closes [#30](https://github.com/kuddy-ai/tabby-sync/issues/30)
* use --auto with fallback for release PR merge ([ce2589a](https://github.com/kuddy-ai/tabby-sync/commit/ce2589a252bf03bfb1eb577d040ec2863a275712)), closes [#30](https://github.com/kuddy-ai/tabby-sync/issues/30)
* use correct release-please output to get PR number ([d97fc4b](https://github.com/kuddy-ai/tabby-sync/commit/d97fc4b519f4592a370e32d8b9a8b6b53556f183))
* use correct release-please output to get PR number ([8c88e44](https://github.com/kuddy-ai/tabby-sync/commit/8c88e44d7be5296ee0ec30d3290bf8bff6bce032)), closes [#30](https://github.com/kuddy-ai/tabby-sync/issues/30)


### Maintenance

* configure release-please GitHub Action for automated releases ([#27](https://github.com/kuddy-ai/tabby-sync/issues/27)) ([c2039ec](https://github.com/kuddy-ai/tabby-sync/commit/c2039ec0d6cb606db6bdd9172ed5460d39fbc212)), closes [#25](https://github.com/kuddy-ai/tabby-sync/issues/25)
* **init:** initialize repository ([7ed5bdc](https://github.com/kuddy-ai/tabby-sync/commit/7ed5bdc41b1fa4c19da3cf331b90a1daf0468ad4)), closes [#1](https://github.com/kuddy-ai/tabby-sync/issues/1)
* **init:** 将仓库基线适配到 Go 技术栈 ([#2](https://github.com/kuddy-ai/tabby-sync/issues/2)) ([29bd46e](https://github.com/kuddy-ai/tabby-sync/commit/29bd46e14d8e2d0d00161a02dfffd21c9029b18d)), closes [#1](https://github.com/kuddy-ai/tabby-sync/issues/1)
* **main:** release tabby-sync 1.0.0 ([#28](https://github.com/kuddy-ai/tabby-sync/issues/28)) ([df6665a](https://github.com/kuddy-ai/tabby-sync/commit/df6665aaa936f87723522a63895ced71974e84b1))
* **main:** release tabby-sync 1.0.1 ([#34](https://github.com/kuddy-ai/tabby-sync/issues/34)) ([63a2a42](https://github.com/kuddy-ai/tabby-sync/commit/63a2a421c221f72a8fcec47d6fd86e34e2ffb9f8))
* **main:** release tabby-sync 1.1.0 ([#37](https://github.com/kuddy-ai/tabby-sync/issues/37)) ([016ce17](https://github.com/kuddy-ai/tabby-sync/commit/016ce17637121305d6c278bee9738e8d9089a1b7))
* **main:** release tabby-sync 1.2.0 ([940cbf1](https://github.com/kuddy-ai/tabby-sync/commit/940cbf10977e0a6367f3b416f62e142a69bd574f))
* **main:** release tabby-sync 1.2.0 ([c871e3d](https://github.com/kuddy-ai/tabby-sync/commit/c871e3d2f04d14630956de8fbe240e5c16899017))
* **main:** release tabby-sync 1.2.1 ([1ec52ab](https://github.com/kuddy-ai/tabby-sync/commit/1ec52ab5d446fa106f16535db9a9feb1637a8dbf))
* **main:** release tabby-sync 1.2.1 ([b9f6416](https://github.com/kuddy-ai/tabby-sync/commit/b9f641647a213a491ede27e9db13bd9a75682e66))


### Documentation

* add v0.1 scope and roadmap documentation ([#29](https://github.com/kuddy-ai/tabby-sync/issues/29)) ([880e59a](https://github.com/kuddy-ai/tabby-sync/commit/880e59adf2eee393ed66f9bfe07c7e082792041d)), closes [#17](https://github.com/kuddy-ai/tabby-sync/issues/17)

## [1.2.1](https://github.com/kuddy-ai/tabby-sync/compare/tabby-sync-1.2.0...tabby-sync-1.2.1) (2026-05-19)


### Bug Fixes

* move auto-merge into release-please workflow ([80fe577](https://github.com/kuddy-ai/tabby-sync/commit/80fe577245d2f727e8b82f5b47e1c77b6d8d9ddb))
* use --auto with fallback for release PR merge ([ce2589a](https://github.com/kuddy-ai/tabby-sync/commit/ce2589a252bf03bfb1eb577d040ec2863a275712)), closes [#30](https://github.com/kuddy-ai/tabby-sync/issues/30)
* use correct release-please output to get PR number ([d97fc4b](https://github.com/kuddy-ai/tabby-sync/commit/d97fc4b519f4592a370e32d8b9a8b6b53556f183))
* use correct release-please output to get PR number ([8c88e44](https://github.com/kuddy-ai/tabby-sync/commit/8c88e44d7be5296ee0ec30d3290bf8bff6bce032)), closes [#30](https://github.com/kuddy-ai/tabby-sync/issues/30)

## [1.2.0](https://github.com/kuddy-ai/tabby-sync/compare/tabby-sync-1.1.0...tabby-sync-1.2.0) (2026-05-19)


### Features

* implement init, user add/rm/rotate, and doctor CLI commands ([#39](https://github.com/kuddy-ai/tabby-sync/issues/39)) ([464adc9](https://github.com/kuddy-ai/tabby-sync/commit/464adc9885f3267ed87677a1f3187da047705317))

## [1.1.0](https://github.com/kuddy-ai/tabby-sync/compare/tabby-sync-1.0.1...tabby-sync-1.1.0) (2026-05-19)


### Features

* add auto-merge workflow for release-please PRs ([#36](https://github.com/kuddy-ai/tabby-sync/issues/36)) ([bffb451](https://github.com/kuddy-ai/tabby-sync/commit/bffb451ef0c48c00d396aa3510dc273d0de1abc4)), closes [#30](https://github.com/kuddy-ai/tabby-sync/issues/30)
* add rate limiting, quotas, Docker deployment, and security docs ([#31](https://github.com/kuddy-ai/tabby-sync/issues/31)) ([3475b6a](https://github.com/kuddy-ai/tabby-sync/commit/3475b6a238c2d25562e762529b69d78962492b36))

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
