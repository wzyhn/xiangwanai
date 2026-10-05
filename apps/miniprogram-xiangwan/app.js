"use strict";

const { createSessionStore } = require("mp-auth-session");
const { bootstrapApp } = require("mp-init");
const { getMiniProgramConfig, lintProductCode } = require("mp-product-registry");
const { createWeconqAuthRuntime } = require("mp-weconq-auth-runtime");
const { isXiangwanProfile, projectMyProfile } = require("./features/profile/model");
const { xiangwanApi } = require("./services/xiangwan-api");
const {
  findPublishedPolicyVersion,
  contactCollectionPolicyBlockMessage,
} = require("./features/registration/model");
const { createXiangwanError } = require("./utils/errors");
const {
  isPlaceholderAppId,
  isWechatMiniProgramAppId,
  readMiniProgramEnvVersion,
  resolveDefaultApiBaseUrl,
  resolveMiniProgramAppId,
} = require("./utils/runtime-config");
const { createSessionStoreOptions } = require("./utils/session-options");

const PRODUCT_CODE = "wq-xiangwan";
const PRODUCT_NAME = "享玩 AI";
const PRODUCT_CONFIG = getMiniProgramConfig(PRODUCT_CODE) || {};
const REGISTRATION_CONTACT_PHONE_PATTERN = /^\+[1-9][0-9]{7,14}$/;

lintProductCode(PRODUCT_CODE);

function cloneMemoryValue(value) {
  if (!value || typeof value !== "object") return null;
  try {
    return JSON.parse(JSON.stringify(value));
  } catch (_error) {
    return null;
  }
}

// The registration form normalizes manual phone input before this cache is
// written. Keep a second guard here because this value is sensitive and must
// never become an unvalidated generic app slot. The cache is deliberately
// process-local; it is only a convenience for another booking during the
// current authenticated app lifetime. Persistent defaults are server-owned.
function normalizeRegistrationContactDefaults(value) {
  if (!value || typeof value !== "object" || Array.isArray(value)) return null;
  const contactName = String(value.contactName || "").trim();
  const contactPhone = String(value.contactPhone || "").trim();
  if (
    !contactName ||
    Array.from(contactName).length > 100 ||
    !REGISTRATION_CONTACT_PHONE_PATTERN.test(contactPhone)
  ) {
    return null;
  }
  return { contactName, contactPhone };
}

function normalizedPrincipalIdValue(value) {
  return String(value || "")
    .trim()
    .toLowerCase();
}

function normalizedPrincipalId(app) {
  return normalizedPrincipalIdValue(app && app.globalData && app.globalData.principalId);
}

// Xiangwan's profile is kept in the same global slot that the shared auth
// runtime uses for its platform profile. Keep a separate in-memory owner
// binding so a profile from principal A can never be rendered for principal B
// while B's own profile request is pending or has failed. The binding is
// intentionally process-local; the profile itself is never persisted.
function readOwnedXiangwanProfile(app) {
  if (!app || !app.globalData) return null;
  const principalId = normalizedPrincipalId(app);
  const ownerPrincipalId = String(app._xiangwanProfileOwnerPrincipalId || "")
    .trim()
    .toLowerCase();
  const profile = app.globalData.profile;
  if (
    !principalId ||
    !ownerPrincipalId ||
    ownerPrincipalId !== principalId ||
    !isXiangwanProfile(profile)
  ) {
    return null;
  }
  return profile;
}

// Registration drafts and success receipts live in app memory only. Bind both
// to the current authenticated principal and an in-process auth generation so
// a response or page retained across logout/login cannot be presented to the
// next account. The generation is deliberately derived from principal
// transitions; token refreshes for the same principal do not discard a form.
function syncXiangwanAuthGeneration(app) {
  const principalId = normalizedPrincipalId(app);
  const previousPrincipalId = String(app._xiangwanAuthPrincipalId || "")
    .trim()
    .toLowerCase();
  if (!Number.isSafeInteger(Number(app._xiangwanAuthGeneration))) {
    app._xiangwanAuthGeneration = 0;
  }
  if (principalId !== previousPrincipalId) {
    app._xiangwanAuthGeneration += 1;
    app._xiangwanAuthPrincipalId = principalId;
    // Drop any sensitive in-memory contact convenience value as soon as the
    // authenticated principal changes, including logout (empty principal).
    app._registrationContactDefaults = null;
    app._registrationContactLoadPromise = null;
    app._profileExtensionOperation = null;
  }
  return {
    principalId,
    generation: app._xiangwanAuthGeneration,
  };
}

