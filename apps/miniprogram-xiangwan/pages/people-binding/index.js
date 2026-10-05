"use strict";
const { xiangwanApi } = require("../../services/xiangwan-api");
const { generateIdempotencyKey } = require("../../api/http");
const { getUserMessage } = require("../../utils/errors");
function app() {
  try {
    return typeof getApp === "function" ? getApp() : null;
  } catch (_error) {
    return null;
  }
}
function binding(value) {
  return value && typeof value.getXiangwanAuthBinding === "function"
    ? value.getXiangwanAuthBinding()
    : null;
}
function same(left, right) {
  return Boolean(
    left &&
      right &&
      left.principalId &&
      left.principalId === right.principalId &&
      left.generation === right.generation,
  );
}
function createPeopleBindingPageDefinition(api = xiangwanApi, makeKey = generateIdempotencyKey) {
  return {
    data: {
      code: "",
      preview: null,
      consent: false,
      loading: false,
      submitting: false,
      errorMessage: "",
      notice: "",
      pending: false,
    },
    onLoad() {
      this._active = true;
      this._request = 0;
    },
    onShow() {
      this._active = true;
    },
    onHide() {
      this.clearPrivateState();
    },
    onUnload() {
      this.clearPrivateState();
    },
    clearPrivateState() {
      this._active = false;
      this._request++;
      this._pending = null;
      this._binding = null;
      this.setData({
        code: "",
        preview: null,
        consent: false,
        pending: false,
        submitting: false,
        loading: false,
      });
    },
    onCodeInput(event) {
      if (this._pending || this._working) return;
      this._request++;
      this.setData({
        code: String(event.detail.value || "").trim(),
        preview: null,
        consent: false,
        errorMessage: "",
        notice: "",
      });
    },
    onConsentChange(event) {
      if (this._pending || this._working) return;
      this.setData({ consent: (event.detail.value || []).includes("consent") });
    },
    async loadPreview() {
      if (this._working || this._pending) return;
      const code = this.data.code;
      if (
        !/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}\.[A-Za-z0-9_-]{43}$/.test(
          code,
        )
      ) {
        this.setData({ errorMessage: "请填写管理员提供的完整邀请码" });
        return;
      }
      this._working = true;
      const version = ++this._request;
      this.setData({ loading: true, preview: null, consent: false, errorMessage: "", notice: "" });
      try {
        const runtime = app();
        if (!runtime || typeof runtime.ensureAuthenticated !== "function")
          throw new Error("登录服务暂时不可用");
        await runtime.ensureAuthenticated();
        if (!this._active || version !== this._request) return;
        const owner = binding(runtime);
        if (!owner) throw new Error("请重新登录后确认");
        const value = await api.previewPeopleBinding(code);
        if (!this._active || version !== this._request || !same(owner, binding(runtime))) return;
        this._binding = owner;
        this.setData({ preview: value });
      } catch (error) {
        if (this._active && version === this._request)
          this.setData({
            errorMessage: getUserMessage(
              error,
              "邀请码已过期、失效或资料已变化，请联系管理员重新邀请",
            ),
          });
      } finally {
        this._working = false;
        if (this._active && version === this._request) this.setData({ loading: false });
      }
    },
    async confirmBinding() {
      if (this._working) return;
      const runtime = app();
      if (!this._active || !same(this._binding, binding(runtime))) {
        this._pending = null;
        this.setData({
          preview: null,
          consent: false,
          pending: false,
          errorMessage: "登录状态已变化，请重新读取邀请码",
        });
        return;
      }
      if (!this._pending) {
        if (!this.data.preview || !this.data.consent) {
          this.setData({ errorMessage: "请核对人物资料并确认这是本人" });
          return;
        }
        this._pending = {
          operation: makeKey(),
          code: this.data.code,
          expected_version: this.data.preview.profile_version,
          peopleProfileId: this.data.preview.people_profile_id,
        };
      }
      const pending = this._pending,
        owner = this._binding,
        version = ++this._request;
      this._working = true;
      this.setData({ submitting: true, pending: true, errorMessage: "" });
      try {
        const value = await api.acceptPeopleBinding(
          { code: pending.code, expected_version: pending.expected_version, consent: true },
          pending.operation,
        );
        if (!this._active || version !== this._request || !same(owner, binding(runtime))) return;
        if (value.people_profile_id !== pending.peopleProfileId)
          throw new Error("绑定回执身份不一致");
        this._pending = null;
        this.setData({
          code: "",
          preview: null,
          consent: false,
          pending: false,
          notice:
            value.status === "active"
              ? "本人资料绑定已确认，可返回我的权益查看"
              : "该绑定已撤销，原记录保留，请联系管理员核对",
        });
      } catch (error) {
        if (!this._active || version !== this._request || !same(owner, binding(runtime))) return;
        const status = Number(error && error.statusCode);
        if ([400, 401, 403, 404, 409, 413, 415, 422].includes(status)) {
          this._pending = null;
          this.setData({ pending: false, preview: null, consent: false });
        }
        this.setData({
          errorMessage: getUserMessage(
            error,
            this._pending
              ? "确认结果暂时未知，请点击核对原请求；离开页面后可在我的权益查看绑定状态"
              : "确认未完成，请联系管理员核对邀请码",
          ),
        });
      } finally {
        this._working = false;
        if (this._active && version === this._request) this.setData({ submitting: false });
      }
    },
    backToBenefits() {
      if (typeof wx !== "undefined") wx.navigateBack({ delta: 1 });
    },
  };
}
if (typeof Page === "function") Page(createPeopleBindingPageDefinition());
module.exports = { createPeopleBindingPageDefinition };
