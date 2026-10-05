const test = require("node:test");
const assert = require("node:assert/strict");
const { createClient } = require("./client");

function installWx(t, request) {
  const previous = global.wx;
  global.wx = { request };
  t.after(() => {
    if (previous === undefined) delete global.wx;
    else global.wx = previous;
  });
}

test("anonymous requests do not read a session or carry Authorization in any case", async (t) => {
  let sent;
  let tokenReads = 0;
  installWx(t, (options) => {
    sent = options;
    options.success({ statusCode: 200, data: { code: 0, data: { public: true } } });
  });
  const request = createClient({
    baseUrl: "https://api.example.test",
    token() {
      tokenReads += 1;
      return "fake-stored-token";
    },
  });
  const header = {
    Authorization: "Bearer fake-upper",
    authorization: "Bearer fake-lower",
    AUTHORIZATION: "Bearer fake-all-caps",
    "X-Request-ID": "public-fixture",
  };
  const original = { ...header };
  assert.deepEqual(await request({ url: "/public", authMode: "none", header }), { public: true });
  assert.equal(tokenReads, 0);
  assert.deepEqual(
    Object.keys(sent.header).filter((key) => key.toLowerCase() === "authorization"),
    [],
  );
  assert.equal(sent.header["X-Request-ID"], "public-fixture");
  assert.deepEqual(header, original);
});

for (const authMode of ["optional", "none"]) {
  for (const statusCode of [200, 401]) {
    test(`${authMode} auth error (${statusCode}) ends once without either host auth hook`, async (t) => {
      let sent;
      let calls = 0;
      let classified = 0;
      let recovered = 0;
      installWx(t, (options) => {
        calls += 1;
        sent = options;
        options.success({
          statusCode,
          data: { code: 10002, message: "auth required" },
          header: { "X-Request-ID": "server-fixture" },
        });
      });
      const request = createClient({
        baseUrl: "https://api.example.test",
        token: "fake-expired-token",
        isAuthRequiredError() {
          classified += 1;
          return true;
        },
        onAuthRequired() {
          recovered += 1;
          return true;
        },
      });
      await assert.rejects(
        request({
          url: "/optional-command",
          method: "POST",
          authMode,
          onAuthRequired() {
            recovered += 1;
            return true;
          },
        }),
        { code: 10002, statusCode, requestId: "server-fixture" },
      );
      assert.equal(calls, 1);
      assert.equal(classified, 0);
      assert.equal(recovered, 0);
      assert.equal(
        sent.header.Authorization,
        authMode === "optional" ? "Bearer fake-expired-token" : undefined,
      );
    });
  }
}

test("explicit token selects one credential without reading or mutating the global session", async (t) => {
  let sent;
  installWx(t, (options) => {
    sent = options;
    options.success({ statusCode: 200, data: { code: 0 } });
  });
  const request = createClient({
    baseUrl: "https://api.example.test",
    token() {
      assert.fail("explicit token must not read the global session");
    },
  });
  const header = { authorization: "Bearer fake-other-owner" };
  await request({
    url: "/session-probe",
    authMode: "optional",
    token: "fake-request-token",
    header,
  });
  assert.equal(sent.header.Authorization, "Bearer fake-request-token");
  assert.deepEqual(
    Object.keys(sent.header).filter((key) => key.toLowerCase() === "authorization"),
    ["Authorization"],
  );
  assert.deepEqual(header, { authorization: "Bearer fake-other-owner" });
});

test("invalid auth modes fail before reading credentials or dispatching", async (t) => {
  let calls = 0;
  installWx(t, () => {
    calls += 1;
  });
  const request = createClient({
    baseUrl: "https://api.example.test",
    token() {
      assert.fail("invalid mode must not read credentials");
    },
  });
  for (const authMode of ["public", "", null, false, {}]) {
    await assert.rejects(request({ url: "/command", authMode }), { code: 10001 });
  }
  assert.equal(calls, 0);
});

for (const authMode of [undefined, "required"]) {
  test(`required auth (${authMode}) retains one recovery and the command key`, async (t) => {
    let token = "fake-old-token";
    let calls = 0;
    let recovered = 0;
    const operation = "11111111-1111-4111-8111-111111111111";
    installWx(t, (options) => {
      calls += 1;
      assert.equal(options.header["Idempotency-Key"], operation);
      assert.equal(options.header.Authorization, `Bearer ${token}`);
      options.success(
        calls === 1
          ? { statusCode: 401, data: { code: 10002 } }
          : { statusCode: 200, data: { code: 0, data: { completed: true } } },
      );
    });
    const request = createClient({
      baseUrl: "https://api.example.test",
      token: () => token,
      onAuthRequired() {
        recovered += 1;
        token = "fake-new-token";
        return true;
      },
    });
    assert.deepEqual(
      await request({
        url: "/command",
        method: "POST",
        authMode,
        header: { "Idempotency-Key": operation },
      }),
      { completed: true },
    );
    assert.equal(calls, 2);
    assert.equal(recovered, 1);
  });
}
