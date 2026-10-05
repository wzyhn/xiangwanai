import test from "node:test";
import assert from "node:assert/strict";
import { completionRecoveryPrompt, parseCompletionRecovery } from "./completion-recovery.ts";

const OPERATION = "abcdefab-cdef-4abc-8def-abcdefabcdef";

test("completion recovery accepts only a canonical operation and positive version", () => {
  assert.deepEqual(parseCompletionRecovery({
    version: 1,
    operation: OPERATION,
    expectedInstanceVersion: 4,
  }), {
    version: 1,
    operation: OPERATION,
    expectedInstanceVersion: 4,
  });
  assert.equal(parseCompletionRecovery({
    version: 1,
    operation: OPERATION.toUpperCase(),
    expectedInstanceVersion: 4,
  }), null);
  assert.equal(parseCompletionRecovery({
    version: 1,
    operation: OPERATION,
    expectedInstanceVersion: 0,
  }), null);
});

test("completion recovery prompt distinguishes a replay from a new command", () => {
  assert.match(completionRecoveryPrompt(true), /同一操作键/);
  assert.match(completionRecoveryPrompt(false), /确认将这一期标记为已结束/);
});
