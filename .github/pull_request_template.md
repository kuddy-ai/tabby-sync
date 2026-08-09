## 关联 Issue

Closes #

## 变更说明

-

## 测试说明

-

## Release Please Changelog Override

如果本 PR 包含 `feat` / `fix` / `perf` / `security` / `deps` 等会进入版本日志的变更，必须填写 `BEGIN_COMMIT_OVERRIDE`。

每一条会进入 CHANGELOG 的变更，标题末尾必须包含对应 Issue 的普通 `#123` 引用。不要手写 Markdown 链接，release-please 会自动生成链接。

请把下面示例复制到本段下方并替换真实内容：

<!--
BEGIN_COMMIT_OVERRIDE
fix(scope): change summary (#123)
END_COMMIT_OVERRIDE
-->

如果本 PR 不进入 release notes，请说明原因：

```text
N/A - docs/test/chore only, no release note required.
```

## 安全影响

- [ ] 未新增依赖
- [ ] 未修改认证 / 授权 / 加密 / token / session
- [ ] 未修改 CI/CD 权限
- [ ] 未新增外部网络请求
- [ ] 未新增敏感日志
- [ ] 未读取或输出密钥
- [ ] 如涉及安全改动，已说明风险点、防护方案、测试方案和回滚方案

## AI 参与情况

- [ ] AI 生成或修改过代码
- [ ] 已人工 review AI 生成内容
- [ ] AI 未绕过测试、lint、类型检查或安全扫描
