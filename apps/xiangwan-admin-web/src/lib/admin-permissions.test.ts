import assert from "node:assert/strict";
import test from "node:test";
import { canOpenAdminPath, canReadAuditLog, defaultAdminPath } from "./admin-permissions.ts";
import type { AuthStatus } from "./types.ts";

function status(capability: "super_admin" | "activity_operator" | "onsite_checkin" | "finance"): AuthStatus {
  return {
    enabled: true, authenticated: true,
    grants: [{ capability, scope_type: "tenant" }],
  };
}

test("audit log navigation is limited to tenant super administrators", () => {
  assert.equal(canReadAuditLog(status("super_admin")), true);
  assert.equal(canOpenAdminPath(status("super_admin"), "/audit-events"), true);
  assert.equal(canOpenAdminPath(status("activity_operator"), "/audit-events"), false);
  assert.equal(canOpenAdminPath(status("onsite_checkin"), "/audit-events"), false);
});

test("Coupon correction leads are limited to tenant super administrators", () => {
  assert.equal(canOpenAdminPath(status("super_admin"), "/coupon-corrections"), true);
  for (const capability of ["activity_operator", "onsite_checkin", "finance"] as const) {
    assert.equal(canOpenAdminPath(status(capability), "/coupon-corrections"), false);
  }
});

test("refund queue navigation requires an independent tenant finance grant", () => {
  assert.equal(canOpenAdminPath(status("super_admin"), "/refunds"), true);
  assert.equal(canOpenAdminPath(status("finance"), "/refunds"), true);
  assert.equal(canOpenAdminPath(status("finance"), "/refunds/case-1"), true);
  assert.equal(canOpenAdminPath(status("activity_operator"), "/refunds"), false);
  assert.equal(canOpenAdminPath(status("onsite_checkin"), "/refunds"), false);
  assert.equal(defaultAdminPath(status("finance")), "/refunds");
});

test("order list navigation accepts activity operations or finance", () => {
  assert.equal(canOpenAdminPath(status("activity_operator"), "/orders"), true);
  assert.equal(canOpenAdminPath(status("finance"), "/orders"), true);
  assert.equal(canOpenAdminPath(status("super_admin"), "/orders"), true);
  assert.equal(canOpenAdminPath(status("onsite_checkin"), "/orders"), false);
});
