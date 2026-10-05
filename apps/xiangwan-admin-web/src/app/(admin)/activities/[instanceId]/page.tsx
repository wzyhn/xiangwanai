"use client";

import { reviewLinkRequest } from "@/lib/review-link";
import { requestRequiredReason } from "@/lib/required-reason-dialog";
import { SessionCancellation } from "@/components/session-cancellation";

import {
  AlertTriangle,
  ArrowLeft,
  Calendar,
  CheckCircle2,
  ImagePlus,
  Info,
  MapPin,
  Pencil,
  Plus,
  Send,
  Trash2,
  Users,
  X,
} from "lucide-react";
import Link from "next/link";
import { useParams } from "next/navigation";
import { useCallback, useEffect, useRef, useState } from "react";
import {
  ApiError,
  api,
  apiBinaryUpload,
  definitiveFailure,
  displayError,
  operationHeaders,
  operationKey,
} from "@/lib/api";
import {
  DetailBlocksEditor,
  serializeDetailBlocks,
  validateDetailBlocks,
} from "@/components/detail-blocks-editor";
import {
  activityImageAccept,
  activityImageMaxBytes,
  isActivityImageRef,
  resolveActivityImageUrl,
  uploadActivityImage,
} from "@/lib/images";
import { formatShanghaiDateTime, shanghaiLocalToISOString } from "@/lib/time";
import {
  completionRecoveryPrompt,
  parseCompletionRecovery,
  type CompletionRecovery,
} from "@/lib/completion-recovery";
import { questionnaireFieldTypeLabel } from "@/lib/questionnaire-labels";
import {
  moveReviewPhoto,
  removeReviewPhoto,
  selectedReviewPhotoURLs,
  type ReviewPhotoDraft,
} from "@/lib/review-photo-draft";
import {
  reviewMediaChoice,
  reviewMediaSHA256,
  type ReviewMediaChoice,
  type ReviewMediaKind,
} from "@/lib/review-media";
import {
  parseReviewResourceRecovery,
  type ReviewResourceRecovery,
} from "@/lib/review-resource-recovery";
import {
  allSessionsTerminal,
  canCompleteInstance,
  nextSessionLifecycleBoundaryMs,
} from "@/lib/session-lifecycle";
import type {
  InstanceDetail,
  InstanceDetailBlock,
  InstanceReviewStatus,
  Session,
} from "@/lib/types";

const statusLabel: Record<string, string> = {
  draft: "草稿",
  pending_publish: "待发布",
  published: "已发布",
  completed: "已结束",
  ended: "已结束",
  cancelled: "已取消",
  archived: "已归档",
};

const publishableStatuses = new Set(["draft", "pending_publish", "published"]);
const publishOperationPattern =
  /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const privateReviewMediaEnabled =
  process.env.NEXT_PUBLIC_XIANGWAN_PRIVATE_MEDIA_UPLOAD === "enabled";

type ReviewMediaUploadReceipt = {
  file_id: string;
  instance_id: string;
  kind: ReviewMediaKind;
  mime: string;
  size: number;
  sha256: string;
  upload_url: string;
  preview_url?: string;
  confirmed_at?: string;
};

type ReviewMediaSelection = {
  fileID: string;
  kind: ReviewMediaKind;
  sha256: string;
  localName: string;
  size: number;
  previewOpened: boolean;
  reviewed: boolean;
};

type PendingReviewMediaUpload = {
  file: File;
  choice: ReviewMediaChoice;
  sha256: string;
  operation: string;
};

function publicationViolationLabel(value: {
  Field?: string;
  field?: string;
  Code?: string;
  code?: string;
}): string {
  const field = (value.field || value.Field || "").toLowerCase();
  const code = (value.code || value.Code || "").toLowerCase();
  // references.* 的 field 挂在 sessions[N]. 前缀下,必须先于 "session" 匹配,
  // 否则「品牌内容未发布」会被误报成「请补全场次」(2026-09-19 生产事故)。
  if (field.includes("references.content")) return "请先在「首页配置」发布品牌内容";
  if (field.includes("references.quick_tags")) return "活动引用的主题标签尚未在「首页配置」发布";
  if (field.includes("references.questionnaire")) return "请检查报名问卷是否已发布";
  if (field.includes("references.people")) return "请检查关联人物资料是否完整";
  if (field.includes("references.resources")) return "请检查活动资料是否可用";
  if (field.includes("session") || code.includes("session"))
    return "请补全场次的时间、地点和人数设置";
  if (field.includes("questionnaire") || code.includes("questionnaire"))
    return "请检查报名问卷是否已发布";
  if (field.includes("person") || code.includes("person")) return "请检查关联人物资料是否完整";
  if (field.includes("resource") || code.includes("resource")) return "请检查活动资料是否可用";
  if (field.includes("tag") || code.includes("tag")) return "请检查活动标签是否已发布";
  return "请补全活动必填信息后再发布";
}

type PublicationRecovery = {
  version: 1;
  operation: string;
  expectedInstanceVersion: number;
};

type ScheduleRecovery = {
  version: 1;
  operation: string;
  expectedInstanceVersion: number;
  scheduledAt: string;
};

type ScheduledPublicationRecord = {
  id: string;
  instance_id: string;
  expected_instance_version: number;
  scheduled_at: string;
  status: "pending" | "processing" | "completed" | "failed" | "cancelled";
  attempt_count: number;
  lease_expires_at?: string;
  completed_at?: string;
  last_error?: string;
  version: number;
};

type ScheduleCancellationRecovery = {
  version: 1;
  operation: string;
  scheduleID: string;
};

type ArchiveRecovery = {
  version: 1;
  operation: string;
  expectedVersion: number;
};

function archiveRecoveryKey(target: string, id: string): string {
  return `xiangwan-admin:archive:${target}:${id}:v1`;
}

function readArchiveRecovery(target: string, id: string): ArchiveRecovery | null {
  if (typeof window === "undefined") return null;
  try {
    const raw = window.sessionStorage.getItem(archiveRecoveryKey(target, id));
    if (!raw) return null;
    const value: unknown = JSON.parse(raw);
    if (typeof value !== "object" || value === null || Array.isArray(value)) throw new Error();
    const candidate = value as Record<string, unknown>;
    if (
      candidate.version !== 1 ||
      typeof candidate.operation !== "string" ||
      !publishOperationPattern.test(candidate.operation) ||
      typeof candidate.expectedVersion !== "number" ||
      !Number.isSafeInteger(candidate.expectedVersion) ||
      candidate.expectedVersion < 1
    )
      throw new Error();
    return candidate as ArchiveRecovery;
  } catch {
    try {
      window.sessionStorage.removeItem(archiveRecoveryKey(target, id));
    } catch {
      /* storage may be unavailable */
    }
    return null;
  }
}

function writeArchiveRecovery(
  target: string,
  id: string,
  value: Omit<ArchiveRecovery, "version">,
): void {
  if (typeof window === "undefined") return;
  try {
    window.sessionStorage.setItem(
      archiveRecoveryKey(target, id),
      JSON.stringify({ version: 1, ...value } satisfies ArchiveRecovery),
    );
  } catch {
    /* the mounted page still retains the in-memory operation */
  }
}

function clearArchiveRecovery(target: string, id: string): void {
  if (typeof window === "undefined") return;
  try {
    window.sessionStorage.removeItem(archiveRecoveryKey(target, id));
  } catch {
    /* storage may be unavailable */
  }
}

// Completion is a state-changing admin command just like publication. Keep
// its operation key and version fence across a transport timeout so a retry
// can replay the same server receipt instead of sending a fresh key against
// an already-completed Instance.
function completionRecoveryKey(instanceId: string): string {
  return `xiangwan-admin:activity-completion:${instanceId}:v1`;
}

function readCompletionRecovery(instanceId: string): CompletionRecovery | null {
  if (typeof window === "undefined") return null;
  try {
    const raw = window.sessionStorage.getItem(completionRecoveryKey(instanceId));
    if (!raw) return null;
    const value: unknown = JSON.parse(raw);
    if (typeof value !== "object" || value === null || Array.isArray(value)) throw new Error();
    const parsed = parseCompletionRecovery(value);
    if (!parsed) throw new Error();
    return parsed;
  } catch {
    try {
      window.sessionStorage.removeItem(completionRecoveryKey(instanceId));
    } catch {
      // Storage may be unavailable in hardened browser modes.
    }
    return null;
  }
}

function writeCompletionRecovery(
  instanceId: string,
  value: Omit<CompletionRecovery, "version">,
): void {
  if (typeof window === "undefined") return;
  try {
    window.sessionStorage.setItem(
      completionRecoveryKey(instanceId),
      JSON.stringify({ version: 1, ...value } satisfies CompletionRecovery),
    );
  } catch {
    // The mounted page still retains the operation key when storage is unavailable.
  }
}

function clearCompletionRecovery(instanceId: string): void {
  if (typeof window === "undefined") return;
  try {
    window.sessionStorage.removeItem(completionRecoveryKey(instanceId));
  } catch {
    // Storage may be unavailable in hardened browser modes.
  }
}

function publicationRecoveryKey(instanceId: string): string {
  return `xiangwan-admin:activity-publication:${instanceId}:v1`;
}

function readPublicationRecovery(instanceId: string): PublicationRecovery | null {
  if (typeof window === "undefined") return null;
  try {
    const raw = window.sessionStorage.getItem(publicationRecoveryKey(instanceId));
    if (!raw) return null;
    const value: unknown = JSON.parse(raw);
    if (typeof value !== "object" || value === null || Array.isArray(value)) throw new Error();
    const candidate = value as Record<string, unknown>;
    if (
      candidate.version !== 1 ||
      typeof candidate.operation !== "string" ||
      !publishOperationPattern.test(candidate.operation) ||
      typeof candidate.expectedInstanceVersion !== "number" ||
      !Number.isSafeInteger(candidate.expectedInstanceVersion) ||
      candidate.expectedInstanceVersion < 1
    ) {
      throw new Error();
    }
    return candidate as PublicationRecovery;
  } catch {
    try {
      window.sessionStorage.removeItem(publicationRecoveryKey(instanceId));
    } catch {
      // Storage may be unavailable in hardened browser modes.
    }
    return null;
  }
}

function writePublicationRecovery(
  instanceId: string,
  value: Omit<PublicationRecovery, "version">,
): void {
  if (typeof window === "undefined") return;
  try {
    window.sessionStorage.setItem(
      publicationRecoveryKey(instanceId),
      JSON.stringify({ version: 1, ...value } satisfies PublicationRecovery),
    );
  } catch {
    // The mounted page still retains the operation evidence when storage is unavailable.
  }
}

function clearPublicationRecovery(instanceId: string): void {
  if (typeof window === "undefined") return;
  try {
    window.sessionStorage.removeItem(publicationRecoveryKey(instanceId));
  } catch {
    // Storage may be unavailable in hardened browser modes.
  }
}

function scheduleRecoveryKey(instanceId: string): string {
  return `xiangwan-admin:activity-publication-schedule:${instanceId}:v1`;
}

function readScheduleRecovery(instanceId: string): ScheduleRecovery | null {
  if (typeof window === "undefined") return null;
  try {
    const raw = window.sessionStorage.getItem(scheduleRecoveryKey(instanceId));
    if (!raw) return null;
    const value: unknown = JSON.parse(raw);
    if (typeof value !== "object" || value === null || Array.isArray(value)) throw new Error();
    const candidate = value as Record<string, unknown>;
    if (
      candidate.version !== 1 ||
      typeof candidate.operation !== "string" ||
      !publishOperationPattern.test(candidate.operation) ||
      typeof candidate.expectedInstanceVersion !== "number" ||
      !Number.isSafeInteger(candidate.expectedInstanceVersion) ||
      candidate.expectedInstanceVersion < 1 ||
      typeof candidate.scheduledAt !== "string" ||
      !Number.isFinite(new Date(candidate.scheduledAt).getTime())
    )
      throw new Error();
    return candidate as ScheduleRecovery;
  } catch {
    try {
      window.sessionStorage.removeItem(scheduleRecoveryKey(instanceId));
    } catch {
      /* unavailable */
    }
    return null;
  }
}

function writeScheduleRecovery(instanceId: string, value: Omit<ScheduleRecovery, "version">): void {
  if (typeof window === "undefined") return;
  try {
    window.sessionStorage.setItem(
      scheduleRecoveryKey(instanceId),
      JSON.stringify({ version: 1, ...value } satisfies ScheduleRecovery),
    );
  } catch {
    /* mounted page retains the in-memory operation */
  }
}

function clearScheduleRecovery(instanceId: string): void {
  if (typeof window === "undefined") return;
  try {
    window.sessionStorage.removeItem(scheduleRecoveryKey(instanceId));
  } catch {
    /* unavailable */
  }
}

function scheduleCancellationRecoveryKey(instanceId: string): string {
  return `xiangwan-admin:activity-publication-schedule-cancellation:${instanceId}:v1`;
}

function readScheduleCancellationRecovery(instanceId: string): ScheduleCancellationRecovery | null {
  if (typeof window === "undefined") return null;
  try {
    const raw = window.sessionStorage.getItem(scheduleCancellationRecoveryKey(instanceId));
    if (!raw) return null;
    const value: unknown = JSON.parse(raw);
    if (typeof value !== "object" || value === null || Array.isArray(value)) throw new Error();
    const candidate = value as Record<string, unknown>;
    if (
      candidate.version !== 1 ||
      typeof candidate.operation !== "string" ||
      !publishOperationPattern.test(candidate.operation) ||
      typeof candidate.scheduleID !== "string" ||
      !publishOperationPattern.test(candidate.scheduleID)
    )
      throw new Error();
    return candidate as ScheduleCancellationRecovery;
  } catch {
    try {
      window.sessionStorage.removeItem(scheduleCancellationRecoveryKey(instanceId));
    } catch {
      /* unavailable */
    }
    return null;
  }
}

function writeScheduleCancellationRecovery(
  instanceId: string,
  value: Omit<ScheduleCancellationRecovery, "version">,
): void {
  if (typeof window === "undefined") return;
  try {
    window.sessionStorage.setItem(
      scheduleCancellationRecoveryKey(instanceId),
      JSON.stringify({ version: 1, ...value } satisfies ScheduleCancellationRecovery),
    );
  } catch {
    /* mounted page retains the operation */
  }
}

function clearScheduleCancellationRecovery(instanceId: string): void {
  if (typeof window === "undefined") return;
  try {
    window.sessionStorage.removeItem(scheduleCancellationRecoveryKey(instanceId));
  } catch {
    /* unavailable */
  }
}

type CancellationImpact = {
  cancelledRegistrationCount: number;
  requestedRefundCents: number;
  couponAdjustmentCount: number;
};

type CancellationRecovery = {
  version: 1;
  stage: "preview" | "final";
  previewOperation: string;
  operation: string;
  reason: string;
  previewId?: string;
  expectedInstanceVersion?: number;
  impact?: CancellationImpact;
};

type SessionEditDraft = {
  title: string;
  registrationStartAt: string;
  registrationEndAt: string;
  sessionStartAt: string;
  sessionEndAt: string;
  capacity: string;
  groupMinimum: string;
  lowStockThreshold: string;
  priceCents: string;
  deliveryMode: "offline" | "online";
  area: string;
  venueName: string;
  address: string;
  longitude: string;
  latitude: string;
  onlineMode: string;
  onlineCompliant: boolean;
  sortOrder: string;
};

function sessionDateTimeInput(value?: string): string {
  if (!value) return "";
  const instant = new Date(value);
  if (!Number.isFinite(instant.getTime())) return "";
  const shanghai = new Date(instant.getTime() + 8 * 60 * 60 * 1000);
  const pad = (part: number) => String(part).padStart(2, "0");
  return `${shanghai.getUTCFullYear()}-${pad(shanghai.getUTCMonth() + 1)}-${pad(shanghai.getUTCDate())}T${pad(shanghai.getUTCHours())}:${pad(shanghai.getUTCMinutes())}`;
}

