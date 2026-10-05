"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const { projectCouponsPage } = require("./model");

test("coupon projection keeps value, applicability and ledger state separate", () => {
  const page = projectCouponsPage({
    active_state: "available",
    as_of: "2026-09-15T09:00:00Z",
    items: [
      {
        coupon_id: "88888888-8888-4888-8888-888888888888",
        face_value_cents: 1000,
        minimum_order_cents: 5000,
        valid_from: "2026-09-01T00:00:00Z",
        expires_at: "2026-12-01T00:00:00Z",
        grant_kind: "initial_guest",
        applicability: { scope_type: "activity_type", activity_type: "ai_roundtable" },
        state: "available",
        usable: true,
        correction_required: false,
        latest_adjustment: {
          disposition: "restored",
          occurred_at: "2026-09-14T09:00:00Z",
        },
      },
    ],
  });

  assert.equal(page.items[0].faceValueText, "¥10.00");
  assert.equal(page.items[0].minimumOrderText, "满 ¥50.00 可用");
  assert.equal(page.items[0].applicabilityText, "AI 圆桌活动可用");
  assert.equal(page.items[0].stateLabel, "可使用");
  assert.equal(page.items[0].adjustment.label, "取消后已返还");
});

test("coupon projection surfaces held order identity without inventing usability", () => {
  const page = projectCouponsPage({
    active_state: "held",
    items: [
      {
        coupon_id: "88888888-8888-4888-8888-888888888888",
        face_value_cents: 1000,
        minimum_order_cents: 0,
        valid_from: "2026-09-01T00:00:00Z",
        expires_at: "2026-12-01T00:00:00Z",
        grant_kind: "manual_replenishment",
        applicability: { scope_type: "series" },
        state: "held",
        usable: false,
        correction_required: false,
        active_order_id: "77777777-7777-4777-8777-777777777777",
        active_registration_id: "22222222-2222-4222-8222-222222222222",
      },
    ],
  });

  assert.equal(page.items[0].minimumOrderText, "无门槛");
  assert.equal(page.items[0].stateLabel, "已锁定");
  assert.equal(page.items[0].activeOrderId, "77777777-7777-4777-8777-777777777777");
  assert.equal(page.items[0].usable, false);
});
