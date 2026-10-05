export type CheckinRevocationRecovery = {
  version: 1;
  operation: string;
  registrationId: string;
  checkinId: string;
  expectedVersion: number;
  reason: string;
};
const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
export const checkinRevocationKey = (registrationId: string) =>
  `xiangwan-admin:checkin-revocation:${registrationId}:v1`;
export function parseCheckinRevocation(
  raw: unknown,
  registrationId: string,
): CheckinRevocationRecovery {
  const value = raw as CheckinRevocationRecovery;
  if (
    !value ||
    value.version !== 1 ||
    value.registrationId !== registrationId ||
    !uuidPattern.test(value.operation) ||
    !uuidPattern.test(value.checkinId) ||
    !Number.isSafeInteger(value.expectedVersion) ||
    value.expectedVersion < 1 ||
    typeof value.reason !== "string" ||
    value.reason.trim() !== value.reason ||
    !value.reason ||
    [...value.reason].length > 500
  )
    throw new Error("已保存的签到撤销请求无法安全恢复，请联系管理员核对审计记录");
  return {
    version: 1,
    operation: value.operation,
    registrationId: value.registrationId,
    checkinId: value.checkinId,
    expectedVersion: value.expectedVersion,
    reason: value.reason,
  };
}
