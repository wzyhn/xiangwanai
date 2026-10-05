let storage = null;
try {
  storage = require("mp-store");
} catch (error) {
  storage = require("../../mp-store/index.js");
}

const { getJSON, getString, remove, setJSON, setString } = storage;

const API_BASE_URL_KEY = "weconq_api_base_url";
const ACCESS_TOKEN_KEY = "weconq_access_token";
const ACCESS_TOKEN_EXPIRES_AT_KEY = "weconq_access_token_expires_at";
const PRINCIPAL_ID_KEY = "weconq_principal_id";
const OPEN_ID_KEY = "weconq_openid";
const PROFILE_KEY = "weconq_profile";
const PRODUCT_STATE_KEY = "weconq_product_state";
const NEXT_STEP_KEY = "weconq_next_step";

function hasOwn(target, key) {
  return !!target && Object.prototype.hasOwnProperty.call(target, key);
}

function normalizeText(value, fallback = "") {
  const normalized = String(value || "").trim();
  return normalized || String(fallback || "").trim();
}

function normalizeTimestamp(value) {
  if (value instanceof Date && !Number.isNaN(value.getTime())) {
    return value.toISOString();
  }

  if (typeof value === "number" && Number.isFinite(value)) {
    const date = new Date(value);
    if (!Number.isNaN(date.getTime())) {
      return date.toISOString();
    }
  }

  const normalized = normalizeText(value);
  if (!normalized) {
    return "";
  }

  const timestamp = Date.parse(normalized);
  if (!Number.isNaN(timestamp)) {
    return new Date(timestamp).toISOString();
  }

  return normalized;
}

function normalizeProfile(value) {
  return value && typeof value === "object" && !Array.isArray(value) ? value : null;
}

function normalizeProductState(value) {
  return value && typeof value === "object" && !Array.isArray(value) ? value : null;
}

