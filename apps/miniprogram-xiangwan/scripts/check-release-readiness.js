#!/usr/bin/env node
"use strict";

const { analyzeReleaseReadiness } = require("./release-contract");

const targetArgument = process.argv.find((value) => value.startsWith("--target="));
const target = targetArgument ? targetArgument.slice("--target=".length) : "release";
const result = analyzeReleaseReadiness({ target });

if (!result.ready) {
  console.error(`Xiangwan ${result.target} release blocked: ${result.issues.join(", ")}`);
  process.exitCode = 1;
} else {
  console.log(`Xiangwan ${result.target} release readiness PASS`);
}
