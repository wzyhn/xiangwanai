"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const { createRegistrationDetailPageDefinition, defaultConfirmCancellation } = require("./index");
const { createXiangwanApi } = require("../../services/xiangwan-api");
const { createPoliciesPageDefinition } = require("../policies/index");
const { findCancellationPolicy } = require("../../features/registration/model");

const REGISTRATION_ID = "11111111-1111-4111-8111-111111111111";
const SESSION_ID = "22222222-2222-4222-8222-222222222222";

function cancellationFixture(overrides = {}) {
  return {
    registration_id: REGISTRATION_ID,
    session_id: SESSION_ID,
    participation_status: "cancelled",
    registration_version: 2,
    cancelled_at: "2026-09-14T10:00:00Z",
    next_action: "cancellation_completed",
    ...overrides,
  };
}

function detailResponse(cancellationAction = "unavailable", overrides = {}) {
  const { registration: registrationOverrides = {}, ...responseOverrides } = overrides;
  const cancelled =
    cancellationAction === "unavailable" && responseOverrides.cancellation_policy_required !== true;
  const registration = {
    registration_id: REGISTRATION_ID,
    registration_version: cancelled ? 2 : 1,
    session_id: SESSION_ID,
    session_title: "AI 共创夜",
    instance_title: "九月场",
    state: cancelled ? "cancelled" : "registered",
    participation_status: cancelled ? "cancelled" : "confirmed",
    session_start_at: "2026-09-20T11:00:00Z",
    session_end_at: "2026-09-20T13:00:00Z",
    delivery_mode: "offline",
    area: "hexi",
    venue_name: "海河实验室",
    has_active_access: !cancelled,
    can_continue_payment: false,
    checkin: { status: "not_recorded" },
    ...registrationOverrides,
  };
  return {
    registration,
    cancellation_action: cancellationAction,
    checkin_credential_eligible: cancellationAction !== "unavailable",
    private_access_eligible: cancellationAction !== "unavailable",
    cancellation_policy_required: false,
    ...responseOverrides,
  };
}

function pageHarness(api, confirmCancellation = async () => true) {
  const page = createRegistrationDetailPageDefinition(api, { confirmCancellation });
  page.setData = (changes) => {
    page.data = { ...page.data, ...changes };
  };
  page._active = true;
  page._registrationId = REGISTRATION_ID;
  page.data.detail = {
    registrationId: REGISTRATION_ID,
    sessionId: SESSION_ID,
    canSelfCancel: true,
    cancellationAction: "available",
    checkinCredentialEligible: true,
    hasActiveAccess: true,
  };
  return page;
}

test("default cancellation confirmation states the immediate irreversible effects", async (t) => {
  let modalOptions;
  global.wx = {
    showModal(options) {
      modalOptions = options;
      options.success({ confirm: true, cancel: false });
    },
  };
  t.after(() => {
    delete global.wx;
  });

  assert.equal(await defaultConfirmCancellation(), true);
  assert.equal(modalOptions.confirmText, "确认取消");
  assert.match(modalOptions.content, /参与资格、签到凭证和私密访问将立即失效/);
  assert.match(modalOptions.content, /名额会同步释放/);
});

function flush() {
  return new Promise((resolve) => setImmediate(resolve));
}

function publishedCancellationPolicy() {
  return {
    published_versions: [{ kind: "cancellation", version: "cancel-v1" }],
    capabilities: { paid_self_service_cancellation_available: true },
    cancellation_policy: {
      version: "cancel-v1",
      content: "取消后全额申请人工退款，提交取消不代表退款已到账。",
    },
  };
}

test("paid cancellation confirmation explains manual refund rather than immediate arrival", async (t) => {
  let options;
  global.wx = {
    showModal(value) {
      options = value;
      value.success({ confirm: true });
    },
  };
  t.after(() => {
    delete global.wx;
  });
  assert.equal(
    await defaultConfirmCancellation({ paymentStatus: "paid_confirmed", paymentText: "¥1.00" }),
    true,
  );
  assert.match(options.content, /本次申请退款金额：¥1\.00/);
  assert.match(options.content, /按实付金额全额申请人工退款/);
  assert.match(options.content, /提交取消不代表退款已到账/);
});

