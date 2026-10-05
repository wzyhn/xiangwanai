import test from "node:test";
import assert from "node:assert/strict";
import { parsePeopleBindingRecovery } from "./people-binding-recovery.ts";
const profileId = "11111111-1111-4111-8111-111111111111";
const base = {
  version: 1,
  kind: "invite",
  profileId,
  expectedVersion: 2,
  operation: "22222222-2222-4222-8222-222222222222",
  reason: "核对本人",
};
test("binding recovery retains the exact original request and rejects code or identity injection", () => {
  assert.deepEqual(parsePeopleBindingRecovery(JSON.parse(JSON.stringify(base)), profileId), base);
  for (const change of [
    { code: "secret" },
    { principalId: "foreign" },
    { kind: "revoke" },
    { expectedVersion: 0 },
    { reason: " " },
    { operation: "invalid" },
    { profileId: "foreign" },
  ])
    assert.throws(() => parsePeopleBindingRecovery({ ...base, ...change }, profileId));
  const revoke = { ...base, kind: "revoke", bindingId: "33333333-3333-4333-8333-333333333333" };
  assert.deepEqual(parsePeopleBindingRecovery(revoke, profileId), revoke);
  const withdrawal={...revoke,kind:"invitation_revoke",expectedVersion:1};
  assert.deepEqual(parsePeopleBindingRecovery(withdrawal,profileId),withdrawal);
});
