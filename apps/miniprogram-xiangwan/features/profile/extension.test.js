"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const { buildPrivateProfileExtension, parseTags } = require("./extension");

const current = {
  profile: {
    version: 2,
    occupation: "设计师",
    introduction: "旧简介",
    tags: ["咖啡"],
    visibility: { occupation: true, introduction: true, tags: true },
  },
};

test("optional tags and introduction preserve occupation and default to private", () => {
  const result = buildPrivateProfileExtension(current, "  喜欢交流  ", "徒步，AI");
  assert.deepEqual(result, {
    ok: true,
    unchanged: false,
    payload: {
      expected_version: 2,
      occupation: "设计师",
      introduction: "喜欢交流",
      tags: ["徒步", "AI"],
      visibility: { occupation: true, introduction: false, tags: false },
    },
  });
  assert.equal(parseTags("AI,AI"), null);
  assert.equal(buildPrivateProfileExtension(current, "x".repeat(501), "").ok, false);
});

test("pending profile updates are not overwritten by onboarding", () => {
  const pending = {
    ...current,
    pending_update: { candidate_version: 3, introduction: "待审核", tags: ["AI"] },
  };
  assert.deepEqual(buildPrivateProfileExtension(pending, "待审核", "AI"), {
    ok: true,
    unchanged: true,
  });
  assert.match(buildPrivateProfileExtension(pending, "新内容", "AI").message, /正在审核/);
});
