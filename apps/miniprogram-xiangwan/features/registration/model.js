"use strict";

const { UUID_PATTERN } = require("../../services/xiangwan-api");
const { formatMoney, maskPhone } = require("../../utils/format");

const CHOICE_TYPES = new Set(["single_choice", "multiple_choice", "area"]);
const TEXT_TYPES = new Set(["single_line", "multiline"]);

function normalizeText(value) {
  return String(value == null ? "" : value).trim();
}

function runeLength(value) {
  return Array.from(String(value == null ? "" : value)).length;
}

function normalizePhoneE164(value) {
  const compact = normalizeText(value).replace(/[\s()-]/g, "");
  if (/^1[3-9][0-9]{9}$/.test(compact)) return `+86${compact}`;
  if (/^\+[1-9][0-9]{7,14}$/.test(compact)) return compact;
  return "";
}

function findPublishedPolicyVersion(policies, kind) {
  const versions = Array.isArray(policies && policies.published_versions)
    ? policies.published_versions
    : [];
  const match = versions.find(
    (item) => item && normalizeText(item.kind) === kind && normalizeText(item.version),
  );
  return match ? normalizeText(match.version) : "";
}

function findManualContactPolicy(policies) {
  const value = policies && policies.manual_registration_contact_policy;
  const version = normalizeText(value && value.version);
  const content = normalizeText(value && value.content);
  const publishedVersion = findPublishedPolicyVersion(policies, "manual_contact");
  if (!version || version !== publishedVersion || !content) return null;
  return { version, content };
}

function findCancellationPolicy(policies) {
  const value = policies && policies.cancellation_policy;
  const version = normalizeText(value && value.version);
  const content = normalizeText(value && value.content);
  if (
    !policies ||
    !policies.capabilities ||
    policies.capabilities.paid_self_service_cancellation_available !== true ||
    !version ||
    version !== findPublishedPolicyVersion(policies, "cancellation") ||
    !content
  )
    return null;
  return { version, content };
}

function contactCollectionPolicyBlockMessage(policies, canOpenPrivacyContract) {
  const capabilities = (policies && policies.capabilities) || {};
  const missing = [];
  if (
    capabilities.privacy_notice_available !== true ||
    !findPublishedPolicyVersion(policies, "privacy")
  )
    missing.push("微信隐私保护指引");
  if (
    capabilities.manual_registration_contact_available !== true ||
    !findManualContactPolicy(policies)
  )
    missing.push("报名联系人政策");
  if (!canOpenPrivacyContract) missing.push("当前微信版本的隐私指引能力");
  return missing.length
    ? `${missing.join("、")}暂不可查看，当前不能填写联系人信息。请活动方完成发布后点击“重新检查”。`
    : "";
}

function normalizeAnswerValues(field, rawValue) {
  if (CHOICE_TYPES.has(field.type)) {
    const input = Array.isArray(rawValue) ? rawValue : rawValue ? [rawValue] : [];
    return input.map(normalizeText).filter(Boolean);
  }
  if (TEXT_TYPES.has(field.type)) {
    const value = normalizeText(Array.isArray(rawValue) ? rawValue[0] : rawValue);
    return value ? [value] : [];
  }
  return [];
}

