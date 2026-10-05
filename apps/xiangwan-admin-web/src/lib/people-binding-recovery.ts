export type PeopleBindingRecovery = {
  version: 1;
  kind: "invite" | "revoke" | "invitation_revoke";
  profileId: string;
  expectedVersion: number;
  operation: string;
  reason: string;
  bindingId?: string;
};
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
export function peopleBindingRecoveryKey(profileId: string) {
  return `xiangwan:people-binding:v1:${profileId}`;
}
export function parsePeopleBindingRecovery(
  value: unknown,
  profileId: string,
): PeopleBindingRecovery {
  if (!value || typeof value !== "object" || Array.isArray(value))
    throw new Error("绑定恢复记录无法读取");
  const v = value as Record<string, unknown>;
  if (
    v.version !== 1 ||
    v.profileId !== profileId ||
    !uuid.test(profileId) ||
    (v.kind !== "invite" && v.kind !== "revoke" && v.kind !== "invitation_revoke") ||
    typeof v.operation !== "string" ||
    !uuid.test(v.operation) ||
    !Number.isSafeInteger(v.expectedVersion) ||
    Number(v.expectedVersion) < 1 ||
    typeof v.reason !== "string" ||
    !v.reason.trim() ||
    v.reason !== v.reason.trim() ||
    [...v.reason].length > 500 ||
    (v.kind !== "invite" && (typeof v.bindingId !== "string" || !uuid.test(v.bindingId))) ||
    Object.keys(v).some(
      (key) =>
        ![
          "version",
          "kind",
          "profileId",
          "expectedVersion",
          "operation",
          "reason",
          "bindingId",
        ].includes(key),
    )
  )
    throw new Error("绑定恢复记录不完整，请核对原操作");
  return v as PeopleBindingRecovery;
}
