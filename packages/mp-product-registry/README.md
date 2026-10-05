# mp-product-registry

> 当前状态：稳定的静态产品注册表。早期 Phase 1B/ADR-005 仅是来源记录，不构成当前迁移指令。

## 1. 一句话职责

跨小程序 product_code 注册表 — 每个 weconq 小程序壳使用自己的 `product_code`(community / wq-schedule / compass / wq-study / wq-chaiquan / ...),让 mp-link-target / mp-share / mp-applink 等跨 app 包知道当前 app 身份。仅实际存在产品注册项的 app 才列入注册表；docs-only 的 record 没有运行时注册项。

## 2. 分类(per `frontend-boundary.md` §2)

- **层**: L1 平台 SDK(后端 capability `applink` 对应)
- **后端模块对应**: `internal/capabilities/applink/`(applink target 注册 + scene token);`internal/capabilities/auth/principal_product_states`(后端 product_code 持久化)

## 3. 安装与同步(miniprogram_npm 模式)

```bash
# 1. 在 packages/mp-product-registry/ 修源(本目录)
# 2. 同步到 apps/*/miniprogram_npm/(LOCKED — 不直接改 miniprogram_npm)
pnpm sync:miniprogram-packages
```

## 4. 主入口

```js
const {
  getMiniProgramConfig,
  listMiniProgramConfigs,
  lintProductCode,
} = require("mp-product-registry");

const PRODUCT_CODE = "wq-store-staff";
lintProductCode(PRODUCT_CODE);
const config = getMiniProgramConfig(PRODUCT_CODE);
```

注册是源码期静态注册：在 `index.js` 增加冻结 config 并写入
`MINI_PROGRAM_REGISTRY`。本包没有运行时 `register()`、`current()`、
`getProductCode()` 或 `getAppId()` API；不要在 app 启动时发明动态注册。

## 5. 依赖

无运行时依赖。当前产品码与 AppID 是冻结静态配置，不读写本地 storage。

## 6. 不依赖

- 业务包(mp-pay / mp-ai / mp-analytics 等)— L1 不依赖 L1+

## 7. 跨 app 复用

| App                     | 用本 package | 备注                                                        |
| ----------------------- | ------------ | ----------------------------------------------------------- |
| miniprogram-schedule    | Yes          | 注册 `wq-schedule`                                          |
| miniprogram-community   | Yes          | 注册 `community`(已实施)                                    |
| miniprogram-compass     | Yes          | 注册 `wq-compass`                                           |
| miniprogram-study       | Yes          | 注册 `wq-study`                                             |
| miniprogram-chaiquan    | Yes          | 注册 `wq-chaiquan`                                          |
| miniprogram-store-staff | Yes          | 注册 `wq-store-staff`；AppID 暂为开发占位                   |
| miniprogram-classops    | Declared     | 使用共享包和接口契约；产品注册项/AppID 仍受 owner gate 约束 |
| miniprogram-xiangwan    | Yes          | 注册 `wq-xiangwan`；真实 AppID 待客户提供                   |
| miniprogram-pindou      | Yes          | 注册 `wq-pindou`；AppID owner-pending，发布门禁阻断         |

## 8. 测试

Pindou (`miniprogram-pindou`) 注册 `wq-pindou`，AppID 保持 `owner-pending`；注册只提供产品身份，不代表发布准入。

```bash
node --test packages/mp-product-registry/index.test.js packages/mp-product-registry/lib/lint.test.js
```

## 9. 当前状态

- **stable**(community/schedule/compass 等多 app 接入；Xiangwan 以显式开发占位注册并由 app 发布门禁阻断)
- 下游 packages(mp-link-target / mp-share / mp-applink)依赖此 registry 拿 productCode 做 scene token / target 解析

## 10. 关联

- `frontend-boundary.md` §2 L1(后端模块对应 applink)
- `packages-registry.md`(共享包当前消费登记；本 README 的静态注册事实需与代码和门禁一起核验)
- 后端 `internal/capabilities/applink/README.md`(若有)
- ADR-006 community product_code cutover(community / 其他 app product_code 全栈贯通的前端起点)
