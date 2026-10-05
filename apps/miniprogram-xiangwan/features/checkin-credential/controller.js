"use strict";

const {
  QR_CANVAS_SIZE,
  REISSUE_FLOOR_MS,
  createQrMatrix,
  drawQrMatrix,
  projectCheckinCredential,
} = require("./model");
const { canonicalUUID, xiangwanApi } = require("../../services/xiangwan-api");
const { getUserMessage, isUnknownWriteResult } = require("../../utils/errors");

const AUTH_RETRY_FLOOR_SECONDS = 60;

function resolveApp() {
  if (typeof getApp !== "function") return null;
  try {
    return getApp();
  } catch (_error) {
    return null;
  }
}

function defaultRenderQr(page, token, onComplete) {
  if (typeof wx !== "undefined" && typeof wx.createSelectorQuery === "function") {
    const query = wx.createSelectorQuery().in(page);
    query
      .select("#checkin-qr")
      .fields({ node: true, size: true })
      .exec((result) => {
        const canvas = result && result[0] && result[0].node;
        if (!canvas || typeof canvas.getContext !== "function") {
          defaultRenderQrLegacy(page, token, onComplete);
          return;
        }
        canvas.width = QR_CANVAS_SIZE;
        canvas.height = QR_CANVAS_SIZE;
        const context = canvas.getContext("2d");
        drawQrMatrix(context, createQrMatrix(token), QR_CANVAS_SIZE);
        if (typeof onComplete === "function") onComplete();
      });
    return;
  }
  defaultRenderQrLegacy(page, token, onComplete);
}

function defaultRenderQrLegacy(page, token, onComplete) {
  if (typeof wx === "undefined" || typeof wx.createCanvasContext !== "function") {
    throw new Error("canvas unavailable");
  }
  const context = wx.createCanvasContext("checkin-qr", page);
  drawQrMatrix(context, createQrMatrix(token), QR_CANVAS_SIZE);
  if (!context || typeof context.draw !== "function") {
    throw new Error("canvas unavailable");
  }
  context.draw(false, onComplete);
}

function defaultClearQr(page) {
  if (typeof wx !== "undefined" && typeof wx.createSelectorQuery === "function") {
    try {
      wx.createSelectorQuery()
        .in(page)
        .select("#checkin-qr")
        .fields({ node: true })
        .exec((result) => {
          const canvas = result && result[0] && result[0].node;
          const context =
            canvas && typeof canvas.getContext === "function" ? canvas.getContext("2d") : null;
          if (context && typeof context.clearRect === "function")
            context.clearRect(0, 0, QR_CANVAS_SIZE, QR_CANVAS_SIZE);
        });
      return;
    } catch (_error) {
      // Fall back to the legacy canvas API below.
    }
  }
  if (typeof wx === "undefined" || typeof wx.createCanvasContext !== "function") return;
  try {
    const context = wx.createCanvasContext("checkin-qr", page);
    if (!context || typeof context.clearRect !== "function" || typeof context.draw !== "function") {
      return;
    }
    context.clearRect(0, 0, QR_CANVAS_SIZE, QR_CANVAS_SIZE);
    context.draw();
  } catch (_error) {
    // Clearing is best effort; reactive state and all retained plaintext are still removed.
  }
}

function positiveRetrySeconds(error, fallbackSeconds = REISSUE_FLOOR_MS / 1000) {
  const value = Number(error && error.retryAfterSeconds);
  if (!Number.isFinite(value) || value <= 0) return fallbackSeconds;
  return Math.min(15 * 60, Math.max(1, Math.ceil(value)));
}

function clearResponseSecrets(value) {
  if (!value || typeof value !== "object") return;
  try {
    value.qr_token = "";
    value.backup_code = "";
  } catch (_error) {
    // Frozen test doubles cannot be overwritten; no reference is retained by the page.
  }
}

