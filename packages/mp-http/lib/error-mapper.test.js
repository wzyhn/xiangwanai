"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");

const { mapErrorToToast, getUserFacingMessage, DEFAULT_GENERIC_TOAST } = require("./error-mapper");

test("maps common 10001-10006 to user-friendly toast", () => {
  assert.equal(mapErrorToToast({ code: 10001 }).title, "有些信息需要再检查一下");
  assert.equal(mapErrorToToast({ code: 10002 }).title, "登录状态已更新，请重新登录");
  assert.equal(mapErrorToToast({ code: 10006 }).title, "服务暂时开了个小差，请稍后再试");
});

test("maps named codes (community / study / record / schedule / billing / syncpkg / ai)", () => {
  assert.equal(mapErrorToToast({ code: 30001 }).title, "这篇内容暂时找不到了");
  assert.equal(mapErrorToToast({ code: 30101 }).title, "这篇笔记暂时找不到了");
  assert.equal(mapErrorToToast({ code: 30201 }).title, "这条记录暂时找不到了");
  assert.equal(mapErrorToToast({ code: 30401 }).title, "这份课表暂时找不到了");
  assert.equal(mapErrorToToast({ code: 40001 }).title, "本期 AI 使用额度已用完");
  assert.equal(mapErrorToToast({ code: 40101 }).title, "配额已用完");
  assert.equal(mapErrorToToast({ code: 40201 }).title, "这份分享已失效，请重新获取");
  assert.equal(mapErrorToToast({ code: 40202 }).title, "分享课表数据异常，请重新获取");
  assert.equal(mapErrorToToast({ code: 40203 }).title, "同步码正在生成，请再试一次");
  assert.equal(mapErrorToToast({ code: 40204 }).title, "当前保存方式暂不可用");
  assert.equal(mapErrorToToast({ code: 40205 }).title, "保存内容已变化，请重新确认");
  assert.equal(mapErrorToToast({ code: 40206 }).title, "这次保存已撤销，请刷新");
  assert.equal(mapErrorToToast({ code: 40207 }).title, "分享功能暂未开放");
  assert.equal(mapErrorToToast({ code: 40208 }).title, "分享封面正在生成，请再试一次");
});

test("falls back to range toast for unmapped namespace codes", () => {
  // 30050 落 30001-30099 community 段
  assert.equal(mapErrorToToast({ code: 30050 }).title, "刚刚没能完成，请稍后再试");
  // 40080 落 40001-40099 ai 段
  assert.equal(mapErrorToToast({ code: 40080 }).title, "AI 正在忙，请稍后再试");
  // 30450 落 30401-30499 schedule 段
  assert.equal(mapErrorToToast({ code: 30450 }).title, "课表暂时没有更新，请稍后再试");
  // 40250 落 40201-40299 syncpkg 段
  assert.equal(mapErrorToToast({ code: 40250 }).title, "分享课表暂时没有更新，请稍后再试");
});

test("reads code from error.body / error.data when error.code missing", () => {
  assert.equal(mapErrorToToast({ body: { code: 30001 } }).title, "这篇内容暂时找不到了");
  assert.equal(mapErrorToToast({ data: { code: 40001 } }).title, "本期 AI 使用额度已用完");
});

test("overrides win over default mapping", () => {
  const out = mapErrorToToast(
    { code: 30001 },
    { overrides: { 30001: { title: "custom", icon: "error" } } },
  );
  assert.equal(out.title, "custom");
  assert.equal(out.icon, "error");
});

test("unmapped error returns generic toast (codex P10 PR#121, 2026-05-12)", () => {
  // 不再直显 backend message — 用通用 fallback,详情留 console
  assert.deepEqual(mapErrorToToast({ message: "网络错误" }), DEFAULT_GENERIC_TOAST);
});

test("uses generic default when nothing matches", () => {
  assert.deepEqual(mapErrorToToast(null), DEFAULT_GENERIC_TOAST);
  assert.deepEqual(mapErrorToToast({}), DEFAULT_GENERIC_TOAST);
});

test("builds safe inline copy without exposing transport or backend messages", () => {
  assert.equal(
    getUserFacingMessage({ message: "request:fail timeout" }, "内容暂时没有更新"),
    "网络有点忙，请稍后再试",
  );
  assert.equal(
    getUserFacingMessage({ message: "sql: duplicate key" }, "内容暂时没有更新"),
    "内容暂时没有更新",
  );
  assert.equal(
    getUserFacingMessage({ code: 30003, message: "internal moderation state" }),
    "内容正在审核，请耐心等待",
  );
});
