/**
 * Add fields introduced after the first version of the activity-create
 * recovery record. Recovery data is local browser state, so this migration
 * only fills fields that were absent; malformed values remain untouched and
 * are rejected by the page validator.
 */
export function normalizeActivityCreateForm(value: unknown): unknown {
  if (typeof value !== "object" || value === null || Array.isArray(value)) return value;
  const record = value as Record<string, unknown>;
  const migrated = { ...record };
  if (migrated.questionnairePrivacyPurpose === undefined) {
    migrated.questionnairePrivacyPurpose = "用于本次活动报名与现场组织";
  }
  if (migrated.questionnairePrivacyPolicyVersion === undefined) {
    migrated.questionnairePrivacyPolicyVersion = "";
  }
  if (migrated.questionnaireFields === undefined) {
    migrated.questionnaireFields = [];
  }
  if (migrated.activitySummary === undefined) {
    migrated.activitySummary = "";
  }
  if (migrated.issueNo === undefined) {
    migrated.issueNo = "";
  }
  if (migrated.additionalSessions === undefined) {
    migrated.additionalSessions = [];
  }
  if (migrated.copySource === undefined) {
    migrated.copySource = null;
  }
  return migrated;
}

export type ActivityInstanceCheckpoint = {
  id: string;
  version: number;
};

/**
 * Reconcile a create-flow checkpoint from the administrator detail response
 * after a version conflict. The GET /instances/:id contract wraps the
 * authoritative instance under `instance`; keeping this projection in a
 * pure helper prevents the recovery path from accidentally reading a flat
 * response and retrying the same stale fence forever.
 */
export function reconcileInstanceCheckpoint(
  detail: { instance?: { id?: string; version?: number } } | null | undefined,
  current: ActivityInstanceCheckpoint,
): ActivityInstanceCheckpoint | null {
  const latest = detail?.instance;
  const version = latest?.version;
  if (!latest || typeof latest.id !== "string" || !latest.id ||
    typeof version !== "number" || !Number.isSafeInteger(version) || version < 1) {
    return null;
  }
  if (version === current.version) return null;
  return { id: latest.id, version };
}
