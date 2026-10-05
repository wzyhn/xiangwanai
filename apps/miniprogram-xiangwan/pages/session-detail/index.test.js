"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const { createSessionDetailPageDefinition } = require("./index");

const SESSION_ID = "11111111-1111-4111-8111-111111111111";
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

test("session detail refreshes on foreground without duplicating its initial request", async () => {
  let calls = 0;
  const page = createSessionDetailPageDefinition({
    async getSessionDetail() {
      calls += 1;
      return {
        session_id: SESSION_ID,
        session_title: calls === 1 ? "第一版" : "服务端更新版",
        price_cents: 0,
        delivery: { mode: "online" },
        display: {},
        cta: {},
      };
    },
  });
  attachPageRuntime(page);

  page.onLoad({ session_id: SESSION_ID });
  page.onShow();
  await flush();
  await flush();
  assert.equal(calls, 1, "initial onShow must not duplicate the onLoad request");

  page.onShow();
  await flush();
  await flush();

  assert.equal(calls, 2);
  assert.equal(page.data.detail.title, "服务端更新版");
});

test("session detail rechecks authority before opening a registration form", async (t) => {
  const navigations = [];
  let calls = 0;
  global.wx = {
    navigateTo(options) {
      navigations.push(options);
    },
  };
  t.after(() => delete global.wx);
  const page = createSessionDetailPageDefinition({
    async getSessionDetail() {
      calls += 1;
      return {
        session_id: SESSION_ID,
        registration_start_at: "2026-09-10T07:00:00Z",
        registration_end_at: calls === 1 ? "2999-09-19T07:00:00Z" : "2026-09-19T07:00:00Z",
        session_start_at: "2999-09-20T07:01:00Z",
        session_end_at: "2999-09-21T07:01:00Z",
        price_cents: 0,
        display: { state: "open", registration_allowed: true, sellable_capacity: 3 },
        cta: { action: "start_registration", label: "register_now", enabled: true },
      };
    },
  });
  attachPageRuntime(page);

  page.onLoad({ session_id: SESSION_ID });
  await flush();
  await flush();
  assert.equal(page.data.detail.registrationReady, true);

  await page.startRegistration();

  assert.equal(calls, 2);
  assert.deepEqual(navigations, []);
  assert.equal(page.data.detail.state, "closed");
  assert.equal(page.data.detail.registrationNotice, "报名已截止，活动尚未开始。");
});

test("session detail finds an existing registration across pages and opens its detail", async (t) => {
  const navigations = [];
  global.getApp = () => ({
    weconqAuth: { hasValidSession: () => true },
  });
  global.wx = {
    navigateTo(options) {
      navigations.push(options);
    },
  };
  t.after(() => {
    delete global.getApp;
    delete global.wx;
  });
  let registrationCalls = 0;
  const page = createSessionDetailPageDefinition({
    async getSessionDetail() {
      return {
        session_id: SESSION_ID,
        price_cents: 0,
        delivery: { mode: "online" },
        display: { state: "open", registration_allowed: true },
        cta: { action: "start_registration", enabled: true },
      };
    },
    async getMyRegistrations({ cursor }) {
      registrationCalls += 1;
      if (!cursor) return { items: [], next_cursor: "page-2" };
      return {
        items: [
          {
            registration_id: "66666666-6666-4666-8666-666666666666",
            session_id: SESSION_ID,
            state: "registered",
          },
        ],
        next_cursor: "",
      };
    },
  });
  attachPageRuntime(page);
  page.onLoad({ session_id: SESSION_ID });
  await flush();
  await flush();
  assert.equal(registrationCalls, 2);
  assert.equal(page.data.alreadyRegistered, true);
  assert.equal(page.data.detail.ctaLabel, "已报名");
  await page.startRegistration();
  assert.deepEqual(navigations, []);
  page.openExistingRegistration();
  assert.deepEqual(navigations, [
    {
      url: "/pages/registration-detail/index?registration_id=66666666-6666-4666-8666-666666666666",
    },
  ]);
});

