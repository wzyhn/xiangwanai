"use strict";

const { syncTabBar } = require("../../utils/tab-bar");

const { projectHomePage } = require("../../features/catalog/model");
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
]);
const AREA_FILTERS = Object.freeze([
  { value: "all", label: "全部" },
  { value: "heping", label: "和平区" },
  { value: "hexi", label: "河西区" },
  { value: "nankai", label: "南开区" },
  { value: "hebei", label: "河北区" },
  { value: "hedong", label: "河东区" },
  { value: "hongqiao", label: "红桥区" },
  { value: "dongli", label: "东丽区" },
  { value: "wuqing", label: "武清区" },
  { value: "binhai", label: "滨海" },
  { value: "online", label: "线上" },
]);
const TIME_FILTERS = Object.freeze([
  { value: "all", label: "全部时间" },
  { value: "this_week", label: "本周" },
  { value: "next_week", label: "下周" },
  { value: "local_date", label: "指定日期" },
]);

function filterLabel(options, value, fallback) {
  const current = options.find((option) => option.value === value);
  return current ? current.label : fallback;
}

function formatLocalDateLabel(value) {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(String(value || ""));
  return match ? `${Number(match[2])}月${Number(match[3])}日` : "指定日期";
}

function todayInShanghai(now = new Date()) {
  const current = now instanceof Date ? now : new Date(now);
  if (Number.isNaN(current.getTime())) return "";
  return new Date(current.getTime() + 8 * 60 * 60 * 1000).toISOString().slice(0, 10);
}

function pickerLocalDate(value) {
  if (typeof value === "string" && /^\d{4}-\d{2}-\d{2}$/.test(value)) return value;
  const timestamp = Array.isArray(value) ? Number(value[0]) : Number(value);
  if (!Number.isFinite(timestamp)) return "";
  const date = new Date(timestamp);
  if (Number.isNaN(date.getTime())) return "";
  // 日历的时间戳都是 +08:00 日界(min/max/选中值都按 T00:00:00+08:00 构造);
  // 必须按上海时区解包,否则非 UTC+8 设备上会偏一天。
  return new Date(date.getTime() + 8 * 60 * 60 * 1000).toISOString().slice(0, 10);
}

function selectedValue(event) {
  const detailValue = event && event.detail && event.detail.value;
  if (detailValue !== undefined && detailValue !== null) return detailValue;
  return event && event.currentTarget && event.currentTarget.dataset
    ? event.currentTarget.dataset.value
    : "";
}

