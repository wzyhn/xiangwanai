"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const test = require("node:test");

const workflowPath = path.resolve(
  __dirname,
  "../../../.github/workflows/xiangwan-release-gate.yml",
);
const workflow = fs.readFileSync(workflowPath, "utf8");
const authorization = workflow.slice(workflow.indexOf("  authorize-upload:"));

test("trial and release upload authorization is bound to approved immutable main", () => {
  assert.match(workflow, /options:\s*\[trial, release\]/);
  assert.match(authorization, /github\.ref == 'refs\/heads\/main'/);
  assert.match(authorization, /vars\.XIANGWAN_GITHUB_AUTHORIZATION_BOUNDARY_STATUS == 'approved'/);
  assert.match(authorization, /ref: refs\/heads\/main/);
  assert.doesNotMatch(authorization, /ref:\s*\$\{\{\s*inputs\.commit\s*\}\}/);
  assert.match(authorization, /test "\$GITHUB_SHA" = "\$AUTHORIZED_COMMIT"/);
  assert.match(authorization, /test "\$\(git rev-parse HEAD\)" = "\$AUTHORIZED_COMMIT"/);
  assert.match(authorization, /git ls-remote --exit-code origin refs\/heads\/main/);
  assert.match(authorization, /test "\$remote_main_sha" = "\$AUTHORIZED_COMMIT"/);
  assert.match(
    authorization,
    /pnpm --dir apps\/miniprogram-xiangwan run check:release -- --target=/,
  );
});

test("the checked-out main source is verified before release readiness runs", () => {
  const checkoutMain = authorization.indexOf("ref: refs/heads/main");
  const bindCommit = authorization.indexOf("Bind authorization to the default main tip");
  const checker = authorization.indexOf("run check:release");
  assert.ok(checkoutMain >= 0 && checkoutMain < bindCommit && bindCommit < checker);
});
