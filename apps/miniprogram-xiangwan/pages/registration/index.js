"use strict";

const { projectSessionDetail } = require("../../features/catalog/model");
const { projectMyProfile, projectProfileBadge } = require("../../features/profile/model");
const {
  buildRegistrationDraft,
  contactCollectionPolicyBlockMessage,
  findManualContactPolicy,
  findPublishedPolicyVersion,
  normalizePhoneE164,
  questionnaireFieldsForView,
  questionnairePrefillAnswers,
} = require("../../features/registration/model");
const { canonicalUUID, xiangwanApi } = require("../../services/xiangwan-api");
const { getUserMessage } = require("../../utils/errors");

function resolveApp() {
  if (typeof getApp !== "function") return null;
  try {
    return getApp();
  } catch (_error) {
    return null;
  }
}

function readPrincipalId(app) {
  return String((app && app.globalData && app.globalData.principalId) || "")
    .trim()
    .toLowerCase();
}

function readAuthBinding(app) {
  if (app && typeof app.getXiangwanAuthBinding === "function") {
    const binding = app.getXiangwanAuthBinding();
    return {
      principalId: String((binding && binding.principalId) || "")
        .trim()
        .toLowerCase(),
      generation: Number(binding && binding.generation),
    };
  }
  return { principalId: readPrincipalId(app), generation: 0 };
}

function sameAuthBinding(app, expected) {
  if (!expected) return true;
  const current = readAuthBinding(app);
  if (expected.principalId && current.principalId !== expected.principalId) return false;
  return !expected.generation || current.generation === expected.generation;
}

function readXiangwanProfile(app) {
  if (app && typeof app.getXiangwanProfile === "function") {
    return app.getXiangwanProfile();
  }
  return app && app.globalData && app.globalData.profile;
}

function readRegistrationContactDefaults(app) {
  if (!app || typeof app.getRegistrationContactDefaults !== "function") return null;
  try {
    const value = app.getRegistrationContactDefaults();
    if (!value || typeof value !== "object" || Array.isArray(value)) return null;
    const contactName = String(value.contactName || "").trim();
    const contactPhone = normalizePhoneE164(value.contactPhone);
    if (!contactName || !contactPhone) return null;
    return { contactName, contactPhone };
  } catch (_error) {
    return null;
  }
}

function sameRegistrationPayload(left, right) {
  if (!left || !right || typeof left !== "object" || typeof right !== "object") return false;
  try {
    return JSON.stringify(left) === JSON.stringify(right);
  } catch (_error) {
    return false;
  }
}

function registrationPolicyBlockMessage(policies) {
  return contactCollectionPolicyBlockMessage(
    policies,
    typeof wx !== "undefined" && typeof wx.openPrivacyContract === "function",
  );
}

function policyVersionsRequireAcknowledgementReset(preserveInput, previousPolicies, nextPolicies) {
  if (!preserveInput) return false;
  return ["privacy", "manual_contact"].some(
    (kind) =>
      findPublishedPolicyVersion(previousPolicies, kind) !==
      findPublishedPolicyVersion(nextPolicies, kind),
  );
}

function reusableDraftForSession(app, sessionId) {
  if (!app || typeof app.getRegistrationDraft !== "function") return null;
  const draft = app.getRegistrationDraft();
  try {
    if (!draft || canonicalUUID(draft.sessionId, "活动场次") !== sessionId) return null;
    const operationKey = canonicalUUID(draft.idempotencyKey, "报名操作");
    if (operationKey[14] !== "4") return null;
    return draft;
  } catch (_error) {
    return null;
  }
}

