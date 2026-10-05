"use client";

import { ArrowLeft, ArrowRight, RefreshCw } from "lucide-react";
import Link from "next/link";
import { useParams } from "next/navigation";
import { useEffect, useState } from "react";
import { ApiError, api, definitiveFailure, displayError, operationHeaders, operationKey } from "@/lib/api";
import { canReadAuditLog, canReadFinance } from "@/lib/admin-permissions";
import { formatCentsAsYuan } from "@/lib/money";
import {
  clearPendingRefundAction, readPendingRefundAction, savePendingRefundAction,
  type PendingRefundAction, type RefundAction,
} from "@/lib/refund-action-recovery";
import { formatShanghaiDateTime } from "@/lib/time";
import type { AuthStatus, RefundCaseDetail } from "@/lib/types";

const statusLabels: Record<string, string> = {
  pending_manual: "待人工处理",
  processing: "处理中",
  failed: "处理失败",
  refunded: "已退款",
  rejected: "不予退款",
};

const reasonLabels: Record<string, string> = {
  hold_expired_after_payment: "支付后名额已过期",
  order_closed_after_payment: "订单关闭后到账",
  session_unavailable_after_payment: "活动场次不可参加",
  user_cancelled: "用户取消报名",
  session_cancelled: "活动场次取消",
  instance_cancelled: "整期活动下架",
  operator_adjustment: "人工更正",
};

const eventLabels: Record<string, string> = {
  processing_started: "开始处理",
  refund_completed: "退款完成",
  refund_failed: "处理失败",
  refund_rejected: "退款驳回",
};

const actionLabels: Record<RefundAction, string> = {
  start: "开始处理", fail: "登记处理失败", complete: "确认已退款", reject: "登记不予退款",
};

type RefundActionReceipt = {
  case_id: string;
  status: RefundCaseDetail["case"]["status"];
  version: number;
  successful_refund_cents: string;
  event_sequence: number;
};

function availableActions(detail: RefundCaseDetail, auth: AuthStatus | null): RefundAction[] {
  if (!auth) return [];
  const actions: RefundAction[] = [];
  if (canReadFinance(auth)) {
    if (["pending_manual", "failed"].includes(detail.case.status)) actions.push("start");
    if (detail.case.status === "processing") actions.push("fail");
  }
  if (canReadAuditLog(auth)) {
    if (detail.case.status === "processing") actions.push("complete");
    if (["pending_manual", "processing", "failed"].includes(detail.case.status)) actions.push("reject");
  }
  return actions;
}

