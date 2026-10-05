"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const { projectOrderDetail, projectOrdersPage } = require("./model");

function order(overrides = {}) {
  return {
    order_id: "77777777-7777-4777-8777-777777777777",
    order_version: 3,
    registration_id: "22222222-2222-4222-8222-222222222222",
    series_id: "44444444-4444-4444-8444-444444444444",
    series_title: "AI 课程",
    instance_id: "55555555-5555-4555-8555-555555555555",
    instance_title: "第二期",
    session_id: "11111111-1111-4111-8111-111111111111",
    session_title: "周末场",
    session_start_at: "2026-09-20T02:00:00Z",
    session_end_at: "2026-09-20T04:00:00Z",
    participation_status: "pending_payment",
    reservation_state: "pending_payment",
    reservation_has_active_access: false,
    payment_status: "pending",
    payment_confirmation_pending: false,
    original_price_cents: 9900,
    discount_cents: 1100,
    payable_cents: 8800,
    hold_status: "active",
    hold_expires_at: "2026-09-15T09:10:00Z",
    can_continue_payment: true,
    state: "pending_payment",
    outcome: "pending_payment",
    last_business_at: "2026-09-15T09:00:00Z",
    ...overrides,
  };
}

test("orders page projects payment facts without inventing a payment action", () => {
  const page = projectOrdersPage({
    active_state: "pending_payment",
    items: [order()],
    next_cursor: "next",
    as_of: "2026-09-15T09:00:01Z",
  });

  assert.equal(page.items[0].payableText, "¥88.00");
  assert.equal(page.items[0].title, "第二期");
  assert.equal(page.items[0].sessionTitle, "周末场");
  assert.equal(page.items[0].stateLabel, "待支付");
  assert.match(page.items[0].paymentNotice, /支付入口尚未开放/);
  assert.equal(page.items[0].actualPaidText, "");
  assert.equal(page.nextCursor, "next");
});

test("exact pending Order offers payment only with a server payment capability", () => {
  const raw = { order: order(), as_of: "2026-09-15T09:00:01Z" };
  const gated = projectOrderDetail(raw);
  const enabled = projectOrderDetail(raw, { wechatPaymentAvailable: true });
  assert.equal(gated.order.canPay, false);
  assert.equal(enabled.order.canPay, true);
  assert.equal(enabled.order.orderVersion, 3);
  assert.equal(enabled.order.payableCents, 8800);
  assert.match(enabled.order.paymentNotice, /锁位截止前/);
  assert.equal(
    projectOrderDetail(
      { ...raw, order: order({ payment_status: "unknown", can_continue_payment: false }) },
      { wechatPaymentAvailable: true },
    ).order.canPay,
    false,
  );
});

test("order detail keeps refund and payment as separate authoritative facts", () => {
  const detail = projectOrderDetail({
    order: order({
      participation_status: "cancelled",
      reservation_state: "refunded",
      payment_status: "paid_confirmed",
      payment_confirmation_pending: false,
      actual_paid_cents: 8800,
      paid_at: "2026-09-15T08:30:00Z",
      hold_status: "converted",
      can_continue_payment: false,
      state: "refunded",
      outcome: "refunded",
      refund: {
        refund_status: "refunded",
        reason_code: "user_cancelled",
        requested_refund_cents: 8800,
        successful_refund_cents: 8800,
        resolved_at: "2026-09-15T08:50:00Z",
        updated_at: "2026-09-15T08:50:00Z",
      },
    }),
    as_of: "2026-09-15T09:00:01Z",
  });

  assert.equal(detail.order.paymentStatusLabel, "已支付");
  assert.equal(detail.order.title, "第二期");
  assert.equal(detail.order.sessionTitle, "周末场");
  assert.equal(detail.order.stateLabel, "已退款");
  assert.equal(detail.order.refund.statusLabel, "退款成功");
  assert.equal(detail.order.refund.successfulRefundText, "¥88.00");
});