function createRegistrationPageDefinition(api = xiangwanApi) {
  return {
    data: {
      loading: true,
      questionnaireLoading: false,
      prefillNotice: "",
      blockedMessage: "",
      errorMessage: "",
      formError: "",
      detail: null,
      fields: [],
      privacyPurpose: "",
      privacyPolicyVersion: "",
      contactPolicyVersion: "",
      contactPolicyText: "",
      contactName: "",
      contactPhone: "",
      contactNameError: "",
      contactPhoneError: "",
      contactAcknowledgementError: "",
      profileNickname: "",
      profileAvatarUrl: "",
      profileAvatarInitial: "享",
      contactAcknowledged: false,
      contactAcknowledgementOptions: [
        { value: "acknowledged", label: "我已查看并知悉隐私与联系人政策" },
      ],
      choiceKeys: { value: "code", label: "label" },
      textareaAutosize: { minHeight: 110, maxHeight: 240 },
      formNotice: "",
      continuing: false,
    },

    onLoad(options = {}) {
      this._active = true;
      this._loadGeneration = Number(this._loadGeneration || 0) + 1;
      this._answers = Object.create(null);
      this._contactNameEdited = false;
      this._contactPhoneEdited = false;
      this._contactNameAutoPrefilled = false;
      this._contactPhoneAutoPrefilled = false;
      try {
        this._sessionId = canonicalUUID(options.session_id, "活动场次");
      } catch (error) {
        this.setData({ loading: false, errorMessage: getUserMessage(error, "活动链接无效") });
        return;
      }
      void this.loadForm();
    },

    onShow() {
      const app = resolveApp();
      if (this._authBinding && !sameAuthBinding(app, this._authBinding)) {
        this.scrubForAuthChange();
        return;
      }
      this.presentProfile(app);
      if (
        this._sessionId &&
        app &&
        typeof app.consumeRegistrationConflict === "function" &&
        app.consumeRegistrationConflict(this._sessionId)
      ) {
        if (typeof app.clearRegistrationDraft === "function") app.clearRegistrationDraft();
        void this.loadForm(true);
      }
    },

    onUnload() {
      this._active = false;
      this._loadGeneration = Number(this._loadGeneration || 0) + 1;
    },

    scrubForAuthChange() {
      this._loadGeneration = Number(this._loadGeneration || 0) + 1;
      this._authBinding = null;
      this._context = null;
      this._answers = Object.create(null);
      this._contactNameEdited = false;
      this._contactPhoneEdited = false;
      this._contactNameAutoPrefilled = false;
      this._contactPhoneAutoPrefilled = false;
      this.setData({
        loading: false,
        questionnaireLoading: false,
        detail: null,
        fields: [],
        contactName: "",
        contactPhone: "",
        contactAcknowledged: false,
        profileNickname: "",
        profileAvatarUrl: "",
        profileAvatarInitial: "享",
        errorMessage: "登录状态已变化，请重新加载报名信息",
        formError: "",
        formNotice: "",
      });
    },

    async loadForm(preserveInput = false) {
      const loadGeneration = Number(this._loadGeneration || 0) + 1;
      this._loadGeneration = loadGeneration;
      const isCurrent = () => this._active && loadGeneration === this._loadGeneration;
      const preservedAnswers = preserveInput ? { ...(this._answers || {}) } : Object.create(null);
      const previousPolicies = this._context && this._context.policies;
      this._context = null;
      this.setData({
        loading: true,
        questionnaireLoading: false,
        errorMessage: "",
        blockedMessage: "",
        formError: "",
        contactNameError: "",
        contactPhoneError: "",
        contactAcknowledgementError: "",
        formNotice: preserveInput ? "活动信息已变化，正在刷新，请重新核对后提交" : "",
      });
      try {
        const [detailRaw, policies] = await Promise.all([
          api.getSessionDetail(this._sessionId),
          api.getPublicPolicies(),
        ]);
        if (!isCurrent()) return;
        const detail = projectSessionDetail(detailRaw, {
          now: Date.now(),
          wechatPaymentAvailable:
            policies.capabilities && policies.capabilities.wechat_payment_available === true,
        });
        const contactPolicyVersion = findPublishedPolicyVersion(policies, "manual_contact");
        const privacyPolicyVersion = findPublishedPolicyVersion(policies, "privacy");
        const contactPolicy = findManualContactPolicy(policies);
        if (!detail.registrationReady) {
          const message =
            detail.registrationNotice ||
            (detail.paidRegistrationPending
              ? "付费报名尚未接入，当前不会创建待支付订单"
              : "当前场次暂不可报名");
          this._context = null;
          this.setData({ detail, blockedMessage: message });
          return;
        }
        const policyBlockMessage = registrationPolicyBlockMessage(policies);
        if (policyBlockMessage) {
          this._context = null;
          this.setData({
            detail,
            blockedMessage: policyBlockMessage,
          });
          return;
        }

        const app = resolveApp();
        if (!app || typeof app.ensureAuthenticated !== "function") {
          throw new Error("runtime unavailable");
        }
        await app.ensureAuthenticated();
        if (!isCurrent()) return;
        const profileBinding = readAuthBinding(app);
        this._authBinding = profileBinding;
        this.presentProfile(app);
        // Profile is optional presentation. A failed profile read must not
        // discard the contact form or block registration. Bind the late read
        // to the page load and principal generation so an account switch can
        // never overwrite the next account's nickname/contact form.
        let profileRead = null;
        try {
          if (typeof app.refreshXiangwanProfile === "function") {
            profileRead = app.refreshXiangwanProfile();
          } else if (typeof api.getMyProfile === "function") {
            profileRead = api.getMyProfile();
          }
        } catch (_error) {
          profileRead = null;
        }
        if (profileRead && typeof profileRead.then === "function") {
          void profileRead
            .then((raw) => {
              if (!raw || !isCurrent() || !sameAuthBinding(app, profileBinding)) return;
              this.presentProfile(
                app,
                projectMyProfile(raw, app.globalData && app.globalData.apiBaseUrl),
              );
            })
            .catch(() => {});
        }
        // Contact entry is available as soon as login completes. The
        // questionnaire may still be in flight, so confirmation stays gated.
        this.setData({
          loading: false,
          questionnaireLoading: true,
          detail,
          privacyPolicyVersion,
          contactPolicyVersion,
          contactPolicyText: contactPolicy.content,
        });
        let questionnaire = null;
        try {
          questionnaire = await api.getSessionQuestionnaire(this._sessionId);
        } catch (error) {
          if (Number(error && error.statusCode) !== 404) throw error;
        }
        if (!isCurrent()) return;
        if (!sameAuthBinding(app, profileBinding)) {
          this.scrubForAuthChange();
          return;
        }
        let prefillNotice = "";
        let answers = preservedAnswers;
        if (!preserveInput && questionnaire && typeof api.getQuestionnairePrefill === "function") {
          try {
            const prefill = await api.getQuestionnairePrefill(this._sessionId);
            if (!isCurrent()) return;
            if (!sameAuthBinding(app, profileBinding)) {
              this.scrubForAuthChange();
              return;
            }
            answers = questionnairePrefillAnswers(questionnaire, prefill);
            if (Object.keys(answers).length)
              prefillNotice = "已带入你在本系列填写的问卷，可直接确认，也可修改。";
          } catch (_error) {
            // Prefill is a convenience; a failed owner read never prevents a
            // fresh questionnaire submission or changes the consent gate.
            prefillNotice = "历史答案暂不可读取，可直接填写本次问卷。";
          }
        }
        if (!isCurrent()) return;
        if (!sameAuthBinding(app, profileBinding)) {
          this.scrubForAuthChange();
          return;
        }
        this._context = { detail: detailRaw, policies, questionnaire };
        this._answers = answers;
        const resetContactAcknowledgement = policyVersionsRequireAcknowledgementReset(
          preserveInput,
          previousPolicies,
          policies,
        );
        this.setData({
          detail,
          questionnaireLoading: false,
          fields: questionnaireFieldsForView(questionnaire, answers),
          prefillNotice,
          privacyPurpose: String((questionnaire && questionnaire.privacy_purpose) || "").trim(),
          privacyPolicyVersion,
          contactPolicyVersion,
          contactPolicyText: contactPolicy.content,
          contactAcknowledged: resetContactAcknowledgement ? false : this.data.contactAcknowledged,
          formNotice: resetContactAcknowledgement
            ? "联系人政策已更新，请重新确认用途后提交"
            : preserveInput
              ? "活动信息已刷新，请重新核对后提交"
              : "",
        });
      } catch (error) {
        if (isCurrent()) {
          this._context = null;
          this.setData({
            questionnaireLoading: false,
            errorMessage: getUserMessage(error, "报名表加载失败，请稍后重试"),
          });
        }
      } finally {
        if (isCurrent()) this.setData({ loading: false });
      }
    },

    updateContactName(event) {
      this._contactNameEdited = true;
      this._contactNameAutoPrefilled = false;
      this.setData({
        contactName: String(event.detail.value || ""),
        contactNameError: "",
        formError: "",
      });
    },

    updateContactPhone(event) {
      this._contactPhoneEdited = true;
      this._contactPhoneAutoPrefilled = false;
      this.setData({
        contactPhone: String(event.detail.value || ""),
        contactPhoneError: "",
        formError: "",
      });
    },

    // tdesign 的清除按钮只触发 clear 事件,不带 change;若不在此同步置空,
    // 确认页与提交体会保留清除前的旧值(与所见不一致)。
    clearContactName() {
      this._contactNameEdited = true;
      this._contactNameAutoPrefilled = false;
      this.setData({ contactName: "", contactNameError: "", formError: "" });
    },

    clearContactPhone() {
      this._contactPhoneEdited = true;
      this._contactPhoneAutoPrefilled = false;
      this.setData({ contactPhone: "", contactPhoneError: "", formError: "" });
    },

    updateContactAcknowledgement(event) {
      const values = Array.isArray(event.detail.value) ? event.detail.value : [];
      this.setData({
        contactAcknowledged: values.includes("acknowledged"),
        contactAcknowledgementError: "",
        formError: "",
      });
    },

    openPrivacyContract() {
      if (typeof wx === "undefined" || typeof wx.openPrivacyContract !== "function") {
        this.setData({ formError: "微信隐私保护指引暂不可查看，请稍后重试" });
        return;
      }
      wx.openPrivacyContract({
        success: () => {
          if (this._active) this.setData({ formError: "" });
        },
        fail: () => {
          if (this._active) {
            this.setData({ formError: "微信隐私保护指引打开失败，请稍后重试" });
          }
        },
      });
    },

    presentProfile(app, profile) {
      const badge = projectProfileBadge(profile || readXiangwanProfile(app));
      const defaults = readRegistrationContactDefaults(app);
      const changes = {
        profileNickname: badge.nickname,
        profileAvatarUrl: badge.avatarUrl,
        profileAvatarInitial: badge.initial,
      };
      const canAutoFillName =
        !this._contactNameEdited && (!this.data.contactName || this._contactNameAutoPrefilled);
      if (canAutoFillName) {
        // A completed registration may intentionally use a contact nickname
        // that differs from the Auth profile nickname. Reuse that same-process
        // value first; it is already bound to the current principal and auth
        // generation by app.js. Fall back to the profile only for a first
        // registration without a prior contact snapshot.
        const nextName = (defaults && defaults.contactName) || badge.nickname || "";
        changes.contactName = nextName;
        this._contactNameAutoPrefilled = Boolean(nextName);
      }
      const canAutoFillPhone =
        !this._contactPhoneEdited && (!this.data.contactPhone || this._contactPhoneAutoPrefilled);
      if (canAutoFillPhone) {
        const nextPhone = (defaults && defaults.contactPhone) || "";
        changes.contactPhone = nextPhone;
        this._contactPhoneAutoPrefilled = Boolean(nextPhone);
      }
      this.setData(changes);
    },

    onProfileAvatarError() {
      this.setData({ profileAvatarUrl: "" });
    },

    openPoliciesPage() {
      if (typeof wx !== "undefined" && typeof wx.navigateTo === "function") {
        wx.navigateTo({ url: "/pages/policies/index" });
      }
    },

    updateTextAnswer(event) {
      const fieldId = String(event.currentTarget.dataset.fieldId || "").trim();
      if (!fieldId) return;
      const answerText = String(event.detail.value || "");
      this._answers[fieldId] = answerText;
      this.updateFieldState(fieldId, { answerText });
    },

    updateChoiceAnswer(event) {
      const fieldId = String(event.currentTarget.dataset.fieldId || "").trim();
      if (!fieldId) return;
      const value = event.detail.value;
      const selectedValues = Array.isArray(value) ? value : [value];
      this._answers[fieldId] = value;
      this.updateFieldState(fieldId, { selectedValues });
    },

    updateFieldState(fieldId, nextState) {
      const selected = new Set(nextState.selectedValues || []);
      const hasSelectedValues = Object.prototype.hasOwnProperty.call(nextState, "selectedValues");
      const fields = this.data.fields.map((field) => {
        if (field.field_id !== fieldId) return field;
        return {
          ...field,
          ...(Object.prototype.hasOwnProperty.call(nextState, "answerText")
            ? { answerText: nextState.answerText }
            : {}),
          ...(hasSelectedValues
            ? {
                selectedValues: nextState.selectedValues,
                selectedValue: nextState.selectedValues[0] || "",
              }
            : {}),
          options: Array.isArray(field.options)
            ? field.options.map((option) => ({
                ...option,
                checked: selected.has(option.code),
              }))
            : [],
          error: "",
        };
      });
      this.setData({ fields, formError: "" });
    },

    continueToConfirmation() {
      if (!this._context || this.data.continuing) return;
      const app = resolveApp();
      if (this._authBinding && !sameAuthBinding(app, this._authBinding)) {
        this.scrubForAuthChange();
        return;
      }
      const previousDraft = reusableDraftForSession(app, this._sessionId);
      const draftInput = {
        ...this._context,
        contactName: this.data.contactName,
        contactPhone: this.data.contactPhone,
        contactAcknowledged: this.data.contactAcknowledged,
        answers: this._answers,
      };
      let result = buildRegistrationDraft({
        ...draftInput,
        idempotencyKey: previousDraft ? previousDraft.idempotencyKey : api.newIdempotencyKey(),
      });
      if (
        result.ok &&
        previousDraft &&
        !sameRegistrationPayload(previousDraft.payload, result.value.payload)
      ) {
        result = buildRegistrationDraft({
          ...draftInput,
          idempotencyKey: api.newIdempotencyKey(),
        });
      }
      if (!result.ok) {
        const fields = this.data.fields.map((field) => ({
          ...field,
          error: result.errors[field.field_id] || "",
        }));
        const firstErrorKey = Object.keys(result.errors)[0];
        const firstError = (firstErrorKey && result.errors[firstErrorKey]) || "请检查报名信息";
        this.setData({
          fields,
          contactNameError: result.errors.contactName || "",
          contactPhoneError: result.errors.contactPhone || "",
          contactAcknowledgementError: result.errors.contactAcknowledged || "",
          formError: firstError,
        });
        return;
      }
      if (!app || typeof app.setRegistrationDraft !== "function") {
        this.setData({ formError: "报名确认页暂不可用，请稍后重试" });
        return;
      }
      app.setRegistrationDraft(result.value);
      if (typeof app.setRegistrationContactDefaults === "function") {
        app.setRegistrationContactDefaults({
          contactName: result.value.payload.contact.name,
          contactPhone: result.value.payload.contact.phone_e164,
        });
      }
      this.setData({ continuing: true });
      wx.navigateTo({
        url: `/pages/registration-confirm/index?session_id=${encodeURIComponent(this._sessionId)}`,
        complete: () => {
          if (this._active) this.setData({ continuing: false });
        },
      });
    },

    retry() {
      void this.loadForm(Boolean(this.data.formNotice));
    },
  };
}

if (typeof Page === "function") Page(createRegistrationPageDefinition());

module.exports = {
  createRegistrationPageDefinition,
  policyVersionsRequireAcknowledgementReset,
  registrationPolicyBlockMessage,
  readRegistrationContactDefaults,
  reusableDraftForSession,
  sameRegistrationPayload,
};
