"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const { createContactSetupPageDefinition } = require("./index");

function policies(privacy = "privacy-v1", contact = "contact-v1", ready = true) {
  return {
    published_versions: [
      { kind: "privacy", version: privacy },
      { kind: "manual_contact", version: contact },
    ],
    capabilities: {
      privacy_notice_available: ready,
      manual_registration_contact_available: ready,
    },
    manual_registration_contact_policy: { version: contact, content: "用于活动报名通知" },
  };
}

function harness(api) {
  let contact = {
    configured: false,
    nickname: "",
    phone_e164: "",
    version: 0,
    principal_profile_etag: "etag-initial",
    privacy_policy_version: "",
    contact_policy_version: "",
  };
  const page = createContactSetupPageDefinition({
    getRegistrationContact: async () => ({ ...contact }),
    updateRegistrationContact: async (payload) => {
      contact = {
        ...payload,
        configured: true,
        version: contact.version + 1,
        principal_profile_etag: "etag-saved",
      };
      return { ...contact };
    },
    ...api,
  });
  page.setData = (changes) => {
    page.data = { ...page.data, ...changes };
  };
  return page;
}

test("contact save reconciles a lost response before unlocking login and never blindly resubmits", async (t) => {
  let contact = {
    configured: false,
    nickname: "",
    phone_e164: "",
    version: 0,
    principal_profile_etag: "initial",
  };
  let writes = 0,
    completes = 0;
  global.wx = { openPrivacyContract() {}, navigateBack() {} };
  global.getApp = () => ({
    getXiangwanAuthBinding: () => ({ principalId: "user", generation: 1 }),
    getContactSetupId: () => 1,
    completeContactSetup: () => {
      completes++;
      return true;
    },
  });
  t.after(() => {
    delete global.wx;
    delete global.getApp;
  });
  const page = harness({
    getPublicPolicies: async () => policies(),
    getRegistrationContact: async () => ({ ...contact }),
    updateRegistrationContact: async (body) => {
      writes++;
      contact = { ...body, configured: true, version: 1 };
      throw new Error("lost response");
    },
  });
  page.onLoad({ setup_id: "1" });
  await new Promise((resolve) => setImmediate(resolve));
  page.updateName({ detail: { value: "测试用户" } });
  page.updatePhone({ detail: { value: "13800000000" } });
  page.updateAcknowledgement({ detail: { value: ["acknowledged"] } });
  await page.save();
  assert.equal(writes, 1);
  assert.equal(completes, 1);
  assert.equal(page._completed, true);
});

test("contact save failure keeps mandatory onboarding open even when optional fields were edited", async (t) => {
  let completed = false;
  global.wx = { openPrivacyContract() {} };
  global.getApp = () => ({
    getXiangwanAuthBinding: () => ({ principalId: "user", generation: 1 }),
    getContactSetupId: () => 1,
    completeContactSetup: () => {
      completed = true;
      return true;
    },
  });
  t.after(() => {
    delete global.wx;
    delete global.getApp;
  });
  const page = harness({
    getPublicPolicies: async () => policies(),
    updateRegistrationContact: async () => {
      throw new Error("write rejected");
    },
  });
  page.onLoad({ setup_id: "1" });
  await new Promise((resolve) => setImmediate(resolve));
  page.updateName({ detail: { value: "测试用户" } });
  page.updatePhone({ detail: { value: "13800000000" } });
  page.updateAcknowledgement({ detail: { value: ["acknowledged"] } });
  page._extensionInputEdited = true;
  await page.saveContact(true);
  assert.equal(completed, false);
  assert.equal(page._completed, false);
  assert.match(page.data.formError, /保存失败/);
  assert.equal(page.data.optionalError, "");
});

test("onboarding waits for published privacy policy before showing contact fields", async (t) => {
  global.wx = { openPrivacyContract() {} };
  global.getApp = () => ({
    getXiangwanAuthBinding: () => ({ principalId: "user", generation: 1 }),
    getContactSetupId: () => 1,
  });
  t.after(() => {
    delete global.wx;
    delete global.getApp;
  });
  const page = harness({
    getPublicPolicies: async () => policies("privacy-v1", "contact-v1", false),
  });
  page.onLoad({ setup_id: "1" });
  await new Promise((resolve) => setImmediate(resolve));
  assert.match(page.data.blockedMessage, /微信隐私保护指引/);
  assert.equal(page.data.contactPolicyText, "");
  assert.equal(page.data.privacyVersion, "");
});

test("onboarding requires nickname, phone and acknowledgement and normalizes phone for registration", async (t) => {
  const saved = [];
  let navigated = false;
  const binding = { principalId: "user", generation: 1 };
  global.wx = {
    openPrivacyContract() {},
    navigateBack: () => {
      navigated = true;
    },
  };
  global.getApp = () => ({
    getXiangwanAuthBinding: () => binding,
    getContactSetupId: () => 1,
    completeContactSetup: (value) => {
      saved.push(value);
      return true;
    },
  });
  t.after(() => {
    delete global.wx;
    delete global.getApp;
  });
  const page = harness({ getPublicPolicies: async () => policies() });
  page.onLoad({ setup_id: "1" });
  await new Promise((resolve) => setImmediate(resolve));
  await page.save();
  assert.match(page.data.formError, /必填/);
  page.updateName({ detail: { value: "小鱼" } });
  page.updatePhone({ detail: { value: "138 1234 5678" } });
  page.updateAcknowledgement({ detail: { value: ["acknowledged"] } });
  await page.save();
  assert.deepEqual(saved, [{ contactName: "小鱼", contactPhone: "+8613812345678" }]);
  assert.equal(navigated, true);
  assert.equal(page._completed, true);
});

