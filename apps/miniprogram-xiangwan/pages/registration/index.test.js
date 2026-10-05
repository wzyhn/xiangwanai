"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const { questionnaireFieldsForView } = require("../../features/registration/model");
const {
  createRegistrationPageDefinition,
  policyVersionsRequireAcknowledgementReset,
  registrationPolicyBlockMessage,
  reusableDraftForSession,
  sameRegistrationPayload,
} = require("./index");
const { createXiangwanAppDefinition, handleXiangwanAuthSessionChange } = require("../../app");

const TEXT_FIELD_ID = "11111111-1111-4111-8111-111111111111";
const CHOICE_FIELD_ID = "22222222-2222-4222-8222-222222222222";

function pageHarness() {
  const page = createRegistrationPageDefinition({});
  page.data = {
    ...page.data,
    fields: questionnaireFieldsForView({
      fields: [
        {
          field_id: TEXT_FIELD_ID,
          type: "single_line",
          options: [],
        },
        {
          field_id: CHOICE_FIELD_ID,
          type: "multiple_choice",
          options: [
            { code: "builder", label: "开发者" },
            { code: "designer", label: "设计师" },
          ],
        },
      ],
    }),
  };
  page._answers = Object.create(null);
  page.setData = (changes) => {
    page.data = { ...page.data, ...changes };
  };
  return page;
}

test("questionnaire input remains controlled while field errors are cleared", () => {
  const page = pageHarness();
  page.data.fields[0].error = "此项为必填";
  page.updateTextAnswer({
    currentTarget: { dataset: { fieldId: TEXT_FIELD_ID } },
    detail: { value: "我想学习智能体" },
  });
  assert.equal(page._answers[TEXT_FIELD_ID], "我想学习智能体");
  assert.equal(page.data.fields[0].answerText, "我想学习智能体");
  assert.equal(page.data.fields[0].error, "");

  page.updateChoiceAnswer({
    currentTarget: { dataset: { fieldId: CHOICE_FIELD_ID } },
    detail: { value: ["builder"] },
  });
  assert.deepEqual(page._answers[CHOICE_FIELD_ID], ["builder"]);
  assert.equal(page.data.fields[1].options[0].checked, true);
  assert.equal(page.data.fields[1].options[1].checked, false);

  page.updateContactAcknowledgement({ detail: { value: ["acknowledged"] } });
  assert.equal(page.data.contactAcknowledged, true);
});

test("unchanged drafts retain their operation identity but edited payloads do not", () => {
  const previous = {
    sessionId: TEXT_FIELD_ID,
    idempotencyKey: "33333333-3333-4333-8333-333333333333",
    payload: { contact: { name: "甲" }, answers: [] },
  };
  const app = { getRegistrationDraft: () => previous };
  assert.equal(reusableDraftForSession(app, TEXT_FIELD_ID), previous);
  assert.equal(
    sameRegistrationPayload(previous.payload, { contact: { name: "甲" }, answers: [] }),
    true,
  );
  assert.equal(
    sameRegistrationPayload(previous.payload, { contact: { name: "乙" }, answers: [] }),
    false,
  );
});

test("a refreshed contact policy requires a fresh acknowledgement without resetting other input", () => {
  const versions = (privacy, contact) => ({
    published_versions: [
      { kind: "privacy", version: privacy },
      { kind: "manual_contact", version: contact },
    ],
  });
  assert.equal(
    policyVersionsRequireAcknowledgementReset(
      true,
      versions("privacy-v1", "contact-v1"),
      versions("privacy-v1", "contact-v2"),
    ),
    true,
  );
  assert.equal(
    policyVersionsRequireAcknowledgementReset(
      true,
      versions("privacy-v1", "contact-v2"),
      versions("privacy-v2", "contact-v2"),
    ),
    true,
  );
  assert.equal(
    policyVersionsRequireAcknowledgementReset(
      false,
      versions("privacy-v1", "contact-v1"),
      versions("privacy-v2", "contact-v2"),
    ),
    false,
  );
});

test("registration policy blocking explains the missing customer configuration", () => {
  const message = registrationPolicyBlockMessage({
    published_versions: [{ kind: "privacy", version: "privacy-v1" }],
    capabilities: {
      privacy_notice_available: true,
      manual_registration_contact_available: false,
    },
  });
  assert.match(message, /报名联系人政策/);
  assert.match(message, /重新检查/);
});

