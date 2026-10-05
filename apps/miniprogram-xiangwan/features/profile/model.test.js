"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const {
  extractAvatarUrl,
  isXiangwanProfile,
  joinAvatarUrl,
  projectMyProfile,
  projectProfileBadge,
  validateNickname,
} = require("./model");

test("avatar url joins only the avatars prefix onto a valid api base", () => {
  assert.equal(
    joinAvatarUrl("https://api.weconq.cn/", "/api/v1/xiangwan/avatars/a1-b2.png"),
    "https://api.weconq.cn/api/v1/xiangwan/avatars/a1-b2.png",
  );
  assert.equal(
    joinAvatarUrl("http://127.0.0.1:8082", "/api/v1/xiangwan/avatars/a1.png"),
    "http://127.0.0.1:8082/api/v1/xiangwan/avatars/a1.png",
  );
  assert.equal(
    joinAvatarUrl("", "https://third-party.cdn.example.com/face.jpg"),
    "https://third-party.cdn.example.com/face.jpg",
    "absolute https urls (e.g. WeChat CDN) pass through untouched",
  );
  assert.equal(
    joinAvatarUrl("https://api.weconq.cn", "https://api.weconq.cn/api/v1/xiangwan/avatars/a1.png"),
    "https://api.weconq.cn/api/v1/xiangwan/avatars/a1.png",
    "same-origin absolute avatar urls also pass through untouched",
  );
});

test("avatar url rejects other paths, traversal and unusable bases", () => {
  const base = "https://api.weconq.cn";
  assert.equal(joinAvatarUrl(base, ""), "");
  assert.equal(joinAvatarUrl(base, "   "), "");
  assert.equal(joinAvatarUrl(base, "/api/v1/xiangwan/private/a"), "");
  assert.equal(joinAvatarUrl(base, "/api/v1/xiangwan/avatars/../secret"), "");
  assert.equal(joinAvatarUrl(base, "/api/v1/xiangwan/avatars/"), "");
  assert.equal(joinAvatarUrl(base, "javascript:alert(1)"), "");
  assert.equal(joinAvatarUrl(base, "http://insecure.example.com/a.png"), "");
  assert.equal(joinAvatarUrl("not-a-url", "/api/v1/xiangwan/avatars/a.png"), "");
  assert.equal(joinAvatarUrl("", "/api/v1/xiangwan/avatars/a.png"), "");
});

test("avatar url shares the public media whitelist for relative paths", () => {
  const base = "https://api.weconq.cn";
  assert.equal(
    joinAvatarUrl(base, "/api/v1/xiangwan/media/a/b/c"),
    `${base}/api/v1/xiangwan/media/a/b/c`,
    "relative paths follow the shared whitelist, not an avatars-only copy",
  );
});

test("avatar extraction prefers avatar_url and tolerates the url alias", () => {
  assert.equal(extractAvatarUrl({ avatar_url: "/a.png", url: "/b.png" }), "/a.png");
  assert.equal(extractAvatarUrl({ avatarUrl: "/camel.png", url: "/b.png" }), "/camel.png");
  assert.equal(extractAvatarUrl({ data: { avatarUrl: "/nested.png" } }), "/nested.png");
  assert.equal(extractAvatarUrl({ url: "/b.png" }), "/b.png");
  assert.equal(extractAvatarUrl({ avatar_url: "  ", url: "/b.png" }), "/b.png");
  assert.equal(extractAvatarUrl({}), "");
  assert.equal(extractAvatarUrl(null), "");
  assert.equal(extractAvatarUrl("https://x"), "");
});

test("nickname validation trims, counts code points and rejects blank input", () => {
  assert.deepEqual(validateNickname("  小林  "), { ok: true, nickname: "小林", message: "" });
  assert.deepEqual(validateNickname("享"), { ok: true, nickname: "享", message: "" });
  assert.equal(validateNickname("🙂".repeat(64)).ok, true);
  assert.equal(validateNickname("🙂".repeat(65)).ok, false);
  assert.equal(validateNickname("x".repeat(64)).ok, true);
  assert.equal(validateNickname("x".repeat(65)).ok, false);
  for (const value of ["", "   ", "\n\t ", null, undefined]) {
    const result = validateNickname(value);
    assert.equal(result.ok, false);
    assert.equal(result.message, "昵称需为 1–64 个字符");
  }
});

test("profile projection normalizes nickname, joins avatar and keeps the etag", () => {
  const profile = projectMyProfile(
    {
      nickname: "  小林  ",
      avatar_url: "/api/v1/xiangwan/avatars/a.png",
      principal_profile_etag: "etag-7",
    },
    "https://api.weconq.cn",
  );
  assert.deepEqual(profile, {
    nickname: "小林",
    avatarUrl: "https://api.weconq.cn/api/v1/xiangwan/avatars/a.png",
    etag: "etag-7",
  });
  assert.deepEqual(projectMyProfile({}, "https://api.weconq.cn"), {
    nickname: "",
    avatarUrl: "",
    etag: "",
  });
  assert.deepEqual(
    projectMyProfile(
      {
        data: {
          nickname: " 新用户 ",
          avatarUrl:
            "/api/v1/xiangwan/avatars/legacy/00000000-0000-0000-0000-000000000001/00000000-0000-0000-0000-000000000002",
          principalProfileETag: "camel-etag",
        },
      },
      "https://api.weconq.cn",
    ),
    {
      nickname: "新用户",
      avatarUrl:
        "https://api.weconq.cn/api/v1/xiangwan/avatars/legacy/00000000-0000-0000-0000-000000000001/00000000-0000-0000-0000-000000000002",
      etag: "camel-etag",
    },
  );
});

test("profile badge falls back to the gradient initial and setup hint", () => {
  const badge = projectProfileBadge({
    nickname: "小林",
    avatarUrl: "https://a.example.com/x.png",
    etag: "e1",
  });
  assert.equal(badge.initial, "小");
  assert.equal(badge.displayName, "小林");
  assert.equal(badge.hasNickname, true);

  const emoji = projectProfileBadge({ nickname: "🙂教练", avatarUrl: "", etag: "e2" });
  assert.equal(emoji.initial, "🙂");

  for (const value of [
    null,
    undefined,
    {},
    { nickname: "平台昵称", avatarUrl: "https://x" },
    { nickname: "", avatarUrl: "", etag: "" },
  ]) {
    const fallback = projectProfileBadge(value);
    assert.equal(fallback.initial, "享");
    assert.equal(fallback.avatarUrl, "");
    assert.equal(fallback.hasNickname, false);
    assert.equal(fallback.displayName, "设置头像昵称 ›");
  }

  assert.equal(isXiangwanProfile({ nickname: "", avatarUrl: "", etag: "" }), true);
  assert.equal(isXiangwanProfile({ nickname: "平台昵称" }), false);
  assert.equal(isXiangwanProfile(null), false);
});
