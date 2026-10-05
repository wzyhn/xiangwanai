"use client";

import { ArrowLeft, Check, ImagePlus, Info, Plus, Send, Trash2, X } from "lucide-react";
import Link from "next/link";
import { useEffect, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import {
  ApiError, allPages, api,
  definitiveFailure,
  displayError,
  operationHeaders,
  operationKey,
} from "@/lib/api";
import { activityImageAccept, activityImageMaxBytes, isActivityImageRef, resolveActivityImageUrl, uploadActivityImage } from "@/lib/images";
import {
  DetailBlocksEditor,
  detailBlockBodyMaxRunes,
  isDetailBlockList,
  serializeDetailBlocks,
  validateDetailBlocks,
} from "@/components/detail-blocks-editor";
import {
  normalizeActivityCreateForm,
  reconcileInstanceCheckpoint,
} from "@/lib/activity-create-recovery";
import { activityCopyPrefill } from "@/lib/activity-copy";
import type { ActivityCopySession, ActivityCopySource } from "@/lib/activity-copy";
import { validateQuestionnaireConfig as validateQuestionnaireFields } from "@/lib/questionnaire-config";
import { shanghaiLocalToISOString } from "@/lib/time";
import type {
  Instance, InstanceDetail, InstanceDetailBlock, QuestionnaireFieldType, QuestionnaireOption,
  BrandQuickTag, BrandProfile, Series, Session,
} from "@/lib/types";

type CustomQuestionnaireField = {
  localId: string;
  code: string;
  type: QuestionnaireFieldType;
  label: string;
  helpText: string;
  required: boolean;
  maxLength: number;
  maxSelections: number;
  options: QuestionnaireOption[];
};

type FormState = {
  copySource: ActivityCopySource | null;
  seriesMode: "existing" | "new";
  seriesId: string;
  seriesTitle: string;
  issueNo: string;
  instanceTitle: string;
  activityType: "ai_roundtable" | "special_event" | "course" | "competition" | "custom";
  quickTags: string;
  activitySummary: string;
  coverImageUrl: string;
  detailBlocks: InstanceDetailBlock[];
  questionnairePrivacyPurpose: string;
  questionnairePrivacyPolicyVersion: string;
  questionnaireFields: CustomQuestionnaireField[];
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
  additionalSessions: ActivityCopySession[];
};

const initialForm: FormState = {
  copySource: null,
  seriesMode: "existing", seriesId: "", seriesTitle: "", issueNo: "",
  instanceTitle: "", activityType: "ai_roundtable", quickTags: "", activitySummary: "",
  coverImageUrl: "", detailBlocks: [],
  questionnairePrivacyPurpose: "用于本次活动报名与现场组织",
  questionnairePrivacyPolicyVersion: "",
  questionnaireFields: [],
  sessionTitle: "", registrationStartAt: "", registrationEndAt: "",
  sessionStartAt: "", sessionEndAt: "", capacity: 30, groupMinimum: 1,
  lowStockThreshold: 5, priceYuan: "0.00", deliveryMode: "offline",
  area: "hexi", venueName: "", address: "", longitude: "", latitude: "",
  onlineMode: "腾讯会议", onlineCompliant: false,
  additionalSessions: [],
};

type ActivityCreateRecovery = {
  version: 1;
  form: FormState;
  seriesOperation: string | null;
  instanceOperation: string | null;
  sessionOperation: string | null;
  questionnaireOperation: string | null;
  seriesCheckpoint: OperationCheckpoint | null;
  instanceCheckpoint: OperationCheckpoint | null;
  sessionCheckpoint: OperationCheckpoint | null;
  additionalSessionOperation: string | null;
  additionalSessionExpectedVersion: number | null;
  additionalSessionCheckpoints: OperationCheckpoint[];
  questionnaireCommitted: boolean;
  committedStage: 0 | 1 | 2 | 3;
};

type OperationCheckpoint = {
  id: string;
  version: number;
};

const activityCreateRecoveryKey = "xiangwan-admin:activity-create-recovery:v1";
const maxPostgresInteger = 2_147_483_647;
const activityCreateStringFields = [
  "seriesId", "seriesTitle", "issueNo", "instanceTitle", "quickTags", "activitySummary", "coverImageUrl", "sessionTitle",
  "registrationStartAt", "registrationEndAt", "sessionStartAt", "sessionEndAt",
  "priceYuan", "area", "venueName", "address", "longitude", "latitude", "onlineMode",
  "questionnairePrivacyPurpose", "questionnairePrivacyPolicyVersion",
] as const;
const copySessionStringFields = [
  "sessionTitle", "registrationStartAt", "registrationEndAt", "sessionStartAt",
  "sessionEndAt", "priceYuan", "area", "venueName", "address", "longitude",
  "latitude", "onlineMode",
] as const;
const operationKeyPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const canonicalUUIDPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

class ActivityCopyCheckpointMismatch extends Error {}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function isQuestionnaireField(value: unknown): value is CustomQuestionnaireField {
  if (!isRecord(value) || typeof value.localId !== "string" || typeof value.code !== "string" ||
    typeof value.label !== "string" || typeof value.helpText !== "string" ||
    typeof value.required !== "boolean" || typeof value.maxLength !== "number" ||
    typeof value.maxSelections !== "number" || !Array.isArray(value.options)) return false;
  if (!["single_choice", "multiple_choice", "single_line", "multiline", "area"].includes(String(value.type))) return false;
  return value.options.every((option) => isRecord(option) && typeof option.code === "string" && typeof option.label === "string");
}

function isCopySession(value: unknown): value is ActivityCopySession {
  return isRecord(value) &&
    copySessionStringFields.every((field) => typeof value[field] === "string") &&
    (value.deliveryMode === "offline" || value.deliveryMode === "online") &&
    typeof value.capacity === "number" && typeof value.groupMinimum === "number" &&
    typeof value.lowStockThreshold === "number" && typeof value.onlineCompliant === "boolean";
}

function isCopySource(value: unknown): value is ActivityCopySource {
  return isRecord(value) &&
    typeof value.instanceId === "string" && canonicalUUIDPattern.test(value.instanceId) &&
    typeof value.seriesId === "string" && canonicalUUIDPattern.test(value.seriesId) &&
    typeof value.version === "number" && Number.isSafeInteger(value.version) && value.version > 0 &&
    typeof value.presentationRevision === "number" &&
    Number.isSafeInteger(value.presentationRevision) && value.presentationRevision > 0 &&
    typeof value.questionnaireVersionId === "string" &&
    (value.questionnaireVersionId === "" || canonicalUUIDPattern.test(value.questionnaireVersionId));
}

function isFormState(value: unknown): value is FormState {
  if (!isRecord(value)) return false;
  return (
    (value.seriesMode === "existing" || value.seriesMode === "new") &&
    (value.copySource === null || isCopySource(value.copySource)) &&
    ["ai_roundtable", "special_event", "course", "competition", "custom"].includes(String(value.activityType)) &&
    (value.deliveryMode === "offline" || value.deliveryMode === "online") &&
    activityCreateStringFields.every((field) => typeof value[field] === "string") &&
    isDetailBlockList(value.detailBlocks) &&
    Array.isArray(value.questionnaireFields) && value.questionnaireFields.every(isQuestionnaireField) &&
    Array.isArray(value.additionalSessions) && value.additionalSessions.length <= 100 &&
    value.additionalSessions.every(isCopySession) &&
    typeof value.capacity === "number" &&
    typeof value.groupMinimum === "number" &&
    typeof value.lowStockThreshold === "number" &&
    typeof value.onlineCompliant === "boolean"
  );
}

function isStoredOperationKey(value: unknown): value is string | null {
  return value === null || (typeof value === "string" && operationKeyPattern.test(value));
}

function isOperationCheckpoint(value: unknown): value is OperationCheckpoint | null {
  return value === null || (
    isRecord(value) &&
    typeof value.id === "string" &&
    canonicalUUIDPattern.test(value.id) &&
    typeof value.version === "number" &&
    Number.isSafeInteger(value.version) &&
    value.version > 0
  );
}

function readActivityCreateRecovery(): ActivityCreateRecovery | null {
  if (typeof window === "undefined") return null;
  try {
    const raw = window.sessionStorage.getItem(activityCreateRecoveryKey);
    if (!raw) return null;
    const value: unknown = JSON.parse(raw);
    const migratedForm = isRecord(value) ? normalizeActivityCreateForm(value.form) : null;
    if (!isRecord(value) || value.version !== 1 || !isFormState(migratedForm) ||
      !isStoredOperationKey(value.seriesOperation) ||
      !isStoredOperationKey(value.instanceOperation) ||
      !isStoredOperationKey(value.sessionOperation) ||
      !isOperationCheckpoint(value.seriesCheckpoint) ||
      !isOperationCheckpoint(value.instanceCheckpoint) ||
      typeof value.committedStage !== "number" ||
      ![0, 1, 2, 3].includes(value.committedStage)) {
      window.sessionStorage.removeItem(activityCreateRecoveryKey);
      return null;
    }
    // Records written before questionnaire/session checkpoints were added are
    // still safe to resume. Treat the new fields as empty instead of dropping
    // an uncertain idempotency record and risking a duplicate write.
    if (value.questionnaireOperation !== undefined && !isStoredOperationKey(value.questionnaireOperation)) {
      window.sessionStorage.removeItem(activityCreateRecoveryKey);
      return null;
    }
    if (value.sessionCheckpoint !== undefined && !isOperationCheckpoint(value.sessionCheckpoint)) {
      window.sessionStorage.removeItem(activityCreateRecoveryKey);
      return null;
    }
    const additionalSessionOperation = value.additionalSessionOperation === undefined
      ? null : value.additionalSessionOperation;
    const additionalSessionExpectedVersion = value.additionalSessionExpectedVersion === undefined
      ? null : value.additionalSessionExpectedVersion;
    const additionalSessionCheckpoints = value.additionalSessionCheckpoints === undefined
      ? [] : value.additionalSessionCheckpoints;
    if (!isStoredOperationKey(additionalSessionOperation) ||
      (additionalSessionExpectedVersion !== null &&
        (typeof additionalSessionExpectedVersion !== "number" ||
          !Number.isSafeInteger(additionalSessionExpectedVersion) || additionalSessionExpectedVersion < 1)) ||
      Boolean(additionalSessionOperation) !== (additionalSessionExpectedVersion !== null) ||
      !Array.isArray(additionalSessionCheckpoints) ||
      additionalSessionCheckpoints.length > migratedForm.additionalSessions.length ||
      !additionalSessionCheckpoints.every((checkpoint) =>
        isOperationCheckpoint(checkpoint) && checkpoint !== null) ||
      (additionalSessionOperation !== null &&
        additionalSessionCheckpoints.length >= migratedForm.additionalSessions.length) ||
      (value.questionnaireCommitted !== undefined && typeof value.questionnaireCommitted !== "boolean")) {
      window.sessionStorage.removeItem(activityCreateRecoveryKey);
      return null;
    }
    return {
      ...value,
      form: migratedForm,
      questionnaireOperation: value.questionnaireOperation === undefined ? null : value.questionnaireOperation,
      sessionCheckpoint: value.sessionCheckpoint === undefined ? null : value.sessionCheckpoint,
      additionalSessionOperation,
      additionalSessionExpectedVersion,
      additionalSessionCheckpoints,
      questionnaireCommitted: value.questionnaireCommitted === true,
    } as ActivityCreateRecovery;
  } catch {
    return null;
  }
}

function writeActivityCreateRecovery(value: Omit<ActivityCreateRecovery, "version">): void {
  if (typeof window === "undefined") return;
  try {
    window.sessionStorage.setItem(
      activityCreateRecoveryKey,
      JSON.stringify({ version: 1, ...value } satisfies ActivityCreateRecovery),
    );
  } catch {
    // The current page still retains the operation keys when storage is unavailable.
  }
}

function clearActivityCreateRecovery(): void {
  if (typeof window === "undefined") return;
  try {
    window.sessionStorage.removeItem(activityCreateRecoveryKey);
  } catch {
    // Storage may be unavailable in hardened browser modes.
  }
}

function quickTagCodes(value: string): string[] {
  return value.split(",").map((item) => item.trim()).filter(Boolean);
}

function parseRequestedIssueNo(value: string): number | null {
  const normalized = value.trim();
  if (!normalized) return null;
  if (!/^\d+$/.test(normalized)) return Number.NaN;
  const parsed = Number(normalized);
  return Number.isSafeInteger(parsed) && parsed >= 1 && parsed <= maxPostgresInteger
    ? parsed
    : Number.NaN;
}

function previewIssueTitle(issueNo: number | null, seriesTitle: string): string {
  const title = seriesTitle.trim();
  if (!title) return "活动标题将在选择系列后生成";
  return issueNo && Number.isFinite(issueNo) ? `第${issueNo}期${title}` : `下一期${title}`;
}

// The shared Instance contract stores the prototype's one-line/paragraph
// introduction in the same ordered detail-block stream as the rest of the
// rich content. Keeping the marker title stable lets the edit page expose the
// same text block later without adding a second mutable summary column.
function detailBlocksWithSummary(form: FormState): InstanceDetailBlock[] {
  const summary = form.activitySummary.trim();
  const blocks = form.detailBlocks.map((block) => ({ ...block }));
  if (!summary) return blocks;
  const summaryIndex = blocks.findIndex(
    (block) => block.type === "text" && block.title.trim() === "简介",
  );
  if (summaryIndex >= 0) {
    const existing = blocks[summaryIndex];
    if (existing.type === "text") {
      blocks[summaryIndex] = { ...existing, title: "简介", body: summary };
    }
    return blocks;
  }
  return [{ type: "text", title: "简介", body: summary }, ...blocks];
}

function questionnaireOptionType(type: QuestionnaireFieldType): boolean {
  return type === "single_choice" || type === "multiple_choice" || type === "area";
}

function validateForm(form: FormState): string {
  // This form submits through React's handler, so the browser's native
  // `required` checks do not run unless we call `reportValidity()` ourselves.
  // Keep the same required contract in the explicit validator so an empty
  // draft cannot create a half-finished Series/Instance before the API rejects
  // its later Session write.
  if (form.seriesMode === "existing" && !form.seriesId.trim()) return "请选择一个活动系列";
  if (form.seriesMode === "new" && !form.seriesTitle.trim()) return "请填写系列名称";
  if (form.copySource && (form.seriesMode !== "existing" || form.seriesId !== form.copySource.seriesId)) {
    return "复制为新一期必须保留来源系列；请返回来源活动重新打开。";
  }
  const requestedIssueNo = parseRequestedIssueNo(form.issueNo);
  if (Number.isNaN(requestedIssueNo)) return "期次编号必须是正整数（可留空，由系统自动分配）";
  if (!form.sessionTitle.trim()) return "请填写场次名称";
  if (form.deliveryMode === "offline") {
    if (!form.venueName.trim()) return "请填写场地名称";
    if (!form.address.trim()) return "请填写完整地址";
  } else if (!form.onlineMode.trim()) {
    return "请填写线上参与方式说明";
  }
  const registrationStartValue = shanghaiLocalToISOString(form.registrationStartAt);
  const registrationEndValue = shanghaiLocalToISOString(form.registrationEndAt);
  const sessionStartValue = shanghaiLocalToISOString(form.sessionStartAt);
  const sessionEndValue = shanghaiLocalToISOString(form.sessionEndAt);
  if (!registrationStartValue || !registrationEndValue || !sessionStartValue || !sessionEndValue) {
    return "请填写完整、有效的报名与活动时间";
  }
  const registrationStart = Date.parse(registrationStartValue);
  const registrationEnd = Date.parse(registrationEndValue);
  const sessionStart = Date.parse(sessionStartValue);
  const sessionEnd = Date.parse(sessionEndValue);
  if (![registrationStart, registrationEnd, sessionStart, sessionEnd].every(Number.isFinite)) {
    return "请填写完整、有效的报名与活动时间";
  }
  if (registrationStart >= registrationEnd) return "报名开始时间必须早于报名截止时间";
  if (registrationEnd > sessionStart) return "报名截止时间不能晚于活动开始时间";
  if (sessionStart >= sessionEnd) return "活动开始时间必须早于活动结束时间";
  if (!Number.isInteger(form.capacity) || form.capacity < 2) return "容量必须是至少 2 的整数";
  if (form.capacity > maxPostgresInteger) return "人数超过系统支持范围，请填写更小的数值";
  if (!Number.isInteger(form.groupMinimum) || form.groupMinimum < 1 || form.groupMinimum > form.capacity) {
    return "成团人数必须是 1 到容量之间的整数";
  }
  if (form.groupMinimum > maxPostgresInteger) return "成团人数超过系统支持范围，请填写更小的数值";
  if (!Number.isInteger(form.lowStockThreshold) || form.lowStockThreshold < 1 || form.lowStockThreshold >= form.capacity) {
    return "余量提醒必须是大于 0 且小于容量的整数";
  }
  if (form.lowStockThreshold > maxPostgresInteger) return "余量提醒超过系统支持范围，请填写更小的数值";
  const tags = quickTagCodes(form.quickTags);
  if (tags.length > 20) return "活动标签最多填写 20 个";
  if (tags.some((code) => !/^[a-z][a-z0-9_]{0,31}$/.test(code))) {
    return "活动标签需以小写字母开头，只能包含小写字母、数字和下划线，且不超过 32 个字符";
  }
  if (new Set(tags).size !== tags.length) return "活动标签不能重复";
  if (form.coverImageUrl.trim() && !isActivityImageRef(form.coverImageUrl)) {
    return "活动封面地址必须是站内封面路径（/api/v1/xiangwan/covers/…）或完整、安全的 HTTPS 地址";
  }
  if ([...form.activitySummary.trim()].length > detailBlockBodyMaxRunes) {
    return `活动简介最长 ${detailBlockBodyMaxRunes} 字`;
  }
  const detailBlocksError = validateDetailBlocks(detailBlocksWithSummary(form));
  if (detailBlocksError) return detailBlocksError;
  const questionnaireError = validateQuestionnaireFields({
    privacyPurpose: form.questionnairePrivacyPurpose,
    privacyPolicyVersion: form.questionnairePrivacyPolicyVersion,
    fields: form.questionnaireFields,
  });
  if (questionnaireError) return questionnaireError;
  const price = Number(form.priceYuan);
  if (!/^\d+(\.\d{1,2})?$/.test(form.priceYuan) || !Number.isFinite(price) ||
    price < 0 || !Number.isSafeInteger(Math.round(price * 100))) {
    return "请输入有效的非负价格";
  }
  if (form.deliveryMode === "offline") {
    const longitude = Number(form.longitude);
    const latitude = Number(form.latitude);
    if (!Number.isFinite(longitude) || longitude < -180 || longitude > 180) return "经度必须在 -180 到 180 之间";
    if (!Number.isFinite(latitude) || latitude < -90 || latitude > 90) return "纬度必须在 -90 到 90 之间";
  } else if (!form.onlineCompliant) {
    return "请确认线上参与方式符合发布要求";
  }
  if (form.additionalSessions.length > 100) return "一次最多复制 100 个附加场次";
  for (const [index, session] of form.additionalSessions.entries()) {
    const error = validateForm({ ...form, ...session, additionalSessions: [] });
    if (error) return `第 ${index + 2} 个场次：${error}`;
  }
  return "";
}

function sessionCreatePayload(session: ActivityCopySession, expectedVersion: number, sortOrder: number) {
  return {
    expected_instance_version: expectedVersion,
    title: session.sessionTitle,
    registration_start_at: shanghaiLocalToISOString(session.registrationStartAt),
    registration_end_at: shanghaiLocalToISOString(session.registrationEndAt),
    session_start_at: shanghaiLocalToISOString(session.sessionStartAt),
    session_end_at: shanghaiLocalToISOString(session.sessionEndAt),
    capacity: session.capacity,
    group_minimum: session.groupMinimum,
    low_stock_threshold: session.lowStockThreshold,
    price_cents: Math.round(Number(session.priceYuan) * 100),
    delivery_mode: session.deliveryMode,
    area: session.deliveryMode === "online" ? "online" : session.area,
    venue_name: session.deliveryMode === "offline" ? session.venueName : "",
    address: session.deliveryMode === "offline" ? session.address : "",
    longitude: session.deliveryMode === "offline" ? Number(session.longitude) : null,
    latitude: session.deliveryMode === "offline" ? Number(session.latitude) : null,
    online_participation_mode: session.deliveryMode === "online" ? session.onlineMode : "",
    online_compliant: session.deliveryMode === "online" && session.onlineCompliant,
    sort_order: sortOrder,
  };
}

function RequiredMark() {
  return <span className="required-mark" aria-hidden="true">*</span>;
}

export default function NewActivityPage() {
  const router = useRouter();
  const [series, setSeries] = useState<Series[]>([]);
  const [brandQuickTags, setBrandQuickTags] = useState<BrandQuickTag[]>([]);
  const [form, setForm] = useState<FormState>(initialForm);
  const [submitting, setSubmitting] = useState(false);
  const [uncertain, setUncertain] = useState(false);
  const [committedStage, setCommittedStage] = useState<0 | 1 | 2 | 3>(0);
  const [sessionCommitted, setSessionCommitted] = useState(false);
  const [additionalSessionCommittedCount, setAdditionalSessionCommittedCount] = useState(0);
  const [error, setError] = useState("");
  const [uploadingCover, setUploadingCover] = useState(false);
  const [copyLoading, setCopyLoading] = useState(false);
  const [copyNotice, setCopyNotice] = useState("");
  const coverFileInput = useRef<HTMLInputElement | null>(null);
  const seriesOperation = useRef<string | null>(null);
  const instanceOperation = useRef<string | null>(null);
  const sessionOperation = useRef<string | null>(null);
  const additionalSessionOperation = useRef<string | null>(null);
  const additionalSessionExpectedVersion = useRef<number | null>(null);
  const additionalSessionCheckpoints = useRef<OperationCheckpoint[]>([]);
  const questionnaireOperation = useRef<string | null>(null);
  const questionnaireCommitted = useRef(false);
  const seriesCheckpoint = useRef<OperationCheckpoint | null>(null);
  const instanceCheckpoint = useRef<OperationCheckpoint | null>(null);
  const sessionCheckpoint = useRef<OperationCheckpoint | null>(null);

  function persistRecovery(stage: 0 | 1 | 2 | 3) {
    writeActivityCreateRecovery({
      form,
      seriesOperation: seriesOperation.current,
      instanceOperation: instanceOperation.current,
      sessionOperation: sessionOperation.current,
      additionalSessionOperation: additionalSessionOperation.current,
      additionalSessionExpectedVersion: additionalSessionExpectedVersion.current,
      additionalSessionCheckpoints: additionalSessionCheckpoints.current,
      questionnaireOperation: questionnaireOperation.current,
      questionnaireCommitted: questionnaireCommitted.current,
      seriesCheckpoint: seriesCheckpoint.current,
      instanceCheckpoint: instanceCheckpoint.current,
      sessionCheckpoint: sessionCheckpoint.current,
      committedStage: stage,
    });
  }

  useEffect(() => {
    const recovery = readActivityCreateRecovery();
    if (!recovery) return;
    setForm(recovery.form);
    seriesOperation.current = recovery.seriesOperation;
    instanceOperation.current = recovery.instanceOperation;
    sessionOperation.current = recovery.sessionOperation;
    additionalSessionOperation.current = recovery.additionalSessionOperation;
    additionalSessionExpectedVersion.current = recovery.additionalSessionExpectedVersion;
    additionalSessionCheckpoints.current = recovery.additionalSessionCheckpoints;
    setAdditionalSessionCommittedCount(recovery.additionalSessionCheckpoints.length);
    questionnaireOperation.current = recovery.questionnaireOperation;
    questionnaireCommitted.current = recovery.questionnaireCommitted;
    seriesCheckpoint.current = recovery.seriesCheckpoint;
    instanceCheckpoint.current = recovery.instanceCheckpoint;
    sessionCheckpoint.current = recovery.sessionCheckpoint;
    setSessionCommitted(Boolean(recovery.sessionCheckpoint));
    setCommittedStage(recovery.committedStage);
    const hasUnknownRequest = Boolean(
      recovery.seriesOperation || recovery.instanceOperation || recovery.sessionOperation ||
        recovery.additionalSessionOperation || recovery.questionnaireOperation,
    );
    setUncertain(hasUnknownRequest);
    setError(hasUnknownRequest
      ? "上次创建尚未确认完成，请保持当前内容并重试，系统会自动接续处理。"
      : "已恢复前面成功创建的内容，可修改剩余信息后继续。");
  }, []);

  useEffect(() => {
    if (typeof window === "undefined" || readActivityCreateRecovery()) return;
    const sourceId = new URLSearchParams(window.location.search).get("copy_instance_id")?.trim().toLowerCase();
    if (!sourceId) return;
    if (!canonicalUUIDPattern.test(sourceId)) {
      setError("复制来源链接无效，请从活动详情重新打开。");
      return;
    }
    let active = true;
    setCopyLoading(true);
    void api<InstanceDetail>(`/instances/${sourceId}`)
      .then((source) => {
        if (!active) return;
        const prefill = activityCopyPrefill(source);
        if (!prefill) {
          setError("来源活动没有可复制的场次，或所属系列已归档。请手动创建新一期。");
          return;
        }
        setForm((current) => ({
          ...current, ...prefill,
          seriesMode: "existing", issueNo: "",
          questionnairePrivacyPolicyVersion: "",
        }));
        setCopyNotice(`已带入「${source.instance.title}」的内容和 ${source.sessions.length} 个场次设置。请逐场重新填写时间、核对场次名称、线上合规确认与隐私政策；创建时会逐场保存到新草稿。原期报名、订单和回顾不会复制。`);
      })
      .catch((reason: unknown) => { if (active) setError(displayError(reason)); })
      .finally(() => { if (active) setCopyLoading(false); });
    return () => { active = false; };
  }, []);

  useEffect(() => {
    let active = true;
    void allPages<Series>("/series")
      .then((page) => {
        if (!active) return;
        const eligibleSeries = page.items.filter((item) => item.status !== "archived");
        setSeries(eligibleSeries);
        const recovering = Boolean(
          seriesOperation.current || instanceOperation.current || sessionOperation.current || questionnaireOperation.current,
        );
        if (recovering) return;
        if (eligibleSeries.length > 0) {
          setForm((current) => ({
            ...current,
            seriesId: eligibleSeries.some((item) => item.id === current.seriesId)
              ? current.seriesId
              : eligibleSeries[0].id,
          }));
        } else {
          setForm((current) => ({ ...current, seriesMode: "new", seriesId: "" }));
        }
      })
      .catch((reason: unknown) => active && setError(displayError(reason)));
    return () => { active = false; };
  }, []);

  useEffect(() => {
    let active = true;
    void api<BrandProfile>("/brand-profile")
      .then((profile) => {
        if (!active) return;
        setBrandQuickTags(Array.isArray(profile.quick_tags) ? profile.quick_tags : []);
      })
      .catch(() => {
        // The raw code input remains available when the optional homepage
        // vocabulary is not yet configured or cannot be read.
        if (active) setBrandQuickTags([]);
      });
    return () => { active = false; };
  }, []);

  // The questionnaire stores the exact public privacy-policy version that
  // the consumer registration form acknowledges. Pre-fill it from the live
  // public policy projection when the deployment exposes that route; the
  // operator can still replace it when preparing a policy rollout.
  useEffect(() => {
    if (form.questionnairePrivacyPolicyVersion || typeof window === "undefined") return;
    let active = true;
    void fetch("/api/v1/xiangwan/public-policies", { credentials: "include", cache: "no-store" })
      .then((response) => response.ok ? response.json() as Promise<{ data?: { published_versions?: Array<{ kind?: string; version?: string }> } }> : null)
      .then((body) => {
        if (!active || !body?.data?.published_versions) return;
        const privacy = body.data.published_versions.find((item) => item.kind === "privacy")?.version;
        if (privacy) setForm((current) => current.questionnairePrivacyPolicyVersion ? current : { ...current, questionnairePrivacyPolicyVersion: privacy });
      })
      .catch(() => {});
    return () => { active = false; };
  }, [form.questionnairePrivacyPolicyVersion]);

  function update<K extends keyof FormState>(key: K, value: FormState[K]) {
    setForm((current) => ({ ...current, [key]: value }));
  }

  function updateAdditionalSession(index: number, patch: Partial<ActivityCopySession>) {
    setForm((current) => ({
      ...current,
      additionalSessions: current.additionalSessions.map((session, position) =>
        position === index ? { ...session, ...patch } : session),
    }));
  }

  function removeAdditionalSession(index: number) {
    if (committedStage !== 0 || uncertain || submitting) return;
    setForm((current) => ({
      ...current,
      additionalSessions: current.additionalSessions.filter((_, position) => position !== index),
    }));
  }

  function toggleQuickTag(code: string) {
    const normalized = code.trim();
    if (!normalized) return;
    const selected = quickTagCodes(form.quickTags);
    const next = selected.includes(normalized)
      ? selected.filter((item) => item !== normalized)
      : selected.length >= 20
        ? selected
        : [...selected, normalized];
    update("quickTags", next.join(", "));
  }

  function localQuestionnaireID(): string {
    try {
      return crypto.randomUUID();
    } catch {
      return `question-${Date.now()}-${Math.random().toString(16).slice(2)}`;
    }
  }

  function newQuestionnaireField(type: QuestionnaireFieldType = "single_line"): CustomQuestionnaireField {
    const choice = questionnaireOptionType(type);
    return {
      localId: localQuestionnaireID(), code: `field_${form.questionnaireFields.length + 1}`,
      type, label: "", helpText: "", required: false, maxLength: type === "multiline" ? 500 : 100,
      maxSelections: 1,
      options: choice ? [{ code: "option_1", label: "" }, { code: "option_2", label: "" }] : [],
    };
  }

  function updateQuestionnaireField(localId: string, patch: Partial<CustomQuestionnaireField>) {
    update("questionnaireFields", form.questionnaireFields.map((field) =>
      field.localId === localId ? { ...field, ...patch } : field));
  }

  function changeQuestionnaireFieldType(field: CustomQuestionnaireField, type: QuestionnaireFieldType) {
    const next = newQuestionnaireField(type);
    updateQuestionnaireField(field.localId, {
      type,
      maxLength: next.maxLength,
      maxSelections: next.maxSelections,
      options: questionnaireOptionType(type) ? next.options : [],
    });
  }

  function addQuestionnaireField() {
    update("questionnaireFields", [...form.questionnaireFields, newQuestionnaireField()]);
  }

  function removeQuestionnaireField(localId: string) {
    update("questionnaireFields", form.questionnaireFields.filter((field) => field.localId !== localId));
  }

  function updateQuestionnaireOption(localId: string, optionIndex: number, patch: Partial<QuestionnaireOption>) {
    updateQuestionnaireField(localId, {
      options: form.questionnaireFields.find((field) => field.localId === localId)?.options.map((option, index) =>
        index === optionIndex ? { ...option, ...patch } : option) || [],
    });
  }

  function addQuestionnaireOption(field: CustomQuestionnaireField) {
    if (field.options.length >= 100) return;
    updateQuestionnaireField(field.localId, {
      options: [...field.options, { code: `option_${field.options.length + 1}`, label: "" }],
    });
  }

  function removeQuestionnaireOption(field: CustomQuestionnaireField, optionIndex: number) {
    updateQuestionnaireField(field.localId, {
      options: field.options.filter((_, index) => index !== optionIndex),
    });
  }

  // 活动封面与品牌横幅同一条上传链路:服务端只接受 JPEG/PNG/WebP(魔数校验)、
  // 5 MiB 以内;图文详情图片走同一封装(lib/images),上传返回站内相对路径,
  // 表单直接保存,预览时经同源媒体代理展示(lib/images resolveActivityImageUrl)。
  async function uploadCoverImage(file: File) {
    if (!activityImageAccept.includes(file.type)) {
      setError("活动封面仅支持 JPG、PNG 或 WebP 格式。");
      return;
    }
    if (file.size > activityImageMaxBytes) {
      setError("活动封面不能超过 5 MiB。");
      return;
    }
    setUploadingCover(true);
    setError("");
    try {
      update("coverImageUrl", await uploadActivityImage(file));
    } catch (reason) {
      setError(reason instanceof ApiError
        ? "活动封面上传失败，请确认格式与大小后重试。"
        : "活动封面上传失败，请稍后重试。");
    } finally {
      setUploadingCover(false);
      if (coverFileInput.current) coverFileInput.current.value = "";
    }
  }

  // 版本冲突(409)后对账父级 checkpoint:另一操作者在该系列/期次下推进了
  // 版本时,重放旧 expected version 只会永远 409。刷新到权威版本(或清除已
  // 不存在的系列),让重试真正可行(codex review 2026-09-19)。返回对操作者
  // 的说明;null 表示无法对账,回退到通用错误。
  async function reconcileCheckpointsAfterConflict(): Promise<string | null> {
    if (seriesCheckpoint.current) {
      try {
        const page = await allPages<Series>("/series");
        const refreshed = page.items.find((item) => item.id === seriesCheckpoint.current?.id);
        if (!refreshed || refreshed.status === "archived") {
          seriesCheckpoint.current = null;
          return "所属系列已不可用，请回到第一步重新选择或创建系列。";
        }
        if (refreshed.version !== seriesCheckpoint.current.version) {
          seriesCheckpoint.current = { id: refreshed.id, version: refreshed.version };
          return "所属系列已被其他操作更新，版本已自动同步，请重试创建。";
        }
      } catch {
        return null;
      }
    }
    if (instanceCheckpoint.current) {
      try {
        const detail = await api<InstanceDetail>(`/instances/${instanceCheckpoint.current.id}`);
        // GET /instances/:id returns the complete administrator detail
        // projection ({ series, instance, sessions, questionnaire }); the
        // checkpoint fence lives on its nested Instance. Treating this
        // response as a flat Instance leaves a stale version after a
        // concurrent writer wins and makes every retry collide again.
        const reconciledCheckpoint = reconcileInstanceCheckpoint(
          detail,
          instanceCheckpoint.current,
        );
        if (reconciledCheckpoint) {
          instanceCheckpoint.current = reconciledCheckpoint;
          return "期次版本已被其他操作更新，已自动同步，请重试创建。";
        }
      } catch {
        return null;
      }
    }
    return null;
  }

  async function submit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (copyLoading) return;
    setError("");
    const validationError = validateForm(form);
    if (validationError) {
      setError(validationError);
      return;
    }
    const detailBlocks = detailBlocksWithSummary(form);
    setSubmitting(true);
    let completedStage = committedStage;
    try {
      let selectedSeries = (completedStage >= 1 || uncertain) ? seriesCheckpoint.current : null;
      if (!selectedSeries && form.seriesMode === "new") {
        seriesOperation.current ||= operationKey();
        persistRecovery(completedStage);
        try {
          const createdSeries = await api<Series>("/series", {
            method: "POST", headers: operationHeaders(seriesOperation.current),
            body: JSON.stringify({ title: form.seriesTitle, home_visible: true }),
          });
          selectedSeries = { id: createdSeries.id, version: createdSeries.version };
          seriesCheckpoint.current = selectedSeries;
          seriesOperation.current = null;
          completedStage = Math.max(completedStage, 1) as 0 | 1 | 2;
          setCommittedStage(completedStage);
        } catch (reason) {
          if (definitiveFailure(reason)) seriesOperation.current = null;
          throw reason;
        }
      } else if (!selectedSeries) {
        const found = series.find((item) => item.id === form.seriesId);
        if (!found) throw new Error("请选择一个活动系列");
        selectedSeries = { id: found.id, version: found.version };
        seriesCheckpoint.current = selectedSeries;
      }

      const requestedIssueNo = parseRequestedIssueNo(form.issueNo);
      const selectedSeriesTitle = form.seriesMode === "new"
        ? form.seriesTitle
        : series.find((item) => item.id === selectedSeries.id)?.title || "";
      const generatedTitle = previewIssueTitle(requestedIssueNo, selectedSeriesTitle);
      let instance = completedStage >= 2 ? instanceCheckpoint.current : null;
      if (!instance) {
        instanceOperation.current ||= operationKey();
        persistRecovery(completedStage);
        try {
          const createdInstance = await api<Instance>(form.copySource
            ? `/instances/${form.copySource.instanceId}/copies` : "/instances", {
            method: "POST", headers: operationHeaders(instanceOperation.current),
            body: JSON.stringify({
              series_id: selectedSeries.id,
              expected_series_version: selectedSeries.version,
              title: generatedTitle,
              ...(requestedIssueNo !== null ? { issue_no: requestedIssueNo } : {}),
              activity_type: form.activityType,
              quick_tag_codes: quickTagCodes(form.quickTags),
              ...(form.coverImageUrl.trim() ? { cover_image_url: form.coverImageUrl.trim() } : {}),
              ...(detailBlocks.length > 0 ? { detail_blocks: serializeDetailBlocks(detailBlocks) } : {}),
              ...(form.copySource ? {
                expected_source_version: form.copySource.version,
                expected_source_presentation_revision: form.copySource.presentationRevision,
                expected_source_questionnaire_version_id: form.copySource.questionnaireVersionId,
              } : {}),
            }),
          });
          instance = { id: createdInstance.id, version: createdInstance.version };
          instanceCheckpoint.current = instance;
          instanceOperation.current = null;
          completedStage = 2;
          setCommittedStage(completedStage);
        } catch (reason) {
          if (definitiveFailure(reason)) instanceOperation.current = null;
          throw reason;
        }
      }

      if (completedStage < 3 && !sessionCheckpoint.current) {
        sessionOperation.current ||= operationKey();
        persistRecovery(completedStage);
        try {
          const createdSession = await api<Session>(`/instances/${instance.id}/sessions`, {
            method: "POST", headers: operationHeaders(sessionOperation.current),
            body: JSON.stringify(sessionCreatePayload(form, instance.version, 0)),
          });
          // The Session write is now known to have committed. Keep its exact
          // receipt so a later questionnaire validation error can be repaired
          // without replaying the Session command with a changed payload.
          sessionCheckpoint.current = { id: createdSession.id, version: createdSession.version };
          sessionOperation.current = null;
          setSessionCommitted(true);
          persistRecovery(completedStage);
        } catch (reason) {
          if (definitiveFailure(reason)) sessionOperation.current = null;
          throw reason;
        }
      }
      for (let index = additionalSessionCheckpoints.current.length;
        index < form.additionalSessions.length; index++) {
        if (!additionalSessionOperation.current) {
          const latest = await api<InstanceDetail>(`/instances/${instance.id}`);
          const committedIds = [sessionCheckpoint.current?.id,
            ...additionalSessionCheckpoints.current.map((checkpoint) => checkpoint.id)];
          if (latest.instance.id !== instance.id || latest.instance.status !== "draft" ||
            committedIds.some((id) => !id || !latest.sessions.some((session) => session.id === id))) {
            throw new ActivityCopyCheckpointMismatch("已创建的场次与服务端记录不一致，请在活动详情页核对后继续。");
          }
          instanceCheckpoint.current = { id: latest.instance.id, version: latest.instance.version };
          additionalSessionExpectedVersion.current = latest.instance.version;
          additionalSessionOperation.current = operationKey();
          persistRecovery(completedStage);
        }
        try {
          const createdSession = await api<Session>(`/instances/${instance.id}/sessions`, {
            method: "POST",
            headers: operationHeaders(additionalSessionOperation.current),
            body: JSON.stringify(sessionCreatePayload(
              form.additionalSessions[index], additionalSessionExpectedVersion.current!, index + 1,
            )),
          });
          additionalSessionCheckpoints.current = [
            ...additionalSessionCheckpoints.current,
            { id: createdSession.id, version: createdSession.version },
          ];
          setAdditionalSessionCommittedCount(additionalSessionCheckpoints.current.length);
          additionalSessionOperation.current = null;
          additionalSessionExpectedVersion.current = null;
          persistRecovery(completedStage);
        } catch (reason) {
          if (definitiveFailure(reason)) {
            additionalSessionOperation.current = null;
            additionalSessionExpectedVersion.current = null;
          }
          throw reason;
        }
      }
      if (form.additionalSessions.length > 0) {
        const latest = await api<InstanceDetail>(`/instances/${instance.id}`);
        const committedIds = [sessionCheckpoint.current?.id,
          ...additionalSessionCheckpoints.current.map((checkpoint) => checkpoint.id)];
        if (latest.instance.id !== instance.id || latest.instance.status !== "draft" ||
          additionalSessionCheckpoints.current.length !== form.additionalSessions.length ||
          committedIds.some((id) => !id || !latest.sessions.some((session) => session.id === id))) {
          throw new ActivityCopyCheckpointMismatch("场次创建结果与服务端记录不一致，请在活动详情页核对后继续。");
        }
        instanceCheckpoint.current = { id: latest.instance.id, version: latest.instance.version };
        persistRecovery(completedStage);
      }
      if (completedStage < 3 && form.questionnaireFields.length > 0 && !questionnaireCommitted.current) {
        questionnaireOperation.current ||= operationKey();
        persistRecovery(completedStage);
        try {
          await api(`/instances/${instance.id}/questionnaire`, {
            method: "POST", headers: operationHeaders(questionnaireOperation.current),
            body: JSON.stringify({
              privacy_purpose: form.questionnairePrivacyPurpose.trim(),
              privacy_policy_version: form.questionnairePrivacyPolicyVersion.trim(),
              fields: form.questionnaireFields.map((field, index) => ({
                code: field.code.trim(), type: field.type, label: field.label.trim(),
                help_text: field.helpText.trim(), required: field.required, sort_order: index,
                min_length: null,
                max_length: field.type === "single_line" || field.type === "multiline" ? field.maxLength : null,
                max_selections: field.type === "multiple_choice" ? field.maxSelections : null,
                options: questionnaireOptionType(field.type)
                  ? field.options.map((option) => ({ code: option.code.trim(), label: option.label.trim() }))
                  : [],
              })),
            }),
          });
          questionnaireCommitted.current = true;
          questionnaireOperation.current = null;
          persistRecovery(completedStage);
        } catch (reason) {
          if (definitiveFailure(reason)) questionnaireOperation.current = null;
          throw reason;
        }
      }
      completedStage = 3;
      setCommittedStage(completedStage);
      clearActivityCreateRecovery();
      router.push(`/activities/${instance.id}`);
    } catch (reason) {
      if (definitiveFailure(reason) || reason instanceof ActivityCopyCheckpointMismatch) {
        if (reason instanceof ApiError && reason.status === 409) {
          const reconciled = await reconcileCheckpointsAfterConflict();
          if (reconciled) {
            persistRecovery(completedStage);
            setUncertain(false);
            setError(reconciled);
            return;
          }
        }
        if (completedStage > 0) {
          persistRecovery(completedStage);
        } else {
          clearActivityCreateRecovery();
        }
        setUncertain(false);
        setError(form.copySource && reason instanceof ApiError && reason.status === 409 && completedStage < 2
          ? "复制来源、所属系列或期号已变化。请从来源活动重新打开复制页面，核对最新内容后再创建。"
          : completedStage > 0
            ? `前 ${completedStage} 步已保留；${displayError(reason)}。请修正剩余信息后继续。`
            : displayError(reason));
      } else {
        persistRecovery(completedStage);
        setUncertain(true);
        setError("暂时无法确认是否创建成功。请保持当前内容并重试，系统会自动接续处理。");
      }
    } finally {
      setSubmitting(false);
    }
  }

  const previewTags = quickTagCodes(form.quickTags)
    .slice(0, 2)
    .map((code, index) => {
      const configured = brandQuickTags.find((tag) => tag.code === code);
      const label = configured?.label?.trim() || code;
      return {
        key: `${code}-${index}`,
        label: label.startsWith("#") ? label : `#${label}`,
      };
    });
  const selectedSeriesTitle = form.seriesMode === "new"
    ? form.seriesTitle
    : series.find((item) => item.id === form.seriesId)?.title || "";
  const requestedIssueNo = parseRequestedIssueNo(form.issueNo);
  const generatedTitle = previewIssueTitle(requestedIssueNo, selectedSeriesTitle);

  return (
    <form className="editor-page" onSubmit={submit}>
      <div className="editor-form">
        {uncertain ? <span className="back-link disabled-link" aria-disabled="true"><ArrowLeft /> 返回活动期次</span> : <Link className="back-link" href="/activities"><ArrowLeft /> 返回活动期次</Link>}
        <div className="stepbar"><span className="on"><b>1</b> 活动系列</span><span className="on"><b>2</b> 期次信息</span><span className="on"><b>3</b> 报名模板</span><span className="on"><b>4</b> 场次与报名</span><span><b>5</b> 发布</span></div>
        {copyLoading && <div className="inline-message">正在读取可复制的活动设置…</div>}
        {copyNotice && <div className="inline-message success">{copyNotice}</div>}
        {error && <div className="inline-message error">{error}</div>}

        <fieldset className="editor-fieldset" disabled={submitting || uncertain || committedStage >= 1}>
        <section className="form-section">
          <div className="form-title"><span>01</span><div><h2>所属活动系列</h2><p>长期栏目与本期活动分开管理。</p></div></div>
          <div className="segmented">
            <button type="button" className={form.seriesMode === "existing" ? "active" : ""} onClick={() => update("seriesMode", "existing")} disabled={series.length === 0}>选择已有系列</button>
            <button type="button" className={form.seriesMode === "new" ? "active" : ""} onClick={() => update("seriesMode", "new")} disabled={Boolean(form.copySource)}>创建新系列</button>
          </div>
          <div className="field-grid single-top">
            {form.seriesMode === "existing" ? <label className="full">活动系列 <RequiredMark /><select value={form.seriesId} onChange={(event) => update("seriesId", event.target.value)} disabled={Boolean(form.copySource)} required aria-required={true}>{series.map((item) => <option key={item.id} value={item.id}>{item.title}</option>)}</select></label> : <label className="full">系列名称 <RequiredMark /><input value={form.seriesTitle} onChange={(event) => update("seriesTitle", event.target.value)} maxLength={200} placeholder="例如：天津 AI 圆桌派" required aria-required={true} /></label>}
          </div>
        </section>
        </fieldset>

        <fieldset className="editor-fieldset" disabled={submitting || uncertain || committedStage >= 2}>
        <section className="form-section">
          <div className="form-title"><span>02</span><div><h2>本期期次</h2><p>保存后仍是草稿，只有明确发布才会进入小程序。</p></div></div>
          <div className="field-grid">
            <label>期次编号（可选）<input inputMode="numeric" pattern="[0-9]*" value={form.issueNo} onChange={(event) => update("issueNo", event.target.value)} placeholder="留空自动分配" /></label>
            <label className="full">活动标题（系统生成）<input value={generatedTitle} readOnly aria-readonly="true" /></label>
            <label>活动类型 <span className="required-mark" aria-hidden="true">*</span><select value={form.activityType} onChange={(event) => update("activityType", event.target.value as FormState["activityType"])} required={true} aria-required={true}><option value="ai_roundtable">AI 圆桌派</option><option value="special_event">专题活动</option><option value="course">课程</option><option value="competition">赛事</option><option value="custom">自定义活动</option></select></label>
            <div className="full">
              <label>活动标签<input value={form.quickTags} onChange={(event) => update("quickTags", event.target.value)} placeholder="选择已发布标签，或输入编码（多个用逗号分隔）" /></label>
              {brandQuickTags.length > 0 && <div className="quick-tag-picker" aria-label="已发布活动标签">
                {brandQuickTags.map((tag) => {
                  const selected = quickTagCodes(form.quickTags).includes(tag.code);
                  return <button
                    type="button"
                    key={tag.code}
                    className={`quick-tag-option${selected ? " selected" : ""}`}
                    aria-pressed={selected}
                    onClick={() => toggleQuickTag(tag.code)}
                    disabled={submitting || uncertain || committedStage >= 2}
                  >#{tag.label}<small>{tag.code}</small></button>;
                })}
              </div>}
              <p className="brand-hero-hint activity-tag-hint">{brandQuickTags.length > 0 ? "点击标签即可加入或移除；发布时只接受首页当前已发布的标签编码。" : "尚未读取到已发布标签，可先填写编码；发布前系统会校验标签是否已在首页配置中发布。"}</p>
            </div>
            <label className="full">活动简介（可选）<textarea rows={3} maxLength={detailBlockBodyMaxRunes} value={form.activitySummary} onChange={(event) => update("activitySummary", event.target.value)} placeholder="用一段话介绍本期活动；保存后会作为详情中的“简介”内容块展示。" /></label>
            <div className="full">
              <label>活动封面（JPG / PNG / WebP，最大 5 MiB，可不设置）
                <input
                  ref={coverFileInput}
                  type="file"
                  accept={activityImageAccept.join(",")}
                  disabled={uploadingCover || submitting}
                  onChange={(event) => {
                    const file = event.target.files?.[0];
                    if (file) void uploadCoverImage(file);
                  }}
                />
              </label>
              {form.coverImageUrl && (
                <div className="cover-preview-row">
                  <div className="cover-preview-thumb" role="img" aria-label="活动封面预览" style={{ backgroundImage: `url(${JSON.stringify(resolveActivityImageUrl(form.coverImageUrl))})` }} />
                  <p className="brand-hero-hint"><ImagePlus /> 已上传封面，选择新文件可替换，随活动创建一并保存。</p>
                  <button type="button" className="secondary-button" disabled={uploadingCover} onClick={() => update("coverImageUrl", "")}><Trash2 /> 清除封面</button>
                </div>
              )}
            </div>
            <div className="full">
              <span style={{ display: "block", marginBottom: 6, color: "#617084", fontSize: 11 }}>图文详情（可选，文字与图片块按顺序展示；活动简介会自动作为“简介”文字块排在最前）</span>
              <DetailBlocksEditor
                blocks={form.detailBlocks}
                onChange={(blocks) => update("detailBlocks", blocks)}
                disabled={submitting}
                onError={setError}
              />
            </div>
          </div>
        </section>
        </fieldset>

        <fieldset className="editor-fieldset" disabled={submitting || uncertain || committedStage >= 3}>
        <section className="form-section">
          <div className="form-title"><span>03</span><div><h2>报名信息模板</h2><p>昵称 <span className="required-mark">*</span> 和手机号 <span className="required-mark">*</span> 由平台自动带入并要求填写；这里可继续添加本期专属问题。</p></div></div>
          <div className="field-grid">
            <label className="full"><span>信息用途说明{form.questionnaireFields.length > 0 && <span className="required-mark">*</span>}</span><input value={form.questionnairePrivacyPurpose} onChange={(event) => update("questionnairePrivacyPurpose", event.target.value)} maxLength={500} placeholder="用于本次活动报名与现场组织" required={form.questionnaireFields.length > 0} aria-required={form.questionnaireFields.length > 0} /></label>
            <label className="full"><span>隐私政策版本{form.questionnaireFields.length > 0 && <span className="required-mark">*</span>}</span><input value={form.questionnairePrivacyPolicyVersion} onChange={(event) => update("questionnairePrivacyPolicyVersion", event.target.value)} maxLength={100} placeholder="例如 privacy-v1（需与当前公开政策版本一致）" required={form.questionnaireFields.length > 0} aria-required={form.questionnaireFields.length > 0} /></label>
          </div>
          <div style={{ display: "grid", gap: 12, marginTop: 14 }}>
            {form.questionnaireFields.map((field, index) => (
              <article className="panel" style={{ margin: 0, padding: 14 }} key={field.localId}>
                <div className="panel-head" style={{ padding: 0, border: 0 }}>
                  <div><b>字段 {index + 1}</b><p>字段编码用于保存报名答案，只能使用小写字母、数字和下划线。</p></div>
                  <button type="button" className="secondary-button" onClick={() => removeQuestionnaireField(field.localId)}><Trash2 /> 删除</button>
                </div>
                <div className="field-grid" style={{ marginTop: 12 }}>
                  <label><span>字段编码 <span className="required-mark">*</span></span><input value={field.code} maxLength={64} onChange={(event) => updateQuestionnaireField(field.localId, { code: event.target.value })} required aria-required={true} /></label>
                  <label><span>字段类型 <span className="required-mark">*</span></span><select value={field.type} onChange={(event) => changeQuestionnaireFieldType(field, event.target.value as QuestionnaireFieldType)} required aria-required={true}><option value="single_line">单行文本</option><option value="multiline">多行文本</option><option value="single_choice">单选</option><option value="multiple_choice">多选</option><option value="area">地区</option></select></label>
                  <label className="full"><span>字段标题 <span className="required-mark">*</span></span><input value={field.label} maxLength={200} onChange={(event) => updateQuestionnaireField(field.localId, { label: event.target.value })} placeholder="例如：你的职业方向" required aria-required={true} /></label>
                  <label className="full">填写提示（可选）<input value={field.helpText} maxLength={500} onChange={(event) => updateQuestionnaireField(field.localId, { helpText: event.target.value })} placeholder="告诉报名者如何填写" /></label>
                  <label className="checkbox-field"><input type="checkbox" checked={field.required} onChange={(event) => updateQuestionnaireField(field.localId, { required: event.target.checked })} /> 必填</label>
                  {(field.type === "single_line" || field.type === "multiline") && <label><span>字数上限 <span className="required-mark">*</span></span><input type="number" min={1} max={field.type === "single_line" ? 200 : 2000} value={field.maxLength} onChange={(event) => updateQuestionnaireField(field.localId, { maxLength: event.target.valueAsNumber })} required aria-required={true} /></label>}
                  {field.type === "multiple_choice" && <label><span>最多选择 <span className="required-mark">*</span></span><input type="number" min={1} max={field.options.length || 1} value={field.maxSelections} onChange={(event) => updateQuestionnaireField(field.localId, { maxSelections: event.target.valueAsNumber })} required aria-required={true} /></label>}
                </div>
                {questionnaireOptionType(field.type) && <div style={{ display: "grid", gap: 8, marginTop: 12 }}>
                  <span style={{ color: "var(--muted)", fontSize: 11 }}>选项（编码和名称均为必填 <span className="required-mark">*</span>）</span>
                  {field.options.map((option, optionIndex) => <div key={`${field.localId}-${optionIndex}`} style={{ display: "grid", gridTemplateColumns: "1fr 1fr auto", gap: 8, alignItems: "center" }}>
                    <label>编码 <span className="required-mark" aria-hidden="true">*</span><input value={option.code} maxLength={64} aria-label={`选项 ${optionIndex + 1} 编码`} onChange={(event) => updateQuestionnaireOption(field.localId, optionIndex, { code: event.target.value })} placeholder="option_code" required aria-required="true" /></label>
                    <label>名称 <span className="required-mark" aria-hidden="true">*</span><input value={option.label} maxLength={100} aria-label={`选项 ${optionIndex + 1} 名称`} onChange={(event) => updateQuestionnaireOption(field.localId, optionIndex, { label: event.target.value })} placeholder="选项名称" required aria-required="true" /></label>
                    <button type="button" className="icon-button" aria-label="删除选项" onClick={() => removeQuestionnaireOption(field, optionIndex)} disabled={field.options.length <= 1}><X /></button>
                  </div>)}
                  <button type="button" className="secondary-button" onClick={() => addQuestionnaireOption(field)} disabled={field.options.length >= 100}><Plus /> 添加选项</button>
                </div>}
              </article>
            ))}
            <button type="button" className="secondary-button" onClick={addQuestionnaireField} disabled={form.questionnaireFields.length >= 100}><Plus /> 添加报名字段</button>
            {form.questionnaireFields.length === 0 && <p className="brand-hero-hint"><Info /> 不添加专属字段时，报名页仍会自动要求昵称和手机号，并在手机号旁提示“请确保手机号正确”。</p>}
          </div>
        </section>
        </fieldset>

        <fieldset className="editor-fieldset" disabled={submitting || uncertain || sessionCommitted}>
        <section className="form-section">
          <div className="form-title"><span>04</span><div><h2>第一个完整场次</h2><p>系统会在创建时检查时间、人数和地点设置。</p></div></div>
          <div className="field-grid">
            <label className="full">场次名称 <RequiredMark /><input value={form.sessionTitle} onChange={(event) => update("sessionTitle", event.target.value)} maxLength={200} placeholder="9 月 26 日晚场" required aria-required={true} /></label>
            <label>报名开始 <RequiredMark /><input type="datetime-local" value={form.registrationStartAt} onChange={(event) => update("registrationStartAt", event.target.value)} required aria-required={true} /></label>
            <label>报名截止 <RequiredMark /><input type="datetime-local" value={form.registrationEndAt} onChange={(event) => update("registrationEndAt", event.target.value)} required aria-required={true} /></label>
            <label>活动开始 <RequiredMark /><input type="datetime-local" value={form.sessionStartAt} onChange={(event) => update("sessionStartAt", event.target.value)} required aria-required={true} /></label>
            <label>活动结束 <RequiredMark /><input type="datetime-local" value={form.sessionEndAt} onChange={(event) => update("sessionEndAt", event.target.value)} required aria-required={true} /></label>
            <label>容量 <RequiredMark /><input type="number" min={2} max={maxPostgresInteger} value={form.capacity} onChange={(event) => update("capacity", event.target.valueAsNumber)} required aria-required={true} /></label>
            <label>成团人数 <RequiredMark /><input type="number" min={1} max={Math.min(maxPostgresInteger, form.capacity)} value={form.groupMinimum} onChange={(event) => update("groupMinimum", event.target.valueAsNumber)} required aria-required={true} /></label>
            <label>余量提醒 <RequiredMark /><input type="number" min={1} max={Math.min(maxPostgresInteger, Math.max(1, form.capacity - 1))} value={form.lowStockThreshold} onChange={(event) => update("lowStockThreshold", event.target.valueAsNumber)} required aria-required={true} /></label>
            <label>价格（元） <RequiredMark /><input inputMode="decimal" value={form.priceYuan} onChange={(event) => update("priceYuan", event.target.value)} pattern="^\d+(\.\d{1,2})?$" required aria-required={true} /></label>
            <label>参与方式 <span className="required-mark" aria-hidden="true">*</span><select value={form.deliveryMode} onChange={(event) => update("deliveryMode", event.target.value as FormState["deliveryMode"])} required={true} aria-required={true}><option value="offline">线下</option><option value="online">线上</option></select></label>
            {form.deliveryMode === "offline" ? <>
              <label>区域 <span className="required-mark" aria-hidden="true">*</span><select value={form.area} onChange={(event) => update("area", event.target.value)} required={true} aria-required={true}><option value="heping">和平区</option><option value="hexi">河西区</option><option value="nankai">南开区</option><option value="hebei">河北区</option><option value="hedong">河东区</option><option value="hongqiao">红桥区</option><option value="wuqing">武清区</option><option value="dongli">东丽区</option><option value="binhai">滨海新区</option></select></label>
              <label>场地名称 <RequiredMark /><input value={form.venueName} onChange={(event) => update("venueName", event.target.value)} maxLength={200} required aria-required={true} /></label>
              <label className="full">完整地址 <RequiredMark /><input value={form.address} onChange={(event) => update("address", event.target.value)} required aria-required={true} /></label>
              <label>经度 <RequiredMark /><input type="number" min={-180} max={180} step="any" value={form.longitude} onChange={(event) => update("longitude", event.target.value)} placeholder="117.20" required aria-required={true} /></label>
              <label>纬度 <RequiredMark /><input type="number" min={-90} max={90} step="any" value={form.latitude} onChange={(event) => update("latitude", event.target.value)} placeholder="39.08" required aria-required={true} /></label>
            </> : <>
              <label>参与方式说明 <RequiredMark /><input value={form.onlineMode} onChange={(event) => update("onlineMode", event.target.value)} maxLength={80} required aria-required={true} /></label>
              <label className="checkbox-field"><input type="checkbox" checked={form.onlineCompliant} onChange={(event) => update("onlineCompliant", event.target.checked)} required aria-required={true} /> 已确认线上参与方式符合发布要求 <RequiredMark /></label>
            </>}
          </div>
        </section>
        </fieldset>
        {form.additionalSessions.map((session, index) => (
          <fieldset className="editor-fieldset" key={`copy-session-${index}`}
            disabled={submitting || uncertain || index < additionalSessionCommittedCount}>
            <section className="form-section">
              <div className="form-title"><span>{String(index + 5).padStart(2, "0")}</span><div>
                <h2>第 {index + 2} 个场次</h2>
                <p>已复制原场次设置；新一期的四个时间必须重新填写。</p>
              </div></div>
              {committedStage === 0 && <button type="button" className="secondary-button" onClick={() => removeAdditionalSession(index)}><Trash2 /> 不复制此场次</button>}
              <div className="field-grid">
                <label className="full">场次名称 <RequiredMark /><input value={session.sessionTitle} maxLength={200} onChange={(event) => updateAdditionalSession(index, { sessionTitle: event.target.value })} required aria-required={true} /></label>
                <label>报名开始 <RequiredMark /><input type="datetime-local" value={session.registrationStartAt} onChange={(event) => updateAdditionalSession(index, { registrationStartAt: event.target.value })} required aria-required={true} /></label>
                <label>报名截止 <RequiredMark /><input type="datetime-local" value={session.registrationEndAt} onChange={(event) => updateAdditionalSession(index, { registrationEndAt: event.target.value })} required aria-required={true} /></label>
                <label>活动开始 <RequiredMark /><input type="datetime-local" value={session.sessionStartAt} onChange={(event) => updateAdditionalSession(index, { sessionStartAt: event.target.value })} required aria-required={true} /></label>
                <label>活动结束 <RequiredMark /><input type="datetime-local" value={session.sessionEndAt} onChange={(event) => updateAdditionalSession(index, { sessionEndAt: event.target.value })} required aria-required={true} /></label>
                <label>容量 <RequiredMark /><input type="number" min={2} max={maxPostgresInteger} value={session.capacity} onChange={(event) => updateAdditionalSession(index, { capacity: event.target.valueAsNumber })} required aria-required={true} /></label>
                <label>成团人数 <RequiredMark /><input type="number" min={1} max={session.capacity} value={session.groupMinimum} onChange={(event) => updateAdditionalSession(index, { groupMinimum: event.target.valueAsNumber })} required aria-required={true} /></label>
                <label>余量提醒 <RequiredMark /><input type="number" min={1} max={Math.max(1, session.capacity - 1)} value={session.lowStockThreshold} onChange={(event) => updateAdditionalSession(index, { lowStockThreshold: event.target.valueAsNumber })} required aria-required={true} /></label>
                <label>价格（元） <RequiredMark /><input inputMode="decimal" value={session.priceYuan} onChange={(event) => updateAdditionalSession(index, { priceYuan: event.target.value })} pattern="^\d+(\.\d{1,2})?$" required aria-required={true} /></label>
                <label>参与方式 <span className="required-mark" aria-hidden="true">*</span><select value={session.deliveryMode} onChange={(event) => updateAdditionalSession(index, { deliveryMode: event.target.value as ActivityCopySession["deliveryMode"] })} required={true} aria-required={true}><option value="offline">线下</option><option value="online">线上</option></select></label>
                {session.deliveryMode === "offline" ? <>
                  <label>区域 <span className="required-mark" aria-hidden="true">*</span><select value={session.area} onChange={(event) => updateAdditionalSession(index, { area: event.target.value })} required={true} aria-required={true}><option value="heping">和平区</option><option value="hexi">河西区</option><option value="nankai">南开区</option><option value="hebei">河北区</option><option value="hedong">河东区</option><option value="hongqiao">红桥区</option><option value="wuqing">武清区</option><option value="dongli">东丽区</option><option value="binhai">滨海新区</option></select></label>
                  <label>场地名称 <RequiredMark /><input value={session.venueName} maxLength={200} onChange={(event) => updateAdditionalSession(index, { venueName: event.target.value })} required aria-required={true} /></label>
                  <label className="full">完整地址 <RequiredMark /><input value={session.address} onChange={(event) => updateAdditionalSession(index, { address: event.target.value })} required aria-required={true} /></label>
                  <label>经度 <RequiredMark /><input type="number" min={-180} max={180} step="any" value={session.longitude} onChange={(event) => updateAdditionalSession(index, { longitude: event.target.value })} required aria-required={true} /></label>
                  <label>纬度 <RequiredMark /><input type="number" min={-90} max={90} step="any" value={session.latitude} onChange={(event) => updateAdditionalSession(index, { latitude: event.target.value })} required aria-required={true} /></label>
                </> : <>
                  <label>参与方式说明 <RequiredMark /><input value={session.onlineMode} maxLength={80} onChange={(event) => updateAdditionalSession(index, { onlineMode: event.target.value })} required aria-required={true} /></label>
                  <label className="checkbox-field"><input type="checkbox" checked={session.onlineCompliant} onChange={(event) => updateAdditionalSession(index, { onlineCompliant: event.target.checked })} required aria-required={true} /> 已确认线上参与方式符合发布要求 <RequiredMark /></label>
                </>}
              </div>
            </section>
          </fieldset>
        ))}
      </div>

      <aside className="editor-side-column">
        <section className="brand-preview-card activity-preview-card">
          <div className="preview-head"><b>小程序活动预览</b><span>未发布草稿</span></div>
          <div className="brand-phone-preview">
            {form.coverImageUrl ? (
              <div className="brand-preview-image activity-preview-cover" role="img" aria-label="活动封面预览" style={{ backgroundImage: `url(${JSON.stringify(resolveActivityImageUrl(form.coverImageUrl))})` }} />
            ) : (
              <div className="activity-preview-cover-fallback"><span>{form.activityType === "custom" ? "自定义活动" : "享玩活动"}</span></div>
            )}
            <div className="activity-preview-body">
              <div className="activity-preview-tags"><span>{form.deliveryMode === "online" ? "#线上" : "#线下"}</span>{previewTags.map((tag) => <span key={tag.key}>{tag.label}</span>)}</div>
              <h3>{generatedTitle}</h3>
              {form.activitySummary.trim() && <p className="activity-preview-summary">{form.activitySummary.trim()}</p>}
              <p>{form.sessionTitle.trim() ? `场次：${form.sessionTitle.trim()}${form.additionalSessions.length > 0 ? ` 等 ${form.additionalSessions.length + 1} 场` : ""}` : "场次时间待填写"}</p>
              <p>{form.deliveryMode === "online" ? (form.onlineMode.trim() || "线上参与") : (form.venueName.trim() || "活动地点待填写")}</p>
              <div className="activity-preview-footer"><b>{form.priceYuan.trim() ? `¥${form.priceYuan.trim()}` : "费用待填写"}</b><span>查看详情</span></div>
            </div>
          </div>
          <p>预览跟随当前表单变化；保存后仍是草稿，发布前可在详情页继续校对。</p>
        </section>
        <section className="publish-guide">
          <div className="preview-head"><b>发布前检查</b><span>自动核对</span></div>
          <ul><li><Check /> 完整活动层级</li><li><Check /> 有效报名与活动时间</li><li><Check /> 容量、价格和参与方式</li><li><Info /> 品牌内容和标签需已发布</li><li><Info /> 人物、问卷、资源如已关联也需就绪</li></ul>
          <p>本页只创建草稿。进入详情确认后，再执行独立发布操作。</p>
        </section>
      </aside>
      <div className="editor-actions">{uncertain ? <span className="secondary-button disabled-link" aria-disabled="true">取消</span> : <Link className="secondary-button" href="/activities">取消</Link>}<button className="primary-button" disabled={submitting || uploadingCover || copyLoading}><Send /> {submitting ? "正在创建…" : uploadingCover ? "正在上传封面…" : copyLoading ? "正在读取来源…" : uncertain ? "确认未知结果并重试" : form.additionalSessions.length > 0 ? `创建草稿与 ${form.additionalSessions.length + 1} 个场次` : "创建草稿与场次"}</button></div>
    </form>
  );
}
