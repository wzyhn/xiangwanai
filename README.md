# 享玩 AI

独立的活动社区微信小程序项目，包含消费者小程序、运营后台、Go API/worker 和 PostgreSQL 数据库部署文件。

## 目录

| 路径 | 内容 |
| --- | --- |
| `apps/miniprogram-xiangwan` | 微信小程序 |
| `apps/xiangwan-admin-web` | Next.js 运营后台 |
| `cmd/xiangwan`、`internal/domains/xiangwan` | 产品 API、后台认证及独立 worker |
| `cmd/sqlmigrate`、`deploy/sql` | 独立基础 schema 与享玩专属迁移 |
| `internal/capabilities`、`internal/pkg`、`packages` | 实际使用的固定公共依赖 |
| `deploy/xiangwan` | 配置模板与部署入口 |

## 本地开发

需要 Go 1.25.13、Node.js 24 和 pnpm 10.8.0。

```sh
pnpm install --frozen-lockfile
pnpm test
go test ./...
go build ./cmd/xiangwan ./cmd/sqlmigrate
pnpm --filter xiangwan-admin-web dev
```

微信开发者工具导入 `apps/miniprogram-xiangwan`，执行“构建 npm”。运行配置以 `config/release.json` 为真相源，手工运行副本 `release.js` 必须逐字段一致。默认 API 为 `https://api.weconq.cn`；请使用自己的 AppID 和相应服务器配置，AppSecret 只放服务端。

开发者工具“构建 npm”会重写目录；每次构建后在仓库根依次执行 `node scripts/sync-miniprogram-workspace-packages.mjs` 和 `node scripts/sync-miniprogram-registry-packages.mjs`，恢复完整 workspace 包与 dayjs/tslib 子路径。

## 部署

见 [独立部署说明](deploy/xiangwan/README.md)。本仓库可从自身源码构建 API、迁移工具和后台，不需要原平台仓库。PostgreSQL 是唯一必需的数据服务；生产 OIDC、微信及支付资产由部署环境独立配置。

公开仓库中没有真实数据库、媒体文件、账号密码、支付私钥或原私有仓库 Git 历史。建仓、合并与镜像构建不会自动改变现有生产环境。

## 协作与集成

非程序员使用 AI 开发：从 [交接入门与可复制提示词](docs/handoff/README.md) 开始。安全约定见 [SECURITY.md](SECURITY.md)。

Fork 本仓库、创建功能分支并提交 PR，维护者检查后接受到 main。你无需访问平台私有仓库。产品源码的已接受改动可以通过限定目录映射生成私有集成 PR；共享依赖及发行支持文件在本仓库单独评审，不自动修改平台公共代码。冲突保留两边改动，由维护者处理。

见 [CONTRIBUTING.md](CONTRIBUTING.md) 与 [发行 manifest](standalone-manifest.json)。代码公开可见；仓库未额外指定开源许可证，贡献不自动取得生产部署、客户数据或品牌使用权限。
