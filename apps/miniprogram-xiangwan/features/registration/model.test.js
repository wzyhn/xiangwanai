"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const {
  buildRegistrationDraft,
  normalizePhoneE164,
  questionnaireFieldsForView,
  validateQuestionnaire,
} = require("./model");

const SESSION_ID = "11111111-1111-4111-8111-111111111111";
const QUESTIONNAIRE_ID = "22222222-2222-4222-8222-222222222222";
const FIELD_ID = "33333333-3333-4333-8333-333333333333";
const OPERATION_ID = "44444444-4444-4444-8444-444444444444";

function fixture() {
  return {
    detail: {
      session_id: SESSION_ID,
      publication_version: 7,
      price_cents: 0,
      instance_title: "AI 共创夜",
      session_title: "第 1 场",
      session_start_at: "2026-09-20T11:00:00Z",
      delivery: { venue_name: "海河实验室", address: "天津市河西区创新路 18 号" },
      cta: { action: "start_registration", label: "register_now", enabled: true },
    },
    policies: {
      published_versions: [
        { kind: "privacy", version: "privacy-v3" },
        { kind: "manual_contact", version: "contact/2026-09" },
      ],
      capabilities: {
        privacy_notice_available: true,
        manual_registration_contact_available: true,
      },
      manual_registration_contact_policy: {
        version: "contact/2026-09",
        content: "联系人信息仅用于本次活动联络。",
      },
    },
    questionnaire: {
      questionnaire_version_id: QUESTIONNAIRE_ID,
      fields: [
        {
          field_id: FIELD_ID,
          type: "single_choice",
          label: "你的角色",
          required: true,
          options: [
            { code: "builder", label: "开发者" },
            { code: "designer", label: "设计师" },
          ],
        },
      ],
    },
    contactName: " 王伟 ",
    contactPhone: "138 1234 5678",
    contactAcknowledged: true,
    answers: { [FIELD_ID]: "builder" },
    idempotencyKey: OPERATION_ID,
  };
}

test("registration draft freezes exact displayed facts and policy version", () => {
  const result = buildRegistrationDraft(fixture());
  assert.equal(result.ok, true);
  assert.deepEqual(result.value.payload, {
    instance_publication_version: 7,
    price_cents: 0,
    privacy_policy_version: "privacy-v3",
    contact: {
      name: "王伟",
      phone_e164: "+8613812345678",
      policy_version: "contact/2026-09",
    },
    questionnaire_version_id: QUESTIONNAIRE_ID,
    answers: [{ field_id: FIELD_ID, values: ["builder"] }],
  });
  assert.equal(result.value.idempotencyKey, OPERATION_ID);
  assert.equal(result.value.presentation.title, "AI 共创夜");
  assert.equal(result.value.presentation.instanceTitle, "AI 共创夜");
  assert.equal(result.value.presentation.sessionTitle, "第 1 场");
  assert.deepEqual(result.value.presentation.questionnaireAnswers[0], {
    fieldId: FIELD_ID,
    label: "你的角色",
    value: "开发者",
  });
  assert.equal(result.value.presentation.privacyPolicyVersion, "privacy-v3");
  assert.equal(result.value.presentation.address, "天津市河西区创新路 18 号");
  assert.equal(result.value.presentation.contactPolicyText, "联系人信息仅用于本次活动联络。");
  assert.equal(result.value.presentation.contactPhoneMasked, "+861****5678");
});

test("registration draft masks short international phone numbers more completely", () => {
  const input = fixture();
  input.contactPhone = "+12345678";

  const result = buildRegistrationDraft(input);

  assert.equal(result.ok, true);
  assert.equal(result.value.payload.contact.phone_e164, "+12345678");
  assert.equal(result.value.presentation.contactPhoneMasked, "+1****78");
});

test("registration presentation falls back to the session title only when the instance title is empty", () => {
  const input = fixture();
  input.detail.instance_title = "";
  const result = buildRegistrationDraft(input);
  assert.equal(result.ok, true);
  assert.equal(result.value.presentation.title, "第 1 场");
});

test("registration draft gates paid submissions on the payment capability", () => {
  const paid = fixture();
  paid.detail.price_cents = 9900;
  assert.equal(buildRegistrationDraft(paid).ok, false);
  paid.policies.capabilities.wechat_payment_available = true;
  const ready = buildRegistrationDraft(paid);
  assert.equal(ready.ok, true);
  assert.equal(ready.value.payload.price_cents, 9900);
});

