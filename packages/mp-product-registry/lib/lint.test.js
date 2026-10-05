"use strict";

// 矩阵命名统一(B5 #121 STOP follow-up,2026-05-12):验证 lint 在 wq- 前缀
// 与 legacy bare 两端的契约 — 关键是 grace period(不 throw,只 warn / error
// 走 logger),确保启动期暴露问题但不 break 已发布版本。

const test = require("node:test");
const assert = require("node:assert");

const { lintProductCode } = require("./lint");

function captureLogger() {
  const calls = { error: [], warn: [] };
  return {
    logger: {
      error: (msg) => calls.error.push(String(msg)),
      warn: (msg) => calls.warn.push(String(msg)),
    },
    calls,
  };
}

test("lintProductCode wq- 前缀(矩阵 canonical) → ok 无任何 logger 调用", () => {
  for (const code of ["wq-community", "wq-study", "wq-schedule", "wq-compass"]) {
    const { logger, calls } = captureLogger();
    const result = lintProductCode(code, { logger });
    assert.strictEqual(result.ok, true, `${code} should be ok`);
    assert.strictEqual(result.level, "ok", `${code} level should be ok`);
    assert.strictEqual(calls.error.length, 0);
    assert.strictEqual(calls.warn.length, 0);
  }
});

test("lintProductCode legacy bare(community/study)→ ok+warn(grace,不 throw)", () => {
  for (const code of ["community", "study", "record", "compass"]) {
    const { logger, calls } = captureLogger();
    const result = lintProductCode(code, { logger });
    assert.strictEqual(result.ok, true, `${code} should still be ok (grace)`);
    assert.strictEqual(result.level, "warn");
    assert.strictEqual(calls.warn.length, 1, `${code} expected one warn`);
    assert.strictEqual(calls.error.length, 0);
  }
});

test("lintProductCode 未知 bare(不在 allowlist 又无 wq- 前缀)→ error 但不 throw", () => {
  const { logger, calls } = captureLogger();
  const result = lintProductCode("randomname", { logger });
  assert.strictEqual(result.ok, false);
  assert.strictEqual(result.level, "error");
  assert.strictEqual(calls.error.length, 1);
});

test("lintProductCode 空值 → error 但不 throw", () => {
  const { logger, calls } = captureLogger();
  assert.doesNotThrow(() => lintProductCode("", { logger }));
  assert.deepStrictEqual(calls.error.length, 1);
});

test("lintProductCode 非法格式(大写 / 特殊字符)→ error 但不 throw", () => {
  const { logger, calls } = captureLogger();
  const result = lintProductCode("WQ-Community!", { logger });
  assert.strictEqual(result.ok, false);
  assert.strictEqual(calls.error.length, 1);
});
