"use strict";

const fs = require("node:fs");
const path = require("node:path");
const { getMiniProgramConfig } = require("mp-product-registry");
const {
  isPlaceholderAppId,
  isWechatMiniProgramAppId,
  parseHttpsApiBaseUrl,
} = require("../utils/runtime-config");

const APP_ROOT = path.resolve(__dirname, "..");
const REQUIRED_APPROVALS = Object.freeze([
  "privacyPolicyStatus",
  "wechatPrivacyGuideStatus",
  "backendDeploymentStatus",
  "realDeviceAcceptanceStatus",
]);

function readJSON(file) {
  return JSON.parse(fs.readFileSync(file, "utf8"));
}

function isPublicProductionHostname(value) {
  const hostname = String(value || "")
    .trim()
    .toLowerCase();
  if (!hostname.includes(".") || /^\d+(?:\.\d+){3}$/.test(hostname)) return false;
  return ![".example", ".invalid", ".localhost", ".local", ".test"].some((suffix) =>
    hostname.endsWith(suffix),
  );
}

function analyzeReleaseReadiness(options = {}) {
  const release = options.release || readJSON(path.join(APP_ROOT, "config", "release.json"));
  const project = options.project || readJSON(path.join(APP_ROOT, "project.config.json"));
  const registry = options.registry || getMiniProgramConfig("wq-xiangwan") || {};
  const target = String(options.target || "release")
    .trim()
    .toLowerCase();
  const issues = [];

  if (release.productCode !== "wq-xiangwan" || registry.productCode !== "wq-xiangwan") {
    issues.push("product_code_mismatch");
  }
  if (!release.appId || release.appId !== project.appid || release.appId !== registry.appId) {
    issues.push("app_id_mismatch");
  }
  if (release.appIdStatus !== "approved" || isPlaceholderAppId(release.appId)) {
    issues.push("production_app_id_pending");
  } else if (!isWechatMiniProgramAppId(release.appId)) {
    issues.push("production_app_id_invalid");
  }
  const parsedProductionApi = parseHttpsApiBaseUrl(release.productionApiBaseUrl);
  if (release.productionApiBaseUrlStatus !== "approved" || !parsedProductionApi) {
    issues.push("production_api_base_pending");
  } else if (parsedProductionApi.hostname === "api.wequer.com") {
    issues.push("shared_platform_api_forbidden");
  } else if (!isPublicProductionHostname(parsedProductionApi.hostname)) {
    issues.push("production_api_host_not_public");
  }
  REQUIRED_APPROVALS.forEach((field) => {
    if (release[field] !== "approved") issues.push(`${field}_pending`);
  });

  const structuralIssues = issues.filter((issue) =>
    [
      "product_code_mismatch",
      "app_id_mismatch",
      "production_app_id_invalid",
      "shared_platform_api_forbidden",
      "production_api_host_not_public",
    ].includes(issue),
  );
  return {
    target,
    ready: target === "develop" ? structuralIssues.length === 0 : issues.length === 0,
    issues: [...new Set(issues)],
  };
}

module.exports = {
  APP_ROOT,
  REQUIRED_APPROVALS,
  analyzeReleaseReadiness,
  isPublicProductionHostname,
};
