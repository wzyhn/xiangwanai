import assert from "node:assert/strict";
import test from "node:test";
import { parseReviewResourceRecovery } from "./review-resource-recovery.ts";

const operation = "87d2c383-69e9-4e58-89f2-94707917788a";

test("review recovery retains exact published request across reload", () => {
  const body = JSON.stringify({
    expected_target_version: 7,
    title: "本期回顾",
    photos: [],
    files: [{ file_id: "file-1", sha256: "a".repeat(64), kind: "photo", reviewed: true }],
    sort_order: 2,
  });
  const result = parseReviewResourceRecovery(JSON.parse(JSON.stringify({
    version: 2, operation, body,
  })));
  assert.equal(result?.body, body);
  assert.equal(result?.operation, operation);
});

test("review recovery rejects operation-only and malformed snapshots", () => {
  assert.equal(parseReviewResourceRecovery({ version: 1, operation }), null);
  assert.equal(parseReviewResourceRecovery({ version: 2, operation, body: "{}" }), null);
  assert.equal(parseReviewResourceRecovery({ version: 2, operation, body: "{" }), null);
  assert.equal(parseReviewResourceRecovery({ version: 2, operation, body: JSON.stringify({
    expected_target_version: 0, title: "本期回顾", photos: [], files: [],
  }) }), null);
});
