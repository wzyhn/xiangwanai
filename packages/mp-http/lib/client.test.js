const test = require("node:test");
const assert = require("node:assert/strict");

const { createClient } = require("./client");

test("createClient retries once after auth callback resolves true", async () => {
  let authHandled = 0;
  let accessToken = "";
  let requestCount = 0;

  global.wx = {
    request(options) {
      requestCount += 1;
      if (requestCount === 1) {
        options.success({
          statusCode: 401,
          data: {
            code: 10002,
            message: "missing authorization header",
          },
        });
        return;
      }

      assert.equal(options.header.Authorization, "Bearer fresh-token");
      options.success({
        statusCode: 200,
        data: {
          code: 0,
          message: "ok",
          data: {
            ok: true,
          },
        },
      });
    },
  };

  const request = createClient({
    baseUrl: "https://api.example.com",
    token: () => accessToken,
    onAuthRequired() {
      authHandled += 1;
      accessToken = "fresh-token";
      return true;
    },
  });

  const payload = await request({
    url: "/api/v1/study/schedules/semesters",
  });

  assert.deepEqual(payload, { ok: true });
  assert.equal(requestCount, 2);
  assert.equal(authHandled, 1);
});

test("createClient rejects when auth callback declines retry", async () => {
  global.wx = {
    request(options) {
      options.success({
        statusCode: 401,
        data: {
          code: 10002,
          message: "auth required",
        },
      });
    },
  };

  const request = createClient({
    baseUrl: "https://api.example.com",
    token: "",
    onAuthRequired() {
      return false;
    },
  });

  await assert.rejects(
    request({
      url: "/api/v1/study/schedules/semesters",
    }),
    (error) => {
      assert.equal(error.statusCode, 401);
      assert.equal(error.code, 10002);
      return true;
    },
  );
});

test("createClient exposes Retry-After on HTTP failures", async () => {
  global.wx = {
    request(options) {
      options.success({
        statusCode: 429,
        header: { "Retry-After": "900" },
        data: { code: 42900, message: "rate limit exceeded" },
      });
    },
  };

  const request = createClient({ baseUrl: "https://api.example.com" });
  await assert.rejects(request({ url: "/demo/reset" }), (error) => {
    assert.equal(error.statusCode, 429);
    assert.equal(error.retryAfterSeconds, 900);
    assert.equal(error.headers["Retry-After"], "900");
    return true;
  });
});

test("createClient omits Content-Type for bodyless commands", async () => {
  const headers = [];
  global.wx = {
    request(options) {
      headers.push(options.header);
      options.success({
        statusCode: 200,
        data: { code: 0, message: "ok", data: {} },
      });
    },
  };

  const request = createClient({ baseUrl: "https://api.example.com" });
  await request({ url: "/orders/order-id/payment-queries", method: "POST" });
  await request({
    url: "/orders/order-id/wechat-prepay-attempts",
    method: "POST",
    data: { order_version: 1, payable_cents: 100 },
  });

  assert.equal(headers[0]["Content-Type"], undefined);
  assert.equal(headers[1]["Content-Type"], "application/json");
});
