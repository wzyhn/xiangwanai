import type { AuthStatus } from "@/lib/types";

function hasTenantGrant(status: AuthStatus, capability: string): boolean {
  return status.grants.some(
    (grant) =>
      grant.scope_type === "tenant" &&
      (grant.capability === "super_admin" || grant.capability === capability),
  );
}

export function canOperateActivities(status: AuthStatus): boolean {
  return hasTenantGrant(status, "activity_operator");
}

export function canReadAuditLog(status: AuthStatus): boolean {
  return status.grants.some((grant) => grant.capability === "super_admin" && grant.scope_type === "tenant");
}

export function canReadFinance(status: AuthStatus): boolean {
  return hasTenantGrant(status, "finance");
}

export function canReadOrders(status: AuthStatus): boolean {
  return canOperateActivities(status) || canReadFinance(status);
}

export function canCheckin(status: AuthStatus): boolean {
  return canOperateActivities(status) || status.grants.some(
    (grant) =>
      (grant.capability === "super_admin" && grant.scope_type === "tenant") ||
      grant.capability === "onsite_checkin",
  );
}

export function canReadRegistrations(status: AuthStatus): boolean {
  return canOperateActivities(status) || canCheckin(status);
}

export function canReadAllRegistrations(status: AuthStatus): boolean {
  return canOperateActivities(status) || hasTenantGrant(status, "onsite_checkin");
}

export function defaultAdminPath(status: AuthStatus): string {
  if (canOperateActivities(status)) return "/activities";
  if (canReadFinance(status)) return "/refunds";
  if (canCheckin(status)) return "/checkin";
  return "/login?reason=identity_rejected";
}

export function canOpenAdminPath(status: AuthStatus, path: string): boolean {
  if (path.startsWith("/audit-events")) return canReadAuditLog(status);
  if (path.startsWith("/coupon-corrections")) return canReadAuditLog(status);
  if (path.startsWith("/refunds")) return canReadFinance(status);
  if (path.startsWith("/orders")) return canReadOrders(status);
  if (path.startsWith("/host-applications")) return canOperateActivities(status);
  if (path.startsWith("/brand")) return canOperateActivities(status);
  if (path.startsWith("/activities")) return canOperateActivities(status);
  if (path.startsWith("/series")) return canOperateActivities(status);
  if (path.startsWith("/past-activities")) return canOperateActivities(status);
  if (path.startsWith("/questionnaire-templates")) return canOperateActivities(status);
  if (path.startsWith("/people")) return canOperateActivities(status);
  if (path.startsWith("/registrations")) return canReadRegistrations(status);
  if (path.startsWith("/checkin")) return canCheckin(status);
  return false;
}
