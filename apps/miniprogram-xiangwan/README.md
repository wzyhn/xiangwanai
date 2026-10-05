# 消费者小程序

活动首页、详情、报名与问卷、微信登录后资料填写、订单与支付、取消与退款状态、往期照片/资料/视频回顾及个人页。

先在仓库根运行 `pnpm install --frozen-lockfile`，再在微信开发者工具导入本目录并构建 npm。`pnpm --filter miniprogram-xiangwan test` 运行完整工程回归。API 与 AppID 来自 `config/release.json` / `release.js`；AppSecret 不进入小程序。

数据和资金结果以服务端持久状态为准。联系人与问卷不写浏览器/微信持久化草稿或日志。正式上传继续受隐私指引、AppID 和真机验收门禁约束。
