"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const { createSessionCollectionPageDefinition } = require("./index");

const INSTANCE_ID = "11111111-1111-4111-8111-111111111111";
const SESSION_A = "22222222-2222-4222-8222-222222222222";
const SESSION_B = "33333333-3333-4333-8333-333333333333";
const SERIES_ID = "44444444-4444-4444-8444-444444444444";
const SESSION_C = "55555555-5555-4555-8555-555555555555";

function flush() {
  return new Promise((resolve) => setImmediate(resolve));
}

test("Session collection renders every server choice and navigates exact Session", async (t) => {
  const navigations = [];
  global.wx = { navigateTo: ({ url }) => navigations.push(url) };
  t.after(() => delete global.wx);
  const page = createSessionCollectionPageDefinition({
    async getInstanceSessions(instanceId) {
      assert.equal(instanceId, INSTANCE_ID);
      return {
        action: "session_selection_required",
        instance_id: INSTANCE_ID,
        sessions: [
          {
            session_id: SESSION_A,
            session_title: "上午工作坊",
            status: "published",
            session_start_at: "2026-10-01T10:00:00Z",
          },
          {
            session_id: SESSION_B,
            session_title: "下午工作坊",
            status: "published",
            session_start_at: "2026-10-01T12:00:00Z",
          },
        ],
      };
    },
  });
  page.setData = (changes) => {
    page.data = { ...page.data, ...changes };
  };
  page.onLoad({ instance_id: INSTANCE_ID });
  await flush();
  assert.deepEqual(
    page.data.collection.sessions.map((session) => session.sessionId),
    [SESSION_A, SESSION_B],
  );
  assert.equal(page.data.collection.action, "session_selection_required");
  assert.equal(page.data.collection.directSessionId, "");
  page.openSession({ currentTarget: { dataset: { sessionId: SESSION_B } } });
  assert.equal(navigations[0], `/pages/session-detail/index?session_id=${SESSION_B}`);
});

test("Session collection routes a single favorite Session directly", async (t) => {
  let instanceCalls = 0;
  const redirects = [];
  global.wx = { redirectTo: ({ url }) => redirects.push(url) };
  t.after(() => delete global.wx);
  const page = createSessionCollectionPageDefinition({
    async getInstanceSessions() {
      instanceCalls += 1;
      return {};
    },
    async getSeriesSessions(seriesId) {
      assert.equal(seriesId, SERIES_ID);
      return {
        action: "session_detail",
        series_id: SERIES_ID,
        instance_id: INSTANCE_ID,
        direct_session_id: SESSION_A,
        sessions: [
          {
            session_id: SESSION_A,
            session_title: "唯一场次",
            status: "published",
            session_start_at: "2026-10-01T10:00:00Z",
          },
        ],
      };
    },
  });
  page.setData = (changes) => {
    page.data = { ...page.data, ...changes };
  };

  page.onLoad({ series_id: SERIES_ID });
  await flush();

  assert.equal(instanceCalls, 0);
  assert.equal(page.data.collection.seriesId, SERIES_ID);
  assert.equal(page.data.heading.title, "选择收藏活动场次");
  assert.deepEqual(redirects, [`/pages/session-detail/index?session_id=${SESSION_A}`]);
});

test("Session collection exposes archived review resources through an exact Session", async (t) => {
  const navigations = [];
  global.wx = { navigateTo: ({ url }) => navigations.push(url) };
  t.after(() => delete global.wx);
  const page = createSessionCollectionPageDefinition({
    async getInstanceSessions(instanceId) {
      assert.equal(instanceId, INSTANCE_ID);
      return {
        action: "session_selection_required",
        series_id: SERIES_ID,
        instance_id: INSTANCE_ID,
        sessions: [
          {
            session_id: SESSION_A,
            session_title: "上午回顾资料",
            status: "archived",
            session_start_at: "2026-10-01T10:00:00Z",
          },
          {
            session_id: SESSION_B,
            session_title: "下午回顾资料",
            status: "ended",
            session_start_at: "2026-10-01T12:00:00Z",
          },
          {
            session_id: SESSION_C,
            session_title: "已取消场次",
            status: "cancelled",
            session_start_at: "2026-10-01T14:00:00Z",
          },
        ],
      };
    },
  });
  page.setData = (changes) => {
    page.data = { ...page.data, ...changes };
  };

  page.onLoad({ review_instance_id: INSTANCE_ID });
  await flush();
  assert.deepEqual(
    page.data.collection.sessions.map((session) => session.sessionId),
    [SESSION_A, SESSION_B],
  );
  assert.equal(page.data.collection.sessions[0].statusLabel, "已归档");
  page.openSession({ currentTarget: { dataset: { sessionId: SESSION_B } } });

  assert.equal(page.data.heading.title, "选择场次资料");
  assert.deepEqual(navigations, [
    `/pages/activity-review/index?instance_id=${INSTANCE_ID}&session_id=${SESSION_B}`,
  ]);
});

