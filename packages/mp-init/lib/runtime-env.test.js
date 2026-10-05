"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");

const runtimeEnv = require("./runtime-env");

test("resolveDefaultApiBaseUrl returns production base url", () => {
  assert.equal(runtimeEnv.resolveDefaultApiBaseUrl(), "https://api.wequer.com");
  assert.equal(runtimeEnv.PRODUCTION_API_BASE_URL, "https://api.wequer.com");
  assert.equal(runtimeEnv.LOCAL_API_BASE_URL, "http://127.0.0.1:8080");
});

test("readMiniProgramEnvVersion falls back to develop when wx missing", () => {
  const previous = global.wx;
  delete global.wx;
  try {
    assert.equal(runtimeEnv.readMiniProgramEnvVersion(), "develop");
    assert.equal(runtimeEnv.isDevelopmentToolsEnabled(), true);
  } finally {
    if (previous !== undefined) global.wx = previous;
  }
});

test("readMiniProgramEnvVersion reads wx.getAccountInfoSync", () => {
  const previous = global.wx;
  global.wx = {
    getAccountInfoSync() {
      return { miniProgram: { envVersion: "release" } };
    },
  };
  try {
    assert.equal(runtimeEnv.readMiniProgramEnvVersion(), "release");
    assert.equal(runtimeEnv.isDevelopmentToolsEnabled(), false);
  } finally {
    if (previous === undefined) delete global.wx;
    else global.wx = previous;
  }
});

test("mp-init index.js re-exports runtime-env", () => {
  const mpInit = require("../index");
  assert.equal(typeof mpInit.readMiniProgramEnvVersion, "function");
  assert.equal(typeof mpInit.resolveDefaultApiBaseUrl, "function");
  assert.equal(mpInit.runtimeEnv, runtimeEnv);
});

// ---- dev local profile(2026-07-09 平台优化 I1-FE/PR-11)----

function withWx(mock, fn) {
  const previous = global.wx;
  global.wx = mock;
  try {
    return fn();
  } finally {
    if (previous === undefined) delete global.wx;
    else global.wx = previous;
  }
}

function storageWx(envVersion, store) {
  return {
    getAccountInfoSync() {
      return { miniProgram: { envVersion } };
    },
    getStorageSync(key) {
      return store[key];
    },
    setStorageSync(key, value) {
      store[key] = value;
    },
    removeStorageSync(key) {
      delete store[key];
    },
  };
}

test("develop 环境读 storage 开关并覆盖默认 base url", () => {
  const store = { [runtimeEnv.DEV_API_BASE_STORAGE_KEY]: "http://127.0.0.1:8080" };
  withWx(storageWx("develop", store), () => {
    assert.equal(runtimeEnv.getDevApiBaseOverride(), "http://127.0.0.1:8080");
    assert.equal(runtimeEnv.resolveDefaultApiBaseUrl(), "http://127.0.0.1:8080");
  });
});

test("非 develop 环境无条件生产(开关被无视=发布安全)", () => {
  const store = { [runtimeEnv.DEV_API_BASE_STORAGE_KEY]: "http://127.0.0.1:8080" };
  for (const env of ["trial", "release"]) {
    withWx(storageWx(env, store), () => {
      assert.equal(runtimeEnv.getDevApiBaseOverride(), "");
      assert.equal(runtimeEnv.resolveDefaultApiBaseUrl(), runtimeEnv.PRODUCTION_API_BASE_URL);
    });
  }
});

test("非法 override 值被忽略;set 校验 http(s);clear 幂等", () => {
  const store = { [runtimeEnv.DEV_API_BASE_STORAGE_KEY]: "not-a-url" };
  withWx(storageWx("develop", store), () => {
    assert.equal(runtimeEnv.getDevApiBaseOverride(), "");
    assert.equal(runtimeEnv.resolveDefaultApiBaseUrl(), runtimeEnv.PRODUCTION_API_BASE_URL);
    assert.throws(() => runtimeEnv.setDevApiBaseOverride("ftp://x"), /http\(s\)/);
    assert.equal(runtimeEnv.enableLocalApiBase(), runtimeEnv.LOCAL_API_BASE_URL);
    assert.equal(runtimeEnv.getDevApiBaseOverride(), runtimeEnv.LOCAL_API_BASE_URL);
    runtimeEnv.clearDevApiBaseOverride();
    runtimeEnv.clearDevApiBaseOverride(); // 幂等
    assert.equal(runtimeEnv.getDevApiBaseOverride(), "");
  });
});
