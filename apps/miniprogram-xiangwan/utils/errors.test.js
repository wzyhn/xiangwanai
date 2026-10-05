"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const { getUserMessage, isCustomerSafeMessage, isUnknownWriteResult } = require("./errors");

test("customer messages never expose backend terminology", () => {
  assert.equal(isCustomerSafeMessage("活动信息已变化，请刷新后重新确认"), true);
  assert.equal(isCustomerSafeMessage("PostgreSQL Session unavailable"), false);
  assert.equal(
    getUserMessage({ statusCode: 500, userMessage: "internal server error" }),
    "暂时无法完成，请稍后重试",
  );
  assert.equal(
    getUserMessage({ statusCode: 409, userMessage: "Registration conflict" }),
    "活动信息已变化，请刷新后重新确认",
  );
});

test("isCustomerSafeMessage rejects empty, whitespace, latin-only and mixed technical copy", () => {
  assert.equal(isCustomerSafeMessage(""), false);
  assert.equal(isCustomerSafeMessage(null), false);
  assert.equal(isCustomerSafeMessage("   "), false);
  assert.equal(isCustomerSafeMessage("ok"), false);
  // 含 CJK 但混入技术词:按泄露处理。
  assert.equal(isCustomerSafeMessage("活动 session 已结束"), false);
  assert.equal(isCustomerSafeMessage("generation 校验未通过"), false);
  // 合法客户文案里的 "AppID" 不构成 \bapi\b 匹配,不得误伤。
  assert.equal(isCustomerSafeMessage("享玩微信 AppID 尚未配置"), true);
});

test("backend customer copy reaches users through error.message", () => {
  // mp-http 把服务端 body.message 放进 error.message;后端 4xx 文案直达用户。
  assert.equal(
    getUserMessage({ statusCode: 409, message: "名额刚刚发生变化，请刷新后重试" }),
    "名额刚刚发生变化，请刷新后重试",
  );
  assert.equal(
    getUserMessage(
      { statusCode: 400, message: "提交内容有误，请检查后重试" },
      "报名表加载失败，请稍后重试",
    ),
    "提交内容有误，请检查后重试",
  );
  // 英文/传输层文案被过滤后回落到状态码文案或调用方 fallback,不再有 400 早返回。
  assert.equal(
    getUserMessage(
      { statusCode: 400, message: "invalid response envelope" },
      "报名表加载失败，请稍后重试",
    ),
    "报名表加载失败，请稍后重试",
  );
  assert.equal(
    getUserMessage({ statusCode: 500, message: "internal server error" }),
    "暂时无法完成，请稍后重试",
  );
});

test("local userMessage keeps priority over backend message", () => {
  assert.equal(
    getUserMessage({
      statusCode: 409,
      message: "内容已更新，请刷新后重试",
      userMessage: "活动信息已变化，请刷新后重新确认",
    }),
    "活动信息已变化，请刷新后重新确认",
  );
});

test("write ambiguity includes transport, invalid-envelope and server failures", () => {
  assert.equal(isUnknownWriteResult(new Error("timeout")), true);
  assert.equal(isUnknownWriteResult({ statusCode: 200, invalidResponse: true }), true);
  assert.equal(isUnknownWriteResult({ statusCode: 503 }), true);
  assert.equal(isUnknownWriteResult({ statusCode: 409 }), false);
  assert.equal(isUnknownWriteResult(null), false);
});
