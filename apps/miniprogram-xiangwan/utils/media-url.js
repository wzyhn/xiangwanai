"use strict";

function normalizeText(value) {
  return String(value || "").trim();
}

// 与 features/past-activities/model.js 的 joinPublicMediaUrl 同一模式:
// 先校验 http(s) base,再把相对路径限制在公开媒体前缀内,其余一律拒绝。
const PUBLIC_MEDIA_PATH_PATTERN =
  /^\/api\/v1\/xiangwan\/(?:(?:covers|avatars|brand-hero)\/[0-9a-zA-Z][0-9a-zA-Z._-]*|avatars\/legacy\/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|media\/[0-9a-f/-]+)$/;
const API_BASE_URL_PATTERN = /^https?:\/\/[^\s?#]+(?::\d{1,5})?(?:\/[^\s?#]*)?$/;
const ABSOLUTE_HTTPS_URL_PATTERN = /^https:\/\/[^\s?#]+(?::\d{1,5})?(?:\/[^\s?#]*)?$/;

// options.allowAnyHttpsAbsolute:头像等场景放行任意 https 绝对地址
// (防御微信 CDN 旧头像),原样返回且不依赖 base 可用;默认模式仍只认
// 与 base 同源且路径落在白名单前缀内的绝对地址。
function joinXiangwanMediaUrl(apiBaseUrl, mediaPath, options = {}) {
  const path = normalizeText(mediaPath);
  if (options && options.allowAnyHttpsAbsolute === true && ABSOLUTE_HTTPS_URL_PATTERN.test(path)) {
    return path;
  }
  const base = normalizeText(apiBaseUrl).replace(/\/+$/, "");
  if (!API_BASE_URL_PATTERN.test(base)) return "";
  let rel = path;
  // 后端封面以 https 绝对 URL 入库(admin web 拼 API_ORIGIN):仅放行与 base 同源、
  // 且路径仍落在白名单前缀内的绝对地址,其余跨源绝对 URL 一律拒绝。
  if (/^https:\/\//.test(path)) {
    if (!path.startsWith(`${base}/`)) return "";
    rel = path.slice(base.length);
  }
  if (!PUBLIC_MEDIA_PATH_PATTERN.test(rel)) return "";
  return `${base}${rel}`;
}

module.exports = { joinXiangwanMediaUrl };
