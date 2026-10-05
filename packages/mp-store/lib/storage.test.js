// mp-store 契约级测试(平台优化方案 D5,2026-07-10)
// 契约:吞错回 fallback(storage 故障静默降级)/ key 强转 String / JSON 坏值回 fallback。
// 体例对齐 mp-auth-session/lib/session.test.js(global.wx mock + node:test)。
const test = require("node:test");
const assert = require("node:assert/strict");

const storage = new Map();
let throwOnGet = false;
let throwOnSet = false;
let throwOnRemove = false;

// wx 用 before/after 安装+恢复,不在 module-top 赋值——run-frontend-tests packages
// tranche 是 --test-isolation=none(全文件同进程,先全加载后跑测试),top 级覆盖
// global.wx 会打翻其他文件的均衡(D5 落地时实踩:renderer/session 双向互污)。
const testWx = {
  getStorageSync(key) {
    if (throwOnGet) {
      throw new Error("storage broken");
    }
    return storage.has(key) ? storage.get(key) : "";
  },
  setStorageSync(key, value) {
    if (throwOnSet) {
      throw new Error("storage broken");
    }
    storage.set(key, value);
  },
  removeStorageSync(key) {
    if (throwOnRemove) {
      throw new Error("storage broken");
    }
    storage.delete(key);
  },
};

const { getString, setString, getJSON, setJSON, remove } = require("./storage");

test.describe("mp-store storage 契约(D5)", () => {
  let savedWx;
  test.before(() => {
    savedWx = global.wx;
    global.wx = testWx;
  });
  test.after(() => {
    global.wx = savedWx;
  });

  test.beforeEach(() => {
    storage.clear();
    throwOnGet = false;
    throwOnSet = false;
    throwOnRemove = false;
  });

  test("getString: 命中返回原串;缺失 key wx 回空串则原样返回(契约:fallback 只兜非 string/异常,不兜空串)", () => {
    setString("k", "v");
    assert.equal(getString("k"), "v");
    // 真实 wx.getStorageSync 对缺失 key 回 ""(string)→ 原样返回,fallback 不触发
    assert.equal(getString("missing"), "");
    assert.equal(getString("missing", "fb"), "");
  });

  test("getString: 底层非 string 值(异物)回 fallback 不外泄", () => {
    storage.set("weird", { a: 1 });
    assert.equal(getString("weird", "fb"), "fb");
  });

  test("getString: wx 抛错吞掉回 fallback(吞错契约)", () => {
    setString("k", "v");
    throwOnGet = true;
    assert.equal(getString("k", "fb"), "fb");
  });

  test("setString: 值与 key 均经 String(x||'') —— null/undefined/0/false 全折叠为空(契约如实)", () => {
    setString("n", null);
    assert.equal(getString("n"), "");
    // ⚠️ falsy 折叠是 || 的真实语义:0 与 false 也变 ""(codex 终审点名;调用方勿存数字/布尔原值)
    setString("zero", 0);
    assert.equal(getString("zero"), "");
    setString("bool", false);
    assert.equal(getString("bool"), "");
    setString(123, 456);
    assert.equal(getString("123"), "456");
    // key null → String(key||"") = ""
    setString(null, "x");
    assert.equal(getString(""), "x");
  });

  test("setString: 写路径**不**吞错(setStorageSync 抛错外泄——与读路径吞错不对称,契约如实)", () => {
    throwOnSet = true;
    assert.throws(() => setString("k", "v"), /storage broken/);
    throwOnSet = false;
  });

  test("setJSON/getJSON 往返;缺失与坏 JSON 回 fallback", () => {
    setJSON("obj", { a: [1, 2], b: "文" });
    assert.deepEqual(getJSON("obj"), { a: [1, 2], b: "文" });
    assert.equal(getJSON("missing"), null);
    assert.deepEqual(getJSON("missing", { d: 1 }), { d: 1 });
    setString("bad", "{not json");
    assert.deepEqual(getJSON("bad", { fb: true }), { fb: true });
  });

  test("getJSON: 空串按缺失处理(回 fallback 而非解析报错)", () => {
    setString("empty", "");
    assert.equal(getJSON("empty", "fb"), "fb");
  });

  test("remove: 删除生效(经 getJSON fallback 观测)且幂等;wx 抛错吞掉", () => {
    setJSON("k", { v: 1 });
    remove("k");
    // 删除后 raw="" → getJSON 空串按缺失回 fallback(getString 面则回 "",见上)
    assert.deepEqual(getJSON("k", { gone: true }), { gone: true });
    assert.equal(getString("k"), "");
    remove("k"); // 幂等
    throwOnRemove = true;
    assert.doesNotThrow(() => remove("k"));
  });
});
