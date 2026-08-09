[English](README.md) | [简体中文](README.zh-CN.md)

# tabby-sync

`tabby-sync` 是面向 [Tabby Terminal](https://tabby.sh) 的轻量自托管配置同步后端。
它由单个 Go 二进制文件提供服务，使用 SQLite、Bearer Token 认证和
AES-256-GCM 静态加密。

> [`AGENTS.md`](./AGENTS.md) 是仓库唯一的 AI Coding Agent 项目规则入口。
> 贡献者还应阅读 [`CONTRIBUTING.md`](./CONTRIBUTING.md) 和
> [`SECURITY.md`](./SECURITY.md)。

## 状态

项目已进入稳定的 **1.x** 发布线。当前状态和后续工作见
[最新 GitHub Release](../../releases/latest)、[`CHANGELOG.md`](./CHANGELOG.md)
和 [`docs/ROADMAP.md`](./docs/ROADMAP.md)。

二进制和 GHCR 镜像工作流是在 1.6.0 之后加入的，因此正式制品从 1.7.0
开始提供。代码合入 `main` 后，Release Please 会持续更新 release PR；
只有维护者手动合并该 PR 时才会发布版本。

## 兼容性与 API

六个 Tabby 兼容业务端点位于 `/api/1/` 下，全部需要 Bearer Token；
`GET /healthz` 和浏览器 CORS 预检请求无需凭据。

- 请求和响应格式：[`docs/API.md`](./docs/API.md)
- 当前 Tabby 配置流程：[`docs/CLIENT_SETUP.md`](./docs/CLIENT_SETUP.md)
- 部署说明：[`docs/DEPLOYMENT.md`](./docs/DEPLOYMENT.md)

## 功能

- 与 Tabby 兼容的配置创建、列表、读取、更新和删除接口
- 幂等 PATCH，避免多设备之间形成反复同步循环
- `users.yml` 中按哈希保存的独立用户 Bearer Token
- SQLite WAL 存储、按用户隔离和每用户 50 个配置的配额
- AES-256-GCM 静态加密及 HKDF-SHA256 用户级派生密钥
- `serve`、`init`、`doctor`、`user add`、`user rm`、`user rotate` 命令
- 结构化日志、请求 ID、请求体限制、安全响应头和速率限制
- Docker 首次启动自动创建第一个用户
- Docker Compose 与 Caddy 部署示例

## 环境要求

- Go 1.25.12 或更新且已包含安全补丁的兼容工具链（以 `go.mod` 为准）
- Git 2.34+
- 容器部署需要 Docker Engine 24+ 和 Docker Compose v2
- Tabby 客户端必须访问可信的 HTTPS 地址

本仓库只使用 Go。未经专门 Issue 和维护者批准，不要加入 JavaScript、
Python、Rust 或其他工具链。

## Docker 快速开始

```bash
git clone https://github.com/kuddy-ai/tabby-sync.git
cd tabby-sync
cp .env.example .env

# 修改 Caddyfile 中的 sync.example.com，然后启动。
docker compose up -d

# 首次启动会创建一个用户，并把只展示一次的明文 Token 保存到这里。
docker compose exec tabby-sync cat /data/token.txt

# Token 保存到 Tabby 后，删除卷中的明文副本。
docker compose exec tabby-sync rm /data/token.txt
```

命名卷 `tabby-data` 会在容器重建后继续保留 `tabby-sync.db`、`master.key`
和 `users.yml`。操作真实数据前，请先阅读
[`docs/DEPLOYMENT.md`](./docs/DEPLOYMENT.md) 中的备份与恢复警告。

## 运行配置

| 变量 | 源码运行时是否必需 | 容器默认值 | 说明 |
| --- | --- | --- | --- |
| `TABBY_SYNC_ADDR` | 否 | `:8080` | 监听地址 |
| `TABBY_SYNC_DATA_DIR` | 是 | `/data` | SQLite 与主密钥目录 |
| `TABBY_SYNC_USERS_FILE` | 是 | `/data/users.yml` | 用户凭据哈希文件 |
| `TABBY_SYNC_MASTER_KEY_PROVIDER` | 是 | `file` | `file` 或 `env` |
| `TABBY_SYNC_MASTER_KEY` | 仅 `env` 模式 | 无 | 64 位十六进制密钥；属于敏感信息 |
| `TABBY_SYNC_USER_NAME` | 否 | `default` | 仅用于 Docker 首次创建用户 |
| `APP_LOG_LEVEL` | 否 | `info` | `error`、`warn`、`info` 或 `debug` |

`.env.example` 不包含真实密钥。Go 程序不会自行读取 `.env`；源码运行时应在
shell 或进程管理器中导出变量。Docker Compose 会读取仓库中的 `.env`。

## 本地开发

首次开发前安装仓库 hooks：

```bash
bash scripts/setup-hooks.sh
# PowerShell: ./scripts/setup-hooks.ps1
```

运行与 CI 一致的检查：

```bash
go mod download
go mod verify
gofmt -s -l .
go vet ./...
go test -race -count=1 ./...
govulncheck ./...
gosec ./...
```

`govulncheck` 和 `gosec` 应使用 `.github/workflows/ci.yml` 固定的版本。

## 构建与发布制品

使用 Makefile 注入版本、提交和构建时间：

```bash
make build VERSION=1.7.0
./bin/tabby-sync version
```

从 1.7.0 起，GitHub Release 会附带：

- Linux amd64
- Linux arm64
- Windows amd64
- `SHA256SUMS`

GHCR 工作流只会在 GitHub Release 发布后推送 Linux amd64 镜像，并生成完整
semver、major/minor 和 `latest` 标签：

```bash
docker pull ghcr.io/kuddy-ai/tabby-sync:1.7.0
```

PR 和手动 workflow 只构建验证用制品，不发布正式二进制或镜像。

## 仓库结构

```text
.
├── .github/                   Issue/PR 模板与 Actions workflow
├── .githooks/                 本地提交和推送保护
├── cmd/tabby-sync/            二进制入口
├── docs/                      API、部署、加密和策略文档
├── internal/                  应用包
├── scripts/                   hook 安装脚本
├── AGENTS.md                  权威 AI Agent 规则
├── CONTRIBUTING.md            Issue、分支、提交和 PR 流程
├── SECURITY.md                安全报告与运维责任
├── Dockerfile / docker-compose.yml / Caddyfile
└── go.mod / go.sum
```

## 贡献与安全

- 每项变更先创建 Issue，再通过 PR 提交。
- 会进入版本日志的 PR 遵循
  [`docs/RELEASE_PLEASE_POLICY.md`](./docs/RELEASE_PLEASE_POLICY.md)。
- 不要提交运行数据库、`users.yml`、`.env`、Token、主密钥或真实客户数据。
- 漏洞应按 [`SECURITY.md`](./SECURITY.md) 通过私密渠道报告，不要创建公开 Issue。
- 日志要求见 [`docs/LOGGING_POLICY.md`](./docs/LOGGING_POLICY.md)。

## 许可证

[MIT](./LICENSE)
