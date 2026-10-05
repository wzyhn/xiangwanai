import test from "node:test";
import assert from "node:assert/strict";
import {
  readSessionCancellation,
  saveSessionCancellation,
  validSessionCancellationPreview,
  type SessionCancellationRecovery,
} from "./session-cancellation-recovery.ts";

const sessionId = "11111111-1111-4111-8111-111111111111";
const preview = {
  id: "22222222-2222-4222-8222-222222222222",
  session_id: sessionId,
  expected_session_version: 3,
  cancelled_registration_count: 2,
  pending_order_count: 1,
  unknown_payment_count: 1,
  requested_refund_cents: 8800,
  coupon_adjustment_count: 0,
  expires_at: "2026-10-02T04:00:00Z",
};
const request: SessionCancellationRecovery = {
  version: 1,
  sessionId,
  previewOperation: "33333333-3333-4333-8333-333333333333",
  operation: "44444444-4444-4444-8444-444444444444",
  reason: "场地调整",
  submitted: true,
  preview,
};

test("unknown cancellation preserves the exact target, impact, reason and operation across reload", (t) => {
  let raw = "";
  Object.assign(globalThis, {
    window: {
      sessionStorage: {
        setItem(_key: string, value: string) {
          raw = value;
        },
        getItem() {
          return raw;
        },
      },
    },
  });
  t.after(() => Reflect.deleteProperty(globalThis, "window"));
  saveSessionCancellation(request);
  assert.deepEqual(readSessionCancellation(sessionId), request);
  for (const change of [
    { sessionId: "other" },
    { operation: request.previewOperation },
    { reason: "  " },
    { preview: undefined },
    { preview: { ...preview, session_id: "other" } },
    { preview: { ...preview, requested_refund_cents: -1 } },
  ]) {
    raw = JSON.stringify({ ...request, ...change });
    assert.throws(() => readSessionCancellation(sessionId), /无法安全恢复/);
  }
});

test("storage failure blocks persistence instead of discarding operation identity", (t) => {
  Object.assign(globalThis, {
    window: {
      sessionStorage: {
        setItem() {
          throw new Error("blocked");
        },
        getItem() {
          throw new Error("blocked");
        },
      },
    },
  });
  t.after(() => Reflect.deleteProperty(globalThis, "window"));
  assert.throws(() => saveSessionCancellation(request), /blocked/);
  assert.throws(() => readSessionCancellation(sessionId), /blocked/);
});

test("preview rejects foreign targets, unknown counts and invalid versions", () => {
  assert.equal(validSessionCancellationPreview(preview, sessionId), true);
  for (const change of [
    { expected_session_version: 0 },
    { pending_order_count: NaN },
    { unknown_payment_count: undefined },
    { id: "not-a-uuid" },
    { expires_at: "invalid" },
  ]) {
    assert.equal(validSessionCancellationPreview({ ...preview, ...change }, sessionId), false);
  }
});