function shanghaiDateTimeInput(value?: string): string {
  if (!value) return "";
  const instant = new Date(value);
  if (!Number.isFinite(instant.getTime())) return "";
  const shanghai = new Date(instant.getTime() + 8 * 60 * 60 * 1000);
  const pad = (part: number) => String(part).padStart(2, "0");
  return `${shanghai.getUTCFullYear()}-${pad(shanghai.getUTCMonth() + 1)}-${pad(shanghai.getUTCDate())}T${pad(shanghai.getUTCHours())}:${pad(shanghai.getUTCMinutes())}`;
}

function sessionEditDraftFromSession(session: Session): SessionEditDraft {
  return {
    title: session.title,
    registrationStartAt: sessionDateTimeInput(session.registration_start_at),
    registrationEndAt: sessionDateTimeInput(session.registration_end_at),
    sessionStartAt: sessionDateTimeInput(session.session_start_at),
    sessionEndAt: sessionDateTimeInput(session.session_end_at),
    capacity: session.capacity === undefined ? "" : String(session.capacity),
    groupMinimum: session.group_minimum === undefined ? "" : String(session.group_minimum),
    lowStockThreshold:
      session.low_stock_threshold === undefined ? "" : String(session.low_stock_threshold),
    priceCents: session.price_cents === undefined ? "" : String(session.price_cents),
    deliveryMode: session.delivery_mode || "offline",
    area: session.area || (session.delivery_mode === "online" ? "online" : "heping"),
    venueName: session.venue_name || "",
    address: session.address || "",
    longitude: session.longitude === undefined ? "" : String(session.longitude),
    latitude: session.latitude === undefined ? "" : String(session.latitude),
    onlineMode: session.online_participation_mode || "",
    onlineCompliant: session.online_participation_compliant === true,
    sortOrder: String(session.sort_order),
  };
}

function newSessionDraftFromSessions(sessions: Session[]): SessionEditDraft {
  const previous =
    sessions.length > 0
      ? sessionEditDraftFromSession(sessions[sessions.length - 1])
      : {
          title: "",
          registrationStartAt: "",
          registrationEndAt: "",
          sessionStartAt: "",
          sessionEndAt: "",
          capacity: "",
          groupMinimum: "",
          lowStockThreshold: "",
          priceCents: "0",
          deliveryMode: "offline" as const,
          area: "heping",
          venueName: "",
          address: "",
          longitude: "",
          latitude: "",
          onlineMode: "",
          onlineCompliant: false,
          sortOrder: "0",
        };
  return {
    ...previous,
    title: previous.title.trim() ? `${previous.title.trim()}（新增）` : "新场次",
    sortOrder: String(sessions.length),
  };
}

type SessionEditorProps = {
  draft: SessionEditDraft;
  disabled: boolean;
  saveLabel: string;
  onChange: (patch: Partial<SessionEditDraft>) => void;
  onCancel: () => void;
  onSave: () => void;
};

function SessionEditor({
  draft,
  disabled,
  saveLabel,
  onChange,
  onCancel,
  onSave,
}: SessionEditorProps) {
  return (
    <div
      style={{
        marginTop: 14,
        padding: 14,
        border: "1px solid var(--line)",
        borderRadius: 8,
        background: "#fbfcfe",
      }}
    >
      <div className="field-grid">
        <label className="full">
          场次名称 <span className="required-mark">*</span>
          <input
            value={draft.title}
            disabled={disabled}
            maxLength={200}
            onChange={(event) => onChange({ title: event.target.value })}
            required={true}
            aria-required={true}
          />
        </label>
        <label>
          报名开始 <span className="required-mark">*</span>
          <input
            type="datetime-local"
            value={draft.registrationStartAt}
            disabled={disabled}
            onChange={(event) => onChange({ registrationStartAt: event.target.value })}
            required={true}
            aria-required={true}
          />
        </label>
        <label>
          报名截止 <span className="required-mark">*</span>
          <input
            type="datetime-local"
            value={draft.registrationEndAt}
            disabled={disabled}
            onChange={(event) => onChange({ registrationEndAt: event.target.value })}
            required={true}
            aria-required={true}
          />
        </label>
        <label>
          活动开始 <span className="required-mark">*</span>
          <input
            type="datetime-local"
            value={draft.sessionStartAt}
            disabled={disabled}
            onChange={(event) => onChange({ sessionStartAt: event.target.value })}
            required={true}
            aria-required={true}
          />
        </label>
        <label>
          活动结束 <span className="required-mark">*</span>
          <input
            type="datetime-local"
            value={draft.sessionEndAt}
            disabled={disabled}
            onChange={(event) => onChange({ sessionEndAt: event.target.value })}
            required={true}
            aria-required={true}
          />
        </label>
        <label>
          容量 <span className="required-mark">*</span>
          <input
            type="number"
            min={1}
            value={draft.capacity}
            disabled={disabled}
            onChange={(event) => onChange({ capacity: event.target.value })}
            required={true}
            aria-required={true}
          />
        </label>
        <label>
          成团人数 <span className="required-mark">*</span>
          <input
            type="number"
            min={1}
            value={draft.groupMinimum}
            disabled={disabled}
            onChange={(event) => onChange({ groupMinimum: event.target.value })}
            required={true}
            aria-required={true}
          />
        </label>
        <label>
          余量提醒 <span className="required-mark">*</span>
          <input
            type="number"
            min={1}
            value={draft.lowStockThreshold}
            disabled={disabled}
            onChange={(event) => onChange({ lowStockThreshold: event.target.value })}
            required={true}
            aria-required={true}
          />
        </label>
        <label>
          价格（分） <span className="required-mark">*</span>
          <input
            type="number"
            min={0}
            value={draft.priceCents}
            disabled={disabled}
            onChange={(event) => onChange({ priceCents: event.target.value })}
            required={true}
            aria-required={true}
          />
        </label>
        <label>
          参与方式{" "}
          <span className="required-mark" aria-hidden="true">
            *
          </span>{" "}
          <select
            value={draft.deliveryMode}
            disabled={disabled}
            onChange={(event) => {
              const deliveryMode = event.target.value as SessionEditDraft["deliveryMode"];
              onChange({ deliveryMode, area: deliveryMode === "online" ? "online" : draft.area });
            }}
            required={true}
            aria-required={true}
          >
            <option value="offline">线下</option>
            <option value="online">线上</option>
          </select>
        </label>
        <label>
          排序{" "}
          <span className="required-mark" aria-hidden="true">
            *
          </span>{" "}
          <input
            type="number"
            min={0}
            value={draft.sortOrder}
            disabled={disabled}
            onChange={(event) => onChange({ sortOrder: event.target.value })}
            required={true}
            aria-required={true}
          />
        </label>
        {draft.deliveryMode === "offline" ? (
          <>
            <label>
              区域{" "}
              <span className="required-mark" aria-hidden="true">
                *
              </span>{" "}
              <select
                value={draft.area}
                disabled={disabled}
                onChange={(event) => onChange({ area: event.target.value })}
                required={true}
                aria-required={true}
              >
                <option value="heping">和平区</option>
                <option value="hexi">河西区</option>
                <option value="hebei">河北区</option>
                <option value="nankai">南开区</option>
                <option value="hongqiao">红桥区</option>
                <option value="hedong">河东区</option>
                <option value="wuqing">武清区</option>
                <option value="dongli">东丽区</option>
                <option value="binhai">滨海新区</option>
              </select>
            </label>
            <label>
              场地名称 <span className="required-mark">*</span>
              <input
                value={draft.venueName}
                disabled={disabled}
                maxLength={200}
                onChange={(event) => onChange({ venueName: event.target.value })}
                required={true}
                aria-required={true}
              />
            </label>
            <label className="full">
              完整地址 <span className="required-mark">*</span>
              <input
                value={draft.address}
                disabled={disabled}
                onChange={(event) => onChange({ address: event.target.value })}
                required={true}
                aria-required={true}
              />
            </label>
            <label>
              经度 <span className="required-mark">*</span>
              <input
                type="number"
                step="any"
                min={-180}
                max={180}
                value={draft.longitude}
                disabled={disabled}
                onChange={(event) => onChange({ longitude: event.target.value })}
                required={true}
                aria-required={true}
              />
            </label>
            <label>
              纬度 <span className="required-mark">*</span>
              <input
                type="number"
                step="any"
                min={-90}
                max={90}
                value={draft.latitude}
                disabled={disabled}
                onChange={(event) => onChange({ latitude: event.target.value })}
                required={true}
                aria-required={true}
              />
            </label>
          </>
        ) : (
          <>
            <label>
              参与方式说明 <span className="required-mark">*</span>
              <input
                value={draft.onlineMode}
                disabled={disabled}
                maxLength={80}
                onChange={(event) => onChange({ onlineMode: event.target.value })}
                required={true}
                aria-required={true}
              />
            </label>
            <label className="checkbox-field">
              <input
                type="checkbox"
                checked={draft.onlineCompliant}
                disabled={disabled}
                onChange={(event) => onChange({ onlineCompliant: event.target.checked })}
                required={true}
                aria-required={true}
              />{" "}
              已确认线上参与方式符合发布要求 <span className="required-mark">*</span>
            </label>
          </>
        )}
      </div>
      <div className="panel-actions" style={{ marginTop: 12 }}>
        <button type="button" className="secondary-button" onClick={onCancel} disabled={disabled}>
          取消
        </button>
        <button type="button" className="primary-button" onClick={onSave} disabled={disabled}>
          {disabled ? "正在保存…" : saveLabel}
        </button>
      </div>
    </div>
  );
}

type SessionWritePayload = {
  title: string;
  registration_start_at: string;
  registration_end_at: string;
  session_start_at: string;
  session_end_at: string;
  capacity: number;
  group_minimum: number;
  low_stock_threshold: number;
  price_cents: number;
  delivery_mode: SessionEditDraft["deliveryMode"];
  area: string;
  venue_name: string;
  address: string;
  longitude: number | null;
  latitude: number | null;
  online_participation_mode: string;
  online_compliant: boolean;
  sort_order: number;
};

function sessionWritePayloadFromDraft(draft: SessionEditDraft): {
  payload?: SessionWritePayload;
  error?: string;
} {
  const registrationStartAt = shanghaiLocalToISOString(draft.registrationStartAt);
  const registrationEndAt = shanghaiLocalToISOString(draft.registrationEndAt);
  const sessionStartAt = shanghaiLocalToISOString(draft.sessionStartAt);
  const sessionEndAt = shanghaiLocalToISOString(draft.sessionEndAt);
  const capacity = Number(draft.capacity);
  const groupMinimum = Number(draft.groupMinimum);
  const lowStockThreshold = Number(draft.lowStockThreshold);
  const priceCents = Number(draft.priceCents);
  const sortOrder = Number(draft.sortOrder);
  if (
    !registrationStartAt ||
    !registrationEndAt ||
    !sessionStartAt ||
    !sessionEndAt ||
    !draft.capacity.trim() ||
    !draft.groupMinimum.trim() ||
    !draft.lowStockThreshold.trim() ||
    !draft.priceCents.trim() ||
    !draft.sortOrder.trim() ||
    !Number.isSafeInteger(capacity) ||
    !Number.isSafeInteger(groupMinimum) ||
    !Number.isSafeInteger(lowStockThreshold) ||
    !Number.isSafeInteger(priceCents) ||
    !Number.isSafeInteger(sortOrder) ||
    !draft.title.trim()
  ) {
    return { error: "请完整填写场次标题、时间、人数、价格和排序。时间按北京时间填写。" };
  }
  if (
    draft.deliveryMode === "offline" &&
    (!draft.area ||
      !draft.venueName.trim() ||
      !draft.address.trim() ||
      !draft.longitude.trim() ||
      !draft.latitude.trim() ||
      !Number.isFinite(Number(draft.longitude)) ||
      !Number.isFinite(Number(draft.latitude)))
  ) {
    return { error: "线下场次需要填写区域、场地、地址和经纬度。" };
  }
  if (draft.deliveryMode === "online" && (!draft.onlineMode.trim() || !draft.onlineCompliant)) {
    return { error: "线上场次需要填写参与方式，并确认符合发布要求。" };
  }
  return {
    payload: {
      title: draft.title.trim(),
      registration_start_at: registrationStartAt,
      registration_end_at: registrationEndAt,
      session_start_at: sessionStartAt,
      session_end_at: sessionEndAt,
      capacity,
      group_minimum: groupMinimum,
      low_stock_threshold: lowStockThreshold,
      price_cents: priceCents,
      delivery_mode: draft.deliveryMode,
      area: draft.deliveryMode === "online" ? "online" : draft.area,
      venue_name: draft.deliveryMode === "offline" ? draft.venueName.trim() : "",
      address: draft.deliveryMode === "offline" ? draft.address.trim() : "",
      longitude: draft.deliveryMode === "offline" ? Number(draft.longitude) : null,
      latitude: draft.deliveryMode === "offline" ? Number(draft.latitude) : null,
      online_participation_mode: draft.deliveryMode === "online" ? draft.onlineMode.trim() : "",
      online_compliant: draft.deliveryMode === "online" ? draft.onlineCompliant : false,
      sort_order: sortOrder,
    },
  };
}

function sessionUpdateRecoveryKey(instanceId: string, sessionId: string): string {
  return `xiangwan-admin:session-update:${instanceId}:${sessionId}:v1`;
}

function readSessionUpdateRecovery(instanceId: string, sessionId: string): string | null {
  if (typeof window === "undefined") return null;
  try {
    const raw = window.sessionStorage.getItem(sessionUpdateRecoveryKey(instanceId, sessionId));
    if (!raw) return null;
    const value: unknown = JSON.parse(raw);
    if (typeof value !== "object" || value === null || Array.isArray(value)) throw new Error();
    const candidate = value as Record<string, unknown>;
    if (
      candidate.version !== 1 ||
      typeof candidate.operation !== "string" ||
      !publishOperationPattern.test(candidate.operation)
    )
      throw new Error();
    return candidate.operation;
  } catch {
    try {
      window.sessionStorage.removeItem(sessionUpdateRecoveryKey(instanceId, sessionId));
    } catch {
      /* storage may be unavailable */
    }
    return null;
  }
}

function writeSessionUpdateRecovery(
  instanceId: string,
  sessionId: string,
  operation: string,
): void {
  if (typeof window === "undefined") return;
  try {
    window.sessionStorage.setItem(
      sessionUpdateRecoveryKey(instanceId, sessionId),
      JSON.stringify({ version: 1, operation }),
    );
  } catch {
    /* the in-memory key still fences the mounted page */
  }
}

function clearSessionUpdateRecovery(instanceId: string, sessionId: string): void {
  if (typeof window === "undefined") return;
  try {
    window.sessionStorage.removeItem(sessionUpdateRecoveryKey(instanceId, sessionId));
  } catch {
    /* storage may be unavailable */
  }
}

function sessionCreateRecoveryKey(instanceId: string): string {
  return `xiangwan-admin:session-create:${instanceId}:v1`;
}

type SessionCreateRecovery = {
  version: 1;
  operation: string;
  draft: SessionEditDraft;
};

function readSessionCreateRecovery(instanceId: string): SessionCreateRecovery | null {
  if (typeof window === "undefined") return null;
  try {
    const raw = window.sessionStorage.getItem(sessionCreateRecoveryKey(instanceId));
    if (!raw) return null;
    const value: unknown = JSON.parse(raw);
    if (typeof value !== "object" || value === null || Array.isArray(value)) throw new Error();
    const candidate = value as Record<string, unknown>;
    if (
      candidate.version !== 1 ||
      typeof candidate.operation !== "string" ||
      !publishOperationPattern.test(candidate.operation) ||
      typeof candidate.draft !== "object" ||
      candidate.draft === null ||
      Array.isArray(candidate.draft)
    )
      throw new Error();
    const draft = candidate.draft as Record<string, unknown>;
    const stringKeys = [
      "title",
      "registrationStartAt",
      "registrationEndAt",
      "sessionStartAt",
      "sessionEndAt",
      "capacity",
      "groupMinimum",
      "lowStockThreshold",
      "priceCents",
      "area",
      "venueName",
      "address",
      "longitude",
      "latitude",
      "onlineMode",
      "sortOrder",
    ] as const;
    if (
      stringKeys.some((key) => typeof draft[key] !== "string") ||
      (draft.deliveryMode !== "offline" && draft.deliveryMode !== "online") ||
      typeof draft.onlineCompliant !== "boolean"
    )
      throw new Error();
    return {
      version: 1,
      operation: candidate.operation,
      draft: draft as unknown as SessionEditDraft,
    };
  } catch {
    try {
      window.sessionStorage.removeItem(sessionCreateRecoveryKey(instanceId));
    } catch {
      /* storage may be unavailable */
    }
    return null;
  }
}

