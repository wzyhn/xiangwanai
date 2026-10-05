const test = require("node:test");
const assert = require("node:assert/strict");
const { createWeconqAuthClient } = require("./client");
const { createDefaultClient } = require("../../mp-http");

test("injected shared transport must not switch an explicit session probe to the global owner", async (t) => {
  const previous = global.wx;
  const headers = [];
  global.wx = {
    request(options) {
      headers.push(options.header.Authorization);
      options.success({
        statusCode: 200,
        data: { code: 0, data: { id: options.header.Authorization } },
      });
    },
  };
  t.after(() => {
    if (previous === undefined) delete global.wx;
    else global.wx = previous;
  });
  const client = createWeconqAuthClient({
    request: createDefaultClient({
      baseUrl: "https://api.example.test",
      token: "fake-global-token",
      onAuthRequired: null,
      onError: false,
    }).request,
  });
  const profile = await client.getCurrentProfile({ token: "fake-request-token" });
  assert.equal(profile.principalId, "Bearer fake-request-token");
  assert.deepEqual(headers, ["Bearer fake-request-token"]);
});

for (const transport of ["native", "shared"]) {
  test(`${transport} SDK login is anonymous and session probes use the supplied token`, async (t) => {
    let tokenReads = 0;
    let authHooks = 0;
    const sent = [];
    const wxApi = {
      request(options) {
        sent.push(options);
        if (options.url.endsWith("/verify")) {
          options.success({ statusCode: 401, data: { code: 10002, message: "session expired" } });
        } else {
          const data = options.url.endsWith("/me")
            ? {
                id:
                  options.header.Authorization === "Bearer fake-request-token"
                    ? "requested-owner"
                    : "wrong-global-owner",
              }
            : { token: "fake-new-token", principal_id: "new-owner" };
          options.success({ statusCode: 200, data: { code: 0, data } });
        }
      },
    };
    const previous = global.wx;
    global.wx = wxApi;
    t.after(() => {
      if (previous === undefined) delete global.wx;
      else global.wx = previous;
    });
    const options = {
      wx: wxApi,
      baseUrl: "https://api.example.test",
      token() {
        tokenReads += 1;
        return "fake-global-token";
      },
    };
    if (transport === "shared") {
      options.request = createDefaultClient({
        ...options,
        onError: false,
        isAuthRequiredError() {
          authHooks += 1;
          return true;
        },
        onAuthRequired() {
          authHooks += 1;
          return true;
        },
      }).request;
    }
    const client = createWeconqAuthClient(options);
    for (const method of ["loginWithWeChat", "devLogin"]) {
      const session = await client[method]({
        appId: "fixture-app",
        code: "fake-login-code",
        providerId: "fixture-user",
      });
      assert.equal(session.principalId, "new-owner");
      assert.equal(sent.at(-1).header.Authorization, undefined);
    }
    const profile = await client.getCurrentProfile({ token: "fake-request-token" });
    assert.equal(profile.principalId, "requested-owner");
    await assert.rejects(
      client.verifySession({ token: "fake-request-token", appId: "fixture-app" }),
      { code: 10002, statusCode: 401 },
    );
    assert.equal(sent.at(-1).header.Authorization, "Bearer fake-request-token");
    assert.equal(tokenReads, 0);
    assert.equal(authHooks, 0);
    assert.equal(sent.length, 4);
  });
}

for (const [label, body] of [
  ["empty body", undefined],
  ["null", null],
  ["missing code", { data: { token: "fake-unacknowledged-token" } }],
  ["HTML", "<html>fake-upstream-detail</html>"],
  ["string code", { code: "0", data: { token: "fake-unacknowledged-token" } }],
  ["boolean code", { code: false }],
  ["array", []],
]) {
  test(`native auth rejects ${label} without accepting a session or probing another route`, async () => {
    let calls = 0;
    const client = createWeconqAuthClient({
      baseUrl: "https://api.example.test",
      wx: {
        request(options) {
          calls += 1;
          options.success({ statusCode: 200, data: body });
        },
      },
    });
    await assert.rejects(
      client.loginWithWeChat({ appId: "fixture-app", code: "fake-login-code" }),
      (error) => {
        assert.equal(error.code, 10006);
        assert.equal(error.statusCode, 200);
        assert.equal(error.invalidResponse, true);
        assert.equal(error.body, undefined);
        assert.equal(error.message, "invalid response envelope");
        return true;
      },
    );
    assert.equal(calls, 1);
  });
}