function createCheckinCredentialPageDefinition(api = xiangwanApi, dependencies = {}) {
  const now = typeof dependencies.now === "function" ? dependencies.now : () => Date.now();
  const scheduleInterval =
    typeof dependencies.setInterval === "function" ? dependencies.setInterval : setInterval;
  const cancelInterval =
    typeof dependencies.clearInterval === "function" ? dependencies.clearInterval : clearInterval;
  const renderQr =
    typeof dependencies.renderQr === "function" ? dependencies.renderQr : defaultRenderQr;
  const clearQr =
    typeof dependencies.clearQr === "function" ? dependencies.clearQr : defaultClearQr;

  return {
    data: {
      loading: false,
      errorMessage: "",
      noticeMessage: "",
      hasCredential: false,
      qrReady: false,
      qrDrawFailed: false,
      backupCode: "",
      issuedAtText: "",
      expiresAtText: "",
      secondsRemaining: 0,
      refreshWaitSeconds: 0,
      refreshButtonText: "重新生成凭证",
      canRefresh: false,
      expired: false,
    },

    onLoad(options = {}) {
      this._active = true;
      this._visible = false;
      this._issueGeneration = 0;
      this._issuePending = false;
      this._nextIssueAtMs = 0;
      this._expiresAtMs = 0;
      this._clock = null;
      try {
        this._registrationId = canonicalUUID(options.registration_id, "报名记录");
      } catch (error) {
        this.setData({ loading: false, errorMessage: getUserMessage(error, "签到链接无效") });
      }
    },

    onShow() {
      this._visible = true;
      if (!this._active || !this._registrationId) return;
      if (this.data.hasCredential && this._expiresAtMs > now()) {
        this._updateClock();
        this._startClock();
        return;
      }
      if (this._issuePending) {
        this.setData({
          loading: true,
          errorMessage: "",
          noticeMessage: "正在确认刚才的凭证请求，请稍候。",
          canRefresh: false,
        });
        return;
      }
      if (this._nextIssueAtMs > now()) {
        this._autoIssueAfterWait = true;
        this.setData({
          loading: false,
          noticeMessage: "正在准备新的签到码，稍后会自动显示。",
        });
        this._updateClock();
        this._startClock();
        return;
      }
      void this.issueCredential();
    },

    onHide() {
      this._visible = false;
      this._issueGeneration += 1;
      this._stopClock();
      this._clearCredentialSecrets(true);
    },

    onUnload() {
      this._active = false;
      this._visible = false;
      this._issueGeneration += 1;
      this._stopClock();
      this._clearCredentialSecrets(false);
      this._registrationId = "";
    },

    async issueCredential() {
      if (
        !this._active ||
        !this._visible ||
        !this._registrationId ||
        this._issuePending ||
        this.data.loading
      ) {
        return null;
      }
      if (this._nextIssueAtMs > now()) {
        this._updateClock();
        this._startClock();
        return null;
      }

      const generation = ++this._issueGeneration;
      this._autoIssueAfterWait = false;
      this._issuePending = true;
      this._stopClock();
      this._clearCredentialSecrets(true);
      this.setData({
        loading: true,
        errorMessage: "",
        noticeMessage: "",
        canRefresh: false,
        expired: false,
      });

      let rawCredential = null;
      let credential = null;
      let requestStartedAt = 0;
      let credentialRequestDispatched = false;
      try {
        const app = resolveApp();
        if (!app || typeof app.ensureAuthenticated !== "function") {
          throw new Error("runtime unavailable");
        }
        await app.ensureAuthenticated();
        if (!this._isCurrent(generation)) return null;

        requestStartedAt = now();
        credentialRequestDispatched = true;
        rawCredential = await api.issueCheckinCredential(this._registrationId);
        // The server mints the credential during the request. Anchor the local
        // reissue floor at response receipt so network latency cannot consume
        // the entire guard and immediately enable another credential.
        this._nextIssueAtMs = now() + REISSUE_FLOOR_MS;
        credential = projectCheckinCredential(rawCredential, this._registrationId);
        clearResponseSecrets(rawCredential);
        rawCredential = null;

        if (!this._isCurrent(generation)) {
          credential.qrToken = "";
          credential.backupCode = "";
          return null;
        }

        this._expiresAtMs = requestStartedAt + (credential.expiresAtMs - credential.issuedAtMs);
        const token = credential.qrToken;
        credential.qrToken = "";
        this.setData(
          {
            loading: false,
            hasCredential: true,
            qrReady: false,
            qrDrawFailed: false,
            backupCode: credential.backupCode,
            issuedAtText: credential.issuedAtText,
            expiresAtText: credential.expiresAtText,
            errorMessage: "",
            noticeMessage: "",
          },
          () => this._renderCredentialQr(token, generation),
        );
        credential.backupCode = "";
        this._updateClock();
        this._startClock();
        return true;
      } catch (error) {
        clearResponseSecrets(rawCredential);
        if (credential) {
          credential.qrToken = "";
          credential.backupCode = "";
        }
        const statusCode = Number((error && error.statusCode) || 0);
        const authenticationFailed = !credentialRequestDispatched;
        const unknownCredentialResult = credentialRequestDispatched && isUnknownWriteResult(error);
        const retrySeconds =
          statusCode === 429
            ? positiveRetrySeconds(
                error,
                authenticationFailed ? AUTH_RETRY_FLOOR_SECONDS : REISSUE_FLOOR_MS / 1000,
              )
            : unknownCredentialResult
              ? REISSUE_FLOOR_MS / 1000
              : 0;
        if (retrySeconds > 0) {
          this._autoIssueAfterWait = true;
          // Authentication and credential issuance have different throttle
          // windows. Preserve the relevant boundary even if hide/show made
          // this response stale before it arrived.
          this._nextIssueAtMs = Math.max(this._nextIssueAtMs, now() + retrySeconds * 1000);
        }
        if (!this._isCurrent(generation)) return null;

        let message = getUserMessage(error, "签到凭证生成失败");
        if (authenticationFailed && statusCode === 429) {
          message = `登录请求过于频繁，请 ${retrySeconds} 秒后再试`;
        } else if (statusCode === 429) {
          message = `签到凭证刚刚生成过，请 ${retrySeconds} 秒后再试`;
        } else if (unknownCredentialResult) {
          message = `签到凭证生成结果暂未确认，请 ${retrySeconds} 秒后再试`;
        } else if (statusCode === 409) {
          message = "当前报名状态无法生成签到凭证，请返回刷新报名状态";
        } else if (statusCode === 503) {
          message = "签到凭证服务暂时不可用，请稍后再试";
        }
        this.setData({ loading: false, errorMessage: message, hasCredential: false });
        this._updateClock();
        this._startClock();
        return null;
      } finally {
        const requestIsCurrent = this._isCurrent(generation);
        this._issuePending = false;
        if (this._active && this._visible && !requestIsCurrent) {
          this._autoIssueAfterWait = true;
          const waitingForRefresh = this._nextIssueAtMs > now();
          this.setData({
            loading: false,
            errorMessage: "",
            noticeMessage: waitingForRefresh
              ? "为保护签到凭证，离开页面后已清除，请稍后重新生成。"
              : "离开页面后凭证请求已停止，请重新生成。",
          });
          this._updateClock();
          this._startClock();
        }
      }
    },

    refreshCredential() {
      if (!this.data.canRefresh) return;
      void this.issueCredential();
    },

    backToRegistration() {
      if (typeof wx === "undefined") return;
      const fallback = () => {
        if (this._registrationId && typeof wx.redirectTo === "function") {
          wx.redirectTo({
            url: `/pages/registration-detail/index?registration_id=${encodeURIComponent(
              this._registrationId,
            )}`,
          });
        } else if (typeof wx.reLaunch === "function") {
          wx.reLaunch({ url: "/pages/my-registrations/index" });
        } else if (typeof wx.redirectTo === "function") {
          wx.redirectTo({ url: "/pages/my-registrations/index" });
        } else if (typeof wx.navigateTo === "function") {
          wx.navigateTo({ url: "/pages/my-registrations/index" });
        }
      };
      if (typeof wx.navigateBack === "function") {
        wx.navigateBack({ delta: 1, fail: fallback });
      } else {
        fallback();
      }
    },

    _isCurrent(generation) {
      return Boolean(this._active && this._visible && generation === this._issueGeneration);
    },

    _renderCredentialQr(token, generation) {
      if (!this._isCurrent(generation) || !token) return;
      try {
        renderQr(this, token, () => {
          if (this._isCurrent(generation) && this.data.hasCredential) {
            this.setData({ qrReady: true });
          }
        });
        token = "";
      } catch (_error) {
        token = "";
        if (this._isCurrent(generation)) {
          this.setData({
            qrReady: false,
            qrDrawFailed: true,
            noticeMessage: "二维码未能绘制，请向工作人员出示下方备份码。",
          });
        }
      }
    },

    _clearCredentialSecrets(updateView) {
      this._expiresAtMs = 0;
      clearQr(this);
      const clearedView = {
        loading: false,
        hasCredential: false,
        qrReady: false,
        qrDrawFailed: false,
        backupCode: "",
        issuedAtText: "",
        expiresAtText: "",
        secondsRemaining: 0,
        canRefresh: false,
      };
      if (updateView && this._active) {
        this.setData(clearedView);
      } else if (this.data) {
        Object.assign(this.data, clearedView);
      }
    },

    _expireCredential() {
      this._autoIssueAfterWait = true;
      clearQr(this);
      this._expiresAtMs = 0;
      if (this._active && this._visible) {
        this.setData({
          hasCredential: false,
          qrReady: false,
          qrDrawFailed: false,
          backupCode: "",
          secondsRemaining: 0,
          expired: true,
          noticeMessage: "签到凭证已过期，请重新生成后再出示。",
        });
      }
    },

    _updateClock() {
      if (!this._active || !this._visible) return;
      const currentTime = now();
      if (this._expiresAtMs > 0 && currentTime >= this._expiresAtMs) {
        this._expireCredential();
      }
      const secondsRemaining =
        this._expiresAtMs > currentTime ? Math.ceil((this._expiresAtMs - currentTime) / 1000) : 0;
      const refreshWaitSeconds =
        this._nextIssueAtMs > currentTime
          ? Math.ceil((this._nextIssueAtMs - currentTime) / 1000)
          : 0;
      const canRefresh = !this.data.loading && refreshWaitSeconds === 0;
      this.setData({
        secondsRemaining,
        refreshWaitSeconds,
        refreshButtonText:
          refreshWaitSeconds > 0 ? `${refreshWaitSeconds} 秒后可重新生成` : "重新生成凭证",
        canRefresh,
      });
      if (!secondsRemaining && !refreshWaitSeconds) this._stopClock();
      if (
        dependencies.autoRefresh === true &&
        this._autoIssueAfterWait &&
        !this.data.hasCredential &&
        canRefresh &&
        !this._issuePending
      ) {
        this._autoIssueAfterWait = false;
        void this.issueCredential();
      }
    },

    _startClock() {
      if (
        this._clock ||
        !this._active ||
        !this._visible ||
        (!this.data.secondsRemaining && !this.data.refreshWaitSeconds)
      ) {
        return;
      }
      this._clock = scheduleInterval(() => this._updateClock(), 1000);
    },

    _stopClock() {
      if (!this._clock) return;
      cancelInterval(this._clock);
      this._clock = null;
    },
  };
}

module.exports = {
  AUTH_RETRY_FLOOR_SECONDS,
  createCheckinCredentialPageDefinition,
  defaultClearQr,
  defaultRenderQr,
  positiveRetrySeconds,
};
