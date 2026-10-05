"use strict";

const { formatDateTime, formatMoney, registrationStateLabel } = require("../../utils/format");
const { canonicalUUID, invalidResponse } = require("../../services/xiangwan-api");

const SERVER_TIME_PATTERN = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.(\d{1,9}))?Z$/;
const POLICY_VERSION_PATTERN = /^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$/;
const PAYMENT_STATUSES = new Set(["unknown", "paid_confirmed", "settled_zero", "closed_unpaid"]);
const PAYMENT_STATUS_LABELS = Object.freeze({
  unknown: "支付确认中",
  paid_confirmed: "已支付",
  settled_zero: "零元结算",
  closed_unpaid: "未支付已关闭",
});
const REFUND_STATUSES = new Set(["pending_manual", "processing", "refunded", "failed", "rejected"]);
const NEXT_ACTIONS = new Set([
  "cancellation_completed",
  "payment_confirmation_pending",
  "refund_completed",
  "refund_processing",
  "contact_support",
]);
const COUPON_DISPOSITIONS = new Set(["restore", "forfeit"]);
const CANCELLATION_TERMINAL_STATES = new Set(["cancelled", "refund_processing", "refunded"]);

function cancellationInvalid() {
  throw invalidResponse("取消结果");
}

function strictObject(value) {
  if (!value || typeof value !== "object" || Array.isArray(value)) cancellationInvalid();
  return value;
}

function responseUUID(value, expected = "") {
  if (typeof value !== "string") cancellationInvalid();
  try {
    const normalized = canonicalUUID(value, "报名记录");
    if (expected && normalized !== expected) cancellationInvalid();
    return normalized;
  } catch (_error) {
    cancellationInvalid();
  }
}

function serverTimestamp(value) {
  if (typeof value !== "string") cancellationInvalid();
  const normalized = value.trim();
  const parts = normalized.match(SERVER_TIME_PATTERN);
  if (!parts) cancellationInvalid();
  const expected = parts.slice(1, 7).map(Number);
  const milliseconds = Number(
    String(parts[7] || "")
      .padEnd(3, "0")
      .slice(0, 3),
  );
  const timestamp = Date.UTC(
    expected[0],
    expected[1] - 1,
    expected[2],
    expected[3],
    expected[4],
    expected[5],
    milliseconds,
  );
  const parsed = new Date(timestamp);
  const actual = [
    parsed.getUTCFullYear(),
    parsed.getUTCMonth() + 1,
    parsed.getUTCDate(),
    parsed.getUTCHours(),
    parsed.getUTCMinutes(),
    parsed.getUTCSeconds(),
  ];
  if (!Number.isFinite(timestamp) || actual.some((part, index) => part !== expected[index])) {
    cancellationInvalid();
  }
  const databasePrecision = `${parts[1]}-${parts[2]}-${parts[3]}T${parts[4]}:${parts[5]}:${parts[6]}.${String(
    parts[7] || "",
  )
    .padEnd(6, "0")
    .slice(0, 6)}Z`;
  return { normalized, timestamp, databasePrecision };
}

function positiveVersion(value) {
  if (!Number.isSafeInteger(value) || value < 1) cancellationInvalid();
  return value;
}

function money(value) {
  if (!Number.isSafeInteger(value) || value < 0) cancellationInvalid();
  return value;
}

function enumValue(value, allowed) {
  if (typeof value !== "string" || !allowed.has(value)) cancellationInvalid();
  return value;
}

function policyVersion(value, optional = false) {
  if (optional && typeof value === "undefined") return "";
  if (typeof value !== "string" || !POLICY_VERSION_PATTERN.test(value)) {
    cancellationInvalid();
  }
  return value;
}

function projectOrder(value) {
  if (typeof value === "undefined") return null;
  const order = strictObject(value);
  return {
    orderId: responseUUID(order.order_id),
    paymentStatus: enumValue(order.payment_status, PAYMENT_STATUSES),
    version: positiveVersion(order.version),
  };
}

