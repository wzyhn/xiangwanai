"use strict";

const { xiangwanApi } = require("../../services/xiangwan-api");
const {
  findManualContactPolicy,
  findCancellationPolicy,
  findPublishedPolicyVersion,
} = require("../../features/registration/model");
const { getUserMessage } = require("../../utils/errors");

function createPoliciesPageDefinition(api = xiangwanApi) {
  return {
    data: {
      loading: true,
      errorMessage: "",
      privacyVersion: "",
      contactVersion: "",
      contactText: "",
      cancellationVersion: "",
      cancellationText: "",
    },
    onLoad() {
      this._active = true;
      void this.loadPolicies();
    },
    onUnload() {
      this._active = false;
    },
    async loadPolicies() {
      this.setData({ loading: true, errorMessage: "" });
      try {
        const policies = await api.getPublicPolicies();
        if (!this._active) return;
        const contact = findManualContactPolicy(policies);
        const cancellation = findCancellationPolicy(policies);
        this.setData({
          privacyVersion: findPublishedPolicyVersion(policies, "privacy"),
          contactVersion: findPublishedPolicyVersion(policies, "manual_contact"),
          contactText: contact ? contact.content : "联系人政策尚未发布，请联系活动方。",
          cancellationVersion: cancellation ? cancellation.version : "",
          cancellationText: cancellation
            ? cancellation.content
            : "付费报名取消与退款规则尚未发布，请联系活动方。",
        });
      } catch (error) {
        if (this._active)
          this.setData({ errorMessage: getUserMessage(error, "政策加载失败，请重试") });
      } finally {
        if (this._active) this.setData({ loading: false });
      }
    },
    openPrivacyContract() {
      if (typeof wx === "undefined" || typeof wx.openPrivacyContract !== "function") {
        this.setData({ errorMessage: "当前微信版本暂不支持打开隐私指引，请更新微信。" });
        return;
      }
      wx.openPrivacyContract({
        fail: () => {
          if (this._active) this.setData({ errorMessage: "隐私指引打开失败，请稍后重试。" });
        },
      });
    },
  };
}

if (typeof Page === "function") Page(createPoliciesPageDefinition());
module.exports = { createPoliciesPageDefinition };
