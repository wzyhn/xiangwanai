"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const { createMyFavoritesPageDefinition, mergeFavorites } = require("./index");

const SERIES_ID = "44444444-4444-4444-8444-444444444444";

function flush() {
  return new Promise((resolve) => setImmediate(resolve));
}

function attachPageRuntime(page) {
  page.data = { ...page.data };
  page.setData = (changes) => {
    page.data = { ...page.data, ...changes };
  };
}

function favorite(overrides = {}) {
  return {
    series_id: SERIES_ID,
    title: "AI 圆桌",
    series_status: "active",
    favorite_count: 8,
    favorited_at: "2026-09-15T08:00:00Z",
    sessions_available: true,
    ...overrides,
  };
}

test("My Favorites waits for session hydration without triggering login", async (t) => {
  let resolveRuntime;
  let loginCalls = 0;
  let reads = 0;
  const app = {
    runtimeReady: new Promise((resolve) => {
      resolveRuntime = resolve;
    }),
    weconqAuth: { hasValidSession: () => true },
    async ensureAuthenticated() {
      loginCalls += 1;
    },
  };
  global.getApp = () => app;
  t.after(() => delete global.getApp);
  const page = createMyFavoritesPageDefinition({
    async getMyFavorites() {
      reads += 1;
      return { items: [], as_of: "2026-09-15T09:00:00Z" };
    },
  });
  attachPageRuntime(page);
  page.onLoad();
  page.onShow();
  assert.equal(reads, 0);

  resolveRuntime(true);
  await flush();
  await flush();

  assert.equal(reads, 1);
  assert.equal(loginCalls, 0);
  assert.equal(page.data.emptyMessage, "还没有收藏活动");
});

test("My Favorites opens the exact Series collection route", (t) => {
  const navigations = [];
  global.wx = { navigateTo: ({ url }) => navigations.push(url) };
  t.after(() => delete global.wx);
  const page = createMyFavoritesPageDefinition();
  page.openSessions({ currentTarget: { dataset: { seriesId: SERIES_ID } } });
  assert.deepEqual(navigations, [`/pages/session-collection/index?series_id=${SERIES_ID}`]);
});

test("My Favorites removes only a failed avatar from its Series card", () => {
  const page = createMyFavoritesPageDefinition();
  attachPageRuntime(page);
  page.data.items = [
    {
      seriesId: SERIES_ID,
      favoriteAvatars: ["https://cdn.example/a.webp", "https://cdn.example/b.webp"],
    },
    {
      seriesId: "55555555-5555-4555-8555-555555555555",
      favoriteAvatars: ["https://cdn.example/a.webp"],
    },
  ];

  page.onFavoriteAvatarError({
    currentTarget: { dataset: { seriesId: SERIES_ID, url: "https://cdn.example/a.webp" } },
  });

  assert.deepEqual(page.data.items[0].favoriteAvatars, ["https://cdn.example/b.webp"]);
  assert.deepEqual(page.data.items[1].favoriteAvatars, ["https://cdn.example/a.webp"]);
});

test("My Favorites confirms exact DELETE then reloads authoritative state", async () => {
  let reads = 0;
  const mutations = [];
  const page = createMyFavoritesPageDefinition(
    {
      async getMyFavorites() {
        reads += 1;
        return {
          items: reads === 1 ? [favorite()] : [],
          as_of: "2026-09-15T09:00:00Z",
        };
      },
      async setSeriesFavorite(seriesId, favorited) {
        mutations.push({ seriesId, favorited });
        return { series_id: seriesId, favorited };
      },
    },
    async () => true,
  );
  attachPageRuntime(page);
  page._active = true;
  await page.loadFavorites(false);

  await page.removeFavorite({
    currentTarget: { dataset: { seriesId: SERIES_ID, title: "AI 圆桌" } },
  });

  assert.deepEqual(mutations, [{ seriesId: SERIES_ID, favorited: false }]);
  assert.equal(reads, 2);
  assert.deepEqual(page.data.items, []);
  assert.equal(page.data.successMessage, "已取消收藏");
});

test("My Favorites keeps cards when mutation fails and merge is stable", async () => {
  const page = createMyFavoritesPageDefinition(
    {
      async setSeriesFavorite() {
        throw new Error("offline");
      },
    },
    async () => true,
  );
  attachPageRuntime(page);
  page._active = true;
  page.data.items = [{ seriesId: SERIES_ID }];

  await page.removeFavorite({
    currentTarget: { dataset: { seriesId: SERIES_ID, title: "AI 圆桌" } },
  });

  assert.deepEqual(page.data.items, [{ seriesId: SERIES_ID }]);
  assert.match(page.data.errorMessage, /取消收藏失败/);
  assert.deepEqual(
    mergeFavorites(
      [{ seriesId: "one", title: "old" }],
      [{ seriesId: "one", title: "new" }, { seriesId: "two" }],
    ),
    [{ seriesId: "one", title: "old" }, { seriesId: "two" }],
  );
});

test("My Favorites keeps confirmation and DELETE single-flight", async () => {
  let releaseConfirmation;
  let confirmations = 0;
  let mutations = 0;
  const page = createMyFavoritesPageDefinition(
    {
      async setSeriesFavorite() {
        mutations += 1;
      },
      async getMyFavorites() {
        return { items: [], as_of: "2026-09-15T09:00:00Z" };
      },
    },
    async () => {
      confirmations += 1;
      return new Promise((resolve) => {
        releaseConfirmation = resolve;
      });
    },
  );
  attachPageRuntime(page);
  page._active = true;

  const first = page.removeFavorite({
    currentTarget: { dataset: { seriesId: SERIES_ID, title: "AI 圆桌" } },
  });
  const second = page.removeFavorite({
    currentTarget: { dataset: { seriesId: SERIES_ID, title: "AI 圆桌" } },
  });
  await flush();
  releaseConfirmation(true);
  await Promise.all([first, second]);

  assert.equal(confirmations, 1);
  assert.equal(mutations, 1);
});
