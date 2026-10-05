"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");

const { createWeconqAuthRuntime } = require("./runtime");

function createFakeSessionStore(initial = {}) {
  const state = {
    apiBaseUrl: "",
    accessToken: "",
    accessTokenExpiresAt: "",
    principalId: "",
    openId: "",
    profile: null,
    productState: null,
    nextStep: "none",
    ...initial,
  };

  return {
    __state: state,
    readApiBaseUrl() {
      return state.apiBaseUrl;
    },
    readAccessToken() {
      return state.accessToken;
    },
    readAccessTokenExpiresAt() {
      return state.accessTokenExpiresAt;
    },
    readPrincipalId() {
      return state.principalId;
    },
    readOpenId() {
      return state.openId;
    },
    readProfile() {
      return state.profile;
    },
    readProductState() {
      return state.productState;
    },
    readNextStep() {
      return state.nextStep;
    },
    setAuthSession(session = {}) {
      state.accessToken = String(session.accessToken || session.token || "").trim();
      state.accessTokenExpiresAt = String(session.expiresAt || session.expires_at || "").trim();
      state.principalId = String(session.principalId || session.principal_id || "").trim();
      state.openId = String(session.openId || session.openid || session.open_id || "").trim();
      state.profile =
        session.profile && typeof session.profile === "object" ? session.profile : null;
      state.productState = session.productState || session.product_state || null;
      state.nextStep = String(session.nextStep || session.next_step || "none").trim() || "none";
      return { ...state };
    },
    setProfile(profile) {
      state.profile = profile;
      return profile;
    },
    clearAuthSession() {
      state.accessToken = "";
      state.accessTokenExpiresAt = "";
      state.principalId = "";
      state.openId = "";
      state.profile = null;
      state.productState = null;
      state.nextStep = "none";
    },
    isAccessTokenValid() {
      return !!state.accessToken;
    },
  };
}

test("runtime clears stale auth state when verify returns 404 for the current auth endpoint", async () => {
  const sessionStore = createFakeSessionStore({
    accessToken: "token-1",
    principalId: "principal-1",
  });
  const runtime = createWeconqAuthRuntime({
    activate: false,
    app: { globalData: {} },
    sessionStore,
    client: {
      verifySession: async () => {
        const error = new Error("request failed with status 404");
        error.statusCode = 404;
        throw error;
      },
      getCurrentProfile: async () => null,
    },
    productName: "纸飞机",
  });

  const snapshot = await runtime.hydrate({
    verify: true,
  });

  assert.equal(snapshot.accessToken, "");
  assert.equal(sessionStore.__state.principalId, "");
  assert.equal(sessionStore.__state.profile, null);
});

test("runtime retries wechat login once with a fresh code after a retryable failure", async () => {
  const sessionStore = createFakeSessionStore();
  const loginPayloads = [];
  let wxLoginCalls = 0;
  let loginCalls = 0;
  const runtime = createWeconqAuthRuntime({
    activate: false,
    app: { globalData: {} },
    appId: "wx-community",
    sessionStore,
    wx: {
      login({ success }) {
        wxLoginCalls += 1;
        success({ code: `code-${wxLoginCalls}` });
      },
    },
    client: {
      async loginWithWeChat(payload) {
        loginCalls += 1;
        loginPayloads.push(payload);
        if (loginCalls === 1) {
          const error = new Error("微信登录服务暂时不可用，请稍后再试");
          error.statusCode = 500;
          error.code = 10006;
          throw error;
        }
        return {
          token: "token-2",
          principalId: "principal-2",
          openId: "openid-2",
          profile: {
            principalId: "principal-2",
            nickname: "测试同学",
          },
        };
      },
      getCurrentProfile: async () => null,
    },
    productName: "纸飞机",
  });

  const snapshot = await runtime.login();

  assert.equal(wxLoginCalls, 2);
  assert.equal(loginCalls, 2);
  assert.deepEqual(loginPayloads, [
    { appId: "wx-community", code: "code-1" },
    { appId: "wx-community", code: "code-2" },
  ]);
  assert.equal(snapshot.accessToken, "token-2");
  assert.equal(snapshot.principalId, "principal-2");
});

test("runtime does not retry wechat login for non-retryable request errors", async () => {
  const sessionStore = createFakeSessionStore();
  let wxLoginCalls = 0;
  let loginCalls = 0;
  const runtime = createWeconqAuthRuntime({
    activate: false,
    app: { globalData: {} },
    appId: "wx-community",
    sessionStore,
    wx: {
      login({ success }) {
        wxLoginCalls += 1;
        success({ code: `code-${wxLoginCalls}` });
      },
    },
    client: {
      async loginWithWeChat() {
        loginCalls += 1;
        const error = new Error("unsupported app_id");
        error.statusCode = 400;
        error.code = 10001;
        throw error;
      },
      getCurrentProfile: async () => null,
    },
    productName: "纸飞机",
  });

  await assert.rejects(runtime.login(), /unsupported app_id/);
  assert.equal(wxLoginCalls, 1);
  assert.equal(loginCalls, 1);
});
