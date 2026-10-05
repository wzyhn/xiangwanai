"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const { createPeoplePageDefinition, mergePeople } = require("./index");

const PEOPLE_ID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa";

function flush() {
  return new Promise((resolve) => setImmediate(resolve));
}

function attachSetData(page) {
  page.setData = (changes) => {
    page.data = { ...page.data, ...changes };
  };
}

test("People page loads anonymously and navigates with the exact PeopleProfile", async (t) => {
  const calls = [];
  const navigations = [];
  global.wx = {
    navigateTo: ({ url }) => navigations.push(url),
    stopPullDownRefresh() {},
  };
  t.after(() => delete global.wx);
  const page = createPeoplePageDefinition({
    async getPeople(filter) {
      calls.push(filter);
      return {
        items: [
          {
            people_id: PEOPLE_ID,
            display_name: "林老师",
            introduction: "介绍",
            published_at: "2026-09-14T08:00:00Z",
          },
        ],
        as_of: "2026-09-15T09:00:00Z",
      };
    },
  });
  attachSetData(page);

  page.onLoad();
  await flush();
  page.openPerson({ currentTarget: { dataset: { peopleId: PEOPLE_ID } } });

  assert.deepEqual(calls, [{ cursor: "", limit: 20 }]);
  assert.deepEqual(navigations, [`/pages/person-detail/index?people_id=${PEOPLE_ID}`]);
});

test("People pagination de-duplicates stable profile cards", () => {
  assert.deepEqual(
    mergePeople(
      [{ peopleId: "one" }, { peopleId: "two" }],
      [{ peopleId: "two" }, { peopleId: "three" }],
    ).map(({ peopleId }) => peopleId),
    ["one", "two", "three"],
  );
});