// Auth runtime session changes are the only reliable signal for a logout
// followed by a login as the same principal. Clear all in-memory registration
// handoff values at that boundary instead of waiting for a page accessor to
// notice a principal id change. Bump the profile generation as well so an
// in-flight read from the previous session cannot populate the new session.
function handleXiangwanAuthSessionChange(app, session) {
  const principalId = normalizedPrincipalIdValue(session && session.principalId);
  const previousPrincipalId = String(app._xiangwanAuthPrincipalId || "")
    .trim()
    .toLowerCase();
  if (!Number.isSafeInteger(Number(app._xiangwanAuthGeneration))) {
    app._xiangwanAuthGeneration = 0;
  }
  if (principalId === previousPrincipalId) return;
  if (app._contactSetupPending) {
    const pending = app._contactSetupPending;
    app._contactSetupPending = null;
    pending.reject(
      createXiangwanError("contact_setup_auth_changed", "登录状态已变化，请重新填写联系信息"),
    );
  }
  app._xiangwanAuthGeneration += 1;
  app._xiangwanAuthPrincipalId = principalId;
  app._registrationContactDefaults = null;
  app._registrationContactLoadPromise = null;
  app._profileExtensionOperation = null;
  app._registrationDraft = null;
  app._registrationReceipt = null;
  app._registrationConflictSessionId = "";
  app._xiangwanProfileOwnerPrincipalId = "";
  app._xiangwanProfileGeneration = Number(app._xiangwanProfileGeneration || 0) + 1;
  app._xiangwanProfilePromise = null;
  app._xiangwanProfilePrincipalId = "";
}

function initializeXiangwanRuntime(app) {
  if (app._runtimePromise) return app._runtimePromise;

  let resolveRuntime;
  let rejectRuntime;
  const runtimePromise = new Promise((resolve, reject) => {
    resolveRuntime = resolve;
    rejectRuntime = reject;
  });
  app._runtimePromise = runtimePromise;

  try {
    // Page.onLoad can run immediately after App.onLaunch returns. Publish the
    // checked-in host synchronously so an anonymous page never races the auth
    // hydration microtask and dispatches with an empty base URL.
    const envVersion = readMiniProgramEnvVersion();
    const defaultApiBaseUrl = resolveDefaultApiBaseUrl(envVersion);
    const storeOptions = createSessionStoreOptions(defaultApiBaseUrl);
    const boot = bootstrapApp(storeOptions);
    // The checked-in environment contract is authoritative. Never inherit a
    // stale host (especially another product's API) from local storage.
    if (String(boot.apiBaseUrl || "").trim() !== defaultApiBaseUrl) {
      boot.clearAuthSession();
    }
    boot.setApiBaseUrl(defaultApiBaseUrl);
    const sessionStore = createSessionStore(storeOptions);

    app.xiangwanSessionStore = sessionStore;
    app.globalData.miniProgramEnvVersion = envVersion;
    app.globalData.defaultApiBaseUrl = defaultApiBaseUrl;
    app.globalData.apiBaseUrl = defaultApiBaseUrl;
    app.globalData.accessToken = sessionStore.readAccessToken();

    app.weconqAuth = createWeconqAuthRuntime({
      app,
      sessionStore,
      appId: () => resolveMiniProgramAppId(PRODUCT_CONFIG.appId),
      baseUrl: () => app.globalData.apiBaseUrl,
      defaultApiBaseUrl,
      allowDevLogin: false,
      productName: PRODUCT_NAME,
      onSessionChange: (session) => handleXiangwanAuthSessionChange(app, session),
    }).install(app);

    app._authHydrationPromise = app.weconqAuth
      .hydrate({ verify: false, refreshProfile: false })
      .catch(() => app.weconqAuth.getSession())
      .then((session) => {
        // 享玩自有资料(昵称/头像),与平台 auth 管道的 refreshProfile 开关无关;
        // 静默拉取,永不阻塞启动
        if (typeof app.refreshXiangwanProfile === "function") {
          app.refreshXiangwanProfile().catch(() => null);
        }
        return session;
      });
    app._authHydrationPromise.then(
      () => resolveRuntime(app),
      (error) => {
        if (app._runtimePromise === runtimePromise) app._runtimePromise = null;
        rejectRuntime(error);
      },
    );
  } catch (error) {
    if (app._runtimePromise === runtimePromise) app._runtimePromise = null;
    rejectRuntime(error);
  }

  return runtimePromise;
}

