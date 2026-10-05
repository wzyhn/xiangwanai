"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const {
  applyCancellationReceiptToDetail,
  detailReflectsCancellationReceipt,
  projectRegistrationCancellation,
} = require("./model");

const REGISTRATION_ID = "11111111-1111-4111-8111-111111111111";
const SESSION_ID = "22222222-2222-4222-8222-222222222222";
const ORDER_ID = "33333333-3333-4333-8333-333333333333";

function cancellationFixture(overrides = {}) {
  return {
    registration_id: REGISTRATION_ID,
    session_id: SESSION_ID,
    participation_status: "cancelled",
    registration_version: 3,
    cancelled_at: "2026-09-14T10:00:00.123456789Z",
    next_action: "cancellation_completed",
    ...overrides,
  };
}

test("free cancellation projection preserves exact identity and server next action", () => {
  const result = projectRegistrationCancellation(
    cancellationFixture(),
    REGISTRATION_ID,
    SESSION_ID,
  );

  assert.equal(result.registrationId, REGISTRATION_ID);
  assert.equal(result.sessionId, SESSION_ID);
  assert.equal(result.registrationVersion, 3);
  assert.equal(result.cancelledAtText, "09-14 18:00");
  assert.equal(result.title, "报名已取消");
  assert.equal(result.order, null);
  assert.equal(result.refund, null);
});

test("paid cancellation projection keeps payment, manual refund and coupon axes separate", () => {
  const result = projectRegistrationCancellation(
    cancellationFixture({
      policy_version: "cancel-v3",
      order: {
        order_id: ORDER_ID,
        payment_status: "paid_confirmed",
        version: 4,
      },
      refund: {
        status: "pending_manual",
        requested_refund_cents: 8800,
        successful_refund_cents: 0,
        updated_at: "2026-09-14T10:00:01Z",
      },
      coupon_adjustment: {
        disposition: "restore",
        policy_version: "coupon-refund-v2",
        occurred_at: "2026-09-14T10:00:02Z",
      },
      next_action: "refund_processing",
    }),
    REGISTRATION_ID,
    SESSION_ID,
  );

  assert.equal(result.order.paymentStatus, "paid_confirmed");
  assert.equal(result.refund.status, "pending_manual");
  assert.equal(result.refund.requestedRefundText, "¥88.00");
  assert.equal(result.couponAdjustment.text, "优惠券已恢复");
  assert.equal(result.title, "取消已生效，退款处理中");
});

test("cancellation projection fails closed on crossed identities and inconsistent facts", () => {
  [
    cancellationFixture({ registration_id: SESSION_ID }),
    cancellationFixture({ session_id: REGISTRATION_ID }),
    cancellationFixture({ participation_status: "confirmed" }),
    cancellationFixture({ registration_version: 0 }),
    cancellationFixture({ cancelled_at: "2026-09-31T10:00:00Z" }),
    cancellationFixture({ policy_version: "not allowed" }),
    cancellationFixture({ next_action: "refund_completed" }),
    cancellationFixture({
      order: { order_id: ORDER_ID, payment_status: "closed_unpaid", version: 1 },
      next_action: "payment_confirmation_pending",
    }),
    cancellationFixture({
      order: { order_id: ORDER_ID, payment_status: "pending", version: 1 },
      next_action: "cancellation_completed",
    }),
    cancellationFixture({
      policy_version: "cancel-v3",
      order: { order_id: ORDER_ID, payment_status: "paid_confirmed", version: 1 },
      next_action: "cancellation_completed",
    }),
    cancellationFixture({
      order: { order_id: ORDER_ID, payment_status: "paid_confirmed", version: 1 },
      refund: {
        status: "refunded",
        requested_refund_cents: 100,
        successful_refund_cents: 101,
        updated_at: "2026-09-14T10:00:01Z",
      },
      next_action: "refund_completed",
    }),
    cancellationFixture({
      order: { order_id: ORDER_ID, payment_status: "closed_unpaid", version: 1 },
      refund: {
        status: "processing",
        requested_refund_cents: 100,
        successful_refund_cents: 0,
        updated_at: "2026-09-14T10:00:01Z",
      },
      next_action: "refund_processing",
    }),
    cancellationFixture({
      coupon_adjustment: {
        disposition: "restore",
        policy_version: "coupon-v1",
        occurred_at: "2026-09-14T10:00:01Z",
      },
    }),
  ].forEach((fixture) => {
    assert.throws(
      () => projectRegistrationCancellation(fixture, REGISTRATION_ID, SESSION_ID),
      (error) => error.invalidResponse === true,
    );
  });
});

