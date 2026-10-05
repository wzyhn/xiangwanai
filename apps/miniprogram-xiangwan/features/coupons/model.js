"use strict";

const { activityTypeLabel, formatDateTime, formatMoney } = require("../../utils/format");

const COUPON_STATE_LABELS = Object.freeze({
  all: "全部",
  available: "可使用",
  held: "已锁定",
  correction_required: "待核对",
  expired: "已过期",
  redeemed: "已使用",
  invalidated: "已失效",
});

const GRANT_KIND_LABELS = Object.freeze({
  initial_guest: "新人权益",
  manual_replenishment: "人工补发",
});

const ADJUSTMENT_LABELS = Object.freeze({
  restored: "取消后已返还",
  forfeited: "取消后不返还",
});

function normalizeText(value) {
  return String(value || "").trim();
}

function couponStateTone(value) {
  const state = normalizeText(value);
  if (state === "available") return "default";
  if (state === "held") return "warm";
  if (state === "correction_required") return "alert";
  return "quiet";
}

function applicabilityText(applicability = {}) {
  if (normalizeText(applicability.scope_type) === "series") return "指定活动系列可用";
  const activityType = activityTypeLabel(applicability.activity_type);
  return `${activityType}活动可用`;
}

function projectCouponItem(item = {}) {
  const state = normalizeText(item.state);
  const minimumOrderCents = Number(item.minimum_order_cents);
  const adjustment = item.latest_adjustment || null;
  return {
    couponId: normalizeText(item.coupon_id),
    faceValueCents: Number.isSafeInteger(Number(item.face_value_cents))
      ? Number(item.face_value_cents)
      : 0,
    minimumOrderCents: Number.isSafeInteger(minimumOrderCents) ? minimumOrderCents : 0,
    faceValueText: formatMoney(item.face_value_cents),
    minimumOrderText:
      Number.isSafeInteger(minimumOrderCents) && minimumOrderCents === 0
        ? "无门槛"
        : `满 ${formatMoney(minimumOrderCents)} 可用`,
    validFrom: formatDateTime(item.valid_from),
    expiresAt: formatDateTime(item.expires_at),
    grantKind: normalizeText(item.grant_kind),
    grantKindLabel: GRANT_KIND_LABELS[normalizeText(item.grant_kind)] || "活动权益",
    applicabilityText: applicabilityText(item.applicability),
    applicability:
      item.applicability && typeof item.applicability === "object"
        ? {
            scopeType: normalizeText(item.applicability.scope_type),
            activityType: normalizeText(item.applicability.activity_type),
            seriesId: normalizeText(item.applicability.series_id).toLowerCase(),
          }
        : { scopeType: "", activityType: "", seriesId: "" },
    state,
    stateLabel: COUPON_STATE_LABELS[state] || "状态待确认",
    stateTone: couponStateTone(state),
    usable: item.usable === true,
    correctionRequired: item.correction_required === true,
    activeOrderId: normalizeText(item.active_order_id),
    activeRegistrationId: normalizeText(item.active_registration_id),
    adjustment: adjustment
      ? {
          disposition: normalizeText(adjustment.disposition),
          label: ADJUSTMENT_LABELS[normalizeText(adjustment.disposition)] || "优惠券已调整",
          occurredAt: formatDateTime(adjustment.occurred_at),
        }
      : null,
  };
}

function projectCouponsPage(page = {}) {
  return {
    activeState: normalizeText(page.active_state) || "all",
    items: (Array.isArray(page.items) ? page.items : []).map(projectCouponItem),
    nextCursor: normalizeText(page.next_cursor),
    emptyState: normalizeText(page.empty_state),
    asOf: page.as_of ? formatDateTime(page.as_of) : "",
  };
}

module.exports = {
  ADJUSTMENT_LABELS,
  COUPON_STATE_LABELS,
  GRANT_KIND_LABELS,
  applicabilityText,
  couponStateTone,
  projectCouponItem,
  projectCouponsPage,
};