function createXiangwanAppDefinition() {
  return {
    onLaunch() {
      this.runtimeReady = initializeXiangwanRuntime(this).catch(() => false);
    },

    onShow() {
      if (!this._runtimePromise) {
        this.runtimeReady = initializeXiangwanRuntime(this).catch(() => false);
      }
    },

    globalData: {
      apiBaseUrl: "",
      defaultApiBaseUrl: "",
      miniProgramEnvVersion: "develop",
      accessToken: "",
      accessTokenExpiresAt: "",
      principalId: "",
      openId: "",
      profile: null,
      productState: null,
      nextStep: "none",
      weconqProductCode: PRODUCT_CODE,
      weconqProductName: PRODUCT_NAME,
    },

    async ensureAuthenticated() {
      await (this.runtimeReady || initializeXiangwanRuntime(this));
      if (!this.weconqAuth || !String(this.globalData.apiBaseUrl || "").trim()) {
        throw createXiangwanError("runtime_not_configured", "享玩服务地址尚未配置，暂时不能登录");
      }
      const appId = resolveMiniProgramAppId(PRODUCT_CONFIG.appId);
      if (isPlaceholderAppId(appId) || !isWechatMiniProgramAppId(appId)) {
        throw createXiangwanError("app_id_not_configured", "享玩微信 AppID 尚未配置，暂时不能登录");
      }
      const session = await this.weconqAuth.ensureSession({
        refreshProfile: false,
      });
      // 登录成功后在后台拉取享玩自有资料。资料只用于头像/昵称展示，不能
      // 阻塞“登录后立即填写联系人”的报名路径；refresh 内部仍按 principal
      // 与 generation 做代际校验，且失败静默，不影响登录结果。
      if (typeof this.refreshXiangwanProfile === "function") {
        void Promise.resolve(this.refreshXiangwanProfile()).catch(() => null);
      }
      // Collect contact details immediately after WeChat login, including
      // logins started outside the registration page. The form checks the
      // published privacy/contact policy before rendering sensitive inputs.
      if (typeof wx !== "undefined") await this.ensureContactSetup();
      return session;
    },

    async ensureContactSetup(force = false) {
      if (!force && this.getRegistrationContactDefaults()) return;
      const binding = syncXiangwanAuthGeneration(this);
      if (!binding.principalId || !this.weconqAuth || !this.weconqAuth.hasValidSession()) {
        throw createXiangwanError("contact_setup_auth_required", "请先完成微信登录");
      }
      if (!force) {
        if (!this._registrationContactLoadPromise) {
          const request = (async () => {
            const saved = await xiangwanApi.getRegistrationContact();
            if (!saved.configured) return;
            const policies = await xiangwanApi.getPublicPolicies();
            const current = syncXiangwanAuthGeneration(this);
            if (
              current.principalId !== binding.principalId ||
              current.generation !== binding.generation
            )
              return;
            if (
              saved.privacy_policy_version !== findPublishedPolicyVersion(policies, "privacy") ||
              saved.contact_policy_version !==
                findPublishedPolicyVersion(policies, "manual_contact") ||
              contactCollectionPolicyBlockMessage(
                policies,
                typeof wx.openPrivacyContract === "function",
              )
            )
              return;
            this.setRegistrationContactDefaults({
              contactName: saved.nickname,
              contactPhone: saved.phone_e164,
            });
          })();
          this._registrationContactLoadPromise = request;
          void request
            .finally(() => {
              if (this._registrationContactLoadPromise === request)
                this._registrationContactLoadPromise = null;
            })
            .catch(() => null);
        }
        try {
          await this._registrationContactLoadPromise;
        } catch (_error) {
          /* setup retries the authoritative read */
        }
      }
      const current = syncXiangwanAuthGeneration(this);
      if (
        current.principalId !== binding.principalId ||
        current.generation !== binding.generation
      ) {
        throw createXiangwanError("contact_setup_auth_changed", "登录状态已变化，请重新登录");
      }
      if (!force && this.getRegistrationContactDefaults()) return;
      return this.openContactSetup();
    },

    openContactSetup() {
      const binding = syncXiangwanAuthGeneration(this);
      if (!binding.principalId || !this.weconqAuth || !this.weconqAuth.hasValidSession()) {
        return Promise.reject(
          createXiangwanError("contact_setup_auth_required", "请先完成微信登录"),
        );
      }
      const pending = this._contactSetupPending;
      if (
        pending &&
        pending.binding.principalId === binding.principalId &&
        pending.binding.generation === binding.generation
      )
        return pending.promise;
      if (typeof wx === "undefined" || typeof wx.navigateTo !== "function") {
        return Promise.reject(
          createXiangwanError(
            "contact_setup_navigation_unavailable",
            "信息收集页暂不可用，请稍后重试",
          ),
        );
      }
      let resolve;
      let reject;
      const promise = new Promise((done, fail) => {
        resolve = done;
        reject = fail;
      });
      const id = Number(this._contactSetupSequence || 0) + 1;
      this._contactSetupSequence = id;
      const setup = { id, binding, promise, resolve, reject };
      this._contactSetupPending = setup;
      try {
        wx.navigateTo({
          url: `/pages/contact-setup/index?setup_id=${id}`,
          fail: () => {
            if (this._contactSetupPending !== setup) return;
            this._contactSetupPending = null;
            reject(
              createXiangwanError("contact_setup_navigation_failed", "信息收集页打开失败，请重试"),
            );
          },
        });
      } catch (_error) {
        this._contactSetupPending = null;
        reject(
          createXiangwanError("contact_setup_navigation_failed", "信息收集页打开失败，请重试"),
        );
      }
      return promise;
    },

    getContactSetupId() {
      return this._contactSetupPending ? this._contactSetupPending.id : null;
    },

    completeContactSetup(value, expectedId) {
      const pending = this._contactSetupPending;
      if (!pending || pending.id !== expectedId) return false;
      const binding = syncXiangwanAuthGeneration(this);
      if (
        binding.principalId !== pending.binding.principalId ||
        binding.generation !== pending.binding.generation ||
        !this.weconqAuth ||
        !this.weconqAuth.hasValidSession()
      )
        return false;
      if (!this.setRegistrationContactDefaults(value)) return false;
      this._contactSetupPending = null;
      pending.resolve();
      return true;
    },

    cancelContactSetup(expectedId) {
      const pending = this._contactSetupPending;
      if (!pending || pending.id !== expectedId) return;
      this._contactSetupPending = null;
      pending.reject(createXiangwanError("contact_setup_incomplete", "请填写昵称和手机号后继续"));
    },

    // 享玩个人资料({nickname, avatarUrl, etag})独立于平台 auth 管道写入的
    // globalData.profile;供“我的”与资料编辑页展示。未登录或拉取失败返回 null。
    getXiangwanProfile() {
      return readOwnedXiangwanProfile(this);
    },

    setXiangwanProfile(value, ownerPrincipalId = normalizedPrincipalId(this)) {
      const currentPrincipalId = normalizedPrincipalId(this);
      const owner = String(ownerPrincipalId || "")
        .trim()
        .toLowerCase();
      if (
        !currentPrincipalId ||
        !owner ||
        owner !== currentPrincipalId ||
        !isXiangwanProfile(value)
      ) {
        return null;
      }
      this.globalData.profile = value;
      this._xiangwanProfileOwnerPrincipalId = owner;
      return value;
    },

    async refreshXiangwanProfile() {
      const principalAtStart = String((this.globalData && this.globalData.principalId) || "")
        .trim()
        .toLowerCase();
      // A principal transition can happen before the new profile request is
      // started. Drop the previous owner binding so all page reads fail closed
      // immediately, including when the request later rejects.
      if (
        this._xiangwanProfileOwnerPrincipalId &&
        this._xiangwanProfileOwnerPrincipalId !== principalAtStart
      ) {
        this._xiangwanProfileOwnerPrincipalId = "";
      }
      if (this._xiangwanProfilePromise && this._xiangwanProfilePrincipalId === principalAtStart) {
        return this._xiangwanProfilePromise;
      }
      const refreshPromise = (async () => {
        try {
          const authenticated = Boolean(
            this.weconqAuth &&
              typeof this.weconqAuth.hasValidSession === "function" &&
              this.weconqAuth.hasValidSession(),
          );
          const apiBaseUrl = String((this.globalData && this.globalData.apiBaseUrl) || "").trim();
          if (!authenticated || !apiBaseUrl) return null;
          const mutationGeneration = Number(this._xiangwanProfileGeneration || 0);
          const profile = projectMyProfile(await xiangwanApi.getMyProfile(), apiBaseUrl);
          const principalAtEnd = String((this.globalData && this.globalData.principalId) || "")
            .trim()
            .toLowerCase();
          if (
            mutationGeneration !== Number(this._xiangwanProfileGeneration || 0) ||
            principalAtStart !== principalAtEnd
          ) {
            return this.getXiangwanProfile();
          }
          return this.setXiangwanProfile(profile, principalAtStart);
        } catch (_error) {
          return this.getXiangwanProfile();
        }
      })();
      this._xiangwanProfilePromise = refreshPromise;
      this._xiangwanProfilePrincipalId = principalAtStart;
      try {
        return await refreshPromise;
      } finally {
        if (this._xiangwanProfilePromise === refreshPromise) this._xiangwanProfilePromise = null;
        if (this._xiangwanProfilePromise === null) this._xiangwanProfilePrincipalId = "";
      }
    },

    markXiangwanProfileMutation() {
      this._xiangwanProfileGeneration = Number(this._xiangwanProfileGeneration || 0) + 1;
      // Any in-flight read was started before this mutation and must not be
      // reused as the etag source for the next write.
      this._xiangwanProfilePromise = null;
      return this._xiangwanProfileGeneration;
    },

    resetRejectedSession() {
      if (this.weconqAuth) {
        this.weconqAuth.clearAuthSession("server-auth-rejected");
      }
    },

    getXiangwanAuthBinding() {
      return syncXiangwanAuthGeneration(this);
    },

    setRegistrationDraft(value) {
      const binding = syncXiangwanAuthGeneration(this);
      if (!binding.principalId) {
        this._registrationDraft = null;
        return null;
      }
      const draft = cloneMemoryValue(value);
      if (!draft) {
        this._registrationDraft = null;
        return null;
      }
      this._registrationDraft = {
        ...draft,
        principalId: binding.principalId,
        authGeneration: binding.generation,
      };
      return this.getRegistrationDraft();
    },

    getRegistrationDraft() {
      const binding = syncXiangwanAuthGeneration(this);
      const draft = cloneMemoryValue(this._registrationDraft);
      if (
        !draft ||
        String(draft.principalId || "")
          .trim()
          .toLowerCase() !== binding.principalId ||
        Number(draft.authGeneration) !== binding.generation
      ) {
        if (this._registrationDraft) this._registrationDraft = null;
        return null;
      }
      delete draft.principalId;
      delete draft.authGeneration;
      return draft;
    },

    clearRegistrationDraft() {
      this._registrationDraft = null;
    },

    // The server owns persistent defaults. This cache is process-local and
    // auth-bound; no phone is written to WeChat storage. Registrations still
    // submit independent snapshots that users can edit before confirmation.
    setRegistrationContactDefaults(value) {
      const binding = syncXiangwanAuthGeneration(this);
      const defaults = normalizeRegistrationContactDefaults(value);
      if (!binding.principalId || !defaults) {
        this._registrationContactDefaults = null;
        return null;
      }
      this._registrationContactDefaults = {
        ...defaults,
        principalId: binding.principalId,
        authGeneration: binding.generation,
      };
      return this.getRegistrationContactDefaults();
    },

    getRegistrationContactDefaults() {
      const binding = syncXiangwanAuthGeneration(this);
      const defaults = cloneMemoryValue(this._registrationContactDefaults);
      if (
        !defaults ||
        String(defaults.principalId || "")
          .trim()
          .toLowerCase() !== binding.principalId ||
        Number(defaults.authGeneration) !== binding.generation
      ) {
        if (this._registrationContactDefaults) this._registrationContactDefaults = null;
        return null;
      }
      delete defaults.principalId;
      delete defaults.authGeneration;
      const profile = this.getXiangwanProfile();
      if (profile && profile.nickname) defaults.contactName = profile.nickname;
      return normalizeRegistrationContactDefaults(defaults);
    },

    clearRegistrationContactDefaults() {
      this._registrationContactDefaults = null;
    },

    setProfileExtensionOperation(value) {
      const binding = syncXiangwanAuthGeneration(this);
      const copy = cloneMemoryValue(value);
      if (!binding.principalId || !copy || !copy.key || !copy.fingerprint || !copy.payload)
        return null;
      this._profileExtensionOperation = {
        ...copy,
        principalId: binding.principalId,
        authGeneration: binding.generation,
      };
      return this.getProfileExtensionOperation();
    },

    getProfileExtensionOperation() {
      const binding = syncXiangwanAuthGeneration(this);
      const copy = cloneMemoryValue(this._profileExtensionOperation);
      if (
        !copy ||
        copy.principalId !== binding.principalId ||
        copy.authGeneration !== binding.generation
      ) {
        this._profileExtensionOperation = null;
        return null;
      }
      delete copy.principalId;
      delete copy.authGeneration;
      return copy;
    },

    clearProfileExtensionOperation() {
      this._profileExtensionOperation = null;
    },

    markRegistrationConflict(sessionId) {
      this._registrationConflictSessionId = String(sessionId || "")
        .trim()
        .toLowerCase();
    },

    consumeRegistrationConflict(sessionId) {
      const expected = String(sessionId || "")
        .trim()
        .toLowerCase();
      const matches = Boolean(expected && this._registrationConflictSessionId === expected);
      if (matches) this._registrationConflictSessionId = "";
      return matches;
    },

    setRegistrationReceipt(value) {
      const binding = syncXiangwanAuthGeneration(this);
      if (!binding.principalId) {
        this._registrationReceipt = null;
        return null;
      }
      const receipt = cloneMemoryValue(value);
      if (!receipt) {
        this._registrationReceipt = null;
        return null;
      }
      this._registrationReceipt = {
        ...receipt,
        principalId: binding.principalId,
        authGeneration: binding.generation,
      };
      return undefined;
    },

    consumeRegistrationReceipt() {
      const binding = syncXiangwanAuthGeneration(this);
      const value = cloneMemoryValue(this._registrationReceipt);
      this._registrationReceipt = null;
      if (
        !value ||
        String(value.principalId || "")
          .trim()
          .toLowerCase() !== binding.principalId ||
        Number(value.authGeneration) !== binding.generation
      ) {
        return null;
      }
      delete value.principalId;
      delete value.authGeneration;
      return value;
    },
  };
}

if (typeof App === "function") {
  App(createXiangwanAppDefinition());
}

module.exports = {
  PRODUCT_CODE,
  PRODUCT_NAME,
  createXiangwanAppDefinition,
  handleXiangwanAuthSessionChange,
  initializeXiangwanRuntime,
  normalizeRegistrationContactDefaults,
};
