"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const { createRegistrationConfirmPageDefinition, registrationConflictReason } = require("./index");

const SESSION_ID = "11111111-1111-4111-8111-111111111111";
const REGISTRATION_ID = "22222222-2222-4222-8222-222222222222";
const OPERATION_ID = "33333333-3333-4333-8333-333333333333";
const ORDER_ID = "77777777-7777-4777-8777-777777777777";

test("final confirmation can reopen both acknowledged policies", (t) => {
  let privacyCalls = 0;
  const draft = {
    sessionId: SESSION_ID,
    idempotencyKey: OPERATION_ID,
    payload: { instance_publication_version: 1, price_cents: 0 },
    presentation: {
      title: "AI 共创夜",
      sessionTitle: "周末场",
      startAt: "2026-09-20T11:00:00Z",
      endAt: "2026-09-20T13:00:00Z",
      contactPolicyText: "联系人仅用于本次活动联络。",
      questionnaireAnswers: [],
    },
  };
  global.getApp = () => ({ getRegistrationDraft: () => draft });
  global.wx = {
    openPrivacyContract(options) {
      privacyCalls += 1;
      options.success();
    },
  };
  t.after(() => {
    delete global.getApp;
    delete global.wx;
  });
  const page = createRegistrationConfirmPageDefinition({});
  page.setData = (changes) => {
    page.data = { ...page.data, ...changes };
  };
  page.onLoad();

  page.openPrivacyContract();
  page.toggleContactPolicy();

  assert.equal(privacyCalls, 1);
  assert.equal(page.data.contactPolicyExpanded, true);
  assert.equal(page.data.draft.contactPolicyText, "联系人仅用于本次活动联络。");
});

test("unknown submit result retries with the original operation key", async (t) => {
  const calls = [];
  let attempt = 0;
  const draft = {
    sessionId: SESSION_ID,
    idempotencyKey: OPERATION_ID,
    payload: { instance_publication_version: 1, price_cents: 0 },
    presentation: {
      title: "AI 共创夜",
      sessionTitle: "周末场",
      startAt: "2026-09-20T11:00:00Z",
      questionnaireAnswers: [],
    },
  };
  global.getApp = () => ({
    getRegistrationDraft: () => draft,
    setRegistrationReceipt() {},
    clearRegistrationDraft() {},
  });
  t.after(() => {
    delete global.getApp;
  });
  const api = {
    async createRegistration(sessionId, payload, operationKey) {
      calls.push({ sessionId, payload, operationKey });
      attempt += 1;
      if (attempt === 1) throw new Error("request:fail timeout");
      return { registration_id: REGISTRATION_ID };
    },
  };
  const page = createRegistrationConfirmPageDefinition(api);
  page.setData = (changes) => {
    page.data = { ...page.data, ...changes };
  };
  page.onLoad();

  await page.submitRegistration();
  assert.match(page.data.submitMessage, /同一操作安全重试/);
  await page.submitRegistration();

  assert.equal(calls.length, 2);
  assert.equal(calls[0].operationKey, OPERATION_ID);
  assert.equal(calls[1].operationKey, OPERATION_ID);
  assert.equal(page.data.submitted, true);
  assert.equal(page.data.draft, null);
});

