"use strict";

const {
  activityTypeLabel,
  areaLabel,
  formatDateTime,
  formatEventDate,
  formatEventTimeRange,
  formatMoney,
} = require("../../utils/format");
const { joinXiangwanMediaUrl } = require("../../utils/media-url");

const DISPLAY_LABELS = Object.freeze({
  cancelled: "活动已取消",
  recurring_gap: "下一期筹备中",
  ended: "活动已结束",
  in_progress: "活动进行中",
  not_open: "报名未开始",
  closed: "报名已结束",
  full: "名额已满",
  temporarily_locked_full: "名额暂满",
  open_low_stock: "余位不多",
  open_need_group: "等待成团",
  open: "报名中",
});

const CTA_LABELS = Object.freeze({
  view_details: "查看详情",
  activity_ended: "活动已结束",
  activity_cancelled: "活动已取消",
  register_now: "填写报名",
  coming_soon: "敬请期待",
  activity_in_progress: "活动进行中",
  registration_not_open: "报名未开始",
  registration_closed: "报名已结束",
  try_again_later: "稍后再试",
  brand_suspended: "活动暂停",
});

const OPEN_DISPLAY_STATES = new Set(["open", "open_low_stock", "open_need_group"]);

const DISPLAY_STATE_CTA_CODES = Object.freeze({
  cancelled: "activity_cancelled",
  recurring_gap: "coming_soon",
  ended: "activity_ended",
  in_progress: "activity_in_progress",
  not_open: "registration_not_open",
  closed: "registration_closed",
  full: "registration_closed",
  temporarily_locked_full: "try_again_later",
});

function normalizeText(value) {
  return String(value || "").trim();
}

function displayStateLabel(value) {
  return DISPLAY_LABELS[normalizeText(value)] || "状态待确认";
}

function ctaLabel(value, fallback = "查看详情") {
  return CTA_LABELS[normalizeText(value)] || fallback;
}

function timestamp(value) {
  const parsed = Date.parse(String(value || "").trim());
  return Number.isFinite(parsed) ? parsed : null;
}

// This is a stale-page guard, not a replacement for the server decision. The
// server remains authoritative; trusted response timestamps let a retained
// page fail closed if it has crossed a known boundary before its refresh runs.
function effectiveSessionDisplayState(detail = {}, now = Date.now()) {
  const display = detail.display || {};
  const state = normalizeText(display.state);
  if (!OPEN_DISPLAY_STATES.has(state)) return state;
  const nowMs = now instanceof Date ? now.getTime() : Number(now);
  if (!Number.isFinite(nowMs)) return state;
  const sessionEnd = timestamp(detail.session_end_at);
  const sessionStart = timestamp(detail.session_start_at);
  const registrationEnd = timestamp(detail.registration_end_at);
  const registrationStart = timestamp(detail.registration_start_at);
  if (sessionEnd !== null && nowMs >= sessionEnd) return "ended";
  if (sessionStart !== null && nowMs >= sessionStart) return "in_progress";
  if (registrationEnd !== null && nowMs >= registrationEnd) return "closed";
  if (registrationStart !== null && nowMs < registrationStart) return "not_open";
  return state;
}

function stateTone(state) {
  return state === "open" || state === "open_need_group"
    ? "warm"
    : state === "open_low_stock"
      ? "alert"
      : "quiet";
}

function locationText(card) {
  if (normalizeText(card.delivery_mode) === "online") {
    return normalizeText(card.online_participation_mode) || "线上参与";
  }
  return normalizeText(card.venue_name) || areaLabel(card.area);
}

function coordinate(value, minimum, maximum) {
  return typeof value === "number" && Number.isFinite(value) && value >= minimum && value <= maximum
    ? value
    : null;
}

function nonnegativeCount(value) {
  return Number.isSafeInteger(value) && value >= 0 ? value : null;
}

function capacityText(display = {}) {
  const state = normalizeText(display.state);
  const sellableCapacity = nonnegativeCount(display.sellable_capacity);
  if (OPEN_DISPLAY_STATES.has(state)) {
    return sellableCapacity === null ? "名额以提交时为准" : `剩余 ${sellableCapacity} 个名额`;
  }

  const capacity = nonnegativeCount(display.capacity);
  const confirmedCount = nonnegativeCount(display.confirmed_registration_count);
  return capacity !== null && confirmedCount !== null && confirmedCount <= capacity
    ? `已确认 ${confirmedCount} / ${capacity} 人`
    : "名额状态以活动方为准";
}

