import test from "node:test";
import assert from "node:assert/strict";
import { questionnaireFieldTypeLabel } from "./questionnaire-labels.ts";

test("questionnaire field types have operator-facing Chinese labels", () => {
  assert.equal(questionnaireFieldTypeLabel("single_line"), "单行文本");
  assert.equal(questionnaireFieldTypeLabel("multiple_choice"), "多选");
  assert.equal(questionnaireFieldTypeLabel("area"), "地区");
  assert.equal(questionnaireFieldTypeLabel("unknown"), "未知字段类型");
});
