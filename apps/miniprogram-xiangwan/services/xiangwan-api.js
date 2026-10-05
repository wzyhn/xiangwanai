"use strict";

const http = require("../api/http");
const { createXiangwanError } = require("../utils/errors");

const UUID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const REGISTRATION_PRIVACY_POLICY_VERSION_PATTERN = /^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$/;
const API_PREFIX = "/api/v1/xiangwan";
// These commands intentionally carry no request body. Keep an explicit empty
// content type as a compatibility guard for DevTools-generated copies of the
// shared HTTP package that still add application/json by default.
const EMPTY_BODY_HEADER = Object.freeze({ "Content-Type": "" });
function assertRegistrationContact(value) {
  const result = assertObject(value, "报名资料");
  if (
    typeof result.configured !== "boolean" ||
    typeof result.nickname !== "string" ||
    typeof result.phone_e164 !== "string" ||
    !Number.isSafeInteger(result.version) ||
    result.version < 0 ||
    typeof result.principal_profile_etag !== "string" ||
    !result.principal_profile_etag ||
    typeof result.privacy_policy_version !== "string" ||
    typeof result.contact_policy_version !== "string" ||
    (result.configured &&
      (!/^\+[1-9][0-9]{7,14}$/.test(result.phone_e164) ||
        result.version < 1 ||
        !result.nickname.trim()))
  ) {
    throw createXiangwanError("invalid_contact_response", "报名资料暂不可读取，请重试");
  }
  return result;
}
const PUBLIC_MEDIA_PATH_PATTERN =
  /^\/api\/v1\/xiangwan\/media\/[0-9a-f-]{36}\/[0-9a-f-]{36}\/[0-9a-f-]{36}$/;
const ACTIVITY_TYPES = new Set([
  "ai_roundtable",
  "special_event",
  "course",
  "competition",
  "custom",
]);
const PUBLIC_REVIEW_BLOCK_TYPES = new Set(["text", "image", "link", "file", "video", "audio"]);
const PUBLIC_REVIEW_AVAILABILITY = new Set(["available", "unavailable", "policy_blocked"]);
const PUBLIC_SESSION_STATUSES = new Set(["published", "ended", "cancelled"]);
const PUBLIC_INSTANCE_SESSION_STATUSES = new Set([...PUBLIC_SESSION_STATUSES, "archived"]);
const PUBLIC_REVIEW_SESSION_STATUSES = new Set(["ended", "archived"]);
const MY_ORDER_STATES = new Set([
  "all",
  "pending_payment",
  "refund_processing",
  "paid",
  "refunded",
  "closed",
]);
const MY_ORDER_OUTCOMES = new Set([
  "pending_payment",
  "payment_confirming",
  "paid_confirmed",
  "settled_zero",
  "closed_unpaid",
  "refund_pending_manual",
  "refund_processing",
  "refund_failed",
  "refund_rejected",
  "refunded",
]);
const PARTICIPATION_STATUSES = new Set(["pending_payment", "confirmed", "cancelled"]);
const MY_REGISTRATION_STATES = new Set([
  "pending_payment",
  "registered",
  "cancelled",
  "refund_processing",
  "refunded",
  "ended",
]);
const PAYMENT_STATUSES = new Set([
  "pending",
  "unknown",
  "paid_confirmed",
  "settled_zero",
  "closed_unpaid",
]);
const CAPACITY_HOLD_STATUSES = new Set(["active", "converted", "released", "expired"]);
const REFUND_STATUSES = new Set(["pending_manual", "processing", "failed", "rejected", "refunded"]);
const MY_ORDER_STATE_RANK = Object.freeze({
  pending_payment: 1,
  refund_processing: 2,
  paid: 3,
  refunded: 4,
  closed: 5,
});
const POSITIVE_PAYABLE_STATUSES = new Set([
  "pending",
  "unknown",
  "paid_confirmed",
  "closed_unpaid",
]);
const OPEN_REFUND_STATUSES = new Set(["pending_manual", "processing"]);
const MY_COUPON_STATES = new Set([
  "all",
  "available",
  "held",
  "correction_required",
  "expired",
  "redeemed",
  "invalidated",
]);
const COUPON_GRANT_KINDS = new Set(["initial_guest", "manual_replenishment"]);
const COUPON_SCOPE_TYPES = new Set(["activity_type", "series"]);
const COUPON_ADJUSTMENTS = new Set(["restored", "forfeited"]);
const SERIES_STATUSES = new Set(["draft", "active", "archived"]);
const MY_COUPON_STATE_RANK = Object.freeze({
  available: 1,
  held: 2,
  correction_required: 3,
  expired: 4,
  redeemed: 5,
  invalidated: 6,
});
const IDENTITY_ROLE_CODES = new Set([
  "host",
  "invited_guest",
  "course_instructor",
  "event_speaker",
]);
const IDENTITY_ROLE_STATUSES = new Set(["active", "revoked"]);
const IDENTITY_ROLE_STATES = new Set(["current", "historical"]);
const IDENTITY_CURRENT_INSTANCE_STATUSES = new Set(["draft", "pending_publish", "published"]);
const IDENTITY_INSTANCE_STATUSES = new Set([
  ...IDENTITY_CURRENT_INSTANCE_STATUSES,
  "completed",
  "cancelled",
  "archived",
]);
const HOST_RULES_STATES = new Set(["pending", "configured"]);
const HOST_APPLICATION_STATUSES = new Set(["pending", "approved", "rejected", "withdrawn"]);
const CONTRIBUTION_STATES = new Set(["active", "reversed"]);
const HOST_APPLICATION_REFERENCE_PATTERN = /^[A-Za-z0-9][A-Za-z0-9._:-]{0,99}$/;

function canonicalUUID(value, label = "id") {
  const normalized = String(value || "")
    .trim()
    .toLowerCase();
  if (!UUID_PATTERN.test(normalized)) {
    throw createXiangwanError("invalid_identifier", `${label} 无效`);
  }
  return normalized;
}

function registrationSubmissionTransport(payload) {
  if (!payload || typeof payload !== "object" || Array.isArray(payload)) {
    throw createXiangwanError("invalid_registration_request", "报名信息无效");
  }
  const privacyPolicyVersion = payload.privacy_policy_version;
  if (
    typeof privacyPolicyVersion !== "string" ||
    !REGISTRATION_PRIVACY_POLICY_VERSION_PATTERN.test(privacyPolicyVersion)
  ) {
    throw createXiangwanError("invalid_registration_request", "隐私政策版本无效");
  }
  const body = { ...payload };
  delete body.privacy_policy_version;
  return { body, privacyPolicyVersion };
}

function assertObject(value, label) {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    throw createXiangwanError("invalid_response", `${label}暂时不可用`, {
      invalidResponse: true,
    });
  }
  return value;
}

function invalidResponse(label) {
  return createXiangwanError("invalid_response", `${label}暂时不可用`, {
    invalidResponse: true,
  });
}

function assertNonnegativeInteger(value, label, minimum = 0) {
  if (!Number.isSafeInteger(value) || value < minimum) throw invalidResponse(label);
  return value;
}

function assertResponseTime(value, label) {
  const normalized = String(value || "").trim();
  if (!normalized || Number.isNaN(Date.parse(normalized))) throw invalidResponse(label);
  return normalized;
}

function assertResponseUUID(value, label, expected = "") {
  const normalized = String(value || "")
    .trim()
    .toLowerCase();
  if (!UUID_PATTERN.test(normalized) || (expected && normalized !== expected)) {
    throw invalidResponse(label);
  }
  return normalized;
}

function assertResponseArray(value, label) {
  if (!Array.isArray(value)) throw invalidResponse(label);
  return value;
}

function assertResponseBoolean(value, label) {
  if (typeof value !== "boolean") throw invalidResponse(label);
  return value;
}

function assertResponseText(value, label) {
  if (typeof value !== "string" || !value.trim() || value !== value.trim()) {
    throw invalidResponse(label);
  }
  return value;
}

function assertBoundedResponseText(value, label, maximumRunes, required = true) {
  if (
    typeof value !== "string" ||
    value !== value.trim() ||
    (required && !value) ||
    Array.from(value).length > maximumRunes
  ) {
    throw invalidResponse(label);
  }
  return value;
}

function assertOptionalResponseTime(value, label) {
  if (value === undefined) return "";
  return assertResponseTime(value, label);
}

function assertResponseCursor(value, label) {
  if (value === undefined) return "";
  if (typeof value !== "string" || value !== value.trim() || value.length > 2048) {
    throw invalidResponse(label);
  }
  return value;
}

function appendQuery(parts, name, value) {
  const normalized = String(value || "").trim();
  if (normalized) parts.push(`${encodeURIComponent(name)}=${encodeURIComponent(normalized)}`);
}

