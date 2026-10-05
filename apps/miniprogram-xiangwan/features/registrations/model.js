"use strict";

const {
  areaLabel,
  formatDateTime,
  formatMoney,
  registrationStateLabel,
} = require("../../utils/format");
const { projectCouponAdjustment } = require("../cancellation/model");

function normalizeText(value) {
  return String(value || "").trim();
}

const REFUND_STATUS_LABELS = Object.freeze({
  pending_manual: "待人工退款",
  processing: "退款处理中",
  failed: "退款失败",
  rejected: "退款已拒绝",
  refunded: "退款成功",
});

const MASKED_PHONE_PATTERN = /^\+(?:[1-9]\*{4}[0-9]{2}|[1-9][0-9]{2}\*{4}[0-9]{4})$/;

function paymentStatusLabel(value) {
  const labels = {
    pending: "待支付",
    unknown: "支付确认中",
    paid_confirmed: "已支付",
    settled_zero: "零元结算",
    closed_unpaid: "未支付已关闭",
  };
  return labels[normalizeText(value)] || "";
}

function refundStatusLabel(value) {
  return REFUND_STATUS_LABELS[normalizeText(value)] || "";
}

function formatRefundAmount(value) {
  return Number.isSafeInteger(value) && value >= 0 ? `¥${(value / 100).toFixed(2)}` : "金额待确认";
}

function projectRefund(value) {
  if (!value || typeof value !== "object") return null;
  const status = normalizeText(value.refund_status);
  const requestedRefundCents = Number.isSafeInteger(value.requested_refund_cents)
    ? value.requested_refund_cents
    : null;
  const successfulRefundCents = Number.isSafeInteger(value.successful_refund_cents)
    ? value.successful_refund_cents
    : null;
  const resolvedAt = normalizeText(value.resolved_at);
  const updatedAt = normalizeText(value.updated_at);
  return {
    status,
    statusLabel: refundStatusLabel(status) || "退款状态待确认",
    reasonCode: normalizeText(value.reason_code),
    requestedRefundCents,
    requestedRefundText: formatRefundAmount(requestedRefundCents),
    successfulRefundCents,
    successfulRefundText: formatRefundAmount(successfulRefundCents),
    resolvedAt: resolvedAt ? formatDateTime(resolvedAt) : "",
    updatedAt: updatedAt ? formatDateTime(updatedAt) : "",
  };
}

function projectRegistrationItem(item = {}) {
  const order = item.order || null;
  const refund = item.refund || null;
  const projectedRefund = projectRefund(refund);
  const checkin = item.checkin || {};
  const online = normalizeText(item.delivery_mode) === "online";
  const location = online
    ? normalizeText(item.online_mode) || "线上参与"
    : normalizeText(item.venue_name) || areaLabel(item.area);
  const instanceTitle = normalizeText(item.instance_title);
  const sessionTitle = normalizeText(item.session_title);
  return {
    registrationId: normalizeText(item.registration_id),
    registrationVersion:
      Number.isSafeInteger(item.registration_version) && item.registration_version >= 1
        ? item.registration_version
        : 0,
    sessionId: normalizeText(item.session_id),
    orderId: normalizeText(order && order.order_id),
    // Registration surfaces follow the public detail hierarchy: the
    // published Instance is the activity heading and the concrete Session is
    // supporting context. Keep both values for old payloads and templates.
    title: instanceTitle || sessionTitle,
    instanceTitle,
    sessionTitle,
    state: normalizeText(item.state),
    stateLabel: registrationStateLabel(item.state),
    participationStatus: normalizeText(item.participation_status),
    startAt: formatDateTime(item.session_start_at),
    endAt: formatDateTime(item.session_end_at),
    location,
    address: online ? "" : normalizeText(item.address),
    hasActiveAccess: item.has_active_access === true,
    canContinuePayment: item.can_continue_payment === true,
    paymentText: order ? formatMoney(order.payable_cents) : "免费",
    paymentStatus: normalizeText(order && order.payment_status),
    paymentStatusText: paymentStatusLabel(order && order.payment_status),
    refund: projectedRefund,
    checkinStatus: normalizeText(checkin.status),
    checkinText:
      checkin.status === "checked_in"
        ? `已于 ${formatDateTime(checkin.checked_in_at)} 签到`
        : checkin.status === "revoked"
          ? "签到已撤销"
          : "尚未签到",
  };
}

function projectRegistrationsPage(page = {}) {
  return {
    activeState: normalizeText(page.active_state) || "all",
    items: (Array.isArray(page.items) ? page.items : []).map(projectRegistrationItem),
    nextCursor: normalizeText(page.next_cursor),
    emptyState: normalizeText(page.empty_state),
  };
}

function projectRegistrationDetail(detail = {}) {
  const registration = projectRegistrationItem(detail.registration || {});
  const contact = detail.contact || {};
  const contactPhoneMasked = normalizeText(contact.phone_masked);
  const cancellationAction = normalizeText(detail.cancellation_action);
  const cancellationPolicyRequired = detail.cancellation_policy_required === true;
  return {
    ...registration,
    contactName: normalizeText(contact.name),
    contactPhoneMasked: MASKED_PHONE_PATTERN.test(contactPhoneMasked) ? contactPhoneMasked : "",
    accessDenial: normalizeText(detail.access_denial),
    checkinCredentialEligible: detail.checkin_credential_eligible === true,
    privateAccessEligible: detail.private_access_eligible === true,
    cancellationAction,
    canSelfCancel: cancellationAction === "available" && !cancellationPolicyRequired,
    cancellationPolicyRequired,
    cancellationPolicyAvailable: false,
    cancellationPolicyVersion: "",
    couponAdjustment: projectCouponAdjustment(detail.coupon_adjustment),
    cancellation: detail.cancellation
      ? {
          scope: normalizeText(detail.cancellation.scope),
          reason: normalizeText(detail.cancellation.reason),
          at: formatDateTime(detail.cancellation.at),
        }
      : null,
  };
}

module.exports = {
  MASKED_PHONE_PATTERN,
  REFUND_STATUS_LABELS,
  formatRefundAmount,
  paymentStatusLabel,
  projectRegistrationDetail,
  projectRegistrationItem,
  projectRegistrationsPage,
  projectRefund,
  refundStatusLabel,
};