function writeSessionCreateRecovery(
  instanceId: string,
  value: Omit<SessionCreateRecovery, "version">,
): void {
  if (typeof window === "undefined") return;
  try {
    window.sessionStorage.setItem(
      sessionCreateRecoveryKey(instanceId),
      JSON.stringify({ version: 1, ...value } satisfies SessionCreateRecovery),
    );
  } catch {
    /* the mounted page still retains the in-memory key */
  }
}

function clearSessionCreateRecovery(instanceId: string): void {
  if (typeof window === "undefined") return;
  try {
    window.sessionStorage.removeItem(sessionCreateRecoveryKey(instanceId));
  } catch {
    /* storage may be unavailable */
  }
}

function reviewRecoveryKey(instanceId: string): string {
  return `xiangwan-admin:activity-review:${instanceId}:v2`;
}

function readReviewRecovery(instanceId: string): ReviewResourceRecovery | null {
  if (typeof window === "undefined") return null;
  try {
    const raw = window.sessionStorage.getItem(reviewRecoveryKey(instanceId));
    if (!raw) return null;
    const recovery = parseReviewResourceRecovery(JSON.parse(raw));
    if (!recovery) throw new Error();
    return recovery;
  } catch {
    try {
      window.sessionStorage.removeItem(reviewRecoveryKey(instanceId));
    } catch {
      /* hardened browser storage */
    }
    return null;
  }
}

function writeReviewRecovery(instanceId: string, recovery: ReviewResourceRecovery): void {
  if (typeof window === "undefined") return;
  try {
    window.sessionStorage.setItem(reviewRecoveryKey(instanceId), JSON.stringify(recovery));
  } catch {
    /* page keeps the in-memory request */
  }
}

function clearReviewRecovery(instanceId: string): void {
  if (typeof window === "undefined") return;
  try {
    window.sessionStorage.removeItem(reviewRecoveryKey(instanceId));
  } catch {
    /* storage may be unavailable */
  }
}

function sessionReviewRecoveryKey(instanceId: string, sessionId: string): string {
  return "xiangwan-admin:session-review:" + instanceId + ":" + sessionId + ":v1";
}

function readSessionReviewRecovery(instanceId: string, sessionId: string): string | null {
  if (typeof window === "undefined") return null;
  const key = sessionReviewRecoveryKey(instanceId, sessionId);
  try {
    const raw = window.sessionStorage.getItem(key);
    if (!raw) return null;
    const value: unknown = JSON.parse(raw);
    if (typeof value !== "object" || value === null || Array.isArray(value)) throw new Error();
    const candidate = value as Record<string, unknown>;
    if (
      candidate.version !== 1 ||
      typeof candidate.operation !== "string" ||
      !publishOperationPattern.test(candidate.operation)
    )
      throw new Error();
    return candidate.operation;
  } catch {
    try {
      window.sessionStorage.removeItem(key);
    } catch {
      /* hardened browser storage */
    }
    return null;
  }
}

function writeSessionReviewRecovery(
  instanceId: string,
  sessionId: string,
  operation: string,
): void {
  if (typeof window === "undefined") return;
  try {
    window.sessionStorage.setItem(
      sessionReviewRecoveryKey(instanceId, sessionId),
      JSON.stringify({ version: 1, operation }),
    );
  } catch {
    /* page keeps the in-memory key */
  }
}

function clearSessionReviewRecovery(instanceId: string, sessionId: string): void {
  if (typeof window === "undefined") return;
  try {
    window.sessionStorage.removeItem(sessionReviewRecoveryKey(instanceId, sessionId));
  } catch {
    /* storage may be unavailable */
  }
}

const cancellationRecoveryUUIDPattern =
  /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

function cancellationRecoveryKey(instanceId: string): string {
  return `xiangwan-admin:activity-cancellation:${instanceId}:v1`;
}

function readCancellationRecovery(instanceId: string): CancellationRecovery | null {
  if (typeof window === "undefined") return null;
  try {
    const raw = window.sessionStorage.getItem(cancellationRecoveryKey(instanceId));
    if (!raw) return null;
    const value: unknown = JSON.parse(raw);
    if (typeof value !== "object" || value === null || Array.isArray(value)) throw new Error();
    const candidate = value as Record<string, unknown>;
    if (
      candidate.version !== 1 ||
      (candidate.stage !== "preview" && candidate.stage !== "final") ||
      typeof candidate.previewOperation !== "string" ||
      !publishOperationPattern.test(candidate.previewOperation) ||
      typeof candidate.operation !== "string" ||
      !publishOperationPattern.test(candidate.operation) ||
      typeof candidate.reason !== "string" ||
      candidate.reason.trim() !== candidate.reason ||
      candidate.reason.length === 0 ||
      candidate.reason.length > 500
    )
      throw new Error();
    if (
      candidate.previewId !== undefined &&
      (typeof candidate.previewId !== "string" ||
        !cancellationRecoveryUUIDPattern.test(candidate.previewId))
    ) {
      throw new Error();
    }
    if (
      candidate.expectedInstanceVersion !== undefined &&
      (typeof candidate.expectedInstanceVersion !== "number" ||
        !Number.isSafeInteger(candidate.expectedInstanceVersion) ||
        candidate.expectedInstanceVersion < 1)
    )
      throw new Error();
    let impact: CancellationImpact | undefined;
    if (candidate.impact !== undefined) {
      if (
        typeof candidate.impact !== "object" ||
        candidate.impact === null ||
        Array.isArray(candidate.impact)
      )
        throw new Error();
      const rawImpact = candidate.impact as Record<string, unknown>;
      if (
        typeof rawImpact.cancelledRegistrationCount !== "number" ||
        !Number.isSafeInteger(rawImpact.cancelledRegistrationCount) ||
        rawImpact.cancelledRegistrationCount < 0 ||
        typeof rawImpact.requestedRefundCents !== "number" ||
        !Number.isSafeInteger(rawImpact.requestedRefundCents) ||
        rawImpact.requestedRefundCents < 0 ||
        typeof rawImpact.couponAdjustmentCount !== "number" ||
        !Number.isSafeInteger(rawImpact.couponAdjustmentCount) ||
        rawImpact.couponAdjustmentCount < 0
      )
        throw new Error();
      impact = {
        cancelledRegistrationCount: rawImpact.cancelledRegistrationCount,
        requestedRefundCents: rawImpact.requestedRefundCents,
        couponAdjustmentCount: rawImpact.couponAdjustmentCount,
      };
    }
    if (
      candidate.stage === "final" &&
      (typeof candidate.previewId !== "string" ||
        candidate.expectedInstanceVersion === undefined ||
        impact === undefined)
    )
      throw new Error();
    return {
      version: 1,
      stage: candidate.stage,
      previewOperation: candidate.previewOperation,
      operation: candidate.operation,
      reason: candidate.reason,
      previewId: candidate.previewId as string | undefined,
      expectedInstanceVersion: candidate.expectedInstanceVersion as number | undefined,
      impact,
    };
  } catch {
    try {
      window.sessionStorage.removeItem(cancellationRecoveryKey(instanceId));
    } catch {
      // Storage may be unavailable in hardened browser modes.
    }
    return null;
  }
}

function writeCancellationRecovery(
  instanceId: string,
  value: Omit<CancellationRecovery, "version">,
): void {
  if (typeof window === "undefined") return;
  try {
    window.sessionStorage.setItem(
      cancellationRecoveryKey(instanceId),
      JSON.stringify({ version: 1, ...value } satisfies CancellationRecovery),
    );
  } catch {
    // The mounted page still retains the operation evidence when storage is unavailable.
  }
}

function clearCancellationRecovery(instanceId: string): void {
  if (typeof window === "undefined") return;
  try {
    window.sessionStorage.removeItem(cancellationRecoveryKey(instanceId));
  } catch {
    // Storage may be unavailable in hardened browser modes.
  }
}

