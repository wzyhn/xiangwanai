"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const { createOrderDetailPageDefinition } = require("./index");

const ORDER_ID = "77777777-7777-4777-8777-777777777777";
const REGISTRATION_ID = "22222222-2222-4222-8222-222222222222";
const PRINCIPAL_ID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa";
const OPERATION_ID = "33333333-3333-4333-8333-333333333333";

function flush() {
  return new Promise((resolve) => setImmediate(resolve));
}

test("order detail authenticates and reads the exact Order", async (t) => {
  const calls = [];
  global.getApp = () => ({
    async ensureAuthenticated() {
      calls.push("login");
    },
  });
  t.after(() => delete global.getApp);
  const page = createOrderDetailPageDefinition({
    async getMyOrderDetail(id) {
      calls.push(id);
      return {
        order: {
          order_id: id,
          registration_id: REGISTRATION_ID,
          state: "closed",
          outcome: "closed_unpaid",
        },
        as_of: "2026-09-15T09:00:00Z",
      };
    },
  });
  page.setData = (changes) => {
    page.data = { ...page.data, ...changes };
  };
  page.onLoad({ order_id: ORDER_ID });
  await flush();
  await flush();

  assert.deepEqual(calls, ["login", ORDER_ID]);
  assert.equal(page.data.detail.order.orderId, ORDER_ID);
  assert.equal(page.data.loading, false);
});

test("order detail rejects invalid identity before authentication", async (t) => {
  let calls = 0;
  global.getApp = () => ({
    async ensureAuthenticated() {
      calls += 1;
    },
  });
  t.after(() => delete global.getApp);
  const page = createOrderDetailPageDefinition();
  page.setData = (changes) => {
    page.data = { ...page.data, ...changes };
  };
  page.onLoad({ order_id: "bad" });
  await flush();
  assert.equal(calls, 0);
  assert.match(page.data.errorMessage, /订单 无效/);
});

test("order detail opens the related Registration for cancellation eligibility", async (t) => {
  const navigations = [];
  global.wx = {
    navigateTo(options) {
      navigations.push(options);
    },
  };
  t.after(() => delete global.wx);
  const page = createOrderDetailPageDefinition();
  page.data.detail = {
    order: { registrationId: REGISTRATION_ID },
  };

  page.openRegistration();

  assert.deepEqual(navigations, [
    {
      url: `/pages/registration-detail/index?registration_id=${REGISTRATION_ID}`,
    },
  ]);
});

function paymentHarness(t, overrides = {}) {
  const values = new Map();
  const calls = [];
  const binding = { principalId: PRINCIPAL_ID, generation: 1 };
  global.getApp = () => ({
    async ensureAuthenticated() {},
    getXiangwanAuthBinding() {
      return { ...binding };
    },
  });
  global.wx = {
    getStorageSync(key) {
      return values.get(key) || "";
    },
    setStorageSync(key, value) {
      values.set(key, value);
    },
    removeStorageSync(key) {
      values.delete(key);
    },
    requestPayment({ fail }) {
      calls.push("wx.payment");
      fail({ errMsg: "cancel" });
    },
  };
  t.after(() => {
    delete global.getApp;
    delete global.wx;
  });
  let paid = false;
  const api = {
    newIdempotencyKey: () => OPERATION_ID,
    async getPublicPolicies() {
      return { capabilities: { wechat_payment_available: true } };
    },
    async getMyOrderDetail() {
      return {
        order: {
          order_id: ORDER_ID,
          registration_id: REGISTRATION_ID,
          order_version: paid ? 3 : 2,
          payable_cents: 8800,
          can_continue_payment: !paid,
          payment_status: paid ? "paid_confirmed" : "pending",
          payment_confirmation_pending: false,
          state: paid ? "paid" : "pending_payment",
          outcome: paid ? "paid_confirmed" : "pending_payment",
        },
        as_of: "2026-09-15T09:00:00Z",
      };
    },
    async createWeChatPrepayAttempt(orderId, orderVersion, payableCents, key) {
      calls.push({ orderId, orderVersion, payableCents, key });
      return {
        attempt_status: "ready",
        next_action: "invoke_wechat_payment",
        payment_parameters: {
          timeStamp: "1789290000",
          nonceStr: "nonce",
          package: "prepay_id=abc",
          signType: "RSA",
          paySign: "signed",
        },
      };
    },
    async queryWeChatPayment() {
      calls.push("query");
      paid = true;
      return {
        payment_status: "paid_confirmed",
        query_status: "converged",
        next_action: "payment_confirmed",
      };
    },
    ...overrides,
  };
  const page = createOrderDetailPageDefinition(api);
  page.setData = (changes) => {
    page.data = { ...page.data, ...changes };
  };
  return { page, calls, values, binding };
}

