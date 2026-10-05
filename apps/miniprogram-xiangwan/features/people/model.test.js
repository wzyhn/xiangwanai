"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const { projectPeoplePage, projectPersonDetail, summarizeText } = require("./model");

test("People projection keeps only display facts and formats publication time", () => {
  const person = {
    people_id: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
    display_name: "林老师",
    headline: "AI 社群共创者",
    introduction: "持续组织面向普通人的 AI 实践活动。",
    published_at: "2026-09-14T08:00:00Z",
  };
  const page = projectPeoplePage({
    items: [person],
    next_cursor: "next",
    as_of: "2026-09-15T09:00:00Z",
  });

  assert.equal(page.items[0].displayName, "林老师");
  assert.equal(page.items[0].publishedAt, "09-14 16:00");
  assert.equal(page.nextCursor, "next");
  assert.equal(page.asOf, "09-15 17:00");
  assert.equal(projectPersonDetail({ person }).peopleId, person.people_id);
  assert.equal(projectPersonDetail({ person }).avatarText, "林");
});

test("People summaries truncate by Unicode code point without splitting emoji", () => {
  assert.equal(summarizeText("😀😀😀", 2), "😀😀…");
  assert.equal(summarizeText("  完整介绍  ", 20), "完整介绍");
});
