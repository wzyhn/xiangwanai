"use strict";

// runtime-env — wx 运行环境探测 + 默认 api base 解析
//
// 历史:apps/miniprogram-{community,study,schedule}/utils/runtime-env.js 三个
// byte-identical copy。Phase B5-followup F5 抽到 mp-init。
//
// 三 app 的 utils/runtime-env.js 改为 1 行 reexport。新 app 直接从
// `require("mp-init")` 取。

const LOCAL_API_BASE_URL = "http://127.0.0.1:8080";
const PRODUCTION_API_BASE_URL = "https://api.wequer.com";
const DEFAULT_API_BASE_URL = PRODUCTION_API_BASE_URL;

function normalizeText(value, fallback = "") {
  const normalized = String(value || "").trim();
  return normalized || String(fallback || "").trim();
}

function readMiniProgramEnvVersion() {
  try {
    if (typeof wx === "undefined" || !wx || typeof wx.getAccountInfoSync !== "function") {
      return "develop";
    }
    const info = wx.getAccountInfoSync();
    return normalizeText(info && info.miniProgram && info.miniProgram.envVersion, "develop");
  } catch (_error) {
    return "develop";
  }
}

function isDevelopmentToolsEnabled(envVersion = readMiniProgramEnvVersion()) {
  return normalizeText(envVersion, "develop") === "develop";
}

// ---- dev local profile(2026-07-09 平台优化 I1-FE/PR-11)----
// 痛点:开发者工具里默认也打生产 api.wequer.com(写坏数据零防护,方案病灶 3)。
// 机制:仅 envVersion===develop 时读 storage 开关;体验版/正式版**无条件生产**,
// 开关在发布包里是死代码路径——与「trial/release 强制生产」既有策略一致。
const DEV_API_BASE_STORAGE_KEY = "weconq_dev_api_base";

function readStorageSync(key) {
  try {
    if (typeof wx === "undefined" || !wx || typeof wx.getStorageSync !== "function") return "";
    return normalizeText(wx.getStorageSync(key));
  } catch (_error) {
    return "";
  }
}

function getDevApiBaseOverride() {
  if (!isDevelopmentToolsEnabled()) return "";
  const v = readStorageSync(DEV_API_BASE_STORAGE_KEY);
  return /^https?:\/\//i.test(v) ? v : "";
}

function setDevApiBaseOverride(url) {
  const v = normalizeText(url);
  if (!/^https?:\/\//i.test(v)) throw new Error("dev api base 必须是 http(s) URL");
  wx.setStorageSync(DEV_API_BASE_STORAGE_KEY, v);
  return v;
}

function clearDevApiBaseOverride() {
  try {
    wx.removeStorageSync(DEV_API_BASE_STORAGE_KEY);
  } catch (_error) {
    /* 幂等 */
  }
}

// 便捷:一键切本地(LOCAL_API_BASE_URL=127.0.0.1:8080,配 make seed-dev 的本地栈)
function enableLocalApiBase() {
  return setDevApiBaseOverride(LOCAL_API_BASE_URL);
}

function resolveDefaultApiBaseUrl() {
  const override = getDevApiBaseOverride();
  return override || DEFAULT_API_BASE_URL;
}

module.exports = {
  DEFAULT_API_BASE_URL,
  LOCAL_API_BASE_URL,
  PRODUCTION_API_BASE_URL,
  DEV_API_BASE_STORAGE_KEY,
  clearDevApiBaseOverride,
  enableLocalApiBase,
  getDevApiBaseOverride,
  isDevelopmentToolsEnabled,
  readMiniProgramEnvVersion,
  resolveDefaultApiBaseUrl,
  setDevApiBaseOverride,
};
