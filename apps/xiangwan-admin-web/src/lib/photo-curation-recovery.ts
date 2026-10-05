export type PendingPhotoCuration = {
  instanceId: string;
  relationId: string;
  expectedVersion: number;
  orderedBlockIDs: string[];
  operationKey: string;
  // Omission preserves the exact payload of retries saved by older clients.
  coverBlockID?: string;
};

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
const uuidV4 = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

export function photoCurationRecoveryKey(instanceId: string, relationId: string): string {
  return `xiangwan-admin-photo-curation:${instanceId}:${relationId}`;
}

export function parsePendingPhotoCuration(
  value: unknown,
  instanceId: string,
  relationId: string,
): PendingPhotoCuration | null {
  if (!value || typeof value !== "object" || !uuid.test(instanceId) || !uuid.test(relationId))
    return null;
  const candidate = value as Record<string, unknown>;
  if (
    candidate.instanceId !== instanceId ||
    candidate.relationId !== relationId ||
    typeof candidate.expectedVersion !== "number" ||
    !Number.isSafeInteger(candidate.expectedVersion) ||
    candidate.expectedVersion < 0 ||
    typeof candidate.operationKey !== "string" ||
    !uuidV4.test(candidate.operationKey) ||
    !Array.isArray(candidate.orderedBlockIDs) ||
    candidate.orderedBlockIDs.length > 30 ||
    !candidate.orderedBlockIDs.every((id) => typeof id === "string" && uuid.test(id)) ||
    new Set(candidate.orderedBlockIDs).size !== candidate.orderedBlockIDs.length ||
    (candidate.coverBlockID !== undefined &&
      (typeof candidate.coverBlockID !== "string" ||
        (candidate.coverBlockID !== "" &&
          (!uuid.test(candidate.coverBlockID) ||
            !candidate.orderedBlockIDs.includes(candidate.coverBlockID)))))
  )
    return null;
  return {
    instanceId,
    relationId,
    expectedVersion: candidate.expectedVersion,
    orderedBlockIDs: [...candidate.orderedBlockIDs],
    operationKey: candidate.operationKey,
    ...(candidate.coverBlockID !== undefined
      ? { coverBlockID: candidate.coverBlockID as string }
      : {}),
  };
}

export function readPendingPhotoCuration(
  instanceId: string,
  relationId: string,
): PendingPhotoCuration | null {
  if (typeof window === "undefined") return null;
  const key = photoCurationRecoveryKey(instanceId, relationId);
  try {
    const raw = window.sessionStorage.getItem(key);
    if (!raw) return null;
    const pending = parsePendingPhotoCuration(JSON.parse(raw), instanceId, relationId);
    if (!pending) window.sessionStorage.removeItem(key);
    return pending;
  } catch {
    try {
      window.sessionStorage.removeItem(key);
    } catch {
      /* storage unavailable */
    }
    return null;
  }
}

export function savePendingPhotoCuration(pending: PendingPhotoCuration): void {
  const safe = parsePendingPhotoCuration(pending, pending.instanceId, pending.relationId);
  if (!safe) throw new Error("invalid photo curation recovery state");
  window.sessionStorage.setItem(
    photoCurationRecoveryKey(safe.instanceId, safe.relationId),
    JSON.stringify(safe),
  );
}

export function clearPendingPhotoCuration(instanceId: string, relationId: string): void {
  window.sessionStorage.removeItem(photoCurationRecoveryKey(instanceId, relationId));
}
