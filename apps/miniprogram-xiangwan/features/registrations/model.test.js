"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const { projectRegistrationDetail, projectRegistrationsPage } = require("./model");

const REGISTRATION_ID = "11111111-1111-4111-8111-111111111111";
const SESSION_ID = "22222222-2222-4222-8222-222222222222";

test("existing order identity and continuation eligibility remain independent server facts", () => {
  const orderId = "33333333-3333-4333-8333-333333333333";
  for (const paymentStatus of [
    "pending_payment",
    "paid_confirmed",
    "closed",
    "refund_processing",
  ]) {
    const detail = projectRegistrationDetail({
      registration: {
        ...registrationFixture(),
        can_continue_payment: false,
        order: { order_id: orderId, payment_status: paymentStatus },
      },
    });
    assert.equal(detail.orderId, orderId);
    assert.equal(detail.canContinuePayment, false);
  }
  assert.equal(projectRegistrationDetail({ registration: registrationFixture() }).orderId, "");
});

function registrationFixture() {
  return {
    registration_id: REGISTRATION_ID,
    registration_version: 4,
    session_id: SESSION_ID,
    instance_title: "AI 共创夜",
    session_title: "第 1 场",
    state: "registered",
    participation_status: "confirmed",
    session_start_at: "2026-09-20T11:00:00Z",
    session_end_at: "2026-09-20T13:00:00Z",
    delivery_mode: "offline",
    area: "hexi",
    venue_name: "海河实验室",
    address: "天津市河西区创新路 18 号",
    has_active_access: true,
    can_continue_payment: false,
    checkin: { status: "not_checked_in" },
  };
}

test("registration list projects the server view state without deriving a client terminal state", () => {
  const page = projectRegistrationsPage({
    active_state: "registered",
    items: [registrationFixture()],
    next_cursor: "owner-bound-cursor",
  });

  assert.equal(page.activeState, "registered");
  assert.equal(page.items[0].state, "registered");
  assert.equal(page.items[0].stateLabel, "报名成功");
  assert.equal(page.items[0].title, "AI 共创夜");
  assert.equal(page.items[0].instanceTitle, "AI 共创夜");
  assert.equal(page.items[0].sessionTitle, "第 1 场");
  assert.equal(page.items[0].location, "海河实验室");
  assert.equal(page.items[0].address, "天津市河西区创新路 18 号");
  assert.equal(page.items[0].checkinText, "尚未签到");
  assert.equal(page.nextCursor, "owner-bound-cursor");
});

test("registration detail keeps eligibility and cancellation decisions from the server", () => {
  const detail = projectRegistrationDetail({
    registration: registrationFixture(),
    contact: { name: "王薇", phone_masked: "+861****5678" },
    checkin_credential_eligible: true,
    private_access_eligible: false,
    access_denial: "private_access_unavailable",
    cancellation_action: "unavailable",
    cancellation_policy_required: true,
    cancellation: {
      scope: "registration",
      reason: "用户取消",
      at: "2026-09-18T02:00:00Z",
    },
  });

  assert.equal(detail.checkinCredentialEligible, true);
  assert.equal(detail.privateAccessEligible, false);
  assert.equal(detail.accessDenial, "private_access_unavailable");
  assert.equal(detail.registrationVersion, 4);
  assert.equal(detail.title, "AI 共创夜");
  assert.equal(detail.sessionTitle, "第 1 场");
  assert.equal(detail.cancellationAction, "unavailable");
  assert.equal(detail.canSelfCancel, false);
  assert.equal(detail.cancellationPolicyRequired, true);
  assert.equal(detail.cancellation.reason, "用户取消");
  assert.equal(detail.address, "天津市河西区创新路 18 号");
  assert.equal(detail.contactName, "王薇");
  assert.equal(detail.contactPhoneMasked, "+861****5678");
});

