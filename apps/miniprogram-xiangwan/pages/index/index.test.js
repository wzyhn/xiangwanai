"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { createHomePageDefinition, formatLocalDateLabel, todayInShanghai } = require("./index");

function attachPageRuntime(page) {
  page.data = { ...page.data, filter: { ...page.data.filter } };
  page.setData = (changes) => {
    Object.entries(changes).forEach(([key, value]) => {
      if (key.startsWith("filter.")) page.data.filter[key.slice(7)] = value;
      else page.data[key] = value;
    });
  };
}

test("Shanghai date helpers keep the exact local-date filter label", () => {
  assert.equal(todayInShanghai(new Date("2026-09-15T16:30:00.000Z")), "2026-09-16");
  assert.equal(formatLocalDateLabel("2026-09-20"), "9月20日");
  assert.equal(formatLocalDateLabel("invalid"), "指定日期");
});

test("home sends an explicit local date and clears it when switching time windows", () => {
  const filters = [];
  const page = createHomePageDefinition({
    async getHomeSessions(filter) {
      filters.push(filter);
      return { cards: [] };
    },
  });
  attachPageRuntime(page);
  page._active = true;
  page.loadHome = (append) => {
    filters.push({ ...page.data.filter, append });
  };

  page.selectLocalDate({ detail: { value: "2026-09-20" } });
  assert.equal(page.data.filter.timeWindow, "local_date");
  assert.equal(page.data.filter.localDate, "2026-09-20");
  assert.equal(page.data.timeFilterLabel, "9月20日");
  assert.equal(page.data.timeFilters.at(-1).label, "9月20日");
  assert.equal(filters[0].localDate, "2026-09-20");

  page.selectTime({ currentTarget: { dataset: { value: "this_week" } } });
  assert.equal(page.data.filter.timeWindow, "this_week");
  assert.equal(page.data.filter.localDate, "");
  assert.equal(page.data.timeFilterLabel, "本周");
});

test("home quick-tag reset returns to the unfiltered feed", () => {
  const page = createHomePageDefinition();
  attachPageRuntime(page);
  page.data.filter.quickTags = ["roundtable"];
  page.loadHome = () => {};

  page.clearQuickTags();

  assert.deepEqual(page.data.filter.quickTags, []);
});

test("home filter triggers show only the selected value and retain accessible context", () => {
  const template = fs.readFileSync(path.join(__dirname, "index.wxml"), "utf8");
  for (const value of ["activityFilterLabel", "areaFilterLabel", "timeFilterLabel"])
    assert.ok(template.includes(">{{" + value + "}}</text>"));
  assert.doesNotMatch(template, /filter-trigger-label/);
  assert.match(template, /aria-label="筛选区域：/);
});
