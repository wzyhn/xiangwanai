"use strict";

const { projectOrderDetail } = require("../../features/orders/model");
const {
  readPaymentOperation,
  savePaymentOperation,
  clearPaymentOperation,
} = require("../../features/orders/payment-operation");
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

function sameAuthBinding(app, binding) {
  if (!app || typeof app.getXiangwanAuthBinding !== "function") return false;
  const current = app.getXiangwanAuthBinding();
  return Boolean(
    current &&
      current.principalId === binding.principalId &&
      current.generation === binding.generation,
  );
}

function invokeWeChatPayment(parameters) {
  if (typeof wx === "undefined" || typeof wx.requestPayment !== "function") {
    const error = new Error("WeChat payment unavailable");
    error.paymentInvocationFailed = true;
    error.userMessage = "当前环境无法拉起微信支付，请重新进入订单后重试";
    throw error;
  }
  return new Promise((resolve, reject) => {
    try {
      wx.requestPayment({
        timeStamp: parameters.timeStamp,
        nonceStr: parameters.nonceStr,
        package: parameters.package,
        signType: parameters.signType,
        paySign: parameters.paySign,
        success: () => resolve({ completed: true }),
        fail: (failure = {}) => {
          const errMsg = String(failure.errMsg || failure.message || "").trim();
          if (/cancel/i.test(errMsg)) {
            // A cancellation is not a payment fact. Keep the server query so a
            // payment that reached WeChat just before the callback can settle.
            resolve({ cancelled: true });
            return;
          }
          const error = new Error("WeChat payment invocation failed");
          error.code = "wechat_payment_invocation_failed";
          error.paymentInvocationFailed = true;
          error.errCode = failure.errCode;
          error.userMessage = /parameter|invalid|signature|sign/i.test(errMsg)
            ? "微信支付参数无效，请返回订单后重新支付"
            : "微信支付未拉起，请重新进入订单后重试";
          reject(error);
        },
      });
    } catch (_error) {
      const error = new Error("WeChat payment invocation failed");
      error.code = "wechat_payment_invocation_failed";
      error.paymentInvocationFailed = true;
      error.userMessage = "微信支付未拉起，请重新进入订单后重试";
      reject(error);
    }
  });
}

function queryMessage(result) {
  if (result.next_action === "payment_confirmed") return "支付已由服务端确认，请查看报名状态。";
  if (result.next_action === "payment_closed") return "订单已关闭，请查看最新订单状态。";
  if (result.next_action === "refund_processing")
    return "支付已核实，报名资格未保留，退款正在处理。";
  if (result.retry_payment_allowed === true) {
    return "微信确认当前未付款；请先核对扣款记录，再决定是否继续支付。";
  }
  return "支付结果仍在确认中，请勿重复付款，稍后可再次查询。";
}

