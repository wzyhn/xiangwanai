"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const { projectFavoritesPage } = require("./model");

test("favorites projection preserves server availability and count", () => {
  const page = projectFavoritesPage(
    {
      items: [
        {
          series_id: "44444444-4444-4444-8444-444444444444",
          title: "AI 圆桌",
          series_status: "active",
          favorite_count: 8,
          favorite_avatars: ["https://thirdwx.qlogo.cn/avatar/1"],
          favorited_at: "2026-09-15T08:00:00Z",
          sessions_available: true,
        },
      ],
      next_cursor: "next",
    },
    { apiBaseUrl: "https://api.example.com" },
  );

  assert.equal(page.items[0].title, "AI 圆桌");
  assert.equal(page.items[0].favoriteCountText, "8 人收藏");
  assert.equal(page.items[0].wantCountText, "8 人想去");
  assert.deepEqual(page.items[0].favoriteAvatars, ["https://thirdwx.qlogo.cn/avatar/1"]);
  assert.equal(page.items[0].sessionsAvailable, true);
  assert.equal(page.nextCursor, "next");
});