function projectHomeCard(card = {}, tagLabels = {}, apiBaseUrl = "", now = null) {
  const display = card.display || {};
  const codes = Array.isArray(card.quick_tag_codes) ? card.quick_tag_codes : [];
  const heatCount = nonnegativeCount(card.heat_count);
  const avatars = Array.isArray(card.participant_avatars)
    ? card.participant_avatars
    : Array.isArray(card.participantAvatars)
      ? card.participantAvatars
      : [];
  const favoriteAvatars = Array.isArray(card.favorite_avatars)
    ? card.favorite_avatars
    : Array.isArray(card.favoriteAvatars)
      ? card.favoriteAvatars
      : [];
  const favoriteCount = nonnegativeCount(card.current_favorite_users);
  const effectiveState = effectiveSessionDisplayState(card, now);
  const staleStateCta = DISPLAY_STATE_CTA_CODES[effectiveState];
  return {
    sessionId: normalizeText(card.session_id),
    instanceTitle: normalizeText(card.instance_title),
    // The card represents one Session, but its primary title is the owning
    // Instance. Keep the exact Session title as a secondary line so two
    // sessions in one period remain distinguishable without replacing the
    // period title that the product contract calls for.
    title: normalizeText(card.instance_title) || normalizeText(card.session_title),
    sessionTitle: normalizeText(card.session_title),
    activityType: activityTypeLabel(card.activity_type),
    tags: codes.map((code) => tagLabels[code] || code).filter(Boolean),
    startAt: formatDateTime(card.session_start_at),
    endAt: formatDateTime(card.session_end_at),
    scheduleDateText: formatEventDate(card.session_start_at, card.session_end_at),
    scheduleTimeText: formatEventTimeRange(card.session_start_at, card.session_end_at),
    location: locationText(card),
    price: formatMoney(card.price_cents),
    coverImageUrl: joinXiangwanMediaUrl(apiBaseUrl, card.cover_image_url),
    // 头像放行任意 https 绝对地址(微信 CDN 旧头像),与 profile 投影同策略。
    participantAvatars: avatars
      .map((path) => joinXiangwanMediaUrl(apiBaseUrl, path, { allowAnyHttpsAbsolute: true }))
      .filter(Boolean)
      .slice(0, 3),
    favoriteAvatars: favoriteAvatars
      .map((path) => joinXiangwanMediaUrl(apiBaseUrl, path, { allowAnyHttpsAbsolute: true }))
      .filter(Boolean)
      .slice(0, 3),
    currentFavoriteUsers: favoriteCount,
    participantCount: nonnegativeCount(display.confirmed_registration_count) || 0,
    participantCountText:
      nonnegativeCount(display.confirmed_registration_count) === null
        ? "已报名"
        : `${display.confirmed_registration_count}人报名`,
    state: effectiveState,
    stateLabel: displayStateLabel(effectiveState),
    stateTone: stateTone(effectiveState),
    remainingText: capacityText({ ...display, state: effectiveState }),
    heatCount,
    heatText: heatCount === null ? "" : `${heatCount} 人想去`,
    wantText: favoriteCount === null ? "" : `${favoriteCount} 人想去`,
    ctaLabel: staleStateCta ? ctaLabel(staleStateCta, "暂不可报名") : ctaLabel(card.cta_label),
  };
}

function projectHomePage(page = {}, options = {}) {
  const quickTags = Array.isArray(page.available_quick_tags) ? page.available_quick_tags : [];
  const tagLabels = {};
  quickTags.forEach((tag) => {
    const code = normalizeText(tag && tag.code);
    if (code) tagLabels[code] = normalizeText(tag.label) || code;
  });
  return {
    communityName: normalizeText(page.community_name),
    brandIntro: normalizeText(page.brand_intro),
    heroMode: page.hero_mode === "image" ? "image" : "text",
    heroEyebrow: normalizeText(page.hero_eyebrow),
    heroSubtitle: normalizeText(page.hero_subtitle),
    heroImageUrl: normalizeText(page.hero_image_url),
    heroImageAlt: normalizeText(page.hero_image_alt),
    openCount: nonnegativeCount(page.open_registration_session_count),
    quickTags: quickTags.map((tag) => ({
      code: normalizeText(tag.code),
      label: normalizeText(tag.label),
    })),
    cards: (Array.isArray(page.cards) ? page.cards : []).map((card) =>
      projectHomeCard(card, tagLabels, options.apiBaseUrl, options.now),
    ),
    nextCursor: normalizeText(page.next_cursor),
    emptyState: normalizeText(page.empty_state),
  };
}

function projectContentBlock(block, apiBaseUrl) {
  if (!block || typeof block !== "object") return null;
  const type = normalizeText(block.type);
  if (type === "text") {
    const title = normalizeText(block.title);
    const body = normalizeText(block.body);
    if (!title && !body) return null;
    return { type: "text", title, body };
  }
  if (type === "image") {
    const imageUrl = joinXiangwanMediaUrl(apiBaseUrl, block.url);
    if (!imageUrl) return null;
    return { type: "image", imageUrl, caption: normalizeText(block.caption) };
  }
  return null;
}

