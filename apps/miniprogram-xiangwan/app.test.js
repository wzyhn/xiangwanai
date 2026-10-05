"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const {
  PRODUCT_CODE,
  PRODUCT_NAME,
  createXiangwanAppDefinition,
  handleXiangwanAuthSessionChange,
  initializeXiangwanRuntime,
} = require("./app");
const { xiangwanApi } = require("./services/xiangwan-api");

test("returning login restores saved contact without reopening setup or writing phone to storage", async (t) => {
  const original = {
    get: xiangwanApi.getRegistrationContact,
    policies: xiangwanApi.getPublicPolicies,
  };
  const urls = [];
  global.wx = {
    navigateTo: ({ url }) => urls.push(url),
    openPrivacyContract() {},
    setStorageSync() {
      throw new Error("phone must not be stored locally");
    },
  };
  t.after(() => {
    xiangwanApi.getRegistrationContact = original.get;
    xiangwanApi.getPublicPolicies = original.policies;
    delete global.wx;
  });
  xiangwanApi.getRegistrationContact = async () => ({
    configured: true,
    nickname: "测试用户",
    phone_e164: "+8613800000000",
    privacy_policy_version: "privacy-v1",
    contact_policy_version: "contact-v1",
  });
  xiangwanApi.getPublicPolicies = async () => ({
    published_versions: [
      { kind: "privacy", version: "privacy-v1" },
      { kind: "manual_contact", version: "contact-v1" },
    ],
    capabilities: { privacy_notice_available: true, manual_registration_contact_available: true },
    manual_registration_contact_policy: { version: "contact-v1", content: "用于活动联系" },
  });
  const app = createXiangwanAppDefinition();
  app.globalData.principalId = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa";
  app.weconqAuth = { hasValidSession: () => true };
  await app.ensureContactSetup();
  assert.deepEqual(urls, []);
  assert.deepEqual(app.getRegistrationContactDefaults(), {
    contactName: "测试用户",
    contactPhone: "+8613800000000",
  });
});

test("an old saved-contact request cannot populate a switched account", async (t) => {
  const original = xiangwanApi.getRegistrationContact;
  let release;
  xiangwanApi.getRegistrationContact = () =>
    new Promise((resolve) => {
      release = resolve;
    });
  global.wx = {
    navigateTo() {
      throw new Error("stale setup must not open");
    },
  };
  t.after(() => {
    xiangwanApi.getRegistrationContact = original;
    delete global.wx;
  });
  const app = createXiangwanAppDefinition();
  app.globalData.principalId = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa";
  app.weconqAuth = { hasValidSession: () => true };
  const pending = app.ensureContactSetup();
  app.globalData.principalId = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb";
  release({ configured: false });
  await assert.rejects(pending, /登录状态已变化/);
  assert.equal(app.getRegistrationContactDefaults(), null);
});

test("app definition owns the Xiangwan identity and memory-only registration handoff", () => {
  const app = createXiangwanAppDefinition();
  app._registrationDraft = null;
  app.globalData.principalId = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa";
  const draft = { sessionId: "session", payload: { contact: { name: "甲" } } };
  app.setRegistrationDraft(draft);
  draft.payload.contact.name = "乙";
  assert.equal(app.getRegistrationDraft().payload.contact.name, "甲");
  assert.equal(app.getRegistrationDraft().principalId, undefined);
  app.globalData.principalId = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb";
  assert.equal(app.getRegistrationDraft(), null);
  app.globalData.principalId = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa";
  assert.equal(app.getRegistrationDraft(), null);
  app.clearRegistrationDraft();
  assert.equal(app.getRegistrationDraft(), null);
  app.markRegistrationConflict("SESSION");
  assert.equal(app.consumeRegistrationConflict("session"), true);
  assert.equal(app.consumeRegistrationConflict("session"), false);
  assert.equal(app.globalData.weconqProductCode, PRODUCT_CODE);
  assert.equal(PRODUCT_CODE, "wq-xiangwan");
  assert.equal(PRODUCT_NAME, "享玩 AI");
});

