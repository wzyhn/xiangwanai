let runtimePkg = null;
try {
  runtimePkg = require("mp-weconq-auth-runtime");
} catch (error) {
  runtimePkg = require("../../mp-weconq-auth-runtime/index.js");
}

const { getActiveWeconqAuthRuntime } = runtimePkg;

function normalizeText(value, fallback = "") {
  const normalized = String(value || "").trim();
  return normalized || String(fallback || "").trim();
}

function createWeconqAuthGuardError(message, extras = {}) {
  const error = new Error(normalizeText(message, "weconq auth guard error"));
  Object.keys(extras || {}).forEach((key) => {
    error[key] = extras[key];
  });
  return error;
}

function getCurrentPage() {
  try {
    if (typeof getCurrentPages !== "function") {
      return null;
    }
    const pages = getCurrentPages();
    return Array.isArray(pages) && pages.length ? pages[pages.length - 1] : null;
  } catch (_error) {
    return null;
  }
}

function resolvePage(page) {
  if (page && typeof page.setData === "function") {
    return page;
  }
  return getCurrentPage();
}

function readAppProductName() {
  try {
    if (typeof getApp !== "function") {
      return "";
    }
    const app = getApp();
    return normalizeText(app && app.globalData && app.globalData.weconqProductName, "WeconQ");
  } catch (_error) {
    return "WeconQ";
  }
}

function isWeconqAuthRequiredError(error) {
  return Boolean(
    error &&
      (error.authRequired ||
        error.cancelled ||
        Number(error.statusCode || 0) === 401 ||
        Number(error.code || 0) === 10002 ||
        normalizeText(error.code) === "auth_required"),
  );
}

function buildPromptData(options = {}, defaults = {}, currentData = {}) {
  const data = currentData && typeof currentData === "object" ? currentData : {};
  return {
    weconqLoginProductName: normalizeText(
      data.weconqLoginProductName,
      normalizeText(options.productName, normalizeText(defaults.productName, readAppProductName())),
    ),
    weconqLoginSceneTitle: normalizeText(
      data.weconqLoginSceneTitle,
      normalizeText(options.sceneTitle, normalizeText(defaults.sceneTitle, "登录后继续")),
    ),
    weconqLoginSceneDetail: normalizeText(
      data.weconqLoginSceneDetail,
      normalizeText(options.sceneDetail, normalizeText(defaults.sceneDetail)),
    ),
    weconqLoginAllowDevLogin: Boolean(
      data.weconqLoginAllowDevLogin !== undefined
        ? data.weconqLoginAllowDevLogin
        : options.allowDevLogin !== undefined
          ? options.allowDevLogin
          : defaults.allowDevLogin,
    ),
  };
}

function bindWeconqAuthPage(page, defaults = {}) {
  const targetPage = resolvePage(page);
  if (!targetPage) {
    return null;
  }
  const nextDefaults = {
    ...(targetPage.__weconqAuthPromptDefaults || {}),
    ...defaults,
  };
  targetPage.__weconqAuthPromptDefaults = nextDefaults;
  targetPage.__weconqAuthPromptBound = true;
  targetPage.setData(buildPromptData({}, nextDefaults, targetPage.data));
  return targetPage;
}

function closePrompt(page, error, detail) {
  const targetPage = resolvePage(page);
  if (!targetPage) {
    return;
  }
  const pending = targetPage.__weconqLoginPrompt || null;
  targetPage.__weconqLoginPrompt = null;
  targetPage.setData({
    showWeconqLoginPopup: false,
  });
  if (!pending) {
    return;
  }
  if (error) {
    pending.reject(error);
    return;
  }
  pending.resolve(detail);
}

