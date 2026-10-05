"use strict";

const {
  contactCollectionPolicyBlockMessage,
  findManualContactPolicy,
  findPublishedPolicyVersion,
  normalizePhoneE164,
} = require("../../features/registration/model");
const { validateNickname } = require("../../features/profile/model");
const {
  buildPrivateProfileExtension,
  readProfileExtension,
} = require("../../features/profile/extension");
const { xiangwanApi } = require("../../services/xiangwan-api");
const { getUserMessage } = require("../../utils/errors");

function resolveApp() {
  if (typeof getApp !== "function") return null;
  try {
    return getApp();
  } catch (_error) {
    return null;
  }
}

function canOpenPrivacyContract() {
  return typeof wx !== "undefined" && typeof wx.openPrivacyContract === "function";
}

function readBinding(app) {
  return app && typeof app.getXiangwanAuthBinding === "function"
    ? app.getXiangwanAuthBinding()
    : null;
}

function sameBinding(left, right) {
  return Boolean(
    left &&
      right &&
      left.principalId &&
      left.principalId === right.principalId &&
      left.generation === right.generation,
  );
}

function createContactSetupPageDefinition(api = xiangwanApi) {
  return {
    data: {
      loading: true,
      blockedMessage: "",
      errorMessage: "",
      formError: "",
      nameError: "",
      phoneError: "",
      acknowledgementError: "",
      contactName: "",
      contactPhone: "",
      acknowledged: false,
      acknowledgementOptions: [{ value: "acknowledged", label: "我已查看并知悉隐私与联系人政策" }],
      privacyVersion: "",
      contactVersion: "",
      contactPolicyText: "",
      introduction: "",
      tagsText: "",
      optionalError: "",
      optionalPendingOperation: false,
      optionalNotice: "新提交的标签和简介默认仅自己可见，提交后需经过内容审核。",
      saving: false,
    },

    onLoad(options = {}) {
      this._active = true;
      this._completed = false;
      const app = resolveApp();
      this._binding = readBinding(app);
      this._setupId = Number(options.setup_id);
      this._nameEdited = false;
      this._phoneEdited = false;
      this._extensionInputEdited = false;
      this.setData({
        optionalPendingOperation: Boolean(
          app &&
            typeof app.getProfileExtensionOperation === "function" &&
            app.getProfileExtensionOperation(),
        ),
      });
      if (
        !Number.isSafeInteger(this._setupId) ||
        this._setupId < 1 ||
        !app ||
        typeof app.getContactSetupId !== "function" ||
        app.getContactSetupId() !== this._setupId
      ) {
        this.setData({ loading: false, errorMessage: "信息收集链接已失效，请重新登录" });
        return;
      }
      void this.loadPolicies();
      this._contactLoadPromise = this.loadSavedContact();
      this._profileLoadPromise = Promise.resolve()
        .then(() => api.getMyProfile())
        .then((raw) => {
          if (!this._active || !sameBinding(this._binding, readBinding(app))) return;
          const extension = readProfileExtension(raw);
          if (!extension) return;
          this._profileRaw = raw;
          if (this._extensionInputEdited) return;
          const display = extension.pending || extension.profile;
          this.setData({
            introduction: display.introduction,
            tagsText: display.tags.join("，"),
            optionalNotice: extension.pending
              ? "已有个人资料正在审核。若需修改，请等待审核完成。"
              : "新提交的标签和简介默认仅自己可见，提交后需经过内容审核。",
          });
        })
        .catch(() => null);
      if (app && typeof app.refreshXiangwanProfile === "function") {
        void Promise.resolve(app.refreshXiangwanProfile())
          .then((profile) => {
            if (!this._active || this._nameEdited || !sameBinding(this._binding, readBinding(app)))
              return;
            const nickname = String((profile && profile.nickname) || "").trim();
            if (nickname) this.setData({ contactName: nickname });
          })
          .catch(() => null);
      }
    },

    onUnload() {
      this._active = false;
      if (!this._completed) {
        const app = resolveApp();
        if (app && typeof app.cancelContactSetup === "function")
          app.cancelContactSetup(this._setupId);
      }
    },

    async loadPolicies() {
      this.setData({ loading: true, blockedMessage: "", errorMessage: "" });
      try {
        const policies = await api.getPublicPolicies();
        if (!this._active) return;
        const blockedMessage = contactCollectionPolicyBlockMessage(
          policies,
          canOpenPrivacyContract(),
        );
        const contact = findManualContactPolicy(policies);
        this.setData({
          blockedMessage,
          privacyVersion: blockedMessage ? "" : findPublishedPolicyVersion(policies, "privacy"),
          contactVersion: blockedMessage ? "" : contact.version,
          contactPolicyText: blockedMessage ? "" : contact.content,
          acknowledged: false,
        });
      } catch (error) {
        if (this._active)
          this.setData({ errorMessage: getUserMessage(error, "政策加载失败，请重试") });
      } finally {
        if (this._active) this.setData({ loading: false });
      }
    },

    async loadSavedContact() {
      const app = resolveApp();
      try {
        const contact = await api.getRegistrationContact();
        if (!this._active || !sameBinding(this._binding, readBinding(app))) return null;
        this._savedContact = contact;
        const patch = {};
        if (!this._nameEdited && contact.nickname) patch.contactName = contact.nickname;
        if (!this._phoneEdited) patch.contactPhone = contact.phone_e164;
        this.setData(patch);
        return contact;
      } catch (_error) {
        if (this._active) this.setData({ formError: "报名资料读取失败，请点击保存重试" });
        return null;
      }
    },

    updateName(event) {
      this._nameEdited = true;
      this.setData({
        contactName: String((event && event.detail && event.detail.value) || ""),
        nameError: "",
        formError: "",
      });
    },
    clearName() {
      this.updateName({ detail: { value: "" } });
    },
    updatePhone(event) {
      this._phoneEdited = true;
      this.setData({
        contactPhone: String((event && event.detail && event.detail.value) || ""),
        phoneError: "",
        formError: "",
      });
    },
    clearPhone() {
      this.updatePhone({ detail: { value: "" } });
    },
    updateAcknowledgement(event) {
      const values = Array.isArray(event && event.detail && event.detail.value)
        ? event.detail.value
        : [];
      this.setData({
        acknowledged: values.includes("acknowledged"),
        acknowledgementError: "",
        formError: "",
      });
    },
    updateIntroduction(event) {
      this._extensionInputEdited = true;
      this.setData({
        introduction: String((event && event.detail && event.detail.value) || ""),
        optionalError: "",
      });
    },
    updateTags(event) {
      this._extensionInputEdited = true;
      this.setData({
        tagsText: String((event && event.detail && event.detail.value) || ""),
        optionalError: "",
      });
    },
    restoreOptionalOperation() {
      const app = resolveApp();
      const operation =
        app && typeof app.getProfileExtensionOperation === "function"
          ? app.getProfileExtensionOperation()
          : null;
      if (!operation || !operation.payload) return;
      this._extensionInputEdited = true;
      this.setData({
        introduction: String(operation.payload.introduction || ""),
        tagsText: Array.isArray(operation.payload.tags) ? operation.payload.tags.join("，") : "",
        optionalError: "",
        optionalPendingOperation: true,
      });
    },

    openPoliciesPage() {
      if (typeof wx !== "undefined" && typeof wx.navigateTo === "function")
        wx.navigateTo({ url: "/pages/policies/index" });
    },

    openPrivacyContract() {
      if (!canOpenPrivacyContract()) {
        this.setData({ formError: "微信隐私保护指引暂不可查看，请稍后重试" });
        return;
      }
      wx.openPrivacyContract({
        fail: () => {
          if (this._active) this.setData({ formError: "微信隐私保护指引打开失败，请稍后重试" });
        },
      });
    },

    async save() {
      return this.saveContact(false);
    },

    async saveContact(skipOptional = false) {
      if (this.data.loading || this.data.blockedMessage || this.data.saving) return;
      const nickname = validateNickname(this.data.contactName);
      const phone = normalizePhoneE164(this.data.contactPhone);
      const nameError = nickname.ok ? "" : nickname.message;
      const phoneError = phone ? "" : "请填写正确的手机号";
      const acknowledgementError = this.data.acknowledged ? "" : "请先查看并知悉隐私与联系人政策";
      if (nameError || phoneError || acknowledgementError) {
        this.setData({ nameError, phoneError, acknowledgementError, formError: "请补全必填信息" });
        return;
      }
      const app = resolveApp();
      if (!app || !sameBinding(this._binding, readBinding(app))) {
        this.setData({ formError: "登录状态已变化，请重新登录" });
        return;
      }
      this.setData({ saving: true, formError: "", optionalError: "" });
      let savingOptional = false;
      try {
        // Refresh immediately before accepting sensitive input. A policy
        // revision must be explicitly acknowledged on the updated page.
        const policies = await api.getPublicPolicies();
        if (!this._active) return;
        const blockedMessage = contactCollectionPolicyBlockMessage(
          policies,
          canOpenPrivacyContract(),
        );
        const privacyVersion = findPublishedPolicyVersion(policies, "privacy");
        const contactVersion = findPublishedPolicyVersion(policies, "manual_contact");
        if (
          blockedMessage ||
          privacyVersion !== this.data.privacyVersion ||
          contactVersion !== this.data.contactVersion
        ) {
          this.setData({
            blockedMessage,
            acknowledged: false,
            formError: blockedMessage || "政策已更新，请重新查看并确认后保存",
          });
          if (!blockedMessage) void this.loadPolicies();
          return;
        }
        if (!sameBinding(this._binding, readBinding(app))) {
          this.setData({ formError: "登录状态已变化，请重新登录" });
          return;
        }
        if (!skipOptional && this._extensionInputEdited) {
          savingOptional = true;
          const fingerprint = JSON.stringify([this.data.introduction, this.data.tagsText]);
          let operation =
            typeof app.getProfileExtensionOperation === "function"
              ? app.getProfileExtensionOperation()
              : null;
          if (operation && operation.fingerprint !== fingerprint) {
            this.setData({
              optionalError: "上次资料提交结果待确认，请保持原内容并重试，或跳过可选资料",
            });
            return;
          }
          if (!operation) {
            if (this._profileLoadPromise) await this._profileLoadPromise;
            if (!this._active) return;
            if (!sameBinding(this._binding, readBinding(app))) {
              this.setData({ formError: "登录状态已变化，请重新登录" });
              return;
            }
            const candidate = buildPrivateProfileExtension(
              this._profileRaw,
              this.data.introduction,
              this.data.tagsText,
            );
            if (!candidate.ok) {
              this.setData({ optionalError: candidate.message });
              return;
            }
            if (!candidate.unchanged) {
              operation = {
                key: api.newIdempotencyKey(),
                fingerprint,
                payload: candidate.payload,
              };
              operation = app.setProfileExtensionOperation(operation);
              if (!operation) {
                this.setData({ optionalError: "资料提交暂不可用，请稍后重试" });
                return;
              }
              this.setData({ optionalPendingOperation: true });
            }
          }
          if (operation) {
            if (!sameBinding(this._binding, readBinding(app))) {
              this.setData({ formError: "登录状态已变化，请重新登录" });
              return;
            }
            await api.updateProfileExtension(operation.payload, operation.key);
            if (!this._active) return;
            if (!sameBinding(this._binding, readBinding(app))) return;
            app.clearProfileExtensionOperation();
            this.setData({ optionalPendingOperation: false });
          }
        }
        savingOptional = false;
        if (this._contactLoadPromise) await this._contactLoadPromise;
        const currentContact = await api.getRegistrationContact();
        if (!this._active || !sameBinding(this._binding, readBinding(app))) return;
        if (
          this._savedContact &&
          (this._savedContact.version !== currentContact.version ||
            this._savedContact.nickname !== currentContact.nickname) &&
          !(currentContact.nickname === nickname.nickname && currentContact.phone_e164 === phone)
        ) {
          this._savedContact = currentContact;
          this.setData({ formError: "报名资料已在其他页面更新，请核对当前填写内容后再次保存" });
          return;
        }
        const payload = {
          nickname: nickname.nickname,
          phone_e164: phone,
          principal_profile_etag: currentContact.principal_profile_etag,
          expected_version: currentContact.version,
          privacy_policy_version: privacyVersion,
          contact_policy_version: contactVersion,
        };
        let saved;
        try {
          saved = await api.updateRegistrationContact(payload);
        } catch (error) {
          // A lost response cannot be treated as success without reading the
          // exact private target. Do not issue a second write with a new state.
          const reconciled = await api.getRegistrationContact();
          if (
            !reconciled.configured ||
            reconciled.nickname !== payload.nickname ||
            reconciled.phone_e164 !== payload.phone_e164 ||
            reconciled.privacy_policy_version !== privacyVersion ||
            reconciled.contact_policy_version !== contactVersion
          )
            throw error;
          saved = reconciled;
        }
        if (!this._active || !sameBinding(this._binding, readBinding(app))) return;
        this._savedContact = saved;
        if (typeof app.markXiangwanProfileMutation === "function")
          app.markXiangwanProfileMutation();
        if (
          typeof app.setXiangwanProfile === "function" &&
          typeof app.getXiangwanProfile === "function"
        ) {
          app.setXiangwanProfile({
            ...(app.getXiangwanProfile() || { avatarUrl: "" }),
            nickname: saved.nickname,
            etag: saved.principal_profile_etag,
          });
        }
        if (typeof app.refreshXiangwanProfile === "function") await app.refreshXiangwanProfile();
        if (
          !sameBinding(this._binding, readBinding(app)) ||
          !app.completeContactSetup(
            { contactName: nickname.nickname, contactPhone: phone },
            this._setupId,
          )
        ) {
          this.setData({ formError: "登录状态已变化，请重新登录" });
          return;
        }
        this._completed = true;
        if (typeof wx !== "undefined" && typeof wx.navigateBack === "function") {
          wx.navigateBack({
            delta: 1,
            fail: () => {
              if (typeof wx.switchTab === "function") wx.switchTab({ url: "/pages/my/index" });
            },
          });
        }
      } catch (error) {
        if (this._active)
          this.setData({
            optionalError: savingOptional
              ? getUserMessage(error, "可选资料提交失败，请重试或跳过")
              : "",
            formError: savingOptional ? "" : getUserMessage(error, "保存失败，请稍后重试"),
          });
      } finally {
        if (this._active) this.setData({ saving: false });
      }
    },

    skipOptionalAndSave() {
      void this.saveContact(true);
    },
  };
}

if (typeof Page === "function") Page(createContactSetupPageDefinition());
module.exports = { createContactSetupPageDefinition };
