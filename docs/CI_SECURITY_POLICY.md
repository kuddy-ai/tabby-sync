# CI Security Policy

## 默认权限

```yaml
permissions:
  contents: read
```

## 禁止

- permissions: write-all
- PR 检查中使用 secrets
- 在 pull_request_target 中 checkout 或执行 PR 代码
- fork PR 自动获得 secrets
- 发布 job 和测试 job 混用
- curl | bash
- 未审核第三方 Action

## 发布要求

- 发布 job 单独隔离
- 仅 tag / release / 受保护分支触发
- 需要 environment protection 或人工审批
- 发布前重新 checkout clean workspace
- 不复用来自 PR 的 cache
