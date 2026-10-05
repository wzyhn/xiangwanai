"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const {
  joinPublicMediaUrl,
  projectActivityReview,
  projectReviewContentBlock,
  groupPastActivities,
  projectPastActivitiesPage,
  projectSessionCollection,
} = require("./model");

test("an independent available cover wins without reordering photos", () => {
  const photos = [
    { type: "image", availability: "available", external_url: "https://media.example/first.webp" },
    {
      type: "image",
      availability: "available",
      is_cover: true,
      external_url: "https://media.example/second.webp",
    },
  ];
  const review = projectActivityReview({ documents: [{ blocks: photos }] });
  assert.equal(review.heroImageUrl, photos[1].external_url);
  assert.equal(review.documents[0].blocks[0].mediaUrl, photos[0].external_url);
  photos[1].availability = "policy_blocked";
  assert.equal(
    projectActivityReview({ documents: [{ blocks: photos }] }).heroImageUrl,
    photos[0].external_url,
  );
});

test("past activity projection keeps Instance identity and Series statistics", () => {
  const page = projectPastActivitiesPage({
    active_activity_type: "course",
    next_cursor: "next",
    items: [
      {
        series_id: "series-1",
        series_title: "AI 课程",
        successful_published_instance_count: 4,
        historical_registration_count: 86,
        instance_id: "instance-1",
        instance_title: "第四期",
        instance_status: "completed",
        activity_type: "course",
        completed_at: "2026-09-12T10:00:00Z",
      },
    ],
  });
  assert.equal(page.items[0].instanceId, "instance-1");
  assert.equal(page.items[0].activityType, "课程");
  assert.equal(page.items[0].historyText, "已举办 4 期 · 累计 86 人参加");
  assert.equal(page.nextCursor, "next");
  assert.equal(page.seriesGroups.length, 1);
  assert.equal(page.seriesGroups[0].items.length, 1);
});

test("past activity grouping keeps every period in a recurring series", () => {
  const groups = groupPastActivities([
    { seriesId: "series-1", seriesTitle: "AI 课程", instanceId: "instance-1" },
    { seriesId: "series-1", seriesTitle: "AI 课程", instanceId: "instance-2" },
    { seriesId: "series-2", seriesTitle: "专题", instanceId: "instance-3" },
  ]);
  assert.deepEqual(
    groups.map((group) => group.seriesId),
    ["series-1", "series-2"],
  );
  assert.deepEqual(
    groups[0].items.map((item) => item.instanceId),
    ["instance-1", "instance-2"],
  );
});

test("past activity projection joins the cover image against the API origin", () => {
  const page = projectPastActivitiesPage(
    {
      items: [
        {
          instance_id: "instance-1",
          cover_image_url: "/api/v1/xiangwan/covers/cover-1.webp",
        },
        { instance_id: "instance-2", cover_image_url: "https://evil.example/cover.webp" },
        { instance_id: "instance-3" },
      ],
    },
    { apiBaseUrl: "https://api.example.com" },
  );
  assert.equal(
    page.items[0].coverImageUrl,
    "https://api.example.com/api/v1/xiangwan/covers/cover-1.webp",
  );
  assert.equal(page.items[1].coverImageUrl, "");
  assert.equal(page.items[2].coverImageUrl, "");
});

