"use strict";

const { formatDateTime } = require("../../utils/format");
const { joinXiangwanMediaUrl } = require("../../utils/media-url");

const SERIES_STATUS_LABELS = Object.freeze({
  draft: "暂不可查看",
  active: "进行中",
  archived: "已归档",
});

function normalizeText(value) {
  return String(value || "").trim();
}

function projectFavoriteItem(item = {}, apiBaseUrl = "") {
  const seriesStatus = normalizeText(item.series_status);
  const favoriteCount = Number(item.favorite_count);
  return {
    seriesId: normalizeText(item.series_id),
    title: normalizeText(item.title),
    titleInitial: Array.from(normalizeText(item.title))[0] || "享",
    seriesStatus,
    statusLabel: SERIES_STATUS_LABELS[seriesStatus] || "状态待确认",
    stateTone: seriesStatus === "active" ? "default" : "quiet",
    favoriteCount,
    favoriteCountText: Number.isSafeInteger(favoriteCount) ? `${favoriteCount} 人收藏` : "",
    wantCountText: Number.isSafeInteger(favoriteCount) ? `${favoriteCount} 人想去` : "",
    favoriteAvatars: (Array.isArray(item.favorite_avatars) ? item.favorite_avatars : [])
      .map((path) => joinXiangwanMediaUrl(apiBaseUrl, path, { allowAnyHttpsAbsolute: true }))
      .filter(Boolean)
      .slice(0, 3),
    favoritedAt: formatDateTime(item.favorited_at),
    sessionsAvailable: item.sessions_available === true,
  };
}

function projectFavoritesPage(page = {}, options = {}) {
  return {
    items: (Array.isArray(page.items) ? page.items : []).map((item) =>
      projectFavoriteItem(item, options.apiBaseUrl),
    ),
    nextCursor: normalizeText(page.next_cursor),
    emptyState: normalizeText(page.empty_state),
    asOf: page.as_of ? formatDateTime(page.as_of) : "",
  };
}

module.exports = { SERIES_STATUS_LABELS, projectFavoriteItem, projectFavoritesPage };
