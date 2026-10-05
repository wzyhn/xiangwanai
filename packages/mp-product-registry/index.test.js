const test = require("node:test"), assert = require("node:assert/strict");
const registry = require("./index");
test("standalone registry contains only Xiangwan", () => {
  assert.deepEqual(Object.keys(registry.MINI_PROGRAM_REGISTRY), ["wq-xiangwan"]);
  assert.equal(registry.listMiniProgramConfigs().length, 1);
  assert.equal(registry.getMiniProgramConfig("wq-xiangwan").appId, "wx05ff9791f73fe3bd");
  assert.equal(registry.getMiniProgramConfig("other-product"), null);
  assert.equal(registry.getMiniProgramConfig("__proto__"), null);
  assert.equal(registry.getMiniProgramConfig(" WQ-XIANGWAN ").appId, "wx05ff9791f73fe3bd");
  assert.equal(registry.lintProductCode("wq-xiangwan").ok, true);
});
