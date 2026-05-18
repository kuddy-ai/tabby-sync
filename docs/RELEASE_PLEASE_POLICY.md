# Release Please Policy

本项目如果使用 release-please，必须保证版本 CHANGELOG 中每条可发布变更都能追溯到对应 Issue。

## 核心规则

- Commit footer 中的 `Refs: #123` 用于审计和追踪。
- PR 描述中的 `Closes #123` 或 `Refs #123` 用于 GitHub Issue 关联。
- CHANGELOG 条目中的 Issue 链接必须通过 `BEGIN_COMMIT_OVERRIDE` 明确写入。
- 禁止只依赖 commit footer，期待 release-please 自动把 Issue 链接追加到 CHANGELOG 标题后。

## PR 必填格式

```markdown
Closes #123

BEGIN_COMMIT_OVERRIDE
fix(scope): change summary ([#123](https://github.com/<owner>/<repo>/issues/123))
END_COMMIT_OVERRIDE
```

## 多条可发布变更

如果一个 PR 包含多条会进入 release notes 的变更，必须在 `BEGIN_COMMIT_OVERRIDE` 中拆成多条 Conventional Commit：

```markdown
BEGIN_COMMIT_OVERRIDE
feat(config): add secure profile ([#123](https://github.com/<owner>/<repo>/issues/123))

fix(hooks): reject commits without issue references ([#124](https://github.com/<owner>/<repo>/issues/124))
END_COMMIT_OVERRIDE
```

## 禁止事项

- 禁止编造 Issue 编号。
- 禁止把无关 Issue 链接到 CHANGELOG 条目。
- 禁止只写 `Refs: #123` 就认为 release-please 的 CHANGELOG 一定会显示 Issue 链接。
- 禁止在没有实际 release note 价值的改动中强行生成 CHANGELOG 条目。

## 不进入 release notes 的 PR

如果 PR 不应进入 release notes，例如纯文档、纯测试、纯 chore，可以在 PR 模板中说明：

```text
N/A - docs/test/chore only, no release note required.
```
