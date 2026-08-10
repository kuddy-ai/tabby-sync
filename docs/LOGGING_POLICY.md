# 日志策略

本策略描述 `tabby-sync serve` 的当前日志契约。服务日志由 Go `slog` 以 JSON
写入 stderr；CLI 管理命令的交互式 stdout/stderr 不属于 HTTP 访问日志。

## 默认级别

`APP_LOG_LEVEL` 支持 `error`、`warn`、`info` 和 `debug`，生产环境默认
`info`。`debug` 仅用于短时间诊断，启用后仍不得输出密钥或配置正文。

## HTTP 访问日志

每个请求在完成后写一条 `http access` 记录，字段为：

- `method`
- `path`（仅 URL path，不记录 query string）
- `status`
- `duration_ms`
- `bytes`
- `remote_ip`
- `request_id`
- `user_agent`

`remote_ip` 是尽力而为的观测字段。它可能来自可信私网代理转发的
`X-Forwarded-For`，不能视为经过认证的客户端身份，也不能作为唯一安全审计证据。
认证、授权和用户隔离不得依赖该字段。

认证成功的 debug 日志可以包含数字 `user_id` 和非敏感的显示名
`user_name`。显示名不得使用邮箱、手机号或其他个人信息。认证失败日志不得包含
Token、Token 前缀或可区分用户的信息。

## 启动与错误日志

- 记录版本、提交、监听地址、启动、就绪、关闭和超时状态。
- 数据目录和凭据文件位置只记录为 `<set>` / `<unset>`，启动错误中的实际路径在
  写日志前清除。
- API 存储错误只记录操作名、用户 ID 和可用时的配置 ID；底层包装错误不会进入
  日志。
- 解密失败使用固定消息 `decrypt failure`，不附带密文、明文或底层错误。
- panic 恢复器会记录请求元数据、panic 类型、panic 值和 Go stack。生产代码因此
  绝不能用外部输入、请求正文、配置内容、Token、密钥或路径构造 panic 值。

## 永不记录

- `Authorization`、Bearer Token、Token 前缀、Cookie、Session
- master key、私钥、证书私钥、环境变量密钥
- 解密后的 Tabby 配置、完整请求正文或密文/nonce
- `users.yml` 内容、数据库内容或真实凭据路径
- 明文邮箱、手机号、证件号和其他客户敏感信息

新增日志字段、改变错误包装或调整 panic 恢复行为时，必须补充防泄漏测试，并按
`AGENTS.md` 的安全自检要求进行评审。
