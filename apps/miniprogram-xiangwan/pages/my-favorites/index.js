"use strict";

const { projectFavoritesPage } = require("../../features/favorites/model");
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

function resolveApp() {
  if (typeof getApp !== "function") return null;
  try {
    return getApp();
  } catch (_error) {
    return null;
  }
}

function confirmUnfavorite(title) {
  if (typeof wx === "undefined" || typeof wx.showModal !== "function") {
    return Promise.resolve(false);
  }
  return new Promise((resolve) => {
    wx.showModal({
      title: "取消收藏",
      content: `确定不再收藏“${String(title || "该活动").trim() || "该活动"}”吗？`,
      confirmText: "取消收藏",
      confirmColor: "#913d50",
      success: (result) => resolve(Boolean(result && result.confirm)),
      fail: () => resolve(false),
    });
  });
}

function createMyFavoritesPageDefinition(api = xiangwanApi, confirmRemoval = confirmUnfavorite) {
  return {
    data: {
      items: [],
      nextCursor: "",
      asOf: "",
      loading: false,
      loadingMore: false,
      needsLogin: false,
      removingSeriesId: "",
      successMessage: "",
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
      this.setData({
        loading: true,
        removingSeriesId: "",
        errorMessage: "",
        successMessage: "",
      });
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
        void this.loadFavorites(false);
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
      this._mutationVersion = Number(this._mutationVersion || 0) + 1;
      this._favoriteMutation = false;
    },

    onUnload() {
      this._active = false;
      this._showVersion = Number(this._showVersion || 0) + 1;
      this._requestVersion = Number(this._requestVersion || 0) + 1;
      this._mutationVersion = Number(this._mutationVersion || 0) + 1;
      this._favoriteMutation = false;
    },

    onPullDownRefresh() {
      if (this.data.needsLogin) {
        if (typeof wx !== "undefined" && wx.stopPullDownRefresh) wx.stopPullDownRefresh();
        return;
      }
      return this.loadFavorites(false);
    },

    onReachBottom() {
      if (!this._favoriteMutation && this.data.nextCursor && !this.data.loadingMore) {
        void this.loadFavorites(true);
      }
    },

    async loginAndLoad() {
      const app = resolveApp();
      if (!app || typeof app.ensureAuthenticated !== "function") {
        this.setData({ errorMessage: "登录服务暂时不可用" });
        return;
      }
      this.setData({ loading: true, errorMessage: "", successMessage: "" });
      try {
        await app.ensureAuthenticated();
        if (!this._active) return;
        this.setData({ needsLogin: false });
        await this.loadFavorites(false);
      } catch (error) {
        if (this._active) {
          this.setData({ errorMessage: getUserMessage(error, "登录未完成，请稍后重试") });
        }
      } finally {
        if (this._active) this.setData({ loading: false });
      }
    },

    async loadFavorites(append) {
      if (append && (!this.data.nextCursor || this.data.loadingMore)) return;
      const version = Number(this._requestVersion || 0) + 1;
      this._requestVersion = version;
      this.setData(
        append
          ? { loadingMore: true, errorMessage: "" }
          : { loading: true, errorMessage: "", emptyMessage: "" },
      );
      try {
        const page = projectFavoritesPage(
          await api.getMyFavorites({
            cursor: append ? this.data.nextCursor : "",
            limit: 20,
          }),
          { apiBaseUrl: currentApiBaseUrl() },
        );
        if (!this._active || version !== this._requestVersion) return;
        const items = append ? mergeFavorites(this.data.items, page.items) : page.items;
        this.setData({
          items,
          nextCursor: page.nextCursor,
          asOf: page.asOf,
          emptyMessage: items.length ? "" : "还没有收藏活动",
        });
      } catch (error) {
        if (!this._active || version !== this._requestVersion) return;
        if (Number(error && error.statusCode) === 401) {
          this.setData({ needsLogin: true, items: [], nextCursor: "", asOf: "" });
        }
        if (append && Number(error && error.statusCode) === 409) {
          this.setData({ nextCursor: "" });
          return this.loadFavorites(false);
        }
        this.setData({ errorMessage: getUserMessage(error, "收藏加载失败，请稍后重试") });
      } finally {
        if (this._active && version === this._requestVersion) {
          this.setData({ loading: false, loadingMore: false });
        }
        if (typeof wx !== "undefined" && wx.stopPullDownRefresh) wx.stopPullDownRefresh();
      }
    },

    retry() {
      if (this.data.needsLogin) void this.loginAndLoad();
      else void this.loadFavorites(false);
    },

    // An expired or revoked avatar must not leave a broken image in the
    // favorites card. Remove only the failed URL from the matching Series;
    // the count and the rest of the card remain authoritative.
    onFavoriteAvatarError(event) {
      const dataset = (event && event.currentTarget && event.currentTarget.dataset) || {};
      const seriesId = String(dataset.seriesId || "").trim();
      const url = String(dataset.url || "").trim();
      if (!seriesId || !url || !Array.isArray(this.data.items)) return;
      const items = this.data.items.map((item) => {
        if (String((item && item.seriesId) || "").trim() !== seriesId) return item;
        const favoriteAvatars = Array.isArray(item.favoriteAvatars)
          ? item.favoriteAvatars.filter((avatar) => avatar !== url)
          : [];
        return { ...item, favoriteAvatars };
      });
      this.setData({ items });
    },

    openSessions(event) {
      const seriesId = String(event.currentTarget.dataset.seriesId || "").trim();
      if (!seriesId || typeof wx === "undefined" || typeof wx.navigateTo !== "function") return;
      wx.navigateTo({
        url: `/pages/session-collection/index?series_id=${encodeURIComponent(seriesId)}`,
      });
    },

    async removeFavorite(event) {
      if (this._favoriteMutation) return;
      let seriesId;
      try {
        seriesId = canonicalUUID(event.currentTarget.dataset.seriesId, "活动系列");
      } catch (error) {
        this.setData({ errorMessage: getUserMessage(error, "收藏记录无效") });
        return;
      }
      const title = String(event.currentTarget.dataset.title || "").trim();
      const version = Number(this._mutationVersion || 0) + 1;
      this._mutationVersion = version;
      this._favoriteMutation = true;
      try {
        const confirmed = await confirmRemoval(title);
        if (!confirmed || !this._active || version !== this._mutationVersion) return;
        this.setData({ removingSeriesId: seriesId, errorMessage: "", successMessage: "" });
        await api.setSeriesFavorite(seriesId, false);
        if (!this._active || version !== this._mutationVersion) return;
        await this.loadFavorites(false);
        if (!this._active || version !== this._mutationVersion) return;
        this.setData({
          successMessage: this.data.errorMessage
            ? "取消收藏已提交，最新列表尚未确认"
            : "已取消收藏",
        });
      } catch (error) {
        if (!this._active || version !== this._mutationVersion) return;
        if (Number(error && error.statusCode) === 401) {
          this.setData({ needsLogin: true });
        }
        this.setData({ errorMessage: getUserMessage(error, "取消收藏失败，请稍后重试") });
      } finally {
        if (version === this._mutationVersion) {
          this._favoriteMutation = false;
          if (this._active) this.setData({ removingSeriesId: "" });
        }
      }
    },
  };
}

function mergeFavorites(current, incoming) {
  const seen = new Set();
  return [
    ...(Array.isArray(current) ? current : []),
    ...(Array.isArray(incoming) ? incoming : []),
  ].filter((item) => {
    const id = String((item && item.seriesId) || "");
    if (!id || seen.has(id)) return false;
    seen.add(id);
    return true;
  });
}

if (typeof Page === "function") Page(createMyFavoritesPageDefinition());

module.exports = {
  confirmUnfavorite,
  createMyFavoritesPageDefinition,
  mergeFavorites,
};