test("a successful submission relaunches the success page and clears the completed form stack", async (t) => {
  const navigations = [];
  let cleared = 0;
  const draft = {
    sessionId: SESSION_ID,
    idempotencyKey: OPERATION_ID,
    payload: { instance_publication_version: 1, price_cents: 0 },
    presentation: {
      title: "AI 共创夜",
      sessionTitle: "周末场",
      startAt: "2026-09-20T11:00:00Z",
      endAt: "2026-09-20T13:00:00Z",
      venue: "和平区某共创空间",
      questionnaireAnswers: [],
    },
  };
  global.getApp = () => ({
    getRegistrationDraft: () => draft,
    setRegistrationReceipt() {},
    clearRegistrationDraft() {
      cleared += 1;
    },
  });
  global.wx = {
    reLaunch(options) {
      navigations.push({ method: "reLaunch", url: options.url });
    },
    redirectTo(options) {
      navigations.push({ method: "redirectTo", url: options.url });
    },
  };
  t.after(() => {
    delete global.getApp;
    delete global.wx;
  });
  const page = createRegistrationConfirmPageDefinition({
    async createRegistration() {
      return { registration_id: REGISTRATION_ID };
    },
  });
  page.setData = (changes) => {
    page.data = { ...page.data, ...changes };
  };
  page.onLoad();

  await page.submitRegistration();

  assert.equal(cleared, 1);
  assert.equal(page._draft, null);
  assert.equal(page.data.draft, null);
  assert.deepEqual(navigations, [
    {
      method: "reLaunch",
      url:
        `/pages/registration-success/index?registration_id=${REGISTRATION_ID}` +
        `&title=${encodeURIComponent("AI 共创夜")}` +
        `&session_title=${encodeURIComponent("周末场")}` +
        `&start_at=${encodeURIComponent("2026-09-20T11:00:00Z")}` +
        `&end_at=${encodeURIComponent("2026-09-20T13:00:00Z")}` +
        `&venue=${encodeURIComponent("和平区某共创空间")}`,
    },
  ]);
});

test("paid registration routes to its exact Order instead of a success claim", async (t) => {
  const draft = {
    sessionId: SESSION_ID,
    idempotencyKey: OPERATION_ID,
    payload: { instance_publication_version: 1, price_cents: 8800 },
    presentation: { title: "AI 共创夜", questionnaireAnswers: [] },
  };
  const routes = [];
  global.getApp = () => ({
    getRegistrationDraft: () => draft,
    setRegistrationReceipt() {},
    clearRegistrationDraft() {},
  });
  global.wx = {
    reLaunch(options) {
      routes.push(options.url);
    },
  };
  t.after(() => {
    delete global.getApp;
    delete global.wx;
  });
  const page = createRegistrationConfirmPageDefinition({
    async createRegistration() {
      return {
        registration_id: REGISTRATION_ID,
        next_action: "wechat_payment_required",
        order: { order_id: ORDER_ID, payable_cents: 8800, payment_status: "pending" },
      };
    },
  });
  page.setData = (changes) => {
    page.data = { ...page.data, ...changes };
  };
  page.onLoad();
  await page.submitRegistration();
  assert.deepEqual(routes, [`/pages/order-detail/index?order_id=${ORDER_ID}`]);
});

test("confirmation offers only applicable coupons and submits the selected id", async (t) => {
  const draft = {
    sessionId: SESSION_ID,
    idempotencyKey: OPERATION_ID,
    payload: { instance_publication_version: 1, price_cents: 8800 },
    presentation: {
      title: "AI 共创夜",
      seriesId: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
      activityTypeCode: "ai_roundtable",
      priceCents: 8800,
      questionnaireAnswers: [],
    },
  };
  const calls = [];
  global.getApp = () => ({
    getRegistrationDraft: () => draft,
    setRegistrationReceipt() {},
    clearRegistrationDraft() {},
  });
  t.after(() => {
    delete global.getApp;
  });
  const page = createRegistrationConfirmPageDefinition({
    async getMyCoupons() {
      return {
        active_state: "available",
        as_of: "2026-09-20T00:00:00Z",
        items: [
          {
            coupon_id: "44444444-4444-4444-8444-444444444444",
            face_value_cents: 1000,
            minimum_order_cents: 0,
            valid_from: "2026-09-01T00:00:00Z",
            expires_at: "2026-12-01T00:00:00Z",
            grant_kind: "initial_guest",
            applicability: { scope_type: "activity_type", activity_type: "ai_roundtable" },
            state: "available",
            usable: true,
            correction_required: false,
          },
          {
            coupon_id: "55555555-5555-4555-8555-555555555555",
            face_value_cents: 1000,
            minimum_order_cents: 0,
            valid_from: "2026-09-01T00:00:00Z",
            expires_at: "2026-12-01T00:00:00Z",
            grant_kind: "initial_guest",
            applicability: {
              scope_type: "series",
              series_id: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
            },
            state: "available",
            usable: true,
            correction_required: false,
          },
        ],
      };
    },
    async createRegistration(sessionId, payload, operationKey) {
      calls.push({ sessionId, payload, operationKey });
      return { registration_id: REGISTRATION_ID };
    },
  });
  page.setData = (changes) => {
    page.data = { ...page.data, ...changes };
  };
  page.onLoad();
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(page.data.coupons.length, 1);
  page.selectCoupon({ currentTarget: { dataset: { couponId: page.data.coupons[0].couponId } } });
  await page.submitRegistration();
  assert.equal(calls[0].payload.coupon_id, "44444444-4444-4444-8444-444444444444");
});

