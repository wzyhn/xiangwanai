let authClientPkg = null;
try {
  authClientPkg = require("mp-weconq-auth-client");
} catch (error) {
  authClientPkg = require("../../mp-weconq-auth-client/index.js");
}

let authSessionPkg = null;
try {
  authSessionPkg = require("mp-auth-session");
} catch (error) {
  authSessionPkg = require("../../mp-auth-session/index.js");
}

const { createWeconqAuthClient } = authClientPkg;
const { createSessionStore } = authSessionPkg;

let activeRuntime = null;

function normalizeText(value, fallback = "") {
  const normalized = String(value || "").trim();
  return normalized || String(fallback || "").trim();
}

function hasOwn(target, key) {
  return !!target && Object.prototype.hasOwnProperty.call(target, key);
}

function resolveGetter(value, fallback = "") {
  if (typeof value === "function") {
    return () => normalizeText(value(), fallback);
  }
  return () => normalizeText(value, fallback);
}

function resolveWx(options = {}) {
  if (options.wx && typeof options.wx.login === "function") {
    return options.wx;
  }
  if (typeof wx !== "undefined" && wx && typeof wx.login === "function") {
    return wx;
  }
  return null;
}

function getMiniProgramAppId(fallback = "") {
  try {
    if (typeof wx === "undefined" || !wx || typeof wx.getAccountInfoSync !== "function") {
      return normalizeText(fallback);
    }
    const info = wx.getAccountInfoSync();
    return normalizeText(info && info.miniProgram && info.miniProgram.appId, fallback);
  } catch (_error) {
    return normalizeText(fallback);
  }
}

function readMiniProgramEnvVersion(fallback = "develop") {
  try {
    if (typeof wx === "undefined" || !wx || typeof wx.getAccountInfoSync !== "function") {
      return normalizeText(fallback, "develop");
    }
    const info = wx.getAccountInfoSync();
    return normalizeText(
      info && info.miniProgram && info.miniProgram.envVersion,
      fallback || "develop",
    );
  } catch (_error) {
    return normalizeText(fallback, "develop");
  }
}

function createWeconqAuthRuntimeError(message, extras = {}) {
  const error = new Error(normalizeText(message, "weconq auth runtime error"));
  Object.keys(extras || {}).forEach((key) => {
    error[key] = extras[key];
  });
  return error;
}

function readErrorMessage(error) {
  return normalizeText(
    error &&
      (error.message || (error.body && error.body.message) || (error.data && error.data.message)),
  ).toLowerCase();
}

function shouldResetAuthSession(error) {
  const statusCode = Number((error && error.statusCode) || 0);
  const errorCode = Number((error && error.code) || 0);
  if (statusCode === 401 || errorCode === 10002) {
    return true;
  }

  return statusCode === 404;
}

function shouldRetryWechatLogin(error) {
  const statusCode = Number((error && error.statusCode) || 0);
  const errorCode = Number((error && error.code) || 0);
  const message = readErrorMessage(error);

  if (statusCode >= 500 || errorCode === 10006) {
    return true;
  }

  if (statusCode === 401 || errorCode === 10002) {
    return true;
  }

  return [
    "code has been used",
    "code been used",
    "invalid code",
    "invalid js_code",
    "js code is invalid",
    "微信登录 code 已被使用",
    "微信登录 code 无效",
    "code 无效或已过期",
    "暂时不可用",
    "稍后再试",
  ].some((pattern) => message.includes(pattern));
}

function setActiveWeconqAuthRuntime(runtime) {
  activeRuntime = runtime || null;
  return activeRuntime;
}

function getActiveWeconqAuthRuntime() {
  if (activeRuntime) {
    return activeRuntime;
  }
  if (typeof getApp !== "function") {
    return null;
  }
  try {
    const app = getApp();
    return app && app.__weconqAuthRuntime ? app.__weconqAuthRuntime : null;
  } catch (_error) {
    return null;
  }
}

