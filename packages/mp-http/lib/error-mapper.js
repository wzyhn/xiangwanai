"use strict";

// error-mapper — Phase B5-followup F7
//
// 把后端 errx code 映射成统一 toast 文案 / icon。三 app 之前各自 if(code === ...)
// 散在 service 层,本模块抽到 mp-http,createDefaultClient 可挂为默认 onError。
//
// 段位真相源: docs/contracts/error-codes.md (与 internal/pkg/errx/errx.go 对齐)。
// 段位规划:
//   10001-10006  通用
//   20001-20099  auth (identity)
//   20301-20399  storage (file)
//   30001-30099  community
//   30101-30199  study (note / parse)
//   30201-30299  record (audit / cascade / transcode)
//   30401-30499  schedule / timetable
//   40001-40099  ai
//   40101-40199  billing
//   40201-40299  syncpkg
//
// 调用方可注入自定义 mappers 覆盖 / 扩展 (例如某 app 的 30005 想要"专属"提示)。

const DEFAULT_GENERIC_TOAST = { title: "刚刚没能完成，请稍后再试", icon: "none" };
const DEFAULT_NETWORK_TOAST = { title: "网络有点忙，请稍后再试", icon: "none" };

// 通用段 (10001-10006) — 单 code 精确映射
const COMMON_TOASTS = {
  10001: { title: "有些信息需要再检查一下", icon: "none" },
  10002: { title: "登录状态已更新，请重新登录", icon: "none" },
  10003: { title: "当前账号暂时不能进行此操作", icon: "none" },
  10004: { title: "这项内容暂时找不到了", icon: "none" },
  10005: { title: "内容刚刚有更新，请刷新后再试", icon: "none" },
  10006: { title: "服务暂时开了个小差，请稍后再试", icon: "none" },
};

// 已落码精确映射 (与 errx.go const 对齐)
const NAMED_TOASTS = {
  20001: { title: "微信授权没有完成，请再试一次", icon: "none" },
  20301: { title: "文件超出大小限制", icon: "none" },
  20302: { title: "这个文件格式暂时不能使用", icon: "none" },
  30001: { title: "这篇内容暂时找不到了", icon: "none" },
  30002: { title: "这篇内容当前不能编辑", icon: "none" },
  30003: { title: "内容正在审核，请耐心等待", icon: "none" },
  30004: { title: "这条评论暂时找不到了", icon: "none" },
  30005: { title: "这份分享已过期，可以回首页看看", icon: "none" },
  30006: { title: "操作有点频繁，请稍后再试", icon: "none" },
  30007: { title: "这项内容需要调整后再提交", icon: "none" },
  30101: { title: "这篇笔记暂时找不到了", icon: "none" },
  30102: { title: "这个文件格式暂时不能导入", icon: "none" },
  30201: { title: "这条记录暂时找不到了", icon: "none" },
  30202: { title: "账号资料暂时没有清理完成", icon: "none" },
  30203: { title: "这项内容需要调整后再提交", icon: "none" },
  30204: { title: "视频还没有生成完成，请稍后再试", icon: "none" },
  30401: { title: "这份课表暂时找不到了", icon: "none" },
  30402: { title: "这份课表还没有导入完成，请再试一次", icon: "none" },
  30403: { title: "课表刚刚有更新，请刷新后再保存", icon: "none" },
  40001: { title: "本期 AI 使用额度已用完", icon: "none" },
  40002: { title: "AI 正在忙，请稍后再试", icon: "none" },
  40101: { title: "配额已用完", icon: "none" },
  40102: { title: "当前账号还没有这项权益", icon: "none" },
  40103: { title: "当前可用余额不足", icon: "none" },
  40104: { title: "撤销时间已过", icon: "none" },
  40105: { title: "记录刚刚有更新，请再试一次", icon: "none" },
  40201: { title: "这份分享已失效，请重新获取", icon: "none" },
  40202: { title: "分享课表数据异常，请重新获取", icon: "none" },
  40203: { title: "同步码正在生成，请再试一次", icon: "none" },
  40204: { title: "当前保存方式暂不可用", icon: "none" },
  40205: { title: "保存内容已变化，请重新确认", icon: "none" },
  40206: { title: "这次保存已撤销，请刷新", icon: "none" },
  40207: { title: "分享功能暂未开放", icon: "none" },
  40208: { title: "分享封面正在生成，请再试一次", icon: "none" },
};