test("only published matching cancellation rules are displayed", () => {
  const policies = publishedCancellationPolicy();
  assert.deepEqual(findCancellationPolicy(policies), policies.cancellation_policy);
  assert.equal(findCancellationPolicy({ ...policies, published_versions: [] }), null);
  assert.equal(findCancellationPolicy({ ...policies, capabilities: {} }), null);
  assert.equal(
    findCancellationPolicy({
      ...policies,
      cancellation_policy: { version: "cancel-v2", content: "wrong" },
    }),
    null,
  );
  assert.equal(
    findCancellationPolicy({
      ...policies,
      cancellation_policy: { version: "cancel-v1", content: " " },
    }),
    null,
  );
});

test("policies page renders the authoritative cancellation and refund text", async () => {
  const policies = publishedCancellationPolicy();
  const page = createPoliciesPageDefinition({
    async getPublicPolicies() {
      return policies;
    },
  });
  page._active = true;
  page.setData = (changes) => {
    page.data = { ...page.data, ...changes };
  };
  await page.loadPolicies();
  assert.equal(page.data.cancellationVersion, "cancel-v1");
  assert.equal(page.data.cancellationText, policies.cancellation_policy.content);
});

test("paid detail shows server cancellation capability and published rules", async (t) => {
  global.getApp = () => ({ async ensureAuthenticated() {} });
  t.after(() => {
    delete global.getApp;
  });
  const policies = publishedCancellationPolicy();
  const page = pageHarness({
    async getMyRegistrationDetail() {
      return detailResponse("available", {
        registration: { order: { order_id: ORDER_ID, payment_status: "paid_confirmed" } },
      });
    },
    async getPublicPolicies() {
      return policies;
    },
  });
  await page.loadDetail();
  assert.equal(page.data.detail.canSelfCancel, true);
  assert.equal(page.data.detail.paymentStatus, "paid_confirmed");
  assert.equal(page.data.cancellationPolicyText, policies.cancellation_policy.content);
});

test("a global cancellation switch cannot override an ineligible paid registration", async (t) => {
  global.getApp = () => ({ async ensureAuthenticated() {} });
  t.after(() => {
    delete global.getApp;
  });
  const page = pageHarness({
    async getMyRegistrationDetail() {
      return detailResponse("unavailable", { cancellation_policy_required: true });
    },
    async getPublicPolicies() {
      return publishedCancellationPolicy();
    },
  });
  await page.loadDetail();
  assert.equal(page.data.detail.canSelfCancel, false);
  assert.notEqual(page.data.cancellationPolicyText, "");
});

const ORDER_ID = "33333333-3333-4333-8333-333333333333";

function setupExistingOrder(t, overrides = {}) {
  const routes = [];
  let binding = { principalId: "owner", generation: 1 };
  global.getApp = () => ({
    async ensureAuthenticated() {},
    getXiangwanAuthBinding: () => binding,
  });
  global.wx = { navigateTo: (options) => routes.push(options.url) };
  t.after(() => {
    delete global.getApp;
    delete global.wx;
  });
  const api = {
    async getMyRegistrationDetail() {
      return detailResponse("unavailable", {
        registration: {
          order: { order_id: ORDER_ID, payment_status: "pending_payment" },
          can_continue_payment: true,
        },
      });
    },
    async getMyOrderDetail(id) {
      assert.equal(id, ORDER_ID);
      return {
        order: { order_id: ORDER_ID, registration_id: REGISTRATION_ID, session_id: SESSION_ID },
        as_of: "2026-09-15T09:10:00Z",
      };
    },
    ...overrides,
  };
  return {
    page: pageHarness(api),
    routes,
    changeIdentity: () => {
      binding = { principalId: "other", generation: 2 };
    },
  };
}

test("existing order opens only after fresh owner registration and exact order relation reads", async (t) => {
  const { page, routes } = setupExistingOrder(t);
  await page.openExistingOrder();
  assert.deepEqual(routes, [`/pages/order-detail/index?order_id=${ORDER_ID}`]);
  assert.equal(page.data.openingOrder, false);
});

