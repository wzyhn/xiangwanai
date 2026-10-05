"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const { createPastActivitiesPageDefinition, mergePastActivities } = require("./index");

function flush() {
  return new Promise((resolve) => setImmediate(resolve));
}

test("past activities reload by category and navigate with exact Instance", async (t) => {
  const calls = [];
  const navigations = [];
  global.wx = {
    navigateTo: ({ url }) => navigations.push(url),
    stopPullDownRefresh() {},
  };
  t.after(() => delete global.wx);
  const page = createPastActivitiesPageDefinition({
    async getPastActivities(filter) {
      calls.push(filter);
      return {
        items: [
          {
            instance_id: "instance-1",
            instance_title: "第一期",
            activity_type: "course",
            instance_status: "completed",
          },
        ],
      };
    },
  });
  page.setData = (changes) => {
    Object.entries(changes).forEach(([key, value]) => {
      if (key === "filter.activityType") page.data.filter.activityType = value;
      else page.data[key] = value;
    });
  };
  page.onLoad();
  await flush();
  page.selectActivity({ currentTarget: { dataset: { value: "course" } } });
  await flush();
  page.openReview({ currentTarget: { dataset: { instanceId: "instance-1" } } });

  assert.equal(calls.length, 2);
  assert.equal(calls[1].activityType, "course");
  assert.equal(navigations[0], "/pages/activity-review/index?instance_id=instance-1");
});

test("past activity pagination de-duplicates Instance cards", () => {
  assert.deepEqual(
    mergePastActivities(
      [{ instanceId: "one" }, { instanceId: "two" }],
      [{ instanceId: "two" }, { instanceId: "three" }],
    ).map((item) => item.instanceId),
    ["one", "two", "three"],
  );
});

test("excellent works placeholder invalidates a slower request from the previous category", async () => {
  let resolveRequest;
  const page = createPastActivitiesPageDefinition({
    getPastActivities() {
      return new Promise((resolve) => {
        resolveRequest = resolve;
      });
    },
  });
  page.setData = (changes) => {
    Object.entries(changes).forEach(([key, value]) => {
      if (key === "filter.activityType") page.data.filter.activityType = value;
      else page.data[key] = value;
    });
  };
  page.onLoad();
  page.selectActivity({ currentTarget: { dataset: { value: "excellent_works" } } });
  assert.equal(page.data.emptyMessage, "优秀作品即将上线");

  resolveRequest({
    items: [
      {
        instance_id: "stale-instance",
        instance_title: "旧分类结果",
        instance_status: "completed",
      },
    ],
  });
  await flush();
  await flush();

  assert.deepEqual(page.data.items, []);
  assert.deepEqual(page.data.seriesGroups, []);
  assert.equal(page.data.emptyMessage, "优秀作品即将上线");
});
