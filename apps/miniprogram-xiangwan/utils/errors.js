"use strict";

function createXiangwanError(code, userMessage, extras = {}) {
  const error = new Error(String(userMessage || "操作未完成"));
  error.code = code;
  error.userMessage = String(userMessage || "操作未完成");
  Object.keys(extras || {}).forEach((key) => {
    error[key] = extras[key];
  });
  return error;
}

// 与 apps/xiangwan-admin-web/src/lib/api.ts 的技术词表保持同步(两侧清单必须一起改)。
const TECHNICAL_MESSAGE_PATTERN =
  /\b(postgresql|redis|oidc|pkce|principal|series|instance|session|registration|checkin|coupon|order|idempotency|runtime|token|api|grant|generation)\b|internal server error|service unavailable|invalid|failed|not found|conflict|unavailable/i;

function isCustomerSafeMessage(message) {
  const normalized = String(message || "").trim();
  return /[\u3400-\u9fff]/u.test(normalized) && !TECHNICAL_MESSAGE_PATTERN.test(normalized);
}

function getUserMessage(error, fallback = "暂时无法完成，请稍后重试") {
  if (error && isCustomerSafeMessage(error.userMessage)) {
    return String(error.userMessage);
  }
  // 后端 4xx 已统一为客户向中文文案(customer_error.go);本地服务层与传输层
  // 的英文/技术词 message 会被 isCustomerSafeMessage 过滤,不会漏给用户;
  // 5xx 由服务端边界改写为英文 internal server error,同样被过滤。
  if (error && isCustomerSafeMessage(error.message)) {
    return String(error.message);
  }
  const statusCode = Number((error && error.statusCode) || 0);
  if (statusCode === 401) return "登录状态已失效，请重新登录";
  if (statusCode === 403) return "当前账号暂不能执行此操作";
  if (statusCode === 404) return "内容已下线或不存在";
  if (statusCode === 409) return "活动信息已变化，请刷新后重新确认";
  if (statusCode === 429) return "操作太频繁，请稍后再试";
  return String(fallback || "暂时无法完成，请稍后重试");
}

function isUnknownWriteResult(error) {
  const statusCode = Number((error && error.statusCode) || 0);
  return Boolean(error && (!statusCode || error.invalidResponse === true || statusCode >= 500));
}

module.exports = {
  createXiangwanError,
  getUserMessage,
  isCustomerSafeMessage,
  isUnknownWriteResult,
};