test("business conflict preserves the draft and requests a server refresh", async (t) => {
  const draft = {
    sessionId: SESSION_ID,
    idempotencyKey: OPERATION_ID,
    payload: { instance_publication_version: 1, price_cents: 0 },
    presentation: {
      title: "AI 共创夜",
      startAt: "2026-09-20T11:00:00Z",
      endAt: "2026-09-20T13:00:00Z",
      questionnaireAnswers: [],
    },
  };
  let conflictSessionId = "";
  global.getApp = () => ({
    getRegistrationDraft: () => draft,
    markRegistrationConflict(value) {
      conflictSessionId = value;
    },
  });
  t.after(() => {
    delete global.getApp;
  });
  const api = {
    async createRegistration() {
      const error = new Error("conflict");
      error.statusCode = 409;
      error.body = { data: { reason: "registration_facts_changed" } };
      throw error;
    },
  };
  const page = createRegistrationConfirmPageDefinition(api);
  page.setData = (changes) => {
    page.data = { ...page.data, ...changes };
  };
  page.onLoad();

  await page.submitRegistration();

  assert.equal(conflictSessionId, SESSION_ID);
  assert.equal(page._draft, draft);
  assert.equal(page.data.conflicted, true);
  assert.match(page.data.submitMessage, /已保留填写内容/);
});

test("terminal registration conflicts cannot re-enter the stale refresh loop", async (t) => {
  const draft = {
    sessionId: SESSION_ID,
    idempotencyKey: OPERATION_ID,
    payload: { instance_publication_version: 1, price_cents: 0 },
    presentation: { title: "AI 共创夜", questionnaireAnswers: [] },
  };
  let cleared = 0;
  let marked = 0;
  const navigations = [];
  global.getApp = () => ({
    getRegistrationDraft: () => draft,
    clearRegistrationDraft() {
      cleared += 1;
    },
    markRegistrationConflict() {
      marked += 1;
    },
  });
  global.wx = {
    reLaunch(options) {
      navigations.push(options.url);
    },
  };
  t.after(() => {
    delete global.getApp;
    delete global.wx;
  });
  const api = {
    async createRegistration() {
      const error = new Error("already open");
      error.statusCode = 409;
      error.body = { data: { reason: "registration_already_open" } };
      throw error;
    },
  };
  const page = createRegistrationConfirmPageDefinition(api);
  page.setData = (changes) => {
    page.data = { ...page.data, ...changes };
  };
  page.onLoad();

  await page.submitRegistration();
  page.openMyRegistrations();

  assert.equal(cleared, 1);
  assert.equal(marked, 0);
  assert.equal(page._draft, null);
  assert.equal(page.data.conflictAction, "open_registrations");
  assert.deepEqual(navigations, ["/pages/my-registrations/index"]);
});

