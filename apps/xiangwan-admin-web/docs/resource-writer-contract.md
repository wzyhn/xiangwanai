# 往期回顾资源写入契约

## 已发布资料修改与原生视频号（2026-10-02）

`GET /instances/:instance_id/review-resources` 返回当前发布版本的编辑字段，包括隐藏的录音梳理、活动资料、原生视频号身份和原始照片地址。读取重新校验当前管理员身份、代际及活动权限，并记录内容无关审计。

原 POST 可携带 `replaces_relation_id` 和 `expected_photo_curation_version`。服务端通过原有 Content 写链创建新私有正文、快照、审核观察和 publication，再在同事务追加迁移 825 的替换事实。目标、排序和原照片必须与旧关系一致；照片隐藏和顺序按 Block 身份继承。所有公开正文、往期预览、场次选择、资源计数及旧文件授权排除已替换关系。旧事实不可变，旧版本并发修改返回冲突，未知响应原样重试。

配置过的 recording/materials 使用 `enabled:false` 时保留名称、副标题和链接，公开投影不返回该 Block。未配置的可选项不因默认名称误标必填。平台 File 原始字节回顾继续由原有文件/照片命令管理，此 HTTPS 编辑器不会移除文件。

原生视频使用 `video_channel:{finder_user_name,feed_id}`；两个 ID 来自视频号助手，进入相同受审核 Content snapshot，与旧 `video_url` 互斥。小程序重新读取精确 Block 的发布事实，再由用户点击调用 `wx.openChannelsActivity`；不解析分享链接，不把长 feedId 转为数值。

本地 PostgreSQL 16 验证多期隔离、隐藏卡回填与公开过滤、照片继承、重复操作、并发编辑及权限撤销。浏览器验证字段回填、独立开关和响应丢失后的刷新重试。线上部署和微信真机证据另行记录。

后台已提供 `POST /api/v1/xiangwan/admin/instances/:instance_id/review-resources`。活动完成或归档后，管理员可以填写回顾标题、可选描述、照片集、可选视频号 HTTPS 链接，并按需添加录音梳理/活动资料的 Feishu HTTPS 链接；至少提供一项视频、已选照片或 typed 链接，描述不能单独发布。服务端会在同一 PostgreSQL 事务中创建私有 `review` Content、绑定精确期次、记录人工审核观察并发布 immutable resource。外链必须命中部署白名单；视频或资料原始字节上传仍不在本次表单范围内。

## 当前实现

- `internal/domains/xiangwan/api/admin_catalog_handler.go` 负责管理员主体、JSON 请求、幂等键、实例/场次参数和错误映射。
- `internal/domains/xiangwan/resource/postgres/review_writer.go` 是 Xiangwan 组合根的唯一写适配器；它使用 `content/standalonepg.Writer` 写 Content/Block，再调用 `Repository.CreateDraft`、`RecordModeration` 和 `Publish`。事务开始后先重新锁定当前 runtime generation、准确的管理员 identity link、Principal 和 activity Grant，同一 operation 还由 PostgreSQL advisory xact lock 串行化。视频号链接是可选资源；缺少视频时，照片或 typed 录音/资料链接仍会生成合法的 review Content。
- `internal/capabilities/content/standalonepg` 只接受调用方持有的 `*sql.Tx`，固定 `product_code=wq-xiangwan`、`type=review`、`visibility=private`、`owner_type=user`，不引入 Redis、eventbus、middleware 或 Content HTTP 服务。
- `internal/capabilities/storage/standalonepg` 提供 Redis-free 文件元数据 Writer 和受限本地字节暂存器；正式表单仍使用白名单 HTTPS 外链。可选文件选择器由 `NEXT_PUBLIC_XIANGWAN_PRIVATE_MEDIA_UPLOAD=enabled` 显式启用，上传确认后要求管理员私有打开并人工复核；可选 writer 接收精确 File ID、kind 和摘要，在同一发布事务 Pin File。正式 Runtime 不装配上传服务、不注册对应路由。
- 公开读取仍走既有 relation → snapshot → approved observation → publication 链和 `ReadPublicReview`，外链再次经过同一 `ExternalDomainPolicy`；没有配置视频号域名时，写入和公开读取都 fail closed。

## 命令与约束

```text
POST /api/v1/xiangwan/admin/instances/:instance_id/review-resources
Idempotency-Key: canonical UUIDv4

{
  "expected_target_version": 7,
  "session_id": "optional exact Session UUID",
  "title": "本期视频回顾",
  "description": "optional text",
  "video_url": "https://allowlisted.example/video/1",
  "photos": ["https://allowlisted.example/photo-1.webp"],
  "recording": {
    "enabled": true,
    "title": "第 12 期讨论纪要",
    "subtitle": "核心观点",
    "url": "https://feishu.cn/..."
  },
  "materials": {
    "enabled": true,
    "title": "现场分享资料合集",
    "subtitle": "活动资料整理",
    "url": "https://feishu.cn/..."
  },
  "sort_order": 0
}
```

