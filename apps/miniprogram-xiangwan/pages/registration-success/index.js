"use strict";

const { canonicalUUID } = require("../../services/xiangwan-api");
const { formatDateTime } = require("../../utils/format");

function normalizeText(value) {
  return String(value || "").trim();
}

function optionalRegistrationId(value) {
  try {
    return canonicalUUID(value, "报名记录");
  } catch (_error) {
    return "";
  }
}

// 成功卡的时间行:start/end 都是可解析的响应时间才展示,任一缺失则整行省略。
function sessionTimeText(startAt, endAt) {
  const start = normalizeText(startAt);
  const end = normalizeText(endAt);
  if (!start || !end || Number.isNaN(Date.parse(start)) || Number.isNaN(Date.parse(end))) {
    return "";
  }
  return `${formatDateTime(start)} 至 ${formatDateTime(end)}`;
}

function createRegistrationSuccessPageDefinition() {
  return {
    data: {
      sessionTitle: "",
      sessionLabel: "",
      sessionTime: "",
      venue: "",
    },

    onLoad(options = {}) {
      this._registrationId = optionalRegistrationId(options.registration_id);
      this.setData({
        // `title` remains the established success-card field for shared
        // links. New confirmation flows also pass the concrete Session title
        // separately so the Instance title stays the primary heading.
        sessionTitle: normalizeText(options.title) || normalizeText(options.session_title),
        sessionLabel: normalizeText(options.session_title),
        sessionTime: sessionTimeText(options.start_at, options.end_at),
        venue: normalizeText(options.venue),
      });
    },

    openRegistrationDetail() {
      if (typeof wx === "undefined") return;
      const url = this._registrationId
        ? `/pages/registration-detail/index?registration_id=${encodeURIComponent(
            this._registrationId,
          )}`
        : "/pages/my-registrations/index";
      if (typeof wx.reLaunch === "function") {
        wx.reLaunch({ url });
      } else if (typeof wx.redirectTo === "function") {
        wx.redirectTo({ url });
      }
    },

    backToHome() {
      if (typeof wx === "undefined") return;
      if (typeof wx.switchTab === "function") {
        wx.switchTab({ url: "/pages/index/index" });
      } else if (typeof wx.reLaunch === "function") {
        wx.reLaunch({ url: "/pages/index/index" });
      }
    },
  };
}

if (typeof Page === "function") Page(createRegistrationSuccessPageDefinition());

module.exports = {
  createRegistrationSuccessPageDefinition,
  optionalRegistrationId,
  sessionTimeText,
};
