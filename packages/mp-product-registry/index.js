"use strict";
const XIANGWAN_CONFIG = Object.freeze({
  productCode: "wq-xiangwan", appId: "wx05ff9791f73fe3bd",
  appIdStatus: "customer-provided", shareEntryPath: "/pages/index/index",
});
const MINI_PROGRAM_REGISTRY = Object.freeze({ "wq-xiangwan": XIANGWAN_CONFIG });
function getMiniProgramConfig(code) { return MINI_PROGRAM_REGISTRY[String(code || "").trim()]; }
function listMiniProgramConfigs() { return [XIANGWAN_CONFIG]; }
function lintProductCode(code) { return code === "wq-xiangwan"; }
module.exports = { DEFAULT_SHARE_ENTRY_PATH: "/pages/index/index", MINI_PROGRAM_REGISTRY,
  getMiniProgramConfig, listMiniProgramConfigs, lintProductCode };
