# mp-product-registry

享玩独立发行仅注册 `wq-xiangwan`。`getMiniProgramConfig` 返回该产品冻结配置，未知产品返回 null；`listMiniProgramConfigs` 只返回享玩。`lintProductCode` 保留公共组件的命名校验接口，校验成功不代表其他产品已在本仓库注册。

AppID 是公开运行身份，AppSecret 始终只在服务端环境配置。修改 AppID 后需同步小程序 release.json/release.js 和 project.config.json，按 release checker 检查。

配置与文件摘要见根 `standalone-manifest.json`。本文件是独立发行适配，不自动回写平台共享注册表。
