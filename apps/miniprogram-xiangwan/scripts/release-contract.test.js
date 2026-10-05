"use strict";

const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");
const assert = require("node:assert/strict");
const { analyzeReleaseReadiness } = require("./release-contract");

const APP_ROOT = path.join(__dirname, "..");

test("shipped runtime release copy stays field-for-field equal to release.json", () => {
  // 微信运行时不能 require JSON,release.js 是手工维护的副本;发布检查只读
  // release.json。两份漂移会让 gate 通过而上传的运行时仍指向旧值
  // (codex review 2026-09-19),所以逐字段锁死。
  const source = JSON.parse(fs.readFileSync(path.join(APP_ROOT, "config", "release.json"), "utf8"));
  const shipped = require("../config/release");
  assert.deepEqual(shipped, source);
});

test("runtime development origin derives from the release config, not a third literal", () => {
  const { DEVELOPMENT_API_BASE_URL, releaseConfig } = require("../utils/runtime-config");
  assert.equal(DEVELOPMENT_API_BASE_URL, releaseConfig.productionApiBaseUrl);
  assert.match(DEVELOPMENT_API_BASE_URL, /^https:\/\//);
});

test("verified Xiangwan deployment keeps independent WeChat and device release gates", () => {
  const development = analyzeReleaseReadiness({ target: "develop" });
  const release = analyzeReleaseReadiness({ target: "release" });
  assert.equal(development.ready, true);
  assert.equal(release.ready, false);
  // 2026-10-04:用户批准新 API 地址,既有隐私政策继续有效;
  // 新域名 DNS/HTTPS、实际 Runtime 与真实后台登录已验收。
  // 客户 AppID 归属确认/微信指引/真机验收也保持原有门禁。
  assert.ok(!release.issues.includes("production_api_base_pending"));
  assert.ok(!release.issues.includes("backendDeploymentStatus_pending"));
  assert.ok(!release.issues.includes("privacyPolicyStatus_pending"));
  assert.ok(release.issues.includes("production_app_id_pending"));
  assert.ok(release.issues.includes("realDeviceAcceptanceStatus_pending"));
});

test("release readiness accepts only aligned approved standalone assets", () => {
  const config = {
    productCode: "wq-xiangwan",
    appId: "wx1234567890abcdef",
    appIdStatus: "approved",
    productionApiBaseUrl: "https://api.xiangwan.cn",
    productionApiBaseUrlStatus: "approved",
    privacyPolicyStatus: "approved",
    wechatPrivacyGuideStatus: "approved",
    backendDeploymentStatus: "approved",
    realDeviceAcceptanceStatus: "approved",
  };
  const result = analyzeReleaseReadiness({
    target: "release",
    release: config,
    project: { appid: config.appId },
    registry: { productCode: config.productCode, appId: config.appId },
  });
  assert.deepEqual(result, { target: "release", ready: true, issues: [] });
});

test("release readiness rejects the shared platform API", () => {
  const config = {
    productCode: "wq-xiangwan",
    appId: "wx1234567890abcdef",
    appIdStatus: "approved",
    productionApiBaseUrl: "https://api.wequer.com",
    productionApiBaseUrlStatus: "approved",
    privacyPolicyStatus: "approved",
    wechatPrivacyGuideStatus: "approved",
    backendDeploymentStatus: "approved",
    realDeviceAcceptanceStatus: "approved",
  };
  const result = analyzeReleaseReadiness({
    release: config,
    project: { appid: config.appId },
    registry: { productCode: config.productCode, appId: config.appId },
  });
  assert.equal(result.ready, false);
  assert.ok(result.issues.includes("shared_platform_api_forbidden"));
});

test("release readiness rejects malformed identities and non-public hosts", () => {
  const config = {
    productCode: "wq-xiangwan",
    appId: "not-a-wechat-app-id",
    appIdStatus: "approved",
    productionApiBaseUrl: "https://127.0.0.1:8443",
    productionApiBaseUrlStatus: "approved",
    privacyPolicyStatus: "approved",
    wechatPrivacyGuideStatus: "approved",
    backendDeploymentStatus: "approved",
    realDeviceAcceptanceStatus: "approved",
  };
  const result = analyzeReleaseReadiness({
    release: config,
    project: { appid: config.appId },
    registry: { productCode: config.productCode, appId: config.appId },
  });
  assert.equal(result.ready, false);
  assert.ok(result.issues.includes("production_app_id_invalid"));
  assert.ok(result.issues.includes("production_api_host_not_public"));
});