export default function ActivityDetailPage() {
  const params = useParams<{ instanceId: string }>();
  const [detail, setDetail] = useState<InstanceDetail | null>(null);
  const [reviewStatus, setReviewStatus] = useState<InstanceReviewStatus | null>(null);
  const [reviewStatusLoading, setReviewStatusLoading] = useState(true);
  const [reviewStatusError, setReviewStatusError] = useState("");
  const [copiedReviewPath, setCopiedReviewPath] = useState("");
  const [reviewTitle, setReviewTitle] = useState("本期视频回顾");
  const [reviewDescription, setReviewDescription] = useState("");
  const [reviewVideoURL, setReviewVideoURL] = useState("");
  const [reviewFinderUserName, setReviewFinderUserName] = useState("");
  const [reviewFeedID, setReviewFeedID] = useState("");
  const [reviewPhotoRows, setReviewPhotoRows] = useState<ReviewPhotoDraft[]>([]);
  const [reviewFiles, setReviewFiles] = useState<ReviewMediaSelection[]>([]);
  const [uploadingReviewFile, setUploadingReviewFile] = useState(false);
  const [hashingReviewFile, setHashingReviewFile] = useState(false);
  const [pendingReviewFileReady, setPendingReviewFileReady] = useState(false);
  const [reviewSubmitPending, setReviewSubmitPending] = useState(false);
  const [reviewRecording, setReviewRecording] = useState({
    added: false,
    enabled: false,
    title: "录音梳理",
    subtitle: "",
    url: "",
  });
  const [reviewMaterials, setReviewMaterials] = useState({
    added: false,
    enabled: false,
    title: "活动资料",
    subtitle: "",
    url: "",
  });
  const [savingReview, setSavingReview] = useState(false);
  const [sessionReviewDrafts, setSessionReviewDrafts] = useState<
    Record<
      string,
      {
        title: string;
        description: string;
        videoURL: string;
      }
    >
  >({});
  const [savingSessionReviewId, setSavingSessionReviewId] = useState("");
  const [editingSessionId, setEditingSessionId] = useState("");
  const [creatingSession, setCreatingSession] = useState(false);
  const [sessionEditDraft, setSessionEditDraft] = useState<SessionEditDraft | null>(null);
  const [savingSession, setSavingSession] = useState(false);
  const [loading, setLoading] = useState(true);
  const [publishing, setPublishing] = useState(false);
  const [scheduledAtLocal, setScheduledAtLocal] = useState("");
  const [scheduling, setScheduling] = useState(false);
  const [scheduleRetryPending, setScheduleRetryPending] = useState(false);
  const [scheduledPublications, setScheduledPublications] = useState<ScheduledPublicationRecord[]>(
    [],
  );
  const [cancellingSchedule, setCancellingSchedule] = useState(false);
  const [completing, setCompleting] = useState(false);
  const [cancelling, setCancelling] = useState(false);
  const [archivingTarget, setArchivingTarget] = useState("");
  const [lifecycleNowMs, setLifecycleNowMs] = useState(() => Date.now());
  const [message, setMessage] = useState("");
  const [violations, setViolations] = useState<string[]>([]);
  const [editingContent, setEditingContent] = useState(false);
  const [editCoverUrl, setEditCoverUrl] = useState("");
  const [editBlocks, setEditBlocks] = useState<InstanceDetailBlock[]>([]);
  const [savingContent, setSavingContent] = useState(false);
  const [uploadingEditCover, setUploadingEditCover] = useState(false);
  const editCoverFileInput = useRef<HTMLInputElement | null>(null);
  const publishOperation = useRef<string | null>(null);
  const publishExpectedInstanceVersion = useRef<number | null>(null);
  const scheduleOperation = useRef<string | null>(null);
  const scheduleExpectedInstanceVersion = useRef<number | null>(null);
  const scheduleAt = useRef<string | null>(null);
  const scheduleCancellationOperation = useRef<string | null>(null);
  const scheduleCancellationID = useRef<string | null>(null);
  const completionOperation = useRef<string | null>(null);
  const completionExpectedInstanceVersion = useRef<number | null>(null);
  const instanceArchiveOperation = useRef<string | null>(null);
  const sessionArchiveOperations = useRef<Record<string, string>>({});
  const cancellationPreviewOperation = useRef<string | null>(null);
  const cancellationOperation = useRef<string | null>(null);
  const reviewOperation = useRef<string | null>(null);
  const reviewRecovery = useRef<ReviewResourceRecovery | null>(null);
  const reviewFileInput = useRef<HTMLInputElement | null>(null);
  const pendingReviewFile = useRef<PendingReviewMediaUpload | null>(null);
  const reviewPhotoSequence = useRef(0);
  const sessionReviewOperations = useRef<Record<string, string>>({});
  const sessionUpdateOperations = useRef<Record<string, string>>({});
  const sessionCreateOperation = useRef<string | null>(null);
  const loadRequest = useRef<AbortController | null>(null);

  const load = useCallback(async () => {
    loadRequest.current?.abort();
    const request = new AbortController();
    loadRequest.current = request;
    setLoading(true);
    setReviewStatusLoading(true);
    setReviewStatusError("");
    setReviewStatus(null);
    setCopiedReviewPath("");
    try {
      const pending = readPublicationRecovery(params.instanceId);
      const pendingCompletion = readCompletionRecovery(params.instanceId);
      const pendingCancellation = readCancellationRecovery(params.instanceId);
      const pendingInstanceArchive = readArchiveRecovery("instance", params.instanceId);
      publishOperation.current = pending?.operation || null;
      publishExpectedInstanceVersion.current = pending?.expectedInstanceVersion || null;
      const pendingSchedule = readScheduleRecovery(params.instanceId);
      const pendingScheduleCancellation = readScheduleCancellationRecovery(params.instanceId);
      scheduleOperation.current = pendingSchedule?.operation || null;
      scheduleExpectedInstanceVersion.current = pendingSchedule?.expectedInstanceVersion || null;
      scheduleAt.current = pendingSchedule?.scheduledAt || null;
      setScheduleRetryPending(Boolean(pendingSchedule));
      scheduleCancellationOperation.current = pendingScheduleCancellation?.operation || null;
      scheduleCancellationID.current = pendingScheduleCancellation?.scheduleID || null;
      completionOperation.current = pendingCompletion?.operation || null;
      completionExpectedInstanceVersion.current =
        pendingCompletion?.expectedInstanceVersion || null;
      instanceArchiveOperation.current = pendingInstanceArchive?.operation || null;
      cancellationPreviewOperation.current = pendingCancellation?.previewOperation || null;
      cancellationOperation.current = pendingCancellation?.operation || null;
      reviewRecovery.current = readReviewRecovery(params.instanceId);
      reviewOperation.current = reviewRecovery.current?.operation || null;
      setReviewSubmitPending(Boolean(reviewOperation.current));
      const pendingSessionCreate = readSessionCreateRecovery(params.instanceId);
      sessionCreateOperation.current = pendingSessionCreate?.operation || null;
      const [detailResult, reviewResult, scheduleResult] = await Promise.allSettled([
        api<InstanceDetail>(`/instances/${params.instanceId}`, { signal: request.signal }),
        api<InstanceReviewStatus>(`/instances/${params.instanceId}/review-status`, {
          signal: request.signal,
        }),
        api<{ items: ScheduledPublicationRecord[] }>(
          `/instances/${params.instanceId}/publication-schedules`,
          { signal: request.signal },
        ),
      ]);
      if (request.signal.aborted) return;
      if (detailResult.status === "rejected") throw detailResult.reason;
      const value = detailResult.value;
      setDetail(value);
      setScheduledPublications(
        scheduleResult.status === "fulfilled" ? scheduleResult.value.items || [] : [],
      );
      setScheduledAtLocal(
        scheduleAt.current
          ? shanghaiDateTimeInput(scheduleAt.current)
          : shanghaiDateTimeInput(value.instance.scheduled_at),
      );
      setLifecycleNowMs(Date.now());
      if (pendingInstanceArchive && value.instance.status === "archived") {
        clearArchiveRecovery("instance", params.instanceId);
        instanceArchiveOperation.current = null;
        setMessage("本期已归档，历史发布和报名事实保持不变。");
      }
      let recoveredSessionArchive = false;
      let pendingSessionArchiveExists = false;
      for (const session of value.sessions) {
        const target = "session:" + session.id;
        const pendingSessionArchive = readArchiveRecovery(target, session.id);
        if (pendingSessionArchive && session.status === "archived") {
          clearArchiveRecovery(target, session.id);
          delete sessionArchiveOperations.current[session.id];
          recoveredSessionArchive = true;
          setMessage("场次已归档，历史报名和回顾事实保持不变。");
        } else if (pendingSessionArchive && session.status === "ended") {
          pendingSessionArchiveExists = true;
          sessionArchiveOperations.current[session.id] = pendingSessionArchive.operation;
        } else if (pendingSessionArchive) {
          clearArchiveRecovery(target, session.id);
          delete sessionArchiveOperations.current[session.id];
        }
      }
      if (pendingSessionCreate && value.instance.status === "draft") {
        setCreatingSession(true);
        setEditingSessionId("");
        setSessionEditDraft(pendingSessionCreate.draft);
      } else if (pendingSessionCreate) {
        clearSessionCreateRecovery(params.instanceId);
        sessionCreateOperation.current = null;
      }
      if (pendingCancellation && value.instance.status === "cancelled") {
        clearCancellationRecovery(params.instanceId);
        cancellationPreviewOperation.current = null;
        cancellationOperation.current = null;
        setMessage("活动已下架，报名、名额与退款事实已按下架操作收口。");
      } else if (pendingCompletion && ["completed", "archived"].includes(value.instance.status)) {
        // A completion request may have committed before the browser lost its
        // response. The authoritative status closes the recovery record.
        clearCompletionRecovery(params.instanceId);
        completionOperation.current = null;
        completionExpectedInstanceVersion.current = null;
        setMessage("已标记为已结束，小程序「往期活动」即将可见。");
      } else if (pendingCompletion && value.instance.status !== "published") {
        // A different terminal transition (for example cancellation) won the
        // lifecycle race; this completion receipt can no longer be replayed.
        clearCompletionRecovery(params.instanceId);
        completionOperation.current = null;
        completionExpectedInstanceVersion.current = null;
        setMessage("");
      } else if (
        (!pendingInstanceArchive || value.instance.status !== "archived") &&
        !recoveredSessionArchive
      ) {
        setMessage(
          pending
            ? "上次发布尚未确认完成，再次发布时系统会自动接续处理。"
            : pendingCompletion
              ? "上次结束请求尚未确认完成，再次点击「标记为已结束」会接续同一操作。"
              : pendingCancellation
                ? "上次下架请求尚未确认完成，再次点击「下架活动」会接续同一操作。"
                : pendingInstanceArchive
                  ? "上次归档尚未确认完成，再次点击「归档本期」会接续同一操作。"
                  : pendingSessionArchiveExists
                    ? "上次场次归档尚未确认完成，再次点击「归档场次」会接续同一操作。"
                    : "",
        );
      }
      if (reviewResult.status === "fulfilled") {
        setReviewStatus(reviewResult.value);
      } else {
        setReviewStatusError(displayError(reviewResult.reason));
      }
    } catch (reason) {
      if (!request.signal.aborted) setMessage(displayError(reason));
    } finally {
      if (loadRequest.current === request) {
        loadRequest.current = null;
        setLoading(false);
        setReviewStatusLoading(false);
      }
    }
  }, [params.instanceId]);

  useEffect(() => {
    void load();
    return () => loadRequest.current?.abort();
  }, [load]);

  useEffect(() => {
    if (!detail || detail.instance.status !== "published") return;
    const boundaryMs = nextSessionLifecycleBoundaryMs(detail.sessions, lifecycleNowMs);
    if (boundaryMs === null) return;
    const delayMs = Math.min(Math.max(boundaryMs - lifecycleNowMs + 1, 0), 2_147_000_000);
    const timer = window.setTimeout(() => setLifecycleNowMs(Date.now()), delayMs);
    return () => window.clearTimeout(timer);
  }, [detail, lifecycleNowMs]);

  function beginSessionEdit(session: Session) {
    if (
      savingSession ||
      creatingSession ||
      session.status !== "draft" ||
      detail?.instance.status !== "draft"
    )
      return;
    sessionUpdateOperations.current[session.id] =
      readSessionUpdateRecovery(detail.instance.id, session.id) ||
      sessionUpdateOperations.current[session.id] ||
      "";
    setEditingSessionId(session.id);
    setCreatingSession(false);
    setSessionEditDraft(sessionEditDraftFromSession(session));
    setMessage("");
  }

  function beginSessionCreate() {
    if (
      savingSession ||
      creatingSession ||
      editingSessionId ||
      !detail ||
      detail.instance.status !== "draft"
    )
      return;
    const pending = readSessionCreateRecovery(detail.instance.id);
    sessionCreateOperation.current = pending?.operation || sessionCreateOperation.current || "";
    setEditingSessionId("");
    setCreatingSession(true);
    setSessionEditDraft(pending?.draft || newSessionDraftFromSessions(detail.sessions));
    setMessage("");
  }

  function cancelSessionEdit() {
    if (savingSession) return;
    setEditingSessionId("");
    setCreatingSession(false);
    setSessionEditDraft(null);
  }

  function patchSessionEdit(patch: Partial<SessionEditDraft>) {
    if (creatingSession && detail && sessionCreateOperation.current) {
      // A changed payload cannot safely reuse the receipt for an earlier
      // command. Keep the draft, but start a fresh operation on the next save.
      clearSessionCreateRecovery(detail.instance.id);
      sessionCreateOperation.current = null;
    }
    setSessionEditDraft((current) => (current ? { ...current, ...patch } : current));
  }

  async function saveSessionEdit() {
    if (!detail || !editingSessionId || !sessionEditDraft || savingSession) return;
    const draft = sessionEditDraft;
    const validated = sessionWritePayloadFromDraft(draft);
    if (validated.error || !validated.payload) {
      setMessage(validated.error || "请检查场次设置。");
      return;
    }
    const session = detail.sessions.find((item) => item.id === editingSessionId);
    if (!session) {
      setMessage("场次不存在，请刷新后重试。");
      return;
    }
    setSavingSession(true);
    setMessage("");
    const operation =
      sessionUpdateOperations.current[editingSessionId] ||
      readSessionUpdateRecovery(detail.instance.id, editingSessionId) ||
      operationKey();
    sessionUpdateOperations.current[editingSessionId] = operation;
    writeSessionUpdateRecovery(detail.instance.id, editingSessionId, operation);
    try {
      await api<Session>(`/instances/${detail.instance.id}/sessions/${editingSessionId}`, {
        method: "PATCH",
        headers: operationHeaders(operation),
        body: JSON.stringify({
          expected_instance_version: detail.instance.version,
          expected_session_version: session.version,
          ...validated.payload,
        }),
      });
      delete sessionUpdateOperations.current[editingSessionId];
      clearSessionUpdateRecovery(detail.instance.id, editingSessionId);
      setEditingSessionId("");
      setSessionEditDraft(null);
      await load();
      setMessage("场次已保存。父级活动版本已同步更新，发布前可继续校对。");
    } catch (reason) {
      if (definitiveFailure(reason)) {
        delete sessionUpdateOperations.current[editingSessionId];
        clearSessionUpdateRecovery(detail.instance.id, editingSessionId);
        setMessage(displayError(reason));
      } else {
        setMessage(`${displayError(reason)}；结果未知，再次保存会沿用同一操作键。`);
      }
    } finally {
      setSavingSession(false);
    }
  }

  async function saveNewSession() {
    if (
      !detail ||
      !creatingSession ||
      !sessionEditDraft ||
      savingSession ||
      detail.instance.status !== "draft"
    )
      return;
    const validated = sessionWritePayloadFromDraft(sessionEditDraft);
    if (validated.error || !validated.payload) {
      setMessage(validated.error || "请检查场次设置。");
      return;
    }
    setSavingSession(true);
    setMessage("");
    const pending = readSessionCreateRecovery(detail.instance.id);
    const operation = sessionCreateOperation.current || pending?.operation || operationKey();
    sessionCreateOperation.current = operation;
    writeSessionCreateRecovery(detail.instance.id, { operation, draft: sessionEditDraft });
    try {
      await api<Session>(`/instances/${detail.instance.id}/sessions`, {
        method: "POST",
        headers: operationHeaders(operation),
        body: JSON.stringify({
          expected_instance_version: detail.instance.version,
          ...validated.payload,
        }),
      });
      clearSessionCreateRecovery(detail.instance.id);
      sessionCreateOperation.current = null;
      setCreatingSession(false);
      setSessionEditDraft(null);
      await load();
      setMessage("场次已新增。父级活动版本已同步更新，发布前可继续校对。");
    } catch (reason) {
      if (definitiveFailure(reason)) {
        clearSessionCreateRecovery(detail.instance.id);
        sessionCreateOperation.current = null;
        setMessage(displayError(reason));
      } else {
        setMessage(`${displayError(reason)}；结果未知，再次保存会沿用同一操作键。`);
      }
    } finally {
      setSavingSession(false);
    }
  }

  async function copyReviewPath(path: string) {
    if (
      !reviewStatus?.public_review_eligible ||
      typeof navigator === "undefined" ||
      !navigator.clipboard
    )
      return;
    try {
      await navigator.clipboard.writeText(path);
      setCopiedReviewPath(path);
      window.setTimeout(
        () => setCopiedReviewPath((current) => (current === path ? "" : current)),
        1800,
      );
    } catch {
      setReviewStatusError("无法复制小程序入口，请手动复制显示的路径。");
    }
  }

  function addReviewPhoto() {
    reviewPhotoSequence.current += 1;
    setReviewPhotoRows((current) => [
      ...current,
      { id: reviewPhotoSequence.current, url: "", enabled: true },
    ]);
  }

  function updateReviewPhoto(index: number, patch: Partial<ReviewPhotoDraft>) {
    setReviewPhotoRows((current) =>
      current.map((row, rowIndex) => (rowIndex === index ? { ...row, ...patch } : row)),
    );
  }

  async function completeReviewMediaUpload() {
    const pending = pendingReviewFile.current;
    if (!privateReviewMediaEnabled || !detail || !pending || uploadingReviewFile) return;
    setUploadingReviewFile(true);
    setMessage("");
    try {
      const base = `/media-upload-intents/${encodeURIComponent(pending.operation)}`;
      const issued = await api<ReviewMediaUploadReceipt>("/media-upload-intents", {
        method: "POST",
        headers: operationHeaders(pending.operation),
        body: JSON.stringify({
          instance_id: detail.instance.id,
          kind: pending.choice.kind,
          mime: pending.choice.mime,
          size: pending.file.size,
          sha256: pending.sha256,
        }),
      });
      if (
        issued.file_id !== pending.operation ||
        issued.instance_id !== detail.instance.id ||
        issued.kind !== pending.choice.kind ||
        issued.mime !== pending.choice.mime ||
        issued.size !== pending.file.size ||
        issued.sha256 !== pending.sha256 ||
        issued.upload_url !== `/api/v1/xiangwan/admin${base}/bytes`
      ) {
        throw new Error("服务返回的活动资料上传意图与所选文件不一致");
      }
      await apiBinaryUpload<ReviewMediaUploadReceipt>(`${base}/bytes`, pending.file);
      const confirmed = await api<ReviewMediaUploadReceipt>("/media-confirmations", {
        method: "POST",
        body: JSON.stringify({ file_id: pending.operation, sha256: pending.sha256 }),
      });
      if (
        confirmed.file_id !== pending.operation ||
        confirmed.sha256 !== pending.sha256 ||
        !confirmed.confirmed_at ||
        confirmed.preview_url !== `/api/v1/xiangwan/admin${base}/preview`
      ) {
        throw new Error("活动资料确认回执不完整，请保持文件并重试同一上传");
      }
      setReviewFiles((current) =>
        current.some((row) => row.fileID === confirmed.file_id)
          ? current
          : [
              ...current,
              {
                fileID: confirmed.file_id,
                kind: pending.choice.kind,
                sha256: pending.sha256,
                localName: pending.file.name,
                size: pending.file.size,
                previewOpened: false,
                reviewed: false,
              },
            ],
      );
      pendingReviewFile.current = null;
      setPendingReviewFileReady(false);
      setMessage(
        "资料已私有暂存。请下载核对原始文件，再勾选人工审核确认。未发布前不会进入小程序。",
      );
    } catch (reason) {
      if (definitiveFailure(reason)) {
        pendingReviewFile.current = null;
        setPendingReviewFileReady(false);
      }
      setMessage(
        `${displayError(reason)}${pendingReviewFile.current ? "；保留所选文件，可用同一操作键重试" : ""}`,
      );
    } finally {
      setUploadingReviewFile(false);
    }
  }

  async function selectReviewMediaFile(file: File) {
    if (!privateReviewMediaEnabled || !detail || savingReview || reviewOperation.current) return;
    if (pendingReviewFile.current) {
      setMessage("请先重试或放弃上一份未确认的资料上传。");
      return;
    }
    const choice = reviewMediaChoice(file.name, file.type, file.size);
    if (!choice) {
      setMessage(
        "文件仅支持 JPG/PNG/WebP（10 MiB）、MP4（200 MiB）、MP3（50 MiB）或 PDF（20 MiB），请核对格式和大小。",
      );
      return;
    }
    if (
      choice.kind === "photo" &&
      selectedReviewPhotoURLs(reviewPhotoRows).length +
        reviewFiles.filter((row) => row.kind === "photo").length >=
        30
    ) {
      setMessage("活动照片最多 30 张。");
      return;
    }
    if (choice.kind !== "photo" && reviewFiles.some((row) => row.kind === choice.kind)) {
      setMessage("同一回顾只可选择一份该类型资料。");
      return;
    }
    const pending: PendingReviewMediaUpload = {
      file,
      choice,
      sha256: "",
      operation: operationKey(),
    };
    pendingReviewFile.current = pending;
    setHashingReviewFile(true);
    setMessage("正在计算文件摘要并核对大小，请稍候…");
    try {
      const sha256 = await reviewMediaSHA256(file);
      pending.sha256 = sha256;
      setHashingReviewFile(false);
      setPendingReviewFileReady(true);
      await completeReviewMediaUpload();
    } catch (reason) {
      pendingReviewFile.current = null;
      setHashingReviewFile(false);
      setPendingReviewFileReady(false);
      setMessage(displayError(reason));
    }
  }

  async function saveReviewResource() {
    if (!detail || !reviewStatus?.public_review_eligible || savingReview) return;
    const pending = reviewRecovery.current || readReviewRecovery(detail.instance.id);
    if (!pending && (pendingReviewFile.current || uploadingReviewFile)) {
      setMessage("请先完成或放弃正在暂存的原始文件。");
      return;
    }
    if (pending) {
      // An unknown result must replay the exact request, including File IDs,
      // checksum assertions, target version and sort order. Current form
      // state may have been lost after a browser refresh.
      await submitReviewResource(pending);
      return;
    }
    const title = reviewTitle.trim();
    const description = reviewDescription.trim();
    const videoURL = reviewVideoURL.trim();
    const videoChannel =
      reviewFinderUserName.trim() || reviewFeedID.trim()
        ? { finder_user_name: reviewFinderUserName.trim(), feed_id: reviewFeedID.trim() }
        : undefined;
    if (
      videoChannel &&
      (!videoChannel.finder_user_name.startsWith("sph") || !videoChannel.feed_id || videoURL)
    ) {
      setMessage("请填写完整的视频号 ID 与视频 ID，并清空旧网页链接");
      return;
    }
    const photos = selectedReviewPhotoURLs(reviewPhotoRows);
    const files = reviewFiles.map((item) => ({
      file_id: item.fileID,
      kind: item.kind,
      sha256: item.sha256,
      reviewed: item.reviewed,
    }));
    let recording;
    let materials;
    try {
      recording = reviewLinkRequest(reviewRecording);
      materials = reviewLinkRequest(reviewMaterials);
    } catch (reason) {
      setMessage(displayError(reason));
      return;
    }
    if (!title) {
      setMessage("请填写回顾标题。");
      return;
    }
    if (photos.length + reviewFiles.filter((item) => item.kind === "photo").length > 30) {
      setMessage("活动照片最多填写 30 个白名单 HTTPS 图片地址。");
      return;
    }
    if (reviewFiles.some((item) => !item.previewOpened || !item.reviewed)) {
      setMessage("请先下载并核对每份原始文件，再逐一确认人工审核。");
      return;
    }
    if (recording && !recording.title) {
      setMessage("请填写录音梳理名称。");
      return;
    }
    if (materials && !materials.title) {
      setMessage("请填写活动资料名称。");
      return;
    }
    if (
      !videoURL &&
      !videoChannel &&
      photos.length === 0 &&
      !recording &&
      !materials &&
      files.length === 0
    ) {
      setMessage("请至少提供视频号链接、已选照片、录音梳理或活动资料中的一项。");
      return;
    }
    const recovery: ReviewResourceRecovery = {
      version: 2,
      operation: operationKey(),
      body: JSON.stringify({
        expected_target_version: detail.instance.version,
        title,
        description,
        video_url: videoURL,
        video_channel: videoChannel,
        photos,
        files,
        recording,
        materials,
        sort_order: reviewStatus.instance_review_document_count,
      }),
    };
    reviewRecovery.current = recovery;
    reviewOperation.current = recovery.operation;
    writeReviewRecovery(detail.instance.id, recovery);
    await submitReviewResource(recovery);
  }

  async function submitReviewResource(recovery: ReviewResourceRecovery) {
    if (!detail || savingReview) return;
    setSavingReview(true);
    setMessage("");
    setReviewSubmitPending(true);
    try {
      await api(`/instances/${detail.instance.id}/review-resources`, {
        method: "POST",
        headers: operationHeaders(recovery.operation),
        body: recovery.body,
      });
      clearReviewRecovery(detail.instance.id);
      reviewRecovery.current = null;
      reviewOperation.current = null;
      setReviewSubmitPending(false);
      setReviewDescription("");
      setReviewVideoURL("");
      setReviewFinderUserName("");
      setReviewFeedID("");
      setReviewPhotoRows([]);
      setReviewFiles([]);
      reviewPhotoSequence.current = 0;
      setReviewRecording({
        added: false,
        enabled: false,
        title: "录音梳理",
        subtitle: "",
        url: "",
      });
      setReviewMaterials({
        added: false,
        enabled: false,
        title: "活动资料",
        subtitle: "",
        url: "",
      });
      await load();
      setMessage("本期活动回顾已审核发布，小程序往期活动页已可查看。");
    } catch (reason) {
      if (definitiveFailure(reason)) {
        clearReviewRecovery(detail.instance.id);
        reviewRecovery.current = null;
        reviewOperation.current = null;
        setReviewSubmitPending(false);
      } else {
        setMessage(`${displayError(reason)}；结果未知，再次提交会原样重放上一笔请求。`);
        return;
      }
      setMessage(displayError(reason));
    } finally {
      setSavingReview(false);
    }
  }

  async function saveSessionReviewResource(sessionId: string) {
    if (!detail || !reviewStatus?.public_review_eligible || savingSessionReviewId) return;
    const status = reviewStatus.sessions.find((item) => item.session_id === sessionId);
    if (!status || !["ended", "archived"].includes(status.status)) {
      setMessage("只有已结束或已归档的场次可以添加公开资源。");
      return;
    }
    const draft = sessionReviewDrafts[sessionId] || {
      title: "本场视频回顾",
      description: "",
      videoURL: "",
    };
    const title = draft.title.trim();
    const description = draft.description.trim();
    const videoURL = draft.videoURL.trim();
    if (!title || !videoURL) {
      setMessage("请填写场次资源标题和视频号链接。");
      return;
    }
    setSavingSessionReviewId(sessionId);
    setMessage("");
    const operation =
      sessionReviewOperations.current[sessionId] ||
      readSessionReviewRecovery(detail.instance.id, sessionId) ||
      operationKey();
    sessionReviewOperations.current[sessionId] = operation;
    writeSessionReviewRecovery(detail.instance.id, sessionId, operation);
    try {
      await api("/instances/" + detail.instance.id + "/review-resources", {
        method: "POST",
        headers: operationHeaders(operation),
        body: JSON.stringify({
          expected_target_version: detail.instance.version,
          session_id: sessionId,
          title,
          description,
          video_url: videoURL,
          sort_order: status.public_resource_count,
        }),
      });
      clearSessionReviewRecovery(detail.instance.id, sessionId);
      delete sessionReviewOperations.current[sessionId];
      setSessionReviewDrafts((current) => ({
        ...current,
        [sessionId]: { title: "本场视频回顾", description: "", videoURL: "" },
      }));
      await load();
      setMessage("场次视频回顾已审核发布，小程序对应场次回顾入口已更新。");
    } catch (reason) {
      if (definitiveFailure(reason)) {
        clearSessionReviewRecovery(detail.instance.id, sessionId);
        delete sessionReviewOperations.current[sessionId];
      } else {
        setMessage(displayError(reason) + "；结果未知，再次提交会沿用同一操作键。");
        return;
      }
      setMessage(displayError(reason));
    } finally {
      setSavingSessionReviewId("");
    }
  }

  // 活动不会自动流转到「已结束」:全部场次结束后,由运营显式标记,
  // 该期才会进入小程序「往期活动」。未到期时按钮隐藏,后端同样拒绝。
  async function complete() {
    if (!detail) return;
    const pending = readCompletionRecovery(detail.instance.id);
    const confirmText = completionRecoveryPrompt(Boolean(pending));
    if (!window.confirm(confirmText)) return;
    setCompleting(true);
    setMessage("");
    completionOperation.current ||= pending?.operation || operationKey();
    completionExpectedInstanceVersion.current ||=
      pending?.expectedInstanceVersion || detail.instance.version;
    writeCompletionRecovery(detail.instance.id, {
      operation: completionOperation.current,
      expectedInstanceVersion: completionExpectedInstanceVersion.current,
    });
    try {
      await api(`/instances/${detail.instance.id}/completion`, {
        method: "POST",
        headers: operationHeaders(completionOperation.current),
        body: JSON.stringify({
          expected_instance_version: completionExpectedInstanceVersion.current,
        }),
      });
      clearCompletionRecovery(detail.instance.id);
      completionOperation.current = null;
      completionExpectedInstanceVersion.current = null;
      await load();
      setMessage("已标记为已结束，小程序「往期活动」即将可见。");
    } catch (reason) {
      if (definitiveFailure(reason)) {
        clearCompletionRecovery(detail.instance.id);
        completionOperation.current = null;
        completionExpectedInstanceVersion.current = null;
      } else {
        setMessage(`${displayError(reason)}；结果未知，再次点击「标记为已结束」会沿用同一操作键。`);
        return;
      }
      setMessage(displayError(reason));
    } finally {
      setCompleting(false);
    }
  }

  async function archiveInstance() {
    if (!detail || detail.instance.status !== "completed" || archivingTarget) return;
    const pending = readArchiveRecovery("instance", detail.instance.id);
    if (
      !window.confirm(
        "确认归档本期活动？归档只改变生命周期状态，不会改写发布、报名、订单、退款或回顾事实。",
      )
    )
      return;
    setArchivingTarget("instance");
    setMessage("");
    const operation = pending?.operation || instanceArchiveOperation.current || operationKey();
    const expectedVersion = pending?.expectedVersion || detail.instance.version;
    instanceArchiveOperation.current = operation;
    writeArchiveRecovery("instance", detail.instance.id, { operation, expectedVersion });
    try {
      await api("/instances/" + detail.instance.id + "/archives", {
        method: "POST",
        headers: operationHeaders(operation),
        body: JSON.stringify({ expected_instance_version: expectedVersion }),
      });
      clearArchiveRecovery("instance", detail.instance.id);
      instanceArchiveOperation.current = null;
      await load();
      setMessage("本期已归档，历史发布和报名事实保持不变。");
    } catch (reason) {
      if (definitiveFailure(reason)) {
        clearArchiveRecovery("instance", detail.instance.id);
        instanceArchiveOperation.current = null;
        setMessage(displayError(reason));
      } else {
        setMessage("结果未知，再次点击「归档本期」会沿用同一操作键。");
      }
    } finally {
      setArchivingTarget("");
    }
  }

  async function archiveSession(session: Session) {
    if (!detail || session.status !== "ended" || archivingTarget) return;
    const target = "session:" + session.id;
    const pending = readArchiveRecovery(target, session.id);
    if (
      !window.confirm(
        "确认归档场次「" +
          session.title +
          "」？归档只改变生命周期状态，不会改写报名、订单、退款或签到事实。",
      )
    )
      return;
    setArchivingTarget(session.id);
    setMessage("");
    const operation =
      pending?.operation || sessionArchiveOperations.current[session.id] || operationKey();
    const expectedVersion = pending?.expectedVersion || session.version;
    sessionArchiveOperations.current[session.id] = operation;
    writeArchiveRecovery(target, session.id, { operation, expectedVersion });
    try {
      await api("/sessions/" + session.id + "/archives", {
        method: "POST",
        headers: operationHeaders(operation),
        body: JSON.stringify({ expected_session_version: expectedVersion }),
      });
      clearArchiveRecovery(target, session.id);
      delete sessionArchiveOperations.current[session.id];
      await load();
      setMessage("场次已归档，历史报名和回顾事实保持不变。");
    } catch (reason) {
      if (definitiveFailure(reason)) {
        clearArchiveRecovery(target, session.id);
        delete sessionArchiveOperations.current[session.id];
        setMessage(displayError(reason));
      } else {
        setMessage("结果未知，再次点击「归档场次」会沿用同一操作键。");
      }
    } finally {
      setArchivingTarget("");
    }
  }

  function startContentEdit() {
    if (!detail) return;
    setEditCoverUrl(detail.instance.cover_image_url || "");
    setEditBlocks(detail.instance.detail_blocks || []);
    setEditingContent(true);
    setMessage("");
  }

  async function uploadEditCoverImage(file: File) {
    if (!activityImageAccept.includes(file.type)) {
      setMessage("活动封面仅支持 JPG、PNG 或 WebP 格式。");
      return;
    }
    if (file.size > activityImageMaxBytes) {
      setMessage("活动封面不能超过 5 MiB。");
      return;
    }
    setUploadingEditCover(true);
    setMessage("");
    try {
      setEditCoverUrl(await uploadActivityImage(file));
    } catch (reason) {
      setMessage(
        reason instanceof ApiError
          ? "活动封面上传失败，请确认格式与大小后重试。"
          : "活动封面上传失败，请稍后重试。",
      );
    } finally {
      setUploadingEditCover(false);
      if (editCoverFileInput.current) editCoverFileInput.current.value = "";
    }
  }

  // PATCH 为整体替换语义:封面留空串即清除,detail_blocks 全量覆盖;
  // 天然幂等,每次保存使用新的幂等键,失败后可直接重试。
  // expected_presentation_revision 以当前详情为准:被他人抢先保存时后端返回
  // 409,页面刷新为最新版本后重新开始编辑。
  async function saveContentEdit() {
    if (!detail || savingContent) return;
    const coverUrl = editCoverUrl.trim();
    if (coverUrl && !isActivityImageRef(coverUrl)) {
      setMessage(
        "活动封面地址必须是站内封面路径（/api/v1/xiangwan/covers/…）或完整、安全的 HTTPS 地址",
      );
      return;
    }
    const blocksError = validateDetailBlocks(editBlocks);
    if (blocksError) {
      setMessage(blocksError);
      return;
    }
    setSavingContent(true);
    setMessage("");
    try {
      await api(`/instances/${detail.instance.id}`, {
        method: "PATCH",
        headers: operationHeaders(operationKey()),
        body: JSON.stringify({
          cover_image_url: coverUrl,
          detail_blocks: serializeDetailBlocks(editBlocks),
          expected_presentation_revision: detail.instance.presentation_revision,
        }),
      });
      setEditingContent(false);
      await load();
      setMessage("封面与图文详情已保存，小程序端已立即生效。");
    } catch (reason) {
      if (reason instanceof ApiError && reason.status === 409) {
        setEditingContent(false);
        await load();
        setMessage("内容已被其他操作更新，页面已刷新为最新版本，请重新开始编辑。");
      } else {
        setMessage(displayError(reason));
      }
    } finally {
      setSavingContent(false);
    }
  }

  async function publish() {
    if (
      !detail ||
      !publishableStatuses.has(detail.instance.status) ||
      !window.confirm("确认将这一期及全部场次发布到小程序？")
    )
      return;
    setPublishing(true);
    setMessage("");
    setViolations([]);
    publishOperation.current ||= operationKey();
    publishExpectedInstanceVersion.current ||= detail.instance.version;
    writePublicationRecovery(detail.instance.id, {
      operation: publishOperation.current,
      expectedInstanceVersion: publishExpectedInstanceVersion.current,
    });
    try {
      const result = await api<{ publication_version: number }>(
        `/instances/${detail.instance.id}/publications`,
        {
          method: "POST",
          headers: operationHeaders(publishOperation.current),
          body: JSON.stringify({
            expected_instance_version: publishExpectedInstanceVersion.current,
          }),
        },
      );
      clearPublicationRecovery(detail.instance.id);
      publishOperation.current = null;
      publishExpectedInstanceVersion.current = null;
      await load();
      setMessage(`发布成功，当前公开版本 v${result.publication_version}`);
    } catch (reason) {
      if (reason instanceof ApiError && reason.code === 30606) {
        const data = reason.data as
          | { violations?: Array<{ Field?: string; field?: string; Code?: string; code?: string }> }
          | undefined;
        setViolations([...new Set((data?.violations || []).map(publicationViolationLabel))]);
      }
      if (definitiveFailure(reason)) {
        clearPublicationRecovery(detail.instance.id);
        publishOperation.current = null;
        publishExpectedInstanceVersion.current = null;
      }
      setMessage(displayError(reason));
    } finally {
      setPublishing(false);
    }
  }

  async function schedulePublication() {
    if (
      !detail ||
      scheduling ||
      !scheduledAtLocal ||
      !publishableStatuses.has(detail.instance.status)
    )
      return;
    const recovered =
      scheduleOperation.current && scheduleExpectedInstanceVersion.current && scheduleAt.current
        ? {
            operation: scheduleOperation.current,
            expectedInstanceVersion: scheduleExpectedInstanceVersion.current,
            scheduledAt: scheduleAt.current,
          }
        : null;
    const scheduledAtValue = recovered?.scheduledAt || shanghaiLocalToISOString(scheduledAtLocal);
    if (!scheduledAtValue) {
      setMessage("计划发布时间格式无效。");
      return;
    }
    const scheduledAt = scheduledAtValue ? new Date(scheduledAtValue) : new Date(NaN);
    if (
      !Number.isFinite(scheduledAt.getTime()) ||
      (!recovered && scheduledAt.getTime() <= Date.now())
    ) {
      setMessage("计划发布时间必须晚于当前时间。");
      return;
    }
    if (
      !window.confirm(
        recovered
          ? "上次定时发布请求结果未知，将使用同一操作键重试。继续？"
          : "确认保存这个计划发布时间？到点后系统会再次执行完整发布检查。",
      )
    )
      return;
    setScheduling(true);
    setMessage("");
    const operation = recovered?.operation || operationKey();
    const expectedInstanceVersion = recovered?.expectedInstanceVersion || detail.instance.version;
    scheduleOperation.current = operation;
    scheduleExpectedInstanceVersion.current = expectedInstanceVersion;
    scheduleAt.current = scheduledAtValue;
    writeScheduleRecovery(detail.instance.id, {
      operation,
      expectedInstanceVersion,
      scheduledAt: scheduledAtValue,
    });
    try {
      await api(`/instances/${detail.instance.id}/publication-schedules`, {
        method: "POST",
        headers: operationHeaders(operation),
        body: JSON.stringify({
          expected_instance_version: expectedInstanceVersion,
          scheduled_at: scheduledAtValue,
        }),
      });
      clearScheduleRecovery(detail.instance.id);
      scheduleOperation.current = null;
      scheduleExpectedInstanceVersion.current = null;
      scheduleAt.current = null;
      setScheduleRetryPending(false);
      await load();
      setMessage("已保存定时发布，活动当前为待发布状态。");
    } catch (reason) {
      if (definitiveFailure(reason)) {
        clearScheduleRecovery(detail.instance.id);
        scheduleOperation.current = null;
        scheduleExpectedInstanceVersion.current = null;
        scheduleAt.current = null;
        setScheduleRetryPending(false);
      }
      setMessage(displayError(reason));
    } finally {
      setScheduling(false);
    }
  }

  async function cancelScheduledPublication() {
    if (!detail || cancellingSchedule) return;
    const pending = readScheduleCancellationRecovery(detail.instance.id);
    const current = scheduledPublications.find((item) => item.status === "pending");
    const scheduleID = pending?.scheduleID || current?.id || "";
    if (!scheduleID) return;
    if (!window.confirm("确认取消当前定时发布？取消后可以重新选择时间并保存。")) return;
    setCancellingSchedule(true);
    setMessage("");
    const operation = pending?.operation || operationKey();
    scheduleCancellationOperation.current = operation;
    scheduleCancellationID.current = scheduleID;
    writeScheduleCancellationRecovery(detail.instance.id, { operation, scheduleID });
    try {
      await api(
        `/instances/${detail.instance.id}/publication-schedules/${scheduleID}/cancellation`,
        {
          method: "POST",
          headers: operationHeaders(operation),
        },
      );
      clearScheduleCancellationRecovery(detail.instance.id);
      scheduleCancellationOperation.current = null;
      scheduleCancellationID.current = null;
      await load();
      setScheduledAtLocal("");
      setMessage("定时发布已取消，可以重新选择发布时间。");
    } catch (reason) {
      if (definitiveFailure(reason)) {
        clearScheduleCancellationRecovery(detail.instance.id);
        scheduleCancellationOperation.current = null;
        scheduleCancellationID.current = null;
      }
      setMessage(displayError(reason));
    } finally {
      setCancellingSchedule(false);
    }
  }

  async function cancelInstance() {
    if (!detail || detail.instance.status !== "published" || cancelling) return;
    const pending = readCancellationRecovery(detail.instance.id);
    const recoveredFinal =
      pending !== null &&
      pending.stage === "final" &&
      pending.previewId !== undefined &&
      pending.expectedInstanceVersion !== undefined &&
      pending.impact !== undefined;
    const recoveredPreview =
      recoveredFinal && pending
        ? {
            id: pending.previewId as string,
            expectedInstanceVersion: pending.expectedInstanceVersion as number,
            impact: pending.impact as CancellationImpact,
          }
        : null;
    const reason =
      pending?.reason ||
      (await requestRequiredReason({
        title: "下架活动",
        description: "填写原因后将核对报名、退款与优惠券的影响，最终确认后才下架。",
        initialValue: "活动安排调整",
      })) ||
      "";
    if (!reason) return;
    setCancelling(true);
    setMessage("");
    const previewOperation = pending?.previewOperation || operationKey();
    const finalOperation = pending?.operation || operationKey();
    cancellationPreviewOperation.current = previewOperation;
    cancellationOperation.current = finalOperation;
    try {
      let preview: {
        id: string;
        expected_instance_version: number;
        cancelled_registration_count: number;
        requested_refund_cents: number;
        coupon_adjustment_count: number;
      };
      if (recoveredPreview) {
        preview = {
          id: recoveredPreview.id,
          expected_instance_version: recoveredPreview.expectedInstanceVersion,
          cancelled_registration_count: recoveredPreview.impact.cancelledRegistrationCount,
          requested_refund_cents: recoveredPreview.impact.requestedRefundCents,
          coupon_adjustment_count: recoveredPreview.impact.couponAdjustmentCount,
        };
      } else {
        writeCancellationRecovery(detail.instance.id, {
          stage: "preview",
          previewOperation,
          operation: finalOperation,
          reason,
        });
        preview = await api<{
          id: string;
          expected_instance_version: number;
          cancelled_registration_count: number;
          requested_refund_cents: number;
          coupon_adjustment_count: number;
        }>(`/instances/${detail.instance.id}/cancellation-previews`, {
          method: "POST",
          headers: operationHeaders(previewOperation),
          body: JSON.stringify({ reason }),
        });
        writeCancellationRecovery(detail.instance.id, {
          stage: "final",
          previewOperation,
          operation: finalOperation,
          reason,
          previewId: preview.id,
          expectedInstanceVersion: preview.expected_instance_version,
          impact: {
            cancelledRegistrationCount: preview.cancelled_registration_count,
            requestedRefundCents: preview.requested_refund_cents,
            couponAdjustmentCount: preview.coupon_adjustment_count,
          },
        });
      }
      const impact = `将关闭 ${preview.cancelled_registration_count} 个报名，记录 ¥${(Number(preview.requested_refund_cents || 0) / 100).toFixed(2)} 退款，调整 ${preview.coupon_adjustment_count} 张优惠券。`;
      const confirmText = recoveredPreview
        ? `上次下架请求可能未确认完成，将使用同一操作键重试。\n${impact}\n继续下架？`
        : `${impact}\n确认下架？下架后小程序不再接受新报名。`;
      if (!window.confirm(confirmText)) {
        if (!recoveredFinal) {
          clearCancellationRecovery(detail.instance.id);
          cancellationPreviewOperation.current = null;
          cancellationOperation.current = null;
          setMessage("已取消下架操作，活动仍保持发布。");
        } else {
          setMessage("已保留下架请求，稍后再次点击「下架活动」会继续使用同一操作键。");
        }
        return;
      }
      await api(`/instances/${detail.instance.id}/cancellation`, {
        method: "POST",
        headers: operationHeaders(finalOperation),
        body: JSON.stringify({
          preview_id: preview.id,
          expected_instance_version: preview.expected_instance_version,
          reason,
        }),
      });
      clearCancellationRecovery(detail.instance.id);
      cancellationPreviewOperation.current = null;
      cancellationOperation.current = null;
      await load();
      setMessage("活动已下架，报名、名额与退款事实已按预览结果收口。");
    } catch (reason) {
      if (definitiveFailure(reason)) {
        clearCancellationRecovery(detail.instance.id);
        cancellationPreviewOperation.current = null;
        cancellationOperation.current = null;
      } else {
        setMessage(`${displayError(reason)}；结果未知，再次点击「下架活动」会沿用同一操作键。`);
        return;
      }
      setMessage(displayError(reason));
    } finally {
      setCancelling(false);
    }
  }

  if (loading)
    return (
      <div className="page-content">
        <div className="state-card inline">正在读取活动详情…</div>
      </div>
    );
  if (!detail)
    return (
      <div className="page-content">
        <div className="inline-message error">{message || "活动不存在"}</div>
      </div>
    );
  const allSessionsTerminalState = allSessionsTerminal(detail.sessions, lifecycleNowMs);
  const completionReady = canCompleteInstance(detail.sessions, lifecycleNowMs);
  const canPublish = publishableStatuses.has(detail.instance.status);
  const canComplete = detail.instance.status === "published" && completionReady;
  const activeSchedule = scheduledPublications.find(
    (item) => item.status === "pending" || item.status === "processing",
  );
  const latestSchedule = scheduledPublications[0];

  return (
    <div className="page-content detail-page">
      <Link className="back-link" href="/activities">
        <ArrowLeft /> 返回活动期次
      </Link>
      {message && (
        <div
          className={`inline-message ${/^(发布成功|已标记|封面与图文详情已保存|场次已(保存|新增|归档)|本场次已取消|本期已归档|活动已下架|本期视频回顾)/.test(message) ? "success" : "error"}`}
        >
          {message}
        </div>
      )}
      {violations.length > 0 && (
        <div className="violation-list">
          <b>
            <AlertTriangle /> 发布检查未通过
          </b>
          {violations.map((item) => (
            <span key={item}>{item}</span>
          ))}
        </div>
      )}
      <section className="detail-hero">
        {detail.instance.cover_image_url && (
          <div
            className="detail-cover-thumb"
            role="img"
            aria-label="活动封面"
            style={{
              backgroundImage: `url(${JSON.stringify(resolveActivityImageUrl(detail.instance.cover_image_url))})`,
            }}
          />
        )}
        <div>
          <span className="eyebrow">
            {detail.series.title}
            {detail.instance.issue_no > 0 ? ` · 第 ${detail.instance.issue_no} 期` : ""}
          </span>
          <h2>{detail.instance.title}</h2>
          <p>
            {detail.instance.publication_version > 0
              ? `已发布 ${detail.instance.publication_version} 次`
              : "尚未发布"}
          </p>
          {detail.copy_lineage && (
            <p>
              本期内容从
              <Link href={`/activities/${detail.copy_lineage.source_instance_id}`}>来源期次</Link>
              独立复制；报名、订单和回顾没有沿用。
            </p>
          )}
        </div>
        <span className={`status-pill ${detail.instance.status}`}>
          {statusLabel[detail.instance.status] || detail.instance.status}
        </span>
      </section>
      <section className="panel no-top-margin">
        <div className="panel-head">
          <div>
            <h2>活动场次</h2>
            <p>发布前请确认所有场次的时间、地点和人数设置。</p>
          </div>
          <div className="panel-actions">
            {detail.series.status !== "archived" && detail.sessions.length > 0 && (
              <Link
                className="secondary-button"
                href={`/activities/new?copy_instance_id=${detail.instance.id}`}
              >
                <Plus /> 复制为新一期
              </Link>
            )}
            {detail.instance.status === "draft" && (
              <button
                className="secondary-button"
                onClick={beginSessionCreate}
                disabled={savingSession || Boolean(editingSessionId) || creatingSession}
              >
                <Plus /> 新增场次
              </button>
            )}
            {canComplete && (
              <button
                className="secondary-button"
                onClick={() => void complete()}
                disabled={
                  completing ||
                  publishing ||
                  cancelling ||
                  Boolean(archivingTarget) ||
                  savingSession ||
                  creatingSession ||
                  Boolean(editingSessionId)
                }
              >
                <CheckCircle2 /> {completing ? "正在标记…" : "标记为已结束"}
              </button>
            )}
            {detail.instance.status === "completed" && (
              <button
                className="secondary-button"
                onClick={() => void archiveInstance()}
                disabled={
                  Boolean(archivingTarget) ||
                  completing ||
                  publishing ||
                  cancelling ||
                  savingSession
                }
              >
                <CheckCircle2 /> {archivingTarget === "instance" ? "正在归档…" : "归档本期"}
              </button>
            )}
            {detail.instance.status === "published" && (
              <button
                className="secondary-button danger-button"
                onClick={() => void cancelInstance()}
                disabled={
                  cancelling ||
                  publishing ||
                  completing ||
                  Boolean(archivingTarget) ||
                  savingSession ||
                  creatingSession ||
                  Boolean(editingSessionId)
                }
              >
                <Trash2 /> {cancelling ? "正在下架…" : "下架活动"}
              </button>
            )}
            <button
              className="primary-button"
              onClick={publish}
              disabled={
                publishing ||
                cancelling ||
                Boolean(archivingTarget) ||
                savingSession ||
                creatingSession ||
                Boolean(editingSessionId) ||
                detail.sessions.length === 0 ||
                !canPublish
              }
            >
              <Send />{" "}
              {publishing
                ? "正在发布…"
                : !canPublish
                  ? "当前状态不可发布"
                  : detail.instance.status === "published"
                    ? "更新小程序内容"
                    : "发布到小程序"}
            </button>
            {(detail.instance.status === "draft" ||
              detail.instance.status === "pending_publish") && (
              <div className="schedule-publication-form">
                {latestSchedule && (
                  <div className="schedule-publication-status">
                    <span>
                      定时发布：
                      {latestSchedule.status === "pending"
                        ? "等待执行"
                        : latestSchedule.status === "processing"
                          ? "执行中"
                          : latestSchedule.status === "completed"
                            ? "已完成"
                            : latestSchedule.status === "cancelled"
                              ? "已取消"
                              : "执行失败"}
                      · {formatShanghaiDateTime(latestSchedule.scheduled_at)}
                    </span>
                    {latestSchedule.last_error && <small>原因：{latestSchedule.last_error}</small>}
                  </div>
                )}
                <label>
                  定时发布
                  <input
                    type="datetime-local"
                    value={scheduledAtLocal}
                    onChange={(event) => setScheduledAtLocal(event.target.value)}
                    disabled={scheduling || publishing || cancelling || completing}
                    aria-label="计划发布时间"
                  />
                </label>
                <button
                  className="secondary-button"
                  type="button"
                  onClick={() => void schedulePublication()}
                  disabled={
                    !scheduledAtLocal ||
                    scheduling ||
                    cancellingSchedule ||
                    publishing ||
                    cancelling ||
                    completing ||
                    Boolean(activeSchedule && !scheduleRetryPending)
                  }
                >
                  {scheduling ? "正在保存…" : "保存定时发布"}
                </button>
                {activeSchedule?.status === "pending" && (
                  <button
                    className="secondary-button danger-outline"
                    type="button"
                    onClick={() => void cancelScheduledPublication()}
                    disabled={
                      cancellingSchedule || scheduling || publishing || cancelling || completing
                    }
                  >
                    {cancellingSchedule ? "正在取消…" : "取消定时发布后改期"}
                  </button>
                )}
              </div>
            )}
          </div>
        </div>
        {detail.instance.status === "published" && !allSessionsTerminalState && (
          <div className="completion-note">
            <Info />{" "}
            <span>
              至少一场结束且其余场次结束或取消后，这里会出现「标记为已结束」。点击后本期才会进入小程序的「往期活动」。
            </span>
          </div>
        )}
        {detail.instance.status === "published" && allSessionsTerminalState && !completionReady && (
          <div className="completion-note">
            <Info />{" "}
            <span>本期所有场次均已取消，不能标记为已结束；可使用「下架活动」收口本期。</span>
          </div>
        )}
        {detail.instance.status === "completed" && (
          <div className="completion-note completion-note-success">
            <CheckCircle2 />{" "}
            <span>本期已结束，已按场次终态收口，并会出现在小程序底部「往期活动」入口。</span>
          </div>
        )}
        {creatingSession && sessionEditDraft && (
          <SessionEditor
            draft={sessionEditDraft}
            disabled={savingSession}
            saveLabel="保存并新增场次"
            onChange={patchSessionEdit}
            onCancel={cancelSessionEdit}
            onSave={() => void saveNewSession()}
          />
        )}
        <div className="session-grid">
          {detail.sessions.length === 0 ? (
            <div className="empty-card">尚无完整场次，请重新从新建流程创建。</div>
          ) : (
            detail.sessions.map((session, index) => {
              const editing = editingSessionId === session.id && sessionEditDraft;
              return (
                <article className="session-card" key={session.id}>
                  <span className="session-number">{String(index + 1).padStart(2, "0")}</span>
                  <div style={{ minWidth: 0, flex: 1 }}>
                    <div
                      style={{
                        display: "flex",
                        alignItems: "center",
                        gap: 8,
                        justifyContent: "space-between",
                        flexWrap: "wrap",
                      }}
                    >
                      <h3>{session.title}</h3>
                      {detail.instance.status === "draft" &&
                        session.status === "draft" &&
                        !editing && (
                          <button
                            type="button"
                            className="secondary-button"
                            onClick={() => beginSessionEdit(session)}
                            disabled={Boolean(archivingTarget)}
                          >
                            <Pencil /> 编辑场次
                          </button>
                        )}
                      {session.status === "ended" && !editing && (
                        <button
                          type="button"
                          className="secondary-button"
                          onClick={() => void archiveSession(session)}
                          disabled={Boolean(archivingTarget)}
                        >
                          <CheckCircle2 />{" "}
                          {archivingTarget === session.id ? "正在归档…" : "归档场次"}
                        </button>
                      )}
                    </div>
                    <p>
                      <Calendar />{" "}
                      {session.session_start_at
                        ? formatShanghaiDateTime(session.session_start_at)
                        : "未设置"}
                    </p>
                    <p>
                      <MapPin />{" "}
                      {session.delivery_mode === "online"
                        ? session.online_participation_mode
                        : `${session.venue_name || ""} ${session.address || ""}`}
                    </p>
                    <p>
                      <Users /> {session.confirmed_registration_count} / {session.capacity ?? "—"}{" "}
                      人
                    </p>
                    {editing && (
                      <SessionEditor
                        draft={sessionEditDraft}
                        disabled={savingSession}
                        saveLabel="保存场次"
                        onChange={patchSessionEdit}
                        onCancel={cancelSessionEdit}
                        onSave={() => void saveSessionEdit()}
                      />
                    )}
                    {detail.instance.status === "published" && !editing && (
                      <SessionCancellation
                        session={session}
                        disabled={cancelling || publishing || Boolean(archivingTarget)}
                        onBusyChange={setCancelling}
                        onComplete={async (notice) => {
                          await load();
                          if (notice) setMessage(notice);
                        }}
                      />
                    )}
                  </div>
                  <span className={`status-pill ${session.status}`}>
                    {statusLabel[session.status] || session.status}
                  </span>
                </article>
              );
            })
          )}
        </div>
      </section>
      <section className="panel">
        <div className="panel-head">
          <div>
            <h2>公开回顾资源</h2>
            <p>只读读取已审核、已发布的资源关系；正文、文件和外部链接仍由公开回顾链路按块授权。</p>
          </div>
          {reviewStatus && (
            <span
              className={`status-pill ${reviewStatus.public_review_available ? "published" : "draft"}`}
            >
              {reviewStatus.public_review_available ? "已有公开回顾" : "暂无公开回顾"}
            </span>
          )}
        </div>
        <div style={{ padding: "18px 22px", borderTop: "1px solid var(--line)" }}>
          {reviewStatusLoading ? (
            <p className="brand-hero-hint">
              <Info /> 正在读取公开资源状态…
            </p>
          ) : reviewStatusError ? (
            <div className="inline-message error">{reviewStatusError}</div>
          ) : reviewStatus ? (
            <>
              <div className="metric-grid compact">
                <article className="metric blue">
                  <span>本期回顾资料</span>
                  <strong>{reviewStatus.instance_review_document_count}</strong>
                  <small>公开回顾读取器实际返回</small>
                </article>
                <article className="metric yellow">
                  <span>场次公开资源</span>
                  <strong>{reviewStatus.public_session_resource_count}</strong>
                  <small>公开回顾读取器实际返回</small>
                </article>
                <article
                  className={`metric ${reviewStatus.public_review_eligible ? "green" : "yellow"}`}
                >
                  <span>公开资格</span>
                  <strong>{reviewStatus.public_review_eligible ? "可查看" : "待结束"}</strong>
                  <small>
                    {reviewStatus.latest_published_at
                      ? `最近发布 ${formatShanghaiDateTime(reviewStatus.latest_published_at)}`
                      : "尚无发布记录"}
                  </small>
                </article>
              </div>
              {reviewStatus.public_review_eligible ? (
                <div
                  className="review-resource-form"
                  style={{
                    marginTop: 16,
                    padding: 14,
                    border: "1px solid var(--line)",
                    borderRadius: 8,
                    background: "#fbfcfe",
                  }}
                >
                  <div style={{ display: "grid", gap: 4, marginBottom: 10 }}>
                    <b style={{ color: "var(--navy)" }}>新增本期活动回顾</b>
                    <span className="brand-hero-hint" style={{ margin: 0 }}>
                      至少选择一项视频号、照片、录音梳理或活动资料。提交前可调整照片顺序并取消选入；服务端会记录审核证据并立即发布到往期活动。
                    </span>
                    {reviewSubmitPending && (
                      <span className="brand-hero-hint" style={{ margin: 0 }}>
                        上次发布结果尚未确认。重试会原样发送上次的标题、资料和版本；当前表单修改不会影响该请求。
                      </span>
                    )}
                  </div>
                  <div className="field-grid">
                    <label>
                      回顾标题 <span className="required-mark">*</span>
                      <input
                        value={reviewTitle}
                        disabled={savingReview}
                        onChange={(event) => setReviewTitle(event.target.value)}
                        placeholder="例如：本期视频回顾"
                        required={true}
                        aria-required={true}
                      />
                    </label>
                    <label>
                      视频号链接（可选）
                      <input
                        value={reviewVideoURL}
                        disabled={savingReview}
                        onChange={(event) => setReviewVideoURL(event.target.value)}
                        placeholder="https://…（需在服务端白名单内）"
                        inputMode="url"
                      />
                    </label>
                    <label>
                      视频号 ID（可选）{" "}
                      {(reviewFinderUserName.trim() || reviewFeedID.trim()) && (
                        <span className="required-mark">*</span>
                      )}
                      <input
                        value={reviewFinderUserName}
                        disabled={savingReview}
                        onChange={(event) => setReviewFinderUserName(event.target.value)}
                        placeholder="sph…（视频号助手首页）"
                        required={Boolean(reviewFinderUserName.trim() || reviewFeedID.trim())}
                      />
                    </label>
                    <label>
                      视频 ID（可选）{" "}
                      {(reviewFinderUserName.trim() || reviewFeedID.trim()) && (
                        <span className="required-mark">*</span>
                      )}
                      <input
                        value={reviewFeedID}
                        disabled={savingReview}
                        onChange={(event) => setReviewFeedID(event.target.value)}
                        placeholder="feedId（视频号助手内容管理）"
                        required={Boolean(reviewFinderUserName.trim() || reviewFeedID.trim())}
                      />
                    </label>
                    <span className="full brand-hero-hint">
                      原生视频回顾请同时填写两个 ID；旧网页链接与这组 ID 选择其一。
                    </span>
                    <label className="full">
                      回顾描述（可选）
                      <textarea
                        rows={3}
                        value={reviewDescription}
                        disabled={savingReview}
                        onChange={(event) => setReviewDescription(event.target.value)}
                        placeholder="补充本期活动的精彩内容、嘉宾或讨论摘要"
                      />
                    </label>
                    <div className="full" style={{ display: "grid", gap: 8 }}>
                      <div
                        style={{
                          display: "flex",
                          alignItems: "center",
                          justifyContent: "space-between",
                          gap: 8,
                          flexWrap: "wrap",
                        }}
                      >
                        <b style={{ color: "var(--navy)" }}>活动照片（可选）</b>
                        <button
                          type="button"
                          className="secondary-button"
                          disabled={savingReview || reviewPhotoRows.length >= 30}
                          onClick={addReviewPhoto}
                        >
                          添加照片地址
                        </button>
                      </div>
                      <span className="brand-hero-hint" style={{ margin: 0 }}>
                        仅接受服务端外链白名单中的 HTTPS
                        图片地址；行顺序就是发布顺序，取消“发布”即可暂不选入。不上传原始字节。
                      </span>
                      {reviewPhotoRows.length === 0 ? (
                        <span className="brand-hero-hint" style={{ margin: 0 }}>
                          尚未添加照片地址。
                        </span>
                      ) : (
                        reviewPhotoRows.map((row, index) => (
                          <div
                            key={row.id}
                            style={{
                              display: "grid",
                              gridTemplateColumns: "auto 1fr auto auto auto",
                              gap: 8,
                              alignItems: "center",
                            }}
                          >
                            <input
                              type="checkbox"
                              checked={row.enabled}
                              disabled={savingReview}
                              aria-label={`发布第 ${index + 1} 张照片`}
                              onChange={(event) =>
                                updateReviewPhoto(index, { enabled: event.target.checked })
                              }
                            />
                            <label>
                              照片地址{" "}
                              {row.enabled && (
                                <span className="required-mark" aria-hidden="true">
                                  *
                                </span>
                              )}
                              <input
                                value={row.url}
                                required={row.enabled}
                                aria-required={row.enabled}
                                disabled={savingReview}
                                aria-label={`第 ${index + 1} 张照片地址`}
                                onChange={(event) =>
                                  updateReviewPhoto(index, { url: event.target.value })
                                }
                                placeholder="https://…（需在服务端白名单内）"
                                inputMode="url"
                              />
                            </label>
                            <button
                              type="button"
                              className="secondary-button"
                              disabled={savingReview || index === 0}
                              aria-label={`第 ${index + 1} 张照片上移`}
                              onClick={() =>
                                setReviewPhotoRows((current) => moveReviewPhoto(current, index, -1))
                              }
                            >
                              ↑
                            </button>
                            <button
                              type="button"
                              className="secondary-button"
                              disabled={savingReview || index === reviewPhotoRows.length - 1}
                              aria-label={`第 ${index + 1} 张照片下移`}
                              onClick={() =>
                                setReviewPhotoRows((current) => moveReviewPhoto(current, index, 1))
                              }
                            >
                              ↓
                            </button>
                            <button
                              type="button"
                              className="secondary-button"
                              disabled={savingReview}
                              aria-label={`删除第 ${index + 1} 张照片`}
                              onClick={() =>
                                setReviewPhotoRows((current) => removeReviewPhoto(current, index))
                              }
                            >
                              删除
                            </button>
                          </div>
                        ))
                      )}
                    </div>
                    {privateReviewMediaEnabled && (
                      <div className="full" style={{ display: "grid", gap: 8 }}>
                        <b style={{ color: "var(--navy)" }}>平台内原始资料（私有审核）</b>
                        <span className="brand-hero-hint" style={{ margin: 0 }}>
                          文件先私有暂存。下载核对实际内容并逐份确认后，才可随本期回顾提交审核发布；未确认的文件会按保留期清理。
                        </span>
                        <input
                          ref={reviewFileInput}
                          type="file"
                          accept=".jpg,.jpeg,.png,.webp,.mp4,.mp3,.pdf"
                          style={{ display: "none" }}
                          onChange={(event) => {
                            const file = event.target.files?.[0];
                            event.target.value = "";
                            if (file) void selectReviewMediaFile(file);
                          }}
                        />
                        <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
                          <button
                            type="button"
                            className="secondary-button"
                            disabled={
                              savingReview ||
                              uploadingReviewFile ||
                              hashingReviewFile ||
                              pendingReviewFileReady ||
                              reviewSubmitPending
                            }
                            onClick={() => reviewFileInput.current?.click()}
                          >
                            {uploadingReviewFile || hashingReviewFile
                              ? "正在校验并暂存…"
                              : "选择原始文件"}
                          </button>
                          {pendingReviewFileReady && (
                            <>
                              <button
                                type="button"
                                className="secondary-button"
                                disabled={uploadingReviewFile}
                                onClick={() => void completeReviewMediaUpload()}
                              >
                                用同一操作键重试上传
                              </button>
                              <button
                                type="button"
                                className="secondary-button"
                                disabled={uploadingReviewFile}
                                onClick={() => {
                                  pendingReviewFile.current = null;
                                  setPendingReviewFileReady(false);
                                  setMessage("已放弃本次未确认上传；已选中对象将按保留期清理。");
                                }}
                              >
                                放弃本次上传
                              </button>
                            </>
                          )}
                        </div>
                        {reviewFiles.map((item) => (
                          <div
                            key={item.fileID}
                            style={{
                              display: "flex",
                              gap: 8,
                              alignItems: "center",
                              flexWrap: "wrap",
                            }}
                          >
                            <span>
                              {item.localName} · {item.kind} · {(item.size / (1 << 20)).toFixed(1)}{" "}
                              MiB
                            </span>
                            <a
                              className="table-link"
                              href={`/api/v1/xiangwan/admin/media-upload-intents/${encodeURIComponent(item.fileID)}/preview`}
                              download
                              onClick={() =>
                                setReviewFiles((current) =>
                                  current.map((row) =>
                                    row.fileID === item.fileID
                                      ? { ...row, previewOpened: true, reviewed: false }
                                      : row,
                                  ),
                                )
                              }
                            >
                              下载核对原始文件
                            </a>
                            <label style={{ display: "inline-flex", gap: 4, alignItems: "center" }}>
                              <input
                                type="checkbox"
                                checked={item.reviewed}
                                disabled={
                                  !item.previewOpened || savingReview || reviewSubmitPending
                                }
                                onChange={(event) =>
                                  setReviewFiles((current) =>
                                    current.map((row) =>
                                      row.fileID === item.fileID
                                        ? { ...row, reviewed: event.target.checked }
                                        : row,
                                    ),
                                  )
                                }
                              />
                              已人工核对内容
                            </label>
                            <button
                              type="button"
                              className="secondary-button"
                              disabled={savingReview || reviewSubmitPending}
                              onClick={() =>
                                setReviewFiles((current) =>
                                  current.filter((row) => row.fileID !== item.fileID),
                                )
                              }
                            >
                              移除
                            </button>
                          </div>
                        ))}
                      </div>
                    )}
                    <div className="full" style={{ display: "grid", gap: 8 }}>
                      <b style={{ color: "var(--navy)" }}>录音梳理（可选，飞书链接）</b>
                      <p>
                        {reviewRecording.added
                          ? reviewRecording.enabled
                            ? "已添加 · 前台展示"
                            : "已添加 · 暂时隐藏"
                          : "未添加"}
                        。链接留空时显示“暂未配置链接”。
                      </p>
                      <button
                        type="button"
                        className="secondary-button"
                        disabled={savingReview}
                        onClick={() =>
                          setReviewRecording((current) => ({
                            ...current,
                            added: !current.added,
                            enabled: !current.added,
                          }))
                        }
                      >
                        {reviewRecording.added ? "移除卡片" : "添加录音梳理卡片"}
                      </button>
                      <label style={{ display: "inline-flex", alignItems: "center", gap: 8 }}>
                        <input
                          type="checkbox"
                          checked={reviewRecording.enabled}
                          disabled={savingReview}
                          onChange={(event) =>
                            setReviewRecording((current) => ({
                              ...current,
                              added: true,
                              enabled: event.target.checked,
                            }))
                          }
                        />
                        前台展示录音梳理
                      </label>
                      <div className="field-grid">
                        <label>
                          显示名称{" "}
                          {reviewRecording.added && (
                            <span className="required-mark" aria-hidden="true">
                              *
                            </span>
                          )}
                          <input
                            value={reviewRecording.title}
                            disabled={savingReview}
                            onChange={(event) =>
                              setReviewRecording((current) => ({
                                ...current,
                                added: true,
                                title: event.target.value,
                              }))
                            }
                            placeholder="例如：第 12 期讨论纪要"
                            required={Boolean(reviewRecording.added)}
                            aria-required={Boolean(reviewRecording.added)}
                          />
                        </label>
                        <label>
                          副标题
                          <input
                            value={reviewRecording.subtitle}
                            disabled={savingReview}
                            onChange={(event) =>
                              setReviewRecording((current) => ({
                                ...current,
                                added: true,
                                subtitle: event.target.value,
                              }))
                            }
                            placeholder="例如：核心观点 · 16 分钟阅读"
                          />
                        </label>
                        <label className="full">
                          飞书链接（可选，留空显示“暂未配置链接”）
                          <input
                            value={reviewRecording.url}
                            disabled={savingReview}
                            onChange={(event) =>
                              setReviewRecording((current) => ({
                                ...current,
                                added: true,
                                url: event.target.value,
                              }))
                            }
                            placeholder="https://feishu.cn/...（需在服务端白名单内）"
                            inputMode="url"
                          />
                        </label>
                      </div>
                    </div>
                    <div className="full" style={{ display: "grid", gap: 8 }}>
                      <b style={{ color: "var(--navy)" }}>活动资料（可选，飞书链接）</b>
                      <p>
                        {reviewMaterials.added
                          ? reviewMaterials.enabled
                            ? "已添加 · 前台展示"
                            : "已添加 · 暂时隐藏"
                          : "未添加"}
                        。链接留空时显示“暂未配置链接”。
                      </p>
                      <button
                        type="button"
                        className="secondary-button"
                        disabled={savingReview}
                        onClick={() =>
                          setReviewMaterials((current) => ({
                            ...current,
                            added: !current.added,
                            enabled: !current.added,
                          }))
                        }
                      >
                        {reviewMaterials.added ? "移除卡片" : "添加活动资料卡片"}
                      </button>
                      <label style={{ display: "inline-flex", alignItems: "center", gap: 8 }}>
                        <input
                          type="checkbox"
                          checked={reviewMaterials.enabled}
                          disabled={savingReview}
                          onChange={(event) =>
                            setReviewMaterials((current) => ({
                              ...current,
                              added: true,
                              enabled: event.target.checked,
                            }))
                          }
                        />
                        前台展示活动资料
                      </label>
                      <div className="field-grid">
                        <label>
                          显示名称{" "}
                          {reviewMaterials.added && (
                            <span className="required-mark" aria-hidden="true">
                              *
                            </span>
                          )}
                          <input
                            value={reviewMaterials.title}
                            disabled={savingReview}
                            onChange={(event) =>
                              setReviewMaterials((current) => ({
                                ...current,
                                added: true,
                                title: event.target.value,
                              }))
                            }
                            placeholder="例如：现场分享资料合集"
                            required={Boolean(reviewMaterials.added)}
                            aria-required={Boolean(reviewMaterials.added)}
                          />
                        </label>
                        <label>
                          副标题
                          <input
                            value={reviewMaterials.subtitle}
                            disabled={savingReview}
                            onChange={(event) =>
                              setReviewMaterials((current) => ({
                                ...current,
                                added: true,
                                subtitle: event.target.value,
                              }))
                            }
                            placeholder="例如：活动资料整理"
                          />
                        </label>
                        <label className="full">
                          飞书链接（可选，留空显示“暂未配置链接”）
                          <input
                            value={reviewMaterials.url}
                            disabled={savingReview}
                            onChange={(event) =>
                              setReviewMaterials((current) => ({
                                ...current,
                                added: true,
                                url: event.target.value,
                              }))
                            }
                            placeholder="https://feishu.cn/...（需在服务端白名单内）"
                            inputMode="url"
                          />
                        </label>
                      </div>
                    </div>
                  </div>
                  <div style={{ display: "flex", justifyContent: "flex-end", marginTop: 10 }}>
                    <button
                      type="button"
                      className="primary-button"
                      disabled={savingReview}
                      onClick={() => void saveReviewResource()}
                    >
                      {savingReview
                        ? "正在发布…"
                        : reviewSubmitPending
                          ? "重试上次发布"
                          : "保存并发布活动回顾"}
                    </button>
                  </div>
                </div>
              ) : (
                <p className="brand-hero-hint" style={{ marginTop: 14 }}>
                  <Info /> 活动完成后才能添加本期视频回顾；当前状态不会提前公开资源。
                </p>
              )}
              {reviewStatus.instance_review_document_count > 0 ? (
                (() => {
                  const path = `/pages/activity-review/index?instance_id=${encodeURIComponent(detail.instance.id)}`;
                  return (
                    <div className="target-summary" style={{ marginTop: 14 }}>
                      <b>整期回顾入口</b>
                      <Link
                        className="table-link"
                        href={`/past-activities/${encodeURIComponent(detail.instance.id)}/resources`}
                      >
                        编辑已发布资料与视频回顾
                      </Link>
                      <span>
                        <code>{path}</code>
                      </span>
                      <button
                        className="secondary-button"
                        type="button"
                        onClick={() => void copyReviewPath(path)}
                        disabled={
                          !reviewStatus.public_review_eligible ||
                          typeof navigator === "undefined" ||
                          !navigator.clipboard
                        }
                      >
                        {copiedReviewPath === path ? "已复制" : "复制路径"}
                      </button>
                    </div>
                  );
                })()
              ) : reviewStatus.public_session_resource_count > 0 ? (
                <p className="brand-hero-hint" style={{ marginTop: 14 }}>
                  <Info /> 本期没有整期资料，请从下方具体场次入口查看视频或资料。
                </p>
              ) : null}
              <div style={{ display: "grid", gap: 8, marginTop: 12 }}>
                {reviewStatus.sessions.map((session) => {
                  const sessionTitle =
                    detail.sessions.find((item) => item.id === session.session_id)?.title ||
                    "未命名场次";
                  const path = `/pages/activity-review/index?instance_id=${encodeURIComponent(detail.instance.id)}&session_id=${encodeURIComponent(session.session_id)}`;
                  const draft = sessionReviewDrafts[session.session_id] || {
                    title: "本场视频回顾",
                    description: "",
                    videoURL: "",
                  };
                  const sessionCanEdit = ["ended", "archived"].includes(session.status);
                  return (
                    <div
                      key={session.session_id}
                      className="target-summary"
                      style={{ display: "grid", gap: 8 }}
                    >
                      <div
                        style={{ display: "flex", gap: 8, alignItems: "center", flexWrap: "wrap" }}
                      >
                        <b>{sessionTitle}</b>
                        <span>
                          {statusLabel[session.status] || session.status} ·{" "}
                          {session.public_resource_count} 个公开回顾文档
                        </span>
                        <span>
                          {session.public_review_available ? "已进入公开回顾" : "暂无公开资源"}
                        </span>
                        {session.public_review_available && (
                          <>
                            <span>
                              <code>{path}</code>
                            </span>
                            <button
                              className="secondary-button"
                              type="button"
                              onClick={() => void copyReviewPath(path)}
                              disabled={
                                !reviewStatus.public_review_eligible ||
                                typeof navigator === "undefined" ||
                                !navigator.clipboard
                              }
                            >
                              {copiedReviewPath === path ? "已复制" : "复制场次路径"}
                            </button>
                          </>
                        )}
                      </div>
                      {sessionCanEdit && reviewStatus.public_review_eligible ? (
                        <div
                          className="review-resource-form"
                          style={{
                            padding: 12,
                            border: "1px solid var(--line)",
                            borderRadius: 8,
                            background: "#fbfcfe",
                          }}
                        >
                          <p className="brand-hero-hint" style={{ margin: "0 0 8px" }}>
                            添加本场录播或资料链接。当前资源链路只接受经白名单审核的视频号 HTTPS
                            链接，文件上传与任意外链保持关闭。
                          </p>
                          <div className="field-grid">
                            <label>
                              场次资源标题 <span className="required-mark">*</span>
                              <input
                                value={draft.title}
                                disabled={savingSessionReviewId !== ""}
                                onChange={(event) =>
                                  setSessionReviewDrafts((current) => ({
                                    ...current,
                                    [session.session_id]: { ...draft, title: event.target.value },
                                  }))
                                }
                                placeholder="例如：本场视频回顾"
                                required={true}
                                aria-required={true}
                              />
                            </label>
                            <label>
                              视频号链接 <span className="required-mark">*</span>
                              <input
                                value={draft.videoURL}
                                disabled={savingSessionReviewId !== ""}
                                onChange={(event) =>
                                  setSessionReviewDrafts((current) => ({
                                    ...current,
                                    [session.session_id]: {
                                      ...draft,
                                      videoURL: event.target.value,
                                    },
                                  }))
                                }
                                placeholder="https://…（需在服务端白名单内）"
                                inputMode="url"
                                required={true}
                                aria-required={true}
                              />
                            </label>
                            <label className="full">
                              资源描述（可选）
                              <textarea
                                rows={2}
                                value={draft.description}
                                disabled={savingSessionReviewId !== ""}
                                onChange={(event) =>
                                  setSessionReviewDrafts((current) => ({
                                    ...current,
                                    [session.session_id]: {
                                      ...draft,
                                      description: event.target.value,
                                    },
                                  }))
                                }
                                placeholder="补充本场内容或资料说明"
                              />
                            </label>
                          </div>
                          <div
                            style={{ display: "flex", justifyContent: "flex-end", marginTop: 8 }}
                          >
                            <button
                              type="button"
                              className="secondary-button"
                              disabled={savingSessionReviewId !== ""}
                              onClick={() => void saveSessionReviewResource(session.session_id)}
                            >
                              {savingSessionReviewId === session.session_id
                                ? "正在发布…"
                                : "保存并发布场次资源"}
                            </button>
                          </div>
                        </div>
                      ) : !sessionCanEdit ? (
                        <span className="brand-hero-hint">
                          <Info /> 场次结束后才能维护公开资源，当前仅展示服务端状态。
                        </span>
                      ) : (
                        <span className="brand-hero-hint">
                          <Info /> 活动完成后才能发布场次资源。
                        </span>
                      )}
                    </div>
                  );
                })}
                {reviewStatus.sessions.length === 0 && (
                  <p className="brand-hero-hint">本期尚未配置场次。</p>
                )}
              </div>
            </>
          ) : (
            <p className="brand-hero-hint">
              <Info /> 暂无公开回顾状态。
            </p>
          )}
        </div>
      </section>
      <section className="panel">
        <div className="panel-head">
          <div>
            <h2>封面与图文详情</h2>
            <p>更换封面或调整图文详情，保存后立即在小程序生效。</p>
          </div>
          <div className="panel-actions">
            {!editingContent && (
              <button
                className="secondary-button"
                onClick={startContentEdit}
                disabled={publishing || completing || savingContent}
              >
                <Pencil /> 编辑
              </button>
            )}
          </div>
        </div>
        <div style={{ padding: "18px 22px", borderTop: "1px solid var(--line)" }}>
          {editingContent ? (
            <>
              <p className="brand-hero-hint" style={{ marginTop: 0 }}>
                <Info />{" "}
                封面与图文详情是实时内容：点击「保存修改」后立即对小程序访客生效，不需要重新发布。
              </p>
              <div className="field-grid">
                <div className="full">
                  <label>
                    活动封面（JPG / PNG / WebP，最大 5 MiB）
                    <input
                      ref={editCoverFileInput}
                      type="file"
                      accept={activityImageAccept.join(",")}
                      disabled={uploadingEditCover || savingContent}
                      onChange={(event) => {
                        const file = event.target.files?.[0];
                        if (file) void uploadEditCoverImage(file);
                      }}
                    />
                  </label>
                  {editCoverUrl ? (
                    <div className="cover-preview-row">
                      <div
                        className="cover-preview-thumb"
                        role="img"
                        aria-label="活动封面预览"
                        style={{
                          backgroundImage: `url(${JSON.stringify(resolveActivityImageUrl(editCoverUrl))})`,
                        }}
                      />
                      <p className="brand-hero-hint">
                        <ImagePlus /> 已设置封面，选择新文件可替换；清除后保存即移除封面。
                      </p>
                      <button
                        type="button"
                        className="secondary-button"
                        disabled={uploadingEditCover || savingContent}
                        onClick={() => setEditCoverUrl("")}
                      >
                        <Trash2 /> 清除封面
                      </button>
                    </div>
                  ) : (
                    <p className="brand-hero-hint">
                      <ImagePlus /> 当前未设置封面，保存后将保持无封面。
                    </p>
                  )}
                </div>
                <div className="full">
                  <span
                    style={{ display: "block", marginBottom: 6, color: "#617084", fontSize: 11 }}
                  >
                    图文详情（文字与图片块按顺序展示；标题填“简介”的文字块可作为活动简介）
                  </span>
                  <DetailBlocksEditor
                    blocks={editBlocks}
                    onChange={setEditBlocks}
                    disabled={savingContent}
                    onError={setMessage}
                  />
                </div>
              </div>
              <div style={{ display: "flex", gap: 10, marginTop: 14, justifyContent: "flex-end" }}>
                <button
                  type="button"
                  className="secondary-button"
                  disabled={savingContent || uploadingEditCover}
                  onClick={() => setEditingContent(false)}
                >
                  <X /> 取消
                </button>
                <button
                  type="button"
                  className="primary-button"
                  disabled={savingContent || uploadingEditCover}
                  onClick={() => void saveContentEdit()}
                >
                  <CheckCircle2 /> {savingContent ? "正在保存…" : "保存修改"}
                </button>
              </div>
            </>
          ) : detail.instance.detail_blocks && detail.instance.detail_blocks.length > 0 ? (
            <div style={{ display: "grid", gap: 14 }}>
              {detail.instance.detail_blocks.map((block, index) =>
                block.type === "text" ? (
                  <div key={index}>
                    <h3 style={{ margin: 0, color: "var(--navy)", fontSize: 14 }}>{block.title}</h3>
                    <p
                      style={{
                        margin: "6px 0 0",
                        color: "var(--muted)",
                        fontSize: 12,
                        lineHeight: 1.7,
                        whiteSpace: "pre-wrap",
                      }}
                    >
                      {block.body}
                    </p>
                  </div>
                ) : (
                  <div key={index}>
                    <div
                      className="cover-preview-thumb"
                      role="img"
                      aria-label={block.caption || `图文详情第 ${index + 1} 块图片`}
                      style={{
                        backgroundImage: `url(${JSON.stringify(resolveActivityImageUrl(block.url))})`,
                      }}
                    />
                    {block.caption && <p className="brand-hero-hint">{block.caption}</p>}
                  </div>
                ),
              )}
            </div>
          ) : (
            <p className="brand-hero-hint">
              <ImagePlus /> 尚未设置图文详情，点击「编辑」可添加文字与图片块。
            </p>
          )}
        </div>
      </section>
      <section className="panel">
        <div className="panel-head">
          <div>
            <h2>报名信息模板</h2>
            <p>昵称和手机号由平台报名页自动带入；以下是本期额外问卷字段。</p>
          </div>
        </div>
        <div style={{ padding: "18px 22px", borderTop: "1px solid var(--line)" }}>
          {!detail.questionnaire.configured ? (
            <p className="brand-hero-hint">
              <Info /> 未配置额外字段，报名页仍会要求昵称和手机号。
            </p>
          ) : (
            <>
              <p className="brand-hero-hint">
                用途：{detail.questionnaire.privacy_purpose} · 政策版本：
                {detail.questionnaire.privacy_policy_version} · 问卷 v{detail.questionnaire.version}
              </p>
              <div style={{ display: "grid", gap: 8 }}>
                {detail.questionnaire.fields.map((field) => (
                  <div key={field.field_id} className="target-summary">
                    <b>
                      {field.label}
                      {field.required && (
                        <span className="required-mark" aria-hidden="true">
                          *
                        </span>
                      )}
                    </b>
                    <span>
                      {questionnaireFieldTypeLabel(field.type)} · {field.code}
                      {field.help_text ? ` · ${field.help_text}` : ""}
                    </span>
                    {field.options.length > 0 && (
                      <span>选项：{field.options.map((option) => option.label).join("、")}</span>
                    )}
                  </div>
                ))}
              </div>
            </>
          )}
        </div>
      </section>
      <aside className="truth-note">
        <CheckCircle2 />
        <div>
          <b>发布前会自动检查</b>
          <p>系统会核对活动内容、报名问卷、人物资料和全部场次；检查通过后才会更新到小程序。</p>
        </div>
      </aside>
    </div>
  );
}
