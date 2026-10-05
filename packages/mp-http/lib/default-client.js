"use strict";

// createDefaultClient — 把 community / study / schedule 三个 app 之前各自
// copy-paste 的 32 行 http boot 收敛到此。封装:
//   - 从 getApp().globalData (apiBaseUrl / accessToken) 读运行时态
//   - fallback 到 mp-auth-session 的 sessionStore
//   - onAuthRequired 默认走 mp-weconq-auth-guard 的 promptCurrentPageWeconqLogin
//
// 使用:
//   const { createDefaultClient } = require("mp-http");
//   const { request, readApiBaseUrl, readAccessToken } = createDefaultClient();
//
// 不在本封装内的(by design):
//   - 任何业务专属 endpoint 拼装
//   - errx 错误码消费(P-B3 扩 mp-errx 时再做)
//
// 历史:Audit 9 抓出 community/study api/http.js byte-identical;schedule 没接,
// 将自行造第三个轮子。本封装阻断该 drift。
const { createClient } = require("./client");
const { mapErrorToToast } = require("./error-mapper");

function readAppValue(getAppFn, key) {
  // lazy: getAppFn 在每次调用时再 resolve;避免 module import 期 getApp 还
  // 没注册(community/study app.js 在 App({}) 之前 require analytics →
  // api/http.js → createDefaultClient 时,getApp 尚未变 function,会永久
  // 锁死 getAppRef = null,导致 globalData.weconqProductCode 永远拿不到)。
  const app = typeof getAppFn === "function" ? safeGetApp(getAppFn) : null;
  return app && app.globalData ? app.globalData[key] : "";
}

function safeGetApp(getAppFn) {
  try {
    return getAppFn();
  } catch (_e) {
    return null;
  }
}

function createDefaultClient(options = {}) {
  // 允许测试 / 非小程序 runtime 注入。生产路径走默认。
  // ⚠ getAppRef lazy 化:不在 import 期固化 typeof getApp。
  // 见 readAppValue 注释——community/study app.js import 链顺序使 getApp
  // 在 createDefaultClient() 调用瞬间还可能不是 function。
  const injected = typeof options.getApp === "function" ? options.getApp : null;
  const getAppRef = injected
    ? injected
    : function lazyGetApp() {
        return typeof getApp === "function" ? getApp() : null;
      };

  // sessionStore: lazy require 防止 packages/mp-auth-session 不在 wx 环境初始化。
  let sessionStore = options.sessionStore || null;
  function ensureSessionStore() {
    if (sessionStore) return sessionStore;
    const { createSessionStore } = require("mp-auth-session");
    sessionStore = createSessionStore();
    return sessionStore;
  }

  function readApiBaseUrl() {
    return readAppValue(getAppRef, "apiBaseUrl") || ensureSessionStore().readApiBaseUrl();
  }

  function readAccessToken() {
    return readAppValue(getAppRef, "accessToken") || ensureSessionStore().readAccessToken();
  }

  let onAuthRequired = options.onAuthRequired;
  if (onAuthRequired === undefined) {
    // 默认行为:走 mp-weconq-auth-guard。lazy require 同样的理由。
    const { promptCurrentPageWeconqLogin } = require("mp-weconq-auth-guard");
    onAuthRequired = function defaultOnAuthRequired() {
      return promptCurrentPageWeconqLogin({
        forceRelogin: true,
        reason: "server-auth-rejected",
      });
    };
  }

  const baseRequest = createClient({
    baseUrl: options.baseUrl || readApiBaseUrl,
    token: options.token || readAccessToken,
    onAuthRequired,
    isAuthRequiredError: options.isAuthRequiredError,
    // Pass-through: optional hook for callers that need response headers / statusCode.
    // community / study / schedule do not pass onResponseMeta → undefined → ignored.
    onResponseMeta: options.onResponseMeta,
  });

  // Phase B5-followup F7: errx → toast 默认映射。
  // - options.onError === false              → 完全 opt-out(原行为,直接 reject)
  // - options.onError === function           → 调用方完全自定义
  // - 默认                                    → mapErrorToToast + wx.showToast
  // - options.errorOverrides                 → 透传给 mapErrorToToast 覆盖个别 code
  const onErrorOption = options.onError;
  let handleError;
  if (onErrorOption === false) {
    handleError = null;
  } else if (typeof onErrorOption === "function") {
    handleError = onErrorOption;
  } else {
    const overrides = options.errorOverrides || {};
    handleError = function defaultOnError(error) {
      const toast = mapErrorToToast(error, { overrides });
      try {
        if (typeof wx !== "undefined" && typeof wx.showToast === "function") {
          wx.showToast({ title: toast.title, icon: toast.icon || "none" });
        }
      } catch (_e) {}
    };
  }

  function request(requestOptions = {}) {
    const promise = baseRequest(requestOptions);
    if (!handleError || (requestOptions && requestOptions.skipDefaultErrorToast === true)) {
      return promise;
    }
    // codex P8 (PR #121, 2026-05-12): silent fallback — request 用作 probe
    // (e.g. "试登录拿身份" / "看看 v2 endpoint 有没有上线") 时,error 上挂
    // expectedFallback / silentOnError 标记,handler 跳过 toast 避免对正常
    // fallback 弹"操作失败"。caller 可在 reject 链 catch 内再抛业务错误。
    // codex P9: 双 toast — caller 自己已 wx.showToast 后,标记 handledToast
    // 防止默认 handler 再覆盖一次。
    return promise.catch((error) => {
      const silent =
        (requestOptions && requestOptions.silentOnError === true) ||
        (error &&
          (error.expectedFallback === true ||
            error.silentOnError === true ||
            error.handledToast === true));
      if (!silent) {
        try {
          handleError(error, requestOptions);
        } catch (_e) {
          // 默认 onError 自身异常不影响 reject 链
        }
      }
      throw error;
    });
  }

  return {
    request,
    readApiBaseUrl,
    readAccessToken,
  };
}

module.exports = {
  createDefaultClient,
};
