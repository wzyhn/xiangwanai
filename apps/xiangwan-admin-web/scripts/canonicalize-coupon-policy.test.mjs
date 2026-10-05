import assert from "node:assert/strict";
import test from "node:test";
import { canonicalize } from "./canonicalize-coupon-policy.mjs";

test("customer Coupon policy canonicalizer fixes field order and byte representation", () => {
  const document = {
    effective_at: "2026-10-03T00:00:00Z",
    evidence_ref: "approval/guest-v1",
    approved_at: "2026-10-01T00:00:00Z",
    minimum_order_cents: 0,
    scope_series_id: null,
    scope_activity_type: "ai_roundtable",
    scope_type: "activity_type",
    validity_seconds: 86400,
    face_value_cents: 2000,
    enabled: true,
    policy_version: "guest-v1",
    tenant_id: "00000000-0000-4000-8000-000000000001",
  };
  assert.equal(canonicalize(document),
    '{"tenant_id":"00000000-0000-4000-8000-000000000001","policy_version":"guest-v1","enabled":true,"face_value_cents":2000,"validity_seconds":86400,"scope_type":"activity_type","scope_activity_type":"ai_roundtable","scope_series_id":null,"minimum_order_cents":0,"evidence_ref":"approval/guest-v1","approved_at":"2026-10-01T00:00:00Z","effective_at":"2026-10-03T00:00:00Z"}');
  assert.throws(() => canonicalize({ ...document, extra: true }), /exactly/);
  assert.throws(() => canonicalize({ ...document, face_value_cents: Number.MAX_SAFE_INTEGER + 1 }), /invalid/);
});