test("view order accepts the real API adapter envelope for a paid registration", async (t) => {
  const calls = [];
  const api = createXiangwanApi({
    request: async (options) => {
      calls.push(options);
      return {
        order: {
          order_id: ORDER_ID,
          order_version: 3,
          registration_id: REGISTRATION_ID,
          registration_version: 2,
          series_id: "44444444-4444-4444-8444-444444444444",
          series_title: "AI 圆桌",
          instance_id: "55555555-5555-4555-8555-555555555555",
          instance_title: "第二期",
          session_id: SESSION_ID,
          session_title: "周末场",
          session_start_at: "2026-09-20T02:00:00Z",
          session_end_at: "2026-09-20T04:00:00Z",
          participation_status: "confirmed",
          reservation_state: "registered",
          reservation_has_active_access: true,
          payment_status: "paid_confirmed",
          payment_confirmation_pending: false,
          original_price_cents: 100,
          discount_cents: 0,
          payable_cents: 100,
          actual_paid_cents: 100,
          paid_at: "2026-09-15T09:05:00Z",
          currency: "CNY",
          hold_status: "converted",
          hold_expires_at: "2026-09-15T09:10:00Z",
          hold_version: 2,
          can_continue_payment: false,
          state: "paid",
          outcome: "paid_confirmed",
          last_business_at: "2026-09-15T09:05:00Z",
        },
        as_of: "2026-09-15T09:10:00Z",
      };
    },
  });
  const { page, routes } = setupExistingOrder(t, { getMyOrderDetail: api.getMyOrderDetail });
  await page.openExistingOrder();
  assert.deepEqual(calls, [{ url: `/api/v1/xiangwan/me/orders/${ORDER_ID}` }]);
  assert.deepEqual(routes, [`/pages/order-detail/index?order_id=${ORDER_ID}`]);
  assert.equal(page.data.orderError, "");
});

test("existing order relation mismatches never navigate", async (t) => {
  for (const field of ["order_id", "registration_id", "session_id"]) {
    const { page, routes } = setupExistingOrder(t, {
      async getMyOrderDetail() {
        return {
          order: {
            order_id: ORDER_ID,
            registration_id: REGISTRATION_ID,
            session_id: SESSION_ID,
            [field]: "44444444-4444-4444-8444-444444444444",
          },
          as_of: "2026-09-15T09:10:00Z",
        };
      },
    });
    await page.openExistingOrder();
    assert.deepEqual(routes, []);
    assert.match(page.data.orderError, /订单暂时无法打开/);
  }
});

test("duplicate taps reuse one read and hidden or switched accounts cannot navigate", async (t) => {
  for (const invalidation of ["hidden", "identity"]) {
    let resolveOrder;
    let reads = 0;
    const { page, routes, changeIdentity } = setupExistingOrder(t, {
      getMyOrderDetail() {
        reads++;
        return new Promise((resolve) => {
          resolveOrder = resolve;
        });
      },
    });
    const first = page.openExistingOrder();
    await flush();
    await page.openExistingOrder();
    assert.equal(reads, 1);
    if (invalidation === "hidden") page.onHide();
    else changeIdentity();
    resolveOrder({
      order: { order_id: ORDER_ID, registration_id: REGISTRATION_ID, session_id: SESSION_ID },
      as_of: "2026-09-15T09:10:00Z",
    });
    await first;
    assert.deepEqual(routes, []);
    if (invalidation === "hidden") {
      page.onShow();
      assert.equal(page.data.openingOrder, false);
      await flush();
    }
  }
});

test("order read failures show a retryable message without rebuilding an order", async (t) => {
  const { page, routes } = setupExistingOrder(t, {
    async getMyOrderDetail() {
      throw new Error("network unknown");
    },
  });
  await page.openExistingOrder();
  assert.deepEqual(routes, []);
  assert.match(page.data.orderError, /订单暂时无法打开/);
  assert.equal(page.data.openingOrder, false);
});

