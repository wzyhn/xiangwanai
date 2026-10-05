import test from "node:test";
import assert from "node:assert/strict";
import {
  normalizeActivityCreateForm,
  reconcileInstanceCheckpoint,
} from "./activity-create-recovery.ts";

test("activity-create recovery fills fields absent from legacy records", () => {
  const legacy = { instanceTitle: "第 1 期", activityType: "course" };
  assert.deepEqual(normalizeActivityCreateForm(legacy), {
    ...legacy,
    questionnairePrivacyPurpose: "用于本次活动报名与现场组织",
    questionnairePrivacyPolicyVersion: "",
    questionnaireFields: [],
    activitySummary: "",
    issueNo: "",
    additionalSessions: [],
    copySource: null,
  });
});

test("activity-create recovery does not hide malformed explicit values", () => {
  const malformed = {
    questionnairePrivacyPurpose: null,
    questionnairePrivacyPolicyVersion: 42,
    questionnaireFields: "invalid",
  };
  assert.deepEqual(normalizeActivityCreateForm(malformed), { ...malformed, activitySummary: "", issueNo: "", additionalSessions: [], copySource: null });
  assert.equal(normalizeActivityCreateForm(null), null);
});

test("activity-create conflict recovery reads the nested administrator instance", () => {
  const current = { id: "instance-old", version: 4 };
  const detail = {
    series: { id: "series-1" },
    instance: { id: "instance-new", version: 5 },
    sessions: [],
    questionnaire: { configured: false, fields: [] },
  };
  assert.deepEqual(reconcileInstanceCheckpoint(detail, current), {
    id: "instance-new",
    version: 5,
  });
  assert.equal(
    reconcileInstanceCheckpoint({ instance: { id: "instance-new", version: 4 } }, current),
    null,
  );
  assert.equal(reconcileInstanceCheckpoint({ version: 5 } as never, current), null);
});
