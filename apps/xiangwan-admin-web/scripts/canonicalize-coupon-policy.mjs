import { readFileSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";

const fields = [
  "tenant_id", "policy_version", "enabled", "face_value_cents",
  "validity_seconds", "scope_type", "scope_activity_type",
  "scope_series_id", "minimum_order_cents", "evidence_ref",
  "approved_at", "effective_at",
];

export function canonicalize(value) {
  if (!value || typeof value !== "object" || Array.isArray(value) ||
    Object.keys(value).length !== fields.length ||
    Object.keys(value).some((key) => !fields.includes(key)) ||
    fields.some((key) => !Object.hasOwn(value, key))) {
    throw new Error("policy must contain exactly the 12 signed fields");
  }
  if (!/^[A-Za-z0-9][A-Za-z0-9._:/#-]{0,511}$/.test(value.evidence_ref) ||
    !/^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$/.test(value.approved_at) ||
    !/^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$/.test(value.effective_at) ||
    !["face_value_cents", "validity_seconds", "minimum_order_cents"].every((key) =>
      value[key] === null || Number.isSafeInteger(value[key]))) {
    throw new Error("policy evidence, UTC timestamps or integer fields are invalid");
  }
  return JSON.stringify(Object.fromEntries(fields.map((key) => [key, value[key]])));
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  if (process.argv.length !== 4) {
    throw new Error("usage: node scripts/canonicalize-coupon-policy.mjs <customer-draft.json> <new-canonical.json>");
  }
  const document = JSON.parse(readFileSync(process.argv[2], "utf8"));
  writeFileSync(process.argv[3], canonicalize(document), { encoding: "utf8", flag: "wx" });
}