test("registration form does not authenticate or collect PII without a published privacy notice", async (t) => {
  let authenticationCalls = 0;
  global.getApp = () => ({
    async ensureAuthenticated() {
      authenticationCalls += 1;
    },
  });
  t.after(() => {
    delete global.getApp;
  });
  const page = createRegistrationPageDefinition({
    async getSessionDetail() {
      return {
        session_id: TEXT_FIELD_ID,
        price_cents: 0,
        delivery: {},
        display: { state: "open" },
        cta: { action: "start_registration", label: "register_now", enabled: true },
      };
    },
    async getPublicPolicies() {
      return {
        published_versions: [
          { kind: "privacy", version: "privacy-v1" },
          { kind: "manual_contact", version: "contact-v1" },
        ],
        capabilities: {
          privacy_notice_available: false,
          manual_registration_contact_available: true,
        },
        manual_registration_contact_policy: {
          version: "contact-v1",
          content: "联系人信息仅用于本次活动联络。",
        },
      };
    },
  });
  page.data = { ...page.data };
  page.setData = (changes) => {
    page.data = { ...page.data, ...changes };
  };
  page._active = true;
  page._sessionId = TEXT_FIELD_ID;

  await page.loadForm();

  assert.equal(authenticationCalls, 0);
  assert.equal(page._context, null);
  assert.match(page.data.blockedMessage, /隐私/);
});

test("registration form exposes both policy texts before authenticating for PII entry", async (t) => {
  let authenticationCalls = 0;
  let privacyOpenCalls = 0;
  global.getApp = () => ({
    async ensureAuthenticated() {
      authenticationCalls += 1;
    },
  });
  global.wx = {
    openPrivacyContract(options) {
      privacyOpenCalls += 1;
      options.success();
    },
  };
  t.after(() => {
    delete global.getApp;
    delete global.wx;
  });
  const page = createRegistrationPageDefinition({
    async getSessionDetail() {
      return {
        session_id: TEXT_FIELD_ID,
        publication_version: 1,
        price_cents: 0,
        delivery: {},
        display: { state: "open" },
        cta: { action: "start_registration", label: "register_now", enabled: true },
      };
    },
    async getPublicPolicies() {
      return {
        published_versions: [
          { kind: "privacy", version: "privacy-v1" },
          { kind: "manual_contact", version: "contact-v1" },
        ],
        capabilities: {
          privacy_notice_available: true,
          manual_registration_contact_available: true,
        },
        manual_registration_contact_policy: {
          version: "contact-v1",
          content: "联系人信息仅用于本次活动联络。",
        },
      };
    },
    async getSessionQuestionnaire() {
      const error = new Error("not found");
      error.statusCode = 404;
      throw error;
    },
  });
  page.data = { ...page.data };
  page.setData = (changes) => {
    page.data = { ...page.data, ...changes };
  };
  page._active = true;
  page._sessionId = TEXT_FIELD_ID;

  await page.loadForm();
  page.openPrivacyContract();

  assert.equal(authenticationCalls, 1);
  assert.equal(privacyOpenCalls, 1);
  assert.equal(page.data.blockedMessage, "");
  assert.equal(page.data.contactPolicyText, "联系人信息仅用于本次活动联络。");
});

test("contact fields appear after login while the questionnaire is still loading", async (t) => {
  let releaseQuestionnaire;
  let questionnaireRequested;
  const requested = new Promise((resolve) => {
    questionnaireRequested = resolve;
  });
  global.getApp = () => ({
    async ensureAuthenticated() {},
    getXiangwanAuthBinding() {
      return { principalId: TEXT_FIELD_ID, generation: 1 };
    },
  });
  global.wx = { openPrivacyContract() {} };
  t.after(() => {
    delete global.getApp;
    delete global.wx;
  });
  const page = createRegistrationPageDefinition({
    async getSessionDetail() {
      return {
        session_id: TEXT_FIELD_ID,
        publication_version: 1,
        instance_title: "本期活动",
        price_cents: 0,
        delivery: {},
        display: { state: "open" },
        cta: { action: "start_registration", label: "register_now", enabled: true },
      };
    },
    async getPublicPolicies() {
      return {
        published_versions: [
          { kind: "privacy", version: "privacy-v1" },
          { kind: "manual_contact", version: "contact-v1" },
        ],
        capabilities: {
          privacy_notice_available: true,
          manual_registration_contact_available: true,
        },
        manual_registration_contact_policy: {
          version: "contact-v1",
          content: "联系人信息仅用于本次活动联络。",
        },
      };
    },
    getSessionQuestionnaire() {
      questionnaireRequested();
      return new Promise((resolve) => {
        releaseQuestionnaire = resolve;
      });
    },
  });
  page.data = { ...page.data };
  page.setData = (changes) => {
    page.data = { ...page.data, ...changes };
  };
  page._active = true;
  page._sessionId = TEXT_FIELD_ID;

  const loading = page.loadForm();
  await requested;
  assert.equal(page.data.loading, false);
  assert.equal(page.data.questionnaireLoading, true);
  assert.equal(page.data.detail.title, "本期活动");
  assert.equal(page._context, null);
  page.updateContactName({ detail: { value: "小林" } });
  page.updateContactPhone({ detail: { value: "13812345678" } });
  page.continueToConfirmation();
  assert.equal(page.data.contactName, "小林");
  assert.equal(page.data.contactPhone, "13812345678");
  assert.equal(page._context, null);

  releaseQuestionnaire(null);
  await loading;
  assert.equal(page.data.questionnaireLoading, false);
  assert.ok(page._context);
  assert.equal(page.data.contactName, "小林");
});

