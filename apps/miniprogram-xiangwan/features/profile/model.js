"use strict";

// 享玩个人资料(昵称/头像)的纯函数模型:契约见
// GET/PATCH /api/v1/xiangwan/me/profile* 与 POST /api/v1/xiangwan/me/avatar。

const { joinXiangwanMediaUrl } = require("../../utils/media-url");

const NICKNAME_MAX_LENGTH = 64;
const NICKNAME_RULE_MESSAGE = "昵称需为 1–64 个字符";

function normalizeText(value) {
  return String(value || "").trim();
}

function profilePayload(payload) {
  if (!payload || typeof payload !== "object" || Array.isArray(payload)) return {};
  if (payload.data && typeof payload.data === "object" && !Array.isArray(payload.data)) {
    return payload.data;
  }
  return payload;
}

// 头像相对路径拼接走公共媒体白名单 utils/media-url.js;已是 https 绝对地址
// (如微信 CDN 旧头像)则原样放行,不依赖 base 可用。
function joinAvatarUrl(apiBaseUrl, avatarPath) {
  return joinXiangwanMediaUrl(apiBaseUrl, avatarPath, { allowAnyHttpsAbsolute: true });
}

// 头像上传响应以 avatar_url 为准,兼容后端改用 url 字段。
function extractAvatarUrl(payload) {
  const source = profilePayload(payload);
  return (
    normalizeText(source.avatar_url) || normalizeText(source.avatarUrl) || normalizeText(source.url)
  );
}

function validateNickname(value) {
  const nickname = normalizeText(value);
  const length = Array.from(nickname).length;
  if (!nickname || length > NICKNAME_MAX_LENGTH) {
    return { ok: false, nickname, message: NICKNAME_RULE_MESSAGE };
  }
  return { ok: true, nickname, message: "" };
}

function projectMyProfile(payload = {}, apiBaseUrl = "") {
  const source = profilePayload(payload);
  return {
    nickname: normalizeText(source.nickname),
    avatarUrl: joinAvatarUrl(apiBaseUrl, extractAvatarUrl(source)),
    etag: normalizeText(
      source.principal_profile_etag || source.principalProfileETag || source.etag,
    ),
  };
}

// 平台 auth 管道也会写 globalData.profile(无 etag);只有享玩投影形状才可展示。
function isXiangwanProfile(value) {
  return Boolean(
    value && typeof value === "object" && !Array.isArray(value) && typeof value.etag === "string",
  );
}

function projectProfileBadge(profile) {
  const current = isXiangwanProfile(profile) ? profile : null;
  const nickname = normalizeText(current && current.nickname);
  return {
    nickname,
    avatarUrl: current ? normalizeText(current.avatarUrl) : "",
    initial: nicknameInitial(nickname),
    displayName: nickname || "设置头像昵称 ›",
    hasNickname: Boolean(nickname),
  };
}

function nicknameInitial(value) {
  const nickname = normalizeText(value);
  return nickname ? String(Array.from(nickname)[0]) : "享";
}

module.exports = {
  NICKNAME_MAX_LENGTH,
  NICKNAME_RULE_MESSAGE,
  extractAvatarUrl,
  isXiangwanProfile,
  joinAvatarUrl,
  nicknameInitial,
  projectMyProfile,
  projectProfileBadge,
  validateNickname,
};
