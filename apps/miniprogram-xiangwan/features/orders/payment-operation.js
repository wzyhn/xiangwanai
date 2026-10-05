"use strict";

const { canonicalUUID } = require("../../services/xiangwan-api");

const STORAGE_PREFIX = "xiangwan:wechat-prepay:";

function storageKey(orderId) {
  return `${STORAGE_PREFIX}${canonicalUUID(orderId, "订单")}`;
}

function checkedStorage(storage) {
  if (
    !storage ||
    typeof storage.getStorageSync !== "function" ||
    typeof storage.setStorageSync !== "function" ||
    typeof storage.removeStorageSync !== "function"
  ) {
    throw new Error("payment recovery storage unavailable");
  }
  return storage;
}

function checkedRecord(record, principalId, orderId) {
  if (
    !record ||
    typeof record !== "object" ||
    Array.isArray(record) ||
    canonicalUUID(record.principal_id, "用户") !== principalId ||
    canonicalUUID(record.order_id, "订单") !== orderId ||
    !Number.isSafeInteger(record.order_version) ||
    record.order_version < 1 ||
    !Number.isSafeInteger(record.payable_cents) ||
    record.payable_cents < 1 ||
    canonicalUUID(record.operation_key, "支付操作")[14] !== "4"
  ) {
    throw new Error("payment recovery record invalid");
  }
  return record;
}

function readPaymentOperation(storage, principal, order) {
  const principalId = canonicalUUID(principal, "用户");
  const orderId = canonicalUUID(order, "订单");
  const value = checkedStorage(storage).getStorageSync(storageKey(orderId));
  if (value === undefined || value === null || value === "") return null;
  if (value && typeof value === "object" && value.principal_id !== principalId) return null;
  return checkedRecord(value, principalId, orderId);
}

function savePaymentOperation(storage, principal, order, orderVersion, payableCents, key) {
  const record = {
    principal_id: canonicalUUID(principal, "用户"),
    order_id: canonicalUUID(order, "订单"),
    order_version: orderVersion,
    payable_cents: payableCents,
    operation_key: canonicalUUID(key, "支付操作"),
  };
  checkedRecord(record, record.principal_id, record.order_id);
  const target = checkedStorage(storage);
  target.setStorageSync(storageKey(record.order_id), record);
  checkedRecord(
    target.getStorageSync(storageKey(record.order_id)),
    record.principal_id,
    record.order_id,
  );
  return record;
}

function clearPaymentOperation(storage, order) {
  checkedStorage(storage).removeStorageSync(storageKey(order));
}

module.exports = { readPaymentOperation, savePaymentOperation, clearPaymentOperation };
