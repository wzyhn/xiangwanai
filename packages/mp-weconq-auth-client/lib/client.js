function normalizeText(value, fallback = "") {
  const normalized = String(value || "").trim();
  return normalized || String(fallback || "").trim();
}

function normalizeTimestamp(value) {
  const normalized = normalizeText(value);
  if (!normalized) {
    return "";
  }
  const timestamp = Date.parse(normalized);
  if (Number.isNaN(timestamp)) {
    return normalized;
  }
  return new Date(timestamp).toISOString();
}

function trimTrailingSlash(value) {
  return normalizeText(value).replace(/\/+$/, "");
}

function resolveGetter(value, fallback = "") {
  if (typeof value === "function") {
    return () => normalizeText(value(), fallback);
  }
  return () => normalizeText(value, fallback);
}

function resolveWx(options = {}) {
  if (options.wx && typeof options.wx.request === "function") {
    return options.wx;
  }
  if (typeof wx !== "undefined" && wx && typeof wx.request === "function") {
    return wx;
  }
  return null;
}

function createWeconqAuthClientError(message, extras = {}) {
  const error = new Error(normalizeText(message, "weconq auth request failed"));
  Object.keys(extras || {}).forEach((key) => {
    error[key] = extras[key];
  });
  return error;
}

function createJSONRequest(options = {}) {
  const baseUrl = resolveGetter(options.baseUrl);
  const token = resolveGetter(options.token);
  const wxApi = resolveWx(options);

  return function request(requestOptions = {}) {
    const path = normalizeText(requestOptions.url);
    const targetBaseUrl = trimTrailingSlash(baseUrl());
    if (!path) {
      return Promise.reject(createWeconqAuthClientError("missing request url"));
    }
    if (!targetBaseUrl) {
      return Promise.reject(createWeconqAuthClientError("missing api base url"));
    }
    if (!wxApi) {
      return Promise.reject(createWeconqAuthClientError("wx.request is unavailable"));
    }

    const header = {
      "Content-Type": "application/json",
      ...(requestOptions.header || {}),
    };

    const anonymous = requestOptions.authMode === "none";
    const authToken = anonymous ? "" : normalizeText(requestOptions.token) || token();
    if (anonymous || authToken) {
      Object.keys(header).forEach((key) => {
        if (key.toLowerCase() === "authorization") delete header[key];
      });
    }
    if (authToken) {
      header.Authorization = `Bearer ${authToken}`;
    }

    return new Promise((resolve, reject) => {
      wxApi.request({
        url: `${targetBaseUrl}${path}`,
        method: requestOptions.method || "GET",
        data: requestOptions.data,
        timeout: requestOptions.timeout || 15000,
        header,
        success(response) {
          const body = response.data;
          const validEnvelope = Boolean(
            body &&
              typeof body === "object" &&
              !Array.isArray(body) &&
              Number.isSafeInteger(body.code),
          );
          if (
            response.statusCode >= 200 &&
            response.statusCode < 300 &&
            validEnvelope &&
            body.code === 0
          ) {
            resolve(body.data);
            return;
          }
          reject(
            createWeconqAuthClientError(
              validEnvelope
                ? String(body.message || `request failed with status ${response.statusCode}`)
                : "invalid response envelope",
              {
                code: validEnvelope ? body.code || Number(response.statusCode || 500) : 10006,
                statusCode: Number(response.statusCode || 0),
                body: validEnvelope ? body : undefined,
                data: validEnvelope ? body : undefined,
                invalidResponse: !validEnvelope,
                url: `${targetBaseUrl}${path}`,
              },
            ),
          );
        },
        fail(error) {
          reject(
            createWeconqAuthClientError(
              String((error && error.errMsg) || "network request failed"),
              { url: `${targetBaseUrl}${path}` },
            ),
          );
        },
      });
    });
  };
}

