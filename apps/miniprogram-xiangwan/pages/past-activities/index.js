"use strict";

const { syncTabBar } = require("../../utils/tab-bar");

const {
  groupPastActivities,
  projectPastActivitiesPage,
} = require("../../features/past-activities/model");
const { xiangwanApi } = require("../../services/xiangwan-api");
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

const ACTIVITY_FILTERS = Object.freeze([
  { value: "all", label: "全部" },
  { value: "ai_roundtable", label: "AI圆桌派" },
  { value: "special_event", label: "专题活动" },
  { value: "course", label: "课程" },
  { value: "competition", label: "赛事" },
  { value: "custom", label: "自定义活动" },
  { value: "excellent_works", label: "优秀作品" },
]);

function createPastActivitiesPageDefinition(api = xiangwanApi) {
  return {
    data: {
      activityFilters: ACTIVITY_FILTERS,
      filter: { activityType: "all", limit: 20 },
      items: [],
      seriesGroups: [],
      nextCursor: "",
      loading: true,
      loadingMore: false,
      errorMessage: "",
      emptyMessage: "",
    },

    onLoad() {
      this._active = true;
      void this.loadPastActivities(false);
    },

    onShow() {
      syncTabBar(this, "pages/past-activities/index");
      this._active = true;
    },

    onUnload() {
      this._active = false;
      this._requestVersion = Number(this._requestVersion || 0) + 1;
    },

    onPullDownRefresh() {
      return this.loadPastActivities(false);
    },

    onReachBottom() {
      if (this.data.nextCursor && !this.data.loadingMore) {
        void this.loadPastActivities(true);
      }
    },

    async loadPastActivities(append) {
      if (this.data.filter.activityType === "excellent_works") {
        // The placeholder tab has no network request of its own. Invalidate
        // any slower request started for the previous category before
        // clearing the list, otherwise its response could repopulate stale
        // cards after the user has switched tabs.
        this._requestVersion = Number(this._requestVersion || 0) + 1;
        this.setData({
          loading: false,
          loadingMore: false,
          items: [],
          seriesGroups: [],
          nextCursor: "",
          errorMessage: "",
          emptyMessage: "优秀作品即将上线",
        });
        return;
      }
      if (append && (!this.data.nextCursor || this.data.loadingMore)) return;
      const version = Number(this._requestVersion || 0) + 1;
      this._requestVersion = version;
      this.setData(
        append
          ? { loadingMore: true, errorMessage: "" }
          : { loading: true, errorMessage: "", emptyMessage: "" },
      );
      try {
        const page = projectPastActivitiesPage(
          await api.getPastActivities({
            ...this.data.filter,
            cursor: append ? this.data.nextCursor : "",
          }),
          { apiBaseUrl: currentApiBaseUrl() },
        );
        if (!this._active || version !== this._requestVersion) return;
        const items = append ? mergePastActivities(this.data.items, page.items) : page.items;
        this.setData({
          items,
          seriesGroups: groupPastActivities(items),
          nextCursor: page.nextCursor,
          emptyMessage: items.length ? "" : "暂时还没有往期活动",
        });
      } catch (error) {
        if (!this._active || version !== this._requestVersion) return;
        if (append && Number(error && error.statusCode) === 409) {
          this.setData({ nextCursor: "" });
          return this.loadPastActivities(false);
        }
        this.setData({
          errorMessage: getUserMessage(error, "往期活动加载失败，请稍后重试"),
        });
      } finally {
        if (this._active && version === this._requestVersion) {
          this.setData({ loading: false, loadingMore: false });
        }
        if (typeof wx !== "undefined" && typeof wx.stopPullDownRefresh === "function") {
          wx.stopPullDownRefresh();
        }
      }
    },

    selectActivity(event) {
      const value = String(event.currentTarget.dataset.value || "").trim();
      if (!value || value === this.data.filter.activityType) return;
      this.setData({ "filter.activityType": value });
      void this.loadPastActivities(false);
    },

    retry() {
      void this.loadPastActivities(false);
    },

    onPastCoverImageError(event) {
      const instanceId = String(event.currentTarget.dataset.instanceId || "").trim();
      if (!instanceId) return;
      const index = this.data.items.findIndex((item) => item.instanceId === instanceId);
      if (index < 0 || !this.data.items[index].coverImageUrl) return;
      const items = this.data.items.slice();
      items[index] = { ...items[index], coverImageUrl: "" };
      this.setData({ items, seriesGroups: groupPastActivities(items) });
    },

    openReview(event) {
      const instanceId = String(event.currentTarget.dataset.instanceId || "").trim();
      if (!instanceId || typeof wx === "undefined" || typeof wx.navigateTo !== "function") return;
      wx.navigateTo({
        url: `/pages/activity-review/index?instance_id=${encodeURIComponent(instanceId)}`,
      });
    },

    onShareAppMessage() {
      return { title: "享玩 AI 往期活动", path: "/pages/past-activities/index" };
    },
  };
}

function mergePastActivities(current, incoming) {
  const seen = new Set();
  return [
    ...(Array.isArray(current) ? current : []),
    ...(Array.isArray(incoming) ? incoming : []),
  ].filter((item) => {
    const id = String((item && item.instanceId) || "");
    if (!id || seen.has(id)) return false;
    seen.add(id);
    return true;
  });
}

if (typeof Page === "function") Page(createPastActivitiesPageDefinition());

module.exports = { createPastActivitiesPageDefinition, mergePastActivities };
