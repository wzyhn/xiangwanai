"use strict";

const { projectActivityReview } = require("../../features/past-activities/model");
const { canonicalUUID, xiangwanApi } = require("../../services/xiangwan-api");
const { getUserMessage } = require("../../utils/errors");

function currentApiBaseUrl() {
  if (typeof getApp !== "function") return "";
  try {
    const app = getApp();
    return String((app && app.globalData && app.globalData.apiBaseUrl) || "").trim();
  } catch (_error) {
    return "";
  }
}

function createActivityReviewPageDefinition(api = xiangwanApi) {
  return {
    data: {
      loading: true,
      errorMessage: "",
      resourceErrorMessage: "",
      showSessionResources: false,
      review: null,
    },

    onLoad(options = {}) {
      this._active = true;
      try {
        this._instanceId = canonicalUUID(options.instance_id, "活动期次");
        this._sessionId = options.session_id ? canonicalUUID(options.session_id, "活动场次") : "";
        this.setData({ showSessionResources: !this._sessionId });
      } catch (error) {
        this.setData({ loading: false, errorMessage: getUserMessage(error, "回顾链接无效") });
        return;
      }
      void this.loadReview();
    },

    onUnload() {
      this._active = false;
      this._requestVersion = Number(this._requestVersion || 0) + 1;
    },

    onPullDownRefresh() {
      if (!this._instanceId) return Promise.resolve();
      return this.loadReview();
    },

    async loadReview() {
      const version = Number(this._requestVersion || 0) + 1;
      this._requestVersion = version;
      this.setData({ loading: true, errorMessage: "", resourceErrorMessage: "" });
      try {
        const review = projectActivityReview(
          await api.getPublicReview(this._instanceId, this._sessionId),
          { apiBaseUrl: currentApiBaseUrl() },
        );
        if (!this._active || version !== this._requestVersion) return;
        this.setData({ review });
      } catch (error) {
        if (this._active && version === this._requestVersion) {
          this.setData({ errorMessage: getUserMessage(error, "活动回顾加载失败") });
        }
      } finally {
        if (this._active && version === this._requestVersion) {
          this.setData({ loading: false });
        }
        if (typeof wx !== "undefined" && typeof wx.stopPullDownRefresh === "function") {
          wx.stopPullDownRefresh();
        }
      }
    },

    retry() {
      void this.loadReview();
    },

    openNextInstance() {
      const next = this.data.review && this.data.review.nextInstance;
      if (!next || !next.available || typeof wx === "undefined") return;
      if (
        next.action === "session_detail" &&
        next.sessionId &&
        typeof wx.navigateTo === "function"
      ) {
        wx.navigateTo({
          url: `/pages/session-detail/index?session_id=${encodeURIComponent(next.sessionId)}`,
        });
        return;
      }
      if (
        next.action === "session_selection_required" &&
        next.instanceId &&
        typeof wx.navigateTo === "function"
      ) {
        wx.navigateTo({
          url: `/pages/session-collection/index?instance_id=${encodeURIComponent(next.instanceId)}`,
        });
      }
    },

    openExternalResource(event) {
      let blockId;
      try {
        blockId = canonicalUUID(event.currentTarget.dataset.blockId, "外部资源");
      } catch (_error) {
        this.reportResourceError("资源链接暂时无法打开");
        return;
      }
      if (!this._instanceId || typeof wx === "undefined" || !wx.navigateTo) {
        this.reportResourceError("资源链接暂时无法打开");
        return;
      }
      const sessionQuery = this._sessionId
        ? `&session_id=${encodeURIComponent(this._sessionId)}`
        : "";
      wx.navigateTo({
        url: `/pages/external-resource/index?instance_id=${encodeURIComponent(
          this._instanceId,
        )}&block_id=${encodeURIComponent(blockId)}${sessionQuery}`,
        fail: () => this.reportResourceError("资源链接打开失败，请稍后重试"),
      });
    },

    openSessionResources() {
      if (
        !this._instanceId ||
        this._sessionId ||
        typeof wx === "undefined" ||
        typeof wx.navigateTo !== "function"
      ) {
        return;
      }
      wx.navigateTo({
        url: `/pages/session-collection/index?review_instance_id=${encodeURIComponent(
          this._instanceId,
        )}`,
      });
    },

    openMediaResource(event) {
      const url = String(event.currentTarget.dataset.url || "").trim();
      const base = currentApiBaseUrl().replace(/\/+$/, "");
      if (
        !url ||
        !base ||
        !url.startsWith(`${base}/api/v1/xiangwan/media/`) ||
        typeof wx === "undefined" ||
        typeof wx.downloadFile !== "function"
      ) {
        this.reportResourceError("文件地址暂时无法使用");
        return;
      }
      this.setData({ resourceErrorMessage: "" });
      wx.downloadFile({
        url,
        success: (result) => {
          if (
            Number(result && result.statusCode) === 200 &&
            result.tempFilePath &&
            typeof wx.openDocument === "function"
          ) {
            wx.openDocument({
              filePath: result.tempFilePath,
              showMenu: true,
              fail: () => this.reportResourceError("文件打开失败，请稍后重试"),
            });
            return;
          }
          this.reportResourceError("文件下载失败，请稍后重试");
        },
        fail: () => this.reportResourceError("文件下载失败，请稍后重试"),
      });
    },

    handleMediaError(event) {
      const kind = String(
        event && event.currentTarget && event.currentTarget.dataset
          ? event.currentTarget.dataset.mediaKind || ""
          : "",
      ).trim();
      if (kind === "hero" && this.data.review && this.data.review.heroImageUrl) {
        this.setData({
          review: { ...this.data.review, heroImageUrl: "" },
          resourceErrorMessage: "回顾封面暂不可用，已切换为文字内容。",
        });
        return;
      }
      this.reportResourceError("媒体资源加载失败，请稍后重试");
    },

    reportResourceError(message) {
      if (this._active) this.setData({ resourceErrorMessage: message });
    },

    onShareAppMessage() {
      const review = this.data.review;
      const sessionQuery = this._sessionId
        ? `&session_id=${encodeURIComponent(this._sessionId)}`
        : "";
      return {
        title: (review && review.instanceTitle) || "享玩 AI 活动回顾",
        path: this._instanceId
          ? `/pages/activity-review/index?instance_id=${encodeURIComponent(this._instanceId)}${sessionQuery}`
          : "/pages/past-activities/index",
      };
    },
  };
}

if (typeof Page === "function") Page(createActivityReviewPageDefinition());

module.exports = { createActivityReviewPageDefinition, currentApiBaseUrl };
