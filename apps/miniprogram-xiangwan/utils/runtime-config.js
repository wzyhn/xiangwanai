"use strict";

// 微信小程序运行时不保证 JSON 文件可作为 CommonJS 模块加载；release.json
// 只由 Node 发布检查读取，运行时使用同目录的 JavaScript 副本。
const releaseConfig = require("../config/release");

// 2026-09-19(owner 决策):开发期不再起本地后端,所有环境版本统一直连生产
// origin。取值从 release 配置派生而不是再写一份字符串——否则改 release.json
// 会通过发布检查,而上传的运行时仍指向旧 origin(codex review 2026-09-19)。
// 若日后恢复本地联调,把这里改回 http://127.0.0.1:8082 即可。
const DEVELOPMENT_API_BASE_URL = releaseConfig.productionApiBaseUrl;

function normalizeText(value) {
  return String(value || "").trim();
}

function readMiniProgramAccountInfo() {
  try {
    if (typeof wx === "undefined" || !wx || typeof wx.getAccountInfoSync !== "function") {
      return null;
    }
    const info = wx.getAccountInfoSync();
    return info && info.miniProgram ? info.miniProgram : null;
  } catch (_error) {
    return null;
  }
}

function readMiniProgramEnvVersion(fallback = "develop") {
  const account = readMiniProgramAccountInfo();
  return normalizeText(account && account.envVersion) || normalizeText(fallback) || "develop";
}

function resolveDefaultApiBaseUrl(envVersion = readMiniProgramEnvVersion()) {
  if (normalizeText(envVersion).toLowerCase() === "develop") {
    return DEVELOPMENT_API_BASE_URL;
  }
  if (normalizeText(releaseConfig.productionApiBaseUrlStatus) !== "approved") return "";
  return normalizeText(releaseConfig.productionApiBaseUrl);
}

function resolveMiniProgramAppId(fallback = releaseConfig.appId) {
  const account = readMiniProgramAccountInfo();
  return normalizeText(account && account.appId) || normalizeText(fallback);
}

function isPlaceholderAppId(value) {
  const appId = normalizeText(value).toLowerCase();
  return !appId || appId === "touristappid" || appId === "wx0000000000000000";
}

function isWechatMiniProgramAppId(value) {
  return /^wx[0-9a-f]{16}$/i.test(normalizeText(value));
}

function parseHttpsApiBaseUrl(value) {
  const normalized = normalizeText(value);
  if (!normalized || /[\s?#]/.test(normalized) || normalized.endsWith("/")) {
    return null;
  }

  const match = /^https:\/\/([^/:@]+)(?::(\d{1,5}))?(\/[^?#\s]*)?$/.exec(normalized);
  if (!match) return null;

  const hostname = match[1].toLowerCase();
  const labels = hostname.split(".");
  if (
    labels.some((label) => !label || !/^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/.test(label)) ||
    (match[2] && (Number(match[2]) < 1 || Number(match[2]) > 65535))
  ) {
    return null;
  }

  return { hostname, value: normalized };
}

function isHttpsApiBaseUrl(value) {
  return !!parseHttpsApiBaseUrl(value);
}

module.exports = {
  DEVELOPMENT_API_BASE_URL,
  isHttpsApiBaseUrl,
  isPlaceholderAppId,
  isWechatMiniProgramAppId,
  parseHttpsApiBaseUrl,
  readMiniProgramEnvVersion,
  releaseConfig,
  resolveDefaultApiBaseUrl,
  resolveMiniProgramAppId,
};
