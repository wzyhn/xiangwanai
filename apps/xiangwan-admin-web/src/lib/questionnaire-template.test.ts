import test from "node:test";
import assert from "node:assert/strict";
import { buildTemplateFields, parseTemplateOptions } from "./questionnaire-template.ts";

test("template option editor accepts code=label rows and readable fallback labels", () => {
  assert.deepEqual(parseTemplateOptions("beginner=零基础\n有经验"), [
    { code: "beginner", label: "零基础" },
    { code: "___", label: "有经验" },
  ]);
});

test("template field payload clears incompatible limits and preserves display order", () => {
  assert.deepEqual(buildTemplateFields([
    {
      code: "role", type: "single_choice", label: "角色", helpText: "选择一个",
      required: true, minLength: "2", maxLength: "200", maxSelections: "3",
      options: "student=学生\nteacher=老师",
    },
    {
      code: "note", type: "multiline", label: "备注", helpText: "",
      required: false, minLength: "", maxLength: "1000", maxSelections: "",
      options: "ignored=忽略",
    },
  ]), [
    {
      code: "role", type: "single_choice", label: "角色", help_text: "选择一个",
      required: true, sort_order: 0, min_length: null, max_length: null,
      max_selections: null, options: [{ code: "student", label: "学生" }, { code: "teacher", label: "老师" }],
    },
    {
      code: "note", type: "multiline", label: "备注", help_text: "",
      required: false, sort_order: 1, min_length: null, max_length: 1000,
      max_selections: null, options: [],
    },
  ]);
});
