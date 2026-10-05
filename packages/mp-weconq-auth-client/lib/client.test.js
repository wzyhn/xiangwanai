"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");

const { createWeconqAuthClient } = require("./client");

function createNotFoundError(message = "not found") {
  const error = new Error(message);
  error.statusCode = 404;
  return error;
}

test("createWeconqAuthClient caches the resolved auth route per method", async () => {
  const urls = [];
  const client = createWeconqAuthClient({
    request: async (options = {}) => {
      const url = String(options.url || "").trim();
      urls.push(
        `${String(options.method || "GET")
          .trim()
          .toUpperCase()} ${url}`,
      );
      if (url === "/api/v1/auth/me") {
        throw createNotFoundError();
      }
      return {
        id: "principal-1",
      };
    },
  });

  await client.getCurrentProfile({ token: "token-1" });
  await client.getCurrentProfile({ token: "token-1" });

  assert.deepEqual(urls, ["GET /api/v1/auth/me", "GET /api/auth/me", "GET /api/auth/me"]);
});

test("createWeconqAuthClient does not cache a full 404 probe chain", async () => {
  const urls = [];
  const client = createWeconqAuthClient({
    request: async (options = {}) => {
      const url = String(options.url || "").trim();
      urls.push(
        `${String(options.method || "GET")
          .trim()
          .toUpperCase()} ${url}`,
      );
      throw createNotFoundError("principal not found");
    },
  });

  await assert.rejects(() => client.verifySession({ token: "token-1" }), { statusCode: 404 });
  await assert.rejects(() => client.verifySession({ token: "token-1" }), { statusCode: 404 });

  assert.deepEqual(urls, [
    "POST /api/v1/auth/verify",
    "POST /api/auth/verify",
    "POST /auth/verify",
    "POST /api/v1/auth/verify",
    "POST /api/auth/verify",
    "POST /auth/verify",
  ]);
});

test("login uses the canonical route without probing the retired wx-login client alias", async () => {
  const requests = [];
  const client = createWeconqAuthClient({
    request: async (options = {}) => {
      requests.push(options);
      return {
        token: "token-1",
        principal_id: "principal-1",
        app_id: "wx-study",
      };
    },
  });

  const session = await client.loginWithWeChat({
    appId: " wx-study ",
    code: " login-code ",
  });

  assert.equal(session.principalId, "principal-1");
  assert.equal(requests.length, 1);
  assert.equal(requests[0].url, "/api/v1/auth/wechat/login");
  assert.deepEqual(requests[0].data, {
    app_id: "wx-study",
    code: "login-code",
  });
});

test("login fallback is limited to the two compatibility prefixes", async () => {
  const urls = [];
  const client = createWeconqAuthClient({
    request: async (options = {}) => {
      urls.push(options.url);
      throw createNotFoundError();
    },
  });

  await assert.rejects(() => client.loginWithWeChat({ appId: "wx-study", code: "login-code" }), {
    statusCode: 404,
  });
  assert.deepEqual(urls, [
    "/api/v1/auth/wechat/login",
    "/api/auth/wechat/login",
    "/auth/wechat/login",
  ]);
});