function validateQuestionnaire(questionnaire, rawAnswers = {}) {
  if (!questionnaire) return { ok: true, answers: [], summaries: [], errors: {} };
  const versionId = normalizeText(questionnaire.questionnaire_version_id).toLowerCase();
  const fields = Array.isArray(questionnaire.fields) ? questionnaire.fields : [];
  if (!UUID_PATTERN.test(versionId) || fields.length === 0) {
    return {
      ok: false,
      answers: [],
      summaries: [],
      errors: { questionnaire: "报名问卷已变化，请返回刷新" },
    };
  }

  const answers = [];
  const summaries = [];
  const errors = {};
  fields.forEach((field) => {
    const fieldId = normalizeText(field && field.field_id).toLowerCase();
    const type = normalizeText(field && field.type);
    const values = normalizeAnswerValues({ type }, rawAnswers[fieldId]);
    const options = Array.isArray(field && field.options) ? field.options : [];
    const allowed = new Map(
      options.map((option) => [
        normalizeText(option && option.code),
        normalizeText(option && option.label),
      ]),
    );
    let error = "";

    if (!UUID_PATTERN.test(fieldId) || (!CHOICE_TYPES.has(type) && !TEXT_TYPES.has(type))) {
      error = "问卷字段已变化，请返回刷新";
    } else if (field.required && values.length === 0) {
      error = "此项为必填";
    } else if ((type === "single_choice" || type === "area") && values.length > 1) {
      error = "此项只能选择一个答案";
    } else if (
      type === "multiple_choice" &&
      (!Number.isInteger(field.max_selections) || values.length > field.max_selections)
    ) {
      error = `最多选择 ${Number(field.max_selections) || 0} 项`;
    } else if (CHOICE_TYPES.has(type) && values.some((value) => !allowed.has(value))) {
      error = "选项已变化，请重新选择";
    } else if (TEXT_TYPES.has(type) && values.length === 1) {
      const minimum = Number.isInteger(field.min_length) ? field.min_length : 0;
      const maximum = Number.isInteger(field.max_length) ? field.max_length : 0;
      const length = runeLength(values[0]);
      if (!maximum || length < minimum || length > maximum) {
        error = minimum > 0 ? `请输入 ${minimum}–${maximum} 个字符` : `最多输入 ${maximum} 个字符`;
      } else if (type === "single_line" && /[\r\n]/.test(values[0])) {
        error = "此项只能输入一行";
      }
    }

    if (error) {
      errors[fieldId || `field-${Object.keys(errors).length}`] = error;
      return;
    }
    if (values.length === 0) return;

    answers.push({ field_id: fieldId, values });
    summaries.push({
      fieldId,
      label: normalizeText(field.label),
      value: CHOICE_TYPES.has(type)
        ? values.map((value) => allowed.get(value) || value).join("、")
        : values[0],
    });
  });

  return { ok: Object.keys(errors).length === 0, answers, summaries, errors };
}

function buildRegistrationDraft(input = {}) {
  const detail = input.detail || {};
  const policies = input.policies || {};
  const sessionId = normalizeText(detail.session_id).toLowerCase();
  const idempotencyKey = normalizeText(input.idempotencyKey).toLowerCase();
  const name = normalizeText(input.contactName);
  const phoneE164 = normalizePhoneE164(input.contactPhone);
  const contactPolicy = findManualContactPolicy(policies);
  const policyVersion = contactPolicy ? contactPolicy.version : "";
  const privacyPolicyVersion = findPublishedPolicyVersion(policies, "privacy");
  const capabilities = policies.capabilities || {};
  const errors = {};

  if (!UUID_PATTERN.test(sessionId) || !Number.isSafeInteger(detail.publication_version)) {
    errors.session = "活动信息无效，请返回刷新";
  }
  if (detail.publication_version < 1 || !Number.isSafeInteger(detail.price_cents)) {
    errors.session = "活动信息无效，请返回刷新";
  }
  if (!detail.cta || detail.cta.enabled !== true) {
    errors.session = "当前场次暂不可报名";
  }
  if (!detail.cta || detail.cta.action !== "start_registration") {
    errors.session = "当前场次暂不可报名";
  }
  if (detail.price_cents > 0 && capabilities.wechat_payment_available !== true) {
    errors.session = "微信支付尚未开放，当前不会创建待支付订单";
  }
  if (!capabilities.manual_registration_contact_available || !contactPolicy) {
    errors.contactPolicy = "报名联系人政策尚未发布，暂不能提交";
  }
  if (!capabilities.privacy_notice_available || !privacyPolicyVersion) {
    errors.privacyPolicy = "隐私政策尚未发布，暂不能提交";
  }
  if (!name || runeLength(name) > 100) {
    errors.contactName = "请输入 1–100 个字符的联系人姓名";
  }
  if (!phoneE164) {
    errors.contactPhone = "请输入有效的中国大陆手机号或国际号码";
  }
  if (input.contactAcknowledged !== true) {
    errors.contactAcknowledged = "请先确认报名联系人信息的使用说明";
  }
  if (!UUID_PATTERN.test(idempotencyKey) || idempotencyKey[14] !== "4") {
    errors.idempotencyKey = "报名操作标识无效，请重试";
  }

  const questionnaireResult = validateQuestionnaire(input.questionnaire, input.answers);
  Object.assign(errors, questionnaireResult.errors);
  if (Object.keys(errors).length > 0) {
    return { ok: false, errors, value: null };
  }

  const questionnaireVersionId = input.questionnaire
    ? normalizeText(input.questionnaire.questionnaire_version_id).toLowerCase()
    : null;
  const payload = {
    instance_publication_version: detail.publication_version,
    price_cents: detail.price_cents,
    privacy_policy_version: privacyPolicyVersion,
    contact: {
      name,
      phone_e164: phoneE164,
      policy_version: policyVersion,
    },
    questionnaire_version_id: questionnaireVersionId,
    answers: questionnaireResult.answers,
  };

  return {
    ok: true,
    errors: {},
    value: {
      sessionId,
      idempotencyKey,
      payload,
      presentation: {
        // Keep the confirmation/success presentation aligned with the public
        // detail card: the Instance title is the primary activity title and
        // the concrete Session title is supporting context.  The previous
        // order made the final confirmation page show only "第 N 场" even
        // though the detail page had already established the activity title.
        title: normalizeText(detail.instance_title) || normalizeText(detail.session_title),
        instanceTitle: normalizeText(detail.instance_title),
        sessionTitle: normalizeText(detail.session_title),
        seriesId: normalizeText(detail.series_id),
        activityTypeCode: normalizeText(detail.activity_type),
        startAt: normalizeText(detail.session_start_at),
        endAt: normalizeText(detail.session_end_at),
        venue: normalizeText(
          detail.delivery &&
            (detail.delivery.venue_name || detail.delivery.online_participation_mode),
        ),
        address: normalizeText(detail.delivery && detail.delivery.address),
        price: formatMoney(detail.price_cents),
        priceCents: detail.price_cents,
        privacyPolicyVersion,
        contactPolicyVersion: policyVersion,
        contactPolicyText: contactPolicy.content,
        contactName: name,
        contactPhoneMasked: maskPhone(phoneE164),
        questionnaireAnswers: questionnaireResult.summaries,
      },
    },
  };
}

