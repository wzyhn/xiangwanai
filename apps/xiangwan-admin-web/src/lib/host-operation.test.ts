import { test } from "node:test";
import assert from "node:assert/strict";
import { parseHostPending } from "./host-operation.ts";
const owner = "owner",
  operation = "11111111-1111-4111-8111-111111111111";
test("host recovery retains exact operator intent and refuses applicant private fields", () => {
  const p = {
    owner,
    operation,
    path: "/host-applications/22222222-2222-4222-8222-222222222222/reviews",
    body: JSON.stringify({ expected_version: 1, decision: "approved", comment: "审核意见" }),
  };
  assert.deepEqual(parseHostPending(JSON.stringify(p), owner), p);
  assert.equal(parseHostPending(JSON.stringify(p), "other"), null);
  assert.equal(
    parseHostPending(
      JSON.stringify({
        ...p,
        body: JSON.stringify({ ...JSON.parse(p.body), contact_method: "private" }),
      }),
      owner,
    ),
    null,
  );
  assert.equal(parseHostPending(JSON.stringify({ ...p, path: "/people" }), owner), null);
});
