# Contributing

## Issue 驱动开发

任何代码变更必须先有 Issue。

Issue 必须包含：

- 背景说明
- 目标
- 变更范围
- 验收标准
- 风险点
- 回滚方案，若适用

## 分支规范

分支名必须匹配：

```regex
^(feat|fix|refactor|docs|chore|perf|test|build|ci|security)/issue-[0-9]+-[a-z0-9._-]+$
```

示例：

```text
feat/issue-12-add-login
fix/issue-34-token-refresh
security/issue-56-mask-sensitive-logs
```

## Commit 规范

使用 Conventional Commits：

```text
<type>[optional scope][!]: <description>

[optional body]

[optional footer(s)]
```

必须引用 Issue：

```text
Refs: #123
Closes: #123
Fixes: #123
```

这些 footer 用于 commit 审计和追踪。

如果项目使用 release-please，不能只依赖 commit footer 生成 CHANGELOG Issue 链接。每个会进入 release notes 的 PR 必须在 PR 描述中提供 `BEGIN_COMMIT_OVERRIDE`。

`BEGIN_COMMIT_OVERRIDE` 中每条 `feat` / `fix` / `perf` / `security` / `deps` 变更，标题末尾必须包含对应 Issue 的 Markdown 链接：

```text
BEGIN_COMMIT_OVERRIDE
feat(scope): add feature ([#123](https://github.com/<owner>/<repo>/issues/123))

fix(scope): repair bug ([#124](https://github.com/<owner>/<repo>/issues/124))
END_COMMIT_OVERRIDE
```

初始化提交示例：

```text
chore(init): initialize repository

Refs: #1
```

## PR 要求

PR 必须包含：

- 关联 Issue，例如 `Closes #123` 或 `Refs #123`
- 变更说明
- 测试说明
- 安全影响说明
- 回滚方案，若适用
- 如果项目使用 release-please，必须填写 `BEGIN_COMMIT_OVERRIDE`
- 每条进入 CHANGELOG 的 `feat` / `fix` / `perf` / `security` / `deps` 变更必须带 Issue Markdown 链接

PR 标题必须遵守 Conventional Commits。

示例：

```text
fix(auth): mask access token in logs
```

release-please override 示例：

```text
BEGIN_COMMIT_OVERRIDE
fix(auth): mask access token in logs ([#123](https://github.com/<owner>/<repo>/issues/123))
END_COMMIT_OVERRIDE
```

## Git hooks

初始化：

```bash
bash scripts/setup-hooks.sh
```

Windows：

```powershell
./scripts/setup-hooks.ps1
```

## AI 参与开发规则

AI 必须遵守 `AGENTS.md`。  
AI 不得自动新增依赖、修改 CI 权限、跳过测试、安全扫描或发布制品。
