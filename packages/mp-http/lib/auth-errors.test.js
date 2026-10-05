const test = require("node:test");
const assert = require("node:assert/strict");
const { createClient } = require("./client");

for (const failure of [
  "classifier throws",
  "recovery throws",
  "recovery rejects",
  "not auth",
  "skip",
]) {
  test(`auth ${failure} rejects the original request without escaping or dispatching again`, async (t) => {
    const previousWx = global.wx;
    let deliver;
    let requestCount = 0;
    let classifierCount = 0;
    let recoveryCount = 0;
    global.wx = {
      request(options) {
        requestCount += 1;
        // wx invokes success after the request Promise executor has returned.
        // A synchronous wx stub would accidentally catch a thrown host hook.
        deliver = options.success;
      },
    };
    t.after(() => {
      if (previousWx === undefined) delete global.wx;
      else global.wx = previousWx;
    });
    const request = createClient({
      baseUrl: "https://api.example.com",
      isAuthRequiredError() {
        classifierCount += 1;
        if (failure === "classifier throws") throw new Error("synthetic host detail");
        return failure !== "not auth";
      },
      onAuthRequired() {
        recoveryCount += 1;
        if (failure === "recovery throws") throw new Error("synthetic host detail");
        if (failure === "recovery rejects")
          return Promise.reject(new Error("synthetic host detail"));
        assert.fail("recovery must not run for a skipped or unclassified error");
      },
    });
    const outcome = request({
      url: "/command",
      method: "POST",
      skipAuthRetry: failure === "skip",
    }).then(
      () => assert.fail("authentication failed; the command must not succeed"),
      (error) => error,
    );
    const body = { code: 10002, message: "authentication required" };
    assert.doesNotThrow(() =>
      deliver({ statusCode: 401, data: body, header: { "X-Request-ID": "original-request" } }),
    );
    const error = await outcome;
    assert.equal(error.code, 10002);
    assert.equal(error.statusCode, 401);
    assert.equal(error.message, "authentication required");
    assert.equal(error.requestId, "original-request");
    assert.equal(error.body, body);
    assert.equal(requestCount, 1);
    assert.equal(classifierCount, failure === "skip" ? 0 : 1);
    assert.equal(recoveryCount, failure.startsWith("recovery") ? 1 : 0);
  });
}