function createWeconqAuthRuntime(options = {}) {
  const sessionStore =
    options.sessionStore ||
    createSessionStore({
      defaultApiBaseUrl: normalizeText(options.defaultApiBaseUrl),
    });
  const wxApi = resolveWx(options);
  const baseUrl = resolveGetter(options.baseUrl, sessionStore.readApiBaseUrl());
  const productName = resolveGetter(options.productName, "WeconQ");
  const fallbackAppId = normalizeText(options.appId);
  const resolveAppId =
    typeof options.appId === "function"
      ? () => normalizeText(options.appId(), getMiniProgramAppId(fallbackAppId))
      : () => getMiniProgramAppId(fallbackAppId);
  const allowDevLogin = () =>
    options.allowDevLogin !== undefined
      ? Boolean(options.allowDevLogin)
      : readMiniProgramEnvVersion("develop") === "develop";
  const client =
    options.client ||
    createWeconqAuthClient({
      baseUrl,
      token: () => sessionStore.readAccessToken(),
      wx: options.wx,
    });

  let hostApp = options.app || null;
  let loginPromise = null;
  const listeners = new Set();

  function getHostApp() {
    if (hostApp) {
      return hostApp;
    }
    if (typeof getApp !== "function") {
      return null;
    }
    try {
      return getApp();
    } catch (_error) {
      return null;
    }
  }

  function getSession() {
    return {
      apiBaseUrl: sessionStore.readApiBaseUrl(),
      accessToken: sessionStore.readAccessToken(),
      accessTokenExpiresAt: sessionStore.readAccessTokenExpiresAt(),
      principalId: sessionStore.readPrincipalId(),
      openId: sessionStore.readOpenId(),
      profile: sessionStore.readProfile(),
      productState:
        typeof sessionStore.readProductState === "function"
          ? sessionStore.readProductState()
          : null,
      nextStep:
        typeof sessionStore.readNextStep === "function" ? sessionStore.readNextStep() : "none",
      productName: productName(),
      hasValidSession: sessionStore.isAccessTokenValid(),
      isAuthenticated: !!sessionStore.readAccessToken(),
    };
  }

  function syncAppGlobalData() {
    const app = getHostApp();
    const session = getSession();
    if (!app) {
      return session;
    }

    if (!app.globalData || typeof app.globalData !== "object") {
      app.globalData = {};
    }

    app.globalData.apiBaseUrl = session.apiBaseUrl;
    app.globalData.accessToken = session.accessToken;
    app.globalData.accessTokenExpiresAt = session.accessTokenExpiresAt;
    app.globalData.principalId = session.principalId;
    app.globalData.openId = session.openId;
    app.globalData.profile = session.profile;
    app.globalData.productState = session.productState;
    app.globalData.nextStep = session.nextStep;
    app.globalData.weconqProductName = session.productName;
    app.__weconqAuthRuntime = runtime;
    return session;
  }

  function emitSessionChange(reason = "updated") {
    const session = syncAppGlobalData();
    if (typeof options.onSessionChange === "function") {
      options.onSessionChange(session, { reason });
    }
    listeners.forEach((listener) => {
      try {
        listener(session, { reason });
      } catch (_error) {
        // ignore listener errors
      }
    });
    return session;
  }

  function clearAuthSession(reason = "logout") {
    sessionStore.clearAuthSession();
    return emitSessionChange(reason);
  }

  function hasValidSession() {
    return sessionStore.isAccessTokenValid();
  }

  function mergeAuthSession(sessionPatch = {}, reason = "session-updated") {
    const merged = {
      token: normalizeText(
        sessionPatch.accessToken || sessionPatch.token,
        sessionStore.readAccessToken(),
      ),
      expiresAt: normalizeText(
        sessionPatch.expiresAt || sessionPatch.expires_at,
        sessionStore.readAccessTokenExpiresAt(),
      ),
      principalId: normalizeText(
        sessionPatch.principalId || sessionPatch.principal_id,
        sessionStore.readPrincipalId(),
      ),
      openId: normalizeText(
        // canonical wire field is `openid` (snake); `openId` (camel) kept as legacy fallback
        sessionPatch.openid || sessionPatch.open_id || sessionPatch.openId,
        sessionStore.readOpenId(),
      ),
      productState:
        hasOwn(sessionPatch, "productState") || hasOwn(sessionPatch, "product_state")
          ? sessionPatch.productState || sessionPatch.product_state
          : typeof sessionStore.readProductState === "function"
            ? sessionStore.readProductState()
            : null,
      nextStep: normalizeText(
        sessionPatch.nextStep || sessionPatch.next_step,
        typeof sessionStore.readNextStep === "function" ? sessionStore.readNextStep() : "none",
      ),
      profile: hasOwn(sessionPatch, "profile") ? sessionPatch.profile : sessionStore.readProfile(),
    };

    sessionStore.setAuthSession(merged);
    return emitSessionChange(reason);
  }

  function performWxLogin() {
    if (!wxApi || typeof wxApi.login !== "function") {
      return Promise.reject(createWeconqAuthRuntimeError("wx.login is unavailable"));
    }

    return new Promise((resolve, reject) => {
      wxApi.login({
        success(result) {
          const code = normalizeText(result && result.code);
          if (!code) {
            reject(createWeconqAuthRuntimeError("missing wechat login code"));
            return;
          }
          resolve(code);
        },
        fail(error) {
          reject(
            createWeconqAuthRuntimeError(String((error && error.errMsg) || "wx.login failed"), {
              cause: error,
            }),
          );
        },
      });
    });
  }

  async function refreshProfile() {
    const token = sessionStore.readAccessToken();
    if (!token) {
      return getSession();
    }

    try {
      const profile = await client.getCurrentProfile({ token });
      sessionStore.setProfile(profile);
      return emitSessionChange("profile-refreshed");
    } catch (error) {
      if (shouldResetAuthSession(error)) {
        clearAuthSession("auth-invalid");
      }
      throw error;
    }
  }

  async function loginWithWechat(loginOptions = {}) {
    const appId = normalizeText(loginOptions.appId, resolveAppId());
    if (!appId) {
      throw createWeconqAuthRuntimeError("missing mini program app id", {
        code: "missing_app_id",
      });
    }

    for (let attempt = 0; attempt < 2; attempt += 1) {
      const code = await performWxLogin();
      try {
        return await client.loginWithWeChat({
          appId,
          code,
        });
      } catch (error) {
        // WeChat login codes are single-use; retries must fetch a brand-new code.
        if (attempt >= 1 || !shouldRetryWechatLogin(error)) {
          throw error;
        }
      }
    }

    throw createWeconqAuthRuntimeError("wechat login failed");
  }

  async function verifySession(verifyOptions = {}) {
    const token = sessionStore.readAccessToken();
    if (!token) {
      throw createWeconqAuthRuntimeError("auth required", {
        code: "auth_required",
        authRequired: true,
        statusCode: 401,
      });
    }

    try {
      const sessionPayload = await client.verifySession({
        token,
        appId: normalizeText(verifyOptions.appId, resolveAppId()),
        productCode: normalizeText(verifyOptions.productCode),
      });
      mergeAuthSession(sessionPayload, "session-verified");
      return getSession();
    } catch (error) {
      if (shouldResetAuthSession(error)) {
        clearAuthSession("auth-invalid");
      }
      throw error;
    }
  }

  async function login(loginOptions = {}) {
    if (loginPromise) {
      return loginPromise;
    }

    loginPromise = (async () => {
      const useDevLogin = Boolean(loginOptions.useDevLogin);
      let sessionPayload = null;
      if (useDevLogin) {
        if (!allowDevLogin()) {
          throw createWeconqAuthRuntimeError(
            "dev login is disabled in this mini program environment",
            {
              code: "dev_login_disabled",
              statusCode: 403,
            },
          );
        }
        sessionPayload = await client.devLogin({
          nickname: normalizeText(
            loginOptions.nickname,
            normalizeText(options.devNickname, `${productName().toLowerCase()}-dev`),
          ),
          providerId: normalizeText(
            loginOptions.providerId,
            normalizeText(options.devProviderId, `${productName().toLowerCase()}-dev`),
          ),
          role: normalizeText(loginOptions.role, normalizeText(options.devRole, "student")),
        });
      } else {
        sessionPayload = await loginWithWechat(loginOptions);
      }

      mergeAuthSession(sessionPayload, useDevLogin ? "dev-login" : "login");

      if (!sessionPayload || !sessionPayload.profile) {
        try {
          await refreshProfile();
        } catch (_error) {
          // keep session if /me is temporarily unavailable
        }
      }

      return getSession();
    })().finally(() => {
      loginPromise = null;
    });

    return loginPromise;
  }

  async function ensureSession(loginOptions = {}) {
    syncAppGlobalData();
    if (hasValidSession()) {
      if (loginOptions.verify) {
        try {
          await verifySession(loginOptions);
        } catch (_error) {
          // ignore verify failures during restore flows and keep local session if token still looks valid
        }
      }
      if (loginOptions.refreshProfile !== false && !sessionStore.readProfile()) {
        try {
          await refreshProfile();
        } catch (_error) {
          // ignore profile refresh failures for restore flows
        }
      }
      return getSession();
    }

    if (sessionStore.readAccessToken() && !hasValidSession()) {
      clearAuthSession("expired");
    }

    if (loginOptions.interactive === false) {
      throw createWeconqAuthRuntimeError("auth required", {
        code: "auth_required",
        authRequired: true,
        statusCode: 401,
      });
    }

    return login(loginOptions);
  }

  function hydrate(hydrateOptions = {}) {
    syncAppGlobalData();
    if (hydrateOptions.verify && sessionStore.readAccessToken()) {
      return verifySession(hydrateOptions).catch(() => getSession());
    }
    if (hydrateOptions.refreshProfile && sessionStore.readAccessToken()) {
      return refreshProfile().catch(() => getSession());
    }
    return Promise.resolve(getSession());
  }

  function subscribe(listener) {
    if (typeof listener !== "function") {
      return () => {};
    }
    listeners.add(listener);
    return () => listeners.delete(listener);
  }

  const runtime = {
    client,
    sessionStore,
    clearAuthSession,
    devLogin(loginOptions = {}) {
      return login({
        ...loginOptions,
        useDevLogin: true,
      });
    },
    ensureSession,
    getSession,
    hasValidSession,
    hydrate,
    install(app) {
      hostApp = app || hostApp;
      setActiveWeconqAuthRuntime(runtime);
      syncAppGlobalData();
      return runtime;
    },
    login,
    logout() {
      return clearAuthSession("logout");
    },
    isDevLoginEnabled() {
      return allowDevLogin();
    },
    refreshProfile,
    verifySession,
    resolveAppId,
    resolveProductName() {
      return productName();
    },
    subscribe,
    syncAppGlobalData,
  };

  if (options.activate !== false) {
    setActiveWeconqAuthRuntime(runtime);
  }
  syncAppGlobalData();

  return runtime;
}

module.exports = {
  createWeconqAuthRuntime,
  createWeconqAuthRuntimeError,
  getActiveWeconqAuthRuntime,
  setActiveWeconqAuthRuntime,
};
