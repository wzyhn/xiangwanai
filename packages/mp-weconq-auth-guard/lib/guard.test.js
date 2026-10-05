"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");

global.getApp =
  global.getApp ||
  (() => ({
    globalData: {
      weconqProductName: "纸飞机",
    },
  }));

const {
  bindWeconqAuthPage,
  ensureWeconqSessionForPage,
  handleWeconqLoginSuccess,
  promptCurrentPageWeconqLogin,
  promptWeconqLogin,
} = require("./guard");
const {
  getActiveWeconqAuthRuntime,
  setActiveWeconqAuthRuntime,
} = require("mp-weconq-auth-runtime");

test("bindWeconqAuthPage seeds login popup strings before the first prompt", () => {
  const page = {
    data: {},
    setData(next) {
      Object.assign(this.data, next);
    },
  };

  bindWeconqAuthPage(page, {
    productName: "纸飞机",
    sceneTitle: "登录后继续",
    sceneDetail: "登录后同步资料",
    allowDevLogin: true,
  });

  assert.equal(page.data.weconqLoginProductName, "纸飞机");
  assert.equal(page.data.weconqLoginSceneTitle, "登录后继续");
  assert.equal(page.data.weconqLoginSceneDetail, "登录后同步资料");
  assert.equal(page.data.weconqLoginAllowDevLogin, true);
  assert.equal(page.__weconqAuthPromptBound, true);
});

test("promptWeconqLogin refuses an unbound page instead of leaving a request pending forever", async () => {
  const savedRuntime = getActiveWeconqAuthRuntime();
  setActiveWeconqAuthRuntime({
    hasValidSession: () => false,
    isDevLoginEnabled: () => false,
  });
  const page = {
    data: {},
    setData(next) {
      Object.assign(this.data, next);
    },
  };
  try {
    await assert.rejects(promptWeconqLogin(page), (error) => error.code === "missing_prompt_host");
    assert.equal(page.data.showWeconqLoginPopup, undefined);
  } finally {
    setActiveWeconqAuthRuntime(savedRuntime);
  }
});

test("an already valid session does not require a popup host", async () => {
  const savedRuntime = getActiveWeconqAuthRuntime();
  const session = { accessToken: "token" };
  setActiveWeconqAuthRuntime({
    hasValidSession: () => true,
    getSession: () => session,
    isDevLoginEnabled: () => false,
  });
  const page = {
    data: {},
    setData(next) {
      Object.assign(this.data, next);
    },
  };
  try {
    assert.equal(await promptWeconqLogin(page), session);
  } finally {
    setActiveWeconqAuthRuntime(savedRuntime);
  }
});

test("a bound prompt resolves after the popup reports login success", async () => {
  const savedRuntime = getActiveWeconqAuthRuntime();
  setActiveWeconqAuthRuntime({
    hasValidSession: () => false,
    isDevLoginEnabled: () => false,
  });
  const page = {
    data: {},
    setData(next) {
      Object.assign(this.data, next);
    },
  };
  try {
    bindWeconqAuthPage(page);
    const pending = promptWeconqLogin(page);
    assert.equal(page.data.showWeconqLoginPopup, true);
    handleWeconqLoginSuccess(page, { detail: { accessToken: "token" } });
    assert.deepEqual(await pending, { accessToken: "token" });
    assert.equal(page.data.showWeconqLoginPopup, false);
  } finally {
    setActiveWeconqAuthRuntime(savedRuntime);
  }
});

test("concurrent page guards share one silent auth and one popup", async () => {
  const savedRuntime = getActiveWeconqAuthRuntime();
  let ensureCalls = 0;
  setActiveWeconqAuthRuntime({
    hasValidSession: () => false,
    isDevLoginEnabled: () => false,
    async ensureSession() {
      ensureCalls += 1;
      const error = new Error("login required");
      error.authRequired = true;
      throw error;
    },
  });
  const page = {
    data: {},
    setData(next) {
      Object.assign(this.data, next);
    },
  };

  try {
    bindWeconqAuthPage(page);
    const first = ensureWeconqSessionForPage(page);
    const second = ensureWeconqSessionForPage(page);
    await Promise.resolve();
    await Promise.resolve();
    assert.equal(ensureCalls, 1);
    assert.equal(page.data.showWeconqLoginPopup, true);
    handleWeconqLoginSuccess(page, { detail: { accessToken: "token" } });
    assert.deepEqual(await Promise.all([first, second]), [true, true]);
    assert.equal(page.__weconqSessionEnsurePromise, null);
  } finally {
    setActiveWeconqAuthRuntime(savedRuntime);
  }
});

test("server-rejected auth clears a locally valid session before silent retry", async () => {
  const savedRuntime = getActiveWeconqAuthRuntime();
  const savedGetCurrentPages = global.getCurrentPages;
  let valid = true;
  let clearReason = "";
  let ensureCalls = 0;
  setActiveWeconqAuthRuntime({
    hasValidSession: () => valid,
    clearAuthSession(reason) {
      clearReason = reason;
      valid = false;
    },
    async ensureSession() {
      ensureCalls += 1;
      valid = true;
      return { accessToken: "fresh-token" };
    },
  });
  global.getCurrentPages = () => [];

  try {
    assert.equal(
      await promptCurrentPageWeconqLogin({
        forceRelogin: true,
        reason: "server-auth-rejected",
      }),
      true,
    );
    assert.equal(clearReason, "server-auth-rejected");
    assert.equal(ensureCalls, 1);
  } finally {
    setActiveWeconqAuthRuntime(savedRuntime);
    if (typeof savedGetCurrentPages === "undefined") {
      delete global.getCurrentPages;
    } else {
      global.getCurrentPages = savedGetCurrentPages;
    }
  }
});