test("a late questionnaire cannot retain contact input after an auth change", async (t) => {
  const app = {
    binding: { principalId: TEXT_FIELD_ID, generation: 1 },
    async ensureAuthenticated() {},
    getXiangwanAuthBinding() {
      return this.binding;
    },
  };
  global.getApp = () => app;
  global.wx = { openPrivacyContract() {} };
  t.after(() => {
    delete global.getApp;
    delete global.wx;
  });
  let releaseQuestionnaire;
  let questionnaireRequested;
  const requested = new Promise((resolve) => {
    questionnaireRequested = resolve;
  });
  const page = createRegistrationPageDefinition({
    async getSessionDetail() {
      return {
        session_id: TEXT_FIELD_ID,
        publication_version: 1,
        price_cents: 0,
        delivery: {},
        display: { state: "open" },
        cta: { action: "start_registration", label: "register_now", enabled: true },
      };
    },
    async getPublicPolicies() {
      return {
        published_versions: [
          { kind: "privacy", version: "privacy-v1" },
          { kind: "manual_contact", version: "contact-v1" },
        ],
        capabilities: {
          privacy_notice_available: true,
          manual_registration_contact_available: true,
        },
        manual_registration_contact_policy: {
          version: "contact-v1",
          content: "联系人信息仅用于本次活动联络。",
        },
      };
    },
    getSessionQuestionnaire() {
      questionnaireRequested();
      return new Promise((resolve) => {
        releaseQuestionnaire = resolve;
      });
    },
  });
  page.data = { ...page.data };
  page.setData = (changes) => {
    page.data = { ...page.data, ...changes };
  };
  page._active = true;
  page._sessionId = TEXT_FIELD_ID;

  const loading = page.loadForm();
  await requested;
  page.updateContactName({ detail: { value: "旧会话昵称" } });
  page.updateContactPhone({ detail: { value: "13812345678" } });
  app.binding = { principalId: TEXT_FIELD_ID, generation: 2 };
  releaseQuestionnaire(null);
  await loading;
  assert.equal(page.data.contactName, "");
  assert.equal(page.data.contactPhone, "");
  assert.equal(page.data.detail, null);
  assert.equal(page._context, null);
  assert.match(page.data.errorMessage, /登录状态已变化/);
});

test("contact validation exposes inline errors and clears them when edited", () => {
  const page = createRegistrationPageDefinition({
    newIdempotencyKey: () => "33333333-3333-4333-8333-333333333333",
  });
  page.data = { ...page.data, fields: [] };
  page.setData = (changes) => {
    page.data = { ...page.data, ...changes };
  };
  page._active = true;
  page._sessionId = TEXT_FIELD_ID;
  page._answers = Object.create(null);
  page._context = {
    detail: {
      session_id: TEXT_FIELD_ID,
      publication_version: 1,
      price_cents: 0,
      cta: { action: "start_registration", enabled: true },
    },
    policies: {
      published_versions: [
        { kind: "privacy", version: "privacy-v1" },
        { kind: "manual_contact", version: "contact-v1" },
      ],
      capabilities: {
        privacy_notice_available: true,
        manual_registration_contact_available: true,
      },
      manual_registration_contact_policy: {
        version: "contact-v1",
        content: "联系人信息仅用于本次活动联络。",
      },
    },
    questionnaire: null,
  };

  page.continueToConfirmation();
  assert.equal(page.data.contactNameError, "请输入 1–100 个字符的联系人姓名");
  assert.equal(page.data.contactPhoneError, "请输入有效的中国大陆手机号或国际号码");
  assert.equal(page.data.contactAcknowledgementError, "请先确认报名联系人信息的使用说明");

  page.updateContactName({ detail: { value: "小林" } });
  page.updateContactPhone({ detail: { value: "13812345678" } });
  page.updateContactAcknowledgement({ detail: { value: ["acknowledged"] } });
  assert.equal(page.data.contactNameError, "");
  assert.equal(page.data.contactPhoneError, "");
  assert.equal(page.data.contactAcknowledgementError, "");
});