test("session detail opens an authoritative offline coordinate in WeChat maps", (t) => {
  const calls = [];
  global.wx = {
    openLocation(options) {
      calls.push(options);
    },
  };
  t.after(() => {
    delete global.wx;
  });
  const page = createSessionDetailPageDefinition({});
  attachPageRuntime(page);
  page.data.detail = {
    canOpenLocation: true,
    latitude: 39.1,
    longitude: 117.2,
    venue: "海河实验室",
    title: "AI 共创夜",
    address: "天津市河西区创新路 18 号",
  };

  page.openLocation();

  assert.deepEqual(calls, [
    {
      latitude: 39.1,
      longitude: 117.2,
      name: "海河实验室",
      address: "天津市河西区创新路 18 号",
    },
  ]);
});

test("session detail degrades failed media and routes previous review by instance id", (t) => {
  const navigations = [];
  global.wx = {
    navigateTo(options) {
      navigations.push(options);
    },
  };
  t.after(() => {
    delete global.wx;
  });
  const page = createSessionDetailPageDefinition({});
  attachPageRuntime(page);
  page.data.detail = {
    coverImageUrl: "https://api.example.com/api/v1/xiangwan/covers/c-1.webp",
    leaders: [
      {
        displayName: "陆远",
        avatarUrl: "https://api.example.com/api/v1/xiangwan/avatars/lu.webp",
      },
    ],
    contentBlocks: [
      { type: "image", imageUrl: "https://api.example.com/api/v1/xiangwan/content/c-1.webp" },
    ],
    previousReview: {
      instanceId: SESSION_ID,
      imageUrls: [
        "https://api.example.com/api/v1/xiangwan/reviews/r-1.webp",
        "https://api.example.com/api/v1/xiangwan/reviews/r-2.webp",
      ],
    },
  };

  page.onDetailCoverError();
  assert.equal(page.data.detail.coverImageUrl, "");
  assert.equal(page.data.detailCoverFailed, true);

  page.onLeaderAvatarError({ currentTarget: { dataset: { index: 0 } } });
  assert.equal(page.data.detail.leaders[0].avatarUrl, "");
  page.onLeaderAvatarError({ currentTarget: { dataset: { index: 9 } } });
  assert.equal(page.data.detail.leaders.length, 1, "out-of-range avatar errors are ignored");

  page.onContentBlockImageError({ currentTarget: { dataset: { blockIndex: 0 } } });
  assert.equal(page.data.detail.contentBlocks[0].imageUrl, "");

  page.onPreviousReviewImageError({ currentTarget: { dataset: { reviewIndex: 0 } } });
  assert.deepEqual(page.data.detail.previousReview.imageUrls, [
    "https://api.example.com/api/v1/xiangwan/reviews/r-2.webp",
  ]);

  page.openPreviousReview();
  assert.deepEqual(navigations, [
    { url: `/pages/activity-review/index?instance_id=${SESSION_ID}` },
  ]);
  page.data.detail.previousReview.sessionId = "96dd41ac-e53c-4d31-bd2c-1f1248a9d540";
  page.openPreviousReview();
  assert.equal(
    navigations[1].url,
    `/pages/activity-review/index?instance_id=${SESSION_ID}&session_id=96dd41ac-e53c-4d31-bd2c-1f1248a9d540`,
  );
});

test("session detail authenticates only after an explicit favorite action", async (t) => {
  let loginCalls = 0;
  const favoriteCalls = [];
  global.getApp = () => ({
    runtimeReady: Promise.resolve(),
    async ensureAuthenticated() {
      loginCalls += 1;
    },
  });
  t.after(() => delete global.getApp);
  const page = createSessionDetailPageDefinition({
    async getSessionDetail() {
      return {
        series_id: SERIES_ID,
        session_id: SESSION_ID,
        session_title: "AI 共创夜",
        price_cents: 0,
        delivery: { mode: "online" },
        display: {},
        cta: {},
      };
    },
    async setSeriesFavorite(seriesId, favorited) {
      favoriteCalls.push({ seriesId, favorited });
      return { series_id: seriesId, favorited };
    },
  });
  attachPageRuntime(page);

  page.onLoad({ session_id: SESSION_ID });
  await flush();
  assert.equal(loginCalls, 0, "anonymous detail load must not trigger login");

  await page.favoriteSeries();

  assert.equal(loginCalls, 1);
  assert.deepEqual(favoriteCalls, [{ seriesId: SERIES_ID, favorited: true }]);
  assert.equal(page.data.favoriteSaved, true);
});
