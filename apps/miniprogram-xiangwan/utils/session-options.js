"use strict";

const SESSION_STORAGE_KEY_PREFIX = "xiangwan_session_v1_";

function createSessionStoreOptions(defaultApiBaseUrl = "") {
  return {
    defaultApiBaseUrl: String(defaultApiBaseUrl || "").trim(),
    apiBaseUrlKey: `${SESSION_STORAGE_KEY_PREFIX}api_base_url`,
    accessTokenKey: `${SESSION_STORAGE_KEY_PREFIX}access_token`,
    accessTokenExpiresAtKey: `${SESSION_STORAGE_KEY_PREFIX}access_token_expires_at`,
    principalIdKey: `${SESSION_STORAGE_KEY_PREFIX}principal_id`,
    openIdKey: `${SESSION_STORAGE_KEY_PREFIX}openid`,
    profileKey: `${SESSION_STORAGE_KEY_PREFIX}profile`,
    productStateKey: `${SESSION_STORAGE_KEY_PREFIX}product_state`,
    nextStepKey: `${SESSION_STORAGE_KEY_PREFIX}next_step`,
  };
}

module.exports = {
  SESSION_STORAGE_KEY_PREFIX,
  createSessionStoreOptions,
};
