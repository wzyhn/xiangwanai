"use strict";

const { activityTypeLabel, formatDateTime } = require("../../utils/format");
const { joinXiangwanMediaUrl } = require("../../utils/media-url");

const INSTANCE_STATUS_LABELS = Object.freeze({
  completed: "已完成",
  archived: "已归档",
});

const SESSION_STATUS_LABELS = Object.freeze({
  published: "可查看",
  ended: "已结束",
  cancelled: "已取消",
  archived: "已归档",
});

function normalizeText(value) {
  return String(value || "").trim();
}

function nonnegativeCount(value) {
  return Number.isSafeInteger(value) && value >= 0 ? value : null;
}

function joinPublicMediaUrl(apiBaseUrl, mediaPath) {
  const base = normalizeText(apiBaseUrl).replace(/\/+$/, "");
  const path = normalizeText(mediaPath);
  if (!/^https?:\/\/[^\s?#]+(?::\d{1,5})?(?:\/[^\s?#]*)?$/.test(base)) return "";
  if (!/^\/api\/v1\/xiangwan\/media\/[0-9a-f/-]+$/.test(path)) return "";
  return `${base}${path}`;
}

function projectPastActivity(item = {}, apiBaseUrl = "") {
  const successfulCount = nonnegativeCount(item.successful_published_instance_count);
  const registrationCount = nonnegativeCount(item.historical_registration_count);
  return {
    seriesId: normalizeText(item.series_id),
    seriesTitle: normalizeText(item.series_title),
    instanceId: normalizeText(item.instance_id),
    instanceTitle: normalizeText(item.instance_title),
    activityType: activityTypeLabel(item.activity_type),
    coverImageUrl: joinXiangwanMediaUrl(apiBaseUrl, item.cover_image_url),
    instanceStatus: normalizeText(item.instance_status),
    statusLabel: INSTANCE_STATUS_LABELS[normalizeText(item.instance_status)] || "往期活动",
    completedAt: formatDateTime(item.completed_at),
    historyText:
      successfulCount !== null && registrationCount !== null
        ? `已举办 ${successfulCount} 期 · 累计 ${registrationCount} 人参加`
        : "",
  };
}

function projectPastActivitiesPage(page = {}, options = {}) {
  const items = (Array.isArray(page.items) ? page.items : []).map((item) =>
    projectPastActivity(item, options.apiBaseUrl),
  );
  return {
    activeActivityType: normalizeText(page.active_activity_type) || "all",
    items,
    seriesGroups: groupPastActivities(items),
    nextCursor: normalizeText(page.next_cursor),
    emptyState: normalizeText(page.empty_state),
  };
}

// The public endpoint intentionally returns one card per completed/archived
// Instance. Keep that flat identity for deep links, and expose a grouped view
// so a recurring Series visibly includes every returned period.
function groupPastActivities(items) {
  const groups = [];
  const bySeries = new Map();
  for (const item of Array.isArray(items) ? items : []) {
    const seriesId = normalizeText(item && item.seriesId) || "__unknown__";
    let group = bySeries.get(seriesId);
    if (!group) {
      group = {
        seriesId,
        seriesTitle: normalizeText(item && item.seriesTitle) || "活动系列",
        items: [],
      };
      bySeries.set(seriesId, group);
      groups.push(group);
    }
    group.items.push(item);
  }
  return groups;
}

function blockUnavailableText(block) {
  if (normalizeText(block.availability) === "policy_blocked") {
    return "该资源暂不可访问";
  }
  return normalizeText(block.type) === "link" ? "暂未配置链接" : "资源暂未配置";
}

function projectReviewBlock(block = {}, apiBaseUrl = "") {
  const type = normalizeText(block.type);
  const availability = normalizeText(block.availability);
  const available = availability === "available";
  return {
    blockId: normalizeText(block.block_id),
    type,
    text: normalizeText(block.text),
    label: normalizeText(block.label) || (type === "link" ? "打开链接" : "查看资源"),
    subtitle: normalizeText(block.subtitle),
    externalUrl:
      available && (type === "link" || type === "image") ? normalizeText(block.external_url) : "",
    mediaUrl: available
      ? normalizeText(block.external_url) || joinPublicMediaUrl(apiBaseUrl, block.media_path)
      : "",
    mime: normalizeText(block.mime),
    available,
    unavailableText: available ? "" : blockUnavailableText(block),
    isText: type === "text",
    isImage: type === "image",
    isCover: type === "image" && block.is_cover === true,
    isVideo: type === "video",
    isAudio: type === "audio",
    isExternalLink: type === "link",
    isDownload: type === "file",
  };
}

function projectReviewDocument(document = {}, apiBaseUrl = "") {
  const scope = normalizeText(document.scope);
  const title = normalizeText(document.title);
  const titleForKind = title.toLowerCase();
  const titledKind =
    titleForKind.includes("照片") || titleForKind.includes("图片")
      ? "photos"
      : titleForKind.includes("视频")
        ? "video"
        : titleForKind.includes("复盘")
          ? "recap"
          : titleForKind.includes("录音")
            ? "audio"
            : titleForKind.includes("资料")
              ? "resources"
              : "intro";
  const blocks = (Array.isArray(document.blocks) ? document.blocks : []).map((block) =>
    projectReviewBlock(block, apiBaseUrl),
  );
  // Content authors may use a generic title such as “活动回顾”. Keep the
  // public renderer useful in that case by inferring media-only sections from
  // the already projected, server-approved block types. Explicit title cues
  // always win, so a deliberately named “复盘” section keeps its layout.
  let kind = titledKind;
  const blockTypes = new Set(blocks.map((block) => block.type));
  const hasImage = blockTypes.has("image");
  const hasOtherMedia =
    blockTypes.has("link") ||
    blockTypes.has("file") ||
    blockTypes.has("video") ||
    blockTypes.has("audio");
  if (hasImage && hasOtherMedia) {
    // A single admin document may contain the video-channel link together
    // with the photo set. Keep it in the generic renderer so neither class
    // of approved block is silently hidden by a title cue.
    kind = "mixed";
  } else if (hasImage && !blockTypes.has("video")) {
    kind = "photos";
  } else if (kind === "intro") {
    if (blockTypes.has("video")) kind = "video";
    else if (blockTypes.has("audio")) kind = "audio";
    else if (blockTypes.has("link") || blockTypes.has("file")) kind = "resources";
  }
  return {
    relationId: normalizeText(document.relation_id),
    sessionId: normalizeText(document.session_id),
    title: title || (scope === "session_resources" ? "本场资料" : "活动回顾"),
    kind,
    scope,
    blocks,
  };
}

function projectReviewContentBlock(block = {}, apiBaseUrl = "") {
  const type = normalizeText(block.type);
  if (type === "text") {
    const title = normalizeText(block.title);
    const body = normalizeText(block.body);
    if (!title && !body) return null;
    return { type: "text", title, body, isText: true, isImage: false };
  }
  if (type === "image") {
    const imageUrl = joinXiangwanMediaUrl(apiBaseUrl, block.url);
    if (!imageUrl) return null;
    return {
      type: "image",
      imageUrl,
      caption: normalizeText(block.caption),
      isText: false,
      isImage: true,
    };
  }
  return null;
}

function projectNextInstance(next = {}) {
  const action = normalizeText(next.action);
  return {
    action,
    instanceId: normalizeText(next.instance_id),
    sessionId: normalizeText(next.session_id),
    candidateSessionIds: Array.isArray(next.candidate_session_ids)
      ? next.candidate_session_ids.map(normalizeText).filter(Boolean)
      : [],
    available: action === "session_detail" || action === "session_selection_required",
    label: action === "session_selection_required" ? "选择下一期场次" : "查看下一期",
  };
}

function projectActivityReview(review = {}, options = {}) {
  const successfulCount = nonnegativeCount(review.successful_published_instance_count);
  const registrationCount = nonnegativeCount(review.historical_registration_count);
  const documents = (Array.isArray(review.documents) ? review.documents : []).map((document) =>
    projectReviewDocument(document, options.apiBaseUrl),
  );
  const contentBlocks = (Array.isArray(review.content_blocks) ? review.content_blocks : [])
    .map((block) => projectReviewContentBlock(block, options.apiBaseUrl))
    .filter(Boolean);
  const photos = documents
    .flatMap((document) => document.blocks)
    .filter((block) => block.isImage && block.mediaUrl);
  const heroImageUrl = (photos.find((block) => block.isCover) || photos[0])?.mediaUrl || "";
  return {
    seriesId: normalizeText(review.series_id),
    seriesTitle: normalizeText(review.series_title),
    instanceId: normalizeText(review.instance_id),
    instanceTitle: normalizeText(review.instance_title),
    activityType: activityTypeLabel(review.activity_type),
    completedAt: formatDateTime(review.completed_at),
    historyText:
      successfulCount !== null && registrationCount !== null
        ? `已举办 ${successfulCount} 期 · 累计 ${registrationCount} 人参加`
        : "",
    heroImageUrl,
    contentBlocks,
    documents,
    nextInstance: projectNextInstance(review.next_instance),
  };
}

function projectSessionCollection(page = {}) {
  return {
    action: normalizeText(page.action),
    seriesId: normalizeText(page.series_id),
    instanceId: normalizeText(page.instance_id),
    directSessionId: normalizeText(page.direct_session_id),
    sessions: (Array.isArray(page.sessions) ? page.sessions : []).map((session, index) => ({
      sessionId: normalizeText(session.session_id),
      label: normalizeText(session.session_title) || `场次 ${index + 1}`,
      startAt: formatDateTime(session.session_start_at),
      status: normalizeText(session.status),
      statusLabel: SESSION_STATUS_LABELS[normalizeText(session.status)] || "状态待确认",
      reviewPath: normalizeText(session.review_path),
    })),
  };
}

module.exports = {
  joinPublicMediaUrl,
  projectActivityReview,
  projectPastActivitiesPage,
  projectPastActivity,
  groupPastActivities,
  projectReviewBlock,
  projectReviewContentBlock,
  projectSessionCollection,
};
