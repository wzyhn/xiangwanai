export type CompletionRecovery = {
  version: 1;
  operation: string;
  expectedInstanceVersion: number;
};

const operationKeyPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

/**
 * Validate the small sessionStorage record used to resume an uncertain
 * instance-completion write. Unknown browser values are discarded rather than
 * replayed with a malformed operation key or stale version fence.
 */
export function parseCompletionRecovery(value: unknown): CompletionRecovery | null {
  if (typeof value !== "object" || value === null || Array.isArray(value)) return null;
  const candidate = value as Record<string, unknown>;
  if (candidate.version !== 1 || typeof candidate.operation !== "string" ||
    !operationKeyPattern.test(candidate.operation) ||
    typeof candidate.expectedInstanceVersion !== "number" ||
    !Number.isSafeInteger(candidate.expectedInstanceVersion) ||
    candidate.expectedInstanceVersion < 1) return null;
  return {
    version: 1,
    operation: candidate.operation,
    expectedInstanceVersion: candidate.expectedInstanceVersion,
  };
}

export function completionRecoveryPrompt(hasPending: boolean): string {
  return hasPending
    ? "上次结束请求可能未确认完成，将使用同一操作键重试。\n确认继续标记为已结束？"
    : "确认将这一期标记为已结束？结束后将出现在小程序「往期活动」中。";
}
