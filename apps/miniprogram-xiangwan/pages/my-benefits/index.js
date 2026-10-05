"use strict";

const { projectMyBenefits } = require("../../features/benefits/model");
const { xiangwanApi } = require("../../services/xiangwan-api");
const { getUserMessage } = require("../../utils/errors");

function resolveApp() {
  if (typeof getApp !== "function") return null;
  try {
    return getApp();
  } catch (_error) {
    return null;
  }
}

function createMyBenefitsPageDefinition(api = xiangwanApi) {
  return {
    data: {
      benefits: null,
      loading: false,
      needsLogin: false,
      errorMessage: "",
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
        void this.loadBenefits();
        return;
      }
      this.setData({ benefits: null, loading: false, needsLogin: true });
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
        if (typeof wx !== "undefined" && typeof wx.stopPullDownRefresh === "function") {
          wx.stopPullDownRefresh();
        }
        return Promise.resolve();
      }
      return this.loadBenefits();
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
        await this.loadBenefits();
      } catch (error) {
        if (this._active) {
          this.setData({ errorMessage: getUserMessage(error, "登录未完成，请稍后重试") });
        }
      } finally {
        if (this._active) this.setData({ loading: false });
      }
    },

    async loadBenefits() {
      const version = Number(this._requestVersion || 0) + 1;
      this._requestVersion = version;
      this.setData({ loading: true, errorMessage: "" });
      try {
        const benefits = projectMyBenefits(await api.getMyBenefits());
        if (!this._active || version !== this._requestVersion) return;
        this.setData({ benefits });
      } catch (error) {
        if (!this._active || version !== this._requestVersion) return;
        const statusCode = Number(error && error.statusCode);
        const changes = {
          errorMessage: getUserMessage(error, "身份与贡献加载失败，请稍后重试"),
        };
        if (statusCode === 401 || statusCode === 403) {
          changes.benefits = null;
          changes.needsLogin = statusCode === 401;
        }
        this.setData(changes);
      } finally {
        if (this._active && version === this._requestVersion) this.setData({ loading: false });
        if (typeof wx !== "undefined" && typeof wx.stopPullDownRefresh === "function") {
          wx.stopPullDownRefresh();
        }
      }
    },

    retry() {
      if (this.data.needsLogin) void this.loginAndLoad();
      else void this.loadBenefits();
    },
    openHostApplication() {
      if (typeof wx !== "undefined") wx.navigateTo({ url: "/pages/host-application/index" });
    },
    openPeopleBinding() {
      if (typeof wx !== "undefined") wx.navigateTo({ url: "/pages/people-binding/index" });
    },
  };
}

if (typeof Page === "function") Page(createMyBenefitsPageDefinition());

module.exports = { createMyBenefitsPageDefinition };