test("registration draft fails closed for stale policy and invalid answers", () => {
  const unavailable = fixture();
  unavailable.policies.capabilities.manual_registration_contact_available = false;
  assert.equal(buildRegistrationDraft(unavailable).ok, false);

  const contactPolicyTextMissing = fixture();
  delete contactPolicyTextMissing.policies.manual_registration_contact_policy;
  assert.equal(buildRegistrationDraft(contactPolicyTextMissing).ok, false);

  const privacyUnavailable = fixture();
  privacyUnavailable.policies.capabilities.privacy_notice_available = false;
  assert.equal(buildRegistrationDraft(privacyUnavailable).ok, false);

  const privacyVersionMissing = fixture();
  privacyVersionMissing.policies.published_versions =
    privacyVersionMissing.policies.published_versions.filter((item) => item.kind !== "privacy");
  assert.equal(buildRegistrationDraft(privacyVersionMissing).ok, false);

  const unacknowledged = fixture();
  unacknowledged.contactAcknowledged = false;
  assert.equal(buildRegistrationDraft(unacknowledged).ok, false);

  const invalidAnswer = fixture();
  invalidAnswer.answers[FIELD_ID] = "removed-option";
  const result = buildRegistrationDraft(invalidAnswer);
  assert.equal(result.ok, false);
  assert.match(result.errors[FIELD_ID], /选项已变化/);
});

test("questionnaire validation supports optional absence and Unicode lengths", () => {
  assert.deepEqual(validateQuestionnaire(null, {}), {
    ok: true,
    answers: [],
    summaries: [],
    errors: {},
  });
  assert.equal(normalizePhoneE164("+44 7911 123456"), "+447911123456");
  assert.equal(normalizePhoneE164("123"), "");
});

test("questionnaire summaries retain field identity when display labels repeat", () => {
  const input = fixture();
  input.questionnaire.fields.push({
    field_id: "55555555-5555-4555-8555-555555555555",
    type: "single_line",
    label: "你的角色",
    required: true,
    min_length: 1,
    max_length: 20,
    options: [],
  });
  input.answers["55555555-5555-4555-8555-555555555555"] = "组织者";

  const result = buildRegistrationDraft(input);

  assert.equal(result.ok, true);
  assert.deepEqual(
    result.value.presentation.questionnaireAnswers.map((answer) => answer.fieldId),
    [FIELD_ID, "55555555-5555-4555-8555-555555555555"],
  );
  assert.equal(result.value.presentation.questionnaireAnswers[0].label, "你的角色");
  assert.equal(result.value.presentation.questionnaireAnswers[1].label, "你的角色");
});

test("refreshed questionnaire view restores still-valid in-memory answers", () => {
  const values = questionnaireFieldsForView(fixture().questionnaire, {
    [FIELD_ID]: "builder",
  });
  assert.equal(values[0].options[0].checked, true);
  assert.equal(values[0].options[1].checked, false);
  assert.deepEqual(values[0].selectedValues, ["builder"]);
  assert.equal(values[0].selectedValue, "builder");
});

test("reused questionnaire values validate against the current fields and never accept a changed version", () => {
  const { questionnairePrefillAnswers } = require("./model");
  const id = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa";
  const version = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb";
  const q = {
    questionnaire_version_id: version,
    fields: [{ field_id: id, type: "single_line", label: "期待", required: true, max_length: 100 }],
  };
  const value = {
    questionnaire_version_id: version,
    answers: [{ field_id: id, values: ["学习 AI"] }],
  };
  assert.equal(questionnairePrefillAnswers(q, value)[id], "学习 AI");
  assert.equal(
    Object.keys(questionnairePrefillAnswers(q, { ...value, questionnaire_version_id: id })).length,
    0,
  );
  assert.equal(
    Object.keys(
      questionnairePrefillAnswers(q, {
        ...value,
        answers: [{ field_id: version, values: ["错位答案"] }],
      }),
    ).length,
    0,
  );
  assert.equal(
    Object.keys(
      questionnairePrefillAnswers(q, {
        ...value,
        answers: [{ field_id: id, values: ["一", "二"] }],
      }),
    ).length,
    0,
  );
});
