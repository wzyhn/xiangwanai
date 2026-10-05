import type { InstanceDetail, InstanceDetailBlock, QuestionnaireFieldType, Session } from "./types";

export type ActivityCopySession = {
  sessionTitle: string;
  registrationStartAt: string;
  registrationEndAt: string;
  sessionStartAt: string;
  sessionEndAt: string;
  capacity: number;
  groupMinimum: number;
  lowStockThreshold: number;
  priceYuan: string;
  deliveryMode: "offline" | "online";
  area: string;
  venueName: string;
  address: string;
  longitude: string;
  latitude: string;
  onlineMode: string;
  onlineCompliant: boolean;
};

export type ActivityCopySource = {
  instanceId: string;
  seriesId: string;
  version: number;
  presentationRevision: number;
  questionnaireVersionId: string;
};

export type ActivityCopyPrefill = ActivityCopySession & {
  copySource: ActivityCopySource;
  seriesId: string;
  seriesTitle: string;
  activityType: "ai_roundtable" | "special_event" | "course" | "competition" | "custom";
  quickTags: string;
  activitySummary: string;
  coverImageUrl: string;
  detailBlocks: InstanceDetailBlock[];
  questionnairePrivacyPurpose: string;
  questionnaireFields: Array<{
    localId: string;
    code: string;
    type: QuestionnaireFieldType;
    label: string;
    helpText: string;
    required: boolean;
    maxLength: number;
    maxSelections: number;
    options: Array<{ code: string; label: string }>;
  }>;
  additionalSessions: ActivityCopySession[];
};

function copySessionConfig(source: Session): ActivityCopySession {
  const priceCents = source.price_cents;
  const priceYuan = typeof priceCents === "number" && Number.isSafeInteger(priceCents) && priceCents >= 0
    ? (priceCents / 100).toFixed(2) : "0.00";
  return {
    sessionTitle: source.title,
    registrationStartAt: "", registrationEndAt: "", sessionStartAt: "", sessionEndAt: "",
    capacity: source.capacity || 30,
    groupMinimum: source.group_minimum || 1,
    lowStockThreshold: source.low_stock_threshold || 5,
    priceYuan,
    deliveryMode: source.delivery_mode || "offline",
    area: source.area === "online" ? "hexi" : source.area || "hexi",
    venueName: source.venue_name || "",
    address: source.address || "",
    longitude: source.longitude == null ? "" : String(source.longitude),
    latitude: source.latitude == null ? "" : String(source.latitude),
    onlineMode: source.online_participation_mode || "腾讯会议",
    // A prior period's compliance confirmation is not consent for this one.
    onlineCompliant: false,
  };
}

// Copy only editable presentation and operational defaults. A new period must
// receive its own schedule, issue number, questionnaire assignment, publication
// and registration facts through the existing create flow.
export function activityCopyPrefill(detail: InstanceDetail): ActivityCopyPrefill | null {
  const questionnaireVersionId = detail?.questionnaire?.configured
    ? detail.questionnaire.questionnaire_version_id || "" : "";
  if (!detail || detail.series.status === "archived" || !detail.sessions.length ||
    !Number.isSafeInteger(detail.instance.version) || detail.instance.version < 1 ||
    !Number.isSafeInteger(detail.instance.presentation_revision) ||
    !detail.instance.presentation_revision || detail.instance.presentation_revision < 1 ||
    (detail.questionnaire.configured &&
      !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(questionnaireVersionId))) return null;
  const sessions = [...detail.sessions].sort((left, right) =>
    left.sort_order - right.sort_order || left.id.localeCompare(right.id));
  const blocks = (detail.instance.detail_blocks || []).map((block) => ({ ...block }));
  const summaryIndex = blocks.findIndex((block) => block.type === "text" && block.title.trim() === "简介");
  const summary = summaryIndex >= 0 && blocks[summaryIndex].type === "text"
    ? blocks[summaryIndex].body : "";
  if (summaryIndex >= 0) blocks.splice(summaryIndex, 1);
  return {
    ...copySessionConfig(sessions[0]),
    copySource: {
      instanceId: detail.instance.id, seriesId: detail.instance.series_id,
      version: detail.instance.version,
      presentationRevision: detail.instance.presentation_revision,
      questionnaireVersionId,
    },
    seriesId: detail.series.id,
    seriesTitle: detail.series.title,
    activityType: detail.instance.activity_type || "ai_roundtable",
    quickTags: detail.instance.quick_tag_codes.join(", "),
    activitySummary: summary,
    coverImageUrl: detail.instance.cover_image_url || "",
    detailBlocks: blocks,
    questionnairePrivacyPurpose: detail.questionnaire.privacy_purpose || "用于本次活动报名与现场组织",
    questionnaireFields: detail.questionnaire.configured
      ? [...detail.questionnaire.fields]
        .sort((left, right) => left.sort_order - right.sort_order || left.field_id.localeCompare(right.field_id))
        .map((field) => ({
          localId: field.field_id,
          code: field.code,
          type: field.type,
          label: field.label,
          helpText: field.help_text,
          required: field.required,
          maxLength: field.max_length || (field.type === "multiline" ? 500 : 100),
          maxSelections: field.max_selections || 1,
          options: field.options.map((option) => ({ ...option })),
        }))
      : [],
    additionalSessions: sessions.slice(1).map(copySessionConfig),
  };
}
