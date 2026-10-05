"use strict";

const { projectSessionDetail } = require("../../features/catalog/model");
const { canonicalUUID, xiangwanApi } = require("../../services/xiangwan-api");
const { getUserMessage } = require("../../utils/errors");

function resolveApp() {
  if (typeof getApp !== "function") return null;
  try {
    return getApp();
  } catch (_error) {
    return null;
  }
}

function currentApiBaseUrl() {
  const app = resolveApp();
  return String((app && app.globalData && app.globalData.apiBaseUrl) || "").trim();
}

function createSessionDetailPageDefinition(api = xiangwanApi) {
  return {
    data: {
      loading: true,
      errorMessage: "",
      detail: null,
      detailCoverFailed: false,
      statusRefreshing: false,
      statusErrorMessage: "",
      registrationStarting: false,
      favoriteSaving: false,
      favoriteSaved: false,
      favoriteMessage: "",
      favoriteError: "",
      alreadyRegistered: false,
      currentRegistrationId: "",
      currentRegistrationState: "",
    },

    onLoad(options = {}) {
      this._active = true;
      try {
        this._sessionId = canonicalUUID(options.session_id, "活动场次");
      } catch (error) {
        this.setData({ loading: false, errorMessage: getUserMessage(error, "活动链接无效") });
        return;
      }
      this._skipNextShowRefresh = true;
      void this.loadDetail();
    },

    onShow() {
      this._active = true;
      if (this._skipNextShowRefresh) {
        this._skipNextShowRefresh = false;
        return;
      }
      if (this._sessionId) void this.loadDetail();
    },

    onHide() {
      this._active = false;
      this._requestVersion = Number(this._requestVersion || 0) + 1;
      this._favoriteVersion = Number(this._favoriteVersion || 0) + 1;
      this._favoriteMutation = false;
      this.clearStatusRefreshTimer();
      this.setData({ favoriteSaving: false, registrationStarting: false });
    },

    onUnload() {
      this._active = false;
      this._requestVersion = Number(this._requestVersion || 0) + 1;
      this._favoriteVersion = Number(this._favoriteVersion || 0) + 1;
      this._favoriteMutation = false;
      this.clearStatusRefreshTimer();
    },

    onPullDownRefresh() {
      if (!this._sessionId) {
        if (typeof wx !== "undefined" && typeof wx.stopPullDownRefresh === "function") {
          wx.stopPullDownRefresh();
        }
        return Promise.resolve();
      }
      return this.loadDetail();
    },

    async loadDetail({ background = false } = {}) {
      const version = Number(this._requestVersion || 0) + 1;
      this._requestVersion = version;
      this.clearStatusRefreshTimer();
      this.setData({
        loading: !background,
        statusRefreshing: background,
        statusErrorMessage: "",
        errorMessage: "",
      });
      try {
        const [rawDetail, policies] = await Promise.all([
          api.getSessionDetail(this._sessionId),
          typeof api.getPublicPolicies === "function"
            ? api.getPublicPolicies().catch(() => null)
            : Promise.resolve(null),
        ]);
        let detail = projectSessionDetail(rawDetail, {
          apiBaseUrl: currentApiBaseUrl(),
          now: Date.now(),
          wechatPaymentAvailable: Boolean(
            policies &&
              policies.capabilities &&
              policies.capabilities.wechat_payment_available === true,
          ),
        });
        if (!this._active || version !== this._requestVersion) return;
        let existing = null;
        try {
          existing = await this.findCurrentRegistration(detail.sessionId, version);
        } catch (_error) {
          detail = {
            ...detail,
            ctaEnabled: false,
            registrationReady: false,
            ctaLabel: "报名状态待确认",
            registrationNotice: "暂时无法确认你的报名状态，请刷新后再试。",
          };
        }
        if (!this._active || version !== this._requestVersion) return;
        if (existing) {
          detail = {
            ...detail,
            ctaEnabled: false,
            registrationReady: false,
            ctaLabel: "已报名",
            registrationNotice: "你已报名本场活动，可在报名详情查看签到和状态。",
          };
        }
        this.setData({
          detail,
          detailCoverFailed: false,
          alreadyRegistered: Boolean(existing),
          currentRegistrationId: existing ? existing.registration_id : "",
          currentRegistrationState: existing ? String(existing.state || "") : "",
        });
        return detail;
      } catch (error) {
        if (this._active && version === this._requestVersion) {
          this.setData(
            background
              ? { statusErrorMessage: "活动状态更新失败，请刷新后再报名" }
              : { errorMessage: getUserMessage(error, "活动详情加载失败") },
          );
        }
      } finally {
        if (this._active && version === this._requestVersion) {
          this.setData({ loading: false, statusRefreshing: false });
          this.scheduleStatusRefresh(this.data.detail);
        }
        if (typeof wx !== "undefined" && typeof wx.stopPullDownRefresh === "function") {
          wx.stopPullDownRefresh();
        }
      }
    },

    clearStatusRefreshTimer() {
      if (this._statusRefreshTimer) {
        clearTimeout(this._statusRefreshTimer);
        this._statusRefreshTimer = null;
      }
    },

    scheduleStatusRefresh(detail) {
      this.clearStatusRefreshTimer();
      if (!detail || ["ended", "cancelled", "recurring_gap"].includes(detail.state)) return;
      const now = Date.now();
      const boundaries = [
        detail.registrationStartAt,
        detail.registrationEndAt,
        detail.sessionStartAt,
        detail.sessionEndAt,
      ]
        .map((value) => Date.parse(String(value || "")))
        .filter((value) => Number.isFinite(value) && value > now);
      if (!boundaries.length) return;
      // Device time is only a refresh hint. Bounded polling also works when
      // the device clock is wrong; only fresh server facts change the state.
      const delay = Math.max(1000, Math.min(60000, Math.min(...boundaries) - now + 250));
      this._statusRefreshTimer = setTimeout(() => {
        this._statusRefreshTimer = null;
        if (this._active) void this.loadDetail({ background: true });
      }, delay);
      // Node-based unit tests should not stay alive for a future activity
      // boundary; WeChat timers do not expose unref and keep normal behavior.
      if (typeof this._statusRefreshTimer.unref === "function") {
        this._statusRefreshTimer.unref();
      }
    },

    retry() {
      void this.loadDetail();
    },

    async findCurrentRegistration(sessionId, version) {
      const app = resolveApp();
      if (
        !api ||
        typeof api.getMyRegistrations !== "function" ||
        !app ||
        !app.weconqAuth ||
        typeof app.weconqAuth.hasValidSession !== "function"
      ) {
        return null;
      }
      let authenticated = false;
      try {
        authenticated = Boolean(app.weconqAuth.hasValidSession());
      } catch (_error) {
        return null;
      }
      if (!authenticated) return null;
      let cursor = "";
      const seenCursors = new Set();
      for (let pageNumber = 0; pageNumber < 20; pageNumber += 1) {
        const result = await api.getMyRegistrations({ state: "all", limit: 100, cursor });
        if (!this._active || version !== this._requestVersion) return null;
        const items = Array.isArray(result && result.items) ? result.items : [];
        const existing = items.find(
          (item) =>
            item &&
            String(item.session_id || "") === String(sessionId || "") &&
            ["registered", "pending_payment", "refund_processing"].includes(
              String(item.state || ""),
            ),
        );
        if (existing) return existing;
        cursor = String((result && result.next_cursor) || "");
        if (cursor && seenCursors.has(cursor)) throw new Error("重复的报名分页游标");
        seenCursors.add(cursor);
        if (!cursor) return null;
      }
      throw new Error("报名状态分页不完整");
    },

    onDetailCoverError() {
      const detail = this.data.detail;
      if (!detail || !detail.coverImageUrl) return;
      this.setData({ detail: { ...detail, coverImageUrl: "" }, detailCoverFailed: true });
    },

    onLeaderAvatarError(event) {
      const detail = this.data.detail;
      const index = Number(event && event.currentTarget && event.currentTarget.dataset.index);
      if (!detail || !Array.isArray(detail.leaders)) return;
      const leader = detail.leaders[index];
      if (!Number.isSafeInteger(index) || !leader || !leader.avatarUrl) return;
      const leaders = detail.leaders.slice();
      leaders[index] = { ...leader, avatarUrl: "" };
      this.setData({ detail: { ...detail, leaders } });
    },

    onContentBlockImageError(event) {
      const detail = this.data.detail;
      const index = Number(event && event.currentTarget && event.currentTarget.dataset.blockIndex);
      if (!detail || !Array.isArray(detail.contentBlocks) || !Number.isSafeInteger(index)) return;
      const block = detail.contentBlocks[index];
      if (!block || block.type !== "image" || !block.imageUrl) return;
      const contentBlocks = detail.contentBlocks.slice();
      contentBlocks[index] = { ...block, imageUrl: "" };
      this.setData({ detail: { ...detail, contentBlocks } });
    },

    onPreviousReviewImageError(event) {
      const detail = this.data.detail;
      const index = Number(event && event.currentTarget && event.currentTarget.dataset.reviewIndex);
      const review = detail && detail.previousReview;
      if (!detail || !review || !Array.isArray(review.imageUrls) || !Number.isSafeInteger(index))
        return;
      if (!review.imageUrls[index]) return;
      const imageUrls = review.imageUrls.filter((_url, position) => position !== index);
      this.setData({ detail: { ...detail, previousReview: { ...review, imageUrls } } });
    },

    openPreviousReview() {
      const review = this.data.detail && this.data.detail.previousReview;
      if (!review || !review.instanceId || typeof wx === "undefined" || !wx.navigateTo) return;
      wx.navigateTo({
        url: `/pages/activity-review/index?instance_id=${encodeURIComponent(review.instanceId)}${review.sessionId ? `&session_id=${encodeURIComponent(review.sessionId)}` : ""}`,
      });
    },

    openLocation() {
      const detail = this.data.detail;
      if (
        !detail ||
        !detail.canOpenLocation ||
        typeof wx === "undefined" ||
        typeof wx.openLocation !== "function"
      ) {
        return;
      }
      wx.openLocation({
        latitude: detail.latitude,
        longitude: detail.longitude,
        name: detail.venue || detail.title,
        address: detail.address,
      });
    },

    async startRegistration() {
      if (
        !this._active ||
        this.data.loading ||
        this.data.statusRefreshing ||
        this.data.registrationStarting ||
        !this.data.detail ||
        !this.data.detail.registrationReady ||
        this.data.alreadyRegistered
      )
        return;
      this.setData({ registrationStarting: true });
      const pending = this.loadDetail({ background: true });
      const version = this._requestVersion;
      try {
        const detail = await pending;
        if (!detail || !this._active || version !== this._requestVersion) return;
        if (!detail.registrationReady) return;
        if (typeof wx !== "undefined" && typeof wx.navigateTo === "function") {
          wx.navigateTo({
            url: `/pages/registration/index?session_id=${encodeURIComponent(detail.sessionId)}`,
          });
        }
      } finally {
        if (this._active && version === this._requestVersion) {
          this.setData({ registrationStarting: false });
        }
      }
    },

    openExistingRegistration() {
      const id = String(this.data.currentRegistrationId || "").trim();
      if (!id || typeof wx === "undefined" || typeof wx.navigateTo !== "function") return;
      wx.navigateTo({
        url: `/pages/registration-detail/index?registration_id=${encodeURIComponent(id)}`,
      });
    },

    async favoriteSeries() {
      const detail = this.data.detail;
      if (!detail || !detail.seriesId || this.data.favoriteSaved || this._favoriteMutation) return;
      const app = resolveApp();
      if (!app || typeof app.ensureAuthenticated !== "function") {
        this.setData({ favoriteError: "登录服务暂时不可用", favoriteMessage: "" });
        return;
      }

      const version = Number(this._favoriteVersion || 0) + 1;
      this._favoriteVersion = version;
      this._favoriteMutation = true;
      this.setData({ favoriteSaving: true, favoriteError: "", favoriteMessage: "" });
      try {
        await Promise.resolve(app.runtimeReady || null);
        await app.ensureAuthenticated();
        if (!this._active || version !== this._favoriteVersion) return;
        await api.setSeriesFavorite(detail.seriesId, true);
        if (!this._active || version !== this._favoriteVersion) return;
        this.setData({ favoriteSaved: true, favoriteMessage: "已加入想去，可在“我的想去”中查看" });
      } catch (error) {
        if (this._active && version === this._favoriteVersion) {
          this.setData({ favoriteError: getUserMessage(error, "收藏失败，请稍后重试") });
        }
      } finally {
        if (version === this._favoriteVersion) {
          this._favoriteMutation = false;
          if (this._active) this.setData({ favoriteSaving: false });
        }
      }
    },

    onShareAppMessage() {
      const detail = this.data.detail;
      return {
        title: (detail && detail.title) || "享玩 AI 活动",
        path: detail
          ? `/pages/session-detail/index?session_id=${encodeURIComponent(detail.sessionId)}`
          : "/pages/index/index",
      };
    },
  };
}

if (typeof Page === "function") Page(createSessionDetailPageDefinition());

module.exports = { createSessionDetailPageDefinition };
