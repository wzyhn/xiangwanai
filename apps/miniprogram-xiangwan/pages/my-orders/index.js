"use strict";

const { projectOrdersPage } = require("../../features/orders/model");
const { xiangwanApi } = require("../../services/xiangwan-api");
const { getUserMessage } = require("../../utils/errors");

const STATE_FILTERS = Object.freeze([
  { value: "all", label: "全部" },
  { value: "pending_payment", label: "待支付" },
  { value: "refund_processing", label: "退款中" },
  { value: "paid", label: "已支付" },
  { value: "refunded", label: "已退款" },
  { value: "closed", label: "已关闭" },
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

function createMyOrdersPageDefinition(api = xiangwanApi) {
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
        void this.loadOrders(false);
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
      return this.loadOrders(false);
    },

    onReachBottom() {
      if (this.data.nextCursor && !this.data.loadingMore) void this.loadOrders(true);
    },

    async loginAndLoad() {
      const app = resolveApp();
      if (!app || typeof app.ensureAuthenticated !== "function") return;
      this.setData({ loading: true, errorMessage: "" });
      try {
        await app.ensureAuthenticated();
        if (!this._active) return;
        this.setData({ needsLogin: false });
        await this.loadOrders(false);
      } catch (error) {
        if (this._active) {
          this.setData({ errorMessage: getUserMessage(error, "登录未完成，请稍后重试") });
        }
      } finally {
        if (this._active) this.setData({ loading: false });
      }
    },

    async loadOrders(append) {
      if (append && (!this.data.nextCursor || this.data.loadingMore)) return;
      const version = Number(this._requestVersion || 0) + 1;
      this._requestVersion = version;
      this.setData(
        append
          ? { loadingMore: true, errorMessage: "" }
          : { loading: true, errorMessage: "", emptyMessage: "" },
      );
      try {
        const page = projectOrdersPage(
          await api.getMyOrders({
            state: this.data.activeState,
            cursor: append ? this.data.nextCursor : "",
            limit: 20,
          }),
        );
        if (!this._active || version !== this._requestVersion) return;
        const items = append ? mergeOrders(this.data.items, page.items) : page.items;
        this.setData({
          items,
          nextCursor: page.nextCursor,
          emptyMessage: items.length ? "" : "当前分类还没有订单",
        });
      } catch (error) {
        if (!this._active || version !== this._requestVersion) return;
        if (Number(error && error.statusCode) === 401) {
          this.setData({ needsLogin: true, items: [], nextCursor: "" });
        }
        if (append && Number(error && error.statusCode) === 409) {
          this.setData({ nextCursor: "" });
          return this.loadOrders(false);
        }
        this.setData({ errorMessage: getUserMessage(error, "订单加载失败，请稍后重试") });
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
      void this.loadOrders(false);
    },

    retry() {
      if (this.data.needsLogin) void this.loginAndLoad();
      else void this.loadOrders(false);
    },

    openOrder(event) {
      const id = String(event.currentTarget.dataset.orderId || "").trim();
      if (!id || typeof wx === "undefined" || typeof wx.navigateTo !== "function") return;
      wx.navigateTo({ url: `/pages/order-detail/index?order_id=${encodeURIComponent(id)}` });
    },
  };
}

function mergeOrders(current, incoming) {
  const seen = new Set();
  return [
    ...(Array.isArray(current) ? current : []),
    ...(Array.isArray(incoming) ? incoming : []),
  ].filter((item) => {
    const id = String((item && item.orderId) || "");
    if (!id || seen.has(id)) return false;
    seen.add(id);
    return true;
  });
}

if (typeof Page === "function") Page(createMyOrdersPageDefinition());

module.exports = { STATE_FILTERS, createMyOrdersPageDefinition, mergeOrders };