test("review projection exposes only allowlisted public block actions", () => {
  const review = projectActivityReview(
    {
      instance_id: "instance-1",
      instance_title: "第四期",
      successful_published_instance_count: 4,
      historical_registration_count: 86,
      content_blocks: [
        { type: "text", title: "简介", body: "本期活动介绍" },
        {
          type: "image",
          url: "/api/v1/xiangwan/covers/0123456789abcdef0123456789abcdef.webp",
          caption: "现场照片",
        },
        { type: "image", url: "https://evil.example/private.webp" },
      ],
      documents: [
        {
          relation_id: "relation-1",
          scope: "instance_review",
          blocks: [
            { block_id: "text-1", type: "text", availability: "available", text: "回顾正文" },
            {
              block_id: "image-1",
              type: "image",
              availability: "available",
              media_path:
                "/api/v1/xiangwan/media/11111111-1111-4111-8111-111111111111/22222222-2222-4222-8222-222222222222/33333333-3333-4333-8333-333333333333",
            },
            {
              block_id: "image-2",
              type: "image",
              availability: "available",
              external_url: "https://media.example.com/photo.webp",
            },
            { block_id: "link-1", type: "link", availability: "unavailable" },
            {
              block_id: "link-2",
              type: "link",
              availability: "policy_blocked",
              external_url: "https://blocked.example/private",
            },
            {
              block_id: "audio-1",
              type: "audio",
              availability: "available",
              label: "现场录音",
              media_path:
                "/api/v1/xiangwan/media/11111111-1111-4111-8111-111111111111/22222222-2222-4222-8222-222222222222/44444444-4444-4444-8444-444444444444",
            },
          ],
        },
      ],
      next_instance: {
        action: "session_selection_required",
        instance_id: "instance-2",
        candidate_session_ids: ["session-1", "session-2"],
      },
    },
    { apiBaseUrl: "https://api.example.com" },
  );
  const blocks = review.documents[0].blocks;
  assert.deepEqual(review.contentBlocks[0], {
    type: "text",
    title: "简介",
    body: "本期活动介绍",
    isText: true,
    isImage: false,
  });
  assert.equal(
    review.contentBlocks[1].imageUrl,
    "https://api.example.com/api/v1/xiangwan/covers/0123456789abcdef0123456789abcdef.webp",
  );
  assert.equal(review.contentBlocks.length, 2);
  assert.equal(
    review.contentBlocks.find((block) => block.imageUrl === "https://evil.example/private.webp"),
    undefined,
  );
  assert.equal(
    projectReviewContentBlock(
      { type: "image", url: "/api/v1/xiangwan/media/private" },
      "https://api.example.com",
    ),
    null,
  );
  assert.equal(blocks[0].text, "回顾正文");
  assert.match(blocks[1].mediaUrl, /^https:\/\/api\.example\.com\/api\/v1\/xiangwan\/media\//);
  assert.equal(blocks[2].mediaUrl, "https://media.example.com/photo.webp");
  assert.equal(blocks[2].isImage, true);
  assert.equal(blocks[3].externalUrl, "");
  assert.equal(blocks[3].unavailableText, "暂未配置链接");
  assert.equal(blocks[4].externalUrl, "");
  assert.equal(blocks[4].unavailableText, "该资源暂不可访问");
  assert.equal(blocks[5].isAudio, true);
  assert.equal(blocks[5].isDownload, false);
  assert.match(blocks[5].mediaUrl, /^https:\/\/api\.example\.com\/api\/v1\/xiangwan\/media\//);
  assert.equal(review.nextInstance.label, "选择下一期场次");
});

test("review projection infers media sections when the document title is generic", () => {
  const review = projectActivityReview(
    {
      instance_id: "instance-1",
      instance_title: "第四期",
      documents: [
        {
          relation_id: "relation-1",
          title: "活动回顾",
          blocks: [
            {
              block_id: "video-1",
              type: "video",
              availability: "available",
              media_path:
                "/api/v1/xiangwan/media/11111111-1111-4111-8111-111111111111/22222222-2222-4222-8222-222222222222/33333333-3333-4333-8333-333333333333",
            },
          ],
        },
      ],
    },
    { apiBaseUrl: "https://api.example.com" },
  );

  assert.equal(review.documents[0].kind, "video");
  assert.equal(review.documents[0].blocks[0].isVideo, true);
  assert.match(
    review.documents[0].blocks[0].mediaUrl,
    /^https:\/\/api\.example\.com\/api\/v1\/xiangwan\/media\//,
  );
});

test("review projection keeps photos visible when a video review also carries links", () => {
  const review = projectActivityReview({
    instance_id: "instance-1",
    instance_title: "第四期",
    documents: [
      {
        relation_id: "relation-1",
        title: "本期视频回顾",
        blocks: [
          {
            block_id: "photo-1",
            type: "image",
            availability: "available",
            external_url: "https://media.example.com/photo.webp",
          },
          {
            block_id: "video-1",
            type: "link",
            availability: "available",
            external_url: "https://video.example.com/1",
          },
        ],
      },
    ],
  });
  assert.equal(review.documents[0].kind, "mixed");
  assert.equal(review.documents[0].blocks[0].mediaUrl, "https://media.example.com/photo.webp");
});

test("public media URL join rejects external and malformed paths", () => {
  assert.equal(joinPublicMediaUrl("https://api.example.com", "https://evil.example/file"), "");
  assert.equal(joinPublicMediaUrl("javascript:alert(1)", "/api/v1/xiangwan/media/a"), "");
  assert.equal(joinPublicMediaUrl("https://api.example.com", "/other/path"), "");
});

test("Session collection projection preserves every explicit choice", () => {
  const collection = projectSessionCollection({
    action: "session_selection_required",
    series_id: "series-1",
    instance_id: "instance-2",
    sessions: [
      {
        session_id: "session-a",
        session_title: "上午工作坊",
        status: "published",
        session_start_at: "2026-10-01T10:00:00Z",
      },
      {
        session_id: "session-b",
        session_title: "下午工作坊",
        status: "published",
        session_start_at: "2026-10-01T12:00:00Z",
      },
    ],
  });
  assert.equal(collection.seriesId, "series-1");
  assert.deepEqual(
    collection.sessions.map((session) => session.sessionId),
    ["session-a", "session-b"],
  );
  assert.deepEqual(
    collection.sessions.map((session) => session.label),
    ["上午工作坊", "下午工作坊"],
  );
  assert.equal(collection.directSessionId, "");
});