test("registration detail never renders an unmasked contact phone", () => {
  const detail = projectRegistrationDetail({
    registration: registrationFixture(),
    contact: { name: "王薇", phone_masked: "+8613812345678" },
  });

  assert.equal(detail.contactName, "王薇");
  assert.equal(detail.contactPhoneMasked, "");
});

test("registration detail accepts the short international masked form", () => {
  const detail = projectRegistrationDetail({
    registration: registrationFixture(),
    contact: { name: "Ada", phone_masked: "+1****78" },
  });

  assert.equal(detail.contactPhoneMasked, "+1****78");
});

test("registration detail keeps failed and resolved refund facts separate from aggregate state", () => {
  const failed = projectRegistrationDetail({
    registration: {
      ...registrationFixture(),
      state: "refund_processing",
      refund: {
        refund_status: "failed",
        reason_code: "session_cancelled",
        requested_refund_cents: 8800,
        successful_refund_cents: 0,
      },
    },
  });
  assert.equal(failed.stateLabel, "退款处理中");
  assert.equal(failed.refund.status, "failed");
  assert.equal(failed.refund.statusLabel, "退款失败");
  assert.equal(failed.refund.requestedRefundCents, 8800);
  assert.equal(failed.refund.requestedRefundText, "¥88.00");
  assert.equal(failed.refund.successfulRefundCents, 0);
  assert.equal(failed.refund.successfulRefundText, "¥0.00");
  assert.equal(failed.refund.resolvedAt, "");

  const rejected = projectRegistrationDetail({
    registration: {
      ...registrationFixture(),
      refund: {
        refund_status: "rejected",
        reason_code: "user_cancelled",
        requested_refund_cents: 8800,
        successful_refund_cents: 0,
        resolved_at: "2026-09-19T02:00:00Z",
      },
    },
  });
  assert.equal(rejected.refund.statusLabel, "退款已拒绝");
  assert.equal(rejected.refund.resolvedAt, "09-19 10:00");
});

test("registration detail exposes payment and refund as separate server facts", () => {
  const detail = projectRegistrationDetail({
    registration: {
      ...registrationFixture(),
      state: "refund_processing",
      order: { payment_status: "paid_confirmed", payable_cents: 8800 },
      refund: {
        refund_status: "pending_manual",
        requested_refund_cents: 8800,
        successful_refund_cents: 0,
        updated_at: "2026-09-18T02:00:00Z",
      },
    },
    cancellation_action: "unavailable",
  });

  assert.equal(detail.paymentStatusText, "已支付");
  assert.equal(detail.refund.statusLabel, "待人工退款");
  assert.equal(detail.refund.requestedRefundText, "¥88.00");
  assert.equal(detail.refund.successfulRefundText, "¥0.00");
  assert.equal(detail.refund.updatedAt, "09-18 10:00");
  assert.equal(detail.canSelfCancel, false);
});

test("registration detail restores the persisted coupon decision after reopening", () => {
  const detail = projectRegistrationDetail({
    registration: {
      ...registrationFixture(),
      state: "cancelled",
      participation_status: "cancelled",
      has_active_access: false,
      order: { payment_status: "settled_zero", payable_cents: 0 },
    },
    cancellation_action: "unavailable",
    coupon_adjustment: {
      disposition: "forfeit",
      policy_version: "coupon-refund-v2",
      occurred_at: "2026-09-18T03:00:00Z",
    },
  });

  assert.equal(detail.couponAdjustment.text, "优惠券按取消政策不予恢复");
  assert.equal(detail.couponAdjustment.policyVersion, "coupon-refund-v2");
  assert.equal(detail.couponAdjustment.occurredAtText, "09-18 11:00");
});

test("registration detail fails closed on a malformed persisted coupon decision", () => {
  assert.throws(
    () =>
      projectRegistrationDetail({
        registration: registrationFixture(),
        cancellation_action: "unavailable",
        coupon_adjustment: {
          disposition: "restore",
          policy_version: "not a canonical version",
          occurred_at: "2026-09-18T03:00:00Z",
        },
      }),
    (error) => error.invalidResponse === true,
  );
});