function createSessionStore(options = {}) {
  const apiBaseUrlKey = normalizeText(options.apiBaseUrlKey, API_BASE_URL_KEY);
  const accessTokenKey = normalizeText(options.accessTokenKey, ACCESS_TOKEN_KEY);
  const accessTokenExpiresAtKey = normalizeText(
    options.accessTokenExpiresAtKey,
    ACCESS_TOKEN_EXPIRES_AT_KEY,
  );
  const principalIdKey = normalizeText(options.principalIdKey, PRINCIPAL_ID_KEY);
  const openIdKey = normalizeText(options.openIdKey, OPEN_ID_KEY);
  const profileKey = normalizeText(options.profileKey, PROFILE_KEY);
  const productStateKey = normalizeText(options.productStateKey, PRODUCT_STATE_KEY);
  const nextStepKey = normalizeText(options.nextStepKey, NEXT_STEP_KEY);
  const defaultApiBaseUrl = normalizeText(options.defaultApiBaseUrl);

  function setOptionalString(key, value) {
    const normalized = normalizeText(value);
    if (normalized) {
      setString(key, normalized);
      return normalized;
    }
    remove(key);
    return "";
  }

  function readSnapshot() {
    return {
      apiBaseUrl: normalizeText(getString(apiBaseUrlKey, defaultApiBaseUrl), defaultApiBaseUrl),
      accessToken: normalizeText(getString(accessTokenKey, "")),
      accessTokenExpiresAt: normalizeText(getString(accessTokenExpiresAtKey, "")),
      principalId: normalizeText(getString(principalIdKey, "")),
      openId: normalizeText(getString(openIdKey, "")),
      profile: normalizeProfile(getJSON(profileKey, null)),
      productState: normalizeProductState(getJSON(productStateKey, null)),
      nextStep: normalizeText(getString(nextStepKey, "none"), "none"),
    };
  }

  return {
    apiBaseUrlKey,
    accessTokenKey,
    accessTokenExpiresAtKey,
    principalIdKey,
    openIdKey,
    profileKey,
    productStateKey,
    nextStepKey,
    defaultApiBaseUrl,
    readApiBaseUrl() {
      return readSnapshot().apiBaseUrl;
    },
    readAccessToken() {
      return readSnapshot().accessToken;
    },
    readAccessTokenExpiresAt() {
      return readSnapshot().accessTokenExpiresAt;
    },
    readPrincipalId() {
      return readSnapshot().principalId;
    },
    readOpenId() {
      return readSnapshot().openId;
    },
    readProfile() {
      return readSnapshot().profile;
    },
    readProductState() {
      return readSnapshot().productState;
    },
    readNextStep() {
      return readSnapshot().nextStep;
    },
    setApiBaseUrl(url) {
      const normalized = normalizeText(url, defaultApiBaseUrl);
      setString(apiBaseUrlKey, normalized);
      return normalized;
    },
    setAccessToken(token) {
      const normalized = normalizeText(token);
      setString(accessTokenKey, normalized);
      return normalized;
    },
    setAccessTokenExpiresAt(value) {
      return setOptionalString(accessTokenExpiresAtKey, normalizeTimestamp(value));
    },
    setPrincipalId(value) {
      return setOptionalString(principalIdKey, value);
    },
    setOpenId(value) {
      return setOptionalString(openIdKey, value);
    },
    setProfile(value) {
      const normalized = normalizeProfile(value);
      if (normalized) {
        setJSON(profileKey, normalized);
        const principalId = normalizeText(
          normalized.principalId || normalized.principal_id || normalized.id,
        );
        if (principalId) {
          setString(principalIdKey, principalId);
        }
        return normalized;
      }
      remove(profileKey);
      return null;
    },
    setProductState(value) {
      const normalized = normalizeProductState(value);
      if (normalized) {
        setJSON(productStateKey, normalized);
        return normalized;
      }
      remove(productStateKey);
      return null;
    },
    setNextStep(value) {
      return setOptionalString(nextStepKey, normalizeText(value, "none"));
    },
    setAuthSession(session = {}) {
      const profile = normalizeProfile(session.profile);
      const productState = normalizeProductState(session.productState || session.product_state);
      const accessToken = normalizeText(session.accessToken || session.token);
      const expiresAt = normalizeTimestamp(
        session.expiresAt || session.expires_at || session.access_token_expires_at,
      );
      const principalId = normalizeText(
        session.principalId ||
          session.principal_id ||
          (profile && (profile.principalId || profile.principal_id || profile.id)),
      );
      // canonical wire field is `openid` (snake); `openId` (camel) kept as legacy fallback
      const openId = normalizeText(session.openid || session.open_id || session.openId);
      const nextStep = normalizeText(session.nextStep || session.next_step, "none");

      setOptionalString(accessTokenKey, accessToken);
      setOptionalString(accessTokenExpiresAtKey, expiresAt);
      setOptionalString(principalIdKey, principalId);
      setOptionalString(openIdKey, openId);
      setOptionalString(nextStepKey, nextStep);

      if (profile || hasOwn(session, "profile")) {
        this.setProfile(profile);
      }
      if (productState || hasOwn(session, "productState") || hasOwn(session, "product_state")) {
        this.setProductState(productState);
      }

      return readSnapshot();
    },
    clearAuthSession() {
      remove(accessTokenKey);
      remove(accessTokenExpiresAtKey);
      remove(principalIdKey);
      remove(openIdKey);
      remove(profileKey);
      remove(productStateKey);
      remove(nextStepKey);
    },
    isAccessTokenValid(bufferMs = 30 * 1000) {
      const snapshot = readSnapshot();
      if (!snapshot.accessToken) {
        return false;
      }
      if (!snapshot.accessTokenExpiresAt) {
        // Conservative: missing expiry means we cannot confirm validity.
        // Force a verify/login pass rather than treating token as perpetually
        // fresh. Prior behaviour (return true) masked stale tokens that had
        // no expiry stored — e.g. setAccessToken("") wrote expiresAt:"".
        return false;
      }
      const expiresAt = Date.parse(snapshot.accessTokenExpiresAt);
      if (Number.isNaN(expiresAt)) {
        // Malformed expiry string — fail closed and trigger re-verification.
        return false;
      }
      return expiresAt - Math.max(0, Number(bufferMs || 0)) > Date.now();
    },
    clearSession() {
      remove(apiBaseUrlKey);
      this.clearAuthSession();
    },
  };
}

function bootstrapSession(options = {}) {
  const store = createSessionStore(options);
  return {
    apiBaseUrl: store.readApiBaseUrl(),
    accessToken: store.readAccessToken(),
    accessTokenExpiresAt: store.readAccessTokenExpiresAt(),
    principalId: store.readPrincipalId(),
    openId: store.readOpenId(),
    profile: store.readProfile(),
    productState: store.readProductState(),
    nextStep: store.readNextStep(),
    setApiBaseUrl(url) {
      return store.setApiBaseUrl(url);
    },
    setAccessToken(token) {
      return store.setAccessToken(token);
    },
    setAuthSession(session) {
      return store.setAuthSession(session);
    },
    setProfile(profile) {
      return store.setProfile(profile);
    },
    setProductState(productState) {
      return store.setProductState(productState);
    },
    setNextStep(nextStep) {
      return store.setNextStep(nextStep);
    },
    clearAuthSession() {
      store.clearAuthSession();
    },
    clearSession() {
      store.clearSession();
    },
  };
}

module.exports = {
  ACCESS_TOKEN_KEY,
  ACCESS_TOKEN_EXPIRES_AT_KEY,
  API_BASE_URL_KEY,
  NEXT_STEP_KEY,
  OPEN_ID_KEY,
  PRINCIPAL_ID_KEY,
  PROFILE_KEY,
  PRODUCT_STATE_KEY,
  bootstrapSession,
  createSessionStore,
};
