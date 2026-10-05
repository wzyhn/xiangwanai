"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const { createMyRegistrationsPageDefinition } = require("./index");

function flush() {
  return new Promise((resolve) => setImmediate(resolve));
}

test("my registrations waits for session hydration without triggering login", async (t) => {
  let resolveRuntime;
  let calls = 0;
  const app = {
    runtimeReady: new Promise((resolve) => {
      resolveRuntime = resolve;
    }),
    weconqAuth: { hasValidSession: () => true },
  };
  global.getApp = () => app;
  t.after(() => {
    delete global.getApp;
  });
  const page = createMyRegistrationsPageDefinition({
    async getMyRegistrations() {
      calls += 1;
      return { active_state: "all", items: [], next_cursor: "" };
    },
  });
  page.setData = (changes) => {
    page.data = { ...page.data, ...changes };
  };
  page.onLoad();
  page.onShow();
  assert.equal(calls, 0);

  resolveRuntime(true);
  await flush();
  await flush();

  assert.equal(calls, 1);
  assert.equal(page.data.needsLogin, false);
});

test("my registrations presents an explicit login action for anonymous users", async (t) => {
  let calls = 0;
  global.getApp = () => ({
    runtimeReady: Promise.resolve(true),
    weconqAuth: { hasValidSession: () => false },
  });
  t.after(() => {
    delete global.getApp;
  });
  const page = createMyRegistrationsPageDefinition({
    async getMyRegistrations() {
      calls += 1;
      return {};
    },
  });
  page.setData = (changes) => {
    page.data = { ...page.data, ...changes };
  };
  page.onLoad();
  page.onShow();
  await flush();

  assert.equal(calls, 0);
  assert.equal(page.data.needsLogin, true);
  assert.equal(page.data.loading, false);
});
