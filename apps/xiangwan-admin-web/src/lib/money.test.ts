import assert from "node:assert/strict";
import test from "node:test";
import { formatCentsAsYuan } from "./money.ts";

test("refund cents retain precision beyond JavaScript's safe integer", () => {
  assert.equal(formatCentsAsYuan("9007199254740993"), "¥90071992547409.93");
  assert.equal(formatCentsAsYuan("0"), "¥0.00");
  assert.equal(formatCentsAsYuan("12.5"), "金额待核对");
});
