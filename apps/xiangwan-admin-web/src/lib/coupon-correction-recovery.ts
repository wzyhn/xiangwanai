export type CorrectionAction = {
  entry_id: string;
  operation_id: string;
  body: {
    action: "start" | "resolve";
    expected_version: number;
    evidence_kind?: "financial" | "entitlement";
    evidence_reference?: string;
    adjustment_cents?: string;
    operator_note: string;
  };
};

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;
const cents = /^(0|[1-9][0-9]*)$/;

function record(value: unknown): value is Record<string, unknown> {
  return !!value && typeof value === "object" && !Array.isArray(value);
}

function text(value: unknown, max: number): value is string {
  return typeof value === "string" && value === value.trim() && value.length > 0 &&
    Array.from(value).length <= max && !/\p{Cc}/u.test(value);
}

// Validate without normalizing: an unknown-result retry must preserve the original command.
export function parseCorrectionAction(raw: string | null): CorrectionAction | null {
  if (!raw || raw.length > 8192) return null;
  try {
    const value: unknown = JSON.parse(raw);
    if (!record(value) || Object.keys(value).some((key) => !["entry_id", "operation_id", "body"].includes(key)) ||
        typeof value.entry_id !== "string" || !uuid.test(value.entry_id) ||
        typeof value.operation_id !== "string" || !uuid.test(value.operation_id) || !record(value.body)) return null;
    const body = value.body;
    if (!text(body.operator_note, 500)) return null;
    if (body.action === "start") {
      if (body.expected_version !== 0 || Object.keys(body).some((key) =>
        !["action", "expected_version", "operator_note"].includes(key))) return null;
    } else if (body.action === "resolve") {
      if (body.expected_version !== 1 || !text(body.evidence_reference, 128) ||
          typeof body.adjustment_cents !== "string" || !cents.test(body.adjustment_cents) ||
          BigInt(body.adjustment_cents) > 9223372036854775807n ||
          Object.keys(body).some((key) => !["action", "expected_version", "operator_note",
            "evidence_kind", "evidence_reference", "adjustment_cents"].includes(key))) return null;
      if (body.evidence_kind === "financial") {
        if (body.adjustment_cents === "0") return null;
      } else if (body.evidence_kind === "entitlement") {
        if (body.adjustment_cents !== "0") return null;
      } else return null;
    } else return null;
    return value as CorrectionAction;
  } catch { return null; }
}
