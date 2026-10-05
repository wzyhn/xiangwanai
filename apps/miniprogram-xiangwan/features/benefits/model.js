"use strict";

const { formatDateTime } = require("../../utils/format");

const ROLE_LABELS = Object.freeze({
  host: "主理人",
  invited_guest: "特邀嘉宾",
  course_instructor: "课程讲师",
  event_speaker: "活动分享者",
});

const INSTANCE_STATUS_LABELS = Object.freeze({
  draft: "筹备中",
  pending_publish: "待发布",
  published: "进行中",
  completed: "已完成",
  cancelled: "已取消",
  archived: "已归档",
});

const APPLICATION_STATUS_LABELS = Object.freeze({
  pending: "审核中",
  approved: "已通过",
  rejected: "未通过",
  withdrawn: "已撤回",
});

function normalizeText(value) {
  return String(value || "").trim();
}

function projectRole(role = {}, index = 0) {
  const state = normalizeText(role.state);
  const roleCode = normalizeText(role.role_code);
  const instanceStatus = normalizeText(role.instance_status);
  return {
    key: `${normalizeText(role.series_id)}:${normalizeText(role.instance_id)}:${roleCode}:${normalizeText(
      role.granted_at,
    )}:${index}`,
    seriesId: normalizeText(role.series_id),
    instanceId: normalizeText(role.instance_id),
    seriesTitle: normalizeText(role.series_title),
    instanceTitle: normalizeText(role.instance_title),
    roleCode,
    roleLabel: ROLE_LABELS[roleCode] || "社区角色",
    roleStatus: normalizeText(role.role_status),
    instanceStatus,
    instanceStatusLabel: INSTANCE_STATUS_LABELS[instanceStatus] || "状态待确认",
    state,
    stateLabel: state === "current" ? "当前身份" : "历史身份",
    stateTone: state === "current" ? "default" : "quiet",
    grantedAt: formatDateTime(role.granted_at),
    revokedAt: role.revoked_at ? formatDateTime(role.revoked_at) : "",
  };
}

function projectApplication(application = {}, index = 0) {
  const status = normalizeText(application.application_status);
  return {
    key: `${normalizeText(application.application_id)}:${index}`,
    applicationId: normalizeText(application.application_id),
    version: Number(application.version),
    cycle: normalizeText(application.application_cycle),
    policyVersion: normalizeText(application.policy_version),
    status,
    statusLabel: APPLICATION_STATUS_LABELS[status] || "状态待确认",
    stateTone: status === "approved" ? "default" : status === "pending" ? "warm" : "quiet",
    reviewComment: normalizeText(application.review_comment),
    submittedAt: formatDateTime(application.submitted_at),
    updatedAt: formatDateTime(application.updated_at),
  };
}

function projectContribution(item = {}, index = 0) {
  const state = normalizeText(item.state);
  return {
    key: `${normalizeText(item.instance_id)}:${normalizeText(item.contribution_type)}:${index}`,
    seriesId: normalizeText(item.series_id),
    instanceId: normalizeText(item.instance_id),
    type: normalizeText(item.contribution_type),
    typeLabel: "主理人签到贡献",
    state,
    stateLabel: state === "active" ? "有效" : "已撤销",
    stateTone: state === "active" ? "default" : "quiet",
    earnedAt: formatDateTime(item.earned_at),
    reversedAt: item.reversed_at ? formatDateTime(item.reversed_at) : "",
  };
}

function projectMyBenefits(value = {}) {
  const roleHistory = Array.isArray(value.role_history) ? value.role_history : [];
  const applicationHistory = Array.isArray(value.host_application_history)
    ? value.host_application_history
    : [];
  const contributionHistory = Array.isArray(value.host_contribution_history)
    ? value.host_contribution_history
    : [];
  const rules = value.host_rules || {};
  const rulesState = normalizeText(rules.state);
  const contributionCount = Number(value.host_contribution_count);
  return {
    trustedPeopleProfileId: normalizeText(value.trusted_people_profile_id),
    hasTrustedBinding: Boolean(normalizeText(value.trusted_people_profile_id)),
    hasHostIdentity: value.has_host_identity === true,
    canApplyForHost: value.can_apply_for_host === true,
    identityHistoryAvailable: value.identity_history_available === true,
    currentRoles: (Array.isArray(value.current_roles) ? value.current_roles : []).map(projectRole),
    historicalRoles: roleHistory
      .filter((role) => normalizeText(role.state) === "historical")
      .map(projectRole),
    hostRules: {
      state: rulesState,
      configured: rulesState === "configured",
      cycle: normalizeText(rules.application_cycle),
      policyVersion: normalizeText(rules.policy_version),
      stateLabel: rulesState === "configured" ? "规则已配置" : "规则待发布",
      requirements: normalizeText(rules.requirements),
      benefits: normalizeText(rules.benefits),
    },
    currentApplication: value.current_host_application
      ? projectApplication(value.current_host_application)
      : null,
    applicationHistory: applicationHistory.map(projectApplication),
    hostContributionCount: Number.isSafeInteger(contributionCount) ? contributionCount : 0,
    hostContributionTarget: 5,
    hostContributionProgressWidth: `${Math.min(
      100,
      Math.round(((Number.isSafeInteger(contributionCount) ? contributionCount : 0) / 5) * 100),
    )}%`,
    hostContributionRemaining:
      Number.isSafeInteger(contributionCount) && contributionCount < 5
        ? `再完成 ${5 - contributionCount} 场，即可获得专题活动免费券 ×1`
        : "已达到主持权益条件",
    hostContributionCountText: `当前有效贡献 ${
      Number.isSafeInteger(contributionCount) ? contributionCount : 0
    } 次`,
    contributions: contributionHistory.map(projectContribution),
  };
}

module.exports = {
  APPLICATION_STATUS_LABELS,
  INSTANCE_STATUS_LABELS,
  ROLE_LABELS,
  projectApplication,
  projectContribution,
  projectMyBenefits,
  projectRole,
};
