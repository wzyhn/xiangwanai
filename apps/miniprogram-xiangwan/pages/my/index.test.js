"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const { createMyPageDefinition, projectBookingStatus } = require("./index");

function flush() {
  return new Promise((resolve) => setImmediate(resolve));
}

test("My hub projects appointment counts from authoritative registration states", () => {
  const counts = projectBookingStatus(
    [
      { registration_id: "r1", state: "registered", session_end_at: "2099-09-06T09:00:00Z" },
      { registration_id: "r2", state: "ended", session_end_at: "2026-09-06T09:00:00Z" },
      { registration_id: "r3", state: "cancelled", session_end_at: "2099-09-06T09:00:00Z" },
      { registration_id: "r4", state: "refunded", session_end_at: "2099-09-06T09:00:00Z" },
    ],
    Date.parse("2026-09-21T00:00:00Z"),
  );

  assert.deepEqual(Object.fromEntries(counts.map((item) => [item.key, item.value])), {
    pending: "1",
    completed: "1",
    cancelled: "2",
    all: "4",
  });
});

test("My hub keeps a registered session pending until the server marks it ended", () => {
  const counts = projectBookingStatus(
    [{ state: "registered", session_end_at: "2026-09-20T09:00:00Z" }],
    Date.parse("2026-09-21T00:00:00Z"),
  );
  assert.equal(counts.find((item) => item.key === "completed").value, "0");
  assert.equal(counts.find((item) => item.key === "pending").value, "1");
});

test("My hub waits for hydration and never triggers login on tab entry", async (t) => {
  let resolveRuntime;
  let loginCalls = 0;
  const app = {
    runtimeReady: new Promise((resolve) => {
      resolveRuntime = resolve;
    }),
    weconqAuth: { hasValidSession: () => false },
    async ensureAuthenticated() {
      loginCalls += 1;
    },
  };
  global.getApp = () => app;
  t.after(() => delete global.getApp);
  const page = createMyPageDefinition();
  page.setData = (changes) => {
    page.data = { ...page.data, ...changes };
  };
  page.onLoad();
  page.onShow();
  assert.equal(page.data.loading, true);

  resolveRuntime(true);
  await flush();

  assert.equal(loginCalls, 0);
  assert.equal(page.data.needsLogin, true);
  assert.equal(page.data.authenticated, false);
});

test("My hub opens only its fixed feature routes after authentication", async (t) => {
  const calls = [];
  global.wx = { navigateTo: (options) => calls.push(options) };
  t.after(() => delete global.wx);
  const page = createMyPageDefinition();
  page.data.authenticated = true;
  page.openFeature({ currentTarget: { dataset: { key: "orders" } } });
  page.openFeature({ currentTarget: { dataset: { key: "coupons" } } });
  page.openFeature({ currentTarget: { dataset: { key: "favorites" } } });
  page.openFeature({ currentTarget: { dataset: { key: "benefits" } } });
  page.openFeature({ currentTarget: { dataset: { key: "../../unsafe" } } });

  assert.deepEqual(calls, [
    { url: "/pages/my-orders/index" },
    { url: "/pages/my-coupons/index" },
    { url: "/pages/my-favorites/index" },
    { url: "/pages/my-benefits/index" },
  ]);
});

test("My hub profile area routes to the profile editor only when authenticated", (t) => {
  const calls = [];
  global.wx = { navigateTo: (options) => calls.push(options) };
  t.after(() => delete global.wx);
  const page = createMyPageDefinition();
  page.data.authenticated = false;
  page.openProfile();
  page.data.authenticated = true;
  page.openProfile();

  assert.deepEqual(calls, [{ url: "/pages/my-profile/index" }]);
});

test("My hub renders cached profile immediately and refreshes it on every return", async (t) => {
  let refreshCalls = 0;
  const app = {
    runtimeReady: Promise.resolve(true),
    weconqAuth: { hasValidSession: () => true },
    globalData: {
      profile: { nickname: "小林", avatarUrl: "https://cdn.example.com/a.png", etag: "e1" },
    },
    async refreshXiangwanProfile() {
      refreshCalls += 1;
      return this.globalData.profile;
    },
  };
  global.getApp = () => app;
  t.after(() => delete global.getApp);
  const page = createMyPageDefinition();
  page.setData = (changes) => {
    page.data = { ...page.data, ...changes };
  };
  page.onLoad();
  page.onShow();
  await flush();

  assert.equal(page.data.authenticated, true);
  assert.equal(page.data.profileBadge.displayName, "小林");
  assert.equal(page.data.profileBadge.avatarUrl, "https://cdn.example.com/a.png");
  assert.equal(page.data.profileBadge.initial, "小");
  assert.equal(refreshCalls, 1, "cached avatars are revalidated on return");

  app.globalData.profile = { nickname: "平台资料形状", avatarUrl: "https://x" };
  app.refreshXiangwanProfile = async function () {
    refreshCalls += 1;
    this.globalData.profile = { nickname: "阿沐", avatarUrl: "", etag: "e2" };
    return this.globalData.profile;
  };
  page.onShow();
  await flush();
  await flush();
  assert.equal(refreshCalls, 2, "a platform-shaped profile also refreshes");
  assert.equal(page.data.profileBadge.displayName, "阿沐");
  assert.equal(page.data.profileBadge.initial, "阿");
});

test("My hub falls back to the setup hint and gradient initial without a profile", async (t) => {
  const app = {
    runtimeReady: Promise.resolve(true),
    weconqAuth: { hasValidSession: () => true },
    globalData: { profile: null },
    async refreshXiangwanProfile() {
      return null;
    },
  };
  global.getApp = () => app;
  t.after(() => delete global.getApp);
  const page = createMyPageDefinition();
  page.setData = (changes) => {
    page.data = { ...page.data, ...changes };
  };
  page.onLoad();
  page.onShow();
  await flush();
  await flush();

  assert.equal(page.data.profileBadge.displayName, "设置头像昵称 ›");
  assert.equal(page.data.profileBadge.initial, "享");
  assert.equal(page.data.profileBadge.avatarUrl, "");

  page.data.profileBadge = {
    ...page.data.profileBadge,
    avatarUrl: "https://broken.example.com/x.png",
  };
  page.onAvatarError();
  assert.equal(page.data.profileBadge.avatarUrl, "", "image binderror degrades to the initial");
});

test("My hub never renders a profile returned for another principal", async (t) => {
  const app = {
    runtimeReady: Promise.resolve(true),
    weconqAuth: { hasValidSession: () => true },
    globalData: {
      profile: { nickname: "旧账号", avatarUrl: "https://cdn.example.com/old.png", etag: "old" },
    },
    getXiangwanProfile() {
      return null;
    },
    async refreshXiangwanProfile() {
      return null;
    },
  };
  global.getApp = () => app;
  t.after(() => delete global.getApp);
  const page = createMyPageDefinition();
  page.setData = (changes) => {
    page.data = { ...page.data, ...changes };
  };
  page.onLoad();
  page.onShow();
  await flush();
  await flush();

  assert.equal(page.data.profileBadge.displayName, "设置头像昵称 ›");
  assert.equal(page.data.profileBadge.avatarUrl, "");
});