// 段位 fallback (落码外的预留 namespace,确保 toast 文案不显示原始 code)
const RANGE_TOASTS = [
  { from: 20001, to: 20099, toast: { title: "登录暂时没有完成，请再试一次", icon: "none" } },
  { from: 20301, to: 20399, toast: { title: "文件暂时没有处理完成", icon: "none" } },
  { from: 30001, to: 30099, toast: { title: "刚刚没能完成，请稍后再试", icon: "none" } },
  { from: 30101, to: 30199, toast: { title: "笔记暂时没有更新，请稍后再试", icon: "none" } },
  { from: 30201, to: 30299, toast: { title: "记录暂时没有更新，请稍后再试", icon: "none" } },
  { from: 30401, to: 30499, toast: { title: "课表暂时没有更新，请稍后再试", icon: "none" } },
  { from: 40001, to: 40099, toast: { title: "AI 正在忙，请稍后再试", icon: "none" } },
  { from: 40101, to: 40199, toast: { title: "账户信息暂时没有更新，请稍后再试", icon: "none" } },
  { from: 40201, to: 40299, toast: { title: "分享课表暂时没有更新，请稍后再试", icon: "none" } },
];

function readErrorCode(error) {
  if (!error) return 0;
  const direct = Number(error.code || 0);
  if (direct) return direct;
  const body = error.body || error.data || {};
  return Number(body.code || 0);
}

function mapErrorToToast(error, options = {}) {
  const overrides = (options && options.overrides) || {};
  const code = readErrorCode(error);

  if (overrides[code]) return overrides[code];
  if (COMMON_TOASTS[code]) return COMMON_TOASTS[code];
  if (NAMED_TOASTS[code]) return NAMED_TOASTS[code];

  for (const entry of RANGE_TOASTS) {
    if (code >= entry.from && code <= entry.to) return entry.toast;
  }

  // codex P10 (PR #121, 2026-05-12): 不再把后端 error.message 直接回显成
  // toast — backend 文案不一定面向最终用户(可能是 stack trace、英文 RPC
  // error、内部 SQL 状态),用户体验差且潜在信息泄露。统一显通用提示;
  // 详情留 console 给 dev / Sentry 抓。
  if (error && error.message) {
    try {
      // eslint-disable-next-line no-console
      console.warn("[mp-http] unmapped error code=%s message=%s", code, String(error.message));
    } catch (_e) {}
  }
  return DEFAULT_GENERIC_TOAST;
}

function isNetworkError(error) {
  const message = [error && error.errMsg, error && error.message]
    .filter(Boolean)
    .join(" ")
    .toLowerCase();
  return /request:fail|network|timeout|timed out|offline|socket|econn|dns/.test(message);
}

// 页面内联状态同样必须遵守「不回显后端 message」的边界。调用方只传自己审阅过的
// fallback；有 errx code 时仍优先使用统一映射，网络问题则给出更可行动的提示。
function getUserFacingMessage(error, fallback = DEFAULT_GENERIC_TOAST.title) {
  if (readErrorCode(error)) {
    return mapErrorToToast(error).title;
  }
  if (isNetworkError(error)) {
    return DEFAULT_NETWORK_TOAST.title;
  }
  const safeFallback = String(fallback || "").trim();
  return safeFallback || DEFAULT_GENERIC_TOAST.title;
}

module.exports = {
  mapErrorToToast,
  COMMON_TOASTS,
  NAMED_TOASTS,
  RANGE_TOASTS,
  DEFAULT_GENERIC_TOAST,
  DEFAULT_NETWORK_TOAST,
  getUserFacingMessage,
};
