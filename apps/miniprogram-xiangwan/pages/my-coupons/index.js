"use strict";

const { projectCouponsPage } = require("../../features/coupons/model");
const { xiangwanApi } = require("../../services/xiangwan-api");
const { getUserMessage } = require("../../utils/errors");

const STATE_FILTERS = Object.freeze([
  { value: "all", label: "全部" },
  { value: "available", label: "可使用" },
  { value: "held", label: "已锁定" },
  { value: "correction_required", label: "待核对" },
  { value: "expired", label: "已过期" },
  { value: "redeemed", label: "已使用" },
  { value: "invalidated", label: "已失效" },
]);
const STATE_VALUES = new Set(STATE_FILTERS.map(({ value }) => value));

function resolveApp() {
  if (typeof getApp !== "function") return null;
  try {
    return getApp();
  } catch (_error) {
    return null;
  }
}

function createMyCouponsPageDefinition(api = xiangwanApi) {
  return {
    data: {
      stateFilters: STATE_FILTERS,
      activeState: "all",
      items: [],
      nextCursor: "",
      asOf: "",
      loading: false,
      loadingMore: false,
      needsLogin: false,
      errorMessage: "",
      emptyMessage: "",
    },

    onLoad() {
      this._active = true;
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
        void this.loadCoupons(false);
        return;
      }
      this.setData({
        needsLogin: true,
        items: [],
        nextCursor: "",
        asOf: "",
        loading: false,
        emptyMessage: "",
      });
    },

    onHide() {
      this._active = false;
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
      return this.loadCoupons(false);
    },

    onReachBottom() {
      if (this.data.nextCursor && !this.data.loadingMore) void this.loadCoupons(true);
    },

    async loginAndLoad() {
      const app = resolveApp();
      if (!app || typeof app.ensureAuthenticated !== "function") {
        this.setData({ errorMessage: "登录服务暂时不可用" });
        return;
      }
      this.setData({ loading: true, errorMessage: "" });
      try {
        await app.ensureAuthenticated();
        if (!this._active) return;
        this.setData({ needsLogin: false });
        await this.loadCoupons(false);
      } catch (error) {
        if (this._active) {
          this.setData({ errorMessage: getUserMessage(error, "登录未完成，请稍后重试") });
        }
      } finally {
        if (this._active) this.setData({ loading: false });
      }
    },

    async loadCoupons(append) {
      if (append && (!this.data.nextCursor || this.data.loadingMore)) return;
      const version = Number(this._requestVersion || 0) + 1;
      this._requestVersion = version;
      this.setData(
        append
          ? { loadingMore: true, errorMessage: "" }
          : { loading: true, errorMessage: "", emptyMessage: "" },
      );
      try {
        const page = projectCouponsPage(
          await api.getMyCoupons({
            state: this.data.activeState,
            cursor: append ? this.data.nextCursor : "",
            limit: 20,
          }),
        );
        if (!this._active || version !== this._requestVersion) return;
        const items = append ? mergeCoupons(this.data.items, page.items) : page.items;
        this.setData({
          items,
          nextCursor: page.nextCursor,
          asOf: page.asOf,
          emptyMessage: items.length ? "" : "当前分类还没有优惠券",
        });
      } catch (error) {
        if (!this._active || version !== this._requestVersion) return;
        if (Number(error && error.statusCode) === 401) {
          this.setData({ needsLogin: true, items: [], nextCursor: "", asOf: "" });
        }
        if (append && Number(error && error.statusCode) === 409) {
          this.setData({ nextCursor: "" });
          return this.loadCoupons(false);
        }
        this.setData({ errorMessage: getUserMessage(error, "优惠券加载失败，请稍后重试") });
      } finally {
        if (this._active && version === this._requestVersion) {
          this.setData({ loading: false, loadingMore: false });
        }
        if (typeof wx !== "undefined" && wx.stopPullDownRefresh) wx.stopPullDownRefresh();
      }
    },

    selectState(event) {
      const state = String(event.currentTarget.dataset.value || "").trim();
      if (!STATE_VALUES.has(state) || state === this.data.activeState) return;
      this.setData({ activeState: state });
      void this.loadCoupons(false);
    },

    retry() {
      if (this.data.needsLogin) void this.loginAndLoad();
      else void this.loadCoupons(false);
    },

    openOrder(event) {
      const orderId = String(event.currentTarget.dataset.orderId || "").trim();
      if (!orderId || typeof wx === "undefined" || typeof wx.navigateTo !== "function") return;
      wx.navigateTo({
        url: `/pages/order-detail/index?order_id=${encodeURIComponent(orderId)}`,
      });
    },
  };
}

function mergeCoupons(current, incoming) {
  const seen = new Set();
  return [
    ...(Array.isArray(current) ? current : []),
    ...(Array.isArray(incoming) ? incoming : []),
  ].filter((item) => {
    const id = String((item && item.couponId) || "");
    if (!id || seen.has(id)) return false;
    seen.add(id);
    return true;
  });
}

if (typeof Page === "function") Page(createMyCouponsPageDefinition());

module.exports = { STATE_FILTERS, createMyCouponsPageDefinition, mergeCoupons };
