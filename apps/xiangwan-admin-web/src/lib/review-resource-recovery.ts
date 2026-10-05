export type ReviewResourceRecovery = {
  version: 2;
  operation: string;
  body: string;
};

const operationPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

// Unknown network outcomes must replay the exact serialized request. Rebuilding
// it from the visible form after a refresh could silently change File IDs,
// target version or ordering while retaining the old operation key.
export function parseReviewResourceRecovery(value: unknown): ReviewResourceRecovery | null {
  if (typeof value !== "object" || value === null || Array.isArray(value)) return null;
  const candidate = value as Record<string, unknown>;
  if (candidate.version !== 2 || typeof candidate.operation !== "string" ||
    !operationPattern.test(candidate.operation) || typeof candidate.body !== "string" ||
    candidate.body.length === 0 || candidate.body.length > 200_000) return null;
  try {
    const body: unknown = JSON.parse(candidate.body);
    if (typeof body !== "object" || body === null || Array.isArray(body)) return null;
    const request = body as Record<string, unknown>;
    if (typeof request.title !== "string" || request.title.trim() === "" ||
      typeof request.expected_target_version !== "number" ||
      !Number.isSafeInteger(request.expected_target_version) ||
      request.expected_target_version < 1 ||
      !Array.isArray(request.photos) || !Array.isArray(request.files)) return null;
  } catch {
    return null;
  }
  return {
    version: 2,
    operation: candidate.operation,
    body: candidate.body,
  };
}
