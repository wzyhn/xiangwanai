"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const { createMyCouponsPageDefinition, mergeCoupons } = require("./index");

const COUPON_ID = "88888888-8888-4888-8888-888888888888";
const ORDER_ID = "77777777-7777-4777-8777-777777777777";

function flush() {
  return new Promise((resolve) => setImmediate(resolve));
}

function attachPageRuntime(page) {
  page.data = { ...page.data };
  page.setData = (changes) => {
    page.data = { ...page.data, ...changes };
  };
}

function coupon(overrides = {}) {
  return {
    coupon_id: COUPON_ID,
    face_value_cents: 1000,
    minimum_order_cents: 5000,
    valid_from: "2026-09-01T00:00:00Z",
    expires_at: "2026-12-01T00:00:00Z",
    grant_kind: "initial_guest",
    applicability: { scope_type: "activity_type", activity_type: "ai_roundtable" },
    state: "available",
    usable: true,
    correction_required: false,
    ...overrides,
  };
}

test("My Coupons waits for session hydration without triggering login", async (t) => {
  let resolveRuntime;
  let calls = 0;
  const app = {
    runtimeReady: new Promise((resolve) => {
      resolveRuntime = resolve;
    }),
    weconqAuth: { hasValidSession: () => true },
  };
  global.getApp = () => app;
  t.after(() => delete global.getApp);
  const page = createMyCouponsPageDefinition({
    async getMyCoupons() {
      calls += 1;
      return { active_state: "all", items: [], as_of: "2026-09-15T09:00:00Z" };
    },
  });
  attachPageRuntime(page);
  page.onLoad();
  page.onShow();
  assert.equal(calls, 0);

  resolveRuntime(true);
  await flush();
  await flush();

  assert.equal(calls, 1);
  assert.equal(page.data.needsLogin, false);
  assert.equal(page.data.emptyMessage, "当前分类还没有优惠券");
});

test("My Coupons changes authoritative state filter and opens its held Order", async (t) => {
  const calls = [];
  const navigations = [];
  global.wx = { navigateTo: ({ url }) => navigations.push(url) };
  t.after(() => delete global.wx);
  const page = createMyCouponsPageDefinition({
    async getMyCoupons(filter) {
      calls.push(filter);
      return {
        active_state: filter.state,
        items:
          filter.state === "held"
            ? [
                coupon({
                  state: "held",
                  usable: false,
                  active_order_id: ORDER_ID,
                  active_registration_id: "22222222-2222-4222-8222-222222222222",
                }),
              ]
            : [],
        as_of: "2026-09-15T09:00:00Z",
      };
    },
  });
  attachPageRuntime(page);
  page._active = true;

  page.selectState({ currentTarget: { dataset: { value: "held" } } });
  await flush();
  page.openOrder({ currentTarget: { dataset: { orderId: ORDER_ID } } });

  assert.equal(calls[0].state, "held");
  assert.equal(page.data.items[0].activeOrderId, ORDER_ID);
  assert.equal(navigations[0], `/pages/order-detail/index?order_id=${ORDER_ID}`);
});

test("My Coupons pagination merge is stable by exact Coupon", () => {
  assert.deepEqual(
    mergeCoupons(
      [{ couponId: "one", state: "available" }],
      [{ couponId: "one", state: "expired" }, { couponId: "two" }],
    ),
    [{ couponId: "one", state: "available" }, { couponId: "two" }],
  );
});
