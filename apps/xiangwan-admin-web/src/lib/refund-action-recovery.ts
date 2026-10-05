export type RefundAction = "start" | "fail" | "complete" | "reject";

export type PendingRefundAction = {
  caseId: string;
  action: RefundAction;
  expectedVersion: number;
  operationKey: string;
};

const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
const operationPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

export function refundActionRecoveryKey(caseId: string): string {
  return `xiangwan-admin-refund-action:${caseId}`;
}

export function parsePendingRefundAction(value: unknown, caseId: string): PendingRefundAction | null {
  if (!value || typeof value !== "object" || !uuidPattern.test(caseId)) return null;
  const candidate = value as Record<string, unknown>;
  if (candidate.caseId !== caseId ||
    !["start", "fail", "complete", "reject"].includes(String(candidate.action)) ||
    typeof candidate.expectedVersion !== "number" ||
    !Number.isSafeInteger(candidate.expectedVersion) || candidate.expectedVersion < 1 ||
    typeof candidate.operationKey !== "string" ||
    !operationPattern.test(candidate.operationKey)) return null;
  return {
    caseId,
    action: candidate.action as RefundAction,
    expectedVersion: candidate.expectedVersion,
    operationKey: candidate.operationKey,
  };
}

export function readPendingRefundAction(caseId: string): PendingRefundAction | null {
  if (typeof window === "undefined") return null;
  const key = refundActionRecoveryKey(caseId);
  try {
    const raw = window.sessionStorage.getItem(key);
    if (!raw) return null;
    const pending = parsePendingRefundAction(JSON.parse(raw), caseId);
    if (!pending) window.sessionStorage.removeItem(key);
    return pending;
  } catch {
    window.sessionStorage.removeItem(key);
    return null;
  }
}

export function savePendingRefundAction(pending: PendingRefundAction): void {
  const safe = parsePendingRefundAction(pending, pending.caseId);
  if (!safe) throw new Error("invalid refund action recovery state");
  window.sessionStorage.setItem(refundActionRecoveryKey(safe.caseId), JSON.stringify(safe));
}

export function clearPendingRefundAction(caseId: string): void {
  window.sessionStorage.removeItem(refundActionRecoveryKey(caseId));
}
