import test from "node:test";
import assert from "node:assert/strict";
import { activityCopyPrefill } from "./activity-copy.ts";
import type { InstanceDetail } from "./types";

const source = {
  series: { id: "series-1", title: "AI 共创", status: "active" },
  instance: {
    id: "instance-1", series_id: "series-1", issue_no: 4,
    title: "第4期AI 共创", status: "completed", activity_type: "custom",
    version: 5, presentation_revision: 2,
    quick_tag_codes: ["workshop"], cover_image_url: "/api/v1/xiangwan/covers/a.png",
    detail_blocks: [
      { type: "text", title: "简介", body: "旧期介绍" },
      { type: "image", url: "https://example.com/photo.png", caption: "现场" },
    ],
  },
  sessions: [
    { id: "session-late", sort_order: 2, title: "晚场", capacity: 20, price_cents: 990,
      delivery_mode: "online", online_participation_compliant: true,
      registration_start_at: "2026-08-01T00:00:00Z", session_start_at: "2026-08-10T00:00:00Z" },
    { id: "session-early", sort_order: 0, title: "早场", capacity: 30, group_minimum: 4,
      low_stock_threshold: 3, price_cents: 1290, delivery_mode: "offline", area: "hexi",
      venue_name: "会场", address: "天津市河西区", longitude: 117.2, latitude: 39.1,
      registration_start_at: "2026-07-01T00:00:00Z", session_start_at: "2026-07-10T00:00:00Z" },
  ],
  questionnaire: {
    configured: true, privacy_purpose: "组织活动", privacy_policy_version: "old-policy",
    questionnaire_version_id: "00000000-0000-4000-8000-000000000001",
    fields: [{ field_id: "field-1", sort_order: 0, code: "role", type: "single_choice",
      label: "职业", help_text: "请选择", required: true, options: [{ code: "dev", label: "开发" }] }],
  },
} as unknown as InstanceDetail;

test("activity copy carries editable configuration but no old schedule or business facts", () => {
  const prefill = activityCopyPrefill(source);
  assert.ok(prefill);
  assert.equal(prefill.seriesId, "series-1");
  assert.deepEqual(prefill.copySource, {
    instanceId: "instance-1", seriesId: "series-1", version: 5,
    presentationRevision: 2,
    questionnaireVersionId: "00000000-0000-4000-8000-000000000001",
  });
  assert.equal(prefill.activityType, "custom");
  assert.equal(prefill.activitySummary, "旧期介绍");
  assert.deepEqual(prefill.detailBlocks, [{ type: "image", url: "https://example.com/photo.png", caption: "现场" }]);
  assert.equal(prefill.sessionTitle, "早场");
  assert.equal(prefill.additionalSessions.length, 1);
  assert.equal(prefill.additionalSessions[0].sessionTitle, "晚场");
  assert.equal(prefill.additionalSessions[0].priceYuan, "9.90");
  assert.equal(prefill.additionalSessions[0].deliveryMode, "online");
  assert.equal(prefill.additionalSessions[0].onlineCompliant, false);
  assert.equal(prefill.additionalSessions[0].registrationStartAt, "");
  assert.equal(prefill.additionalSessions[0].sessionStartAt, "");
  assert.equal(prefill.priceYuan, "12.90");
  assert.equal(prefill.questionnaireFields[0].code, "role");
  assert.equal(prefill.questionnaireFields[0].required, true);
  assert.equal(prefill.registrationStartAt, "");
  assert.equal("issueNo" in prefill, false);
  assert.equal("privacyPolicyVersion" in prefill, false);
  prefill.questionnaireFields[0].options[0].label = "修改";
  assert.equal(source.questionnaire.fields[0].options[0].label, "开发");
});

test("activity copy refuses archived series and source periods with no Session", () => {
  assert.equal(activityCopyPrefill({ ...source, series: { ...source.series, status: "archived" } }), null);
  assert.equal(activityCopyPrefill({ ...source, sessions: [] }), null);
  assert.equal(activityCopyPrefill({ ...source, instance: { ...source.instance, version: 0 } }), null);
  assert.equal(activityCopyPrefill({ ...source, questionnaire: {
    ...source.questionnaire, questionnaire_version_id: "",
  } }), null);
});

test("activity copy fences a source without a questionnaire as empty", () => {
  const prefill = activityCopyPrefill({ ...source, questionnaire: {
    configured: false, fields: [],
  } } as InstanceDetail);
  assert.equal(prefill?.copySource.questionnaireVersionId, "");
});
