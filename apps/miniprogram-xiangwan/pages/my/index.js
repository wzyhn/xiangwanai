"use strict";

const { syncTabBar } = require("../../utils/tab-bar");

const { isXiangwanProfile, projectProfileBadge } = require("../../features/profile/model");
const { xiangwanApi } = require("../../services/xiangwan-api");
const { getUserMessage } = require("../../utils/errors");

const PROFILE_ROUTE = "/pages/my-profile/index";

const FEATURE_ROUTES = Object.freeze({
  registrations: "/pages/my-registrations/index",
  orders: "/pages/my-orders/index",
  coupons: "/pages/my-coupons/index",
  favorites: "/pages/my-favorites/index",
  benefits: "/pages/my-benefits/index",
});

const FEATURES = Object.freeze([
  {
    key: "registrations",
    icon: "calendar",
    title: "我的报名",
    displayTitle: "我的预约",
    description: "查看报名、退款、签到与取消状态",
    displayDescription: "查看进行中、已结束与已取消的活动",
  },
  {
    key: "orders",
    icon: "order",
    title: "我的订单",
    description: "查看价格、支付与退款进度",
  },
  {
    key: "coupons",
    icon: "coupon",
    title: "我的优惠券",
    description: "查看可用、锁定、已用和已调整的优惠权益",
  },
  {
    key: "favorites",
    icon: "heart",
    title: "我的收藏",
    displayTitle: "我的想去",
    description: "查看收藏的活动系列与当前公开场次",
    displayDescription: "收藏的活动都在这里",
  },
  {
    key: "benefits",
    icon: "usergroup",
    title: "身份与贡献",
    displayTitle: "我的权益",
    description: "查看公开身份、参与经历与社区贡献",
    displayDescription: "查看社区身份与贡献记录",
  },
]);

const BOOKING_STATUS = Object.freeze([
  { key: "pending", value: "—", label: "待参加" },
  { key: "completed", value: "—", label: "已结束" },
  { key: "cancelled", value: "—", label: "已取消" },
  { key: "all", value: "—", label: "全部" },
]);

function resolveApp() {
  if (typeof getApp !== "function") return null;
  try {
    return getApp();
  } catch (_error) {
    return null;
  }
}

function hasValidSession(app) {
  return Boolean(app && app.weconqAuth && app.weconqAuth.hasValidSession());
}

function readXiangwanProfile(app) {
  if (app && typeof app.getXiangwanProfile === "function") {
    return app.getXiangwanProfile();
  }
  return app && app.globalData && app.globalData.profile;
}

function readProfileBadge(app) {
  return projectProfileBadge(readXiangwanProfile(app));
}

function projectBookingStatus(items = []) {
  const counts = { pending: 0, completed: 0, cancelled: 0, all: 0 };
  const source = Array.isArray(items) ? items : [];
  counts.all = source.length;
  source.forEach((item) => {
    const state = String((item && item.state) || "").trim();
    if (["cancelled", "refund_processing", "refunded"].includes(state)) counts.cancelled += 1;
    if (state === "ended") counts.completed += 1;
    if (state === "registered") counts.pending += 1;
  });
  return BOOKING_STATUS.map((item) => ({ ...item, value: String(counts[item.key]) }));
}