test("WeChat cancel callback still queries server and only server confirmation settles the Order", async (t) => {
  const { page, calls, values } = paymentHarness(t);
  page.onLoad({ order_id: ORDER_ID });
  await flush();
  await flush();
  assert.equal(page.data.detail.order.canPay, true);
  await page.startPayment();
  assert.deepEqual(calls, [
    { orderId: ORDER_ID, orderVersion: 2, payableCents: 8800, key: OPERATION_ID },
    "wx.payment",
    "query",
  ]);
  assert.equal(page.data.detail.order.paymentStatus, "paid_confirmed");
  assert.match(page.data.paymentMessage, /服务端确认/);
  assert.equal(values.size, 0);
});

test("WeChat invocation failures are shown instead of being mislabeled as pending", async (t) => {
  const { page, calls } = paymentHarness(t);
  global.wx.requestPayment = ({ fail }) => {
    calls.push("wx.payment.failure");
    fail({ errMsg: "requestPayment:fail parameter error", errCode: -1 });
  };
  page.onLoad({ order_id: ORDER_ID });
  await flush();
  await flush();

  await page.startPayment();

  assert.deepEqual(calls, [
    { orderId: ORDER_ID, orderVersion: 2, payableCents: 8800, key: OPERATION_ID },
    "wx.payment.failure",
  ]);
  assert.equal(page.data.paymentNeedsQuery, false);
  assert.equal(page.data.paymentMessage, "微信支付参数无效，请返回订单后重新支付");
});

test("unknown prepay write retains the exact operation key for replay", async (t) => {
  const keys = [];
  let attempt = 0;
  const { page, values } = paymentHarness(t, {
    async createWeChatPrepayAttempt(_orderId, _version, _cents, key) {
      keys.push(key);
      attempt += 1;
      if (attempt === 1) throw new Error("request:fail timeout");
      return { attempt_status: "in_progress", next_action: "payment_confirmation_pending" };
    },
    async queryWeChatPayment() {
      const error = new Error("no queryable attempt");
      error.statusCode = 409;
      throw error;
    },
  });
  page.onLoad({ order_id: ORDER_ID });
  await flush();
  await flush();
  await page.startPayment();
  assert.equal(values.size, 1);
  assert.equal(page.data.paymentNeedsQuery, true);
  await page.startPayment();
  assert.deepEqual(keys, [OPERATION_ID, OPERATION_ID]);
  assert.match(page.data.paymentMessage, /支付请求仍在确认中/);
});

test("provider USERPAYING never reopens the same prepay sheet", async (t) => {
  const { page, calls } = paymentHarness(t, {
    async queryWeChatPayment() {
      calls.push("query");
      return {
        payment_status: "pending",
        query_status: "pending",
        retry_payment_allowed: false,
        next_action: "retry_payment_query",
      };
    },
  });
  page.onLoad({ order_id: ORDER_ID });
  await flush();
  await flush();
  await page.startPayment();
  await page.startPayment();
  assert.equal(calls.filter((value) => value === "wx.payment").length, 1);
  assert.equal(calls.filter((value) => typeof value === "object").length, 1);
  assert.match(page.data.paymentMessage, /请勿重复付款/);
});