function createHomePageDefinition(api = xiangwanApi) {
  const today = todayInShanghai();
  const calendarMinDate = new Date(`${today}T00:00:00+08:00`).getTime();
  return {
    data: {
      activityFilters: ACTIVITY_FILTERS,
      areaFilters: AREA_FILTERS,
      timeFilters: TIME_FILTERS,
      filter: {
        activityType: "all",
        area: "all",
        timeWindow: "all",
        localDate: "",
        quickTags: [],
        limit: 20,
      },
      activityFilterLabel: "全部",
      areaFilterLabel: "全部",
      timeFilterLabel: "全部时间",
      activeFilterMenu: "",
      today,
      calendarVisible: false,
      calendarValue: calendarMinDate,
      calendarMinDate,
      calendarMaxDate: calendarMinDate + 366 * 24 * 60 * 60 * 1000,
      communityName: "",
      brandIntro: "",
      heroMode: "text",
      heroEyebrow: "",
      heroSubtitle: "",
      heroImageUrl: "",
      heroImageAlt: "",
      heroImageFailed: false,
      quickTags: [],
      cards: [],
      openCount: null,
      nextCursor: "",
      loading: true,
      loadingMore: false,
      errorMessage: "",
      emptyMessage: "",
    },

    onLoad() {
      this._active = true;
      void this.loadHome(false);
    },

    onUnload() {
      this._active = false;
      this._requestVersion = Number(this._requestVersion || 0) + 1;
    },

    onPullDownRefresh() {
      return this.loadHome(false);
    },

    onReachBottom() {
      if (this.data.nextCursor && !this.data.loadingMore) {
        void this.loadHome(true);
      }
    },

    async loadHome(append) {
      if (append && (!this.data.nextCursor || this.data.loadingMore)) return;
      const version = Number(this._requestVersion || 0) + 1;
      this._requestVersion = version;
      this.setData(
        append
          ? { loadingMore: true, errorMessage: "" }
          : { loading: true, errorMessage: "", emptyMessage: "" },
      );
      const filter = {
        ...this.data.filter,
        cursor: append ? this.data.nextCursor : "",
      };
      try {
        const page = projectHomePage(await api.getHomeSessions(filter), {
          apiBaseUrl: currentApiBaseUrl(),
          now: Date.now(),
        });
        if (!this._active || version !== this._requestVersion) return;
        const cards = append ? mergeCardsBySession(this.data.cards, page.cards) : page.cards;
        this.setData({
          communityName: page.communityName,
          brandIntro: page.brandIntro,
          heroMode: page.heroMode,
          heroEyebrow: page.heroEyebrow,
          heroSubtitle: page.heroSubtitle,
          heroImageUrl: page.heroImageUrl,
          heroImageAlt: page.heroImageAlt,
          heroImageFailed: false,
          openCount: page.openCount,
          quickTags: page.quickTags.map((tag) => ({
            ...tag,
            selected: this.data.filter.quickTags.includes(tag.code),
          })),
          cards,
          nextCursor: page.nextCursor,
          emptyMessage: cards.length ? "" : "暂时没有符合条件的活动",
        });
      } catch (error) {
        if (!this._active || version !== this._requestVersion) return;
        if (append && Number(error && error.statusCode) === 409) {
          this.setData({ nextCursor: "" });
          return this.loadHome(false);
        }
        this.setData({
          errorMessage: getUserMessage(error, "活动列表加载失败，请稍后重试"),
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
      this.updateFilter("activityType", selectedValue(event));
    },

    selectArea(event) {
      this.updateFilter("area", selectedValue(event));
    },

    selectTime(event) {
      const value = selectedValue(event);
      if (value === "local_date") {
        // 打开日历时同步收起筛选菜单:取消选择后菜单不应停留在展开态。
        this.setData({ calendarVisible: true, activeFilterMenu: "" });
        return;
      }
      this.updateFilter("timeWindow", value);
    },

    selectLocalDate(event) {
      const rawValue = event && event.detail ? event.detail.value : "";
      const localDate = pickerLocalDate(rawValue);
      if (!/^\d{4}-\d{2}-\d{2}$/.test(localDate)) return;
      if (
        this.data.filter.timeWindow === "local_date" &&
        this.data.filter.localDate === localDate
      ) {
        this.setData({ activeFilterMenu: "", calendarVisible: false });
        return;
      }
      this.setData({
        "filter.timeWindow": "local_date",
        "filter.localDate": localDate,
        timeFilterLabel: formatLocalDateLabel(localDate),
        timeFilters: TIME_FILTERS.map((option) =>
          option.value === "local_date"
            ? { ...option, label: formatLocalDateLabel(localDate) }
            : option,
        ),
        activeFilterMenu: "",
        calendarVisible: false,
        calendarValue: new Date(`${localDate}T00:00:00+08:00`).getTime(),
      });
      void this.loadHome(false);
    },

    closeDatePicker() {
      this.setData({ calendarVisible: false });
    },

    toggleFilterMenu(event) {
      const key = String(event.currentTarget.dataset.key || "").trim();
      if (!new Set(["activity", "area", "time"]).has(key)) return;
      this.setData({ activeFilterMenu: this.data.activeFilterMenu === key ? "" : key });
    },

    toggleQuickTag(event) {
      const code = String(event.currentTarget.dataset.code || "").trim();
      if (!code) return;
      const selected = new Set(this.data.filter.quickTags);
      if (selected.has(code)) selected.delete(code);
      else if (selected.size < 5) selected.add(code);
      this.setData({ "filter.quickTags": [...selected] });
      void this.loadHome(false);
    },

    clearQuickTags() {
      if (!this.data.filter.quickTags.length) return;
      this.setData({ "filter.quickTags": [] });
      void this.loadHome(false);
    },

    updateFilter(key, value) {
      const normalized = String(value || "").trim();
      if (!normalized) return;
      if (this.data.filter[key] === normalized) {
        this.setData({ activeFilterMenu: "" });
        return;
      }
      const changes = {
        [`filter.${key}`]: normalized,
        activeFilterMenu: "",
      };
      if (key === "activityType") {
        changes.activityFilterLabel = filterLabel(ACTIVITY_FILTERS, normalized, "全部");
      } else if (key === "area") {
        changes.areaFilterLabel = filterLabel(AREA_FILTERS, normalized, "全部");
      } else if (key === "timeWindow") {
        changes["filter.localDate"] = "";
        changes.timeFilterLabel = filterLabel(TIME_FILTERS, normalized, "全部时间");
      }
      this.setData(changes);
      void this.loadHome(false);
    },

    retry() {
      void this.loadHome(false);
    },

    openPeople() {
      if (typeof wx === "undefined" || typeof wx.navigateTo !== "function") return;
      wx.navigateTo({ url: "/pages/people/index" });
    },

    onHeroImageError() {
      this.setData({ heroImageFailed: true });
    },

    onCoverImageError(event) {
      const sessionId = String(event.currentTarget.dataset.sessionId || "").trim();
      if (!sessionId) return;
      const index = this.data.cards.findIndex((card) => card.sessionId === sessionId);
      if (index < 0 || !this.data.cards[index].coverImageUrl) return;
      this.setData({ [`cards[${index}].coverImageUrl`]: "" });
    },

    openDetail(event) {
      const sessionId = String(event.currentTarget.dataset.sessionId || "").trim();
      if (!sessionId || typeof wx === "undefined" || typeof wx.navigateTo !== "function") return;
      wx.navigateTo({
        url: `/pages/session-detail/index?session_id=${encodeURIComponent(sessionId)}`,
      });
    },

    onSocialAvatarError(event) {
      const { sessionId, kind, url } = event.currentTarget.dataset;
      if (!["participantAvatars", "favoriteAvatars"].includes(kind)) return;
      const cards = this.data.cards.map((card) =>
        card.sessionId === sessionId
          ? { ...card, [kind]: card[kind].filter((avatar) => avatar !== url) }
          : card,
      );
      this.setData({ cards });
    },

    onShareAppMessage() {
      return { title: this.data.communityName || "享玩 AI", path: "/pages/index/index" };
    },
  };
}

function mergeCardsBySession(current, incoming) {
  const seen = new Set();
  return [
    ...(Array.isArray(current) ? current : []),
    ...(Array.isArray(incoming) ? incoming : []),
  ].filter((card) => {
    const id = String((card && card.sessionId) || "");
    if (!id || seen.has(id)) return false;
    seen.add(id);
    return true;
  });
}

if (typeof Page === "function") Page(createHomePageDefinition());

module.exports = {
  createHomePageDefinition,
  filterLabel,
  formatLocalDateLabel,
  mergeCardsBySession,
  pickerLocalDate,
  todayInShanghai,
};