test("policy revision during onboarding requires a fresh acknowledgement", async (t) => {
  let calls = 0;
  let saved = false;
  global.wx = { openPrivacyContract() {} };
  global.getApp = () => ({
    getXiangwanAuthBinding: () => ({ principalId: "user", generation: 1 }),
    getContactSetupId: () => 1,
    completeContactSetup: () => {
      saved = true;
      return true;
    },
  });
  t.after(() => {
    delete global.wx;
    delete global.getApp;
  });
  const page = harness({
    getPublicPolicies: async () => (++calls === 1 ? policies() : policies("privacy-v2")),
  });
  page.onLoad({ setup_id: "1" });
  await new Promise((resolve) => setImmediate(resolve));
  page.updateName({ detail: { value: "小鱼" } });
  page.updatePhone({ detail: { value: "13812345678" } });
  page.updateAcknowledgement({ detail: { value: ["acknowledged"] } });
  await page.save();
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(saved, false);
  assert.equal(page.data.acknowledged, false);
  assert.equal(page.data.privacyVersion, "privacy-v2");
});

test("optional tags and introduction use a private moderated profile candidate and retain the operation key after an unknown result", async (t) => {
  let operation = null;
  const calls = [];
  let attempts = 0;
  const app = {
    getXiangwanAuthBinding: () => ({ principalId: "user", generation: 1 }),
    getContactSetupId: () => 1,
    getProfileExtensionOperation: () => operation,
    setProfileExtensionOperation: (value) => {
      operation = value;
      return value;
    },
    clearProfileExtensionOperation: () => {
      operation = null;
    },
    completeContactSetup: () => true,
  };
  global.getApp = () => app;
  global.wx = { openPrivacyContract() {}, navigateBack() {} };
  t.after(() => {
    delete global.wx;
    delete global.getApp;
  });
  const page = harness({
    getPublicPolicies: async () => policies(),
    getMyProfile: async () => ({
      profile: {
        version: 0,
        occupation: "",
        introduction: "",
        tags: [],
        visibility: { occupation: false, introduction: false, tags: false },
      },
    }),
    newIdempotencyKey: () => "33333333-3333-4333-8333-333333333333",
    updateProfileExtension: async (payload, key) => {
      calls.push({ payload, key });
      attempts += 1;
      if (attempts === 1) throw new Error("connection lost");
      return { candidate_version: 1, moderation_status: "pending_review" };
    },
  });
  page.onLoad({ setup_id: "1" });
  await new Promise((resolve) => setImmediate(resolve));
  page.updateName({ detail: { value: "小鱼" } });
  page.updatePhone({ detail: { value: "13812345678" } });
  page.updateIntroduction({ detail: { value: "喜欢徒步" } });
  page.updateTags({ detail: { value: "徒步，咖啡" } });
  page.updateAcknowledgement({ detail: { value: ["acknowledged"] } });
  await page.save();
  assert.ok(operation);
  assert.match(page.data.optionalError, /提交失败/);
  await page.save();
  assert.equal(operation, null);
  assert.equal(page._completed, true);
  assert.equal(calls.length, 2);
  assert.deepEqual(calls[0], calls[1]);
  assert.deepEqual(calls[0].payload.visibility, {
    occupation: false,
    introduction: false,
    tags: false,
  });
});

test("account switch while checking policy stops optional profile and contact writes", async (t) => {
  let policyCalls = 0;
  let releasePolicy;
  let writes = 0;
  let completes = 0;
  let binding = { principalId: "user-a", generation: 1 };
  global.getApp = () => ({
    getXiangwanAuthBinding: () => binding,
    getContactSetupId: () => 1,
    getProfileExtensionOperation: () => null,
    completeContactSetup: () => {
      completes += 1;
      return true;
    },
  });
  global.wx = { openPrivacyContract() {} };
  t.after(() => {
    delete global.wx;
    delete global.getApp;
  });
  const page = harness({
    getPublicPolicies: () => {
      policyCalls += 1;
      if (policyCalls === 1) return Promise.resolve(policies());
      return new Promise((resolve) => {
        releasePolicy = resolve;
      });
    },
    getMyProfile: async () => ({
      profile: {
        version: 0,
        occupation: "",
        introduction: "",
        tags: [],
        visibility: { occupation: false, introduction: false, tags: false },
      },
    }),
    updateProfileExtension: async () => {
      writes += 1;
    },
  });
  page.onLoad({ setup_id: "1" });
  await new Promise((resolve) => setImmediate(resolve));
  page.updateName({ detail: { value: "小鱼" } });
  page.updatePhone({ detail: { value: "13812345678" } });
  page.updateIntroduction({ detail: { value: "简介" } });
  page.updateAcknowledgement({ detail: { value: ["acknowledged"] } });
  const save = page.save();
  await new Promise((resolve) => setImmediate(resolve));
  binding = { principalId: "user-b", generation: 2 };
  releasePolicy(policies());
  await save;
  assert.equal(writes, 0);
  assert.equal(completes, 0);
  assert.match(page.data.formError, /登录状态已变化/);
});
