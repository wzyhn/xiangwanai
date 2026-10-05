"use client";
import { useRef, useState } from "react";
import { api, definitiveFailure, displayError, operationHeaders, operationKey } from "@/lib/api";
import { requestRequiredReason } from "@/lib/required-reason-dialog";
import {
  parsePeopleBindingRecovery,
  peopleBindingRecoveryKey,
  type PeopleBindingRecovery,
} from "@/lib/people-binding-recovery";
import type { Person } from "@/lib/types";
type Binding = {
  id: string;
  people_profile_id: string;
  status: "active" | "revoked";
  version: number;
};
type Invitation = {
  id: string;
  people_profile_id: string;
  profile_version: number;
  status: string;
  version: number;
  expires_at: string;
  code?: string;
};
export function PeopleBindingControls({
  person,
  onComplete,
}: {
  person: Person;
  onComplete: (notice?: string) => Promise<void>;
}) {
  const [busy, setBusy] = useState(false),
    [message, setMessage] = useState(""),
    [invitation, setInvitation] = useState<Invitation | null>(null);
  const working = useRef(false);
  async function operate(cancelInvitation = false) {
    if (working.current) return;
    working.current = true;
    setBusy(true);
    setMessage("");
    const key = peopleBindingRecoveryKey(person.id);
    try {
      const stored = window.sessionStorage.getItem(key);
      let pending: PeopleBindingRecovery | null = stored
        ? parsePeopleBindingRecovery(JSON.parse(stored), person.id)
        : null;
      if (!pending) {
        const kind = cancelInvitation
          ? "invitation_revoke"
          : person.has_active_binding
            ? "revoke"
            : "invite";
        const reason = await requestRequiredReason({
          title:
            kind === "invitation_revoke"
              ? "撤销未使用邀请"
              : kind === "invite"
                ? `邀请“${person.display_name}”本人绑定`
                : `撤销“${person.display_name}”的账号绑定`,
          description:
            kind === "invite"
              ? "请先核对本人；邀请仅由本人微信登录并明确确认后生效。"
              : "公开人物资料及历史角色、贡献和优惠券事实保留。",
        });
        if (!reason) return;
        let expectedVersion = person.version,
          bindingId: string | undefined;
        if (kind === "invitation_revoke") {
          if (!invitation || invitation.status !== "pending")
            throw new Error("邀请状态已变化，请刷新核对");
          expectedVersion = invitation.version;
          bindingId = invitation.id;
        } else if (kind === "revoke") {
          const binding = await api<Binding>(`/people/${person.id}/binding`);
          if (
            binding.people_profile_id !== person.id ||
            binding.status !== "active" ||
            !Number.isSafeInteger(binding.version) ||
            binding.version < 1 ||
            !binding.id
          )
            throw new Error("绑定状态已变化，请刷新核对");
          expectedVersion = binding.version;
          bindingId = binding.id;
        }
        pending = {
          version: 1,
          kind,
          profileId: person.id,
          expectedVersion,
          operation: operationKey(),
          reason,
          ...(bindingId ? { bindingId } : {}),
        };
      }
      if (
        !window.confirm(
          `${stored ? "上次结果未知，将沿用原请求。\n" : ""}${pending.kind === "invite" ? "生成 24 小时有效的本人确认邀请码？原未使用邀请将失效。" : "确认撤销账号绑定？原始身份和权益历史保留。"}\n人物：${person.display_name}\n原因：${pending.reason}`,
        )
      )
        return;
      window.sessionStorage.setItem(key, JSON.stringify(pending));
      setInvitation(null);
      const result = await api<Invitation | Binding>(
        `/people/${person.id}/${pending.kind === "invite" ? "binding-invitations" : pending.kind === "invitation_revoke" ? "binding-invitation-revocations" : "binding-revocations"}`,
        {
          method: "POST",
          headers: operationHeaders(pending.operation),
          body: JSON.stringify({
            expected_version: pending.expectedVersion,
            reason: pending.reason,
            ...(pending.bindingId ? { binding_id: pending.bindingId } : {}),
          }),
        },
      );
      if (result.people_profile_id !== person.id)
        throw new Error("绑定回执身份不一致，请用原请求核对");
      if (pending.kind === "invite") {
        const value = result as Invitation;
        if (
          value.id !== pending.operation ||
          value.profile_version !== pending.expectedVersion ||
          !["pending", "accepted", "revoked"].includes(value.status) ||
          !Number.isFinite(Date.parse(value.expires_at)) ||
          (value.code && !/^[0-9a-f-]{36}\.[A-Za-z0-9_-]{43}$/.test(value.code))
        )
          throw new Error("邀请回执无法确认，请用原请求核对");
        setInvitation(value);
        setMessage(
          value.code
            ? "请将邀请码交给核对后的本人，在小程序“我的权益 → 绑定人物资料”中确认。"
            : "原邀请已使用、撤销或过期，请刷新查看绑定状态。",
        );
      } else {
        if (
          result.id !== pending.bindingId ||
          result.status !== "revoked" ||
          result.version !== pending.expectedVersion + 1
        )
          throw new Error("撤销回执无法确认，请用原请求核对");
        setMessage(
          pending.kind === "invitation_revoke"
            ? "未使用邀请已撤销，原邀请码立即失效。"
            : "账号绑定已撤销，历史事实已保留。",
        );
      }
      window.sessionStorage.removeItem(key);
      // Refreshing the loading table unmounts this row and its private code.
      if (pending.kind !== "invite" || !(result as Invitation).code) {
        await onComplete(
          pending.kind === "invitation_revoke"
            ? "未使用邀请已撤销，原邀请码立即失效。"
            : pending.kind === "revoke"
              ? "账号绑定已撤销，历史事实已保留。"
              : "原邀请状态已核对，请查看当前绑定。",
        ).catch(() => setMessage("操作已确认，列表刷新失败，请稍后刷新核对。"));
      }
    } catch (error) {
      if (definitiveFailure(error)) {
        try {
          window.sessionStorage.removeItem(key);
        } catch {
          /* retain recovery */
        }
      }
      setMessage(
        `${displayError(error)}${definitiveFailure(error) ? "" : "；再次操作将沿用原请求核对。"}`,
      );
    } finally {
      working.current = false;
      setBusy(false);
    }
  }
  return (
    <div>
      <button type="button" className="table-link" disabled={busy} onClick={() => void operate()}>
        {busy ? "处理中…" : person.has_active_binding ? "撤销账号绑定" : "邀请本人绑定"}
      </button>
      {message && (
        <p role="status" className="field-help">
          {message}
        </p>
      )}
      {invitation?.code && (
        <div>
          <label>
            本人确认邀请码
            <input readOnly value={invitation.code} aria-label="本人确认邀请码" />
          </label>
          <p className="field-help">
            有效至{" "}
            {new Date(invitation.expires_at).toLocaleString("zh-CN", { timeZone: "Asia/Shanghai" })}
            。请勿公开发布邀请码。
          </p>
          <button
            type="button"
            className="table-link"
            onClick={() => {
              void navigator.clipboard
                .writeText(invitation.code || "")
                .then(() => setMessage("邀请码已复制，请交给本人确认。"))
                .catch(() => setMessage("复制失败，请手动选择邀请码复制。"));
            }}
          >
            复制邀请码
          </button>
          <button
            type="button"
            className="table-link"
            onClick={() =>
              setInvitation((current) => (current ? { ...current, code: undefined } : null))
            }
          >
            关闭显示
          </button>
        </div>
      )}
      {invitation?.status === "pending" && (
        <button
          type="button"
          className="table-link"
          disabled={busy}
          onClick={() => void operate(true)}
        >
          撤销未使用邀请
        </button>
      )}
    </div>
  );
}
