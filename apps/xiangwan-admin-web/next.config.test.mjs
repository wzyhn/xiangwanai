import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import test from "node:test";

function load(origin) {
  const env = { ...process.env, NODE_ENV: "production" };
  delete env.XIANGWAN_ADMIN_API_ORIGIN;
  if (origin !== undefined) env.XIANGWAN_ADMIN_API_ORIGIN = origin;
  return spawnSync(process.execPath, ["--input-type=module", "-e",
    `const {default:config}=await import(${JSON.stringify(new URL("./next.config.mjs", import.meta.url).href)}); console.log(JSON.stringify(await config.rewrites()));`],
    { env, encoding: "utf8", timeout: 10000, windowsHide: true });
}
test("production requires an explicit API origin", () => {
  assert.notEqual(load(undefined).status, 0);
});
test("credentials and non-origin components cannot enter the public build", () => {
  for (const origin of ["https://operator:fake@api.example", "https://api.example/path",
    "https://api.example?key=fake", "https://api.example#fragment", "https://api.example/", "file:///tmp/api"])
    assert.notEqual(load(origin).status, 0);
});
test("exact public and internal origins produce the expected same-origin rewrites", () => {
  for (const origin of ["https://api.example", "http://127.0.0.1:8082", "http://api:8080"]) {
    const result = load(origin);
    assert.equal(result.status, 0, result.stderr);
    const rewrites = JSON.parse(result.stdout);
    assert.ok(rewrites.length >= 4);
    assert.ok(rewrites.every((rewrite) => rewrite.destination.startsWith(`${origin}/api/v1/xiangwan/`)));
  }
});
