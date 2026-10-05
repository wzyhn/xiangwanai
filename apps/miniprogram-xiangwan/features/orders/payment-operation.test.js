"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const {
  readPaymentOperation,
  savePaymentOperation,
  clearPaymentOperation,
} = require("./payment-operation");

const PRINCIPAL = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa";
const OTHER_PRINCIPAL = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb";
const ORDER = "77777777-7777-4777-8777-777777777777";
const OPERATION = "33333333-3333-4333-8333-333333333333";

function storage() {
  const values = new Map();
  return {
    values,
    getStorageSync(key) {
      return values.get(key) || "";
    },
    setStorageSync(key, value) {
      values.set(key, value);
    },
    removeStorageSync(key) {
      values.delete(key);
    },
  };
}

test("payment operation survives reopening and remains scoped to one principal and Order", () => {
  const device = storage();
  assert.equal(readPaymentOperation(device, PRINCIPAL, ORDER), null);
  const record = savePaymentOperation(device, PRINCIPAL, ORDER, 3, 8800, OPERATION);
  assert.deepEqual(readPaymentOperation(device, PRINCIPAL, ORDER), record);
  assert.equal(readPaymentOperation(device, OTHER_PRINCIPAL, ORDER), null);
  clearPaymentOperation(device, ORDER);
  assert.equal(readPaymentOperation(device, PRINCIPAL, ORDER), null);
});

test("payment cannot start when recovery storage is unavailable or corrupted", () => {
  assert.throws(() => savePaymentOperation({}, PRINCIPAL, ORDER, 3, 8800, OPERATION));
  const device = storage();
  assert.throws(() =>
    savePaymentOperation(device, PRINCIPAL, ORDER, 3, 8800, "33333333-3333-3333-8333-333333333333"),
  );
  device.setStorageSync(`xiangwan:wechat-prepay:${ORDER}`, { principal_id: PRINCIPAL });
  assert.throws(() => readPaymentOperation(device, PRINCIPAL, ORDER));
});