function requestFirstAvailable(request, paths, requestOptions = {}, routeResolutionCache = null) {
  const candidates = Array.isArray(paths) ? paths.filter(Boolean) : [];
  if (!candidates.length) {
    return Promise.reject(createWeconqAuthClientError("missing auth endpoint candidates"));
  }

  const method = normalizeText(requestOptions.method, "GET").toUpperCase();
  const cacheKey = routeResolutionCache ? `${method} ${normalizeText(candidates[0])}` : "";

  const dispatch = (path, options = {}) =>
    request({
      ...options,
      url: path,
    });

  const probeCandidates = (options = {}, startIndex = 0, lastError = null) => {
    if (startIndex >= candidates.length) {
      return Promise.reject(lastError || createWeconqAuthClientError("no auth endpoint responded"));
    }

    const path = candidates[startIndex];
    return dispatch(path, options)
      .then((payload) => {
        if (cacheKey) {
          routeResolutionCache.set(cacheKey, path);
        }
        return payload;
      })
      .catch((error) => {
        if (Number((error && error.statusCode) || 0) === 404) {
          return probeCandidates(options, startIndex + 1, error);
        }
        if (cacheKey) {
          routeResolutionCache.delete(cacheKey);
        }
        return Promise.reject(error);
      });
  };

  if (cacheKey) {
    const preferredPath = routeResolutionCache.get(cacheKey);
    if (preferredPath) {
      return dispatch(preferredPath, requestOptions).catch((error) => {
        if (Number((error && error.statusCode) || 0) === 404) {
          routeResolutionCache.delete(cacheKey);
          return probeCandidates(requestOptions);
        }
        return Promise.reject(error);
      });
    }
  }

  return probeCandidates(requestOptions);
}

function normalizeProfile(payload) {
  if (!payload || typeof payload !== "object" || Array.isArray(payload)) {
    return null;
  }

  const principalId = normalizeText(payload.principalId || payload.principal_id || payload.id);
  const avatarUrl = normalizeText(payload.avatarUrl || payload.avatar_url);
  const primaryTenantId = normalizeText(payload.primaryTenantId || payload.primary_tenant_id);
  const createdAt = normalizeTimestamp(payload.createdAt || payload.created_at);

  return {
    ...payload,
    principalId,
    principal_id: principalId,
    nickname: normalizeText(payload.nickname),
    avatarUrl,
    avatar_url: avatarUrl,
    phone: payload.phone || null,
    phoneBound: Boolean(payload.phoneBound || payload.phone_bound),
    phone_bound: Boolean(payload.phoneBound || payload.phone_bound),
    schoolBound: Boolean(payload.schoolBound || payload.school_bound),
    school_bound: Boolean(payload.schoolBound || payload.school_bound),
    profileCompleted: Boolean(payload.profileCompleted || payload.profile_completed),
    profile_completed: Boolean(payload.profileCompleted || payload.profile_completed),
    role: normalizeText(payload.role),
    status: normalizeText(payload.status),
    primaryTenantId,
    primary_tenant_id: primaryTenantId,
    createdAt,
    created_at: createdAt,
  };
}

function normalizeProductState(payload) {
  if (!payload || typeof payload !== "object" || Array.isArray(payload)) {
    return null;
  }

  const productCode = normalizeText(payload.productCode || payload.product_code);
  const lastLoginAt = normalizeTimestamp(payload.lastLoginAt || payload.last_login_at);
  const lastLoginAppId = normalizeText(payload.lastLoginAppId || payload.last_login_app_id);

  return {
    ...payload,
    productCode,
    product_code: productCode,
    phoneBound: Boolean(payload.phoneBound || payload.phone_bound),
    phone_bound: Boolean(payload.phoneBound || payload.phone_bound),
    schoolBound: Boolean(payload.schoolBound || payload.school_bound),
    school_bound: Boolean(payload.schoolBound || payload.school_bound),
    profileCompleted: Boolean(payload.profileCompleted || payload.profile_completed),
    profile_completed: Boolean(payload.profileCompleted || payload.profile_completed),
    lastLoginAt,
    last_login_at: lastLoginAt,
    lastLoginAppId,
    last_login_app_id: lastLoginAppId,
  };
}