function projectRefund(value) {
  if (typeof value === "undefined") return null;
  const refund = strictObject(value);
  const requestedRefundCents = money(refund.requested_refund_cents);
  const successfulRefundCents = money(refund.successful_refund_cents);
  const status = enumValue(refund.status, REFUND_STATUSES);
  if (
    requestedRefundCents < 1 ||
    successfulRefundCents > requestedRefundCents ||
    (status === "refunded" && successfulRefundCents !== requestedRefundCents) ||
    (status !== "refunded" && successfulRefundCents !== 0)
  ) {
    cancellationInvalid();
  }
  const updatedAt = serverTimestamp(refund.updated_at);
  return {
    status,
    requestedRefundCents,
    requestedRefundText: formatMoney(requestedRefundCents),
    successfulRefundCents,
    successfulRefundText: formatMoney(successfulRefundCents),
    updatedAt: updatedAt.normalized,
    updatedAtText: formatDateTime(updatedAt.normalized),
  };
}

function projectCouponAdjustment(value) {
  if (typeof value === "undefined") return null;
  const adjustment = strictObject(value);
  const occurredAt = serverTimestamp(adjustment.occurred_at);
  const disposition = enumValue(adjustment.disposition, COUPON_DISPOSITIONS);
  return {
    disposition,
    policyVersion: policyVersion(adjustment.policy_version),
    occurredAt: occurredAt.normalized,
    occurredAtText: formatDateTime(occurredAt.normalized),
    text: disposition === "restore" ? "优惠券已恢复" : "优惠券按取消政策不予恢复",
  };
}

function assertNextActionConsistency(nextAction, order, refund) {
  const refundAction = refund
    ? refund.status === "refunded"
      ? "refund_completed"
      : refund.status === "failed" || refund.status === "rejected"
        ? "contact_support"
        : "refund_processing"
    : "";
  const expected = refundAction
    ? refundAction
    : order && order.paymentStatus === "unknown"
      ? "payment_confirmation_pending"
      : "cancellation_completed";
  if (
    nextAction !== expected ||
    (refund && (!order || order.paymentStatus !== "paid_confirmed")) ||
    (order && order.paymentStatus === "paid_confirmed" && !refund)
  ) {
    cancellationInvalid();
  }
}

function nextActionCopy(nextAction, refund) {
  switch (nextAction) {
    case "payment_confirmation_pending":
      return {
        title: "取消已生效，支付确认中",
        detail: "参与资格和签到凭证已失效；支付结果确认后，请刷新查看后续处理。",
      };
    case "refund_processing":
      return {
        title: "取消已生效，退款处理中",
        detail: `参与资格已失效，${refund.requestedRefundText} 退款将由人工处理，请留意报名详情。`,
      };
    case "refund_completed":
      return {
        title: "取消已生效，退款已完成",
        detail: `参与资格已失效，已退款 ${refund.successfulRefundText}。`,
      };
    case "contact_support":
      return {
        title: "取消已生效，请联系活动方",
        detail: "参与资格已失效，退款处理需要人工协助，请联系活动方核对。",
      };
    default:
      return {
        title: "报名已取消",
        detail: "参与资格和签到凭证已失效，名额已释放。",
      };
  }
}

function projectRegistrationCancellation(payload, expectedRegistrationId, expectedSessionId) {
  const value = strictObject(payload);
  const registrationId = responseUUID(
    value.registration_id,
    canonicalUUID(expectedRegistrationId, "报名记录"),
  );
  const sessionId = responseUUID(value.session_id, canonicalUUID(expectedSessionId, "活动场次"));
  if (value.participation_status !== "cancelled") cancellationInvalid();
  const cancelledAt = serverTimestamp(value.cancelled_at);
  const order = projectOrder(value.order);
  const refund = projectRefund(value.refund);
  const couponAdjustment = projectCouponAdjustment(value.coupon_adjustment);
  const nextAction = enumValue(value.next_action, NEXT_ACTIONS);
  const appliedPolicyVersion = policyVersion(value.policy_version, true);
  if (
    order &&
    (order.paymentStatus === "unknown" ||
      order.paymentStatus === "paid_confirmed" ||
      order.paymentStatus === "settled_zero") &&
    !appliedPolicyVersion
  ) {
    cancellationInvalid();
  }
  if (
    couponAdjustment &&
    (!order || (order.paymentStatus !== "paid_confirmed" && order.paymentStatus !== "settled_zero"))
  ) {
    cancellationInvalid();
  }
  assertNextActionConsistency(nextAction, order, refund);
  const copy = nextActionCopy(nextAction, refund);

  return {
    registrationId,
    sessionId,
    participationStatus: "cancelled",
    registrationVersion: positiveVersion(value.registration_version),
    cancelledAt: cancelledAt.normalized,
    cancelledAtText: formatDateTime(cancelledAt.normalized),
    policyVersion: appliedPolicyVersion,
    order,
    refund,
    couponAdjustment,
    nextAction,
    title: copy.title,
    detail: copy.detail,
  };
}

