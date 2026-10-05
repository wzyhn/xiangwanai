"use strict";
const test = require("node:test"),
  assert = require("node:assert/strict");
const { createPeopleBindingPageDefinition } = require("./index");
const code = "11111111-1111-4111-8111-111111111111." + "a".repeat(43);
const profile = {
  people_profile_id: "22222222-2222-4222-8222-222222222222",
  display_name: "人物",
  introduction: "公开简介",
  profile_version: 2,
  expires_at: "2026-10-03T00:00:00Z",
};
function setup(t, api = {}) {
  let owner = { principalId: "owner", generation: 1 };
  const old = global.getApp;
  global.getApp = () => ({
    ensureAuthenticated: async () => {},
    getXiangwanAuthBinding: () => owner,
  });
  t.after(() => {
    global.getApp = old;
  });
  const page = createPeopleBindingPageDefinition(
    { previewPeopleBinding: async () => profile, ...api },
    () => "33333333-3333-4333-8333-333333333333",
  );
  page.setData = (patch) => {
    page.data = { ...page.data, ...patch };
  };
  page.onLoad();
  page.onCodeInput({ detail: { value: code } });
  return {
    page,
    changeOwner: () => {
      owner = { principalId: "other", generation: 2 };
    },
  };
}
test("binding requires a successful preview and explicit consent before writing", async (t) => {
  let writes = 0;
  const { page } = setup(t, {
    acceptPeopleBinding: async () => {
      writes++;
      return { people_profile_id: profile.people_profile_id, status: "active", duplicate: false };
    },
  });
  await page.confirmBinding();
  assert.equal(writes, 0);
  await page.loadPreview();
  await page.confirmBinding();
  assert.equal(writes, 0);
  page.onConsentChange({ detail: { value: ["consent"] } });
  await page.confirmBinding();
  assert.equal(writes, 1);
  assert.equal(page.data.code, "");
  assert.equal(page.data.preview, null);
  assert.match(page.data.notice, /已确认/);
});
test("unknown confirmation reuses code, version and operation and blocks altered input", async (t) => {
  const calls = [];
  const { page } = setup(t, {
    acceptPeopleBinding: async (payload, operation) => {
      calls.push({ payload, operation });
      if (calls.length === 1) throw new Error("receipt lost");
      return { people_profile_id: profile.people_profile_id, status: "active", duplicate: true };
    },
  });
  await page.loadPreview();
  page.onConsentChange({ detail: { value: ["consent"] } });
  await page.confirmBinding();
  assert.equal(page.data.pending, true);
  page.onCodeInput({ detail: { value: "changed" } });
  page.onConsentChange({ detail: { value: [] } });
  assert.equal(page.data.code, code);
  await page.confirmBinding();
  assert.deepEqual(calls[0], calls[1]);
  assert.equal(page.data.pending, false);
});
test("account change after preview prevents identity writes", async (t) => {
  let writes = 0;
  const { page, changeOwner } = setup(t, {
    acceptPeopleBinding: async () => {
      writes++;
    },
  });
  await page.loadPreview();
  page.onConsentChange({ detail: { value: ["consent"] } });
  changeOwner();
  await page.confirmBinding();
  assert.equal(writes, 0);
  assert.equal(page.data.preview, null);
});
test("hidden pages clear invitations and discard late private preview responses", async (t) => {
  let resolve;
  const { page } = setup(t, {
    previewPeopleBinding: () =>
      new Promise((done) => {
        resolve = done;
      }),
  });
  const pending = page.loadPreview();
  await new Promise((done) => setImmediate(done));
  page.onHide();
  resolve(profile);
  await pending;
  assert.equal(page.data.code, "");
  assert.equal(page.data.preview, null);
  assert.equal(page._pending, null);
});
test("repeated confirm taps cannot create parallel operations", async (t) => {
  let calls = 0,
    resolve;
  const { page } = setup(t, {
    acceptPeopleBinding: () => {
      calls++;
      return new Promise((done) => {
        resolve = done;
      });
    },
  });
  await page.loadPreview();
  page.onConsentChange({ detail: { value: ["consent"] } });
  const pending = page.confirmBinding();
  await page.confirmBinding();
  assert.equal(calls, 1);
  resolve({ people_profile_id: profile.people_profile_id, status: "active", duplicate: false });
  await pending;
});
