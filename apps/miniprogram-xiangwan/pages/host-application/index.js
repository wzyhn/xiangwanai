"use strict";
const { xiangwanApi } = require("../../services/xiangwan-api");
const { projectMyBenefits } = require("../../features/benefits/model");
const { findPublishedPolicyVersion } = require("../../features/registration/model");
const { getUserMessage } = require("../../utils/errors");
function runtime() {
  try {
    return typeof getApp === "function" ? getApp() : null;
  } catch (_e) {
    return null;
  }
}
function owner(app) {
  return app && typeof app.getXiangwanAuthBinding === "function"
    ? app.getXiangwanAuthBinding()
    : null;
}
function same(a, b) {
  return !!(
    a &&
    b &&
    a.principalId &&
    a.principalId === b.principalId &&
    a.generation === b.generation
  );
}
function isSubmitDisabled(state) {
  return Boolean(state.submitting || !state.privacyVersion || (!state.consent && !state.pending));
}
function createHostApplicationPageDefinition(api = xiangwanApi) {
  return {
    data: {
      benefits: null,
      loading: false,
      submitting: false,
      consent: false,
      privacyVersion: "",
      submitDisabled: true,
      personal_introduction: "",
      relevant_experience: "",
      availability: "",
      contact_method: "",
      errorMessage: "",
      notice: "",
      pending: false,
    },
    onLoad() {
      this._active = true;
      this._revision = 0;
    },
    onShow() {
      this._active = true;
      void this.load();
    },
    onHide() {
      this.clear();
    },
    onUnload() {
      this.clear();
    },
    clear() {
      this._active = false;
      this._revision++;
      this._pending = null;
      this._owner = null;
      this.setData({
        benefits: null,
        consent: false,
        personal_introduction: "",
        relevant_experience: "",
        availability: "",
        contact_method: "",
        pending: false,
        submitting: false,
        submitDisabled: true,
        loading: false,
      });
    },
    async load() {
      if (this._working || this._pending) return;
      this._working = true;
      const rev = ++this._revision;
      this.setData({ loading: true, errorMessage: "", consent: false, submitDisabled: true });
      try {
        const app = runtime();
        if (!app || typeof app.ensureAuthenticated !== "function") throw new Error("请重新登录");
        await app.ensureAuthenticated();
        const binding = owner(app);
        if (!binding) throw new Error("请重新登录");
        const [raw, policies] = await Promise.all([api.getMyBenefits(), api.getPublicPolicies()]);
        if (!this._active || rev !== this._revision || !same(binding, owner(app))) return;
        this._owner = binding;
        const privacy = findPublishedPolicyVersion(policies, "privacy");
        this.setData({
          benefits: projectMyBenefits(raw),
          privacyVersion: privacy,
          submitDisabled: isSubmitDisabled({
            ...this.data,
            consent: false,
            pending: false,
            submitting: false,
            privacyVersion: privacy,
          }),
        });
      } catch (e) {
        if (this._active && rev === this._revision)
          this.setData({ errorMessage: getUserMessage(e, "申请信息加载失败，请重试") });
      } finally {
        this._working = false;
        if (this._active && rev === this._revision) this.setData({ loading: false });
      }
    },
    onInput(e) {
      if (this._pending || this._working) return;
      const field = e.currentTarget.dataset.field;
      if (
        ["personal_introduction", "relevant_experience", "availability", "contact_method"].includes(
          field,
        )
      )
        this.setData({ [field]: String(e.detail.value || "") });
    },
    onConsent(e) {
      if (!this._pending && !this._working) {
        const consent = (e.detail.value || []).includes("consent");
        this.setData({
          consent,
          submitDisabled: isSubmitDisabled({ ...this.data, consent }),
        });
      }
    },
    openPrivacy() {
      if (typeof wx !== "undefined" && typeof wx.openPrivacyContract === "function")
        wx.openPrivacyContract({
          fail: () => this.setData({ errorMessage: "隐私指引暂时无法打开" }),
        });
      else this.setData({ errorMessage: "请更新微信后查看隐私指引" });
    },
    async submit() {
      if (this._working) return;
      const benefits = this.data.benefits;
      if (!this._pending) {
        if (
          !benefits ||
          !benefits.canApplyForHost ||
          !benefits.hostRules.cycle ||
          !benefits.hostRules.policyVersion ||
          !this.data.privacyVersion ||
          !this.data.consent
        ) {
          this.setData({ errorMessage: "请先确认当前规则与隐私用途" });
          return;
        }
        const payload = {
          expected_cycle: benefits.hostRules.cycle,
          expected_policy_version: benefits.hostRules.policyVersion,
          expected_privacy_policy_version: this.data.privacyVersion,
          consent: true,
        };
        for (const [field, max] of [
          ["personal_introduction", 4000],
          ["relevant_experience", 4000],
          ["availability", 1000],
          ["contact_method", 500],
        ]) {
          payload[field] = this.data[field].trim();
          if (!payload[field] || Array.from(payload[field]).length > max) {
            this.setData({ errorMessage: "请完整填写申请资料并检查字数" });
            return;
          }
        }
        this._pending = { kind: "apply", payload };
      }
      await this.runPending();
    },
    async withdraw() {
      if (this._working || this._pending) return;
      const a = this.data.benefits && this.data.benefits.currentApplication;
      if (!a || a.status !== "pending") return;
      if (typeof wx === "undefined" || typeof wx.showModal !== "function") return;
      const rev = this._revision,
        binding = this._owner;
      wx.showModal({
        title: "撤回申请",
        content: "确认撤回当前待审核申请？申请记录将保留。",
        success: (res) => {
          if (
            res.confirm &&
            this._active &&
            rev === this._revision &&
            !this._working &&
            !this._pending &&
            same(binding, owner(runtime()))
          ) {
            this._pending = { kind: "withdraw", id: a.applicationId, version: a.version };
            void this.runPending();
          }
        },
      });
    },
    async runPending() {
      if (this._working || !this._pending) return;
      if (!this._active || !same(this._owner, owner(runtime()))) {
        this._pending = null;
        this.setData({
          benefits: null,
          pending: false,
          errorMessage: "登录状态已变化，请重新加载",
        });
        return;
      }
      this._working = true;
      const rev = this._revision,
        pending = this._pending;
      this.setData({
        submitting: true,
        pending: true,
        submitDisabled: true,
        errorMessage: "",
        notice: "",
      });
      try {
        const result =
          pending.kind === "apply"
            ? await api.applyHostApplication(pending.payload)
            : await api.withdrawHostApplication(pending.id, pending.version);
        if (!this._active || rev !== this._revision || !same(this._owner, owner(runtime()))) return;
        this._pending = null;
        this.setData({
          pending: false,
          consent: false,
          submitDisabled: true,
          personal_introduction: "",
          relevant_experience: "",
          availability: "",
          contact_method: "",
          notice:
            result.application.application_status === "pending"
              ? "申请已提交，请等待审核"
              : result.application.application_status === "withdrawn"
                ? "申请已撤回"
                : "申请已有审核结果，请查看记录",
        });
      } catch (e) {
        if (this._active && rev === this._revision) {
          if (
            [400, 401, 403, 404, 409, 413, 415, 422].includes(
              Number((e && e.status) || (e && e.statusCode)),
            )
          ) {
            this._pending = null;
            this.setData({ pending: false });
          }
          this.setData({
            errorMessage: getUserMessage(
              e,
              "操作结果未知，请重试原请求；离开后可在我的权益核对记录",
            ),
          });
        }
      } finally {
        this._working = false;
        if (this._active && rev === this._revision) {
          this.setData({
            submitting: false,
            submitDisabled: isSubmitDisabled({ ...this.data, submitting: false }),
          });
          if (!this._pending) await this.load();
        }
      }
    },
    retryPending() {
      return this.runPending();
    },
  };
}
if (typeof Page === "function") Page(createHostApplicationPageDefinition());
module.exports = { createHostApplicationPageDefinition };
