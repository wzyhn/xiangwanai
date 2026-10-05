# 享玩独立部署

本仓库独立构建，不依赖 weconq checkout。这里的基础 schema 与迁移器面向**新的独立 PostgreSQL 16 数据库**；不能直接替换原平台迁移器或对现有平台库执行。

## 配置与启动

复制 `.env.example` 为本目录的 `.env`，填写数据库口令、各运行身份的 DSN、tenant/generation UUID、JWT 与签到凭据密钥、微信 AppID/AppSecret、运营后台 OIDC 配置。所有秘密均在私有环境设置。

`DATABASE_DSN` 是 migration/bootstrap 的管理身份；API 与各 worker 用各自 DSN。首次空库可以先用测试身份验证闭包；正式交付应在自己的库创建受限运行角色，并按产品专属迁移中的授权语句授权。不要把已有生产库管理员口令发给协作者。

本地源码镜像名称可设置为 `XIANGWAN_IMAGE=xiangwan:local`、`XIANGWAN_MIGRATE_IMAGE=xiangwan-migrate:local`、`XIANGWAN_ADMIN_IMAGE=xiangwan-admin:local`。

```sh
docker compose --env-file deploy/xiangwan/.env \
  -f deploy/xiangwan/compose.yml -f deploy/xiangwan/admin.compose.yml up -d --build
```

Compose 自动按 PostgreSQL → 登记迁移 → bootstrap → API → Admin 顺序启动。API 默认绑定本机 8082，后台默认 3002；自行配置 HTTPS 反向代理和相应域名。`deploy/sql/manifest.json` 登记每份 SQL 的 SHA256；已有迁移的内容与名称不可修改，新增迁移前滚并更新清单。

首次后台使用 OIDC，独立身份提供方需要正确 issuer、client 与回调 URL。后台 `/api/v1/xiangwan/admin/auth/callback` 必须通过同源反向代理转发至 API；Next 的 rewrite 已包含该路径。将登录账号的 issuer/subject 填入 seed 变量后执行：

```sh
docker compose --env-file deploy/xiangwan/.env -f deploy/xiangwan/compose.yml \
  --profile admin-seed run --rm admin-identity-seed
```

可选 worker 按需要启用 `profile-moderation`、`media-cleanup`、`coupon-reconciliation`、`payment-close` profile，并配置各身份。头像审核涉及微信接口，正式环境应启用相应 worker。

## 微信与支付

小程序按自己的 AppID 配置合法 API/upload/download 域名及隐私指引。支付默认关闭；要开启需准备 AppID 与商户绑定、证书序列号、商户私钥、微信支付公钥 ID/公钥、APIv3 密钥、HTTPS 通知 URL，并给 API/worker 只读挂载自己的密钥文件。当前 compose 的示例注释说明额外证书挂载方式，支付凭据挂载也需要在自己的 override 中配置。

已付金额、支付确认、退款结果以对应服务端和微信回执为准。构建或仓库同步不会授权或执行生产付款、退款、数据导入。

## 验收与恢复

先运行源码检查和空库迁移/重复执行，再检查 `/ready`、公开首页、OIDC 登录、资料、报名和自己的测试环境支付。备份数据库与媒体卷，固定 Git commit 和镜像 digest；升级失败回到上一制品，schema 通过新增前滚修复。不要通过改写已应用 SQL 或删除迁移 ledger 回退。

外部依赖未配置时，仅能说明源码与数据库闭包通过，不能声称微信、OIDC、支付或生产部署已经可用。
