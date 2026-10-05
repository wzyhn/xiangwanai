"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const { joinXiangwanMediaUrl } = require("./media-url");

test("joinXiangwanMediaUrl joins the four allowlisted public prefixes", () => {
  const base = "https://api.example.com";
  for (const prefix of ["covers", "avatars", "brand-hero"]) {
    assert.equal(
      joinXiangwanMediaUrl(base, `/api/v1/xiangwan/${prefix}/f-1.webp`),
      `${base}/api/v1/xiangwan/${prefix}/f-1.webp`,
    );
  }
  assert.equal(
    joinXiangwanMediaUrl(`${base}/`, "/api/v1/xiangwan/media/aa/bb-cc"),
    `${base}/api/v1/xiangwan/media/aa/bb-cc`,
  );
});

test("joinXiangwanMediaUrl rejects external, traversal and unknown paths", () => {
  const base = "https://api.example.com";
  assert.equal(
    joinXiangwanMediaUrl(base, "https://evil.example/api/v1/xiangwan/covers/f.webp"),
    "",
  );
  assert.equal(joinXiangwanMediaUrl(base, "/api/v1/xiangwan/private/f.webp"), "");
  assert.equal(joinXiangwanMediaUrl(base, "/api/v1/xiangwan/covers/.."), "");
  assert.equal(joinXiangwanMediaUrl(base, "/api/v1/xiangwan/covers/../secret"), "");
  assert.equal(joinXiangwanMediaUrl(base, "/API/V1/XIANGWAN/COVERS/F.WEBP"), "");
  assert.equal(joinXiangwanMediaUrl(base, ""), "");
  assert.equal(joinXiangwanMediaUrl(base, null), "");
});

test("joinXiangwanMediaUrl accepts same-origin absolute https URLs", () => {
  const base = "https://api.example.com";
  assert.equal(
    joinXiangwanMediaUrl(base, `${base}/api/v1/xiangwan/covers/f-1.webp`),
    `${base}/api/v1/xiangwan/covers/f-1.webp`,
  );
  // 同源但路径不在白名单 → 拒绝
  assert.equal(joinXiangwanMediaUrl(base, `${base}/api/v1/xiangwan/private/f.webp`), "");
  // 跨源绝对 URL → 拒绝(即便路径形如白名单)
  assert.equal(
    joinXiangwanMediaUrl(base, "https://api.example.com.evil/api/v1/xiangwan/covers/f.webp"),
    "",
  );
  // 绝对 URL 仅认 https
  assert.equal(
    joinXiangwanMediaUrl(
      "http://127.0.0.1:8082",
      "http://127.0.0.1:8082/api/v1/xiangwan/covers/f.webp",
    ),
    "",
  );
});

test("joinXiangwanMediaUrl rejects malformed API origins", () => {
  const path = "/api/v1/xiangwan/covers/f.webp";
  assert.equal(joinXiangwanMediaUrl("", path), "");
  assert.equal(joinXiangwanMediaUrl("javascript:alert(1)", path), "");
  assert.equal(joinXiangwanMediaUrl("https://api.example.com/path?q=1", path), "");
});

test("allowAnyHttpsAbsolute passes foreign https media through untouched", () => {
  const cdn = "https://third-party.cdn.example.com/face.jpg";
  assert.equal(
    joinXiangwanMediaUrl("https://api.example.com", cdn, { allowAnyHttpsAbsolute: true }),
    cdn,
  );
  // base 不可用也放行:微信 CDN 旧头像与 API base 无关
  assert.equal(joinXiangwanMediaUrl("", cdn, { allowAnyHttpsAbsolute: true }), cdn);
  assert.equal(joinXiangwanMediaUrl("not-a-url", cdn, { allowAnyHttpsAbsolute: true }), cdn);
  // 默认模式仍拒绝跨源绝对地址
  assert.equal(joinXiangwanMediaUrl("https://api.example.com", cdn), "");
  assert.equal(joinXiangwanMediaUrl("https://api.example.com", cdn, {}), "");
  // 仅放行 https;http 绝对地址与带 query/hash 的地址仍按相对路径规则拒绝
  assert.equal(
    joinXiangwanMediaUrl("", "http://third-party.cdn.example.com/face.jpg", {
      allowAnyHttpsAbsolute: true,
    }),
    "",
  );
  assert.equal(
    joinXiangwanMediaUrl("", "https://third-party.cdn.example.com/face.jpg?v=1", {
      allowAnyHttpsAbsolute: true,
    }),
    "",
  );
  // 相对路径与白名单规则不受该开关影响
  assert.equal(
    joinXiangwanMediaUrl("https://api.example.com", "/api/v1/xiangwan/avatars/a1.png", {
      allowAnyHttpsAbsolute: true,
    }),
    "https://api.example.com/api/v1/xiangwan/avatars/a1.png",
  );
  assert.equal(
    joinXiangwanMediaUrl("https://api.example.com", "/api/v1/xiangwan/private/f.webp", {
      allowAnyHttpsAbsolute: true,
    }),
    "",
  );
});