test("payment confirmation pending is accepted only for an unknown order fact", () => {
  const result = projectRegistrationCancellation(
    cancellationFixture({
      policy_version: "cancel-v3",
      order: { order_id: ORDER_ID, payment_status: "unknown", version: 2 },
      next_action: "payment_confirmation_pending",
    }),
    REGISTRATION_ID,
    SESSION_ID,
  );

  assert.equal(result.nextAction, "payment_confirmation_pending");
  assert.match(result.detail, /支付结果确认后/);
});

test("failed refund directs the consumer to human support without restoring access", () => {
  const result = projectRegistrationCancellation(
    cancellationFixture({
      policy_version: "cancel-v3",
      order: { order_id: ORDER_ID, payment_status: "paid_confirmed", version: 2 },
      refund: {
        status: "failed",
        requested_refund_cents: 8800,
        successful_refund_cents: 0,
        updated_at: "2026-09-14T10:00:01Z",
      },
      next_action: "contact_support",
    }),
    REGISTRATION_ID,
    SESSION_ID,
  );

  assert.equal(result.title, "取消已生效，请联系活动方");
  assert.match(result.detail, /参与资格已失效/);
});

test("a successful receipt immediately replaces every stale access and state decision", () => {
  const receipt = projectRegistrationCancellation(
    cancellationFixture({
      order: {
        order_id: ORDER_ID,
        payment_status: "closed_unpaid",
        version: 3,
      },
    }),
    REGISTRATION_ID,
    SESSION_ID,
  );
  const projected = applyCancellationReceiptToDetail(
    {
      registrationId: REGISTRATION_ID,
      registrationVersion: 2,
      sessionId: SESSION_ID,
      state: "registered",
      stateLabel: "报名成功",
      participationStatus: "confirmed",
      paymentStatus: "pending",
      paymentStatusText: "待支付",
      accessDenial: "",
      hasActiveAccess: true,
      canContinuePayment: true,
      checkinCredentialEligible: true,
      privateAccessEligible: true,
      checkinText: "尚未签到",
      cancellationAction: "available",
      canSelfCancel: true,
      cancellationPolicyRequired: true,
      cancellationPolicyAvailable: true,
      cancellationPolicyVersion: "cancel-v3",
    },
    receipt,
  );

  assert.equal(projected.state, "cancelled");
  assert.equal(projected.stateLabel, "已取消");
  assert.equal(projected.participationStatus, "cancelled");
  assert.equal(projected.registrationVersion, 3);
  assert.equal(projected.paymentStatus, "closed_unpaid");
  assert.equal(projected.paymentStatusText, "未支付已关闭");
  assert.equal(projected.accessDenial, "registration_cancelled");
  assert.equal(projected.hasActiveAccess, false);
  assert.equal(projected.canContinuePayment, false);
  assert.equal(projected.checkinCredentialEligible, false);
  assert.equal(projected.privateAccessEligible, false);
  assert.equal(projected.checkinText, "签到资格已失效");
  assert.equal(projected.cancellationPolicyRequired, false);
  assert.equal(projected.cancellationPolicyAvailable, false);
  assert.equal(projected.cancellationPolicyVersion, "");
  assert.equal(detailReflectsCancellationReceipt(projected, receipt), true);
});

test("cancellation convergence compares coupon timestamps at PostgreSQL precision", () => {
  const receipt = projectRegistrationCancellation(
    cancellationFixture({
      policy_version: "cancel-v3",
      order: {
        order_id: ORDER_ID,
        payment_status: "settled_zero",
        version: 3,
      },
      coupon_adjustment: {
        disposition: "restore",
        policy_version: "coupon-refund-v2",
        occurred_at: "2026-09-14T10:00:02.123456789Z",
      },
    }),
    REGISTRATION_ID,
    SESSION_ID,
  );
  const projected = applyCancellationReceiptToDetail(
    {
      registrationId: REGISTRATION_ID,
      registrationVersion: 2,
      sessionId: SESSION_ID,
      paymentStatus: "settled_zero",
    },
    receipt,
  );
  projected.couponAdjustment = {
    ...projected.couponAdjustment,
    occurredAt: "2026-09-14T10:00:02.123456Z",
  };

  assert.equal(detailReflectsCancellationReceipt(projected, receipt), true);
});

test("a cancellation receipt never crosses registration or Session identity", () => {
  const receipt = projectRegistrationCancellation(
    cancellationFixture(),
    REGISTRATION_ID,
    SESSION_ID,
  );
  const crossed = {
    registrationId: REGISTRATION_ID,
    sessionId: REGISTRATION_ID,
    state: "registered",
  };

  assert.equal(applyCancellationReceiptToDetail(crossed, receipt), crossed);
  assert.equal(detailReflectsCancellationReceipt(crossed, receipt), false);
});