test("verified NOTPAY replays the original prepay key after an explicit retry", async (t) => {
  const { page, calls, values } = paymentHarness(t, {
    async queryWeChatPayment() {
      calls.push("query");
      return {
        payment_status: "pending",
        query_status: "pending",
        retry_payment_allowed: true,
        next_action: "retry_payment_query",
      };
    },
  });
  page.onLoad({ order_id: ORDER_ID });
  await flush();
  await flush();
  await page.startPayment();
  await page.startPayment();
  assert.equal(calls.filter((value) => value === "wx.payment").length, 2);
  assert.equal(calls.filter((value) => value === "query").length, 2);
  assert.deepEqual(
    calls.filter((value) => typeof value === "object").map((value) => value.key),
    [OPERATION_ID, OPERATION_ID],
  );
  assert.equal(values.size, 1);
});

test("disabled payment capability never creates a prepay attempt", async (t) => {
  const { page, calls, values } = paymentHarness(t, {
    async getPublicPolicies() {
      return { capabilities: { wechat_payment_available: false } };
    },
  });
  page.onLoad({ order_id: ORDER_ID });
  await flush();
  await flush();
  assert.equal(page.data.detail.order.canPay, false);
  await page.startPayment();
  assert.deepEqual(calls, []);
  assert.equal(values.size, 0);
});

test("a switched login cannot display an in-flight Order response from the previous principal", async (t) => {
  const binding = { principalId: PRINCIPAL_ID, generation: 1 };
  let resolveOrder;
  global.getApp = () => ({
    async ensureAuthenticated() {},
    getXiangwanAuthBinding() {
      return { ...binding };
    },
  });
  t.after(() => delete global.getApp);
  const page = createOrderDetailPageDefinition({
    getMyOrderDetail() {
      return new Promise((resolve) => {
        resolveOrder = resolve;
      });
    },
    async getPublicPolicies() {
      return { capabilities: { wechat_payment_available: true } };
    },
  });
  page.setData = (changes) => {
    page.data = { ...page.data, ...changes };
  };
  page.onLoad({ order_id: ORDER_ID });
  await flush();
  binding.principalId = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb";
  binding.generation = 2;
  resolveOrder({
    order: {
      order_id: ORDER_ID,
      payment_status: "pending",
      can_continue_payment: true,
      payable_cents: 8800,
    },
  });
  await flush();
  assert.equal(page.data.detail, null);
  assert.match(page.data.errorMessage, /登录状态已变化/);
});

test("returning to an Order after an account switch clears the previous account's detail", async (t) => {
  const { page, binding } = paymentHarness(t);
  page.onLoad({ order_id: ORDER_ID });
  await flush();
  await flush();
  assert.equal(page.data.detail.order.orderId, ORDER_ID);
  binding.principalId = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb";
  binding.generation = 2;
  page.onShow();
  assert.equal(page.data.detail, null);
  assert.equal(page.data.paymentNeedsQuery, false);
});

test("rejected prepay refreshes a closed order and clears payment operation", async (t) => {
  let rejected = false;
  const message = "微信未能创建支付，本订单已关闭且未扣款。";
  const { page, values, calls } = paymentHarness(t, {
    async getMyOrderDetail() {
      return {
        order: {
          order_id: ORDER_ID,
          registration_id: REGISTRATION_ID,
          order_version: rejected ? 3 : 2,
          payable_cents: 8800,
          can_continue_payment: !rejected,
          payment_status: rejected ? "closed_unpaid" : "pending",
          payment_confirmation_pending: false,
          state: rejected ? "closed" : "pending_payment",
          outcome: rejected ? "closed_unpaid" : "pending_payment",
        },
        as_of: "2026-09-15T09:00:00Z",
      };
    },
    async createWeChatPrepayAttempt() {
      rejected = true;
      const error = new Error(message);
      error.statusCode = 409;
      throw error;
    },
  });
  page.onLoad({ order_id: ORDER_ID });
  await flush();
  await flush();
  await page.startPayment();
  assert.equal(page.data.detail.order.paymentStatus, "closed_unpaid");
  assert.equal(page.data.detail.order.canPay, false);
  assert.equal(page.data.paymentNeedsQuery, false);
  assert.equal(page.data.paymentMessage, message);
  assert.equal(values.size, 0);
  assert.deepEqual(calls, []);
});
