"use strict";

const { formatDateTime, formatMoney } = require("../../utils/format");
const { paymentStatusLabel, projectRefund } = require("../registrations/model");

const ORDER_STATE_LABELS = Object.freeze({
  all: "全部",
  pending_payment: "待支付",
  refund_processing: "退款处理中",
  paid: "已支付",
  refunded: "已退款",
  closed: "已关闭",
});

const ORDER_OUTCOME_LABELS = Object.freeze({
  pending_payment: "等待支付",
  payment_confirming: "支付结果确认中",
  paid_confirmed: "支付成功",
  settled_zero: "优惠后零元结算",
  closed_unpaid: "未支付已关闭",
  refund_pending_manual: "等待人工退款",
  refund_processing: "退款处理中",
  refund_failed: "退款失败，等待处理",
  refund_rejected: "退款未通过",
  refunded: "退款成功",
});

const CAPACITY_HOLD_LABELS = Object.freeze({
  active: "锁位中",
  converted: "已转为有效报名",
  released: "已释放",
  expired: "已过期",
});

function normalizeText(value) {
  return String(value || "").trim();
}

function orderStateTone(value) {
  const state = normalizeText(value);
  if (state === "pending_payment") return "warm";
  if (state === "refund_processing") return "alert";
  if (state === "refunded" || state === "closed") return "quiet";
  return "default";
}

function projectOrderItem(item = {}, options = {}) {
  const state = normalizeText(item.state);
  const outcome = normalizeText(item.outcome);
  const paymentStatus = normalizeText(item.payment_status);
  const instanceTitle = normalizeText(item.instance_title);
  const sessionTitle = normalizeText(item.session_title);
  const discountCents = Number(item.discount_cents);
  const actualPaidCents = Number.isSafeInteger(item.actual_paid_cents)
    ? item.actual_paid_cents
    : null;
  const canContinuePayment = item.can_continue_payment === true;
  const paymentAvailable = options.wechatPaymentAvailable === true;
  return {
    orderId: normalizeText(item.order_id),
    orderVersion: Number(item.order_version),
    registrationId: normalizeText(item.registration_id),
    seriesId: normalizeText(item.series_id),
    seriesTitle: normalizeText(item.series_title),
    instanceId: normalizeText(item.instance_id),
    instanceTitle,
    sessionId: normalizeText(item.session_id),
    sessionTitle,
    // Keep order facts unchanged while matching the public activity title
    // hierarchy in list and detail banners.
    title: instanceTitle || sessionTitle,
    startAt: formatDateTime(item.session_start_at),
    endAt: formatDateTime(item.session_end_at),
    participationStatus: normalizeText(item.participation_status),
    reservationState: normalizeText(item.reservation_state),
    hasActiveAccess: item.reservation_has_active_access === true,
    paymentStatus,
    paymentStatusLabel: paymentStatusLabel(paymentStatus) || "支付状态待确认",
    paymentConfirmationPending: item.payment_confirmation_pending === true,
    originalPriceText: formatMoney(item.original_price_cents),
    hasDiscount: Number.isSafeInteger(discountCents) && discountCents > 0,
    discountText: formatMoney(discountCents),
    payableText: formatMoney(item.payable_cents),
    payableCents: Number(item.payable_cents),
    actualPaidText: actualPaidCents === null ? "" : formatMoney(actualPaidCents),
    paidAt: item.paid_at ? formatDateTime(item.paid_at) : "",
    closedAt: item.closed_at ? formatDateTime(item.closed_at) : "",
    holdStatus: normalizeText(item.hold_status),
    holdStatusLabel: CAPACITY_HOLD_LABELS[normalizeText(item.hold_status)] || "锁位状态待确认",
    holdExpiresAt: formatDateTime(item.hold_expires_at),
    canContinuePayment,
    canPay: canContinuePayment && paymentAvailable,
    paymentNotice:
      paymentStatus === "unknown"
        ? "支付结果正在确认中，请勿重复付款。"
        : canContinuePayment
          ? paymentAvailable
            ? "请在锁位截止前完成支付，结果以订单状态为准。"
            : "该订单仍可继续支付，但小程序支付入口尚未开放，请联系活动方处理。"
          : "",
    refund: projectRefund(item.refund),
    state,
    stateLabel: ORDER_STATE_LABELS[state] || "状态待确认",
    stateTone: orderStateTone(state),
    outcome,
    outcomeLabel: ORDER_OUTCOME_LABELS[outcome] || "订单结果待确认",
    lastBusinessAt: formatDateTime(item.last_business_at),
  };
}

function projectOrdersPage(page = {}) {
  return {
    activeState: normalizeText(page.active_state) || "all",
    items: (Array.isArray(page.items) ? page.items : []).map(projectOrderItem),
    nextCursor: normalizeText(page.next_cursor),
    emptyState: normalizeText(page.empty_state),
    asOf: page.as_of ? formatDateTime(page.as_of) : "",
  };
}

function projectOrderDetail(value = {}, options = {}) {
  return {
    order: projectOrderItem(value.order || {}, options),
    asOf: value.as_of ? formatDateTime(value.as_of) : "",
  };
}

module.exports = {
  CAPACITY_HOLD_LABELS,
  ORDER_OUTCOME_LABELS,
  ORDER_STATE_LABELS,
  orderStateTone,
  projectOrderDetail,
  projectOrderItem,
  projectOrdersPage,
};
