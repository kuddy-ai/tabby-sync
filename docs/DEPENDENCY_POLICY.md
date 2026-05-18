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

提交 go.sum，CI 运行 govulncheck。