test("registration detail refreshes from the server whenever it returns to the foreground", async (t) => {
  let calls = 0;
  global.getApp = () => ({
    consumeRegistrationReceipt: () => null,
    async ensureAuthenticated() {},
  });
  t.after(() => {
    delete global.getApp;
  });
  const page = createRegistrationDetailPageDefinition({
    async getMyRegistrationDetail() {
      calls += 1;
      return {
        registration: {
          registration_id: REGISTRATION_ID,
          state: calls === 1 ? "registered" : "ended",
        },
      };
    },
  });
  page.data = { ...page.data };
  page.setData = (changes) => {
    page.data = { ...page.data, ...changes };
  };

  page.onLoad({ registration_id: REGISTRATION_ID });
  page.onShow();
  await flush();
  await flush();
  assert.equal(calls, 1, "initial onShow must not duplicate the onLoad request");

  page.onShow();
  await flush();
  await flush();

  assert.equal(calls, 2);
  assert.equal(page.data.detail.state, "ended");
});

test("registration detail immediately renders an eligible credential and clears it on hide", async (t) => {
  let issued = 0;
  let rendered = 0;
  let eligible = true;
  global.getApp = () => ({ ensureAuthenticated: async () => true });
  t.after(() => delete global.getApp);
  const page = createRegistrationDetailPageDefinition(
    {
      async getMyRegistrationDetail() {
        return detailResponse(eligible ? "available" : "unavailable");
      },
      async issueCheckinCredential(id) {
        assert.equal(id, REGISTRATION_ID);
        issued += 1;
        return {
          registration_id: REGISTRATION_ID,
          session_id: SESSION_ID,
          series_id: SESSION_ID,
          instance_id: SESSION_ID,
          credential_jti: SESSION_ID,
          credential_epoch: 1,
          qr_token: `xw1.${SESSION_ID}.${"A".repeat(43)}`,
          backup_code: "0123-ABCD-EFGH",
          issued_at: "2026-09-21T06:00:00Z",
          expires_at: "2026-09-21T06:10:00Z",
        };
      },
    },
    {
      renderQr(_page, _token, done) {
        rendered += 1;
        done();
      },
      clearQr() {},
      checkin: {
        now: () => Date.parse("2026-09-21T06:00:00Z"),
        setInterval: () => 1,
        clearInterval() {},
      },
    },
  );
  page.setData = (changes, done) => {
    page.data = { ...page.data, ...changes };
    if (done) done();
  };
  page.onLoad({ registration_id: REGISTRATION_ID });
  page.onShow();
  await flush();
  await flush();
  assert.equal(issued, 1);
  assert.equal(rendered, 1);
  assert.equal(page.data.checkin.qrReady, true);
  assert.equal(page.data.checkin.backupCode, "0123-ABCD-EFGH");
  assert.equal(JSON.stringify(page.data).includes("xw1."), false);
  await page.loadDetail();
  assert.equal(issued, 1, "refreshing status must not rotate a usable credential");
  page.onHide();
  assert.equal(page.data.checkin.hasCredential, false);
  assert.equal(page.data.checkin.backupCode, "");
  eligible = false;
  page.onShow();
  await flush();
  await flush();
  assert.equal(issued, 1, "ineligible registration must not dispatch credential issuance");
  page.onUnload();
});

test("registration cancellation requires confirmation, submits once and reloads authority", async (t) => {
  let cancellationCalls = 0;
  let detailCalls = 0;
  global.getApp = () => ({ ensureAuthenticated: async () => true });
  t.after(() => {
    delete global.getApp;
  });
  const page = pageHarness({
    async cancelRegistration(registrationId) {
      cancellationCalls += 1;
      assert.equal(registrationId, REGISTRATION_ID);
      return cancellationFixture();
    },
    async getMyRegistrationDetail(registrationId) {
      detailCalls += 1;
      assert.equal(registrationId, REGISTRATION_ID);
      return detailResponse("unavailable");
    },
  });

  const result = await page.requestCancellation();

  assert.equal(cancellationCalls, 1);
  assert.equal(detailCalls, 1);
  assert.equal(result.nextAction, "cancellation_completed");
  assert.equal(page.data.cancellationResult.title, "报名已取消");
  assert.equal(page.data.detail.state, "cancelled");
  assert.equal(page.data.detail.stateLabel, "已取消");
  assert.equal(page.data.detail.participationStatus, "cancelled");
  assert.equal(page.data.detail.registrationVersion, 2);
  assert.equal(page.data.detail.canSelfCancel, false);
  assert.equal(page.data.detail.checkinCredentialEligible, false);
  assert.equal(page.data.detail.privateAccessEligible, false);
  assert.equal(page.data.detail.hasActiveAccess, false);
  assert.equal(page.data.detail.canContinuePayment, false);
  assert.equal(page.data.cancelling, false);
});