test("registration reuses a same-process contact default without overwriting edits", () => {
  const page = createRegistrationPageDefinition({});
  page.data = { ...page.data };
  page.setData = (changes) => {
    page.data = { ...page.data, ...changes };
  };
  const app = {
    getRegistrationContactDefaults() {
      return {
        contactName: "上次报名姓名",
        contactPhone: "+8613812345678",
      };
    },
  };

  page.presentProfile(app);
  assert.equal(page.data.contactName, "上次报名姓名");
  assert.equal(page.data.contactPhone, "+8613812345678");

  page.presentProfile(app, { nickname: "最新资料昵称", avatarUrl: "", etag: "etag-2" });
  assert.equal(page.data.contactName, "上次报名姓名");

  page.updateContactName({ detail: { value: "本次改填昵称" } });
  page.presentProfile(app, { nickname: "再次更新的资料昵称", avatarUrl: "", etag: "etag-3" });
  assert.equal(page.data.contactName, "本次改填昵称");

  page.updateContactPhone({ detail: { value: "13900000000" } });
  page.presentProfile(app);
  assert.equal(page.data.contactPhone, "13900000000");

  page.clearContactName();
  page.presentProfile(app);
  assert.equal(page.data.contactName, "", "clearing the field opts out of the default");
});

test("registration page does not prefill a contact from a prior same-principal auth session", () => {
  const app = createXiangwanAppDefinition();
  const principalId = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa";
  app.globalData.principalId = principalId;
  app.getXiangwanAuthBinding();
  app.setRegistrationContactDefaults({
    contactName: "旧会话",
    contactPhone: "+8613812345678",
  });
  handleXiangwanAuthSessionChange(app, { principalId: "" });
  handleXiangwanAuthSessionChange(app, { principalId });

  const page = createRegistrationPageDefinition({});
  page.data = { ...page.data };
  page.setData = (changes) => {
    page.data = { ...page.data, ...changes };
  };
  page.presentProfile(app);

  assert.equal(page.data.contactName, "");
  assert.equal(page.data.contactPhone, "");
});

test("wechat-authenticated registration prefills the Xiangwan nickname and freezes edited contact facts", async (t) => {
  const sessionId = "44444444-4444-4444-8444-444444444444";
  const operationId = "55555555-5555-4555-8555-555555555555";
  const storedDrafts = [];
  const storedContactDefaults = [];
  const navigations = [];
  const app = {
    globalData: {
      apiBaseUrl: "https://api.weconq.cn",
      profile: null,
    },
    async ensureAuthenticated() {
      this.globalData.profile = {
        nickname: "微信昵称",
        avatarUrl: "",
        etag: "etag-1",
      };
    },
    setRegistrationDraft(value) {
      storedDrafts.push(value);
    },
    setRegistrationContactDefaults(value) {
      storedContactDefaults.push(value);
    },
  };
  global.getApp = () => app;
  global.wx = {
    openPrivacyContract() {},
    navigateTo(options) {
      navigations.push(options);
    },
  };
  t.after(() => {
    delete global.getApp;
    delete global.wx;
  });

  const page = createRegistrationPageDefinition({
    async getSessionDetail() {
      return {
        session_id: sessionId,
        publication_version: 3,
        price_cents: 0,
        instance_title: "享玩活动",
        session_title: "周末场",
        session_start_at: "2099-09-20T11:00:00Z",
        session_end_at: "2099-09-20T13:00:00Z",
        delivery: { venue_name: "活动空间" },
        display: { state: "open" },
        cta: { action: "start_registration", label: "register_now", enabled: true },
      };
    },
    async getPublicPolicies() {
      return {
        published_versions: [
          { kind: "privacy", version: "privacy-v1" },
          { kind: "manual_contact", version: "contact-v1" },
        ],
        capabilities: {
          privacy_notice_available: true,
          manual_registration_contact_available: true,
        },
        manual_registration_contact_policy: {
          version: "contact-v1",
          content: "联系人信息仅用于本次活动联络。",
        },
      };
    },
    async getSessionQuestionnaire() {
      const error = new Error("not found");
      error.statusCode = 404;
      throw error;
    },
    async getMyProfile() {
      return { nickname: "微信昵称", avatar_url: "", principal_profile_etag: "etag-1" };
    },
    newIdempotencyKey() {
      return operationId;
    },
  });
  page.data = { ...page.data };
  page.setData = (changes) => {
    page.data = { ...page.data, ...changes };
  };
  page._active = true;
  page._sessionId = sessionId;

  await page.loadForm();
  await Promise.resolve();
  assert.equal(page.data.contactName, "微信昵称");

  page.updateContactPhone({ detail: { value: "13812345678" } });
  page.updateContactAcknowledgement({ detail: { value: ["acknowledged"] } });
  page.continueToConfirmation();

  assert.equal(storedDrafts.length, 1);
  assert.deepEqual(storedDrafts[0].payload.contact, {
    name: "微信昵称",
    phone_e164: "+8613812345678",
    policy_version: "contact-v1",
  });
  assert.deepEqual(storedContactDefaults, [
    { contactName: "微信昵称", contactPhone: "+8613812345678" },
  ]);
  assert.equal(navigations.length, 1);
  assert.equal(navigations[0].url, `/pages/registration-confirm/index?session_id=${sessionId}`);
  assert.equal(typeof navigations[0].complete, "function");
});

