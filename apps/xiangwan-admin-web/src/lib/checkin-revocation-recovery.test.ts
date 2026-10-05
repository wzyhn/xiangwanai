import test from "node:test";
import assert from "node:assert/strict";
import { parseCheckinRevocation } from "./checkin-revocation-recovery.ts";
const registrationId = "11111111-1111-4111-8111-111111111111";
const request = {
  version: 1,
  operation: "22222222-2222-4222-8222-222222222222",
  registrationId,
  checkinId: "33333333-3333-4333-8333-333333333333",
  expectedVersion: 1,
  reason: "误签纠正",
};
test("lost revocation receipt preserves exact identity, expected version, reason and operation", () => {
  assert.deepEqual(
    parseCheckinRevocation(JSON.parse(JSON.stringify(request)), registrationId),
    request,
  );
  for (const change of [
    { registrationId: "foreign" },
    { operation: "invalid" },
    { checkinId: "invalid" },
    { expectedVersion: 0 },
    { reason: " " },
    { reason: "错误".repeat(501) },
  ]) {
    assert.throws(
      () => parseCheckinRevocation({ ...request, ...change }, registrationId),
      /无法安全恢复/,
    );
  }
});