test("paid cancellation stays closed when registration denies it and public rules cannot load", async (t) => {
  let confirmations = 0;
  let policyCalls = 0;
  global.getApp = () => ({ ensureAuthenticated: async () => true });
  t.after(() => {
    delete global.getApp;
  });
  const page = pageHarness(
    {
      async getMyRegistrationDetail() {
        return detailResponse("unavailable", {
          cancellation_policy_required: true,
        });
      },
      async getPublicPolicies() {
        policyCalls += 1;
        throw new Error("published rule text temporarily unavailable");
      },
    },
    async () => {
      confirmations += 1;
      return false;
    },
  );

  await page.loadDetail();
  await page.requestCancellation();

  assert.equal(page.data.detail.canSelfCancel, false);
  assert.equal(page.data.detail.cancellationPolicyRequired, true);
  assert.equal(policyCalls, 1);
  assert.equal(confirmations, 0);
});

test("a confirmed receipt keeps stale detail safe when authority refresh fails", async (t) => {
  global.getApp = () => ({ ensureAuthenticated: async () => true });
  t.after(() => {
    delete global.getApp;
  });
  const page = pageHarness({
    async cancelRegistration() {
      return cancellationFixture({ registration_version: 3 });
    },
    async getMyRegistrationDetail() {
      throw new Error("temporarily unavailable");
    },
  });
  page.data.detail = {
    ...page.data.detail,
    registrationVersion: 2,
    state: "registered",
    stateLabel: "报名成功",
    participationStatus: "confirmed",
    privateAccessEligible: true,
    canContinuePayment: true,
    cancellationPolicyRequired: false,
  };

  await page.requestCancellation();

  assert.equal(page.data.detail.state, "cancelled");
  assert.equal(page.data.detail.stateLabel, "已取消");
  assert.equal(page.data.detail.participationStatus, "cancelled");
  assert.equal(page.data.detail.registrationVersion, 3);
  assert.equal(page.data.detail.privateAccessEligible, false);
  assert.equal(page.data.detail.canContinuePayment, false);
  assert.equal(page.data.detail.cancellationPolicyRequired, false);
  assert.equal(page.data.cancellationOutcomeUnknown, true);
});

test("a lagging authority response cannot reintroduce pre-cancellation access", async (t) => {
  global.getApp = () => ({ ensureAuthenticated: async () => true });
  t.after(() => {
    delete global.getApp;
  });
  const page = pageHarness({
    async cancelRegistration() {
      return cancellationFixture({ registration_version: 3 });
    },
    async getMyRegistrationDetail() {
      return detailResponse("available", {
        registration: { registration_version: 2 },
      });
    },
  });
  page.data.detail = {
    ...page.data.detail,
    registrationVersion: 2,
    state: "registered",
    stateLabel: "报名成功",
    participationStatus: "confirmed",
    privateAccessEligible: true,
    canContinuePayment: true,
    cancellationPolicyRequired: true,
  };

  await page.requestCancellation();

  assert.equal(page.data.detail.state, "cancelled");
  assert.equal(page.data.detail.stateLabel, "已取消");
  assert.equal(page.data.detail.participationStatus, "cancelled");
  assert.equal(page.data.detail.registrationVersion, 3);
  assert.equal(page.data.detail.canSelfCancel, false);
  assert.equal(page.data.detail.checkinCredentialEligible, false);
  assert.equal(page.data.detail.privateAccessEligible, false);
  assert.equal(page.data.detail.hasActiveAccess, false);
  assert.equal(page.data.detail.cancellationPolicyRequired, false);
  assert.equal(page.data.cancellationOutcomeUnknown, true);
  assert.match(page.data.cancellationNotice, /尚未更新/);
});

test("declining cancellation leaves the registration untouched", async () => {
  let cancellationCalls = 0;
  const page = pageHarness(
    {
      async cancelRegistration() {
        cancellationCalls += 1;
      },
    },
    async () => false,
  );

  const result = await page.requestCancellation();

  assert.equal(result, null);
  assert.equal(cancellationCalls, 0);
  assert.equal(page.data.detail.canSelfCancel, true);
});

