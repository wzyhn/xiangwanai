"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const {
  DEVELOPMENT_API_BASE_URL,
  isHttpsApiBaseUrl,
  isPlaceholderAppId,
  isWechatMiniProgramAppId,
  releaseConfig,
  resolveDefaultApiBaseUrl,
} = require("./runtime-config");

test("mini-program runtime release config stays aligned with the release manifest", () => {
  const manifest = JSON.parse(
    fs.readFileSync(path.join(__dirname, "..", "config", "release.json"), "utf8"),
  );
  assert.deepEqual(releaseConfig, manifest);
});
const { SESSION_STORAGE_KEY_PREFIX, createSessionStoreOptions } = require("./session-options");

test("Xiangwan resolves every environment version to the production origin", () => {
  assert.equal(resolveDefaultApiBaseUrl("develop"), DEVELOPMENT_API_BASE_URL);
  assert.equal(DEVELOPMENT_API_BASE_URL, "https://api.weconq.cn");
  // 2026-09-19(owner 决策):开发期不起本地后端,所有环境版本直连生产。
  assert.equal(resolveDefaultApiBaseUrl("trial"), "https://api.weconq.cn");
  assert.equal(resolveDefaultApiBaseUrl("release"), "https://api.weconq.cn");
});

test("production URL and placeholder checks fail closed", () => {
  assert.equal(isHttpsApiBaseUrl("https://api.xiangwan.example"), true);
  assert.equal(isHttpsApiBaseUrl("https://api.xiangwan.example/v1"), true);
  assert.equal(isHttpsApiBaseUrl("http://api.xiangwan.example"), false);
  assert.equal(isHttpsApiBaseUrl("https://api.xiangwan.example/"), false);
  assert.equal(isHttpsApiBaseUrl("https://user@api.xiangwan.example"), false);
  assert.equal(isHttpsApiBaseUrl("https://api..xiangwan.example"), false);
  assert.equal(isHttpsApiBaseUrl("https://api.xiangwan.example:0"), false);
  assert.equal(isHttpsApiBaseUrl("https://api.xiangwan.example:1"), true);
  assert.equal(isHttpsApiBaseUrl("https://api.xiangwan.example:65535"), true);
  assert.equal(isHttpsApiBaseUrl("https://api.xiangwan.example:70000"), false);
  assert.equal(isPlaceholderAppId("touristappid"), true);
  assert.equal(isPlaceholderAppId("wx1234567890abcdef"), false);
  assert.equal(isWechatMiniProgramAppId("wx1234567890abcdef"), true);
  assert.equal(isWechatMiniProgramAppId("not-an-app-id"), false);
});

test("all auth storage keys remain inside the unique Xiangwan namespace", () => {
  const options = createSessionStoreOptions(DEVELOPMENT_API_BASE_URL);
  const keys = Object.entries(options)
    .filter(([name]) => name.endsWith("Key"))
    .map(([, value]) => value);
  assert.equal(SESSION_STORAGE_KEY_PREFIX, "xiangwan_session_v1_");
  assert.equal(new Set(keys).size, 8);
  keys.forEach((key) => assert.ok(key.startsWith(SESSION_STORAGE_KEY_PREFIX)));
});