test("ensureAuthenticated returns after auth without waiting for the optional profile read", async () => {
  const app = createXiangwanAppDefinition();
  app.globalData.apiBaseUrl = "https://api.weconq.cn";
  app.runtimeReady = Promise.resolve();
  app.weconqAuth = {
    async ensureSession() {
      return { principalId: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" };
    },
  };

  let releaseProfile;
  app.refreshXiangwanProfile = () =>
    new Promise((resolve) => {
      releaseProfile = resolve;
    });

  const session = await app.ensureAuthenticated();
  assert.deepEqual(session, { principalId: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa" });
  assert.equal(typeof releaseProfile, "function");

  releaseProfile(null);
});

test("WeChat login opens one immediate contact setup and registration reuses its memory-only values", async (t) => {
  const urls = [];
  global.wx = { navigateTo: ({ url }) => urls.push(url) };
  t.after(() => {
    delete global.wx;
  });
  const app = createXiangwanAppDefinition();
  const principalId = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa";
  app.globalData.apiBaseUrl = "https://api.weconq.cn";
  app.runtimeReady = Promise.resolve();
  app.weconqAuth = {
    hasValidSession: () => true,
    async ensureSession() {
      app.globalData.principalId = principalId;
      return { principalId };
    },
  };
  app.refreshXiangwanProfile = async () => null;

  const first = app.ensureAuthenticated();
  const second = app.ensureAuthenticated();
  await new Promise((resolve) => setImmediate(resolve));
  assert.deepEqual(urls, ["/pages/contact-setup/index?setup_id=1"]);
  assert.equal(
    app.completeContactSetup({ contactName: "小鱼", contactPhone: "+8613812345678" }, 1),
    true,
  );
  assert.deepEqual(await first, { principalId });
  assert.deepEqual(await second, { principalId });
  assert.deepEqual(app.getRegistrationContactDefaults(), {
    contactName: "小鱼",
    contactPhone: "+8613812345678",
  });
  await app.ensureAuthenticated();
  assert.equal(urls.length, 1);
});

test("cancelled contact setup cannot unlock an authenticated action or leak into a new session", async (t) => {
  global.wx = { navigateTo: () => {} };
  t.after(() => {
    delete global.wx;
  });
  const app = createXiangwanAppDefinition();
  app.globalData.apiBaseUrl = "https://api.weconq.cn";
  app.runtimeReady = Promise.resolve();
  app.weconqAuth = {
    hasValidSession: () => true,
    async ensureSession() {
      app.globalData.principalId = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa";
      return { principalId: app.globalData.principalId };
    },
  };
  app.refreshXiangwanProfile = async () => null;
  const login = app.ensureAuthenticated();
  await new Promise((resolve) => setImmediate(resolve));
  app.cancelContactSetup(1);
  await assert.rejects(login, /请填写昵称和手机号/);
  assert.equal(app.getRegistrationContactDefaults(), null);
  const later = app.ensureAuthenticated();
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(app.getContactSetupId(), 2);
  app.cancelContactSetup(1);
  assert.equal(app.getContactSetupId(), 2, "a stale page cannot cancel its replacement");
  handleXiangwanAuthSessionChange(app, { principalId: "" });
  await assert.rejects(later, /登录状态已变化/);
  assert.equal(app.getRegistrationContactDefaults(), null);
});

test("registration contact defaults are reusable only for the same principal and auth generation", () => {
  const app = createXiangwanAppDefinition();
  const firstPrincipal = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa";
  const secondPrincipal = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb";
  const defaults = {
    contactName: "王薇",
    contactPhone: "+8613812345678",
  };

  app.globalData.principalId = firstPrincipal;
  assert.deepEqual(app.setRegistrationContactDefaults(defaults), defaults);
  const copy = app.getRegistrationContactDefaults();
  assert.deepEqual(copy, defaults);
  copy.contactPhone = "+8613900000000";
  assert.deepEqual(app.getRegistrationContactDefaults(), defaults);

  app.globalData.principalId = secondPrincipal;
  assert.equal(app.getRegistrationContactDefaults(), null);
  app.globalData.principalId = firstPrincipal;
  assert.equal(app.getRegistrationContactDefaults(), null);

  app.globalData.principalId = secondPrincipal;
  assert.equal(
    app.setRegistrationContactDefaults({
      contactName: "王薇",
      contactPhone: "13812345678",
    }),
    null,
    "the app cache accepts only the already-normalized E.164 phone snapshot",
  );
  assert.deepEqual(app.setRegistrationContactDefaults(defaults), defaults);
  app._xiangwanAuthGeneration += 1;
  assert.equal(
    app.getRegistrationContactDefaults(),
    null,
    "a newer auth generation cannot reuse an older contact cache",
  );
  app.globalData.principalId = "";
  assert.equal(app.getRegistrationContactDefaults(), null);
});

test("unknown optional profile writes keep their key in session memory and clear on logout", () => {
  const app = createXiangwanAppDefinition();
  const principalId = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa";
  app.globalData.principalId = principalId;
  app.getXiangwanAuthBinding();
  const value = {
    key: "33333333-3333-4333-8333-333333333333",
    fingerprint: "draft",
    payload: { introduction: "简介" },
  };
  assert.deepEqual(app.setProfileExtensionOperation(value), value);
  value.payload.introduction = "changed";
  assert.equal(app.getProfileExtensionOperation().payload.introduction, "简介");
  handleXiangwanAuthSessionChange(app, { principalId: "" });
  assert.equal(app.getProfileExtensionOperation(), null);
});

test("registration receipts are isolated across the auth generation", () => {
  const app = createXiangwanAppDefinition();
  app.globalData.principalId = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa";
  app.setRegistrationReceipt({ registration_id: "registration-a" });
  assert.deepEqual(app.consumeRegistrationReceipt(), { registration_id: "registration-a" });

  app.setRegistrationReceipt({ registration_id: "registration-b" });
  app.globalData.principalId = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb";
  assert.equal(app.consumeRegistrationReceipt(), null);
});

test("logout and same-principal relogin clear sensitive registration handoffs", () => {
  const app = createXiangwanAppDefinition();
  const principalId = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa";
  app.globalData.principalId = principalId;
  app.getXiangwanAuthBinding();
  app.setRegistrationContactDefaults({
    contactName: "王薇",
    contactPhone: "+8613812345678",
  });
  app.setRegistrationDraft({ sessionId: "session", payload: { contact: { name: "王薇" } } });
  app.setRegistrationReceipt({ registration_id: "registration-a" });
  app.markRegistrationConflict("session");

  const generationBeforeLogout = app._xiangwanAuthGeneration;
  handleXiangwanAuthSessionChange(app, { principalId: "" });
  assert.equal(app._xiangwanAuthGeneration, generationBeforeLogout + 1);
  assert.equal(app.getRegistrationContactDefaults(), null);
  assert.equal(app.getRegistrationDraft(), null);
  assert.equal(app.consumeRegistrationReceipt(), null);
  assert.equal(app.consumeRegistrationConflict("session"), false);

  handleXiangwanAuthSessionChange(app, { principalId });
  const generationAfterRelogin = app._xiangwanAuthGeneration;
  assert.equal(generationAfterRelogin, generationBeforeLogout + 2);
  app.setRegistrationContactDefaults({
    contactName: "李宁",
    contactPhone: "+8613900000000",
  });
  // A token refresh for the same principal keeps the current handoff.
  handleXiangwanAuthSessionChange(app, { principalId });
  assert.deepEqual(app.getRegistrationContactDefaults(), {
    contactName: "李宁",
    contactPhone: "+8613900000000",
  });
  assert.equal(app._xiangwanAuthGeneration, generationAfterRelogin);
});

test("an in-flight profile read from a prior auth generation cannot populate a relogin", async (t) => {
  const originalGetMyProfile = xiangwanApi.getMyProfile;
  let resolveProfile;
  xiangwanApi.getMyProfile = () =>
    new Promise((resolve) => {
      resolveProfile = resolve;
    });
  t.after(() => {
    xiangwanApi.getMyProfile = originalGetMyProfile;
  });

  const app = createXiangwanAppDefinition();
  const principalId = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa";
  app.weconqAuth = { hasValidSession: () => true };
  app.globalData.apiBaseUrl = "https://api.weconq.cn";
  app.globalData.principalId = principalId;
  app.getXiangwanAuthBinding();
  const refresh = app.refreshXiangwanProfile();

  handleXiangwanAuthSessionChange(app, { principalId: "" });
  handleXiangwanAuthSessionChange(app, { principalId });
  resolveProfile({ nickname: "旧会话昵称", avatar_url: "", principal_profile_etag: "old" });

  assert.equal(await refresh, null);
  assert.equal(app.getXiangwanProfile(), null);
});

test("profile refresh cannot overwrite a profile mutation that finishes while it is in flight", async (t) => {
  const originalGetMyProfile = xiangwanApi.getMyProfile;
  let resolveProfile;
  xiangwanApi.getMyProfile = () =>
    new Promise((resolve) => {
      resolveProfile = resolve;
    });
  t.after(() => {
    xiangwanApi.getMyProfile = originalGetMyProfile;
  });

  const app = createXiangwanAppDefinition();
  app.weconqAuth = { hasValidSession: () => true };
  app.globalData.apiBaseUrl = "https://api.weconq.cn";
  app.globalData.principalId = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa";
  const refresh = app.refreshXiangwanProfile();
  app.markXiangwanProfileMutation();
  app.setXiangwanProfile({ nickname: "新昵称", avatarUrl: "", etag: "new" });
  resolveProfile({ nickname: "旧昵称", avatar_url: "", principal_profile_etag: "old" });

  assert.deepEqual(await refresh, app.globalData.profile);
  assert.equal(app.globalData.profile.nickname, "新昵称");
  assert.equal(app.globalData.profile.etag, "new");
});

test("concurrent profile refreshes share one request", async (t) => {
  const originalGetMyProfile = xiangwanApi.getMyProfile;
  let calls = 0;
  let resolveProfile;
  xiangwanApi.getMyProfile = () => {
    calls += 1;
    return new Promise((resolve) => {
      resolveProfile = resolve;
    });
  };
  t.after(() => {
    xiangwanApi.getMyProfile = originalGetMyProfile;
  });

  const app = createXiangwanAppDefinition();
  app.weconqAuth = { hasValidSession: () => true };
  app.globalData.apiBaseUrl = "https://api.weconq.cn";
  app.globalData.principalId = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa";
  const first = app.refreshXiangwanProfile();
  const second = app.refreshXiangwanProfile();
  assert.equal(calls, 1);
  resolveProfile({ nickname: "小林", avatar_url: "", principal_profile_etag: "etag-1" });

  assert.deepEqual(await first, await second);
  assert.equal(app.globalData.profile.nickname, "小林");
  assert.equal(app.getXiangwanProfile().nickname, "小林");
});

test("profile refresh cannot cross a principal switch", async (t) => {
  const originalGetMyProfile = xiangwanApi.getMyProfile;
  const resolvers = [];
  let calls = 0;
  xiangwanApi.getMyProfile = () => {
    calls += 1;
    return new Promise((resolve) => resolvers.push(resolve));
  };
  t.after(() => {
    xiangwanApi.getMyProfile = originalGetMyProfile;
  });

  const app = createXiangwanAppDefinition();
  app.weconqAuth = { hasValidSession: () => true };
  app.globalData.apiBaseUrl = "https://api.weconq.cn";
  app.globalData.principalId = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa";
  const oldRefresh = app.refreshXiangwanProfile();
  app.globalData.principalId = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb";
  const newRefresh = app.refreshXiangwanProfile();

  assert.equal(calls, 2);
  resolvers[0]({ nickname: "旧账号", avatar_url: "", principal_profile_etag: "old" });
  resolvers[1]({ nickname: "新账号", avatar_url: "", principal_profile_etag: "new" });
  await Promise.all([oldRefresh, newRefresh]);
  assert.equal(app.globalData.profile.nickname, "新账号");
  assert.equal(app.globalData.profile.etag, "new");
  assert.equal(app.getXiangwanProfile().nickname, "新账号");
});

test("profile owner binding hides the previous account after a failed switch refresh", async (t) => {
  const originalGetMyProfile = xiangwanApi.getMyProfile;
  let calls = 0;
  xiangwanApi.getMyProfile = async () => {
    calls += 1;
    if (calls === 1) return { nickname: "旧账号", avatar_url: "", principal_profile_etag: "old" };
    throw new Error("profile unavailable");
  };
  t.after(() => {
    xiangwanApi.getMyProfile = originalGetMyProfile;
  });

  const app = createXiangwanAppDefinition();
  app.weconqAuth = { hasValidSession: () => true };
  app.globalData.apiBaseUrl = "https://api.weconq.cn";
  app.globalData.principalId = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa";
  await app.refreshXiangwanProfile();
  assert.equal(app.getXiangwanProfile().nickname, "旧账号");

  app.globalData.principalId = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb";
  assert.equal(app.getXiangwanProfile(), null);
  assert.equal(await app.refreshXiangwanProfile(), null);
  assert.equal(app.getXiangwanProfile(), null);
  assert.equal(app.globalData.profile.nickname, "旧账号", "the raw slot may retain stale data");
});

test("runtime discards a stored host and auth session from another environment", async (t) => {
  const storage = new Map([
    ["xiangwan_session_v1_api_base_url", "https://api.wequer.com"],
    ["xiangwan_session_v1_access_token", "stale-token"],
    ["xiangwan_session_v1_access_token_expires_at", "2099-01-01T00:00:00.000Z"],
  ]);
  global.wx = {
    getAccountInfoSync() {
      return { miniProgram: { appId: "touristappid", envVersion: "develop" } };
    },
    getStorageSync(key) {
      return storage.get(key);
    },
    setStorageSync(key, value) {
      storage.set(key, value);
    },
    removeStorageSync(key) {
      storage.delete(key);
    },
  };
  t.after(() => {
    delete global.wx;
  });

  const app = createXiangwanAppDefinition();
  const runtimeReady = initializeXiangwanRuntime(app);

  assert.equal(
    app.globalData.apiBaseUrl,
    "https://api.weconq.cn",
    "the anonymous API host must be ready before the first page onLoad",
  );
  await runtimeReady;

  assert.equal(app.globalData.apiBaseUrl, "https://api.weconq.cn");
  assert.equal(storage.get("xiangwan_session_v1_api_base_url"), "https://api.weconq.cn");
  assert.equal(storage.has("xiangwan_session_v1_access_token"), false);
  assert.equal(app.globalData.accessToken, "");
});