function createOrderDetailPageDefinition(api = xiangwanApi) {
  return {
    data: {
      loading: true,
      showMore: false,
      errorMessage: "",
      detail: null,
      paymentBusy: false,
      paymentMessage: "",
      paymentNeedsQuery: false,
    },

    onLoad(options = {}) {
      this._active = true;
      try {
        this._orderId = canonicalUUID(options.order_id, "订单");
      } catch (error) {
        this.setData({ loading: false, errorMessage: getUserMessage(error, "订单链接无效") });
        return;
      }
      this._skipNextShowRefresh = true;
      void this.loadDetail();
    },

    onShow() {
      this._active = true;
      if (this._detailBinding && !sameAuthBinding(resolveApp(), this._detailBinding)) {
        this._detailBinding = null;
        this._freshRetryPermit = null;
        this._paymentAvailable = false;
        this.setData({ detail: null, paymentMessage: "", paymentNeedsQuery: false });
      }
      if (this._skipNextShowRefresh) {
        this._skipNextShowRefresh = false;
        return;
      }
      if (this._orderId) void this.loadDetail();
    },

    onHide() {
      this._active = false;
      this._freshRetryPermit = null;
      this._requestVersion = Number(this._requestVersion || 0) + 1;
    },

    onUnload() {
      this._active = false;
      this._freshRetryPermit = null;
      this._requestVersion = Number(this._requestVersion || 0) + 1;
    },

    onPullDownRefresh() {
      return this.loadDetail();
    },

    async loadDetail() {
      if (!this._orderId) return;
      const version = Number(this._requestVersion || 0) + 1;
      this._requestVersion = version;
      this.setData({ loading: true, errorMessage: "" });
      try {
        const app = resolveApp();
        if (!app || typeof app.ensureAuthenticated !== "function") {
          throw new Error("runtime unavailable");
        }
        await app.ensureAuthenticated();
        const binding =
          typeof app.getXiangwanAuthBinding === "function" ? app.getXiangwanAuthBinding() : null;
        if (typeof app.getXiangwanAuthBinding === "function" && (!binding || !binding.principalId))
          throw new Error("auth unavailable");
        if (this._detailBinding && !sameAuthBinding(app, this._detailBinding)) {
          this._detailBinding = null;
          this._freshRetryPermit = null;
          this._paymentAvailable = false;
          this.setData({ detail: null, paymentMessage: "", paymentNeedsQuery: false });
        }
        const [raw, policies] = await Promise.all([
          api.getMyOrderDetail(this._orderId),
          typeof api.getPublicPolicies === "function"
            ? api.getPublicPolicies().catch(() => null)
            : Promise.resolve(null),
        ]);
        const paymentAvailable = Boolean(
          policies &&
            policies.capabilities &&
            policies.capabilities.wechat_payment_available === true,
        );
        const detail = projectOrderDetail(raw, { wechatPaymentAvailable: paymentAvailable });
        if (!this._active || version !== this._requestVersion) return;
        if (binding && !sameAuthBinding(app, binding)) {
          this._detailBinding = null;
          this._freshRetryPermit = null;
          this._paymentAvailable = false;
          this.setData({
            detail: null,
            paymentMessage: "",
            paymentNeedsQuery: false,
            errorMessage: "登录状态已变化，请重新加载订单。",
          });
          return;
        }
        this._detailBinding = binding;
        this._paymentAvailable = paymentAvailable;
        const terminal = ["paid_confirmed", "settled_zero", "closed_unpaid"].includes(
          detail.order.paymentStatus,
        );
        if (terminal) {
          try {
            clearPaymentOperation(wx, this._orderId);
          } catch (_error) {
            /* read remains usable */
          }
        }
        this.setData({
          detail,
          paymentNeedsQuery: terminal
            ? false
            : detail.order.paymentConfirmationPending
              ? true
              : this.data.paymentNeedsQuery,
          paymentMessage: terminal ? "" : this.data.paymentMessage,
        });
        return detail;
      } catch (error) {
        if (this._active && version === this._requestVersion) {
          this.setData({ errorMessage: getUserMessage(error, "订单详情加载失败") });
        }
      } finally {
        if (this._active && version === this._requestVersion) {
          this.setData({ loading: false });
        }
        if (typeof wx !== "undefined" && wx.stopPullDownRefresh) wx.stopPullDownRefresh();
      }
    },

    toggleMore() {
      this.setData({ showMore: !this.data.showMore });
    },

    retry() {
      void this.loadDetail();
    },

    startPayment() {
      if (this._paymentPromise || !this._orderId || !this._active) return this._paymentPromise;
      this.setData({ paymentBusy: true, paymentMessage: "", errorMessage: "" });
      this._paymentPromise = this.beginPayment()
        .catch((error) => {
          if (!this._active) return;
          this.setData({
            paymentNeedsQuery:
              error.paymentWriteUnknown === true
                ? true
                : error.paymentInvocationFailed === true
                  ? false
                  : this.data.paymentNeedsQuery,
            paymentMessage:
              error.paymentWriteUnknown === true
                ? "支付请求结果尚未确认，请先查询订单，勿重新生成支付操作。"
                : getUserMessage(error, "支付暂不可用，请刷新订单后重试"),
          });
        })
        .finally(() => {
          this._paymentPromise = null;
          if (this._active) this.setData({ paymentBusy: false });
        });
      return this._paymentPromise;
    },

    async beginPayment() {
      const app = resolveApp();
      if (
        !app ||
        typeof app.ensureAuthenticated !== "function" ||
        typeof app.getXiangwanAuthBinding !== "function"
      )
        throw new Error("runtime unavailable");
      await app.ensureAuthenticated();
      const binding = app.getXiangwanAuthBinding();
      if (!binding || !binding.principalId) throw new Error("auth unavailable");
      const [raw, policies] = await Promise.all([
        api.getMyOrderDetail(this._orderId),
        api.getPublicPolicies(),
      ]);
      if (!sameAuthBinding(app, binding)) throw new Error("auth changed");
      const available =
        policies.capabilities && policies.capabilities.wechat_payment_available === true;
      const order = projectOrderDetail(raw, { wechatPaymentAvailable: available }).order;
      if (
        !order.canPay ||
        order.paymentStatus !== "pending" ||
        order.payableCents < 1 ||
        !Number.isSafeInteger(order.orderVersion) ||
        order.orderVersion < 1
      ) {
        await this.loadDetail();
        if (this._active) this.setData({ paymentMessage: "订单目前不可支付，请以最新状态为准。" });
        return;
      }
      const storage = typeof wx === "undefined" ? null : wx;
      let operation = readPaymentOperation(storage, binding.principalId, this._orderId);
      if (
        operation &&
        (operation.order_version !== order.orderVersion ||
          operation.payable_cents !== order.payableCents)
      ) {
        if (this._active)
          this.setData({
            paymentNeedsQuery: true,
            paymentMessage: "订单金额或版本已变化，请先查询支付结果并联系活动方处理。",
          });
        return;
      }
      if (operation) {
        const permit = this._freshRetryPermit;
        this._freshRetryPermit = null;
        if (
          !permit ||
          permit.principalId !== binding.principalId ||
          permit.orderVersion !== order.orderVersion ||
          permit.payableCents !== order.payableCents ||
          permit.expiresAt <= Date.now()
        ) {
          let query;
          try {
            query = await api.queryWeChatPayment(this._orderId);
          } catch (error) {
            // No queryable attempt exists when the prior prepay write never reached
            // the server or is still in progress. Only exact-key replay is safe.
            if (Number(error && error.statusCode) !== 409) throw error;
          }
          if (query && query.retry_payment_allowed !== true) {
            await this.loadDetail();
            if (this._active)
              this.setData({
                paymentNeedsQuery: true,
                paymentMessage: queryMessage(query),
              });
            return;
          }
        }
      } else {
        operation = savePaymentOperation(
          storage,
          binding.principalId,
          this._orderId,
          order.orderVersion,
          order.payableCents,
          api.newIdempotencyKey(),
        );
      }
      if (!sameAuthBinding(app, binding)) throw new Error("auth changed");
      let prepay;
      try {
        prepay = await api.createWeChatPrepayAttempt(
          this._orderId,
          operation.order_version,
          operation.payable_cents,
          operation.operation_key,
        );
      } catch (error) {
        if (isUnknownWriteResult(error)) error.paymentWriteUnknown = true;
        else if (Number(error.statusCode) === 409) await this.loadDetail();
        throw error;
      }
      if (prepay.next_action !== "invoke_wechat_payment") {
        if (this._active)
          this.setData({
            paymentNeedsQuery: true,
            paymentMessage: "支付请求仍在确认中，请稍后查询订单，勿重复付款。",
          });
        if (prepay.attempt_status === "unknown") await this.verifyPaymentResult();
        return;
      }
      if (!sameAuthBinding(app, binding)) throw new Error("auth changed");
      const paymentInvocation = await invokeWeChatPayment(prepay.payment_parameters);
      if (this._active)
        this.setData({
          paymentNeedsQuery: true,
          paymentMessage: paymentInvocation.cancelled
            ? "已取消支付，正在向服务端核对订单状态…"
            : "正在向服务端核对微信支付结果…",
        });
      await this.verifyPaymentResult();
    },

    verifyPayment() {
      if (this._paymentPromise || !this._orderId || !this._active) return this._paymentPromise;
      this.setData({ paymentBusy: true, paymentMessage: "" });
      this._paymentPromise = this.verifyPaymentResult()
        .catch((error) => {
          if (this._active)
            this.setData({
              paymentNeedsQuery: true,
              paymentMessage: getUserMessage(error, "支付结果暂无法确认，请稍后再次查询"),
            });
        })
        .finally(() => {
          this._paymentPromise = null;
          if (this._active) this.setData({ paymentBusy: false });
        });
      return this._paymentPromise;
    },

    async verifyPaymentResult() {
      const app = resolveApp();
      if (!app || typeof app.ensureAuthenticated !== "function")
        throw new Error("runtime unavailable");
      await app.ensureAuthenticated();
      const binding =
        typeof app.getXiangwanAuthBinding === "function" ? app.getXiangwanAuthBinding() : null;
      if (binding && !sameAuthBinding(app, binding)) throw new Error("auth changed");
      const result = await api.queryWeChatPayment(this._orderId);
      if (binding && !sameAuthBinding(app, binding)) throw new Error("auth changed");
      const detail = await this.loadDetail();
      if (binding && !sameAuthBinding(app, binding)) throw new Error("auth changed");
      this._freshRetryPermit =
        this._active &&
        result.retry_payment_allowed === true &&
        detail &&
        detail.order.canPay &&
        binding &&
        binding.principalId
          ? {
              principalId: binding.principalId,
              orderVersion: detail.order.orderVersion,
              payableCents: detail.order.payableCents,
              expiresAt: Date.now() + 10000,
            }
          : null;
      if (this._active)
        this.setData({
          paymentNeedsQuery: !["payment_confirmed", "payment_closed", "refund_processing"].includes(
            result.next_action,
          ),
          paymentMessage: queryMessage(result),
        });
      return result;
    },

    openRegistration() {
      const registrationId = String(
        (this.data.detail && this.data.detail.order.registrationId) || "",
      ).trim();
      if (!registrationId || typeof wx === "undefined" || typeof wx.navigateTo !== "function") {
        return;
      }
      wx.navigateTo({
        url: `/pages/registration-detail/index?registration_id=${encodeURIComponent(registrationId)}`,
      });
    },
  };
}

if (typeof Page === "function") Page(createOrderDetailPageDefinition());

module.exports = { createOrderDetailPageDefinition };
