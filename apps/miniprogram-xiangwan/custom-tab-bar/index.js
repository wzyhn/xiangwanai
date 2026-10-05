"use strict";

const TABS = [
  { value: "pages/index/index", label: "首页", icon: "home" },
  { value: "pages/past-activities/index", label: "往期活动", icon: "history" },
  { value: "pages/my/index", label: "我的", icon: "user" },
];

function createTabBarDefinition() {
  return {
    data: { value: TABS[0].value, tabs: TABS, switching: false },
    lifetimes: {
      attached() {
        this.syncSelection();
      },
    },
    pageLifetimes: {
      show() {
        this.syncSelection();
      },
    },
    methods: {
      syncSelection() {
        const pages = typeof getCurrentPages === "function" ? getCurrentPages() : [];
        const page = pages[pages.length - 1];
        if (page && TABS.some((tab) => tab.value === page.route)) {
          this.setData({ value: page.route, switching: false });
        }
      },
      changeTab(event) {
        const value = String(
          (event &&
            event.currentTarget &&
            event.currentTarget.dataset &&
            event.currentTarget.dataset.value) ||
            (event && event.detail && event.detail.value) ||
            "",
        );
        if (
          this.data.switching ||
          value === this.data.value ||
          !TABS.some((tab) => tab.value === value)
        )
          return;
        if (typeof wx === "undefined" || typeof wx.switchTab !== "function") return;
        this.setData({ switching: true });
        wx.switchTab({
          url: `/${value}`,
          success: () => this.setData({ value }),
          fail: () => {
            this.syncSelection();
            if (typeof wx.showToast === "function")
              wx.showToast({ title: "页面暂时无法打开，请重试", icon: "none" });
          },
          complete: () => this.setData({ switching: false }),
        });
      },
    },
  };
}

if (typeof Component === "function") Component(createTabBarDefinition());
module.exports = { TABS, createTabBarDefinition };
