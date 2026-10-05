export type SessionCancellationPreview = {
  id: string;
  session_id: string;
  expected_session_version: number;
  cancelled_registration_count: number;
  pending_order_count: number;
  unknown_payment_count: number;
  requested_refund_cents: number;
  coupon_adjustment_count: number;
  expires_at: string;
};

export type SessionCancellationRecovery = {
  version: 1;
  sessionId: string;
  previewOperation: string;
  operation: string;
  reason: string;
  submitted: boolean;
  preview?: SessionCancellationPreview;
};

const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const key = (sessionId: string) => `xiangwan-admin:session-cancellation:${sessionId}:v1`;

export function validSessionCancellationPreview(
  value: unknown,
  sessionId: string,
): value is SessionCancellationPreview {
  if (!value || typeof value !== "object") return false;
  const candidate = value as SessionCancellationPreview;
  return (
    uuidPattern.test(candidate.id) &&
    candidate.session_id === sessionId &&
    Number.isSafeInteger(candidate.expected_session_version) &&
    candidate.expected_session_version > 0 &&
    [
      candidate.cancelled_registration_count,
      candidate.pending_order_count,
      candidate.unknown_payment_count,
      candidate.requested_refund_cents,
      candidate.coupon_adjustment_count,
    ].every((count) => Number.isSafeInteger(count) && count >= 0) &&
    typeof candidate.expires_at === "string" &&
    Number.isFinite(Date.parse(candidate.expires_at))
  );
}

export function readSessionCancellation(sessionId: string): SessionCancellationRecovery | null {
  const raw = window.sessionStorage.getItem(key(sessionId));
  if (!raw) return null;
  const value = JSON.parse(raw) as SessionCancellationRecovery;
  if (
    value.version !== 1 ||
    value.sessionId !== sessionId ||
    !uuidPattern.test(value.previewOperation) ||
    !uuidPattern.test(value.operation) ||
    value.operation === value.previewOperation ||
    typeof value.reason !== "string" ||
    value.reason.trim() !== value.reason ||
    !value.reason ||
    [...value.reason].length > 500 ||
    typeof value.submitted !== "boolean" ||
    (value.submitted && !value.preview) ||
    (value.preview && !validSessionCancellationPreview(value.preview, sessionId))
  ) {
    throw new Error("已保存的取消请求无法安全恢复，请联系管理员核对操作记录");
  }
  return value;
}

export function saveSessionCancellation(value: SessionCancellationRecovery): void {
  window.sessionStorage.setItem(key(value.sessionId), JSON.stringify(value));
}

export function clearSessionCancellation(sessionId: string): void {
  window.sessionStorage.removeItem(key(sessionId));
}
