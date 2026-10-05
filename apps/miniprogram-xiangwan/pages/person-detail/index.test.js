"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const { createPersonDetailPageDefinition } = require("./index");

const PEOPLE_ID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa";

function flush() {
  return new Promise((resolve) => setImmediate(resolve));
}

function attachSetData(page) {
  page.setData = (changes) => {
    page.data = { ...page.data, ...changes };
  };
}

test("person detail reads only its canonical PeopleProfile and shares the same target", async () => {
  const calls = [];
  const page = createPersonDetailPageDefinition({
    async getPerson(peopleId) {
      calls.push(peopleId);
      return {
        person: {
          people_id: peopleId,
          display_name: "林老师",
          introduction: "介绍",
          published_at: "2026-09-14T08:00:00Z",
        },
      };
    },
  });
  attachSetData(page);

  page.onLoad({ people_id: PEOPLE_ID });
  await flush();

  assert.deepEqual(calls, [PEOPLE_ID]);
  assert.equal(page.data.person.displayName, "林老师");
  assert.deepEqual(page.onShareAppMessage(), {
    title: "林老师 · 享玩 AI",
    path: `/pages/person-detail/index?people_id=${PEOPLE_ID}`,
  });
});

test("person detail rejects a non-canonical route before API dispatch", () => {
  let calls = 0;
  const page = createPersonDetailPageDefinition({
    async getPerson() {
      calls += 1;
    },
  });
  attachSetData(page);
  page.onLoad({ people_id: "not-a-person" });

  assert.equal(calls, 0);
  assert.match(page.data.errorMessage, /无效/);
});

test("person detail removes a cached profile after the server withdraws it", async () => {
  let calls = 0;
  const page = createPersonDetailPageDefinition({
    async getPerson(peopleId) {
      calls += 1;
      if (calls === 1) {
        return {
          person: {
            people_id: peopleId,
            display_name: "林老师",
            introduction: "介绍",
            published_at: "2026-09-14T08:00:00Z",
          },
        };
      }
      throw Object.assign(new Error("withdrawn"), { statusCode: 404 });
    },
  });
  attachSetData(page);

  page.onLoad({ people_id: PEOPLE_ID });
  await flush();
  assert.equal(page.data.person.displayName, "林老师");

  await page.loadPerson();
  assert.equal(page.data.person, null);
  assert.match(page.data.errorMessage, /下线|不存在/);
});