function projectLeader(leader, apiBaseUrl) {
  if (!leader || typeof leader !== "object") return null;
  const displayName = normalizeText(leader.display_name);
  if (!displayName) return null;
  return {
    displayName,
    roleLabel: normalizeText(leader.role_label),
    headline: normalizeText(leader.headline),
    avatarUrl: joinXiangwanMediaUrl(apiBaseUrl, leader.avatar_url, { allowAnyHttpsAbsolute: true }),
    avatarText: Array.from(displayName)[0] || "享",
  };
}

function projectPreviousReview(review, apiBaseUrl) {
  if (!review || typeof review !== "object") return null;
  const instanceId = normalizeText(review.instance_id);
  if (!instanceId) return null;
  const imageUrls = (Array.isArray(review.image_urls) ? review.image_urls : [])
    .map((path) =>
      /^https:\/\/[^\s]+$/.test(normalizeText(path)) &&
      !normalizeText(path).includes("/api/v1/xiangwan/")
        ? normalizeText(path)
        : joinXiangwanMediaUrl(apiBaseUrl, path),
    )
    .filter(Boolean)
    .slice(0, 3);
  return {
    instanceId,
    ...(normalizeText(review.session_id) ? { sessionId: normalizeText(review.session_id) } : {}),
    title: normalizeText(review.title),
    completedAt: formatDateTime(review.completed_at),
    imageUrls,
  };
}

function projectDetailQuickTags(detail = {}) {
  const codes = Array.isArray(detail.quick_tag_codes) ? detail.quick_tag_codes : [];
  const labels = new Map();
  const configured = Array.isArray(detail.quick_tags) ? detail.quick_tags : [];
  configured.forEach((tag) => {
    const code = normalizeText(tag && tag.code);
    const label = normalizeText(tag && tag.label);
    if (code && label) labels.set(code, label);
  });
  return codes
    .map((code) => {
      const normalizedCode = normalizeText(code);
      if (!normalizedCode) return "";
      const value = labels.get(normalizedCode) || normalizedCode;
      return value.startsWith("#") ? value : `#${value}`;
    })
    .filter(Boolean);
}