function couponAdjustmentMatches(current, expected) {
  if (!expected) return true;
  return Boolean(
    current &&
      current.disposition === expected.disposition &&
      current.policyVersion === expected.policyVersion &&
      serverTimestamp(current.occurredAt).databasePrecision ===
        serverTimestamp(expected.occurredAt).databasePrecision,
  );
}

function orderMatches(currentPaymentStatus, expected) {
  return !expected || currentPaymentStatus === expected.paymentStatus;
}

function cancellationReceiptMatchesDetail(detail, receipt) {
  return Boolean(
    detail &&
      receipt &&
      detail.registrationId === receipt.registrationId &&
      detail.sessionId === receipt.sessionId,
  );
}

function detailReflectsCancellationReceipt(detail, receipt) {
  if (!cancellationReceiptMatchesDetail(detail, receipt)) return false;
  return Boolean(
    detail.participationStatus === "cancelled" &&
      CANCELLATION_TERMINAL_STATES.has(detail.state) &&
      Number.isSafeInteger(detail.registrationVersion) &&
      detail.registrationVersion >= receipt.registrationVersion &&
      detail.cancellationAction === "unavailable" &&
      detail.canSelfCancel === false &&
      detail.cancellationPolicyRequired === false &&
      detail.checkinCredentialEligible === false &&
      detail.privateAccessEligible === false &&
      detail.hasActiveAccess === false &&
      detail.canContinuePayment === false &&
      orderMatches(detail.paymentStatus, receipt.order) &&
      couponAdjustmentMatches(detail.couponAdjustment, receipt.couponAdjustment),
  );
}

function applyCancellationReceiptToDetail(detail, receipt) {
  if (!cancellationReceiptMatchesDetail(detail, receipt)) return detail;
  const receiptPaymentStatus = receipt.order && receipt.order.paymentStatus;
  return {
    ...detail,
    state: "cancelled",
    stateLabel: registrationStateLabel("cancelled"),
    participationStatus: "cancelled",
    registrationVersion: receipt.registrationVersion,
    accessDenial: "registration_cancelled",
    hasActiveAccess: false,
    canContinuePayment: false,
    checkinCredentialEligible: false,
    privateAccessEligible: false,
    checkinText: "签到资格已失效",
    cancellationAction: "unavailable",
    canSelfCancel: false,
    cancellationPolicyRequired: false,
    cancellationPolicyAvailable: false,
    cancellationPolicyVersion: "",
    paymentStatus: receiptPaymentStatus || detail.paymentStatus,
    paymentStatusText: receiptPaymentStatus
      ? PAYMENT_STATUS_LABELS[receiptPaymentStatus]
      : detail.paymentStatusText,
    couponAdjustment: receipt.couponAdjustment || detail.couponAdjustment || null,
  };
}

module.exports = {
  CANCELLATION_TERMINAL_STATES,
  COUPON_DISPOSITIONS,
  NEXT_ACTIONS,
  PAYMENT_STATUSES,
  POLICY_VERSION_PATTERN,
  REFUND_STATUSES,
  SERVER_TIME_PATTERN,
  applyCancellationReceiptToDetail,
  detailReflectsCancellationReceipt,
  projectCouponAdjustment,
  projectRegistrationCancellation,
};
