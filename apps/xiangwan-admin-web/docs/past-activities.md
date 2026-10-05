# 往期活动后台

已发布资料使用 `/past-activities/:instanceId/resources` 回填并维护回顾标题、描述、视频号原生身份、录音梳理和活动资料。两类资料各有前台展示开关，关闭保留内容；新版本完成原写链与替换，原照片隐藏和顺序不会复活。往期列表、活动详情均有编辑入口。版本冲突需要刷新，未知保存结果原样重试；此编辑器不处理平台 File 原始字节。

`/past-activities` 按活动系列列出所有 `completed` 和 `archived` 期次。每个期次仍然使用自己的 `instance_id`，通过“维护与查看”进入既有活动详情页维护封面和资料描述（`detail_blocks`）。这些已发布的文字/图片块会在小程序活动回顾的 `content_blocks` 中安全投影；草稿、未发布、取消或未来完成的 Instance 不会进入匿名回顾。因此同一系列的不同期次不会被合并成一个可编辑对象。

公开视频回顾沿用 Xiangwan resource 的发布链路：内容需要先登记为 resource relation，经审核观察、快照和 publication 完成后，公开 review reader 才会投影完整的视频号原生身份、已发布文件或通过 `ExternalDomainPolicy` 白名单的兼容链接。活动详情页的“公开回顾资源”计数直接来自同一个 reader 的文档结果；仅有 publication 行、但正文被治理或资源链路过滤的内容不会被标为可查看。已发布的链接/文字回顾即使没有图片，也会让小程序显示“往期精彩”入口，图片列表为空时进入完整回顾页。整期资料使用期次入口，只有场次资料时后台会显示带 `session_id` 的精确小程序入口，避免打开空的整期回顾。

当前后台页提供资料描述维护，并在活动详情的“公开回顾资源”区域提供“新增本期活动回顾”命令。命令支持标题、可选描述、最多 30 张照片、可选视频号原生 `finder_user_name`（`sph` 开头）和字符串 `feed_id`，或兼容的白名单 HTTPS 视频链接，以及录音梳理/活动资料 typed 链接；照片可以在提交前逐行取消、删除和调整顺序。服务端通过 Content standalonepg seam 写入私有 review Content，再完成 relation、snapshot、manual-review observation 和 publication。原始文件选择器已经落代码，但构建开关 `NEXT_PUBLIC_XIANGWAN_PRIVATE_MEDIA_UPLOAD` 默认关闭，正式 Runtime 也不注册对应路由；不能把代码存在误认为运营可用。

已发布的本期照片可从 `/past-activities/:instanceId/photos` 继续整理，选择整期或场次回顾资料。后台读取含隐藏照片的原始 image Block 列表，保存只提交可见照片 Block ID 的新顺序；显式空数组表示隐藏全部照片。迁移 797 为每份 relation 追加版本化 curation 事实；原 Content、审核观察和 publication 不被覆盖。公开回顾正文、上一期图片预览及已发布文件的读取授权均按最新 curation 过滤隐藏照片。读写均在同一租户活动运营 Grant 下审计；写入使用 expected version 和操作键，网络未知结果复用原键。

## 资源写入的固定边界

后台命令调用 Xiangwan 的受控 review writer：先由 Content standalonepg seam
创建私有 `review` 内容，再按 `relation → snapshot → moderation observation →
publication` 顺序提交，并以同一个 operation key 记录回执。当前视频文件已有
Storage standalonepg 的私有字节暂存、确认、审核下载 seam；受控选择器会在浏览器内计算 SHA-256，上传并确认后要求管理员下载核对原始内容，再提交 `file_id`、kind、摘要及人工审核确认。服务端只在完整字节核验后记录成功打开审计，可选的资源 writer 要求同一管理员与身份链接在确认后有这份证据，再重验精确上传意图和字节摘要，并在同一发布事务内 Pin File。未知发布结果在浏览器会保存原始请求体和操作键，重试原样发送，避免刷新后表单变化造成同键异内容。生产构造器仍拒绝 File；视频号原生身份保留为字符串，经同一审核和发布链路后，由小程序显式点击调用 `wx.openChannelsActivity`。原生身份不解析分享链接；兼容 HTTPS 链接继续受 `ExternalDomainPolicy` 约束。审核 provider 返回 `unknown`、`review` 或
`rejected` 时保持不可公开。

独立 `cmd/xiangwan` 装配 PostgreSQL 读路径、确认对象读取器和上述窄写适配器。因此
不能把通用 `/contents`、原始 URL 或直接 SQL 当作旁路实现；独立媒体安全判定、客户环境中的审核责任和生产装配仍需收口。

命令、幂等语义、媒体/外链约束和验证证据见
[资源写入契约](resource-writer-contract.md)。
