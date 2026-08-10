# Security Policy

## Supported versions

Security fixes are made against the latest published release and `main`.
Operators should upgrade to the latest release before requesting a backport.

| Version | Supported |
| --- | --- |
| Latest published release | Yes |
| Older releases | No guaranteed fixes |
| Unreleased development builds | Best effort only |

## 私密报告漏洞

不要在公开 Issue、PR、Discussion 或日志中披露可利用细节。请使用仓库的
[Private Vulnerability Reporting](https://github.com/kuddy-ai/tabby-sync/security/advisories/new)
提交报告。该渠道会创建私密 Security Advisory，只有报告者和获授权维护者可见。

报告中建议包含：

- 受影响版本和部署方式
- 最小复现步骤
- 影响范围与前置条件
- 已验证的缓解措施
- 希望使用的贡献者署名

维护者应在私密 Advisory 中完成确认、修复和协调披露。只有在补丁或缓解措施
可用后，才公开发布安全说明。不要创建公开的 `security` Issue 来跟踪未披露漏洞。

## 加密落库与 master key 备份

配置 `content` 在落库前使用 AES-256-GCM 加密。每行密钥通过 HKDF-SHA256
从主密钥按用户派生，AAD 绑定加密版本、用户 ID 和配置 ID。完整格式见
[`docs/CRYPTO.md`](docs/CRYPTO.md)。

master key 是恢复加密数据的唯一凭据，丢失后没有后备恢复通道：

- 文件 provider 默认写入 `${TABBY_SYNC_DATA_DIR}/master.key`，权限为 `0600`
- `env` provider 从 `TABBY_SYNC_MASTER_KEY` 读取 64 位十六进制字符串
- 第一次写入真实配置前必须备份主密钥
- 主密钥与数据库应分别存放；两者落入同一备份等同于明文数据备份
- 轮换主密钥后必须重新验证并备份

## 禁止提交或记录

不要提交或写入日志：

- `.env`、Token、密码、Cookie、Session
- master key、私钥、证书私钥或云访问密钥
- `users.yml`、运行中的 SQLite 数据库及 WAL/SHM 文件
- 解密后的配置内容或真实客户数据
- 明文手机号、身份证号、邮箱等个人信息

## 凭据泄露处理

1. 立即撤销或轮换泄露凭据。
2. 检查 Git 历史、Actions 日志、镜像层和备份。
3. 确认是否存在异常访问或数据导出。
4. 在私密 Security Advisory 或内部事件记录中保存调查过程。
5. 修复完成后再决定公开范围和升级建议。

依赖漏洞必须确认可达性和影响范围，并通过经过完整 CI 的 PR 修复。不得通过
关闭安全检查、删除失败测试或发布未经审核的制品来绕过问题。
