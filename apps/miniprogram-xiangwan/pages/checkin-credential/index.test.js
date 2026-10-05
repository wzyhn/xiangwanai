"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const { createCheckinCredentialPageDefinition } = require("./index");

const REGISTRATION_ID = "11111111-1111-4111-8111-111111111111";
const SERIES_ID = "22222222-2222-4222-8222-222222222222";
const INSTANCE_ID = "33333333-3333-4333-8333-333333333333";
const SESSION_ID = "44444444-4444-4444-8444-444444444444";
const CREDENTIAL_JTI = "55555555-5555-4555-8555-555555555555";
const QR_TOKEN = `xw1.${CREDENTIAL_JTI}.${"A".repeat(43)}`;

function flush() {
  return new Promise((resolve) => setImmediate(resolve));
}

function credentialFixture() {
  return {
    registration_id: REGISTRATION_ID,
    series_id: SERIES_ID,
    instance_id: INSTANCE_ID,
    session_id: SESSION_ID,
    credential_jti: CREDENTIAL_JTI,
    credential_epoch: 1,
    qr_token: QR_TOKEN,
    backup_code: "0123-ABCD-EFGH",
    issued_at: "2026-09-14T09:00:00Z",
    expires_at: "2026-09-14T09:10:00Z",
  };
}

function pageHarness(api, startAt = "2026-09-14T09:00:05Z", overrides = {}) {
  const clock = { now: Date.parse(startAt), tick: null };
  const renderedTokens = [];
  let clears = 0;
  const page = createCheckinCredentialPageDefinition(api, {
    now: () => clock.now,
    setInterval(callback) {
      clock.tick = callback;
      return 1;
    },
    clearInterval() {
      clock.tick = null;
    },
    renderQr(_page, token, done) {
      renderedTokens.push(token);
      done();
    },
    clearQr() {
      clears += 1;
    },
    ...overrides,
  });
  page.setData = (changes, callback) => {
    page.data = { ...page.data, ...changes };
    if (typeof callback === "function") callback();
  };
  return { page, clock, renderedTokens, clears: () => clears };
}

test("credential page renders locally, never exposes QR plaintext in data, and clears on hide", async (t) => {
  let calls = 0;
  global.getApp = () => ({ ensureAuthenticated: async () => true });
  t.after(() => {
    delete global.getApp;
  });
  const harness = pageHarness({
    async issueCheckinCredential() {
      calls += 1;
      return credentialFixture();
    },
  });

  harness.page.onLoad({ registration_id: REGISTRATION_ID });
  harness.page.onShow();
  await flush();
  await flush();

  assert.equal(calls, 1);
  assert.deepEqual(harness.renderedTokens, [QR_TOKEN]);
  assert.equal(harness.page.data.qrReady, true);
  assert.equal(harness.page.data.backupCode, "0123-ABCD-EFGH");
  assert.doesNotMatch(JSON.stringify(harness.page.data), /xw1\./);
  assert.equal(harness.page.data.canRefresh, false);

  harness.clock.now += 31 * 1000;
  harness.page._updateClock();
  assert.equal(harness.page.data.canRefresh, true);

  const clearCount = harness.clears();
  harness.page.onHide();
  assert.equal(harness.page.data.backupCode, "");
  assert.equal(harness.page.data.hasCredential, false);
  assert.ok(harness.clears() > clearCount);
});

test("unload clears retained backup plaintext even when no setData call is possible", async (t) => {
  global.getApp = () => ({ ensureAuthenticated: async () => true });
  t.after(() => {
    delete global.getApp;
  });
  const harness = pageHarness({
    async issueCheckinCredential() {
      return credentialFixture();
    },
  });

  harness.page.onLoad({ registration_id: REGISTRATION_ID });
  harness.page.onShow();
  await flush();
  await flush();
  assert.equal(harness.page.data.backupCode, "0123-ABCD-EFGH");

  harness.page.onUnload();

  assert.equal(harness.page.data.backupCode, "");
  assert.equal(harness.page.data.hasCredential, false);
  assert.equal(harness.page._registrationId, "");
});

