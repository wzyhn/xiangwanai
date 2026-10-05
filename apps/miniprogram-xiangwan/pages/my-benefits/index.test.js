"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const { createMyBenefitsPageDefinition } = require("./index");

function flush() {
  return new Promise((resolve) => setImmediate(resolve));
}

function attachSetData(page) {
  page.setData = (changes) => {
    page.data = { ...page.data, ...changes };
  };
}

function emptyBenefits() {
  return {
    has_host_identity: false,
    current_roles: [],
    role_history: [],
    host_rules: { state: "pending" },
    can_apply_for_host: false,
    host_application_history: [],
    host_contribution_count: 0,
    host_contribution_history: [],
    identity_history_available: false,
  };
}

test("My Benefits waits for hydration and never triggers login on entry", async (t) => {
  let resolveRuntime;
  let loginCalls = 0;
  let benefitCalls = 0;
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
  const page = createMyBenefitsPageDefinition({
    async getMyBenefits() {
      benefitCalls += 1;
      return emptyBenefits();
    },
  });
  attachSetData(page);
  page.onLoad();
  page.onShow();

  resolveRuntime(true);
  await flush();

  assert.equal(loginCalls, 0);
  assert.equal(benefitCalls, 0);
  assert.equal(page.data.needsLogin, true);
});

test("My Benefits reloads the owner-scoped snapshot for an authenticated user", async (t) => {
  let calls = 0;
  const app = {
    runtimeReady: Promise.resolve(),
    weconqAuth: { hasValidSession: () => true },
  };
  global.getApp = () => app;
  t.after(() => delete global.getApp);
  const page = createMyBenefitsPageDefinition({
    async getMyBenefits() {
      calls += 1;
      return emptyBenefits();
    },
  });
  attachSetData(page);
  page.onLoad();
  page.onShow();
  await flush();
  await flush();

  assert.equal(calls, 1);
  assert.equal(page.data.needsLogin, false);
  assert.equal(page.data.benefits.identityHistoryAvailable, false);
});

test("My Benefits removes private facts when the account loses authorization", async () => {
  const page = createMyBenefitsPageDefinition({
    async getMyBenefits() {
      throw Object.assign(new Error("principal inactive"), { statusCode: 403 });
    },
  });
  attachSetData(page);
  page._active = true;
  page.data.benefits = { identityHistoryAvailable: true };

  await page.loadBenefits();

  assert.equal(page.data.benefits, null);
  assert.equal(page.data.needsLogin, false);
  assert.match(page.data.errorMessage, /账号/);
});
