import assert from "node:assert/strict";
import test from "node:test";
import { parsePendingCouponReplenishment } from "./coupon-replenishment-recovery.ts";

const source = "11111111-1111-4111-8111-111111111111";
const operation = "22222222-2222-4222-8222-222222222222";

test("preserves the exact unknown-result Coupon replenishment request", () => {
  const raw = JSON.stringify({ source_coupon_id: source, operation_id: operation, reason: "case", context: "support-1" });
  assert.deepEqual(parsePendingCouponReplenishment(raw), JSON.parse(raw));
});

test("rejects malformed recovery state", () => {
  assert.equal(parsePendingCouponReplenishment("{"), null);
  assert.equal(parsePendingCouponReplenishment(JSON.stringify({ source_coupon_id: source, operation_id: operation })), null);
  assert.equal(parsePendingCouponReplenishment(JSON.stringify({ source_coupon_id: source, operation_id: "wrong", reason: "case", context: "support-1" })), null);
});
