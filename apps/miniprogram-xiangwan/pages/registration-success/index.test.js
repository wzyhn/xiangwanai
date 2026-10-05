"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const {
  createRegistrationSuccessPageDefinition,
  optionalRegistrationId,
  sessionTimeText,
} = require("./index");

const REGISTRATION_ID = "22222222-2222-4222-8222-222222222222";

function createPage(options) {
  const page = createRegistrationSuccessPageDefinition();
  page.setData = (changes) => {
    page.data = { ...page.data, ...changes };
  };
  page.onLoad(options);
  return page;
}

test("success page renders the chained session facts", () => {
  const page = createPage({
    registration_id: REGISTRATION_ID,
    title: "AI 共创夜",
    session_title: "周末场",
    start_at: "2026-09-20T11:00:00Z",
    end_at: "2026-09-20T13:00:00Z",
    venue: "和平区某共创空间",
  });

  assert.equal(page._registrationId, REGISTRATION_ID);
  assert.equal(page.data.sessionTitle, "AI 共创夜");
  assert.equal(page.data.sessionLabel, "周末场");
  assert.equal(page.data.sessionTime, "09-20 19:00 至 09-20 21:00");
  assert.equal(page.data.venue, "和平区某共创空间");
});

test("missing session facts degrade to hidden card rows", () => {
  const page = createPage({ registration_id: REGISTRATION_ID });
  assert.equal(page.data.sessionTitle, "");
  assert.equal(page.data.sessionLabel, "");
  assert.equal(page.data.sessionTime, "");
  assert.equal(page.data.venue, "");
  assert.equal(sessionTimeText("not-a-time", "2026-09-20T11:00:00Z"), "");
  assert.equal(sessionTimeText("2026-09-20T11:00:00Z", ""), "");
  assert.equal(optionalRegistrationId("not-a-uuid"), "");
});

test("session title can populate the success card when the legacy title is absent", () => {
  const page = createPage({ registration_id: REGISTRATION_ID, session_title: "周末场" });
  assert.equal(page.data.sessionTitle, "周末场");
  assert.equal(page.data.sessionLabel, "周末场");
});

test("primary action relaunches the registration detail with the same id", (t) => {
  const navigations = [];
  global.wx = {
    reLaunch(options) {
      navigations.push({ method: "reLaunch", url: options.url });
    },
  };
  t.after(() => {
    delete global.wx;
  });
  const page = createPage({ registration_id: REGISTRATION_ID });

  page.openRegistrationDetail();

  assert.deepEqual(navigations, [
    {
      method: "reLaunch",
      url: `/pages/registration-detail/index?registration_id=${REGISTRATION_ID}`,
    },
  ]);
});

test("a missing registration id falls back to my registrations", (t) => {
  const navigations = [];
  global.wx = {
    reLaunch(options) {
      navigations.push(options.url);
    },
  };
  t.after(() => {
    delete global.wx;
  });
  const page = createPage({ registration_id: "not-a-uuid" });

  page.openRegistrationDetail();

  assert.deepEqual(navigations, ["/pages/my-registrations/index"]);
});

test("secondary action switches back to the home tab", (t) => {
  const calls = [];
  global.wx = {
    switchTab(options) {
      calls.push({ method: "switchTab", url: options.url });
    },
  };
  t.after(() => {
    delete global.wx;
  });
  const page = createPage({ registration_id: REGISTRATION_ID });

  page.backToHome();

  assert.deepEqual(calls, [{ method: "switchTab", url: "/pages/index/index" }]);
});
