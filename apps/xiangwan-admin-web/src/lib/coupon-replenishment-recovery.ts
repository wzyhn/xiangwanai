export type PendingCouponReplenishment = {
  source_coupon_id: string;
  operation_id: string;
  reason: string;
  context: string;
};

const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export function parsePendingCouponReplenishment(raw: string | null): PendingCouponReplenishment | null {
  if (!raw || raw.length > 2048) return null;
  try {
    const value: unknown = JSON.parse(raw);
    if (!value || typeof value !== "object") return null;
    const draft = value as Record<string, unknown>;
    if (typeof draft.source_coupon_id !== "string" || !uuidPattern.test(draft.source_coupon_id) ||
        typeof draft.operation_id !== "string" || !uuidPattern.test(draft.operation_id) ||
        typeof draft.reason !== "string" || draft.reason.trim().length === 0 || draft.reason.length > 500 ||
        typeof draft.context !== "string" || draft.context.trim().length === 0 || draft.context.length > 128) {
      return null;
    }
    return {
      source_coupon_id: draft.source_coupon_id,
      operation_id: draft.operation_id,
      reason: draft.reason,
      context: draft.context,
    };
  } catch {
    return null;
  }
}
