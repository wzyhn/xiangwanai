"use strict";

const AREA_LABELS = Object.freeze({
  all: "全部区域",
  heping: "和平区",
  hexi: "河西区",
  hebei: "河北区",
  nankai: "南开区",
  hongqiao: "红桥区",
  hedong: "河东区",
  wuqing: "武清区",
  dongli: "东丽区",
  binhai: "滨海新区",
  online: "线上",
});

const ACTIVITY_TYPE_LABELS = Object.freeze({
  all: "全部类型",
  ai_roundtable: "AI 圆桌",
  special_event: "特别活动",
  course: "课程",
  competition: "赛事",
  custom: "自定义活动",
});

const REGISTRATION_STATE_LABELS = Object.freeze({
  all: "全部",
  pending_payment: "待支付",
  registered: "报名成功",
  cancelled: "已取消",
  refund_processing: "退款处理中",
  refunded: "已退款",
  ended: "已结束",
});

function normalizeText(value) {
  return String(value || "").trim();
}

function formatMoney(cents) {
  const value = Number(cents);
  if (!Number.isSafeInteger(value) || value < 0) return "价格待确认";
  if (value === 0) return "免费";
  return `¥${(value / 100).toFixed(2)}`;
}

function formatDateTime(value) {
  const parts = shanghaiDateParts(value);
  if (!parts) return "时间待确认";
  return `${parts.month}-${parts.day} ${parts.hour}:${parts.minute}`;
}

function shanghaiDateParts(value) {
  const timestamp = Date.parse(normalizeText(value));
  if (Number.isNaN(timestamp)) return null;
  const shanghai = new Date(timestamp + 8 * 60 * 60 * 1000);
  const twoDigits = (part) => (part < 10 ? `0${part}` : String(part));
  const weekday = ["日", "一", "二", "三", "四", "五", "六"][shanghai.getUTCDay()];
  return {
    month: twoDigits(shanghai.getUTCMonth() + 1),
    day: twoDigits(shanghai.getUTCDate()),
    hour: twoDigits(shanghai.getUTCHours()),
    minute: twoDigits(shanghai.getUTCMinutes()),
    displayDate: `${twoDigits(shanghai.getUTCMonth() + 1)}月${twoDigits(shanghai.getUTCDate())}日 周${weekday}`,
    time: `${twoDigits(shanghai.getUTCHours())}:${twoDigits(shanghai.getUTCMinutes())}`,
  };
}

function formatEventDate(value, endValue = "") {
  const parts = shanghaiDateParts(value);
  if (!parts) return "";
  const endParts = shanghaiDateParts(endValue);
  return endParts && endParts.displayDate !== parts.displayDate
    ? `${parts.displayDate} 至 ${endParts.displayDate}`
    : parts.displayDate;
}

function formatEventTimeRange(start, end) {
  const startParts = shanghaiDateParts(start);
  const endParts = shanghaiDateParts(end);
  if (!startParts || !endParts) return "";
  return `${startParts.time}–${endParts.time}`;
}

function maskPhone(value) {
  const phone = normalizeText(value);
  const match = /^\+([1-9][0-9]{7,14})$/.exec(phone);
  if (!match) return "";
  const digits = match[1];
  if (digits.length <= 10) return `+${digits.slice(0, 1)}****${digits.slice(-2)}`;
  return `+${digits.slice(0, 3)}****${digits.slice(-4)}`;
}

function areaLabel(value) {
  return AREA_LABELS[normalizeText(value)] || "地点待确认";
}

function activityTypeLabel(value) {
  return ACTIVITY_TYPE_LABELS[normalizeText(value)] || "活动";
}

function registrationStateLabel(value) {
  return REGISTRATION_STATE_LABELS[normalizeText(value)] || "状态待确认";
}

module.exports = {
  ACTIVITY_TYPE_LABELS,
  AREA_LABELS,
  REGISTRATION_STATE_LABELS,
  activityTypeLabel,
  areaLabel,
  formatDateTime,
  formatEventDate,
  formatEventTimeRange,
  formatMoney,
  maskPhone,
  registrationStateLabel,
};