function normalizeSessionStatePayload(payload) {
  const profile = normalizeProfile(payload && payload.profile);
  const productState = normalizeProductState(
    payload && (payload.productState || payload.product_state),
  );
  const productCode = normalizeText(
    payload &&
      (payload.productCode ||
        payload.product_code ||
        (productState && (productState.productCode || productState.product_code))),
  );
  const accessToken = normalizeText(payload && (payload.accessToken || payload.token));
  const principalId = normalizeText(
    payload &&
      (payload.principalId ||
        payload.principal_id ||
        (profile && (profile.principalId || profile.principal_id))),
  );
  // canonical wire field is `openid` (snake); `openId` (camel) kept as legacy fallback
  // pending step-3 server-side removal — see docs/standards/api-deprecation.md
  const openId = normalizeText(payload && (payload.openid || payload.open_id || payload.openId));
  const appId = normalizeText(payload && (payload.appId || payload.app_id));
  const expiresAt = normalizeTimestamp(payload && (payload.expiresAt || payload.expires_at));
  const expiresIn = Math.max(
    0,
    Number((payload && (payload.expiresIn || payload.expires_in)) || 0) || 0,
  );
  const isNew = Boolean(payload && (payload.isNew || payload.is_new));
  const isNewUser = Boolean(payload && (payload.isNewUser || payload.is_new_user || isNew));
  const nextStep = normalizeText(payload && (payload.nextStep || payload.next_step), "none");

  return {
    ...payload,
    token: accessToken,
    accessToken,
    tokenType: normalizeText(payload && (payload.tokenType || payload.token_type), "Bearer"),
    principalId,
    principal_id: principalId,
    openId,
    openid: openId,
    appId,
    app_id: appId,
    productCode,
    product_code: productCode,
    expiresAt,
    expires_at: expiresAt,
    expiresIn,
    expires_in: expiresIn,
    isNew,
    is_new: isNew,
    isNewUser,
    is_new_user: isNewUser,
    productState,
    product_state: productState,
    nextStep,
    next_step: nextStep,
    profile,
  };
}

function createWeconqAuthClient(options = {}) {
  const request =
    typeof options.request === "function" ? options.request : createJSONRequest(options);
  const routeResolutionCache = new Map();

  const loginPaths =
    Array.isArray(options.loginPaths) && options.loginPaths.length
      ? options.loginPaths
      : ["/api/v1/auth/wechat/login", "/api/auth/wechat/login", "/auth/wechat/login"];
  const devLoginPaths =
    Array.isArray(options.devLoginPaths) && options.devLoginPaths.length
      ? options.devLoginPaths
      : ["/api/v1/auth/dev-login", "/api/auth/dev-login", "/auth/dev-login"];
  const mePaths =
    Array.isArray(options.mePaths) && options.mePaths.length
      ? options.mePaths
      : ["/api/v1/auth/me", "/api/auth/me", "/auth/me"];
  const verifyPaths =
    Array.isArray(options.verifyPaths) && options.verifyPaths.length
      ? options.verifyPaths
      : ["/api/v1/auth/verify", "/api/auth/verify", "/auth/verify"];

  return {
    loginWithWeChat(payload = {}) {
      return requestFirstAvailable(
        request,
        loginPaths,
        {
          method: "POST",
          data: {
            app_id: normalizeText(payload.appId || payload.app_id),
            code: normalizeText(payload.code),
          },
          authMode: "none",
          skipAuthRetry: true,
        },
        routeResolutionCache,
      ).then(normalizeSessionStatePayload);
    },

    devLogin(payload = {}) {
      return requestFirstAvailable(
        request,
        devLoginPaths,
        {
          method: "POST",
          data: {
            nickname: normalizeText(payload.nickname || payload.name),
            provider_id: normalizeText(
              payload.providerId || payload.provider_id || payload.nickname || payload.name,
            ),
            role: normalizeText(payload.role, "student"),
          },
          authMode: "none",
          skipAuthRetry: true,
        },
        routeResolutionCache,
      ).then(normalizeSessionStatePayload);
    },

    getCurrentProfile(payload = {}) {
      return requestFirstAvailable(
        request,
        mePaths,
        {
          method: "GET",
          token: normalizeText(payload.token),
          authMode: "optional",
          skipAuthRetry: true,
        },
        routeResolutionCache,
      ).then(normalizeProfile);
    },

    verifySession(payload = {}) {
      return requestFirstAvailable(
        request,
        verifyPaths,
        {
          method: "POST",
          token: normalizeText(payload.token),
          data: {
            app_id: normalizeText(payload.appId || payload.app_id),
            product_code: normalizeText(payload.productCode || payload.product_code),
          },
          authMode: "optional",
          skipAuthRetry: true,
        },
        routeResolutionCache,
      ).then(normalizeSessionStatePayload);
    },
  };
}

module.exports = {
  createWeconqAuthClient,
  createWeconqAuthClientError,
};