test("confirmation and command are both single-flight", async (t) => {
  let releaseConfirmation;
  let cancellationCalls = 0;
  global.getApp = () => ({ ensureAuthenticated: async () => true });
  t.after(() => {
    delete global.getApp;
  });
  const page = pageHarness(
    {
      async cancelRegistration() {
        cancellationCalls += 1;
        return cancellationFixture();
      },
      async getMyRegistrationDetail() {
        return detailResponse("unavailable");
      },
    },
    () =>
      new Promise((resolve) => {
        releaseConfirmation = resolve;
      }),
  );

  const first = page.requestCancellation();
  const duplicate = await page.requestCancellation();
  assert.equal(duplicate, null);
  releaseConfirmation(true);
  await first;

  assert.equal(cancellationCalls, 1);
});

test("unknown cancellation outcome blocks blind resubmission until authority refresh", async (t) => {
  let cancellationCalls = 0;
  let detailAvailable = false;
  global.getApp = () => ({ ensureAuthenticated: async () => true });
  t.after(() => {
    delete global.getApp;
  });
  const page = pageHarness({
    async cancelRegistration() {
      cancellationCalls += 1;
      const error = new Error("response lost");
      error.statusCode = 503;
      throw error;
    },
    async getMyRegistrationDetail() {
      if (detailAvailable) return detailResponse("available");
      throw new Error("still offline");
    },
  });

  await page.requestCancellation();
  await page.requestCancellation();

  assert.equal(cancellationCalls, 1);
  assert.equal(page.data.cancellationOutcomeUnknown, true);
  assert.match(page.data.cancellationNotice, /不要连续重复提交/);

  detailAvailable = true;
  await page.loadDetail();
  assert.equal(page.data.cancellationOutcomeUnknown, false);
  assert.match(page.data.cancellationNotice, /状态已刷新/);
});

test("cancellation refresh prevents an older foreground response from restoring access", async (t) => {
  const detailResolvers = [];
  global.getApp = () => ({ ensureAuthenticated: async () => true });
  t.after(() => {
    delete global.getApp;
  });
  const page = pageHarness({
    async cancelRegistration() {
      const error = new Error("response lost");
      error.statusCode = 503;
      throw error;
    },
    getMyRegistrationDetail() {
      return new Promise((resolve) => detailResolvers.push(resolve));
    },
  });

  const foregroundRefresh = page.loadDetail();
  const cancellation = page.requestCancellation();
  await flush();
  await flush();
  assert.equal(detailResolvers.length, 2);

  detailResolvers[1](detailResponse("unavailable"));
  await cancellation;
  detailResolvers[0](detailResponse("available"));
  await foregroundRefresh;

  assert.equal(page.data.detail.state, "cancelled");
  assert.equal(page.data.detail.canSelfCancel, false);
  assert.equal(page.data.detail.checkinCredentialEligible, false);
  assert.equal(page.data.detail.privateAccessEligible, false);
  assert.equal(page.data.cancellationOutcomeUnknown, false);
  assert.equal(page.data.loading, false);
});

test("a cancellation conflict refreshes facts before another confirmation", async (t) => {
  global.getApp = () => ({ ensureAuthenticated: async () => true });
  t.after(() => {
    delete global.getApp;
  });
  const page = pageHarness({
    async cancelRegistration() {
      const error = new Error("conflict");
      error.statusCode = 409;
      throw error;
    },
    async getMyRegistrationDetail() {
      return detailResponse("available");
    },
  });

  await page.requestCancellation();

  assert.equal(page.data.cancellationOutcomeUnknown, false);
  assert.equal(page.data.detail.canSelfCancel, true);
  assert.match(page.data.cancellationNotice, /已刷新/);
});

test("server eligibility and exact registration identity gate the destructive action", async () => {
  let confirmations = 0;
  const page = pageHarness({}, async () => {
    confirmations += 1;
    return true;
  });

  page.data.detail.canSelfCancel = false;
  await page.requestCancellation();
  page.data.detail = {
    registrationId: SESSION_ID,
    sessionId: SESSION_ID,
    canSelfCancel: true,
  };
  await page.requestCancellation();

  assert.equal(confirmations, 0);
});
