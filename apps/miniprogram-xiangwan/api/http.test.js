"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const { request, uploadFile } = require("./http");

test("bodyless profile reads clear the shared JSON content type default", async (t) => {
  const calls = [];
  global.getApp = () => ({
    globalData: {
      apiBaseUrl: "https://api.weconq.cn",
      accessToken: "profile-token",
    },
  });
  global.wx = {
    request(options) {
      calls.push(options);
      options.success({
        statusCode: 200,
        data: { code: 0, data: { nickname: "小林" } },
      });
    },
  };
  t.after(() => {
    delete global.getApp;
    delete global.wx;
  });

  const profile = await request({
    url: "/api/v1/xiangwan/me/profile",
    header: { "Content-Type": "" },
  });

  assert.deepEqual(profile, { nickname: "小林" });
  assert.equal(calls.length, 1);
  assert.equal(calls[0].method, "GET");
  assert.equal(calls[0].header["Content-Type"], "");
  assert.equal(calls[0].header.Authorization, "Bearer profile-token");
  assert.equal(calls[0].data, undefined);
  assert.match(calls[0].header["X-Request-ID"], /^fe-/);
});

test("uploadFile recovers authentication once and retries with the refreshed token", async (t) => {
  const headers = [];
  const app = {
    globalData: {
      apiBaseUrl: "https://api.weconq.cn",
      accessToken: "expired-token",
    },
    resetRejectedSession() {
      this.globalData.accessToken = "";
    },
    async ensureAuthenticated() {
      this.globalData.accessToken = "fresh-token";
    },
  };
  global.getApp = () => app;
  global.wx = {
    uploadFile(options) {
      headers.push(options.header);
      if (headers.length === 1) {
        options.success({
          statusCode: 401,
          data: JSON.stringify({ code: 10002, message: "authentication required" }),
        });
        return;
      }
      options.success({ statusCode: 200, data: JSON.stringify({ code: 0, data: { ok: true } }) });
    },
  };
  t.after(() => {
    delete global.getApp;
    delete global.wx;
  });

  const result = await uploadFile({
    url: "/api/v1/xiangwan/me/avatar",
    filePath: "wxfile://tmp/avatar.png",
    name: "avatar",
  });

  assert.deepEqual(result, { ok: true });
  assert.deepEqual(headers, [
    { Authorization: "Bearer expired-token" },
    { Authorization: "Bearer fresh-token" },
  ]);
});
