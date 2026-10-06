# 检查命令与故障定位

以下命令在仓库根运行。Windows 示例使用 PowerShell；AI 应按接收电脑系统调整，不硬编码发送者的用户目录。

```powershell
git status --short
git remote -v
gh auth status
node --version
pnpm --version
go version
pnpm install --frozen-lockfile
pnpm test
$env:GOTOOLCHAIN='auto'
go test ./...
go build ./cmd/xiangwan ./cmd/sqlmigrate
$env:XIANGWAN_ADMIN_API_ORIGIN='http://localhost:8080'
pnpm --filter xiangwan-admin-web lint
pnpm --filter xiangwan-admin-web typecheck
pnpm --filter xiangwan-admin-web build
node scripts/sync-miniprogram-workspace-packages.mjs
node scripts/sync-miniprogram-registry-packages.mjs
node scripts/check-public-boundary.mjs
gitleaks git . --log-opts='--all --full-history' --redact=100 --no-banner
```

`check-public-boundary` 检查 Git 暂存区，提交前先暂存本次任务的明确文件，再运行它；忽略文件没有暂存也不能强行加入。Gitleaks 按官方固定版本安装，CI 固定 8.30.1 并校验下载哈希。

| 现象 | 定位与处理 |
| --- | --- |
| GitHub 无法推送 | 查询登录账号与 origin；用自己的 Fork，或由所有者邀请该账号为公开仓库 write 协作者；不需要私有平台账号 |
| main 不能直接推送 | 创建分支与 PR，等待必要 CI；不要关掉保护或强推来完成普通功能 |
| 小程序包缺失 | 开发者工具构建 npm 后执行两个 sync 脚本，再编译；不要提交生成目录 |
| WXML 编译错误 | 查看具体文件与行号；表达式应采用 WXML 支持语法，不能把 JS 运算符错误编码成实体 |
| 请求域名被拦截 | 真机核对微信合法 request/upload/download 域名；开发工具临时设置不能代替正式域名配置 |
| 后台 typegen/build 缺 API origin | 设置 XIANGWAN_ADMIN_API_ORIGIN 后再执行；生产 rewrite 使用构建时值 |
| 后台登录循环或页面无法载入 | 查 API origin、OIDC issuer/client/redirect、同源 auth callback 代理、Cookie/SameSite 与 CSRF；只查看脱敏日志 |
| 新库首页暂不可用 | 首次管理员 seed 与 OIDC 登录后，保存并发布首页配置；ready 成功不代表配置存在 |
| 头像偶发丢失 | 查上传、审核 worker、媒体持久卷、公开 URL 与默认头像回退整条链；不关闭生产内容审核绕过失败 |
| 支付一直确认中 | 查原订单/请求、微信接口真实返回与服务器持久状态，核对 AppID/商户/密钥、公钥 ID、通知回调和关闭 worker；不直接改表为成功、不重复下单 |
| 退款列表为空 | 查取消报名资格、退款 case 是否实际创建、状态筛选与角色；手工退款登记不能冒充微信已退款回执 |
| MCP 401/403 | 从 private/mcp.json 加载配置并核对令牌团队、过期与能力；不要把令牌输出到提示词；需要轮换时由服务器所有者处理 |
| CI 未运行/额度阻断 | 记录精确 head 与未执行原因；本地验证可提供另一种证据，不能称远程 CI 通过 |
| 公开改动没有回到平台/现网 | 分别查公开 merge、私有集成 PR、构建镜像和 deployment；当前没有自动完成这四步 |

独立新环境需要 PostgreSQL 16、服务端私有环境值、自己的 OIDC 和微信身份。完整启动顺序见 deploy/xiangwan/README.md。接收者管理现有服务器时应核对其当前配置，避免把文档示例当真实值覆盖。

参考：[Coolify API 权限](https://coolify.io/docs/api/permissions)、[MCP 安全与客户端配置](https://coolify.io/docs/mcp/security)、[Gitleaks](https://github.com/gitleaks/gitleaks)、[GitHub secret scanning](https://docs.github.com/en/code-security/how-tos/secure-your-secrets/detect-secret-leaks/enable-secret-scanning)。
