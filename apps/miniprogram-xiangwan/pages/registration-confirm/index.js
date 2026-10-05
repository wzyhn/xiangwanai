"use strict";

const { canonicalUUID, xiangwanApi } = require("../../services/xiangwan-api");
const { projectCouponsPage } = require("../../features/coupons/model");
const { getUserMessage, isUnknownWriteResult } = require("../../utils/errors");
const { formatDateTime, formatMoney } = require("../../utils/format");

function resolveApp() {
  if (typeof getApp !== "function") return null;
  try {
    return getApp();
  } catch (_error) {
    return null;
  }
}

function optionalSessionId(value) {
  try {
    return canonicalUUID(value, "活动场次");
  } catch (_error) {
    return "";
  }
}

function couponAppliesToDraft(coupon, presentation) {
  if (
    !coupon ||
    coupon.state !== "available" ||
    coupon.usable !== true ||
    coupon.correctionRequired
  )
    return false;
  const priceCents = Number(presentation && presentation.priceCents);
  if (
    !Number.isSafeInteger(priceCents) ||
    coupon.minimumOrderCents > priceCents ||
    coupon.faceValueCents > priceCents
  )
    return false;
  const applicability = coupon.applicability || {};
  if (applicability.scopeType === "series") {
    return (
      Boolean(applicability.seriesId) &&
      applicability.seriesId === String(presentation.seriesId || "").toLowerCase()
    );
  }
  if (applicability.scopeType === "activity_type") {
    return (
      Boolean(applicability.activityType) &&
      applicability.activityType === String(presentation.activityTypeCode || "").trim()
    );
  }
  return false;
}

function draftForCoupon(draft, coupon) {
  const presentation = { ...(draft.presentation || {}) };
  const priceCents = Number(presentation.priceCents);
  const discountCents = coupon ? coupon.faceValueCents : 0;
  presentation.couponDiscountText = discountCents ? formatMoney(discountCents) : "";
  presentation.payableText = formatMoney(Math.max(0, priceCents - discountCents));
  const payload = { ...(draft.payload || {}) };
  if (coupon) payload.coupon_id = coupon.couponId;
  else delete payload.coupon_id;
  return { ...draft, payload, presentation };
}

function registrationConflictReason(error) {
  if (Number(error && error.statusCode) !== 409) return "";
  const reason = String(
    error && error.body && error.body.data && error.body.data.reason ? error.body.data.reason : "",
  ).trim();
  return [
    "registration_facts_changed",
    "registration_already_open",
    "idempotency_key_conflict",
  ].includes(reason)
    ? reason
    : "unknown_conflict";
}

