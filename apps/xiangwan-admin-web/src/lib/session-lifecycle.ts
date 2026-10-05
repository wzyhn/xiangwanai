export type SessionLifecycleFacts = {
  status?: string | null;
  session_end_at?: string | null;
};

const terminalStatuses = new Set(["cancelled", "ended", "archived"]);

function isSessionEnded(
  session: SessionLifecycleFacts,
  nowMs: number,
): boolean {
  const status = String(session.status || "").trim().toLowerCase();
  if (status === "ended" || status === "archived") return true;
  if (status !== "published" || !session.session_end_at) return false;
  const endMs = Date.parse(session.session_end_at);
  return Number.isFinite(endMs) && endMs <= nowMs;
}

export function isSessionTerminal(
  session: SessionLifecycleFacts,
  nowMs = Date.now(),
): boolean {
  const status = String(session.status || "").trim().toLowerCase();
  if (terminalStatuses.has(status)) return true;
  return isSessionEnded(session, nowMs);
}

export function allSessionsTerminal(
  sessions: SessionLifecycleFacts[],
  nowMs = Date.now(),
): boolean {
  return sessions.length > 0 && sessions.every((session) => isSessionTerminal(session, nowMs));
}

export function canCompleteInstance(
  sessions: SessionLifecycleFacts[],
  nowMs = Date.now(),
): boolean {
  return allSessionsTerminal(sessions, nowMs) &&
    sessions.some((session) => isSessionEnded(session, nowMs));
}

export function nextSessionLifecycleBoundaryMs(
  sessions: SessionLifecycleFacts[],
  nowMs = Date.now(),
): number | null {
  let nextBoundary: number | null = null;
  for (const session of sessions) {
    const status = String(session.status || "").trim().toLowerCase();
    if (status !== "published" || !session.session_end_at) continue;
    const endMs = Date.parse(session.session_end_at);
    if (!Number.isFinite(endMs) || endMs <= nowMs) continue;
    if (nextBoundary === null || endMs < nextBoundary) nextBoundary = endMs;
  }
  return nextBoundary;
}
