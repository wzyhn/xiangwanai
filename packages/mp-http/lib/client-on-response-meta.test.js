"use strict";

// Tests for the additive onResponseMeta hook in createClient.
//
// Additive proof:
//   - All tests that pass onResponseMeta verify the hook fires correctly.
//   - Tests that omit onResponseMeta verify zero behavior change vs. original.

const test = require("node:test");
const assert = require("node:assert/strict");

const { createClient } = require("./client");

// ---------------------------------------------------------------------------
// Helper: build a wx.request stub that returns a canned HTTP response.
// ---------------------------------------------------------------------------
function makeWx(statusCode, body, headers) {
  return {
    request(options) {
      options.success({
        statusCode,
        header: headers || {},
        data: body,
      });
    },
  };
}

// ---------------------------------------------------------------------------
// 1. Hook fires on successful (2xx, code=0) response
// ---------------------------------------------------------------------------
test("onResponseMeta fires on 200 success response", async () => {
  const captured = [];
  global.wx = makeWx(200, { code: 0, data: { ok: true } }, { "X-Emergency-Flag": "active" });

  const request = createClient({
    baseUrl: "https://api.example.com",
    token: "tok",
    onResponseMeta(meta) {
      captured.push(meta);
    },
  });

  await request({ url: "/api/v1/compass/emergency/state" });

  assert.equal(captured.length, 1);
  assert.equal(captured[0].statusCode, 200);
  assert.equal(captured[0].headers["X-Emergency-Flag"], "active");
});

// ---------------------------------------------------------------------------
// 2. Hook fires on application-level error (2xx HTTP but body.code != 0)
// ---------------------------------------------------------------------------
test("onResponseMeta fires on app-level error response (code=10002)", async () => {
  const captured = [];
  global.wx = makeWx(200, { code: 10002, message: "auth required" }, { "x-request-id": "req-abc" });

  const request = createClient({
    baseUrl: "https://api.example.com",
    token: "",
    onResponseMeta(meta) {
      captured.push(meta);
    },
  });

  await assert.rejects(request({ url: "/api/v1/compass/pois" }));

  assert.equal(captured.length, 1);
  assert.equal(captured[0].statusCode, 200);
});

// ---------------------------------------------------------------------------
// 3. Hook fires on HTTP 4xx error status
// ---------------------------------------------------------------------------
test("onResponseMeta fires on 404 HTTP error", async () => {
  const captured = [];
  global.wx = makeWx(404, { code: 10004, message: "not found" }, {});

  const request = createClient({
    baseUrl: "https://api.example.com",
    token: "tok",
    onResponseMeta(meta) {
      captured.push(meta);
    },
  });

  await assert.rejects(request({ url: "/api/v1/compass/pois/missing-id" }));

  assert.equal(captured.length, 1);
  assert.equal(captured[0].statusCode, 404);
});

// ---------------------------------------------------------------------------
// 4. Hook NOT called on network-level fail() (no HTTP response available)
// ---------------------------------------------------------------------------
test("onResponseMeta NOT called when wx.request fail() fires (network error)", async () => {
  const captured = [];
  global.wx = {
    request(options) {
      options.fail({ errMsg: "request:fail timeout" });
    },
  };

  const request = createClient({
    baseUrl: "https://api.example.com",
    token: "tok",
    onResponseMeta(meta) {
      captured.push(meta);
    },
  });

  await assert.rejects(request({ url: "/api/v1/compass/pois" }));
  assert.equal(captured.length, 0, "hook must not fire on network fail");
});

// ---------------------------------------------------------------------------
// 5. Omitting onResponseMeta: existing behavior unchanged (no error, correct resolve)
// ---------------------------------------------------------------------------
test("omitting onResponseMeta does not change request resolve behavior", async () => {
  global.wx = makeWx(200, { code: 0, data: { items: [1, 2, 3] } }, {});

  const request = createClient({
    baseUrl: "https://api.example.com",
    token: "tok",
    // no onResponseMeta
  });

  const result = await request({ url: "/api/v1/compass/pois" });
  assert.deepEqual(result, { items: [1, 2, 3] });
});

// ---------------------------------------------------------------------------
// 6. Buggy hook (throws) must not break the request pipeline
// ---------------------------------------------------------------------------
test("onResponseMeta that throws does not prevent successful resolve", async () => {
  global.wx = makeWx(200, { code: 0, data: { id: "poi-1" } }, {});

  const request = createClient({
    baseUrl: "https://api.example.com",
    token: "tok",
    onResponseMeta() {
      throw new Error("hook bug");
    },
  });

  const result = await request({ url: "/api/v1/compass/pois/poi-1" });
  assert.deepEqual(result, { id: "poi-1" });
});

// ---------------------------------------------------------------------------
// 7. Hook receives headers object (even if backend sends empty headers)
// ---------------------------------------------------------------------------
test("onResponseMeta receives headers as object (empty headers case)", async () => {
  const captured = [];
  // wx.request success with no header key at all
  global.wx = {
    request(options) {
      options.success({ statusCode: 200, data: { code: 0, data: {} } });
    },
  };

  const request = createClient({
    baseUrl: "https://api.example.com",
    token: "tok",
    onResponseMeta(meta) {
      captured.push(meta);
    },
  });

  await request({ url: "/api/v1/compass/pois" });
  assert.equal(captured.length, 1);
  assert.ok(
    typeof captured[0].headers === "object" && captured[0].headers !== null,
    "headers must be an object",
  );
});
