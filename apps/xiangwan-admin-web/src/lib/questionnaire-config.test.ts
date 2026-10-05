import test from "node:test";
import assert from "node:assert/strict";
import { validateQuestionnaireConfig } from "./questionnaire-config.ts";

test("questionnaire validation applies to a non-custom activity", () => {
  assert.equal(validateQuestionnaireConfig({
    privacyPurpose: "用于课程报名",
    privacyPolicyVersion: "privacy-v1",
    fields: [{
      code: "role",
      type: "single_line",
      label: "职业",
      maxLength: 100,
      maxSelections: 1,
      options: [],
    }],
  }), "");
});

test("questionnaire validation keeps the platform-only form optional", () => {
  assert.equal(validateQuestionnaireConfig({
    privacyPurpose: "",
    privacyPolicyVersion: "",
    fields: [],
  }), "");
});
