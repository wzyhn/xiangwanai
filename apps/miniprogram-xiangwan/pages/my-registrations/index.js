"use strict";

const { projectRegistrationsPage } = require("../../features/registrations/model");
const { xiangwanApi } = require("../../services/xiangwan-api");
const { getUserMessage } = require("../../utils/errors");

const STATE_FILTERS = Object.freeze([
  { value: "all", label: "全部" },
  { value: "registered", label: "报名成功" },
  { value: "pending_payment", label: "待支付" },
  { value: "ended", label: "已结束" },
  { value: "cancelled", label: "已取消" },
  { value: "refund_processing", label: "退款中" },
  { value: "refunded", label: "已退款" },
]);

function resolveApp() {
  if (typeof getApp !== "function") return null;
  try {
    return getApp();
  } catch (_error) {
    return null;
  }
}

function createMyRegistrationsPageDefinition(api = xiangwanApi) {
  return {
    data: {
      stateFilters: STATE_FILTERS,
      activeState: "all",
      items: [],
      nextCursor: "",
      loading: false,
      loadingMore: false,
      needsLogin: false,
      errorMessage: "",
      emptyMessage: "",
    },

    onLoad(options = {}) {
      this._active = true;
      if (STATE_FILTERS.some((item) => item.value === options.state)) {
        this.setData({ activeState: options.state });
      }
    },

    onShow() {
      this._active = true;
      const version = Number(this._showVersion || 0) + 1;
      this._showVersion = version;
      const app = resolveApp();
      this.setData({ loading: true, errorMessage: "" });
      Promise.resolve((app && app.runtimeReady) || null)
        .then(() => {
          if (!this._active || version !== this._showVersion) return;
          this.presentCurrentSession(app);
        })
        .catch((error) => {
          if (!this._active || version !== this._showVersion) return;
          this.setData({
            loading: false,
            needsLogin: true,
            errorMessage: getUserMessage(error, "登录状态读取失败，请稍后重试"),
          });
        });
    },

    presentCurrentSession(app) {
      const authenticated = Boolean(app && app.weconqAuth && app.weconqAuth.hasValidSession());
      if (authenticated) {
        this.setData({ needsLogin: false });
        void this.loadRegistrations(false);
        return;
      }
      this.setData({
        needsLogin: true,
        items: [],
        nextCursor: "",
        loading: false,
        emptyMessage: "",
      });
    },

    onHide() {
      this._showVersion = Number(this._showVersion || 0) + 1;
      this._requestVersion = Number(this._requestVersion || 0) + 1;
    },

    onUnload() {
      this._active = false;
      this._showVersion = Number(this._showVersion || 0) + 1;
      this._requestVersion = Number(this._requestVersion || 0) + 1;
    },

    onPullDownRefresh() {
      if (this.data.needsLogin) {
        if (typeof wx !== "undefined" && wx.stopPullDownRefresh) wx.stopPullDownRefresh();
        return;
      }
      return this.loadRegistrations(false);
    },

    onReachBottom() {
      if (this.data.nextCursor && !this.data.loadingMore) {
        void this.loadRegistrations(true);
      }
    },

    async loginAndLoad() {
      const app = resolveApp();
      if (!app || typeof app.ensureAuthenticated !== "function") return;
      this.setData({ loading: true, errorMessage: "" });
      try {
        await app.ensureAuthenticated();
        if (!this._active) return;
        this.setData({ needsLogin: false });
        await this.loadRegistrations(false);
      } catch (error) {
        if (this._active) {
          this.setData({ errorMessage: getUserMessage(error, "登录未完成，请稍后重试") });
        }
      } finally {
        if (this._active) this.setData({ loading: false });
      }
    },

    async loadRegistrations(append) {
      if (append && (!this.data.nextCursor || this.data.loadingMore)) return;
      const version = Number(this._requestVersion || 0) + 1;
      this._requestVersion = version;
      this.setData(
        append
          ? { loadingMore: true, errorMessage: "" }
          : { loading: true, errorMessage: "", emptyMessage: "" },
      );
      try {
        const page = projectRegistrationsPage(
          await api.getMyRegistrations({
            state: this.data.activeState,
            cursor: append ? this.data.nextCursor : "",
            limit: 20,
          }),
        );
        if (!this._active || version !== this._requestVersion) return;
        const items = append ? mergeRegistrations(this.data.items, page.items) : page.items;
        this.setData({
          items,
          nextCursor: page.nextCursor,
          emptyMessage: items.length ? "" : "当前分类还没有报名记录",
        });
      } catch (error) {
        if (!this._active || version !== this._requestVersion) return;
        if (Number(error && error.statusCode) === 401) {
          this.setData({ needsLogin: true, items: [], nextCursor: "" });
        }
        if (append && Number(error && error.statusCode) === 409) {
          this.setData({ nextCursor: "" });
          return this.loadRegistrations(false);
        }
        this.setData({
          errorMessage: getUserMessage(error, "报名记录加载失败，请稍后重试"),
        });
      } finally {
        if (this._active && version === this._requestVersion) {
          this.setData({ loading: false, loadingMore: false });
        }
        if (typeof wx !== "undefined" && wx.stopPullDownRefresh) wx.stopPullDownRefresh();
      }
    },

    selectState(event) {
      const state = String(event.currentTarget.dataset.value || "").trim();
      if (!state || state === this.data.activeState) return;
      this.setData({ activeState: state });
      void this.loadRegistrations(false);
    },

    retry() {
      if (this.data.needsLogin) void this.loginAndLoad();
      else void this.loadRegistrations(false);
    },

    openRegistration(event) {
      const id = String(event.currentTarget.dataset.registrationId || "").trim();
      if (!id || typeof wx === "undefined" || !wx.navigateTo) return;
      wx.navigateTo({
        url: `/pages/registration-detail/index?registration_id=${encodeURIComponent(id)}`,
      });
    },
  };
}

function mergeRegistrations(current, incoming) {
  const seen = new Set();
  return [
    ...(Array.isArray(current) ? current : []),
    ...(Array.isArray(incoming) ? incoming : []),
  ].filter((item) => {
    const id = String((item && item.registrationId) || "");
    if (!id || seen.has(id)) return false;
    seen.add(id);
    return true;
  });
}

if (typeof Page === "function") Page(createMyRegistrationsPageDefinition());

module.exports = { createMyRegistrationsPageDefinition, mergeRegistrations };