test("a late profile response from the previous auth generation cannot prefill the next account", async (t) => {
  const sessionId = "66666666-6666-4666-8666-666666666666";
  const app = {
    globalData: {
      apiBaseUrl: "https://api.weconq.cn",
      profile: { nickname: "旧账号昵称", avatarUrl: "", etag: "old" },
    },
    binding: {
      principalId: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
      generation: 1,
    },
    async ensureAuthenticated() {},
    getXiangwanAuthBinding() {
      return this.binding;
    },
    getXiangwanProfile() {
      return null;
    },
  };
  let resolveProfile;
  global.getApp = () => app;
  global.wx = { openPrivacyContract() {} };
  t.after(() => {
    delete global.getApp;
    delete global.wx;
  });

  const page = createRegistrationPageDefinition({
    async getSessionDetail() {
      return {
        session_id: sessionId,
        publication_version: 1,
        price_cents: 0,
        registration_start_at: "2026-09-01T00:00:00Z",
        registration_end_at: "2099-09-19T00:00:00Z",
        session_start_at: "2099-09-20T11:00:00Z",
        session_end_at: "2099-09-20T13:00:00Z",
        delivery: {},
        display: { state: "open" },
        cta: { action: "start_registration", label: "register_now", enabled: true },
      };
    },
    async getPublicPolicies() {
      return {
        published_versions: [
          { kind: "privacy", version: "privacy-v1" },
          { kind: "manual_contact", version: "contact-v1" },
        ],
        capabilities: {
          privacy_notice_available: true,
          manual_registration_contact_available: true,
        },
        manual_registration_contact_policy: {
          version: "contact-v1",
          content: "联系人信息仅用于本次活动联络。",
        },
      };
    },
    getSessionQuestionnaire() {
      const error = new Error("not found");
      error.statusCode = 404;
      return Promise.reject(error);
    },
    getMyProfile() {
      return new Promise((resolve) => {
        resolveProfile = resolve;
      });
    },
  });
  page.data = { ...page.data };
  page.setData = (changes) => {
    page.data = { ...page.data, ...changes };
  };
  page._active = true;
  page._sessionId = sessionId;

  await page.loadForm();
  app.binding = {
    principalId: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
    generation: 2,
  };
  resolveProfile({ nickname: "旧账号昵称", avatar_url: "", principal_profile_etag: "old" });
  await Promise.resolve();

  assert.equal(page.data.contactName, "");
  assert.equal(page.data.profileNickname, "");
});

