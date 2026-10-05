"use strict";

// 微信小程序运行时不会把 JSON 文件解析为 CommonJS 模块。这个运行时副本
// 必须与 release.json 保持逐字段同步；release.json 仍是 Node 发布检查的真相源。
module.exports = Object.freeze({
  productCode: "wq-xiangwan",
  appId: "wx05ff9791f73fe3bd",
  appIdStatus: "customer-provided",
  productionApiBaseUrl: "https://api.weconq.cn",
  productionApiBaseUrlStatus: "approved",
  privacyPolicyStatus: "approved",
  wechatPrivacyGuideStatus: "pending",
  backendDeploymentStatus: "approved",
  realDeviceAcceptanceStatus: "pending",
});