function homeQuery(filter = {}) {
  const parts = [];
  appendQuery(parts, "activity_type", filter.activityType);
  appendQuery(parts, "area", filter.area);
  appendQuery(parts, "time_window", filter.timeWindow);
  appendQuery(parts, "local_date", filter.localDate);
  (Array.isArray(filter.quickTags) ? filter.quickTags : []).forEach((tag) => {
    appendQuery(parts, "quick_tag", tag);
  });
  appendQuery(parts, "cursor", filter.cursor);
  if (Number.isSafeInteger(filter.limit) && filter.limit > 0) {
    parts.push(`limit=${filter.limit}`);
  }
  return parts.length ? `?${parts.join("&")}` : "";
}

function registrationsQuery(filter = {}) {
  const parts = [];
  appendQuery(parts, "state", filter.state);
  appendQuery(parts, "cursor", filter.cursor);
  if (Number.isSafeInteger(filter.limit) && filter.limit > 0) {
    parts.push(`limit=${filter.limit}`);
  }
  return parts.length ? `?${parts.join("&")}` : "";
}

function ordersQuery(filter = {}) {
  const parts = [];
  appendQuery(parts, "state", filter.state);
  appendQuery(parts, "cursor", filter.cursor);
  if (Number.isSafeInteger(filter.limit) && filter.limit > 0) {
    parts.push(`limit=${filter.limit}`);
  }
  return parts.length ? `?${parts.join("&")}` : "";
}

function couponsQuery(filter = {}) {
  const parts = [];
  appendQuery(parts, "state", filter.state);
  appendQuery(parts, "cursor", filter.cursor);
  if (Number.isSafeInteger(filter.limit) && filter.limit > 0) {
    parts.push(`limit=${filter.limit}`);
  }
  return parts.length ? `?${parts.join("&")}` : "";
}

function favoritesQuery(filter = {}) {
  const parts = [];
  appendQuery(parts, "cursor", filter.cursor);
  if (Number.isSafeInteger(filter.limit) && filter.limit > 0) {
    parts.push(`limit=${filter.limit}`);
  }
  return parts.length ? `?${parts.join("&")}` : "";
}

function peopleQuery(filter = {}) {
  const parts = [];
  appendQuery(parts, "cursor", filter.cursor);
  if (Number.isSafeInteger(filter.limit) && filter.limit > 0) {
    parts.push(`limit=${filter.limit}`);
  }
  return parts.length ? `?${parts.join("&")}` : "";
}

function pastActivitiesQuery(filter = {}) {
  const parts = [];
  appendQuery(parts, "activity_type", filter.activityType);
  appendQuery(parts, "cursor", filter.cursor);
  if (Number.isSafeInteger(filter.limit) && filter.limit > 0) {
    parts.push(`limit=${filter.limit}`);
  }
  return parts.length ? `?${parts.join("&")}` : "";
}

function assertPublicReviewBlock(block) {
  const current = assertObject(block, "活动回顾");
  assertResponseUUID(current.block_id, "活动回顾");
  const type = String(current.type || "").trim();
  const availability = String(current.availability || "").trim();
  if (!PUBLIC_REVIEW_BLOCK_TYPES.has(type)) {
    throw invalidResponse("活动回顾");
  }
  if (!PUBLIC_REVIEW_AVAILABILITY.has(availability)) {
    throw invalidResponse("活动回顾");
  }
  if (
    current.is_cover !== undefined &&
    (typeof current.is_cover !== "boolean" || (current.is_cover && type !== "image"))
  )
    throw invalidResponse("活动回顾");
  if (availability !== "available") return;
  if (type === "text" && !String(current.text || "").trim()) {
    throw invalidResponse("活动回顾");
  }
  if (type === "link" && !/^https:\/\/[^\s]+$/.test(String(current.external_url || ""))) {
    throw invalidResponse("活动回顾");
  }
  if (
    new Set(["file", "video", "audio"]).has(type) &&
    !PUBLIC_MEDIA_PATH_PATTERN.test(String(current.media_path || ""))
  ) {
    throw invalidResponse("活动回顾");
  }
  if (
    type === "image" &&
    !PUBLIC_MEDIA_PATH_PATTERN.test(String(current.media_path || "")) &&
    !/^https:\/\/[^\s]+$/.test(String(current.external_url || ""))
  ) {
    throw invalidResponse("活动回顾");
  }
}

