# Dependency Policy

## 通用要求

- 必须使用 lockfile
- 禁止混用包管理器
- 新增依赖必须人工确认
- 依赖升级必须走 PR
- 依赖更新必须有冷却期
- 依赖升级 PR 默认不自动合并

## JS / TS / Node / Tauri

默认 pnpm。

建议配置：

```yaml
minimumReleaseAge: 10080
trustPolicy: no-downgrade
onlyBuiltDependencies: []
ignoredBuiltDependencies: []
```

禁止自动执行 `pnpm approve-builds`。

## Python

默认 uv。

建议配置：

```toml
[tool.uv]
exclude-newer = "7 days"
```

## Rust

提交 Cargo.lock，CI 运行 cargo audit。

## Go

- 提交 `go.mod` 和 `go.sum`，禁止漏交 `go.sum`
- CI 必须使用 `GOFLAGS=-mod=readonly`，禁止 CI 期间隐式修改 `go.mod`
- CI 必须执行 `go mod verify` 校验模块完整性
- CI 必须运行 `gofmt -s -l`、`go vet`、`go test -race`、`govulncheck`
- govulncheck 必须固定版本（例如 `@v1.3.0`），禁止 `@latest`
- 新增依赖：`go get module@version` → `go mod tidy` → `go mod verify`，并通过 PR 走人工确认
- 禁止 AI 自动执行 `go get -u`、`go get ...@latest`
- 禁止使用 `replace` 指令指向私有 fork、未审计仓库或本地路径，除非 Issue 明确要求并经人工确认
- 主版本升级（v1 → v2 等）必须人工确认
