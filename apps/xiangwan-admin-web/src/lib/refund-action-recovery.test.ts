import assert from "node:assert/strict";
import test from "node:test";
import {
  parsePendingRefundAction, readPendingRefundAction, refundActionRecoveryKey,
  savePendingRefundAction, type PendingRefundAction,
} from "./refund-action-recovery.ts";

const caseId = "4bd1612e-f38a-4c97-8e1c-f9303f451201";
const operationKey = "39f612b3-940b-45ef-888f-b0b069591801";

test("refund recovery accepts only the exact case and a valid operation key", () => {
  const pending = { caseId, action: "complete", expectedVersion: 2, operationKey };
  assert.deepEqual(parsePendingRefundAction(pending, caseId), pending);
  assert.equal(parsePendingRefundAction(pending, "5bd1612e-f38a-4c97-8e1c-f9303f451201"), null);
  assert.equal(parsePendingRefundAction({ ...pending, expectedVersion: 1.5 }, caseId), null);
  assert.equal(parsePendingRefundAction({ ...pending, operationKey: "00000000-0000-1000-8000-000000000000" }, caseId), null);
});

test("refund recovery persists only the operation marker, never financial evidence", () => {
  const saved = new Map<string, string>();
  const oldWindow = Object.getOwnPropertyDescriptor(globalThis, "window");
  Object.defineProperty(globalThis, "window", {
    configurable: true,
    value: { sessionStorage: {
      getItem: (key: string) => saved.get(key) ?? null,
      setItem: (key: string, value: string) => saved.set(key, value),
      removeItem: (key: string) => saved.delete(key),
    } },
  });
  try {
    savePendingRefundAction({
      caseId, action: "complete", expectedVersion: 2, operationKey,
      evidence_reference: "private merchant reference",
      external_refund_id: "private refund ID",
    } as PendingRefundAction);
    const raw = saved.get(refundActionRecoveryKey(caseId)) ?? "";
    assert.equal(raw.includes("private"), false);
    assert.deepEqual(readPendingRefundAction(caseId), {
      caseId, action: "complete", expectedVersion: 2, operationKey,
    });
  } finally {
    if (oldWindow) Object.defineProperty(globalThis, "window", oldWindow);
    else Reflect.deleteProperty(globalThis, "window");
  }
});