function projectSessionDetail(detail = {}, options = {}) {
  const delivery = detail.delivery || {};
  const display = detail.display || {};
  const cta = detail.cta || {};
  const effectiveState = effectiveSessionDisplayState(detail, options.now);
  const suspended = detail.brand_status === "suspended" || cta.label === "brand_suspended";
  const priceCents = Number(detail.price_cents);
  const validPrice = Number.isSafeInteger(priceCents) && priceCents >= 0;
  const startsRegistration =
    cta.enabled === true &&
    cta.action === "start_registration" &&
    cta.label === "register_now" &&
    display.registration_allowed !== false &&
    !suspended &&
    OPEN_DISPLAY_STATES.has(effectiveState);
  const canonicalCtaCode = DISPLAY_STATE_CTA_CODES[effectiveState];
  const paidRegistrationAvailable = options.wechatPaymentAvailable === true;
  const registrationReady =
    startsRegistration && validPrice && (priceCents === 0 || paidRegistrationAvailable);
  const paidRegistrationPending =
    startsRegistration && validPrice && priceCents > 0 && !paidRegistrationAvailable;
  const projectedCtaLabel = suspended
    ? "活动暂停"
    : canonicalCtaCode
      ? ctaLabel(canonicalCtaCode, "暂不可报名")
      : paidRegistrationPending
        ? "付费报名暂未开放"
        : registrationReady
          ? priceCents > 0
            ? "报名并支付"
            : "立即报名"
          : "暂不可报名";
  const unavailableMessages = {
    cancelled: "活动已取消，无法报名。",
    recurring_gap: "本期已结束，下一期正在筹备中。",
    ended: "活动已结束，报名已关闭。",
    in_progress: "报名已截止，活动正在进行中。",
    not_open: "报名尚未开始，请留意报名开放时间。",
    closed: "报名已截止，活动尚未开始。",
    full: "本场名额已满，暂时无法报名。",
    temporarily_locked_full: "当前名额暂被占用，请稍后刷新查看。",
  };
  const longitude = coordinate(delivery.longitude, -180, 180);
  const latitude = coordinate(delivery.latitude, -90, 90);
  const successfulPublishedInstanceCount = nonnegativeCount(
    detail.successful_published_instance_count,
  );
  const historicalRegistrationCount = nonnegativeCount(detail.historical_registration_count);
  const capacity = nonnegativeCount(display.capacity);
  const confirmedRegistrationCount = nonnegativeCount(display.confirmed_registration_count);
  const neededToReachGroupMinimum = nonnegativeCount(display.needed_to_reach_group_minimum);
  const registrationCountText =
    capacity !== null && confirmedRegistrationCount !== null
      ? `已报名 ${confirmedRegistrationCount} 人 / 限额 ${capacity} 人`
      : capacityText({ ...display, state: effectiveState });
  return {
    seriesId: normalizeText(detail.series_id),
    sessionId: normalizeText(detail.session_id),
    instanceTitle: normalizeText(detail.instance_title),
    // Detail identity is the Instance; keep the exact Session title as a
    // secondary line so a multi-session Instance does not look like a new
    // activity on every Session route.
    title: normalizeText(detail.instance_title) || normalizeText(detail.session_title),
    sessionTitle: normalizeText(detail.session_title),
    activityType: activityTypeLabel(detail.activity_type),
    activityTypeCode: normalizeText(detail.activity_type),
    tagItems: [
      normalizeText(delivery.mode) === "online" ? "#线上" : "#线下",
      activityTypeLabel(detail.activity_type)
        ? `#${activityTypeLabel(detail.activity_type)
            .replace(/\s+/g, "")
            .replace("AI圆桌", "AI圆桌派")
            .replace("特别活动", "专题活动")}`
        : "",
      ...projectDetailQuickTags(detail),
    ].filter(Boolean),
    coverImageUrl: joinXiangwanMediaUrl(options.apiBaseUrl, detail.cover_image_url),
    scheduleDateText: formatEventDate(detail.session_start_at, detail.session_end_at),
    scheduleTimeText: formatEventTimeRange(detail.session_start_at, detail.session_end_at),
    startAt: formatDateTime(detail.session_start_at),
    endAt: formatDateTime(detail.session_end_at),
    registrationStartAt: normalizeText(detail.registration_start_at),
    registrationEndAt: normalizeText(detail.registration_end_at),
    sessionStartAt: normalizeText(detail.session_start_at),
    sessionEndAt: normalizeText(detail.session_end_at),
    registrationWindow: `${formatDateTime(detail.registration_start_at)} 至 ${formatDateTime(
      detail.registration_end_at,
    )}`,
    price: formatMoney(priceCents),
    priceCents,
    area: areaLabel(delivery.area),
    venue: normalizeText(delivery.venue_name),
    address: normalizeText(delivery.address),
    onlineMode: normalizeText(delivery.online_participation_mode),
    deliveryMode: normalizeText(delivery.mode),
    longitude,
    latitude,
    canOpenLocation:
      normalizeText(delivery.mode) === "offline" && longitude !== null && latitude !== null,
    successfulPublishedInstanceCount,
    historicalRegistrationCount,
    historyText:
      successfulPublishedInstanceCount !== null && historicalRegistrationCount !== null
        ? `已举办 ${successfulPublishedInstanceCount} 期 · 累计 ${historicalRegistrationCount} 人参加`
        : "",
    state: effectiveState,
    stateLabel: suspended ? "活动暂停" : displayStateLabel(effectiveState),
    stateTone: suspended ? "quiet" : stateTone(effectiveState),
    remainingText: capacityText({ ...display, state: effectiveState }),
    capacity,
    confirmedRegistrationCount,
    neededToReachGroupMinimum,
    groupMinimumText:
      neededToReachGroupMinimum !== null && neededToReachGroupMinimum > 0
        ? `成团还需 ${neededToReachGroupMinimum} 人`
        : "",
    registrationCountText,
    ctaLabel: projectedCtaLabel,
    ctaEnabled: registrationReady,
    registrationReady,
    paidRegistrationPending,
    registrationNotice: suspended
      ? "活动已暂停，暂不接受新报名。"
      : paidRegistrationPending
        ? "付费报名暂未开放，请留意后续通知。"
        : unavailableMessages[effectiveState] ||
          (registrationReady ? "" : "当前暂不可报名，请刷新查看最新状态。"),
    contentBlocks: (Array.isArray(detail.content_blocks) ? detail.content_blocks : [])
      .map((block) => projectContentBlock(block, options.apiBaseUrl))
      .filter(Boolean),
    leaders: (Array.isArray(detail.leaders) ? detail.leaders : [])
      .map((leader) => projectLeader(leader, options.apiBaseUrl))
      .filter(Boolean),
    previousReview: projectPreviousReview(detail.previous_review, options.apiBaseUrl),
  };
}

module.exports = {
  CTA_LABELS,
  DISPLAY_LABELS,
  capacityText,
  ctaLabel,
  coordinate,
  displayStateLabel,
  effectiveSessionDisplayState,
  projectContentBlock,
  projectDetailQuickTags,
  projectHomeCard,
  projectHomePage,
  projectLeader,
  projectPreviousReview,
  projectSessionDetail,
};
