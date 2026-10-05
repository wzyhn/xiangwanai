import assert from "node:assert/strict";
import test from "node:test";
import { parsePendingPhotoCuration } from "./photo-curation-recovery.ts";

const instanceId = "4bd1612e-f38a-4c97-8e1c-f9303f451201";
const relationId = "abed7a24-4727-47a7-928f-598d4af9b046";
const first = "3f9cf2c2-8025-4e0c-a6dd-92c7e91c6f10";
const second = "6bfcd012-d75a-4d83-bad1-1238ecc27556";
const operationKey = "71fa8980-adcf-4a5e-a7df-37169054be70";

test("photo curation recovery is exact to instance, relation, version and ordered IDs", () => {
  const pending = {
    instanceId,
    relationId,
    expectedVersion: 3,
    orderedBlockIDs: [second, first],
    operationKey,
  };
  assert.deepEqual(parsePendingPhotoCuration(pending, instanceId, relationId), pending);
  assert.equal(parsePendingPhotoCuration(pending, second, relationId), null);
  assert.equal(
    parsePendingPhotoCuration(
      { ...pending, orderedBlockIDs: [first, first] },
      instanceId,
      relationId,
    ),
    null,
  );
  assert.equal(
    parsePendingPhotoCuration({ ...pending, expectedVersion: -1 }, instanceId, relationId),
    null,
  );
  assert.equal(
    parsePendingPhotoCuration(
      { ...pending, operationKey: instanceId.replace("-4", "-1") },
      instanceId,
      relationId,
    ),
    null,
  );
});

test("cover recovery preserves omission and rejects a hidden or foreign photo", () => {
  const legacy = {
    instanceId,
    relationId,
    expectedVersion: 0,
    orderedBlockIDs: [first],
    operationKey,
  };
  assert.equal(
    Object.hasOwn(parsePendingPhotoCuration(legacy, instanceId, relationId)!, "coverBlockID"),
    false,
  );
  for (const coverBlockID of ["", first]) {
    const pending = { ...legacy, coverBlockID };
    assert.deepEqual(parsePendingPhotoCuration(pending, instanceId, relationId), pending);
  }
  assert.equal(
    parsePendingPhotoCuration({ ...legacy, coverBlockID: second }, instanceId, relationId),
    null,
  );
  assert.equal(
    parsePendingPhotoCuration({ ...legacy, coverBlockID: "bad" }, instanceId, relationId),
    null,
  );
});