test("countdown uses the server-issued TTL instead of trusting device clock offset", async (t) => {
  global.getApp = () => ({ ensureAuthenticated: async () => true });
  t.after(() => {
    delete global.getApp;
  });
  const harness = pageHarness(
    {
      async issueCheckinCredential() {
        return credentialFixture();
      },
    },
    "2026-09-14T12:00:00Z",
  );

  harness.page.onLoad({ registration_id: REGISTRATION_ID });
  harness.page.onShow();
  await flush();
  await flush();

  assert.equal(harness.page.data.hasCredential, true);
  assert.equal(harness.page.data.secondsRemaining, 10 * 60);
});

test("back navigation falls through to the exact registration after a cold page restore", (t) => {
  const redirects = [];
  global.wx = {
    navigateBack(options) {
      options.fail();
    },
    redirectTo(options) {
      redirects.push(options);
    },
  };
  t.after(() => {
    delete global.wx;
  });
  const harness = pageHarness({});
  harness.page.onLoad({ registration_id: REGISTRATION_ID });

  harness.page.backToRegistration();

  assert.deepEqual(redirects, [
    {
      url: `/pages/registration-detail/index?registration_id=${REGISTRATION_ID}`,
    },
  ]);
});

test("credential page honors Retry-After before enabling another issue", async (t) => {
  global.getApp = () => ({ ensureAuthenticated: async () => true });
  t.after(() => {
    delete global.getApp;
  });
  const harness = pageHarness({
    async issueCheckinCredential() {
      const error = new Error("rate limited");
      error.statusCode = 429;
      error.retryAfterSeconds = 12;
      throw error;
    },
  });

  harness.page.onLoad({ registration_id: REGISTRATION_ID });
  harness.page.onShow();
  await flush();
  await flush();

  assert.match(harness.page.data.errorMessage, /12 秒/);
  assert.equal(harness.page.data.refreshWaitSeconds, 12);
  assert.equal(harness.page.data.canRefresh, false);

  harness.clock.now += 12 * 1000;
  harness.page._updateClock();
  assert.equal(harness.page.data.canRefresh, true);
});

test("unknown credential issuance backs off with no blind retry", async (t) => {
  let calls = 0;
  global.getApp = () => ({ ensureAuthenticated: async () => true });
  t.after(() => {
    delete global.getApp;
  });
  const harness = pageHarness({
    async issueCheckinCredential() {
      calls += 1;
      throw new Error("request timeout");
    },
  });

  harness.page.onLoad({ registration_id: REGISTRATION_ID });
  harness.page.onShow();
  await flush();
  await flush();

  assert.equal(calls, 1);
  assert.match(harness.page.data.errorMessage, /生成结果暂未确认/);
  assert.equal(harness.page.data.refreshWaitSeconds, 5);
  assert.equal(harness.page.data.canRefresh, false);

  harness.page.onShow();
  await flush();
  assert.equal(calls, 1);

  harness.clock.now += 30 * 1000;
  harness.page._updateClock();
  assert.equal(harness.page.data.canRefresh, true);
});

test("login throttling keeps its own retry boundary and never claims credential issuance", async (t) => {
  let credentialCalls = 0;
  global.getApp = () => ({
    async ensureAuthenticated() {
      const error = new Error("login rate limited");
      error.statusCode = 429;
      throw error;
    },
  });
  t.after(() => {
    delete global.getApp;
  });
  const harness = pageHarness({
    async issueCheckinCredential() {
      credentialCalls += 1;
      return credentialFixture();
    },
  });

  harness.page.onLoad({ registration_id: REGISTRATION_ID });
  harness.page.onShow();
  await flush();
  await flush();

  assert.equal(credentialCalls, 0);
  assert.match(harness.page.data.errorMessage, /登录请求过于频繁/);
  assert.doesNotMatch(harness.page.data.errorMessage, /凭证刚刚生成/);
  assert.equal(harness.page.data.refreshWaitSeconds, 60);
  assert.equal(harness.page.data.canRefresh, false);
});

