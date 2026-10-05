"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const { createExternalResourcePageDefinition, resolveExternalResourceUrl } = require("./index");

const INSTANCE_ID = "11111111-1111-4111-8111-111111111111";
const SESSION_ID = "22222222-2222-4222-8222-222222222222";
const BLOCK_ID = "33333333-3333-4333-8333-333333333333";

function flush() {
  return new Promise((resolve) => setImmediate(resolve));
}

test("native video uses exact string IDs from the fresh published block and explicit tap", async (t) => {
  const opens = [];
  global.wx = { openChannelsActivity: (options) => opens.push(options) };
  t.after(() => {
    delete global.wx;
  });
  let published = true;
  const page = createExternalResourcePageDefinition({
    async getPublicReview() {
      return {
        documents: [
          {
            blocks: published
              ? [
                  {
                    block_id: BLOCK_ID,
                    type: "link",
                    availability: "available",
                    video_channel: {
                      finder_user_name: "sphExampleID",
                      feed_id: "900719925474099312345",
                    },
                  },
                ]
              : [],
          },
        ],
      };
    },
  });
  page.setData = (changes) => {
    page.data = { ...page.data, ...changes };
  };
  page.onLoad({ instance_id: INSTANCE_ID, block_id: BLOCK_ID });
  await flush();
  assert.equal(opens.length, 0);
  assert.equal(page.data.url, "");
  page.openVideoChannel();
  assert.equal(opens[0].finderUserName, "sphExampleID");
  assert.equal(opens[0].feedId, "900719925474099312345");
  published = false;
  await page.loadResource();
  page.openVideoChannel();
  assert.equal(opens.length, 1);
  assert.equal(page.data.videoChannel, null);
  assert.match(page.data.errorMessage, /失效/);
});

test("malformed native IDs and unavailable blocks never open a video or web view", async (t) => {
  global.wx = { openChannelsActivity: () => assert.fail("invalid resource opened") };
  t.after(() => {
    delete global.wx;
  });
  for (const block of [
    { availability: "available", video_channel: { finder_user_name: "sphExample", feed_id: "" } },
    {
      availability: "available",
      external_url: "https://feishu.cn/docx/1",
      video_channel: { finder_user_name: "sphExample", feed_id: "123" },
    },
    {
      availability: "policy_blocked",
      video_channel: { finder_user_name: "sphExample", feed_id: "123" },
    },
  ]) {
    const page = createExternalResourcePageDefinition({
      async getPublicReview() {
        return { documents: [{ blocks: [{ block_id: BLOCK_ID, type: "link", ...block }] }] };
      },
    });
    page.setData = (changes) => {
      page.data = { ...page.data, ...changes };
    };
    page.onLoad({ instance_id: INSTANCE_ID, block_id: BLOCK_ID });
    await flush();
    page.openVideoChannel();
    assert.equal(page.data.url, "");
    assert.equal(page.data.videoChannel, null);
    assert.match(page.data.errorMessage, /失效/);
  }
});

test("external resource page resolves an exact server-approved review block", async () => {
  const calls = [];
  const page = createExternalResourcePageDefinition({
    async getPublicReview(instanceId, sessionId) {
      calls.push({ instanceId, sessionId });
      return {
        documents: [
          {
            blocks: [
              {
                block_id: BLOCK_ID,
                type: "link",
                availability: "available",
                external_url: "https://docs.example.com/activity?id=1",
              },
            ],
          },
        ],
      };
    },
  });
  page.setData = (changes) => {
    page.data = { ...page.data, ...changes };
  };

  page.onLoad({ instance_id: INSTANCE_ID, session_id: SESSION_ID, block_id: BLOCK_ID });
  await flush();

  assert.deepEqual(calls, [{ instanceId: INSTANCE_ID, sessionId: SESSION_ID }]);
  assert.equal(page.data.url, "https://docs.example.com/activity?id=1");
  assert.equal(page.data.errorMessage, "");
  assert.equal(page.data.loading, false);
});

test("external resource page rejects a crafted raw URL without calling the API", () => {
  let calls = 0;
  const page = createExternalResourcePageDefinition({
    async getPublicReview() {
      calls += 1;
      return {};
    },
  });
  page.setData = (changes) => {
    page.data = { ...page.data, ...changes };
  };

  page.onLoad({ url: encodeURIComponent("https://evil.example/path") });

  assert.equal(calls, 0);
  assert.equal(page.data.url, "");
  assert.equal(page.data.loading, false);
  assert.match(page.data.errorMessage, /无效/);
});

test("external resource resolution fails closed for blocked or duplicate blocks", () => {
  const blocked = {
    documents: [
      {
        blocks: [
          {
            block_id: BLOCK_ID,
            type: "link",
            availability: "policy_blocked",
            external_url: "https://blocked.example/path",
          },
        ],
      },
    ],
  };
  assert.equal(resolveExternalResourceUrl(blocked, BLOCK_ID), "");
  assert.equal(
    resolveExternalResourceUrl(
      { documents: [...blocked.documents, ...blocked.documents] },
      BLOCK_ID,
    ),
    "",
  );
});
