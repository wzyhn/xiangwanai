# 运营后台

Next.js / React / TypeScript。支持活动与系列、首页配置、报名模板、往期资料、人员角色、报名与订单查询、退款登记、签到与审计。

```sh
pnpm --filter xiangwan-admin-web dev
pnpm --filter xiangwan-admin-web lint
XIANGWAN_ADMIN_API_ORIGIN=http://localhost:8080 pnpm --filter xiangwan-admin-web typecheck
XIANGWAN_ADMIN_API_ORIGIN=https://api.example.com pnpm --filter xiangwan-admin-web build
```

开发服务为 `https://localhost:3002`，API 同源代理目标由本目录 `.env.example` 配置。生产必须显式指定 API origin；登录由 API 的 OIDC + PKCE 和 Secure HttpOnly session 完成，写操作使用同源 CSRF 保护。

退款登记、优惠券与往期资料的运营边界见本目录 `docs/`。本仓库不内置账号或开发登录后门。