export default function RefundCasePage() {
  const params = useParams<{ caseId: string }>();
  const [detail, setDetail] = useState<RefundCaseDetail | null>(null);
  const [auth, setAuth] = useState<AuthStatus | null>(null);
  const [refreshKey, setRefreshKey] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [pending, setPending] = useState<PendingRefundAction | null>(null);
  const [action, setAction] = useState<RefundAction | "">("");
  const [failureReason, setFailureReason] = useState("");
  const [externalRefundID, setExternalRefundID] = useState("");
  const [evidenceReference, setEvidenceReference] = useState("");
  const [evidenceChecked, setEvidenceChecked] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [actionError, setActionError] = useState("");
  const [actionSuccess, setActionSuccess] = useState("");

  useEffect(() => {
    const restored = readPendingRefundAction(params.caseId);
    setPending(restored);
    setAction(restored?.action || "");
    setFailureReason("");
    setExternalRefundID("");
    setEvidenceReference("");
    setEvidenceChecked(false);
    setActionError("");
    setActionSuccess("");
  }, [params.caseId]);

  useEffect(() => {
    const request = new AbortController();
    setDetail(null);
    setAuth(null);
    setLoading(true);
    setError("");
    void Promise.all([
      api<RefundCaseDetail>(`/refund-cases/${encodeURIComponent(params.caseId)}`, { signal: request.signal }),
      api<AuthStatus>("/auth/status", { signal: request.signal }),
    ])
      .then(([value, status]) => {
        if (request.signal.aborted) return;
        if (value.case.case_id !== params.caseId || !Array.isArray(value.events)) {
          throw new Error("退款详情与当前记录不匹配");
        }
        setDetail(value);
        setAuth(status);
      })
      .catch((reason: unknown) => {
        if (!request.signal.aborted) setError(displayError(reason));
      })
      .finally(() => {
        if (!request.signal.aborted) setLoading(false);
      });
    return () => request.abort();
  }, [params.caseId, refreshKey]);

  function chooseAction(next: RefundAction) {
    if (pending || submitting) return;
    setAction(next);
    setFailureReason("");
    setExternalRefundID("");
    setEvidenceReference("");
    setEvidenceChecked(false);
    setActionError("");
    setActionSuccess("");
  }

  async function submitAction() {
    if (!detail || !auth || !action || submitting ||
      !availableActions(detail, auth).includes(action) ||
      (pending && (pending.action !== action || pending.expectedVersion !== detail.case.version))) return;
    const reason = failureReason.trim();
    const refundID = externalRefundID.trim();
    const reference = evidenceReference.trim();
    if ((action === "fail" || action === "reject") && !reason) {
      setActionError("请填写处理原因。");
      return;
    }
    if (action === "complete" && (!refundID || !reference || !evidenceChecked)) {
      setActionError("请核对商户渠道的最终成功结果，并填写退款单号和官方记录引用。");
      return;
    }
    if ((action === "complete" || action === "reject") && !window.confirm(
      action === "complete"
        ? `确认已在商户渠道核实退款成功，金额为 ${formatCentsAsYuan(detail.case.requested_refund_cents)}？此操作不能撤销。`
        : "确认有明确业务依据、确实无需退款？此操作不能撤销。",
    )) return;
    const operation = pending || {
      caseId: detail.case.case_id,
      action,
      expectedVersion: detail.case.version,
      operationKey: operationKey(),
    };
    try {
      savePendingRefundAction(operation);
    } catch {
      setActionError("浏览器无法保存操作键，请启用本会话存储后重试。");
      return;
    }
    setPending(operation);
    setSubmitting(true);
    setActionError("");
    setActionSuccess("");
    try {
      const receipt = await api<RefundActionReceipt>(
        `/refund-cases/${encodeURIComponent(detail.case.case_id)}/actions`,
        {
          method: "POST",
          headers: operationHeaders(operation.operationKey),
          body: JSON.stringify({
            action,
            expected_version: operation.expectedVersion,
            successful_refund_cents: action === "complete" ? detail.case.requested_refund_cents : "0",
            external_refund_id: action === "complete" ? refundID : "",
            evidence_reference: action === "complete" ? reference : "",
            failure_reason: action === "fail" || action === "reject" ? reason : "",
          }),
        },
      );
      if (receipt.case_id !== detail.case.case_id || receipt.version < operation.expectedVersion + 1) {
        throw new Error("服务返回的退款回执与当前记录不匹配，请核对时间线。");
      }
      clearPendingRefundAction(detail.case.case_id);
      setPending(null);
      setAction("");
      setFailureReason("");
      setExternalRefundID("");
      setEvidenceReference("");
      setEvidenceChecked(false);
      setActionSuccess(`${actionLabels[action]}已记录，正在刷新服务端时间线。`);
      setRefreshKey((value) => value + 1);
    } catch (reason: unknown) {
      if (definitiveFailure(reason) && !(reason instanceof ApiError && reason.status === 409)) {
        clearPendingRefundAction(detail.case.case_id);
        setPending(null);
        setActionError(displayError(reason));
      } else {
        setActionError(`${displayError(reason)}。结果尚未确认，请刷新时间线后使用原操作键重试；重试时须重新填写与上次完全相同的原因或凭证。`);
        setRefreshKey((value) => value + 1);
      }
    } finally {
      setSubmitting(false);
    }
  }

  const actions = detail ? availableActions(detail, auth) : [];
  const pendingVersionChanged = Boolean(pending && detail && pending.expectedVersion !== detail.case.version);

  return <div className="page-content detail-page">
    <Link className="back-link" href="/refunds"><ArrowLeft /> 返回退款待办</Link>
    {loading ? <div className="inline-message">正在读取退款详情…</div> : error ? <div className="inline-message error">{error}</div> : detail && <>
      <section className="detail-hero">
        <div><span className="eyebrow">退款记录</span><h2>{detail.case.instance_title}</h2><p>{detail.case.series_title} · {detail.case.session_title}</p></div>
        <span className={`status-pill ${detail.case.status}`}>{statusLabels[detail.case.status] || detail.case.status}</span>
      </section>
      <div className="fact-grid">
        <article><span>退款原因</span><b>{reasonLabels[detail.case.reason_code] || detail.case.reason_code}</b><small>以服务端登记为准</small></article>
        <article><span>应退金额</span><b>{formatCentsAsYuan(detail.case.requested_refund_cents)}</b><small>人民币</small></article>
        <article><span>已退金额</span><b>{formatCentsAsYuan(detail.case.successful_refund_cents)}</b><small>仅在退款完成后计入</small></article>
        <article><span>最近更新</span><b>{formatShanghaiDateTime(detail.case.updated_at)}</b><small>创建于 {formatShanghaiDateTime(detail.case.created_at)}</small></article>
      </div>
      {(actions.length > 0 || pending) && <section className="panel refund-action-panel">
        <div className="panel-head"><div><h3>人工退款处理</h3><p>先在商户渠道核对与办理；这里仅登记处理状态，不会发起渠道退款。</p></div></div>
        {actionSuccess && <div className="inline-message success">{actionSuccess}</div>}
        {pending && <div className="inline-message warning">
          检测到上次提交结果未确认：{actionLabels[pending.action]}，基于版本 {pending.expectedVersion}。
          {pendingVersionChanged
            ? "当前记录版本已变化，请先核对下方时间线，再清除此待核对操作；请勿直接重新登记外部退款。"
            : "刷新时间线后可用原操作键重试；页面不保存原因或财务凭证，请逐字重新填写。"}
        </div>}
        {actionError && <div className="inline-message error">{actionError}</div>}
        {pending && <button className="secondary-button" type="button" disabled={submitting} onClick={() => {
          if (!window.confirm("已核对退款时间线和商户渠道记录，确认清除本地待核对操作？此操作只清除浏览器操作键，不改变退款状态。")) return;
          clearPendingRefundAction(detail.case.case_id);
          setPending(null);
          setAction("");
          setActionError("");
          setRefreshKey((value) => value + 1);
        }}>已核对，清除待核对操作</button>}
        {!pending && <div className="refund-action-choices">{actions.map((option) => <button key={option} className="secondary-button" type="button" disabled={submitting} onClick={() => chooseAction(option)}>{actionLabels[option]}</button>)}</div>}
        {action && actions.includes(action) && !pendingVersionChanged && <div className="refund-action-form">
          <h4>{actionLabels[action]}</h4>
          {action === "start" && <p>财务人员领取该退款单，核对关联订单和应退金额后开始在外部渠道办理。</p>}
          {(action === "fail" || action === "reject") && <div className="field-grid"><label className="full">{action === "fail" ? "渠道失败或无法核实的原因" : "确实无需退款的业务依据"} <span className="required-mark">*</span>
            <textarea rows={3} maxLength={500} value={failureReason} onChange={(event) => setFailureReason(event.target.value)} required aria-required={true} />
          </label></div>}
          {action === "complete" && <>
            <p>只有另一名超级管理员核对商户渠道的最终成功结果后，才能确认完成。应退金额：<b>{formatCentsAsYuan(detail.case.requested_refund_cents)}</b>。</p>
            <div className="field-grid">
              <label>外部退款单号 <span className="required-mark">*</span><input maxLength={128} value={externalRefundID} onChange={(event) => setExternalRefundID(event.target.value)} required aria-required={true} /></label>
              <label>官方记录引用 <span className="required-mark">*</span><input maxLength={500} value={evidenceReference} onChange={(event) => setEvidenceReference(event.target.value)} required aria-required={true} /></label>
            </div>
            <label className="refund-evidence-check"><input type="checkbox" checked={evidenceChecked} onChange={(event) => setEvidenceChecked(event.target.checked)} required={true} aria-required={true} /> <span className="required-mark" aria-hidden="true">*</span> 我已核对渠道状态为最终成功、退款单号和成功金额均一致。</label>
          </>}
          <button className="primary-button" type="button" disabled={submitting} onClick={() => void submitAction()}>{submitting ? "正在提交…" : pending ? `使用原操作键重试${actionLabels[action]}` : actionLabels[action]}</button>
        </div>}
      </section>}
      <section className="panel">
        <div className="panel-head"><div><h3>处理时间线</h3><p>记录服务端状态变化；财务凭证和经办备注不在本页展示。</p></div><button className="secondary-button" type="button" onClick={() => setRefreshKey((value) => value + 1)}><RefreshCw /> 刷新</button></div>
        <div className="table-wrap"><table><thead><tr><th>时间</th><th>事件</th><th>状态变化</th><th>已退金额</th></tr></thead><tbody>
          <tr><td>{formatShanghaiDateTime(detail.case.created_at)}</td><td>创建退款记录</td><td>{statusLabels.pending_manual}</td><td>—</td></tr>
          {detail.events.map((event) => <tr key={event.sequence}>
            <td>{formatShanghaiDateTime(event.occurred_at)}</td>
            <td>{eventLabels[event.type] || event.type}</td>
            <td>{statusLabels[event.from_status] || event.from_status} → {statusLabels[event.to_status] || event.to_status}</td>
            <td>{event.type === "refund_completed" ? formatCentsAsYuan(event.successful_refund_cents) : "—"}</td>
          </tr>)}
        </tbody></table></div>
      </section>
      <section className="panel"><h3>关联记录</h3><p>退款单号：{detail.case.case_id}</p><p>报名号：{detail.case.registration_id}</p><Link className="table-link" href={`/orders/${detail.case.order_id}`}>查看关联订单 <ArrowRight /></Link></section>
    </>}
  </div>;
}
