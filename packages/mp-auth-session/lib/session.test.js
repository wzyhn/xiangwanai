const test = require("node:test");
const assert = require("node:assert/strict");

const storage = new Map();

global.wx = {
  getStorageSync(key) {
    return storage.has(key) ? storage.get(key) : "";
  },
  removeStorageSync(key) {
    storage.delete(key);
  },
  setStorageSync(key, value) {
    storage.set(key, value);
  },
};

const { bootstrapSession, createSessionStore } = require("./session");

test("createSessionStore reads and writes session fields", () => {
  storage.clear();
  const store = createSessionStore({
    defaultApiBaseUrl: "http://127.0.0.1:8080",
  });

  assert.equal(store.readApiBaseUrl(), "http://127.0.0.1:8080");
  assert.equal(store.readAccessToken(), "");

  assert.equal(store.setApiBaseUrl(" https://api.example.com/ "), "https://api.example.com/");
  assert.equal(store.setAccessToken(" token-value "), "token-value");
  assert.equal(store.readApiBaseUrl(), "https://api.example.com/");
  assert.equal(store.readAccessToken(), "token-value");

  store.clearSession();
  assert.equal(store.readApiBaseUrl(), "http://127.0.0.1:8080");
  assert.equal(store.readAccessToken(), "");
});

test("createSessionStore manages auth session metadata", () => {
  storage.clear();
  const store = createSessionStore();

  const snapshot = store.setAuthSession({
    token: "token-123",
    expiresAt: "2026-04-10T10:00:00+08:00",
    principalId: "principal-1",
    openId: "openid-1",
    nextStep: "complete_profile",
    productState: {
      productCode: "study",
      profileCompleted: false,
    },
    profile: {
      principal_id: "principal-1",
      nickname: "测试同学",
    },
  });

  assert.equal(snapshot.accessToken, "token-123");
  assert.equal(store.readAccessToken(), "token-123");
  assert.equal(store.readPrincipalId(), "principal-1");
  assert.equal(store.readOpenId(), "openid-1");
  assert.equal(store.readNextStep(), "complete_profile");
  assert.deepEqual(store.readProductState(), {
    productCode: "study",
    profileCompleted: false,
  });
  assert.deepEqual(store.readProfile(), {
    principal_id: "principal-1",
    nickname: "测试同学",
  });

  store.clearAuthSession();
  assert.equal(store.readAccessToken(), "");
  assert.equal(store.readPrincipalId(), "");
  assert.equal(store.readOpenId(), "");
  assert.equal(store.readNextStep(), "none");
  assert.equal(store.readProductState(), null);
  assert.equal(store.readProfile(), null);
});

test("createSessionStore clears stale metadata when explicit null session fields are provided", () => {
  storage.clear();
  const store = createSessionStore();

  store.setAuthSession({
    token: "token-old",
    principalId: "principal-old",
    openId: "openid-old",
    nextStep: "complete_profile",
    productState: {
      productCode: "community",
      profileCompleted: false,
    },
    profile: {
      principal_id: "principal-old",
      nickname: "旧用户",
    },
  });

  const snapshot = store.setAuthSession({
    token: "token-new",
    expiresAt: "",
    principalId: "",
    openId: "",
    nextStep: "none",
    productState: null,
    profile: null,
  });

  assert.equal(snapshot.accessToken, "token-new");
  assert.equal(store.readPrincipalId(), "");
  assert.equal(store.readOpenId(), "");
  assert.equal(store.readNextStep(), "none");
  assert.equal(store.readProductState(), null);
  assert.equal(store.readProfile(), null);
});

test("bootstrapSession returns normalized values and mutators", () => {
  storage.clear();
  const session = bootstrapSession({
    defaultApiBaseUrl: "http://127.0.0.1:9000",
  });

  assert.equal(session.apiBaseUrl, "http://127.0.0.1:9000");
  assert.equal(session.accessToken, "");
  assert.equal(session.setApiBaseUrl(""), "http://127.0.0.1:9000");
  assert.equal(session.setAccessToken("abc"), "abc");
});

// ─── D2: isAccessTokenValid conservative expiry behaviour ────────────────────

test("isAccessTokenValid returns false when accessToken is absent", () => {
  storage.clear();
  const store = createSessionStore();
  assert.equal(store.isAccessTokenValid(), false);
});

test("isAccessTokenValid returns false when expiresAt is empty string (conservative)", () => {
  storage.clear();
  const store = createSessionStore();
  // Simulate what app.js setAccessToken writes: token present, expiresAt ""
  store.setAuthSession({ token: "tok", expiresAt: "" });
  // Previously returned true (treated missing expiry as perpetually valid).
  // Now returns false so hydrate({ verify:true }) is triggered.
  assert.equal(store.isAccessTokenValid(), false);
});

test("isAccessTokenValid returns false when expiresAt is missing/undefined (conservative)", () => {
  storage.clear();
  const store = createSessionStore();
  store.setAuthSession({ token: "tok" });
  // No expiresAt key written — readAccessTokenExpiresAt() returns ""
  assert.equal(store.isAccessTokenValid(), false);
});

test("isAccessTokenValid returns false when expiresAt is malformed (conservative)", () => {
  storage.clear();
  const store = createSessionStore();
  store.setAuthSession({ token: "tok", expiresAt: "not-a-date" });
  // Date.parse("not-a-date") is NaN — previously returned true, now false.
  assert.equal(store.isAccessTokenValid(), false);
});

test("isAccessTokenValid returns true for future expiry (happy path — normal WxLogin flow)", () => {
  storage.clear();
  const store = createSessionStore();
  // WxLogin response carries a real expires_at — far in the future
  const future = new Date(Date.now() + 7200 * 1000).toISOString(); // +2 hours
  store.setAuthSession({ token: "tok-fresh", expiresAt: future });
  assert.equal(store.isAccessTokenValid(), true);
});

test("isAccessTokenValid returns false for past expiry", () => {
  storage.clear();
  const store = createSessionStore();
  const past = new Date(Date.now() - 60 * 1000).toISOString(); // -1 minute
  store.setAuthSession({ token: "tok-stale", expiresAt: past });
  assert.equal(store.isAccessTokenValid(), false);
});

test("isAccessTokenValid applies buffer: token within buffer window is invalid", () => {
  storage.clear();
  const store = createSessionStore();
  // Expires in 20 seconds — within the 30s default buffer
  const nearFuture = new Date(Date.now() + 20 * 1000).toISOString();
  store.setAuthSession({ token: "tok-near", expiresAt: nearFuture });
  assert.equal(store.isAccessTokenValid(), false); // default 30s buffer
});

test("isAccessTokenValid with zero buffer: token expiring in 1s is still valid", () => {
  storage.clear();
  const store = createSessionStore();
  const almostExpired = new Date(Date.now() + 1000).toISOString();
  store.setAuthSession({ token: "tok-almost", expiresAt: almostExpired });
  assert.equal(store.isAccessTokenValid(0), true);
});
