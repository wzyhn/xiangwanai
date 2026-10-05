export type HostPending = { owner: string; operation: string; path: string; body: string };
export const hostPendingKey = "xiangwan:host-operation:v1";
// Only operator intent is retained. Applicant contact and narratives have no
// representation in this recovery format.
export function parseHostPending(raw: string | null, owner: string): HostPending | null {
  if (!raw || raw.length > 20000) return null;
  try {
    const v = JSON.parse(raw) as HostPending;
    if (
      Object.keys(v).sort().join() !== "body,operation,owner,path" ||
      v.owner !== owner ||
      !/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(v.operation)
    )
      return null;
    const b = JSON.parse(v.body);
    if (!Number.isSafeInteger(b.expected_version) || b.expected_version < 0) return null;
    if (v.path === "/host-rules") {
      if (
        Object.keys(b).sort().join() !==
          "application_cycle,benefits,enabled,expected_version,policy_version,reason,requirements" ||
        typeof b.enabled !== "boolean" ||
        !["application_cycle", "policy_version"].every(
          (k) => typeof b[k] === "string" && /^[A-Za-z0-9][A-Za-z0-9._:-]{0,99}$/.test(b[k]),
        ) ||
        !["requirements", "benefits"].every(
          (k) => typeof b[k] === "string" && b[k].trim().length > 0 && b[k].length <= 8000,
        ) ||
        typeof b.reason !== "string" ||
        !b.reason.trim() ||
        b.reason.length > 1000
      )
        return null;
    } else if (/^\/host-applications\/[0-9a-f-]{36}\/reviews$/.test(v.path)) {
      if (
        Object.keys(b).sort().join() !== "comment,decision,expected_version" ||
        b.expected_version < 1 ||
        !["approved", "rejected"].includes(b.decision) ||
        typeof b.comment !== "string" ||
        !b.comment.trim() ||
        b.comment.length > 4000
      )
        return null;
    } else return null;
    return v;
  } catch {
    return null;
  }
}
