import assert from "node:assert/strict";
import test from "node:test";
import { parseCorrectionAction } from "./coupon-correction-recovery.ts";

const start = {
  entry_id: "11111111-1111-4111-8111-111111111111",
  operation_id: "22222222-2222-4222-8222-222222222222",
  body: { action: "start", expected_version: 0, operator_note: "核对工单 case-1" },
};
const financial = { ...start, body: {
  action: "resolve", expected_version: 1, operator_note: "核对最终记录",
  evidence_kind: "financial", evidence_reference: "finance-case-1", adjustment_cents: "9007199254740993",
} };

test("preserves the exact start and resolution requests, including money above Number precision", () => {
  for (const command of [start, financial, { ...financial, body: { ...financial.body,
    evidence_kind: "entitlement", adjustment_cents: "0" } }]) {
    assert.deepEqual(parseCorrectionAction(JSON.stringify(command)), command);
  }
});

test("rejects mixed actions, invalid versions, money and untrusted injected fields", () => {
  const invalid = [null, "{", "x".repeat(8193), ...[
    { ...start, operation_id: "22222222-2222-1222-8222-222222222222" },
    { ...start, actor_principal_id: start.entry_id },
    { ...start, body: { ...start.body, expected_version: 1 } },
    { ...start, body: { ...start.body, evidence_reference: "unexpected" } },
    { ...start, body: { ...start.body, operator_note: " " } },
    { ...start, body: { ...start.body, operator_note: "note\nprivate" } },
    { ...financial, body: { ...financial.body, adjustment_cents: "1.5" } },
    { ...financial, body: { ...financial.body, adjustment_cents: "01" } },
    { ...financial, body: { ...financial.body, adjustment_cents: "9223372036854775808" } },
    { ...financial, body: { ...financial.body, adjustment_cents: 5000 } },
    { ...financial, body: { ...financial.body, evidence_kind: "entitlement" } },
    { ...financial, body: { ...financial.body, evidence_reference: "a".repeat(129) } },
  ].map((command) => JSON.stringify(command))];
  for (const raw of invalid) assert.equal(parseCorrectionAction(raw), null, raw ?? "null");
});