test("idempotency conflicts discard the occupied operation key", async (t) => {
  assert.equal(
    registrationConflictReason({
      statusCode: 409,
      body: { data: { reason: "idempotency_key_conflict" } },
    }),
    "idempotency_key_conflict",
  );
  assert.equal(registrationConflictReason({ statusCode: 409, body: {} }), "unknown_conflict");

  const draft = {
    sessionId: SESSION_ID,
    idempotencyKey: OPERATION_ID,
    payload: { instance_publication_version: 1, price_cents: 0 },
    presentation: { title: "AI 共创夜", questionnaireAnswers: [] },
  };
  let cleared = 0;
  global.getApp = () => ({
    getRegistrationDraft: () => draft,
    clearRegistrationDraft() {
      cleared += 1;
    },
  });
  t.after(() => {
    delete global.getApp;
  });
  const page = createRegistrationConfirmPageDefinition({
    async createRegistration() {
      const error = new Error("occupied operation key");
      error.statusCode = 409;
      error.body = { data: { reason: "idempotency_key_conflict" } };
      throw error;
    },
  });
  page.setData = (changes) => {
    page.data = { ...page.data, ...changes };
  };
  page.onLoad();

  await page.submitRegistration();

  assert.equal(cleared, 1);
  assert.equal(page._draft, null);
  assert.equal(page.data.conflictAction, "new_submission");
});

test("a failed submit settles quietly after the confirmation page is unloaded", async (t) => {
  let rejectSubmit;
  const draft = {
    sessionId: SESSION_ID,
    idempotencyKey: OPERATION_ID,
    payload: { instance_publication_version: 1, price_cents: 0 },
    presentation: { title: "AI 共创夜", questionnaireAnswers: [] },
  };
  global.getApp = () => ({ getRegistrationDraft: () => draft });
  t.after(() => {
    delete global.getApp;
  });
  const page = createRegistrationConfirmPageDefinition({
    createRegistration() {
      return new Promise((_resolve, reject) => {
        rejectSubmit = reject;
      });
    },
  });
  page.setData = (changes) => {
    page.data = { ...page.data, ...changes };
  };
  page.onLoad();

  const pending = page.submitRegistration();
  page.onUnload();
  rejectSubmit(new Error("request:fail timeout"));

  assert.equal(await pending, null);
  assert.equal(page._draft, draft);
});

test("an expired memory-only draft falls back to its exact session when the stack is gone", (t) => {
  const calls = [];
  global.getApp = () => ({ getRegistrationDraft: () => null });
  global.wx = {
    navigateBack(options) {
      calls.push({ method: "navigateBack", delta: options.delta });
      options.fail();
    },
    redirectTo(options) {
      calls.push({ method: "redirectTo", url: options.url });
    },
  };
  t.after(() => {
    delete global.getApp;
    delete global.wx;
  });
  const page = createRegistrationConfirmPageDefinition({});
  page.setData = (changes) => {
    page.data = { ...page.data, ...changes };
  };

  page.onLoad({ session_id: SESSION_ID });
  page.backToDetail();

  assert.match(page.data.invalidMessage, /草稿已失效/);
  assert.deepEqual(calls, [
    { method: "navigateBack", delta: 2 },
    {
      method: "redirectTo",
      url: `/pages/session-detail/index?session_id=${SESSION_ID}`,
    },
  ]);
});

test("an invalid confirmation link relaunches home when navigation back fails", (t) => {
  const calls = [];
  global.getApp = () => ({ getRegistrationDraft: () => null });
  global.wx = {
    navigateBack(options) {
      options.fail();
    },
    reLaunch(options) {
      calls.push(options.url);
    },
  };
  t.after(() => {
    delete global.getApp;
    delete global.wx;
  });
  const page = createRegistrationConfirmPageDefinition({});
  page.setData = (changes) => {
    page.data = { ...page.data, ...changes };
  };

  page.onLoad({ session_id: "invalid" });
  page.backToDetail();

  assert.deepEqual(calls, ["/pages/index/index"]);
});
