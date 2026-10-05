"use client";

import { useRef, useState } from "react";
import { api, definitiveFailure, displayError, operationHeaders, operationKey } from "@/lib/api";
import { requestRequiredReason } from "@/lib/required-reason-dialog";
import {
  clearSessionCancellation,
  readSessionCancellation,
  saveSessionCancellation,
  validSessionCancellationPreview,
  type SessionCancellationPreview,
  type SessionCancellationRecovery,
} from "@/lib/session-cancellation-recovery";
import type { Session } from "@/lib/types";

export function SessionCancellation({
  session,
  disabled,
  onBusyChange,
  onComplete,
}: {
  session: Session;
  disabled: boolean;
  onBusyChange: (value: boolean) => void;
  onComplete: (notice?: string) => Promise<void>;
}) {
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const submitting = useRef(false);

  async function cancel() {
    if (submitting.current || disabled) return;
    submitting.current = true;
    setBusy(true);
    onBusyChange(true);
    setMessage("");
    let pending: SessionCancellationRecovery | null = null;
    try {
      pending = readSessionCancellation(session.id);
      if (!pending && session.status === "cancelled") {
        setMessage("本场次已取消，请在报名及退款页面核对后续处理。");
        await onComplete("本场次已取消，请在报名及退款页面核对后续处理。");
        return;
      }
      const reason =
        pending?.reason ||
        (await requestRequiredReason({
          title: "取消本场次",
          description: "将核对本场次的报名与退款影响，其他场次保持原状。",
        }));
      if (!reason) return;
      pending ||= {
        version: 1,
        sessionId: session.id,
        reason,
        previewOperation: operationKey(),
        operation: operationKey(),
        submitted: false,
      };
      // Persist before each write: an unavailable store must prevent submission.
      saveSessionCancellation(pending);
      if (!pending.preview) {
        const preview = await api<SessionCancellationPreview>(
          `/sessions/${session.id}/cancellation-previews`,
          {
            method: "POST",
            headers: operationHeaders(pending.previewOperation),
            body: "{}",
          },
        );
        if (!validSessionCancellationPreview(preview, session.id))
          throw new Error("取消预览暂时无法确认，请用原请求重试");
        pending = { ...pending, preview };
        saveSessionCancellation(pending);
      }
      const preview = pending.preview!;
      const confirmation = `仅取消「${session.title}」。将关闭 ${preview.cancelled_registration_count} 个报名、${preview.pending_order_count} 个待支付订单，记录 ¥${(preview.requested_refund_cents / 100).toFixed(2)} 退款，调整 ${preview.coupon_adjustment_count} 张优惠券。${preview.unknown_payment_count ? `另有 ${preview.unknown_payment_count} 个支付结果待核实，后续按实际结果处理。` : ""}\n原因：${pending.reason}\n需人工通知报名用户。${pending.submitted ? "上次结果未知，此次沿用同一请求核对。" : ""}\n确认取消？`;
      if (!window.confirm(confirmation)) {
        if (!pending.submitted) clearSessionCancellation(session.id);
        setMessage(
          pending.submitted ? "已保留原请求，稍后可继续核对。" : "已放弃取消，场次保持原状。",
        );
        return;
      }
      pending = { ...pending, submitted: true };
      saveSessionCancellation(pending);
      const result = await api<{ session: Session; receipt_id: string; cancelled_at: string }>(
        `/sessions/${session.id}/cancellation`,
        {
          method: "POST",
          headers: operationHeaders(pending.operation),
          body: JSON.stringify({
            preview_id: preview.id,
            expected_session_version: preview.expected_session_version,
            reason: pending.reason,
          }),
        },
      );
      if (
        result?.session?.id !== session.id ||
        result.session.status !== "cancelled" ||
        !result.receipt_id ||
        !Number.isFinite(Date.parse(result.cancelled_at))
      ) {
        throw new Error("取消回执暂时无法确认，请用原请求核对");
      }
      clearSessionCancellation(session.id);
      setMessage("本场次已取消，请按报名名单通知用户并处理退款。其他场次保持原状。");
      await onComplete("本场次已取消，请按报名名单通知用户并处理退款。其他场次保持原状。").catch(
        () => setMessage("取消结果已确认，活动详情刷新失败，请稍后刷新核对。"),
      );
    } catch (error) {
      if (definitiveFailure(error)) {
        try {
          clearSessionCancellation(session.id);
        } catch {
          /* keep the original request if storage is unavailable */
        }
        setMessage(displayError(error));
        await onComplete(displayError(error)).catch(() => {});
      } else {
        setMessage(`${displayError(error)}；再次操作将沿用原请求，请先核对最新状态。`);
      }
    } finally {
      submitting.current = false;
      setBusy(false);
      onBusyChange(false);
    }
  }

  return (
    <div>
      {(session.status === "published" || session.status === "cancelled") && (
        <button
          type="button"
          className="secondary-button"
          disabled={disabled || busy}
          onClick={() => void cancel()}
        >
          {busy ? "正在核对…" : session.status === "cancelled" ? "核对取消结果" : "取消本场次"}
        </button>
      )}
      {message && (
        <p role="status" className="field-help">
          {message}
        </p>
      )}
    </div>
  );
}