test("Session collection does not auto-open a cancelled review Session", async (t) => {
  const redirects = [];
  global.wx = { redirectTo: ({ url }) => redirects.push(url) };
  t.after(() => delete global.wx);
  const page = createSessionCollectionPageDefinition({
    async getInstanceSessions() {
      return {
        action: "session_detail",
        instance_id: INSTANCE_ID,
        direct_session_id: SESSION_C,
        sessions: [
          {
            session_id: SESSION_C,
            session_title: "已取消场次",
            status: "cancelled",
            session_start_at: "2026-10-01T14:00:00Z",
          },
        ],
      };
    },
  });
  page.setData = (changes) => {
    page.data = { ...page.data, ...changes };
  };

  page.onLoad({ review_instance_id: INSTANCE_ID });
  await flush();

  assert.deepEqual(redirects, []);
  assert.equal(page.data.collection.action, "unavailable");
  assert.equal(page.data.collection.directSessionId, "");
  assert.deepEqual(page.data.collection.sessions, []);
  assert.equal(page.data.emptyMessage, "本期暂时没有可查看的场次资料");
});

test("Session collection directly opens the sole reviewable Session after filtering", async (t) => {
  const redirects = [];
  global.wx = { redirectTo: ({ url }) => redirects.push(url) };
  t.after(() => delete global.wx);
  const page = createSessionCollectionPageDefinition({
    async getInstanceSessions() {
      return {
        action: "session_selection_required",
        instance_id: INSTANCE_ID,
        sessions: [
          {
            session_id: SESSION_A,
            session_title: "已结束场次",
            status: "ended",
            session_start_at: "2026-10-01T10:00:00Z",
          },
          {
            session_id: SESSION_C,
            session_title: "已取消场次",
            status: "cancelled",
            session_start_at: "2026-10-01T14:00:00Z",
          },
        ],
      };
    },
  });
  page.setData = (changes) => {
    page.data = { ...page.data, ...changes };
  };

  page.onLoad({ review_instance_id: INSTANCE_ID });
  await flush();

  assert.equal(page.data.collection.action, "session_detail");
  assert.equal(page.data.collection.directSessionId, SESSION_A);
  assert.deepEqual(
    page.data.collection.sessions.map((session) => session.sessionId),
    [SESSION_A],
  );
  assert.deepEqual(redirects, [
    `/pages/activity-review/index?instance_id=${INSTANCE_ID}&session_id=${SESSION_A}`,
  ]);
});

test("Instance collection follows a review-only Session target", async (t) => {
  const redirects = [];
  global.wx = { redirectTo: ({ url }) => redirects.push(url) };
  t.after(() => delete global.wx);
  const page = createSessionCollectionPageDefinition({
    async getInstanceSessions() {
      return {
        action: "session_detail",
        instance_id: INSTANCE_ID,
        direct_session_id: SESSION_A,
        sessions: [
          {
            session_id: SESSION_A,
            session_title: "回顾资料",
            status: "ended",
            session_start_at: "2026-10-01T10:00:00Z",
            review_path: `/api/v1/xiangwan/instances/${INSTANCE_ID}/review?session_id=${SESSION_A}`,
          },
        ],
      };
    },
  });
  page.setData = (changes) => {
    page.data = { ...page.data, ...changes };
  };

  page.onLoad({ instance_id: INSTANCE_ID });
  await flush();

  assert.deepEqual(redirects, [
    `/pages/activity-review/index?instance_id=${INSTANCE_ID}&session_id=${SESSION_A}`,
  ]);
});

test("Session collection fails closed when route identity is ambiguous or absent", () => {
  for (const options of [
    {},
    { instance_id: INSTANCE_ID, series_id: SERIES_ID },
    { instance_id: INSTANCE_ID, review_instance_id: INSTANCE_ID },
  ]) {
    const page = createSessionCollectionPageDefinition();
    page.setData = (changes) => {
      page.data = { ...page.data, ...changes };
    };
    page.onLoad(options);
    assert.equal(page.data.loading, false);
    assert.equal(page.data.errorMessage, "场次链接无效");
    assert.equal(page.data.canRetry, false);
  }
});