function createMyPageDefinition(api = xiangwanApi) {
  return {
    data: {
      features: FEATURES,
      bookingStatus: BOOKING_STATUS,
      authenticated: false,
      needsLogin: false,
      loading: true,
      bookingStatusLoading: false,
      bookingStatusError: false,
      errorMessage: "",
      profileBadge: projectProfileBadge(null),
    },

    onLoad() {
      this._active = true;
    },

    onShow() {
      syncTabBar(this, "pages/my/index");
      this._active = true;
      const version = Number(this._showVersion || 0) + 1;
      this._showVersion = version;
      const app = resolveApp();
      this.setData({
        loading: true,
        errorMessage: "",
        bookingStatus: BOOKING_STATUS,
        bookingStatusError: false,
      });
      Promise.resolve((app && app.runtimeReady) || null)
        .then(() => {
          if (!this._active || version !== this._showVersion) return;
          const authenticated = hasValidSession(app);
          this.setData({
            authenticated,
            needsLogin: !authenticated,
            loading: false,
          });
          if (authenticated) {
            this.presentProfile(app, version);
            void this.loadBookingStatus(version);
          }
        })
        .catch((error) => {
          if (!this._active || version !== this._showVersion) return;
          this.setData({
            authenticated: false,
            needsLogin: true,
            loading: false,
            errorMessage: getUserMessage(error, "登录状态读取失败，请稍后重试"),
          });
        });
    },

    // 优先渲染 globalData.profile;缺失(或仍是平台 auth 形状)则静默补拉
    presentProfile(app, expectedVersion = this._showVersion) {
      this.setData({ profileBadge: readProfileBadge(app) });

      if (!app || typeof app.refreshXiangwanProfile !== "function") return;
      Promise.resolve()
        .then(() => app.refreshXiangwanProfile())
        .then(() => {
          if (!this._active || expectedVersion !== this._showVersion) return;
          this.setData({ profileBadge: readProfileBadge(app) });
        })
        .catch((error) => {
          if (!this._active || expectedVersion !== this._showVersion) return;
          this.setData({ errorMessage: getUserMessage(error, "资料同步失败，请稍后重试") });
        });
    },

    onHide() {
      this._active = false;
      this._showVersion = Number(this._showVersion || 0) + 1;
    },

    onUnload() {
      this._active = false;
      this._showVersion = Number(this._showVersion || 0) + 1;
    },

    async login() {
      if (this.data.loading) return;
      const app = resolveApp();
      if (!app || typeof app.ensureAuthenticated !== "function") {
        this.setData({ errorMessage: "登录服务暂时不可用" });
        return;
      }
      const version = Number(this._showVersion || 0);
      this.setData({ loading: true, errorMessage: "" });
      try {
        await app.ensureAuthenticated();
        if (!this._active || version !== this._showVersion) return;
        const authenticated = hasValidSession(app);
        this.setData({
          authenticated,
          needsLogin: !authenticated,
        });
        if (authenticated) this.presentProfile(app, version);
        if (authenticated) void this.loadBookingStatus(version);
        if (!authenticated) this.setData({ errorMessage: "登录未完成，请稍后重试" });
      } catch (error) {
        if (this._active && version === this._showVersion) {
          this.setData({ errorMessage: getUserMessage(error, "登录未完成，请稍后重试") });
        }
      } finally {
        if (this._active && version === this._showVersion) this.setData({ loading: false });
      }
    },

    async loadBookingStatus(expectedVersion = this._showVersion) {
      if (typeof wx === "undefined" || !api || typeof api.getMyRegistrations !== "function") return;
      const requestVersion = Number(this._bookingRequestVersion || 0) + 1;
      this._bookingRequestVersion = requestVersion;
      this.setData({ bookingStatusLoading: true, bookingStatusError: false });
      const items = [];
      const seen = new Set();
      const cursors = new Set();
      const isCurrent = () =>
        this._active &&
        expectedVersion === this._showVersion &&
        requestVersion === this._bookingRequestVersion;
      let cursor = "";
      try {
        for (let pageNumber = 0; pageNumber < 20; pageNumber += 1) {
          const page = await api.getMyRegistrations({ state: "all", cursor, limit: 100 });
          if (!isCurrent()) return;
          if (!page || !Array.isArray(page.items)) throw new Error("Invalid appointment summary");
          const pageItems = page.items;
          pageItems.forEach((item) => {
            const id = String((item && item.registration_id) || "").trim();
            if (!id || !seen.has(id)) {
              if (id) seen.add(id);
              items.push(item);
            }
          });
          const nextCursor = String((page && page.next_cursor) || "").trim();
          if (!nextCursor) {
            this.setData({
              bookingStatus: projectBookingStatus(items),
              bookingStatusLoading: false,
            });
            return;
          }
          if (cursors.has(nextCursor)) throw new Error("Repeated appointment cursor");
          cursors.add(nextCursor);
          cursor = nextCursor;
        }
        // Never label an incomplete page count as a total.
        throw new Error("Appointment summary incomplete");
      } catch (_error) {
        if (isCurrent()) {
          this.setData({
            bookingStatusLoading: false,
            bookingStatusError: true,
            bookingStatus: BOOKING_STATUS,
          });
        }
      }
    },

    retryBookingStatus() {
      if (this.data.authenticated && !this.data.bookingStatusLoading) void this.loadBookingStatus();
    },

    openBookingStatus(event) {
      if (!this.data.authenticated || typeof wx === "undefined") return;
      const state = {
        pending: "registered",
        completed: "ended",
        cancelled: "cancelled",
        all: "all",
      }[event.currentTarget.dataset.key];
      if (state && typeof wx.navigateTo === "function")
        wx.navigateTo({ url: `${FEATURE_ROUTES.registrations}?state=${state}` });
    },

    openProfile() {
      if (!this.data.authenticated) return;
      if (typeof wx === "undefined" || typeof wx.navigateTo !== "function") return;
      wx.navigateTo({ url: PROFILE_ROUTE });
    },

    openPoliciesPage() {
      if (typeof wx !== "undefined" && typeof wx.navigateTo === "function") {
        wx.navigateTo({ url: "/pages/policies/index" });
      }
    },

    onAvatarError(event) {
      const badge = this.data.profileBadge;
      if (!badge || !badge.avatarUrl) return;
      const failedUrl =
        event &&
        event.currentTarget &&
        event.currentTarget.dataset &&
        event.currentTarget.dataset.url;
      if (failedUrl && failedUrl !== badge.avatarUrl) return;
      this.setData({ profileBadge: { ...badge, avatarUrl: "" } });
    },

    openFeature(event) {
      if (!this.data.authenticated) return;
      const key = String(event.currentTarget.dataset.key || "").trim();
      const url = FEATURE_ROUTES[key];
      if (!url || typeof wx === "undefined" || typeof wx.navigateTo !== "function") return;
      wx.navigateTo({ url });
    },
  };
}

if (typeof Page === "function") Page(createMyPageDefinition());

module.exports = {
  BOOKING_STATUS,
  FEATURES,
  FEATURE_ROUTES,
  PROFILE_ROUTE,
  projectBookingStatus,
  createMyPageDefinition,
};
