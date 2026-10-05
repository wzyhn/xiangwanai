"use strict";

const {
  applyCancellationReceiptToDetail,
  detailReflectsCancellationReceipt,
  projectRegistrationCancellation,
} = require("../../features/cancellation/model");
const { projectRegistrationDetail } = require("../../features/registrations/model");
const { findCancellationPolicy } = require("../../features/registration/model");
const {
  createCheckinCredentialPageDefinition,
  defaultRenderQr,
  defaultClearQr,
} = require("../../features/checkin-credential/controller");
const { canonicalUUID, xiangwanApi } = require("../../services/xiangwan-api");
const { getUserMessage, isUnknownWriteResult } = require("../../utils/errors");

function resolveApp() {
  if (typeof getApp !== "function") return null;
  try {
    return getApp();
  } catch (_error) {
    return null;
  }
}

function defaultConfirmCancellation(detail = {}) {
  return new Promise((resolve, reject) => {
    if (typeof wx === "undefined" || typeof wx.showModal !== "function") {
      reject(new Error("confirmation unavailable"));
      return;
    }
    wx.showModal({
      title: "确认取消报名？",
      content:
        "取消成功后，参与资格、签到凭证和私密访问将立即失效，名额会同步释放。" +
        (detail.paymentStatus === "paid_confirmed"
          ? (detail.paymentText ? `本次申请退款金额：${detail.paymentText}。` : "") +
            "按实付金额全额申请人工退款，提交取消不代表退款已到账。"
          : ""),
      confirmText: "确认取消",
      confirmColor: "#913d50",
      cancelText: "暂不取消",
      success: (result) => resolve(Boolean(result && result.confirm)),
      fail: reject,
    });
  });
}

