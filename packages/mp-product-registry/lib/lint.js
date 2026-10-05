"use strict";

// product_code 命名规范 lint。
//
// 当前 weconq 矩阵存在前缀分裂的历史现实(Audit 9):
//   - community / study / record / compass → 单词裸字(legacy)
//   - wq-schedule / wq-community / wq-study → 带 wq- 前缀(矩阵命名统一)
//
// 矩阵命名统一(B5 #121 STOP follow-up,2026-05-12):前端三 app 已切 wq-
// 前缀;后端 middleware 接受 wq- 前缀并 canonicalize 回 bare(详见
// internal/modules/{community,study/schedule}/middleware_product_code.go
// productCodeAliases),旧数据列 100% 可见。
//
// 单词裸字仍允许(transitional grace period)— 已落进数据列、seeder、SQL
// filter literal,强行 sed 会断 prod。lint 行为:
//   - wq-<lowercase>:✅ 推荐(新 canonical)
//   - 单词裸字 in legacy allowlist:console.warn,不 throw(grace,不阻断启动)
//   - 其他形态:console.error,不 throw(报告但放行)
//   - 空 / 不规则:console.error,不 throw
//
// "不 throw" 是 grace period 的关键约束:启动期暴露问题但不 break 已发布版本;
// 配合 ADR-006 cutover 时再升级为 throw / 后端 canonical 翻转。
//
// 启动期调用:在 app.js onLaunch 注册 productCode 后立即 lintProductCode(code)。

const KNOWN_LEGACY_BARE = Object.freeze({
  community: true,
  study: true,
  record: true,
  compass: true,
});

const WQ_PATTERN = /^wq-[a-z][a-z0-9-]*$/;
const BARE_PATTERN = /^[a-z][a-z0-9-]*$/;

function lintProductCode(productCode, options = {}) {
  const logger = options.logger || (typeof console !== "undefined" ? console : null);
  const code = String(productCode || "").trim();
  if (!code) {
    if (logger && logger.error) {
      logger.error("[mp-product-registry] product_code is empty");
    }
    return { ok: false, level: "error", reason: "empty" };
  }
  if (WQ_PATTERN.test(code)) {
    return { ok: true, level: "ok", reason: "wq-prefixed" };
  }
  if (KNOWN_LEGACY_BARE[code]) {
    if (logger && logger.warn) {
      logger.warn(
        `[mp-product-registry] product_code "${code}" is legacy bare-word; ` +
          "new apps should adopt 'wq-' prefix (e.g. wq-community). " +
          "See docs/standards/frontend-request-id.md + ADR-006.",
      );
    }
    return { ok: true, level: "warn", reason: "legacy-bare" };
  }
  if (BARE_PATTERN.test(code)) {
    if (logger && logger.error) {
      logger.error(
        `[mp-product-registry] product_code "${code}" is NOT in legacy allowlist ` +
          "and lacks 'wq-' prefix. Use 'wq-<name>' for new apps.",
      );
    }
    return { ok: false, level: "error", reason: "unknown-bare" };
  }
  if (logger && logger.error) {
    logger.error(
      `[mp-product-registry] product_code "${code}" violates naming rule ` +
        "(must match /^wq-[a-z][a-z0-9-]*$/ or legacy allowlist).",
    );
  }
  return { ok: false, level: "error", reason: "invalid-format" };
}

module.exports = {
  lintProductCode,
};