function questionnairePrefillAnswers(questionnaire, prefill) {
  const empty = Object.create(null);
  if (
    !questionnaire ||
    !prefill ||
    prefill.questionnaire_version_id !== questionnaire.questionnaire_version_id ||
    !Array.isArray(prefill.answers) ||
    !prefill.answers.length
  )
    return empty;
  const fields = new Map(questionnaire.fields.map((field) => [field.field_id, field]));
  const answers = Object.create(null);
  for (const answer of prefill.answers) {
    const field = answer && fields.get(answer.field_id);
    if (
      !field ||
      Object.prototype.hasOwnProperty.call(answers, answer.field_id) ||
      !Array.isArray(answer.values) ||
      !answer.values.every((value) => typeof value === "string") ||
      (TEXT_TYPES.has(field.type) && answer.values.length > 1)
    )
      return empty;
    answers[answer.field_id] = TEXT_TYPES.has(field.type)
      ? answer.values[0] || ""
      : [...answer.values];
  }
  return validateQuestionnaire(questionnaire, answers).ok ? answers : empty;
}

function questionnaireFieldsForView(questionnaire, rawAnswers = {}) {
  const fields = Array.isArray(questionnaire && questionnaire.fields) ? questionnaire.fields : [];
  return fields.map((field) => {
    const fieldId = normalizeText(field && field.field_id).toLowerCase();
    const rawValue = rawAnswers[fieldId];
    const selectedValues = Array.isArray(rawValue) ? rawValue : rawValue ? [rawValue] : [];
    return {
      ...field,
      selectedValues,
      selectedValue: selectedValues[0] || "",
      options: (Array.isArray(field.options) ? field.options : []).map((option) => ({
        ...option,
        checked: selectedValues.includes(option.code),
      })),
      isChoice: field.type === "single_choice" || field.type === "area",
      isMultipleChoice: field.type === "multiple_choice",
      isMultiline: field.type === "multiline",
      isSingleLine: field.type === "single_line",
      answerText: TEXT_TYPES.has(field.type) ? normalizeText(rawValue) : "",
      error: "",
    };
  });
}

module.exports = {
  buildRegistrationDraft,
  contactCollectionPolicyBlockMessage,
  findManualContactPolicy,
  findCancellationPolicy,
  findPublishedPolicyVersion,
  normalizePhoneE164,
  questionnaireFieldsForView,
  questionnairePrefillAnswers,
  runeLength,
  validateQuestionnaire,
};