function createRegistrationDetailPageDefinition(api = xiangwanApi, dependencies = {}) {
  const confirmCancellation =
    typeof dependencies.confirmCancellation === "function"
      ? dependencies.confirmCancellation
      : defaultConfirmCancellation;

  // Keep the credential state machine single-sourced, while rendering it in
  // the registration detail page so the user can scan as soon as this page
  // opens. The old standalone route remains available for old deep links.
  const createController = (hostPage) =>
    createCheckinCredentialPageDefinition(api, {
      ...dependencies.checkin,
      autoRefresh: true,
      renderQr: (_controller, token, done) =>
        (dependencies.renderQr || defaultRenderQr)(hostPage, token, done),
      clearQr: () => (dependencies.clearQr || defaultClearQr)(hostPage),
    });

  return {
    data: {
      loading: true,
      errorMessage: "",
      successMessage: "",
      cancellationError: "",
      cancellationNotice: "",
      cancellationPolicyText: "",
      cancellationResult: null,
      cancellationOutcomeUnknown: false,
      cancelling: false,
      openingOrder: false,
      orderError: "",
      detail: null,
      checkin: { ...createController(null).data },
    },

    onLoad(options = {}) {
      this._active = true;
      this._checkinController = createController(this);
      this._checkinController.setData = (changes, callback) => {
        this._checkinController.data = { ...this._checkinController.data, ...changes };
        this.setData({ checkin: this._checkinController.data }, callback);
      };
      try {
        this._registrationId = canonicalUUID(options.registration_id, "报名记录");
      } catch (error) {
        this.setData({ loading: false, errorMessage: getUserMessage(error, "报名链接无效") });
        return;
      }
      this._checkinController.onLoad({ registration_id: this._registrationId });
      const app = resolveApp();
      const receipt =
        app && typeof app.consumeRegistrationReceipt === "function"
          ? app.consumeRegistrationReceipt()
          : null;
      if (receipt && receipt.registration_id === this._registrationId) {
        this.setData({ successMessage: "报名已提交，以下为最新进度" });
      }
      this._skipNextShowRefresh = true;
      void this.loadDetail();
    },

    onShow() {
      this._active = true;
      if (!this._openingOrder) this.setData({ openingOrder: false });
      if (this._skipNextShowRefresh) {
        this._skipNextShowRefresh = false;
        return;
      }
      if (this._registrationId) void this.loadDetail();
    },

    onHide() {
      this._active = false;
      this._detailGeneration = (this._detailGeneration || 0) + 1;
      if (this._checkinController) this._checkinController.onHide();
    },

    onUnload() {
      this._active = false;
      this._detailGeneration = (this._detailGeneration || 0) + 1;
      this._confirmingCancellation = false;
      if (this._checkinController) this._checkinController.onUnload();
      this._checkinController = null;
    },

    async loadDetail() {
      const generation = this._beginDetailRequest();
      const refreshingUnknownOutcome = this.data.cancellationOutcomeUnknown === true;
      this.setData({ loading: true, errorMessage: "" });
      try {
        const [detail, policies] = await Promise.all([
          this._fetchAuthoritativeDetail(),
          typeof api.getPublicPolicies === "function"
            ? Promise.resolve()
                .then(() => api.getPublicPolicies())
                .catch(() => null)
            : Promise.resolve(null),
        ]);
        if (!this._isCurrentDetailRequest(generation)) return null;
        const reconciled = this._reconcileDetailWithReceipt(detail);
        this.setData(
          {
            detail: reconciled.detail,
            cancellationPolicyText: (findCancellationPolicy(policies) || {}).content || "",
            loading: false,
            cancellationOutcomeUnknown: reconciled.awaitingSync,
            cancellationNotice: reconciled.awaitingSync
              ? "取消结果已确认，但报名详情尚未更新，请稍后刷新。"
              : this.data.cancellationResult
                ? ""
                : refreshingUnknownOutcome
                  ? "报名状态已刷新，请核对后再操作。"
                  : this.data.cancellationNotice,
          },
          () => {
            if (!this._checkinController || !this._isCurrentDetailRequest(generation)) return;
            if (reconciled.detail.checkinCredentialEligible === true) {
              this._checkinController.onShow();
            } else {
              this._checkinController.onHide();
            }
          },
        );
        return reconciled.detail;
      } catch (error) {
        if (this._isCurrentDetailRequest(generation)) {
          this.setData({ errorMessage: getUserMessage(error, "报名详情加载失败") });
        }
        return null;
      } finally {
        if (this._isCurrentDetailRequest(generation)) {
          this.setData({ loading: false });
        }
      }
    },

    async _fetchAuthoritativeDetail() {
      const app = resolveApp();
      if (!app || typeof app.ensureAuthenticated !== "function") {
        throw new Error("runtime unavailable");
      }
      await app.ensureAuthenticated();
      return projectRegistrationDetail(await api.getMyRegistrationDetail(this._registrationId));
    },

    async _refreshAfterCancellation() {
      const generation = this._beginDetailRequest();
      try {
        const detail = await this._fetchAuthoritativeDetail();
        if (!this._isCurrentDetailRequest(generation)) {
          return { status: "superseded", detail: null };
        }
        const reconciled = this._reconcileDetailWithReceipt(detail);
        this.setData({
          detail: reconciled.detail,
          loading: false,
          errorMessage: "",
          cancellationOutcomeUnknown: reconciled.awaitingSync,
          cancellationNotice: reconciled.awaitingSync
            ? "取消结果已确认，但报名详情尚未更新，请稍后刷新。"
            : this.data.cancellationResult
              ? ""
              : this.data.cancellationNotice,
        });
        return { status: "applied", detail: reconciled.detail };
      } catch (_error) {
        if (!this._isCurrentDetailRequest(generation)) {
          return { status: "superseded", detail: null };
        }
        this.setData({ loading: false });
        return { status: "failed", detail: null };
      }
    },

    _beginDetailRequest() {
      const generation = (this._detailGeneration || 0) + 1;
      this._detailGeneration = generation;
      return generation;
    },

    _isCurrentDetailRequest(generation) {
      return Boolean(this._active && generation === this._detailGeneration);
    },

    _reconcileDetailWithReceipt(detail) {
      const receipt = this.data.cancellationResult;
      if (!receipt || detailReflectsCancellationReceipt(detail, receipt)) {
        return { detail, awaitingSync: false };
      }
      const reconciled = applyCancellationReceiptToDetail(detail, receipt);
      if (reconciled === detail) return { detail, awaitingSync: false };
      return {
        awaitingSync: true,
        detail: reconciled,
      };
    },

    retry() {
      void this.loadDetail();
    },

    async openExistingOrder() {
      if (this._openingOrder || this.data.cancelling || this.data.cancellationOutcomeUnknown)
        return;
      this._openingOrder = true;
      this.setData({ openingOrder: true, orderError: "" });
      let generation;
      try {
        const detail = await this.loadDetail();
        if (!detail || !this._active) return;
        generation = this._detailGeneration;
        const app = resolveApp();
        const binding =
          app && typeof app.getXiangwanAuthBinding === "function"
            ? app.getXiangwanAuthBinding()
            : null;
        const orderId = canonicalUUID(detail.orderId, "已有订单");
        const { order } = await api.getMyOrderDetail(orderId);
        if (!this._isCurrentDetailRequest(generation)) return;
        if (binding) {
          const current = app.getXiangwanAuthBinding();
          if (
            !current ||
            current.principalId !== binding.principalId ||
            current.generation !== binding.generation
          )
            return;
        }
        if (
          order.order_id !== orderId ||
          order.registration_id !== this._registrationId ||
          order.session_id !== detail.sessionId
        ) {
          throw new Error("existing order relation conflict");
        }
        if (typeof wx === "undefined" || typeof wx.navigateTo !== "function")
          throw new Error("navigation unavailable");
        wx.navigateTo({
          url: `/pages/order-detail/index?order_id=${encodeURIComponent(orderId)}`,
          fail: () => {
            if (this._isCurrentDetailRequest(generation))
              this.setData({ orderError: "订单页面暂时无法打开，请稍后重试" });
          },
        });
      } catch (error) {
        if (this._active && (!generation || this._isCurrentDetailRequest(generation)))
          this.setData({
            orderError: getUserMessage(error, "订单暂时无法打开，请刷新核对报名状态"),
          });
      } finally {
        this._openingOrder = false;
        if (this._active) this.setData({ openingOrder: false });
      }
    },

    refreshCancellationStatus() {
      if (this.data.cancelling) return;
      void this.loadDetail();
    },

    async requestCancellation() {
      const detail = this.data.detail;
      if (
        !detail ||
        detail.canSelfCancel !== true ||
        detail.registrationId !== this._registrationId ||
        this.data.cancelling ||
        this.data.cancellationOutcomeUnknown ||
        this._confirmingCancellation
      ) {
        return null;
      }

      this._confirmingCancellation = true;
      this.setData({ cancellationError: "" });
      let confirmed = false;
      try {
        confirmed = await confirmCancellation(this.data.detail);
      } catch (error) {
        if (this._active) {
          this.setData({
            cancellationError: getUserMessage(error, "暂时无法打开取消确认，请稍后重试"),
          });
        }
      } finally {
        this._confirmingCancellation = false;
      }
      if (!confirmed || !this._active) return null;
      return this._submitCancellation(detail);
    },

    async _submitCancellation(detailAtConfirmation) {
      if (this.data.cancelling) return null;
      if (this._checkinController) this._checkinController.onHide();
      this.setData({
        cancelling: true,
        cancellationError: "",
        cancellationNotice: "",
        cancellationResult: null,
      });
      try {
        const app = resolveApp();
        if (!app || typeof app.ensureAuthenticated !== "function") {
          throw new Error("runtime unavailable");
        }
        await app.ensureAuthenticated();
        const result = projectRegistrationCancellation(
          await api.cancelRegistration(this._registrationId),
          this._registrationId,
          detailAtConfirmation.sessionId,
        );
        if (!this._active) return null;

        const reconciledDetail = applyCancellationReceiptToDetail(this.data.detail, result);
        this.setData({
          cancellationResult: result,
          successMessage: result.title,
          detail: reconciledDetail,
        });
        const refresh = await this._refreshAfterCancellation();
        if (refresh.status === "failed" && this._active) {
          this.setData({
            cancellationNotice: "取消已受理，但最新报名状态加载失败，请点击刷新后核对。",
            cancellationOutcomeUnknown: true,
          });
        }
        return result;
      } catch (error) {
        if (!this._active) return null;
        const statusCode = Number((error && error.statusCode) || 0);
        if (isUnknownWriteResult(error)) {
          this.setData({
            cancellationNotice: "暂时无法确认取消结果，正在刷新报名状态。",
            cancellationOutcomeUnknown: true,
          });
          const refresh = await this._refreshAfterCancellation();
          if (this._active && refresh.status !== "superseded") {
            const refreshed = refresh.status === "applied" ? refresh.detail : null;
            this.setData({
              cancellationNotice: refreshed
                ? refreshed.participationStatus === "cancelled"
                  ? "报名已取消，请核对下方退款等处理进度。"
                  : "暂时无法确认取消结果，已刷新报名状态，请核对后再操作。"
                : "取消请求结果未确认，请先刷新报名状态；不要连续重复提交。",
              cancellationOutcomeUnknown: !refreshed,
            });
          }
        } else if (statusCode === 409) {
          const refresh = await this._refreshAfterCancellation();
          if (this._active && refresh.status !== "superseded") {
            const refreshed = refresh.status === "applied" ? refresh.detail : null;
            this.setData({
              cancellationNotice: refreshed
                ? "报名状态已变化，已刷新；如仍可取消，请重新确认。"
                : "报名状态已变化，请刷新后重新确认。",
              cancellationOutcomeUnknown: !refreshed,
            });
          }
        } else {
          this.setData({ cancellationError: getUserMessage(error, "取消报名失败") });
        }
        return null;
      } finally {
        if (this._active) this.setData({ cancelling: false });
      }
    },

    refreshCredential() {
      if (this._checkinController) this._checkinController.refreshCredential();
    },

    openPoliciesPage() {
      if (typeof wx !== "undefined" && typeof wx.navigateTo === "function")
        wx.navigateTo({ url: "/pages/policies/index" });
    },

    backToHome() {
      if (typeof wx !== "undefined" && typeof wx.switchTab === "function") {
        wx.switchTab({ url: "/pages/index/index" });
      }
    },
  };
}

if (typeof Page === "function") Page(createRegistrationDetailPageDefinition());

module.exports = { createRegistrationDetailPageDefinition, defaultConfirmCancellation };
