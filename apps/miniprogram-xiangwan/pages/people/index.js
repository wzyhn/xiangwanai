"use strict";

const { projectPeoplePage } = require("../../features/people/model");
const { canonicalUUID, xiangwanApi } = require("../../services/xiangwan-api");
const { getUserMessage } = require("../../utils/errors");

function createPeoplePageDefinition(api = xiangwanApi) {
  return {
    data: {
      items: [],
      nextCursor: "",
      asOf: "",
      loading: true,
      loadingMore: false,
      errorMessage: "",
      emptyMessage: "",
    },

    onLoad() {
      this._active = true;
      void this.loadPeople(false);
    },

    onShow() {
      this._active = true;
    },

    onUnload() {
      this._active = false;
      this._requestVersion = Number(this._requestVersion || 0) + 1;
    },

    onPullDownRefresh() {
      return this.loadPeople(false);
    },

    onReachBottom() {
      if (this.data.nextCursor && !this.data.loadingMore) void this.loadPeople(true);
    },

    async loadPeople(append) {
      if (append && (!this.data.nextCursor || this.data.loadingMore)) return;
      const version = Number(this._requestVersion || 0) + 1;
      this._requestVersion = version;
      this.setData(
        append
          ? { loadingMore: true, errorMessage: "" }
          : { loading: true, errorMessage: "", emptyMessage: "" },
      );
      try {
        const page = projectPeoplePage(
          await api.getPeople({ cursor: append ? this.data.nextCursor : "", limit: 20 }),
        );
        if (!this._active || version !== this._requestVersion) return;
        const items = append ? mergePeople(this.data.items, page.items) : page.items;
        this.setData({
          items,
          nextCursor: page.nextCursor,
          asOf: page.asOf,
          emptyMessage: items.length ? "" : "暂时还没有公开的社区人物",
        });
      } catch (error) {
        if (!this._active || version !== this._requestVersion) return;
        if (append && Number(error && error.statusCode) === 409) {
          this.setData({ nextCursor: "" });
          return this.loadPeople(false);
        }
        this.setData({ errorMessage: getUserMessage(error, "社区人物加载失败，请稍后重试") });
      } finally {
        if (this._active && version === this._requestVersion) {
          this.setData({ loading: false, loadingMore: false });
        }
        if (typeof wx !== "undefined" && typeof wx.stopPullDownRefresh === "function") {
          wx.stopPullDownRefresh();
        }
      }
    },

    retry() {
      void this.loadPeople(false);
    },

    openPerson(event) {
      let peopleId;
      try {
        peopleId = canonicalUUID(event.currentTarget.dataset.peopleId, "社区人物");
      } catch (error) {
        this.setData({ errorMessage: getUserMessage(error, "人物链接无效") });
        return;
      }
      if (typeof wx === "undefined" || typeof wx.navigateTo !== "function") return;
      wx.navigateTo({
        url: `/pages/person-detail/index?people_id=${encodeURIComponent(peopleId)}`,
      });
    },

    onShareAppMessage() {
      return { title: "享玩 AI 社区人物", path: "/pages/people/index" };
    },
  };
}

function mergePeople(current, incoming) {
  const seen = new Set();
  return [
    ...(Array.isArray(current) ? current : []),
    ...(Array.isArray(incoming) ? incoming : []),
  ].filter((person) => {
    const id = String((person && person.peopleId) || "");
    if (!id || seen.has(id)) return false;
    seen.add(id);
    return true;
  });
}

if (typeof Page === "function") Page(createPeoplePageDefinition());

module.exports = { createPeoplePageDefinition, mergePeople };
