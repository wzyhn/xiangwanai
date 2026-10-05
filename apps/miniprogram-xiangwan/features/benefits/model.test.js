"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const { projectMyBenefits } = require("./model");

function role(state, overrides = {}) {
  return {
    series_id: "44444444-4444-4444-8444-444444444444",
    instance_id: "55555555-5555-4555-8555-555555555555",
    role_code: "host",
    role_status: state === "current" ? "active" : "revoked",
    instance_status: state === "current" ? "published" : "completed",
    state,
    series_title: "AI 圆桌",
    instance_title: "秋季场",
    granted_at: "2026-09-10T08:00:00Z",
    ...(state === "historical" ? { revoked_at: "2026-09-11T08:00:00Z" } : {}),
    ...overrides,
  };
}

test("benefits projection separates current roles from historical-only cards", () => {
  const current = role("current");
  const historical = role("historical", {
    instance_id: "66666666-6666-4666-8666-666666666666",
  });
  const result = projectMyBenefits({
    trusted_people_profile_id: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
    has_host_identity: true,
    current_roles: [current],
    role_history: [current, historical],
    host_rules: { state: "configured", requirements: "要求", benefits: "支持" },
    can_apply_for_host: false,
    host_application_history: [],
    host_contribution_count: 1,
    host_contribution_history: [
      {
        series_id: current.series_id,
        instance_id: current.instance_id,
        contribution_type: "host_checkin",
        state: "active",
        earned_at: "2026-09-12T08:00:00Z",
      },
    ],
    identity_history_available: true,
  });

  assert.equal(result.hasTrustedBinding, true);
  assert.equal(result.currentRoles.length, 1);
  assert.equal(result.historicalRoles.length, 1);
  assert.equal(result.currentRoles[0].roleLabel, "主理人");
  assert.equal(result.hostContributionCountText, "当前有效贡献 1 次");
  assert.equal(result.contributions[0].earnedAt, "09-12 16:00");
});

test("benefits projection presents pending host rules without inventing application actions", () => {
  const result = projectMyBenefits({
    has_host_identity: false,
    current_roles: [],
    role_history: [],
    host_rules: { state: "pending" },
    can_apply_for_host: false,
    host_application_history: [],
    host_contribution_count: 0,
    host_contribution_history: [],
    identity_history_available: false,
  });

  assert.equal(result.hostRules.configured, false);
  assert.equal(result.hostRules.stateLabel, "规则待发布");
  assert.equal(result.currentApplication, null);
  assert.equal(result.identityHistoryAvailable, false);
});