function promptWeconqLogin(page, options = {}) {
  const targetPage = resolvePage(page);
  if (!targetPage) {
    return Promise.reject(
      createWeconqAuthGuardError("no active page for weconq login", {
        code: "no_active_page",
      }),
    );
  }

  const runtime = getActiveWeconqAuthRuntime();
  if (!runtime) {
    return Promise.reject(
      createWeconqAuthGuardError("missing weconq auth runtime", {
        code: "missing_runtime",
      }),
    );
  }
  const runtimeAllowsDevLogin =
    typeof runtime.isDevLoginEnabled === "function" ? runtime.isDevLoginEnabled() : true;
  if (runtime.hasValidSession()) {
    return Promise.resolve(runtime.getSession());
  }
  if (!targetPage.__weconqAuthPromptBound) {
    return Promise.reject(
      createWeconqAuthGuardError("page has no bound weconq login prompt", {
        code: "missing_prompt_host",
      }),
    );
  }
  if (targetPage.__weconqLoginPrompt && targetPage.__weconqLoginPrompt.promise) {
    return targetPage.__weconqLoginPrompt.promise;
  }

  const defaults = targetPage.__weconqAuthPromptDefaults || {};
  targetPage.setData({
    showWeconqLoginPopup: true,
    ...buildPromptData(options, defaults, targetPage.data),
    weconqLoginAllowDevLogin: Boolean(
      runtimeAllowsDevLogin &&
        (options.allowDevLogin !== undefined ? options.allowDevLogin : defaults.allowDevLogin),
    ),
  });

  let resolvePromise = null;
  let rejectPromise = null;
  const promise = new Promise((resolve, reject) => {
    resolvePromise = resolve;
    rejectPromise = reject;
  });
  targetPage.__weconqLoginPrompt = {
    promise,
    resolve: resolvePromise,
    reject: rejectPromise,
  };

  return promise;
}

function handleWeconqLoginClose(page) {
  closePrompt(
    page,
    createWeconqAuthGuardError("weconq login cancelled", {
      code: "login_cancelled",
      cancelled: true,
    }),
  );
}

function handleWeconqLoginSuccess(page, event) {
  closePrompt(page, null, event && event.detail ? event.detail : null);
}

async function ensureWeconqSessionForPage(page, options = {}) {
  const targetPage = resolvePage(page);
  if (targetPage && targetPage.__weconqSessionEnsurePromise) {
    return targetPage.__weconqSessionEnsurePromise;
  }

  const pending = (async () => {
    const runtime = getActiveWeconqAuthRuntime();
    if (!runtime) {
      throw createWeconqAuthGuardError("missing weconq auth runtime", {
        code: "missing_runtime",
      });
    }

    if (runtime.hasValidSession()) {
      return true;
    }

    try {
      await runtime.ensureSession({
        interactive: false,
      });
      return true;
    } catch (error) {
      if (!isWeconqAuthRequiredError(error)) {
        throw error;
      }
    }

    try {
      await promptWeconqLogin(targetPage || page, options);
      return true;
    } catch (_error) {
      return false;
    }
  })();

  if (targetPage) {
    targetPage.__weconqSessionEnsurePromise = pending;
  }
  try {
    return await pending;
  } finally {
    if (targetPage && targetPage.__weconqSessionEnsurePromise === pending) {
      targetPage.__weconqSessionEnsurePromise = null;
    }
  }
}

async function runWithWeconqAuth(page, action, options = {}) {
  const ready = await ensureWeconqSessionForPage(page, options);
  if (!ready) {
    return undefined;
  }
  return action();
}

async function promptCurrentPageWeconqLogin(options = {}) {
  const runtime = getActiveWeconqAuthRuntime();
  if (runtime && options.forceRelogin === true && typeof runtime.clearAuthSession === "function") {
    runtime.clearAuthSession(normalizeText(options.reason, "server-auth-rejected"));
  }
  if (runtime && !runtime.hasValidSession()) {
    try {
      await runtime.ensureSession({ interactive: false });
      return true;
    } catch (_error) {
      // silent auth failed, fall through to interactive popup
    }
  }
  try {
    await promptWeconqLogin(getCurrentPage(), options);
    return true;
  } catch (_error) {
    return false;
  }
}

module.exports = {
  bindWeconqAuthPage,
  createWeconqAuthGuardError,
  ensureWeconqSessionForPage,
  handleWeconqLoginClose,
  handleWeconqLoginSuccess,
  isWeconqAuthRequiredError,
  promptCurrentPageWeconqLogin,
  promptWeconqLogin,
  runWithWeconqAuth,
};
