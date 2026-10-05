"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const { createMyOrdersPageDefinition, mergeOrders } = require("./index");

function flush() {
  return new Promise((resolve) => setImmediate(resolve));
}

test("My Orders waits for session hydration without triggering login", async (t) => {
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
  const page = createMyOrdersPageDefinition({
    async getMyOrders() {
      calls += 1;
      return { active_state: "all", items: [], as_of: "2026-09-15T09:00:00Z" };
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
  assert.equal(page.data.emptyMessage, "当前分类还没有订单");
});

test("My Orders merge is stable and deduplicates by exact Order", () => {
  assert.deepEqual(
    mergeOrders(
      [{ orderId: "one", state: "old" }],
      [
        { orderId: "one", state: "new" },
        { orderId: "two", state: "new" },
      ],
    ),
    [
      { orderId: "one", state: "old" },
      { orderId: "two", state: "new" },
    ],
  );
});

test("My Orders opens an exact order detail path", (t) => {
  const calls = [];
  global.wx = { navigateTo: (options) => calls.push(options) };
  t.after(() => delete global.wx);
  const page = createMyOrdersPageDefinition();
  page.openOrder({ currentTarget: { dataset: { orderId: "order/id" } } });
  assert.deepEqual(calls, [{ url: "/pages/order-detail/index?order_id=order%2Fid" }]);
});
