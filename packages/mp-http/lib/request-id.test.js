"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");

const { generateIdempotencyKey, generateRequestId } = require("./request-id");

test("request IDs remain compact trace identifiers", () => {
  assert.match(generateRequestId(), /^fe-[a-z0-9]+-[a-z0-9]{16}$/);
});

test("idempotency keys are unique canonical UUID v4 values", () => {
  const keys = new Set();
  for (let index = 0; index < 128; index += 1) {
    const key = generateIdempotencyKey();
    assert.match(key, /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
    keys.add(key);
  }
  assert.equal(keys.size, 128);
});
