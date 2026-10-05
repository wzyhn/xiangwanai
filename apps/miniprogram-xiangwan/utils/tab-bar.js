"use strict";

function syncTabBar(page, route) {
  if (!page || typeof page.getTabBar !== "function") return;
  const tabBar = page.getTabBar();
  if (tabBar && typeof tabBar.setData === "function") {
    tabBar.setData({ value: route, switching: false });
  }
}

module.exports = { syncTabBar };
