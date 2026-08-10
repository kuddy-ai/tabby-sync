# CI 与发布安全策略

本仓库当前只有 Go 项目。PR 和 `main` 分支运行代码检查与安全基线；二进制和
Docker 镜像在独立工作流中验证，但只有 GitHub Release 发布后才会对外发布。

## 最小权限

所有工作流默认只授予：

```yaml
permissions:
  contents: read
```

只有具体发布 job 可以按需获得 `contents: write` 或 `packages: write`。Release
Please 工作流的 `GITHUB_TOKEN` 仍为只读；创建和更新 release PR 所需的写权限
来自仓库 GitHub App 生成的短期 Token。该工作流不会由外部 PR 直接触发。

## PR 安全边界

- 不使用 `pull_request_target` 执行或 checkout PR 代码。
- PR 检查不得读取发布凭据或其他仓库 Secret。
- PR 构建出的 artifact 仅用于验证，不会直接发布。
- Go 模块以 `-mod=readonly` 校验，工具链版本由 `go.mod` 固定。
- Gitleaks 镜像使用版本和摘要双重固定；Go 安全工具使用明确版本。
- Action 升级必须像依赖升级一样经过评审。

## 发布门禁

Release Please 只持续更新 release PR，不自动合并。维护者人工合并 release PR
后，GitHub Release 才会触发发布工作流。发布 job 会从该 release 指向的修订重新
构建，不复用 PR artifact。

依赖缓存和 Docker 构建缓存可以用于加速，但缓存不是发布产物；最终二进制和镜像
必须在 `release: published` 事件中重新生成。正式构建必须注入 release 版本、提交
SHA 和发布时间，确保 `tabby-sync version` 可追溯。

## 禁止事项

- `permissions: write-all`
- 在 PR job 中使用发布 Secret
- 在 `pull_request_target` 中执行不受信任代码
- 从 fork PR 自动获得 Secret
- 将测试 job 和发布 job 合并为同一个权限边界
- `curl | bash` 或等价的未审查远程脚本执行
- 使用未固定的 `@latest` 工具作为发布或安全检查依赖
- 将 PR artifact 直接附加到正式 Release
