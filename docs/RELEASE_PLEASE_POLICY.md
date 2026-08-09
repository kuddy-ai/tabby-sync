# Release Please Policy

本项目使用 release-please，必须保证版本 CHANGELOG 中每条可发布变更都能追溯到对应 Issue。

## 发布流程

- 每次向 `main` 合并变更后，Release Please 创建或更新同一个 release PR。
- release PR 不得自动合并；维护者决定发布时间并手动合并。
- release PR 合并后产生的 GitHub Release 才会触发正式二进制和镜像发布。
- 普通 PR、`main` push 和手动验证 workflow 都不得发布正式制品。

## 核心规则

- Commit footer 中的 `Refs: #123` 用于审计和追踪。
- PR 描述中的 `Closes #123` 或 `Refs #123` 用于 GitHub Issue 关联。
- CHANGELOG 条目中的 Issue 引用必须通过 `BEGIN_COMMIT_OVERRIDE` 明确写入。
- 禁止只依赖 commit footer，期待 release-please 自动把 Issue 链接追加到 CHANGELOG 标题后。
- override 中使用普通 `#123` 引用；不要预先写成 Markdown 链接，否则 release-please 会生成嵌套链接。

## PR 必填格式

```markdown
Closes #123

BEGIN_COMMIT_OVERRIDE
fix(scope): change summary (#123)
END_COMMIT_OVERRIDE
```

## 多条可发布变更

如果一个 PR 包含多条会进入 release notes 的变更，必须在 `BEGIN_COMMIT_OVERRIDE` 中拆成多条 Conventional Commit：

```markdown
BEGIN_COMMIT_OVERRIDE
feat(config): add secure profile (#123)

fix(hooks): reject commits without issue references (#124)
END_COMMIT_OVERRIDE
```

## 禁止事项

- 禁止编造 Issue 编号。
- 禁止把无关 Issue 链接到 CHANGELOG 条目。
- 禁止只写 `Refs: #123` 就认为 release-please 的 CHANGELOG 一定会显示 Issue 链接。
- 禁止在 override 中手写 `[#123](...)`，避免 release-please 生成双重链接。
- 禁止在没有实际 release note 价值的改动中强行生成 CHANGELOG 条目。

## 不进入 release notes 的 PR

如果 PR 不应进入 release notes，例如纯文档、纯测试、纯 chore，可以在 PR 模板中说明：

```text
N/A - docs/test/chore only, no release note required.
```