1. 管理员写请求必须通过现有 admin session、租户/grant、请求体和 write-request 校验；服务端从主体上下文取得 operator，不信任客户端的 actor、tenant 或审核字段。
2. `title`、描述、照片、资料链接和规范化视频号链接形成唯一 Content body。`video_url` 可以省略或为空，但至少要有一项视频、照片或 typed 链接；所有 URL 只能是 HTTPS、无用户信息、端口为空或 443，并命中部署的 exact 或显式 wildcard host allowlist；照片最多 30 个，录音梳理和活动资料各最多一个。
3. relation 绑定精确 `Instance`，若提供 `session_id` 则绑定同一期的精确 `Session`；`expected_target_version`、排序、访问策略和 Content revision 都进入不可变事实。写入前会锁定并检查 completed/archived 期次，场次资源则只接受 published/completed/archived 期次下的 published/ended/archived 场次。迁移 765 负责 snapshot/Block immutability guard。
4. 当前实现使用有理由的 `manual_review` observation，记录 `provider=xiangwan.admin`、operator、policy version、subject digest 和 payload digest；照片与两类链接和视频号链接都包含在同一个不可变 Content snapshot 中，只有 `approved` observation 才能进入 publication。
5. Admin 照片编辑器在提交前提供逐行启用、删除和上移/下移；提交的 `photos` 数组顺序就是 immutable Block 顺序。发布后关系、Content、Block、snapshot 和 publication 均不可变；已发布照片使用迁移 797 的独立追加式 curation 版本隐藏、恢复或重排，公开正文、预览和文件读取授权按最新版本过滤，不直接 UPDATE 已发布事实。
6. 返回值只包含 relation/publication/content 身份、目标身份、标题和发布时间，不返回 storage key、签名 URL、原始审核内容或消费者隐私。

## 幂等与失败语义

- 同一 `(tenant_id, actor_id, operation_id)` 且命令 body 完全一致的重试返回原 publication receipt。
- 复用幂等键但改变期次、场次、版本、排序、标题、描述或视频链接会返回冲突，不能覆盖已经绑定的 Content 或 relation。
- 外链白名单、目标版本、层级外键、snapshot、审核或 publication 任一事实不满足时，事务回滚或保持不可公开；前端显示明确错误，不把未知结果当成成功。
- 页面在 `sessionStorage` 保存 operation key。网络结果未知时重试会继续使用同一个 key；确定性 4xx/冲突会清理该恢复记录。

## 可复验证据

- `go test ./internal/domains/xiangwan/resource/postgres ./internal/domains/xiangwan/api`：覆盖命令校验、Block 形状、幂等 body/target 比对、管理员参数传递和外链错误映射。
- `go run ./tools/content-access-lint --enforce`：standalonepg 的 Content 表访问有注册 seam，未引入 Redis/eventbus/middleware。
- `go run ./tools/import-lint --enforce`：唯一新增跨模块边界是登记过的 `xiangwan/content` standalonepg seam。
- 外链 host 必须在部署配置 `XIANGWAN_EXTERNAL_DOMAINS` 中登记，并配套 `XIANGWAN_EXTERNAL_DOMAINS_POLICY_VERSION`；仅有照片/资料时不要求视频号域名，实际提交的每个外链仍必须经过白名单。

私有意图、真实字节暂存、MIME/摘要确认、审核下载、永久 pin、Content/Relation/Publication 原子绑定和默认关闭的前端文件选择器已落代码；服务端要求确认后同一管理员和身份链接的成功私有打开审计，不能用客户端布尔值替代。File 名由服务端按 File ID 与允许的 MIME 生成。独立清理 worker 使用客户库 File 生命周期、租约、代际与重试约束；超过意图截止的上传被取消。真实 PostgreSQL 16 合成事务验证了暂存到发布、拒绝跳过打开证据、精确字节读取和重复请求。客户媒体安全政策/责任、客户库及对象存储大文件链路与管理员浏览器验收仍缺。正式 Runtime 不装配上传服务，库存与 OpenAPI 均不发布暂存接口，不能宣称运营可用。

上传在字节 IO 前后都通过 Storage seam 重验当前 File 生命周期；即使旧意图仍有效，已删除/失效文件也不能重新暂存。实库清理回归验证发布后 pinned File 不被认领、锁定行跳过、失败退避、崩溃租约重领、旧租约不能完成、物理删除可重试，以及清理完成与在途上传交错时不复活对象。

期次详情的「公开回顾资源」面板支持选择精确的已结束/已归档场次提交 session_id。该入口使用同一 writer、目标版本和幂等恢复键，资源计数与公开入口均从 review-status 投影刷新。未知发布结果保存原请求体与操作键，刷新后原样发送；客户端不会以新表单覆盖旧操作。正式照片和资料链接继续接受白名单 HTTPS 链接并进入同一 immutable snapshot，未经装配的文件上传继续 fail closed。
