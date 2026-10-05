"use strict";

// 微信小程序 runtime 无 crypto.randomUUID;用 Date.now() + Math.random() 拼一个
// RFC4122 v4 形态的 22-char 短 id 已足够 trace 关联。前端不签名,长度优先于密码学强度。
// 契约见 docs/standards/frontend-request-id.md。
function generateRequestId() {
  const ts = Date.now().toString(36);
  let rand = "";
  for (let i = 0; i < 4; i++) {
    rand += Math.random().toString(36).slice(2, 6);
  }
  return `fe-${ts}-${rand.slice(0, 16)}`;
}

// Idempotency-Key is a command identity, not an authentication secret. WeChat
// runtimes do not consistently expose crypto.randomUUID, so use the same local
// entropy source as request IDs while emitting the canonical RFC 4122 v4 shape
// required by backend command ledgers.
function generateIdempotencyKey() {
  const chars = new Array(32);
  for (let index = 0; index < chars.length; index += 1) {
    chars[index] = Math.floor(Math.random() * 16).toString(16);
  }
  chars[12] = "4";
  chars[16] = (8 + Math.floor(Math.random() * 4)).toString(16);
  return (
    chars.slice(0, 8).join("") +
    "-" +
    chars.slice(8, 12).join("") +
    "-" +
    chars.slice(12, 16).join("") +
    "-" +
    chars.slice(16, 20).join("") +
    "-" +
    chars.slice(20).join("")
  );
}

module.exports = {
  generateRequestId,
  generateIdempotencyKey,
};
