"use client";
import { useRef, useState } from "react";
import { api, definitiveFailure, displayError, operationHeaders, operationKey } from "@/lib/api";
import { requestRequiredReason } from "@/lib/required-reason-dialog";
import {
  checkinRevocationKey,
  parseCheckinRevocation,
  type CheckinRevocationRecovery,
} from "@/lib/checkin-revocation-recovery";
import type { RegistrationDetail } from "@/lib/types";
type CheckinFact = {
  checkin_id: string;
  registration_id: string;
  status: "checked_in" | "revoked";
  version: number;
  revocation_reason?: string;
};

export function CheckinRevocation({
  detail,
  onComplete,
}: {
  detail: RegistrationDetail;
  onComplete: (notice: string) => Promise<void>;
}) {
  const [busy, setBusy] = useState(false),
    [message, setMessage] = useState("");
  const working = useRef(false);
  async function revoke() {
    if (working.current) return;
    working.current = true;
    setBusy(true);
    setMessage("");
    const key = checkinRevocationKey(detail.id);
    try {
      const stored = window.sessionStorage.getItem(key);
      let pending: CheckinRevocationRecovery | null = stored
        ? parseCheckinRevocation(JSON.parse(stored), detail.id)
        : null;
      const current = await api<CheckinFact>(`/registrations/${detail.id}/checkin`);
      if (
        current.registration_id !== detail.id ||
        current.checkin_id !== detail.checkin_id ||
        !Number.isSafeInteger(current.version) ||
        current.version < 1
      )
        throw new Error("签到记录已变化，请刷新后核对");
      if (current.status === "revoked" && !pending) {
        await onComplete(
          `签到已撤销${current.revocation_reason ? `：${current.revocation_reason}` : ""}`,
        );
        return;
      }
      if (current.status !== "checked_in" && current.status !== "revoked")
        throw new Error("签到状态暂时无法确认");
      if (!pending) {
        const reason = await requestRequiredReason({
          title: "撤销签到",
          description: "撤销会保留原签到事实，并触发贡献与优惠券权益的纠错处理。",
        });
        if (!reason) return;
        pending = {
          version: 1,
          registrationId: detail.id,
          checkinId: current.checkin_id,
          expectedVersion: current.version,
          reason,
          operation: operationKey(),
        };
      }
      if (
        !window.confirm(
          `${stored ? "上次撤销结果未知，本次沿用同一请求核对。\n" : ""}确认撤销本笔签到？\n原因：${pending.reason}\n原签到历史保留，贡献与优惠券按原事实追加纠错。`,
        )
      )
        return;
      window.sessionStorage.setItem(key, JSON.stringify(pending));
      const result = await api<{ checkin: CheckinFact; event_id: string }>(
        `/registrations/${detail.id}/checkin-revocations`,
        {
          method: "POST",
          headers: operationHeaders(pending.operation),
          body: JSON.stringify({
            checkin_id: pending.checkinId,
            expected_version: pending.expectedVersion,
            reason: pending.reason,
          }),
        },
      );
      if (
        result?.checkin?.checkin_id !== pending.checkinId ||
        result.checkin.registration_id !== detail.id ||
        result.checkin.status !== "revoked" ||
        result.checkin.version !== pending.expectedVersion + 1 ||
        !result.event_id
      )
        throw new Error("撤销回执暂时无法确认，请用原请求核对");
      window.sessionStorage.removeItem(key);
      await onComplete("签到已撤销；原记录已保留，贡献和优惠券纠错任务已记录。").catch(() =>
        setMessage("撤销结果已确认，报名详情刷新失败，请稍后刷新核对。"),
      );
    } catch (error) {
      if (definitiveFailure(error)) {
        try {
          window.sessionStorage.removeItem(key);
        } catch {
          /* retain the original operation */
        }
      }
      setMessage(
        `${displayError(error)}${definitiveFailure(error) ? "" : "；再次操作将沿用原请求核对，请勿重复建立撤销请求。"}`,
      );
    } finally {
      working.current = false;
      setBusy(false);
    }
  }
  return (
    <section className="panel">
      <h3>签到纠错</h3>
      <p className="field-help">
        仅系统管理员可撤销签到。原签到、撤销原因及相关权益处理历史均保留。
      </p>
      <button
        type="button"
        className="secondary-button"
        disabled={busy}
        onClick={() => void revoke()}
      >
        {busy ? "正在核对…" : detail.checkin_status === "revoked" ? "核对撤销结果" : "撤销签到"}
      </button>
      {message && (
        <p role="status" className="inline-message error">
          {message}
        </p>
      )}
    </section>
  );
}