test("same-Series questionnaire prefill remains editable, keeps consent required, and survives a refresh", async (t) => {
  const q = {
    questionnaire_version_id: CHOICE_FIELD_ID,
    fields: [
      {
        field_id: TEXT_FIELD_ID,
        type: "single_line",
        label: "期待",
        required: true,
        max_length: 100,
      },
    ],
  };
  const app = {
    binding: { principalId: TEXT_FIELD_ID, generation: 1 },
    async ensureAuthenticated() {},
    getXiangwanAuthBinding() {
      return this.binding;
    },
  };
  global.getApp = () => app;
  global.wx = { openPrivacyContract() {} };
  t.after(() => delete global.wx);
  t.after(() => delete global.getApp);
  let reads = 0;
  const api = {
    async getSessionDetail() {
      return {
        session_id: TEXT_FIELD_ID,
        publication_version: 1,
        price_cents: 0,
        delivery: {},
        display: { state: "open" },
        cta: { action: "start_registration", label: "register_now", enabled: true },
      };
    },
    async getPublicPolicies() {
      return {
        published_versions: [
          { kind: "privacy", version: "privacy-v1" },
          { kind: "manual_contact", version: "contact-v1" },
        ],
        capabilities: {
          privacy_notice_available: true,
          manual_registration_contact_available: true,
        },
        manual_registration_contact_policy: { version: "contact-v1", content: "本次活动联络" },
      };
    },
    async getSessionQuestionnaire() {
      return q;
    },
    async getQuestionnairePrefill() {
      reads++;
      return {
        questionnaire_version_id: q.questionnaire_version_id,
        answers: [{ field_id: TEXT_FIELD_ID, values: ["上次答案"] }],
      };
    },
  };
  const page = createRegistrationPageDefinition(api);
  page.setData = (changes) => {
    page.data = { ...page.data, ...changes };
  };
  page._active = true;
  page._sessionId = TEXT_FIELD_ID;
  await page.loadForm();
  assert.equal(page.data.errorMessage, "");
  assert.equal(page.data.blockedMessage, "");
  assert.equal(page.data.fields[0].answerText, "上次答案");
  assert.equal(page.data.contactAcknowledged, false);
  assert.match(page.data.prefillNotice, /可直接确认/);
  page.updateTextAnswer({
    currentTarget: { dataset: { fieldId: TEXT_FIELD_ID } },
    detail: { value: "这次修改" },
  });
  await page.loadForm(true);
  assert.equal(reads, 1);
  assert.equal(page._answers[TEXT_FIELD_ID], "这次修改");
});

test("late owner questionnaire prefill is discarded after an account switch", async (t) => {
  const app = {
    binding: { principalId: TEXT_FIELD_ID, generation: 1 },
    async ensureAuthenticated() {},
    getXiangwanAuthBinding() {
      return this.binding;
    },
  };
  global.getApp = () => app;
  global.wx = { openPrivacyContract() {} };
  t.after(() => delete global.wx);
  t.after(() => delete global.getApp);
  let finish, started;
  const requested = new Promise((r) => {
    started = r;
  });
  const page = createRegistrationPageDefinition({
    async getSessionDetail() {
      return {
        session_id: TEXT_FIELD_ID,
        publication_version: 1,
        price_cents: 0,
        delivery: {},
        display: { state: "open" },
        cta: { action: "start_registration", label: "register_now", enabled: true },
      };
    },
    async getPublicPolicies() {
      return {
        published_versions: [
          { kind: "privacy", version: "privacy-v1" },
          { kind: "manual_contact", version: "contact-v1" },
        ],
        capabilities: {
          privacy_notice_available: true,
          manual_registration_contact_available: true,
        },
        manual_registration_contact_policy: { version: "contact-v1", content: "本次联络" },
      };
    },
    async getSessionQuestionnaire() {
      return {
        questionnaire_version_id: CHOICE_FIELD_ID,
        fields: [
          {
            field_id: TEXT_FIELD_ID,
            type: "single_line",
            label: "期待",
            required: true,
            max_length: 100,
          },
        ],
      };
    },
    getQuestionnairePrefill() {
      started();
      return new Promise((r) => {
        finish = r;
      });
    },
  });
  page.setData = (changes) => {
    page.data = { ...page.data, ...changes };
  };
  page._active = true;
  page._sessionId = TEXT_FIELD_ID;
  const loading = page.loadForm();
  await requested;
  app.binding = { principalId: CHOICE_FIELD_ID, generation: 2 };
  finish({
    questionnaire_version_id: CHOICE_FIELD_ID,
    answers: [{ field_id: TEXT_FIELD_ID, values: ["不能跨账号"] }],
  });
  await loading;
  assert.equal(page._context, null);
  assert.equal(page.data.fields.length, 0);
  assert.equal(Object.keys(page._answers).length, 0);
});
