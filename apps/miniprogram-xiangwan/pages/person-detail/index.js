"use strict";

const { projectPersonDetail } = require("../../features/people/model");
const { canonicalUUID, xiangwanApi } = require("../../services/xiangwan-api");
const { getUserMessage } = require("../../utils/errors");

function createPersonDetailPageDefinition(api = xiangwanApi) {
  return {
    data: {
      loading: true,
      errorMessage: "",
      person: null,
    },

    onLoad(options = {}) {
      this._active = true;
      try {
        this._peopleId = canonicalUUID(options.people_id, "社区人物");
      } catch (error) {
        this.setData({ loading: false, errorMessage: getUserMessage(error, "人物链接无效") });
        return;
      }
      this._skipNextShowRefresh = true;
      void this.loadPerson();
    },

    onShow() {
      this._active = true;
      if (this._skipNextShowRefresh) {
        this._skipNextShowRefresh = false;
        return;
      }
      if (this._peopleId) void this.loadPerson();
    },

    onHide() {
      this._active = false;
      this._requestVersion = Number(this._requestVersion || 0) + 1;
    },

    onUnload() {
      this._active = false;
      this._requestVersion = Number(this._requestVersion || 0) + 1;
    },

    onPullDownRefresh() {
      if (!this._peopleId) {
        if (typeof wx !== "undefined" && typeof wx.stopPullDownRefresh === "function") {
          wx.stopPullDownRefresh();
        }
        return Promise.resolve();
      }
      return this.loadPerson();
    },

    async loadPerson() {
      const version = Number(this._requestVersion || 0) + 1;
      this._requestVersion = version;
      this.setData({ loading: true, errorMessage: "" });
      try {
        const person = projectPersonDetail(await api.getPerson(this._peopleId));
        if (!this._active || version !== this._requestVersion) return;
        this.setData({ person });
      } catch (error) {
        if (this._active && version === this._requestVersion) {
          this.setData({
            person: Number(error && error.statusCode) === 404 ? null : this.data.person,
            errorMessage: getUserMessage(error, "人物详情加载失败，请稍后重试"),
          });
        }
      } finally {
        if (this._active && version === this._requestVersion) this.setData({ loading: false });
        if (typeof wx !== "undefined" && typeof wx.stopPullDownRefresh === "function") {
          wx.stopPullDownRefresh();
        }
      }
    },

    retry() {
      if (this._peopleId) void this.loadPerson();
    },

    onShareAppMessage() {
      const person = this.data.person;
      return {
        title: person ? `${person.displayName} · 享玩 AI` : "享玩 AI 社区人物",
        path: this._peopleId
          ? `/pages/person-detail/index?people_id=${encodeURIComponent(this._peopleId)}`
          : "/pages/people/index",
      };
    },
  };
}

if (typeof Page === "function") Page(createPersonDetailPageDefinition());

module.exports = { createPersonDetailPageDefinition };
