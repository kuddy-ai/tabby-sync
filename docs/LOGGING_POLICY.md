# Logging Policy

## 必须记录

- 应用启动 / 退出
- 配置加载成功 / 失败
- 关键状态变更
- 关键操作成功 / 失败
- 错误码
- 耗时
- 外部依赖调用结果

## 禁止记录

- 密码
- token
- cookie
- session
- 私钥
- 明文手机号
- 明文身份证号
- 明文邮箱
- 客户敏感信息

## 日志级别

- ERROR
- WARN
- INFO
- DEBUG

生产环境默认 INFO。DEBUG 只能在开发环境或显式配置下启用。
