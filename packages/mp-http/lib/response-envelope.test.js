const test = require("node:test");
const assert = require("node:assert/strict");
const { createClient } = require("./client");

function respond(t, data, statusCode = 200) {
  const previousWx = global.wx;
  const calls = [];
  global.wx = {
    request(options) {
      calls.push(options);
      options.success({
        statusCode,
        data,
        header: { "x-request-id": "server-trace", "Retry-After": "7" },
      });
    },
  };
  t.after(() => {
    if (previousWx === undefined) delete global.wx;
    else global.wx = previousWx;
  });
  return calls;
}

const malformedBodies = [
  ["absent body", undefined],
  ["null body", null],
  ["empty body", ""],
  ["HTML from a gateway", "<html>gateway diagnostic</html>"],
  ["array body", []],
  ["missing code", { message: "gateway diagnostic", data: { saved: true } }],
  ["null code", { code: null }],
  ["boolean code", { code: false }],
  ["string code", { code: "0" }],
  ["array code", { code: [] }],
  ["fractional code", { code: 0.5 }],
  ["nonfinite code", { code: Infinity }],
];

for (const [label, body] of malformedBodies) {
  test(`HTTP 200 with ${label} is not a successful command`, async (t) => {
    const calls = respond(t, body);
    const metadata = [];
    const request = createClient({
      baseUrl: "https://api.example.com",
      onResponseMeta: (meta) => metadata.push(meta),
      onAuthRequired() {
        assert.fail("a malformed response must not start a login or command retry");
      },
    });
    await assert.rejects(request({ url: "/command", method: "POST" }), (error) => {
      assert.equal(error.statusCode, 200, "retain the actual transport status");
      assert.equal(error.code, 10006);
      assert.equal(error.invalidResponse, true);
      assert.equal(error.requestId, "server-trace");
      assert.equal(error.retryAfterSeconds, 7);
      assert.equal(error.message, "invalid response envelope");
      assert.equal(error.body, undefined, "do not expose an unexpected response body");
      assert.equal(error.data, undefined);
      return true;
    });
    assert.equal(calls.length, 1);
    assert.equal(metadata.length, 1, "response-header hooks still observe the response");
  });
}

test("numeric zero accepts valid payloads and the backend's omitted data field", async (t) => {
  for (const payload of [{ saved: true }, [1], "", false, 0, null, undefined]) {
    await t.test(`payload ${JSON.stringify(payload)}`, async (subtest) => {
      const body = { code: 0, message: "ok" };
      if (payload !== undefined) body.data = payload;
      respond(subtest, body, 201);
      const request = createClient({ baseUrl: "https://api.example.com" });
      assert.deepEqual(await request({ url: "/command", method: "POST" }), payload);
    });
  }
});

test("a real application error retains its code and conflict details", async (t) => {
  const body = { code: 10005, message: "conflict", data: { current_revision: 2 } };
  respond(t, body);
  const request = createClient({ baseUrl: "https://api.example.com" });
  await assert.rejects(request({ url: "/command" }), (error) => {
    assert.equal(error.code, 10005);
    assert.deepEqual(error.body, body);
    assert.equal(error.invalidResponse, false);
    return true;
  });
});

test("HTTP failure cannot become success merely by carrying code zero", async (t) => {
  respond(t, { code: 0, data: { saved: true } }, 503);
  const request = createClient({ baseUrl: "https://api.example.com" });
  await assert.rejects(request({ url: "/command" }), { statusCode: 503 });
});

test("HTTP 401 without an envelope still allows only one explicit auth recovery", async (t) => {
  const calls = respond(t, "unauthorized gateway", 401);
  let recoveryCount = 0;
  const request = createClient({
    baseUrl: "https://api.example.com",
    onAuthRequired() {
      recoveryCount += 1;
      return true;
    },
  });
  await assert.rejects(request({ url: "/command" }), { statusCode: 401 });
  assert.equal(calls.length, 2);
  assert.equal(recoveryCount, 1);
  await assert.rejects(request({ url: "/public", skipAuthRetry: true }), { statusCode: 401 });
  assert.equal(calls.length, 3);
  assert.equal(recoveryCount, 1);
});
