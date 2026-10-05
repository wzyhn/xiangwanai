"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const { createActivityReviewPageDefinition } = require("./index");

const INSTANCE_ID = "11111111-1111-4111-8111-111111111111";
const NEXT_INSTANCE_ID = "22222222-2222-4222-8222-222222222222";
const SESSION_ID = "33333333-3333-4333-8333-333333333333";
const BLOCK_ID = "44444444-4444-4444-8444-444444444444";

function flush() {
  return new Promise((resolve) => setImmediate(resolve));
}

test("activity review loads exact Instance and preserves explicit next-Session choice", async (t) => {
  const calls = [];
  const navigations = [];
  global.getApp = () => ({ globalData: { apiBaseUrl: "https://api.example.com" } });
  global.wx = {
    navigateTo: ({ url }) => navigations.push(url),
    stopPullDownRefresh() {},
  };
  t.after(() => {
    delete global.getApp;
    delete global.wx;
  });
  const page = createActivityReviewPageDefinition({
    async getPublicReview(instanceId, sessionId) {
      calls.push({ instanceId, sessionId });
      return {
        instance_id: instanceId,
        instance_title: "第一期",
        content_blocks: [{ type: "text", title: "简介", body: "本期内容" }],
        documents: [],
        next_instance: {
          action: "session_selection_required",
          instance_id: NEXT_INSTANCE_ID,
          candidate_session_ids: ["a", "b"],
        },
      };
    },
  });
  page.setData = (changes) => {
    page.data = { ...page.data, ...changes };
  };
  page.onLoad({ instance_id: INSTANCE_ID });
  await flush();
  page.openNextInstance();
  page.openSessionResources();

  assert.deepEqual(calls, [{ instanceId: INSTANCE_ID, sessionId: "" }]);
  assert.equal(navigations[0], `/pages/session-collection/index?instance_id=${NEXT_INSTANCE_ID}`);
  assert.equal(navigations[1], `/pages/session-collection/index?review_instance_id=${INSTANCE_ID}`);
  assert.equal(page.data.showSessionResources, true);
  assert.deepEqual(page.data.review.contentBlocks, [
    { type: "text", title: "简介", body: "本期内容", isText: true, isImage: false },
  ]);
});

test("activity review rejects a malformed deep link before dispatch", () => {
  let calls = 0;
  const page = createActivityReviewPageDefinition({
    async getPublicReview() {
      calls += 1;
      return {};
    },
  });
  page.setData = (changes) => {
    page.data = { ...page.data, ...changes };
  };
  page.onLoad({ instance_id: "not-an-instance" });
  assert.equal(calls, 0);
  assert.equal(page.data.loading, false);
  assert.match(page.data.errorMessage, /无效/);
});

test("activity review routes external links by trusted block identity", (t) => {
  const navigations = [];
  global.wx = {
    navigateTo(options) {
      navigations.push(options.url);
    },
  };
  t.after(() => {
    delete global.wx;
  });
  const page = createActivityReviewPageDefinition();
  page._active = true;
  page._instanceId = INSTANCE_ID;
  page._sessionId = SESSION_ID;
  page.setData = (changes) => {
    page.data = { ...page.data, ...changes };
  };

  page.openExternalResource({ currentTarget: { dataset: { blockId: BLOCK_ID } } });

  assert.deepEqual(navigations, [
    `/pages/external-resource/index?instance_id=${INSTANCE_ID}&block_id=${BLOCK_ID}&session_id=${SESSION_ID}`,
  ]);
});

test("activity review reports file download failures instead of failing silently", (t) => {
  global.getApp = () => ({ globalData: { apiBaseUrl: "https://api.example.com" } });
  global.wx = {
    downloadFile({ fail }) {
      fail(new Error("network unavailable"));
    },
  };
  t.after(() => {
    delete global.getApp;
    delete global.wx;
  });
  const page = createActivityReviewPageDefinition();
  page._active = true;
  page.setData = (changes) => {
    page.data = { ...page.data, ...changes };
  };

  page.openMediaResource({
    currentTarget: {
      dataset: {
        url: "https://api.example.com/api/v1/xiangwan/media/11111111-1111-4111-8111-111111111111/22222222-2222-4222-8222-222222222222/33333333-3333-4333-8333-333333333333",
      },
    },
  });

  assert.equal(page.data.resourceErrorMessage, "文件下载失败，请稍后重试");
});

test("activity review removes a failed hero image and keeps the text review", () => {
  const page = createActivityReviewPageDefinition();
  page._active = true;
  page.data.review = { instanceTitle: "第一期", heroImageUrl: "https://cdn.example/hero.webp" };
  page.setData = (changes) => {
    page.data = { ...page.data, ...changes };
  };

  page.handleMediaError({ currentTarget: { dataset: { mediaKind: "hero" } } });

  assert.equal(page.data.review.heroImageUrl, "");
  assert.equal(page.data.resourceErrorMessage, "回顾封面暂不可用，已切换为文字内容。");
});