test("successful issue starts the local refresh floor when the response arrives", async (t) => {
  let resolveIssue;
  global.getApp = () => ({ ensureAuthenticated: async () => true });
  t.after(() => {
    delete global.getApp;
  });
  const harness = pageHarness({
    issueCheckinCredential() {
      return new Promise((resolve) => {
        resolveIssue = resolve;
      });
    },
  });

  harness.page.onLoad({ registration_id: REGISTRATION_ID });
  harness.page.onShow();
  await flush();
  harness.clock.now += 45 * 1000;
  resolveIssue(credentialFixture());
  await flush();

  assert.equal(harness.page.data.hasCredential, true);
  assert.equal(harness.page.data.refreshWaitSeconds, 5);
  assert.equal(harness.page.data.canRefresh, false);

  harness.clock.now += 30 * 1000;
  harness.page._updateClock();
  assert.equal(harness.page.data.canRefresh, true);
});

test("an in-flight issue remains single-flight across hide and show", async (t) => {
  let calls = 0;
  let resolveIssue;
  global.getApp = () => ({ ensureAuthenticated: async () => true });
  t.after(() => {
    delete global.getApp;
  });
  const harness = pageHarness({
    issueCheckinCredential() {
      calls += 1;
      return new Promise((resolve) => {
        resolveIssue = resolve;
      });
    },
  });

  harness.page.onLoad({ registration_id: REGISTRATION_ID });
  harness.page.onShow();
  await flush();
  assert.equal(calls, 1);

  harness.page.onHide();
  harness.page.onShow();
  await flush();
  assert.equal(calls, 1, "foregrounding must not start a duplicate credential POST");
  assert.equal(harness.page.data.loading, true);
  assert.match(harness.page.data.noticeMessage, /正在确认/);

  const raw = credentialFixture();
  resolveIssue(raw);
  await flush();

  assert.equal(raw.qr_token, "");
  assert.equal(raw.backup_code, "");
  assert.equal(calls, 1);
  assert.equal(harness.page.data.hasCredential, false);
  assert.equal(harness.page.data.loading, false);
  assert.equal(harness.page.data.refreshWaitSeconds, 5);
  assert.equal(harness.page.data.canRefresh, false);

  harness.clock.now += 30 * 1000;
  harness.page._updateClock();
  assert.equal(harness.page.data.canRefresh, true);
});

test("an issue response arriving after page hide is scrubbed and never rendered", async (t) => {
  let resolveIssue;
  global.getApp = () => ({ ensureAuthenticated: async () => true });
  t.after(() => {
    delete global.getApp;
  });
  const harness = pageHarness({
    issueCheckinCredential() {
      return new Promise((resolve) => {
        resolveIssue = resolve;
      });
    },
  });

  harness.page.onLoad({ registration_id: REGISTRATION_ID });
  harness.page.onShow();
  await flush();
  harness.page.onHide();
  const raw = credentialFixture();
  resolveIssue(raw);
  await flush();

  assert.equal(raw.qr_token, "");
  assert.equal(raw.backup_code, "");
  assert.equal(harness.renderedTokens.length, 0);
  assert.equal(harness.page.data.backupCode, "");
});

test("expiry erases the displayed backup secret and canvas", async (t) => {
  global.getApp = () => ({ ensureAuthenticated: async () => true });
  t.after(() => {
    delete global.getApp;
  });
  const harness = pageHarness({
    async issueCheckinCredential() {
      return credentialFixture();
    },
  });

  harness.page.onLoad({ registration_id: REGISTRATION_ID });
  harness.page.onShow();
  await flush();
  await flush();
  const clearCount = harness.clears();

  harness.clock.now = Date.parse("2026-09-14T09:10:05Z");
  harness.page._updateClock();

  assert.equal(harness.page.data.expired, true);
  assert.equal(harness.page.data.hasCredential, false);
  assert.equal(harness.page.data.backupCode, "");
  assert.ok(harness.clears() > clearCount);
});