function createRegistrationConfirmPageDefinition(api = xiangwanApi) {
  return {
    data: {
      draft: null,
      invalidMessage: "",
      submitMessage: "",
      submitting: false,
      submitted: false,
      conflicted: false,
      conflictAction: "",
      contactPolicyExpanded: false,
      coupons: [],
      selectedCouponId: "",
      couponLoading: false,
      couponMessage: "",
    },

    onLoad(options = {}) {
      this._active = true;
      const app = resolveApp();
      const draft =
        app && typeof app.getRegistrationDraft === "function" ? app.getRegistrationDraft() : null;
      const routeSessionId = optionalSessionId(options.session_id);
      const draftSessionId = optionalSessionId(draft && draft.sessionId);
      this._sessionId = routeSessionId || draftSessionId;
      if (
        !draft ||
        !draft.payload ||
        !draftSessionId ||
        !draft.idempotencyKey ||
        (routeSessionId && routeSessionId !== draftSessionId)
      ) {
        this.setData({ invalidMessage: "报名草稿已失效，请返回活动详情重新填写" });
        return;
      }
      this._draft = draft;
      this.setData({
        draft: {
          ...draft.presentation,
          startAt: formatDateTime(draft.presentation.startAt),
          endAt: formatDateTime(draft.presentation.endAt),
        },
      });
      void this.loadCoupons();
    },

    onUnload() {
      this._active = false;
    },

    async loadCoupons() {
      if (!this._draft || typeof api.getMyCoupons !== "function") return;
      this.setData({ couponLoading: true, couponMessage: "" });
      try {
        const page = projectCouponsPage(await api.getMyCoupons({ state: "available", limit: 100 }));
        if (!this._active || !this._draft) return;
        const coupons = page.items
          .filter((coupon) => couponAppliesToDraft(coupon, this._draft.presentation))
          .sort((left, right) => right.faceValueCents - left.faceValueCents);
        this.setData({ coupons, couponLoading: false });
      } catch (error) {
        if (this._active)
          this.setData({
            couponLoading: false,
            couponMessage: getUserMessage(error, "优惠券暂时无法读取"),
          });
      }
    },

    selectCoupon(event) {
      if (!this._draft || this.data.submitting || this.data.submitted) return;
      const couponID = String(event.currentTarget.dataset.couponId || "")
        .trim()
        .toLowerCase();
      const coupon = couponID ? this.data.coupons.find((item) => item.couponId === couponID) : null;
      if (couponID && !coupon) return;
      this._draft = draftForCoupon(this._draft, coupon || null);
      this.setData({
        draft: {
          ...this._draft.presentation,
          startAt: formatDateTime(this._draft.presentation.startAt),
          endAt: formatDateTime(this._draft.presentation.endAt),
        },
        selectedCouponId: coupon ? coupon.couponId : "",
      });
    },

    openPoliciesPage() {
      if (typeof wx !== "undefined" && typeof wx.navigateTo === "function") {
        wx.navigateTo({ url: "/pages/policies/index" });
      }
    },

    submitRegistration() {
      if (!this._draft || this._submitPromise) return this._submitPromise;
      this.setData({ submitting: true, submitMessage: "" });
      const draft = this._draft;
      this._submitPromise = api
        .createRegistration(draft.sessionId, draft.payload, draft.idempotencyKey)
        .then((receipt) => {
          if (!this._active) return receipt;
          const app = resolveApp();
          if (app && typeof app.setRegistrationReceipt === "function") {
            app.setRegistrationReceipt(receipt);
            app.clearRegistrationDraft();
          }
          this._draft = null;
          this.setData({ draft: null, submitted: true, submitMessage: "报名已提交" });
          if (receipt.order && Number(receipt.order.payable_cents) > 0) {
            this.openPaymentOrder(receipt.order.order_id);
          } else {
            this.openRegistrationSuccess(receipt, draft.presentation);
          }
          return receipt;
        })
        .catch((error) => {
          if (!this._active) return null;
          const conflictReason = registrationConflictReason(error);
          if (conflictReason === "registration_facts_changed") {
            const app = resolveApp();
            if (app && typeof app.markRegistrationConflict === "function") {
              app.markRegistrationConflict(draft.sessionId);
            }
            this.setData({
              conflicted: true,
              conflictAction: "refresh_form",
              submitMessage: "活动或报名规则已变化；已保留填写内容，请返回报名表刷新并重新核对",
            });
          } else if (conflictReason === "registration_already_open") {
            this.discardTerminalDraft();
            this.setData({
              conflicted: true,
              conflictAction: "open_registrations",
              submitMessage: "你已有一笔有效报名，请前往“我的报名”查看最新进度",
            });
          } else if (conflictReason === "idempotency_key_conflict") {
            this.discardTerminalDraft();
            this.setData({
              conflicted: true,
              conflictAction: "new_submission",
              submitMessage: "这次提交已被使用，请返回报名表重新核对后再次提交",
            });
          } else if (conflictReason === "unknown_conflict") {
            this.discardTerminalDraft();
            this.setData({
              conflicted: true,
              conflictAction: "open_registrations",
              submitMessage: "报名状态发生冲突，请先到“我的报名”确认是否已提交",
            });
          } else {
            this.setData({
              submitMessage: isUnknownWriteResult(error)
                ? "提交结果尚未确认，请保持本页并使用同一操作安全重试"
                : getUserMessage(error, "报名提交失败，请稍后重试"),
            });
          }
          return null;
        })
        .finally(() => {
          this._submitPromise = null;
          if (this._active) this.setData({ submitting: false });
        });
      return this._submitPromise;
    },

    openPrivacyContract() {
      if (typeof wx === "undefined" || typeof wx.openPrivacyContract !== "function") {
        this.setData({ submitMessage: "微信隐私保护指引暂不可查看，请稍后重试" });
        return;
      }
      wx.openPrivacyContract({
        success: () => {
          if (this._active) this.setData({ submitMessage: "" });
        },
        fail: () => {
          if (this._active) {
            this.setData({ submitMessage: "微信隐私保护指引打开失败，请稍后重试" });
          }
        },
      });
    },

    toggleContactPolicy() {
      if (!this.data.draft || !this.data.draft.contactPolicyText) return;
      this.setData({ contactPolicyExpanded: !this.data.contactPolicyExpanded });
    },

    discardTerminalDraft() {
      const app = resolveApp();
      if (app && typeof app.clearRegistrationDraft === "function") {
        app.clearRegistrationDraft();
      }
      this._draft = null;
    },

    backToDetail() {
      if (typeof wx !== "undefined" && typeof wx.navigateBack === "function") {
        wx.navigateBack({ delta: 2, fail: () => this.openSessionDetailOrHome() });
        return;
      }
      this.openSessionDetailOrHome();
    },

    backToForm() {
      if (typeof wx !== "undefined" && typeof wx.navigateBack === "function") {
        wx.navigateBack({ delta: 1, fail: () => this.openRegistrationFormOrHome() });
        return;
      }
      this.openRegistrationFormOrHome();
    },

    openMyRegistrations() {
      if (typeof wx === "undefined") return;
      const url = "/pages/my-registrations/index";
      if (typeof wx.reLaunch === "function") {
        wx.reLaunch({ url });
      } else if (typeof wx.redirectTo === "function") {
        wx.redirectTo({ url });
      } else if (typeof wx.navigateTo === "function") {
        wx.navigateTo({ url });
      }
    },

    openPaymentOrder(value) {
      let orderId;
      try {
        orderId = canonicalUUID(value, "订单");
      } catch (_error) {
        this.openMyRegistrations();
        return;
      }
      if (typeof wx === "undefined") return;
      const url = `/pages/order-detail/index?order_id=${encodeURIComponent(orderId)}`;
      if (typeof wx.reLaunch === "function") {
        wx.reLaunch({ url, fail: () => this.openMyRegistrations() });
      } else if (typeof wx.redirectTo === "function") {
        wx.redirectTo({ url });
      } else {
        this.openMyRegistrations();
      }
    },

    // 提交成功改跳独立成功页:registration_id 供成功页串联到报名详情,
    // 成功卡的场次标题/时间/地点取提交时刻的草稿展示快照(公共事实,非敏感)。
    // 回执仍留在 app 内存,报名详情页 consumeRegistrationReceipt 后展示顶部 banner。
    openRegistrationSuccess(receipt, presentation = {}) {
      if (typeof wx === "undefined") return;
      const params = [`registration_id=${encodeURIComponent(receipt.registration_id)}`];
      const chained = {
        title: presentation.title,
        session_title: presentation.sessionTitle,
        start_at: presentation.startAt,
        end_at: presentation.endAt,
        venue: presentation.venue,
      };
      Object.keys(chained).forEach((key) => {
        const value = String(chained[key] || "").trim();
        if (value) params.push(`${key}=${encodeURIComponent(value)}`);
      });
      const url = `/pages/registration-success/index?${params.join("&")}`;
      if (typeof wx.reLaunch === "function") {
        wx.reLaunch({
          url,
          fail: () => this.openMyRegistrations(),
        });
      } else {
        this.openMyRegistrations();
      }
    },

    openSessionDetailOrHome() {
      if (this._sessionId && typeof wx !== "undefined" && typeof wx.redirectTo === "function") {
        wx.redirectTo({
          url: `/pages/session-detail/index?session_id=${encodeURIComponent(this._sessionId)}`,
        });
        return;
      }
      this.openHome();
    },

    openRegistrationFormOrHome() {
      if (this._sessionId && typeof wx !== "undefined" && typeof wx.redirectTo === "function") {
        wx.redirectTo({
          url: `/pages/registration/index?session_id=${encodeURIComponent(this._sessionId)}`,
        });
        return;
      }
      this.openHome();
    },

    openHome() {
      if (typeof wx === "undefined") return;
      if (typeof wx.reLaunch === "function") {
        wx.reLaunch({ url: "/pages/index/index" });
      } else if (typeof wx.switchTab === "function") {
        wx.switchTab({ url: "/pages/index/index" });
      }
    },
  };
}

if (typeof Page === "function") Page(createRegistrationConfirmPageDefinition());

module.exports = {
  createRegistrationConfirmPageDefinition,
  optionalSessionId,
  registrationConflictReason,
};