function assertPublicReviewContentBlock(block) {
  const current = assertObject(block, "活动详情");
  const type = String(current.type || "").trim();
  if (type === "text") {
    if (!String(current.title || "").trim() || !String(current.body || "").trim()) {
      throw invalidResponse("活动详情");
    }
    return;
  }
  if (type === "image") {
    const url = String(current.url || "").trim();
    if (
      !url ||
      (!/^\/api\/v1\/xiangwan\/covers\/[0-9a-f]{32}\.(?:jpg|png|webp)$/.test(url) &&
        !/^https:\/\/[^\s?#]+(?::\d{1,5})?(?:\/[^\s?#]*)?$/.test(url))
    ) {
      throw invalidResponse("活动详情");
    }
    if (current.caption !== undefined && typeof current.caption !== "string") {
      throw invalidResponse("活动详情");
    }
    return;
  }
  throw invalidResponse("活动详情");
}

function assertNextInstanceRoute(value, sourceInstanceId) {
  const route = assertObject(value, "下一期活动");
  const action = String(route.action || "").trim();
  const candidates = assertResponseArray(route.candidate_session_ids, "下一期活动").map((id) =>
    assertResponseUUID(id, "下一期活动"),
  );
  if (action === "unavailable") {
    if (route.instance_id || route.session_id || candidates.length)
      throw invalidResponse("下一期活动");
    return;
  }
  const nextInstanceId = assertResponseUUID(route.instance_id, "下一期活动");
  if (nextInstanceId === sourceInstanceId) throw invalidResponse("下一期活动");
  if (action === "session_detail") {
    assertResponseUUID(route.session_id, "下一期活动");
    if (candidates.length) throw invalidResponse("下一期活动");
    return;
  }
  if (action === "session_selection_required") {
    if (
      route.session_id ||
      candidates.length < 2 ||
      new Set(candidates).size !== candidates.length
    ) {
      throw invalidResponse("下一期活动");
    }
    return;
  }
  throw invalidResponse("下一期活动");
}

function assertPublicSessionCollection(result, expected = {}) {
  const label = "活动场次";
  const expectedSeriesId = expected.seriesId || "";
  const expectedInstanceId = expected.instanceId || "";
  const responseInstanceId = result.instance_id
    ? assertResponseUUID(result.instance_id, label, expectedInstanceId)
    : "";
  if (expectedInstanceId && !responseInstanceId) throw invalidResponse(label);
  const allowedStatuses = expectedInstanceId
    ? PUBLIC_INSTANCE_SESSION_STATUSES
    : PUBLIC_SESSION_STATUSES;
  if (expectedSeriesId) assertResponseUUID(result.series_id, label, expectedSeriesId);
  const sessions = assertResponseArray(result.sessions, label);
  const sessionIds = new Set();
  sessions.forEach((session) => {
    const current = assertObject(session, label);
    const sessionId = assertResponseUUID(current.session_id, label);
    assertResponseText(current.session_title, label);
    const status = String(current.status || "");
    const hasReviewTarget = current.review_path !== undefined;
    const validTarget = hasReviewTarget
      ? Boolean(expectedInstanceId) &&
        Boolean(responseInstanceId) &&
        PUBLIC_REVIEW_SESSION_STATUSES.has(status) &&
        current.detail_path === undefined &&
        current.review_path ===
          `${API_PREFIX}/instances/${responseInstanceId}/review?session_id=${sessionId}`
      : status !== "archived" &&
        current.review_path === undefined &&
        current.detail_path === `${API_PREFIX}/sessions/${sessionId}`;
    if (
      sessionIds.has(sessionId) ||
      !allowedStatuses.has(status) ||
      !Number.isSafeInteger(current.sort_order) ||
      current.sort_order < 0 ||
      !validTarget
    ) {
      throw invalidResponse(label);
    }
    sessionIds.add(sessionId);
    assertResponseTime(current.session_start_at, label);
  });
  if (sessions.length) {
    assertResponseUUID(result.series_id, label, expectedSeriesId);
    assertResponseUUID(result.instance_id, label, expectedInstanceId);
  }
  const action = String(result.action || "").trim();
  if (action === "unavailable") {
    if (result.direct_session_id || sessions.length) throw invalidResponse(label);
  } else if (action === "session_detail") {
    const direct = assertResponseUUID(result.direct_session_id, label);
    if (sessions.length !== 1 || String(sessions[0].session_id).toLowerCase() !== direct) {
      throw invalidResponse(label);
    }
  } else if (action === "session_selection_required") {
    if (result.direct_session_id || sessions.length < 2) throw invalidResponse(label);
  } else {
    throw invalidResponse(label);
  }
  if (
    (sessions.length === 0 && result.empty_state !== "no_public_sessions") ||
    (sessions.length > 0 && result.empty_state !== undefined)
  ) {
    throw invalidResponse(label);
  }
  return result;
}

function assertMyOrderRefund(value, label) {
  const refund = assertObject(value, label);
  assertResponseUUID(refund.refund_case_id, label);
  const status = String(refund.refund_status || "").trim();
  if (!REFUND_STATUSES.has(status)) throw invalidResponse(label);
  assertResponseText(refund.reason_code, label);
  const requested = assertNonnegativeInteger(refund.requested_refund_cents, label, 1);
  const successful = assertNonnegativeInteger(refund.successful_refund_cents, label);
  if (successful > requested) throw invalidResponse(label);
  assertNonnegativeInteger(refund.version, label, 1);
  const updatedAt = assertResponseTime(refund.updated_at, label);
  const resolvedAt = assertOptionalResponseTime(refund.resolved_at, label);
  if (resolvedAt && Date.parse(resolvedAt) > Date.parse(updatedAt)) {
    throw invalidResponse(label);
  }
  if (
    (status === "refunded" && successful !== requested) ||
    (status !== "refunded" && successful !== 0) ||
    (OPEN_REFUND_STATUSES.has(status) && resolvedAt) ||
    (!OPEN_REFUND_STATUSES.has(status) && !resolvedAt)
  ) {
    throw invalidResponse(label);
  }
  return { status, updatedAt };
}

function expectedMyOrderOutcome(paymentStatus, refundStatus) {
  if (refundStatus) {
    return {
      pending_manual: "refund_pending_manual",
      processing: "refund_processing",
      failed: "refund_failed",
      rejected: "refund_rejected",
      refunded: "refunded",
    }[refundStatus];
  }
  return {
    pending: "pending_payment",
    unknown: "payment_confirming",
    paid_confirmed: "paid_confirmed",
    settled_zero: "settled_zero",
    closed_unpaid: "closed_unpaid",
  }[paymentStatus];
}

function expectedMyOrderState(paymentStatus, refundStatus) {
  if (refundStatus === "refunded") return "refunded";
  if (new Set(["pending_manual", "processing", "failed"]).has(refundStatus)) {
    return "refund_processing";
  }
  if (paymentStatus === "paid_confirmed" || paymentStatus === "settled_zero") return "paid";
  if (paymentStatus === "closed_unpaid") return "closed";
  return "pending_payment";
}

function assertMyOrderItem(value, label, expectedOrderId = "") {
  const item = assertObject(value, label);
  assertResponseUUID(item.order_id, label, expectedOrderId);
  assertNonnegativeInteger(item.order_version, label, 1);
  assertResponseUUID(item.registration_id, label);
  assertNonnegativeInteger(item.registration_version, label, 1);
  assertResponseUUID(item.series_id, label);
  assertResponseText(item.series_title, label);
  assertResponseUUID(item.instance_id, label);
  assertResponseText(item.instance_title, label);
  assertResponseUUID(item.session_id, label);
  assertResponseText(item.session_title, label);
  const sessionStartAt = assertResponseTime(item.session_start_at, label);
  const sessionEndAt = assertResponseTime(item.session_end_at, label);
  if (Date.parse(sessionStartAt) >= Date.parse(sessionEndAt)) throw invalidResponse(label);

  const participationStatus = String(item.participation_status || "").trim();
  const reservationState = String(item.reservation_state || "").trim();
  const paymentStatus = String(item.payment_status || "").trim();
  const holdStatus = String(item.hold_status || "").trim();
  const state = String(item.state || "").trim();
  const outcome = String(item.outcome || "").trim();
  if (
    !PARTICIPATION_STATUSES.has(participationStatus) ||
    !MY_REGISTRATION_STATES.has(reservationState) ||
    !PAYMENT_STATUSES.has(paymentStatus) ||
    !CAPACITY_HOLD_STATUSES.has(holdStatus) ||
    !MY_ORDER_STATES.has(state) ||
    state === "all" ||
    !MY_ORDER_OUTCOMES.has(outcome)
  ) {
    throw invalidResponse(label);
  }

  assertResponseBoolean(item.reservation_has_active_access, label);
  const confirmationPending = assertResponseBoolean(item.payment_confirmation_pending, label);
  const canContinuePayment = assertResponseBoolean(item.can_continue_payment, label);
  if (
    confirmationPending !== (paymentStatus === "unknown") ||
    (canContinuePayment && paymentStatus !== "pending")
  ) {
    throw invalidResponse(label);
  }

  const originalPrice = assertNonnegativeInteger(item.original_price_cents, label, 1);
  const discount = assertNonnegativeInteger(item.discount_cents, label);
  const payable = assertNonnegativeInteger(item.payable_cents, label);
  if (discount > originalPrice || payable !== originalPrice - discount || item.currency !== "CNY") {
    throw invalidResponse(label);
  }
  const actualPaidPresent = item.actual_paid_cents !== undefined;
  if (paymentStatus === "paid_confirmed") {
    if (!actualPaidPresent || assertNonnegativeInteger(item.actual_paid_cents, label) !== payable) {
      throw invalidResponse(label);
    }
  } else if (actualPaidPresent) {
    throw invalidResponse(label);
  }
  if (paymentStatus === "settled_zero" && payable !== 0) throw invalidResponse(label);
  if (POSITIVE_PAYABLE_STATUSES.has(paymentStatus) && payable < 1) {
    throw invalidResponse(label);
  }

  const paidAt = assertOptionalResponseTime(item.paid_at, label);
  const closedAt = assertOptionalResponseTime(item.closed_at, label);
  if ((paymentStatus === "paid_confirmed") !== Boolean(paidAt)) throw invalidResponse(label);
  if (
    (paymentStatus === "closed_unpaid" && !closedAt) ||
    (closedAt && paymentStatus !== "closed_unpaid" && paymentStatus !== "paid_confirmed")
  ) {
    throw invalidResponse(label);
  }
  assertResponseTime(item.hold_expires_at, label);
  assertNonnegativeInteger(item.hold_version, label, 1);
  const lastBusinessAt = assertResponseTime(item.last_business_at, label);

  let refundStatus = "";
  let refundUpdatedAt = "";
  if (item.refund !== undefined) {
    if (paymentStatus !== "paid_confirmed") throw invalidResponse(label);
    const refund = assertMyOrderRefund(item.refund, label);
    refundStatus = refund.status;
    refundUpdatedAt = refund.updatedAt;
  }
  if (
    expectedMyOrderState(paymentStatus, refundStatus) !== state ||
    expectedMyOrderOutcome(paymentStatus, refundStatus) !== outcome ||
    (refundUpdatedAt && Date.parse(refundUpdatedAt) > Date.parse(lastBusinessAt))
  ) {
    throw invalidResponse(label);
  }
  return { orderId: String(item.order_id).toLowerCase(), state, lastBusinessAt };
}

function assertMyCouponItem(value, label, asOf) {
  const item = assertObject(value, label);
  const couponId = assertResponseUUID(item.coupon_id, label);
  assertNonnegativeInteger(item.face_value_cents, label, 1);
  assertNonnegativeInteger(item.minimum_order_cents, label);
  if (item.currency !== "CNY") throw invalidResponse(label);
  const validFrom = assertResponseTime(item.valid_from, label);
  const expiresAt = assertResponseTime(item.expires_at, label);
  if (Date.parse(validFrom) >= Date.parse(expiresAt)) throw invalidResponse(label);
  if (!COUPON_GRANT_KINDS.has(String(item.grant_kind || ""))) throw invalidResponse(label);

  const applicability = assertObject(item.applicability, label);
  const scopeType = String(applicability.scope_type || "").trim();
  if (!COUPON_SCOPE_TYPES.has(scopeType)) throw invalidResponse(label);
  if (scopeType === "activity_type") {
    if (
      !ACTIVITY_TYPES.has(String(applicability.activity_type || "")) ||
      applicability.series_id !== undefined
    ) {
      throw invalidResponse(label);
    }
  } else {
    if (applicability.activity_type !== undefined) throw invalidResponse(label);
    assertResponseUUID(applicability.series_id, label);
  }

  const state = String(item.state || "").trim();
  if (!MY_COUPON_STATES.has(state) || state === "all") throw invalidResponse(label);
  const usable = assertResponseBoolean(item.usable, label);
  const correctionRequired = assertResponseBoolean(item.correction_required, label);
  if (
    usable !== (state === "available") ||
    correctionRequired !== (state === "correction_required")
  ) {
    throw invalidResponse(label);
  }

  const activeOrderPresent = item.active_order_id !== undefined;
  const activeRegistrationPresent = item.active_registration_id !== undefined;
  if (
    activeOrderPresent !== activeRegistrationPresent ||
    (state === "held" && !activeOrderPresent)
  ) {
    throw invalidResponse(label);
  }
  if (activeOrderPresent) {
    assertResponseUUID(item.active_order_id, label);
    assertResponseUUID(item.active_registration_id, label);
    if (state !== "held" && state !== "correction_required") throw invalidResponse(label);
  }

  if (item.latest_adjustment !== undefined) {
    const adjustment = assertObject(item.latest_adjustment, label);
    if (!COUPON_ADJUSTMENTS.has(String(adjustment.disposition || ""))) {
      throw invalidResponse(label);
    }
    const occurredAt = assertResponseTime(adjustment.occurred_at, label);
    if (Date.parse(occurredAt) > Date.parse(asOf)) throw invalidResponse(label);
  }
  return { couponId, state };
}

function assertMyFavoriteItem(value, label, asOf) {
  const item = assertObject(value, label);
  const seriesId = assertResponseUUID(item.series_id, label);
  assertResponseText(item.title, label);
  const seriesStatus = String(item.series_status || "").trim();
  if (!SERIES_STATUSES.has(seriesStatus)) throw invalidResponse(label);
  assertNonnegativeInteger(item.favorite_count, label);
  const favoritedAt = assertResponseTime(item.favorited_at, label);
  const sessionsAvailable = assertResponseBoolean(item.sessions_available, label);
  if (
    Date.parse(favoritedAt) > Date.parse(asOf) ||
    (sessionsAvailable && seriesStatus !== "active") ||
    (sessionsAvailable && item.sessions_path !== `${API_PREFIX}/series/${seriesId}/sessions`) ||
    (!sessionsAvailable && item.sessions_path !== undefined)
  ) {
    throw invalidResponse(label);
  }
  return seriesId;
}

function assertSeriesFavoriteState(value, seriesId, favorited) {
  const result = assertObject(value, "收藏状态");
  assertResponseUUID(result.series_id, "收藏状态", seriesId);
  if (
    assertResponseBoolean(result.favorited, "收藏状态") !== favorited ||
    typeof result.changed !== "boolean"
  ) {
    throw invalidResponse("收藏状态");
  }
  assertNonnegativeInteger(result.favorite_count, "收藏状态");
  assertNonnegativeInteger(result.series_version, "收藏状态", 1);
  assertResponseTime(result.occurred_at, "收藏状态");
  return result;
}

function assertPublicPerson(value, label, expectedPeopleId = "") {
  const person = assertObject(value, label);
  const peopleId = assertResponseUUID(person.people_id, label, expectedPeopleId);
  assertBoundedResponseText(person.display_name, label, 120);
  if (person.headline !== undefined) {
    assertBoundedResponseText(person.headline, label, 200);
  }
  assertBoundedResponseText(person.introduction, label, 4000, false);
  assertNonnegativeInteger(person.version, label, 1);
  const publishedAt = assertResponseTime(person.published_at, label);
  return { peopleId, publishedAt };
}

function assertIdentityRole(value, label) {
  const role = assertObject(value, label);
  const seriesId = assertResponseUUID(role.series_id, label);
  const instanceId = assertResponseUUID(role.instance_id, label);
  const roleCode = String(role.role_code || "").trim();
  const roleStatus = String(role.role_status || "").trim();
  const instanceStatus = String(role.instance_status || "").trim();
  const state = String(role.state || "").trim();
  if (
    role.role_code !== roleCode ||
    role.role_status !== roleStatus ||
    role.instance_status !== instanceStatus ||
    role.state !== state ||
    !IDENTITY_ROLE_CODES.has(roleCode) ||
    !IDENTITY_ROLE_STATUSES.has(roleStatus) ||
    !IDENTITY_INSTANCE_STATUSES.has(instanceStatus) ||
    !IDENTITY_ROLE_STATES.has(state)
  ) {
    throw invalidResponse(label);
  }
  assertBoundedResponseText(role.series_title, label, 200);
  assertBoundedResponseText(role.instance_title, label, 200);
  const grantedAt = assertResponseTime(role.granted_at, label);
  const revokedAt = assertOptionalResponseTime(role.revoked_at, label);
  if (
    (roleStatus === "active" && revokedAt) ||
    (roleStatus === "revoked" && !revokedAt) ||
    (revokedAt && Date.parse(revokedAt) < Date.parse(grantedAt)) ||
    (state === "current") !==
      (roleStatus === "active" && IDENTITY_CURRENT_INSTANCE_STATUSES.has(instanceStatus))
  ) {
    throw invalidResponse(label);
  }
  return {
    key: `${seriesId}:${instanceId}:${roleCode}`,
    roleCode,
    state,
    grantedAt,
  };
}

function assertIdentityRoles(value, label, requiredState = "") {
  const roles = assertResponseArray(value, label);
  const checked = [];
  let previousGrantedAt = Number.POSITIVE_INFINITY;
  roles.forEach((role) => {
    const current = assertIdentityRole(role, label);
    const grantedAt = Date.parse(current.grantedAt);
    if (grantedAt > previousGrantedAt || (requiredState && current.state !== requiredState)) {
      throw invalidResponse(label);
    }
    previousGrantedAt = grantedAt;
    current.key = `${current.key}:${current.grantedAt}`;
    checked.push(current);
  });
  return checked;
}

function assertHostApplication(value, label) {
  const application = assertObject(value, label);
  const applicationId = assertResponseUUID(application.application_id, label);
  const applicationCycle = assertBoundedResponseText(application.application_cycle, label, 100);
  const policyVersion = assertBoundedResponseText(application.policy_version, label, 100);
  const status = String(application.application_status || "").trim();
  if (application.application_status !== status || !HOST_APPLICATION_STATUSES.has(status)) {
    throw invalidResponse(label);
  }
  const version = assertNonnegativeInteger(application.version, label, 1);
  const submittedAt = assertResponseTime(application.submitted_at, label);
  const updatedAt = assertResponseTime(application.updated_at, label);
  const reviewComment =
    application.review_comment === undefined
      ? ""
      : assertBoundedResponseText(application.review_comment, label, 2000);
  if (
    !HOST_APPLICATION_REFERENCE_PATTERN.test(applicationCycle) ||
    !HOST_APPLICATION_REFERENCE_PATTERN.test(policyVersion) ||
    Date.parse(updatedAt) < Date.parse(submittedAt) ||
    (status === "pending" && (version !== 1 || reviewComment || updatedAt !== submittedAt)) ||
    (status !== "pending" && version !== 2) ||
    (status === "approved" || status === "rejected") !== Boolean(reviewComment)
  ) {
    throw invalidResponse(label);
  }
  return { applicationId, status, submittedAt };
}

function assertHostApplications(value, label) {
  const applications = assertResponseArray(value, label);
  const checked = [];
  const seen = new Set();
  let previousSubmittedAt = Number.POSITIVE_INFINITY;
  applications.forEach((application) => {
    const current = assertHostApplication(application, label);
    const submittedAt = Date.parse(current.submittedAt);
    if (seen.has(current.applicationId) || submittedAt > previousSubmittedAt) {
      throw invalidResponse(label);
    }
    seen.add(current.applicationId);
    previousSubmittedAt = submittedAt;
    checked.push(current);
  });
  return checked;
}

function assertContributions(value, label) {
  const contributions = assertResponseArray(value, label);
  const seen = new Set();
  let activeCount = 0;
  let previousEarnedAt = Number.POSITIVE_INFINITY;
  contributions.forEach((value) => {
    const item = assertObject(value, label);
    const seriesId = assertResponseUUID(item.series_id, label);
    const instanceId = assertResponseUUID(item.instance_id, label);
    if (item.contribution_type !== "host_checkin" || !CONTRIBUTION_STATES.has(item.state)) {
      throw invalidResponse(label);
    }
    const earnedAt = assertResponseTime(item.earned_at, label);
    const reversedAt = assertOptionalResponseTime(item.reversed_at, label);
    const key = `${instanceId}:${item.contribution_type}`;
    if (
      seen.has(key) ||
      Date.parse(earnedAt) > previousEarnedAt ||
      (item.state === "active" && reversedAt) ||
      (item.state === "reversed" && !reversedAt) ||
      (reversedAt && Date.parse(reversedAt) < Date.parse(earnedAt))
    ) {
      throw invalidResponse(label);
    }
    seen.add(key);
    previousEarnedAt = Date.parse(earnedAt);
    if (item.state === "active") activeCount += 1;
  });
  return { contributions, activeCount };
}

function assertMyBenefits(value) {
  const label = "我的身份与贡献";
  const result = assertObject(value, label);
  const trustedPeopleProfileId =
    result.trusted_people_profile_id === undefined
      ? ""
      : assertResponseUUID(result.trusted_people_profile_id, label);
  const hasHostIdentity = assertResponseBoolean(result.has_host_identity, label);
  const currentRoles = assertIdentityRoles(result.current_roles, label, "current");
  const roleHistory = assertIdentityRoles(result.role_history, label);
  const expectedCurrentRoleKeys = roleHistory
    .filter(({ state }) => state === "current")
    .map(({ key }) => key);
  if (
    currentRoles.length !== expectedCurrentRoleKeys.length ||
    currentRoles.some(({ key }, index) => key !== expectedCurrentRoleKeys[index])
  ) {
    throw invalidResponse(label);
  }

  const hostRules = assertObject(result.host_rules, label);
  const hostRulesState = String(hostRules.state || "").trim();
  if (hostRules.state !== hostRulesState || !HOST_RULES_STATES.has(hostRulesState)) {
    throw invalidResponse(label);
  }
  const cycle = hostRules.application_cycle || "",
    policy = hostRules.policy_version || "";
  if (
    (cycle || policy) &&
    (hostRulesState !== "configured" ||
      !/^[A-Za-z0-9][A-Za-z0-9._:-]{0,99}$/.test(cycle) ||
      !/^[A-Za-z0-9][A-Za-z0-9._:-]{0,99}$/.test(policy))
  )
    throw invalidResponse(label);
  const requirements =
    hostRules.requirements === undefined
      ? ""
      : assertBoundedResponseText(hostRules.requirements, label, 4000);
  const benefits =
    hostRules.benefits === undefined
      ? ""
      : assertBoundedResponseText(hostRules.benefits, label, 4000);
  if (
    (hostRulesState === "pending" && (requirements || benefits)) ||
    (hostRulesState === "configured" && (!requirements || !benefits))
  ) {
    throw invalidResponse(label);
  }

  const applicationHistory = assertHostApplications(result.host_application_history, label);
  if (applicationHistory.filter(({ status }) => status === "approved").length > 1) {
    throw invalidResponse(label);
  }
  const currentApplication =
    result.current_host_application === undefined
      ? null
      : assertHostApplication(result.current_host_application, label);
  if (
    currentApplication &&
    (!new Set(["pending", "approved"]).has(currentApplication.status) ||
      !applicationHistory.some(
        ({ applicationId, status }) =>
          applicationId === currentApplication.applicationId &&
          status === currentApplication.status,
      ))
  ) {
    throw invalidResponse(label);
  }
  const expectedCurrentApplication =
    applicationHistory.find(({ status }) => status === "pending" || status === "approved") || null;
  if (
    Boolean(currentApplication) !== Boolean(expectedCurrentApplication) ||
    (currentApplication &&
      (currentApplication.applicationId !== expectedCurrentApplication.applicationId ||
        currentApplication.status !== expectedCurrentApplication.status))
  ) {
    throw invalidResponse(label);
  }

  const { contributions, activeCount } = assertContributions(
    result.host_contribution_history,
    label,
  );
  const hostContributionCount = assertNonnegativeInteger(result.host_contribution_count, label);
  const identityHistoryAvailable = assertResponseBoolean(result.identity_history_available, label);
  const expectedHostIdentity =
    currentRoles.some(({ roleCode }) => roleCode === "host") ||
    applicationHistory.some(({ status }) => status === "approved");
  const expectedHistoryAvailable = Boolean(
    trustedPeopleProfileId ||
      roleHistory.length ||
      applicationHistory.length ||
      contributions.length,
  );
  const canApplyForHost = assertResponseBoolean(result.can_apply_for_host, label);
  if (
    activeCount !== hostContributionCount ||
    hasHostIdentity !== expectedHostIdentity ||
    identityHistoryAvailable !== expectedHistoryAvailable ||
    canApplyForHost !== (hostRulesState === "configured" && !hasHostIdentity && !currentApplication)
  ) {
    throw invalidResponse(label);
  }
  return result;
}

function createXiangwanApi(options = {}) {
  const request = options.request || http.request;
  const upload = options.uploadFile || http.uploadFile;
  const makeIdempotencyKey = options.generateIdempotencyKey || http.generateIdempotencyKey;

  return {
    newIdempotencyKey() {
      return makeIdempotencyKey();
    },

    async getHomeSessions(filter = {}) {
      const result = assertObject(
        await request({
          url: `${API_PREFIX}/home-sessions${homeQuery(filter)}`,
          authMode: "none",
        }),
        "活动列表",
      );
      assertResponseArray(result.cards, "活动列表").forEach((card) => {
        assertResponseUUID(card && card.session_id, "活动列表");
      });
      const communityName = assertBoundedResponseText(
        result.community_name,
        "活动列表",
        100,
        false,
      );
      const brandIntro = assertBoundedResponseText(result.brand_intro, "活动列表", 2000, false);
      const heroMode = String(result.hero_mode || "");
      const heroEyebrow = assertBoundedResponseText(result.hero_eyebrow, "活动列表", 100, false);
      const heroSubtitle = assertBoundedResponseText(result.hero_subtitle, "活动列表", 200, false);
      const heroImageURL = assertBoundedResponseText(
        result.hero_image_url,
        "活动列表",
        2048,
        false,
      );
      const heroImageAlt = assertBoundedResponseText(result.hero_image_alt, "活动列表", 200, false);
      if (
        !new Set(["text", "image"]).has(heroMode) ||
        (heroMode === "text" && (heroImageURL || heroImageAlt)) ||
        (heroMode === "image" && (!/^https:\/\/\S+$/.test(heroImageURL) || !heroImageAlt)) ||
        !new Set(["active", "suspended"]).has(String(result.brand_status || ""))
      ) {
        throw invalidResponse("活动列表");
      }
      assertNonnegativeInteger(result.brand_publication_version, "活动列表", 1);
      assertResponseTime(result.brand_published_at, "活动列表");
      assertResponseArray(result.available_quick_tags, "活动列表").forEach((tag) => {
        const code = String((tag && tag.code) || "");
        const label = assertBoundedResponseText(tag && tag.label, "活动列表", 40);
        if (!/^[a-z][a-z0-9_]{0,31}$/.test(code) || !label) {
          throw invalidResponse("活动列表");
        }
      });
      return result;
    },

    async getPastActivities(filter = {}) {
      const result = assertObject(
        await request({
          url: `${API_PREFIX}/past-activities${pastActivitiesQuery(filter)}`,
          authMode: "none",
        }),
        "往期活动",
      );
      assertResponseArray(result.items, "往期活动").forEach((item) => {
        const current = assertObject(item, "往期活动");
        assertResponseUUID(current.series_id, "往期活动");
        assertResponseUUID(current.instance_id, "往期活动");
        const seriesTitle = String(current.series_title || "").trim();
        const instanceTitle = String(current.instance_title || "").trim();
        const publishedAt = assertResponseTime(current.published_at, "往期活动");
        const completedAt = assertResponseTime(current.completed_at, "往期活动");
        if (
          !seriesTitle ||
          !instanceTitle ||
          !ACTIVITY_TYPES.has(String(current.activity_type || "")) ||
          !new Set(["completed", "archived"]).has(String(current.instance_status || "")) ||
          assertNonnegativeInteger(current.successful_published_instance_count, "往期活动", 1) <
            1 ||
          assertNonnegativeInteger(current.historical_registration_count, "往期活动") < 0 ||
          assertNonnegativeInteger(current.publication_version, "往期活动", 1) < 1 ||
          Date.parse(publishedAt) > Date.parse(completedAt)
        ) {
          throw invalidResponse("往期活动");
        }
      });
      const activeType = String(result.active_activity_type || "").trim();
      if (activeType !== "all" && !ACTIVITY_TYPES.has(activeType)) {
        throw invalidResponse("往期活动");
      }
      return result;
    },

    async getPeople(filter = {}) {
      const result = assertObject(
        await request({
          url: `${API_PREFIX}/people${peopleQuery(filter)}`,
          authMode: "none",
        }),
        "社区人物",
      );
      const asOf = assertResponseTime(result.as_of, "社区人物");
      const items = assertResponseArray(result.items, "社区人物");
      const requestedLimit =
        Number.isSafeInteger(filter.limit) && filter.limit > 0 ? filter.limit : 20;
      if (items.length > requestedLimit) throw invalidResponse("社区人物");
      const seen = new Set();
      items.forEach((item) => {
        const checked = assertPublicPerson(item, "社区人物");
        if (seen.has(checked.peopleId) || Date.parse(checked.publishedAt) > Date.parse(asOf)) {
          throw invalidResponse("社区人物");
        }
        seen.add(checked.peopleId);
      });
      const nextCursor = assertResponseCursor(result.next_cursor, "社区人物");
      if (
        (items.length === 0 && (nextCursor || result.empty_state !== "no_people")) ||
        (items.length > 0 && result.empty_state !== undefined)
      ) {
        throw invalidResponse("社区人物");
      }
      return result;
    },

    async getPerson(peopleId) {
      const id = canonicalUUID(peopleId, "社区人物");
      const result = assertObject(
        await request({ url: `${API_PREFIX}/people/${id}`, authMode: "none" }),
        "人物详情",
      );
      assertPublicPerson(result.person, "人物详情", id);
      return result;
    },

    async getSessionDetail(sessionId) {
      const id = canonicalUUID(sessionId, "活动场次");
      const result = assertObject(
        await request({ url: `${API_PREFIX}/sessions/${id}`, authMode: "none" }),
        "活动详情",
      );
      assertResponseUUID(result.series_id, "活动详情");
      assertResponseUUID(result.instance_id, "活动详情");
      assertResponseUUID(result.session_id, "活动详情", id);
      if (result.previous_review != null) {
        const review = assertObject(result.previous_review, "上一期精彩");
        const previousInstanceID = assertResponseUUID(review.instance_id, "上一期精彩");
        if (previousInstanceID === result.instance_id) throw invalidResponse("上一期精彩");
        if (review.session_id !== undefined) assertResponseUUID(review.session_id, "上一期精彩");
      }
      return result;
    },

    async getInstanceSessions(instanceId) {
      const id = canonicalUUID(instanceId, "活动期次");
      const result = assertObject(
        await request({
          url: `${API_PREFIX}/instances/${id}/sessions`,
          authMode: "none",
        }),
        "活动场次",
      );
      return assertPublicSessionCollection(result, { instanceId: id });
    },

    async getSeriesSessions(seriesId) {
      const id = canonicalUUID(seriesId, "活动系列");
      const result = assertObject(
        await request({ url: `${API_PREFIX}/series/${id}/sessions`, authMode: "none" }),
        "活动场次",
      );
      return assertPublicSessionCollection(result, { seriesId: id });
    },

    async getPublicReview(instanceId, sessionId = "") {
      const id = canonicalUUID(instanceId, "活动期次");
      const selectedSessionId = sessionId ? canonicalUUID(sessionId, "活动场次") : "";
      const query = selectedSessionId ? `?session_id=${encodeURIComponent(selectedSessionId)}` : "";
      const result = assertObject(
        await request({
          url: `${API_PREFIX}/instances/${id}/review${query}`,
          authMode: "none",
        }),
        "活动回顾",
      );
      assertResponseUUID(result.series_id, "活动回顾");
      assertResponseUUID(result.instance_id, "活动回顾", id);
      const seriesTitle = String(result.series_title || "").trim();
      const instanceTitle = String(result.instance_title || "").trim();
      const publishedAt = assertResponseTime(result.published_at, "活动回顾");
      const completedAt = assertResponseTime(result.completed_at, "活动回顾");
      if (
        !seriesTitle ||
        !instanceTitle ||
        !ACTIVITY_TYPES.has(String(result.activity_type || "")) ||
        !new Set(["completed", "archived"]).has(String(result.instance_status || "")) ||
        assertNonnegativeInteger(result.successful_published_instance_count, "活动回顾", 1) < 1 ||
        assertNonnegativeInteger(result.historical_registration_count, "活动回顾") < 0 ||
        assertNonnegativeInteger(result.publication_version, "活动回顾", 1) < 1 ||
        Date.parse(publishedAt) > Date.parse(completedAt)
      ) {
        throw invalidResponse("活动回顾");
      }
      assertResponseArray(result.content_blocks, "活动详情").forEach(
        assertPublicReviewContentBlock,
      );
      const returnedSessionId = String(result.selected_session_id || "")
        .trim()
        .toLowerCase();
      if (
        (selectedSessionId && returnedSessionId !== selectedSessionId) ||
        (!selectedSessionId && returnedSessionId)
      ) {
        throw invalidResponse("活动回顾");
      }
      assertResponseArray(result.documents, "活动回顾").forEach((document) => {
        const current = assertObject(document, "活动回顾");
        assertResponseUUID(current.relation_id, "活动回顾");
        const scope = String(current.scope || "").trim();
        if (scope === "instance_review") {
          if (current.session_id) throw invalidResponse("活动回顾");
        } else if (scope === "session_resources" && current.session_id) {
          const documentSessionId = assertResponseUUID(current.session_id, "活动回顾");
          if (!selectedSessionId || documentSessionId !== selectedSessionId) {
            throw invalidResponse("活动回顾");
          }
        } else {
          throw invalidResponse("活动回顾");
        }
        assertResponseArray(current.blocks, "活动回顾").forEach(assertPublicReviewBlock);
      });
      assertNextInstanceRoute(result.next_instance, id);
      return result;
    },

    async getPublicPolicies() {
      const result = assertObject(
        await request({ url: `${API_PREFIX}/public-policies`, authMode: "none" }),
        "报名政策",
      );
      assertResponseArray(result.published_versions, "报名政策");
      assertObject(result.capabilities, "报名政策");
      return result;
    },

    async getSessionQuestionnaire(sessionId) {
      const id = canonicalUUID(sessionId, "活动场次");
      const result = assertObject(
        await request({ url: `${API_PREFIX}/sessions/${id}/questionnaire` }),
        "报名问卷",
      );
      assertResponseUUID(result.session_id, "报名问卷", id);
      if (result.available === false) {
        if (
          result.questionnaire_version_id ||
          !Array.isArray(result.fields) ||
          result.fields.length
        ) {
          throw invalidResponse("报名问卷");
        }
        return null;
      }
      assertResponseUUID(result.questionnaire_version_id, "报名问卷");
      assertResponseArray(result.fields, "报名问卷");
      return result;
    },

    async getQuestionnairePrefill(sessionId) {
      const id = canonicalUUID(sessionId, "活动场次");
      const result = assertObject(
        await request({ url: `${API_PREFIX}/me/questionnaire-prefill/${id}` }),
        "历史问卷",
      );
      assertResponseUUID(result.session_id, "历史问卷", id);
      if (result.questionnaire_version_id)
        assertResponseUUID(result.questionnaire_version_id, "历史问卷");
      assertResponseArray(result.answers, "历史问卷");
      if (
        result.answers.length > 100 ||
        (!result.questionnaire_version_id && result.answers.length)
      )
        throw invalidResponse("历史问卷");
      result.answers.forEach((answer) => {
        assertObject(answer, "历史问卷");
        assertResponseUUID(answer.field_id, "历史问卷");
        if (
          !Array.isArray(answer.values) ||
          answer.values.length > 100 ||
          !answer.values.every((value) => typeof value === "string")
        )
          throw invalidResponse("历史问卷");
      });
      return result;
    },

    async createRegistration(sessionId, payload, idempotencyKey) {
      const id = canonicalUUID(sessionId, "活动场次");
      const operationKey = canonicalUUID(idempotencyKey, "报名操作");
      const submission = registrationSubmissionTransport(payload);
      const result = assertObject(
        await request({
          url: `${API_PREFIX}/sessions/${id}/registrations`,
          method: "POST",
          data: submission.body,
          header: {
            "Idempotency-Key": operationKey,
            "X-Xiangwan-Privacy-Policy-Version": submission.privacyPolicyVersion,
          },
        }),
        "报名结果",
      );
      assertResponseUUID(result.registration_id, "报名结果");
      assertResponseUUID(result.session_id, "报名结果", id);
      if (
        ["wechat_payment_required", "payment_confirmation_pending"].includes(result.next_action) &&
        (!result.order || typeof result.order !== "object")
      ) {
        throw invalidResponse("报名结果");
      }
      if (result.order !== undefined && result.order !== null) {
        const order = assertObject(result.order, "报名结果");
        assertResponseUUID(order.order_id, "报名结果");
        const payable = assertNonnegativeInteger(order.payable_cents, "报名结果");
        if (
          result.next_action === "wechat_payment_required" &&
          (payable <= 0 || order.payment_status !== "pending")
        ) {
          throw invalidResponse("报名结果");
        }
      }
      return result;
    },

    async getMyRegistrations(filter = {}) {
      const result = assertObject(
        await request({ url: `${API_PREFIX}/me/registrations${registrationsQuery(filter)}` }),
        "我的报名",
      );
      assertResponseArray(result.items, "我的报名").forEach((item) => {
        assertResponseUUID(item && item.registration_id, "我的报名");
        assertResponseUUID(item && item.session_id, "我的报名");
      });
      return result;
    },

    async getMyOrders(filter = {}) {
      const result = assertObject(
        await request({ url: `${API_PREFIX}/me/orders${ordersQuery(filter)}` }),
        "我的订单",
      );
      const asOf = assertResponseTime(result.as_of, "我的订单");
      const activeState = String(result.active_state || "").trim();
      const requestedState = String(filter.state || "").trim() || "all";
      if (!MY_ORDER_STATES.has(activeState) || activeState !== requestedState) {
        throw invalidResponse("我的订单");
      }
      const seenOrders = new Set();
      const seenRegistrations = new Set();
      const items = assertResponseArray(result.items, "我的订单");
      const requestedLimit =
        Number.isSafeInteger(filter.limit) && filter.limit > 0 ? filter.limit : 20;
      if (items.length > requestedLimit) throw invalidResponse("我的订单");
      let previousRank = 0;
      items.forEach((item) => {
        const checked = assertMyOrderItem(item, "我的订单");
        const registrationId = String(item.registration_id).toLowerCase();
        const rank = MY_ORDER_STATE_RANK[checked.state];
        const sortTime = Date.parse(checked.lastBusinessAt);
        if (
          seenOrders.has(checked.orderId) ||
          seenRegistrations.has(registrationId) ||
          (activeState !== "all" && checked.state !== activeState) ||
          sortTime > Date.parse(asOf) ||
          previousRank > rank
        ) {
          throw invalidResponse("我的订单");
        }
        seenOrders.add(checked.orderId);
        seenRegistrations.add(registrationId);
        previousRank = rank;
      });
      const nextCursor = assertResponseCursor(result.next_cursor, "我的订单");
      if (
        (items.length === 0 && (nextCursor || result.empty_state !== "no_orders")) ||
        (items.length > 0 && result.empty_state !== undefined)
      ) {
        throw invalidResponse("我的订单");
      }
      return result;
    },

    async getMyOrderDetail(orderId) {
      const id = canonicalUUID(orderId, "订单");
      const result = assertObject(
        await request({ url: `${API_PREFIX}/me/orders/${id}` }),
        "订单详情",
      );
      const asOf = assertResponseTime(result.as_of, "订单详情");
      const checked = assertMyOrderItem(result.order, "订单详情", id);
      if (Date.parse(checked.lastBusinessAt) > Date.parse(asOf)) {
        throw invalidResponse("订单详情");
      }
      return result;
    },

    async createWeChatPrepayAttempt(orderId, orderVersion, payableCents, operationKey) {
      const id = canonicalUUID(orderId, "订单");
      const key = canonicalUUID(operationKey, "支付操作");
      if (
        key[14] !== "4" ||
        !Number.isSafeInteger(orderVersion) ||
        orderVersion < 1 ||
        !Number.isSafeInteger(payableCents) ||
        payableCents <= 0
      ) {
        throw createXiangwanError("invalid_payment_request", "订单信息已变化，请刷新后重试");
      }
      const result = assertObject(
        await request({
          url: `${API_PREFIX}/orders/${id}/wechat-prepay-attempts`,
          method: "POST",
          data: { order_version: orderVersion, payable_cents: payableCents },
          header: { "Idempotency-Key": key },
        }),
        "微信支付",
      );
      assertResponseUUID(result.attempt_id, "微信支付");
      assertResponseUUID(result.order_id, "微信支付", id);
      assertResponseTime(result.hold_expires_at, "微信支付");
      const ready =
        result.attempt_status === "ready" && result.next_action === "invoke_wechat_payment";
      const pending =
        new Set(["in_progress", "unknown"]).has(result.attempt_status) &&
        result.next_action === "payment_confirmation_pending";
      if (!ready && !pending) throw invalidResponse("微信支付");
      if (ready) {
        const params = assertObject(result.payment_parameters, "微信支付");
        if (
          !/^\d{10,13}$/.test(String(params.timeStamp || "")) ||
          !String(params.nonceStr || "").trim() ||
          !/^prepay_id=[^\s\x00-\x1f]{1,64}$/.test(String(params.package || "")) ||
          params.signType !== "RSA" ||
          !String(params.paySign || "").trim()
        ) {
          throw invalidResponse("微信支付");
        }
      } else if (result.payment_parameters !== undefined) {
        throw invalidResponse("微信支付");
      }
      return result;
    },

    async queryWeChatPayment(orderId) {
      const id = canonicalUUID(orderId, "订单");
      const result = assertObject(
        await request({
          url: `${API_PREFIX}/orders/${id}/payment-queries`,
          method: "POST",
          header: EMPTY_BODY_HEADER,
        }),
        "支付查询",
      );
      assertResponseUUID(result.order_id, "支付查询", id);
      if (
        !PAYMENT_STATUSES.has(result.payment_status) ||
        !new Set(["in_progress", "pending", "unknown", "converged", "closed"]).has(
          result.query_status,
        ) ||
        typeof result.confirmation_pending !== "boolean" ||
        typeof result.retry_payment_allowed !== "boolean" ||
        (result.retry_payment_allowed &&
          (result.query_status !== "pending" || result.payment_status !== "pending")) ||
        !new Set([
          "retry_payment_query",
          "wait_for_payment_confirmation",
          "payment_confirmed",
          "payment_closed",
          "refund_processing",
        ]).has(result.next_action) ||
        (result.next_query_at && !assertResponseTime(result.next_query_at, "支付查询"))
      ) {
        throw invalidResponse("支付查询");
      }
      return result;
    },

    async getMyCoupons(filter = {}) {
      const result = assertObject(
        await request({ url: `${API_PREFIX}/me/coupons${couponsQuery(filter)}` }),
        "我的优惠券",
      );
      const asOf = assertResponseTime(result.as_of, "我的优惠券");
      const activeState = String(result.active_state || "").trim();
      const requestedState = String(filter.state || "").trim() || "all";
      if (!MY_COUPON_STATES.has(activeState) || activeState !== requestedState) {
        throw invalidResponse("我的优惠券");
      }
      const items = assertResponseArray(result.items, "我的优惠券");
      const requestedLimit =
        Number.isSafeInteger(filter.limit) && filter.limit > 0 ? filter.limit : 20;
      if (items.length > requestedLimit) throw invalidResponse("我的优惠券");
      const seen = new Set();
      let previousRank = 0;
      items.forEach((item) => {
        const checked = assertMyCouponItem(item, "我的优惠券", asOf);
        const rank = MY_COUPON_STATE_RANK[checked.state];
        if (
          seen.has(checked.couponId) ||
          previousRank > rank ||
          (activeState !== "all" && checked.state !== activeState)
        ) {
          throw invalidResponse("我的优惠券");
        }
        seen.add(checked.couponId);
        previousRank = rank;
      });
      const nextCursor = assertResponseCursor(result.next_cursor, "我的优惠券");
      if (
        (items.length === 0 && (nextCursor || result.empty_state !== "no_coupons")) ||
        (items.length > 0 && result.empty_state !== undefined)
      ) {
        throw invalidResponse("我的优惠券");
      }
      return result;
    },

    async getMyFavorites(filter = {}) {
      const result = assertObject(
        await request({ url: `${API_PREFIX}/me/favorites${favoritesQuery(filter)}` }),
        "我的收藏",
      );
      const asOf = assertResponseTime(result.as_of, "我的收藏");
      const items = assertResponseArray(result.items, "我的收藏");
      const requestedLimit =
        Number.isSafeInteger(filter.limit) && filter.limit > 0 ? filter.limit : 20;
      if (items.length > requestedLimit) throw invalidResponse("我的收藏");
      const seen = new Set();
      items.forEach((item) => {
        const seriesId = assertMyFavoriteItem(item, "我的收藏", asOf);
        if (seen.has(seriesId)) throw invalidResponse("我的收藏");
        seen.add(seriesId);
      });
      const nextCursor = assertResponseCursor(result.next_cursor, "我的收藏");
      if (
        (items.length === 0 && (nextCursor || result.empty_state !== "no_favorites")) ||
        (items.length > 0 && result.empty_state !== undefined)
      ) {
        throw invalidResponse("我的收藏");
      }
      return result;
    },

    async applyHostApplication(payload) {
      const v = assertObject(
        await request({ url: `${API_PREFIX}/me/host-applications`, method: "POST", data: payload }),
        "主理人申请",
      );
      return {
        application:
          (assertHostApplication(v.application, "主理人申请"),
          {
            application_id: v.application.application_id,
            application_cycle: v.application.application_cycle,
            policy_version: v.application.policy_version,
            application_status: v.application.application_status,
            version: v.application.version,
            submitted_at: v.application.submitted_at,
            updated_at: v.application.updated_at,
            review_comment: v.application.review_comment,
          }),
        duplicate: assertResponseBoolean(v.duplicate, "主理人申请"),
      };
    },
    async withdrawHostApplication(id, version) {
      const v = assertObject(
        await request({
          url: `${API_PREFIX}/me/host-applications/${canonicalUUID(id, "申请")}/withdrawal`,
          method: "POST",
          data: { expected_version: version },
        }),
        "撤回申请",
      );
      return {
        application:
          (assertHostApplication(v.application, "撤回申请"),
          {
            application_id: v.application.application_id,
            application_cycle: v.application.application_cycle,
            policy_version: v.application.policy_version,
            application_status: v.application.application_status,
            version: v.application.version,
            submitted_at: v.application.submitted_at,
            updated_at: v.application.updated_at,
            review_comment: v.application.review_comment,
          }),
        duplicate: assertResponseBoolean(v.duplicate, "撤回申请"),
      };
    },
    async getMyBenefits() {
      return assertMyBenefits(await request({ url: `${API_PREFIX}/me/benefits` }));
    },

    async previewPeopleBinding(code) {
      const value = assertObject(
        await request({
          url: `${API_PREFIX}/me/people-binding-preview`,
          method: "POST",
          data: { code },
        }),
        "人物绑定预览",
      );
      if (
        !UUID_PATTERN.test(value.people_profile_id) ||
        typeof value.display_name !== "string" ||
        !value.display_name.trim() ||
        typeof value.introduction !== "string" ||
        !Number.isSafeInteger(value.profile_version) ||
        value.profile_version < 1 ||
        !Number.isFinite(Date.parse(value.expires_at))
      )
        throw createXiangwanError("invalid_binding_preview", "人物绑定资料无法确认");
      return {
        people_profile_id: value.people_profile_id,
        display_name: value.display_name,
        headline: typeof value.headline === "string" ? value.headline : "",
        introduction: value.introduction,
        profile_version: value.profile_version,
        expires_at: value.expires_at,
      };
    },

    async acceptPeopleBinding(payload, operation) {
      const value = assertObject(
        await request({
          url: `${API_PREFIX}/me/people-bindings`,
          method: "POST",
          header: { "Idempotency-Key": operation },
          data: payload,
        }),
        "人物绑定结果",
      );
      if (
        !UUID_PATTERN.test(value.people_profile_id) ||
        !["active", "revoked"].includes(value.status) ||
        typeof value.duplicate !== "boolean"
      )
        throw createXiangwanError(
          "invalid_binding_receipt",
          "人物绑定结果无法确认，请沿用原请求核对",
        );
      return {
        people_profile_id: value.people_profile_id,
        status: value.status,
        duplicate: value.duplicate,
      };
    },

    async getMyProfile() {
      // The profile read endpoint is deliberately bodyless and rejects a
      // Content-Type header. mp-http defaults that header for every request,
      // so clear it explicitly for this GET instead of changing the shared
      // transport contract used by other applications.
      const result = assertObject(
        await request({
          url: `${API_PREFIX}/me/profile`,
          header: { "Content-Type": "" },
        }),
        "个人资料",
      );
      if (
        (result.nickname !== undefined && typeof result.nickname !== "string") ||
        (result.avatar_url !== undefined && typeof result.avatar_url !== "string") ||
        (result.avatarUrl !== undefined && typeof result.avatarUrl !== "string") ||
        (result.principal_profile_etag !== undefined &&
          typeof result.principal_profile_etag !== "string") ||
        (result.principalProfileETag !== undefined &&
          typeof result.principalProfileETag !== "string")
      ) {
        throw invalidResponse("个人资料");
      }
      return result;
    },

    async getRegistrationContact() {
      return assertRegistrationContact(
        await request({
          url: `${API_PREFIX}/me/registration-contact`,
          header: { "Content-Type": "" },
        }),
      );
    },

    async updateRegistrationContact(data) {
      return assertRegistrationContact(
        await request({
          url: `${API_PREFIX}/me/registration-contact`,
          method: "PATCH",
          data,
        }),
      );
    },

    async updateProfileExtension(payload, operationKey) {
      const key = canonicalUUID(operationKey, "个人资料操作");
      if (key[14] !== "4" || !payload || typeof payload !== "object") {
        throw createXiangwanError("invalid_profile_extension", "个人资料信息无效");
      }
      const result = assertObject(
        await request({
          url: `${API_PREFIX}/me/profile`,
          method: "PATCH",
          header: { "Idempotency-Key": key },
          data: payload,
        }),
        "个人资料",
      );
      if (
        !Number.isSafeInteger(result.candidate_version) ||
        result.candidate_version < 1 ||
        !["approved", "pending_review"].includes(result.moderation_status)
      ) {
        throw invalidResponse("个人资料");
      }
      return result;
    },

    async updateNickname(nickname, expectedETag) {
      const normalized = String(nickname || "").trim();
      if (!normalized || Array.from(normalized).length > 64) {
        throw createXiangwanError("invalid_nickname", "昵称需为 1–64 个字符");
      }
      const etag = String(expectedETag || "").trim();
      if (!etag) {
        throw createXiangwanError("profile_conflict", "资料已发生变化，请刷新后重试");
      }
      const result = assertObject(
        await request({
          url: `${API_PREFIX}/me/profile/nickname`,
          method: "PATCH",
          data: { nickname: normalized, principal_profile_etag: etag },
        }),
        "个人资料",
      );
      if (
        typeof result.nickname !== "string" ||
        (typeof result.principal_profile_etag !== "string" &&
          typeof result.principalProfileETag !== "string") ||
        !String(result.principal_profile_etag || result.principalProfileETag).trim()
      ) {
        throw invalidResponse("个人资料");
      }
      return {
        nickname: result.nickname,
        principalProfileETag: result.principal_profile_etag || result.principalProfileETag,
      };
    },

    async uploadAvatar(filePath) {
      const normalized = String(filePath || "").trim();
      if (!normalized) {
        throw createXiangwanError("invalid_avatar_file", "头像文件无效");
      }
      const result = assertObject(
        await upload({
          url: `${API_PREFIX}/me/avatar`,
          filePath: normalized,
          name: "image",
        }),
        "头像",
      );
      const avatarUrl =
        (typeof result.avatar_url === "string" && result.avatar_url.trim()) ||
        (typeof result.avatarUrl === "string" && result.avatarUrl.trim()) ||
        (typeof result.url === "string" && result.url.trim()) ||
        "";
      if (!avatarUrl) throw invalidResponse("头像");
      return result;
    },

    async setSeriesFavorite(seriesId, favorited) {
      const id = canonicalUUID(seriesId, "活动系列");
      if (typeof favorited !== "boolean") {
        throw createXiangwanError("invalid_target_state", "收藏状态无效");
      }
      const result = await request({
        url: `${API_PREFIX}/series/${id}/favorite`,
        method: favorited ? "PUT" : "DELETE",
      });
      return assertSeriesFavoriteState(result, id, favorited);
    },

    async getMyRegistrationDetail(registrationId) {
      const id = canonicalUUID(registrationId, "报名记录");
      const result = assertObject(
        await request({ url: `${API_PREFIX}/me/registrations/${id}` }),
        "报名详情",
      );
      const registration = assertObject(result.registration, "报名详情");
      assertResponseUUID(registration.registration_id, "报名详情", id);
      assertResponseUUID(registration.session_id, "报名详情");
      return result;
    },

    async issueCheckinCredential(registrationId) {
      const id = canonicalUUID(registrationId, "报名记录");
      const result = assertObject(
        await request({
          url: `${API_PREFIX}/registrations/${id}/checkin-credentials`,
          method: "POST",
          header: EMPTY_BODY_HEADER,
        }),
        "签到凭证",
      );
      assertResponseUUID(result.registration_id, "签到凭证", id);
      assertResponseUUID(result.series_id, "签到凭证");
      assertResponseUUID(result.instance_id, "签到凭证");
      assertResponseUUID(result.session_id, "签到凭证");
      assertResponseUUID(result.credential_jti, "签到凭证");
      return result;
    },

    async cancelRegistration(registrationId) {
      const id = canonicalUUID(registrationId, "报名记录");
      const result = assertObject(
        await request({
          url: `${API_PREFIX}/registrations/${id}/cancellations`,
          method: "POST",
          header: EMPTY_BODY_HEADER,
        }),
        "取消结果",
      );
      assertResponseUUID(result.registration_id, "取消结果", id);
      assertResponseUUID(result.session_id, "取消结果");
      return result;
    },
  };
}

module.exports = {
  API_PREFIX,
  UUID_PATTERN,
  canonicalUUID,
  couponsQuery,
  createXiangwanApi,
  favoritesQuery,
  homeQuery,
  invalidResponse,
  ordersQuery,
  pastActivitiesQuery,
  peopleQuery,
  registrationsQuery,
  xiangwanApi: createXiangwanApi(),
};
