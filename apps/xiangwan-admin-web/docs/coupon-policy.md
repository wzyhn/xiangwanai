# 优惠券发放策略签收与登记

`/coupon-corrections` 的策略登记区只接收客户已签名的版本，不允许管理员在页面直接填写面额或适用范围。没有完整管理员 OIDC 与 `XIANGWAN_COUPON_GRANT_POLICY_PUBLIC_KEY` 时，服务端不注册登记或人工补发路由。登记成功本身不发券；自动发券由独立 worker 执行。

客户在自己的环境生成 Ed25519 密钥，私钥始终离线保存。部署方仅把 32 字节公钥的标准 Base64 写入 API 的私有环境变量。签收文档是 UTF-8 JSON，恰好包含以下字段：`tenant_id`、`policy_version`、`enabled`、`face_value_cents`、`validity_seconds`、`scope_type`、`scope_activity_type`、`scope_series_id`、`minimum_order_cents`、`evidence_ref`、`approved_at`、`effective_at`。金额单位为分；有效期单位为秒；时间必须是 UTC `Z` 且精确到秒。`evidence_ref` 是客户审批档案的稳定 ASCII 引用，不能是敏感原文。停用版本将面额、有效期和全部适用范围字段设为 `null`。

客户先按上述字段准备草稿，再运行 `npm run coupon-policy:canonicalize -- <draft.json> <new-canonical.json>` 生成不覆盖已有文件的规范字节。客户应直接对生成文件的原始字节签名，分别交付该文件与标准 Base64 签名。管理员将文件内容和签名原样粘贴到后台。服务端会重新核对签名、唯一规范编码、客户租户、未来生效时间、当前代际和实时 `super_admin` Grant；同一版本同一事实重试返回原回执，不覆盖历史。签名值、私钥和客户实际面额不能写入仓库。

迁移 819 只提供不可变策略表，不预置任何发券值。客户产品、财务和法务的签收值及有效日期必须由客户自己决定；未签收时保留发券关闭状态。

人工补发使用 `POST /api/v1/xiangwan/admin/coupon-grants`。后台只提交一张已有券的 ID、操作 UUID、原因和工单引用；服务端从券反查所属用户，不能从浏览器指定用户、面额、期限或范围。一个来源券和操作 UUID 组成不可重复的业务键。在同一可串行化事务内重查当前代际、精确管理员身份和超级管理员 Grant、已有发券历史、当前可用余额为零、生效的客户签名策略，并把五张券和管理审计一起提交。结果未知时后台在当前浏览器会话保存原请求，刷新后只能原样重试；确定的 4xx 拒绝会清除待重试状态。这个操作不会将 `correction_required` 异常标记视为已完成财务或权益更正。
