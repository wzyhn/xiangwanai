"use client";

import { RefreshCw } from "lucide-react";
import Link from "next/link";
import { useEffect, useRef, useState } from "react";
import { ApiError, api, displayError } from "@/lib/api";
import { parseCorrectionAction, type CorrectionAction } from "@/lib/coupon-correction-recovery";
import { parsePendingCouponReplenishment, type PendingCouponReplenishment } from "@/lib/coupon-replenishment-recovery";
import { formatShanghaiDateTime } from "@/lib/time";
import type { CouponCorrectionPage } from "@/lib/types";

const pageSize = 50;
const replenishmentRecoveryKey = "xiangwan:coupon-replenishment:v1";
const correctionRecoveryKey = "xiangwan:coupon-correction-action:v1";

type CorrectionItem = CouponCorrectionPage["items"][number];
type CouponPolicyActivationReceipt = {
  policy_version: string;
  enabled: boolean;
  effective_at: string;
  recorded_at: string;
  duplicate: boolean;
};

type CouponReplenishmentReceipt = {
  source_coupon_id: string;
  business_key: string;
  coupon_ids: string[];
  granted_at: string;
  duplicate: boolean;
};

function base64UTF8(value: string): string {
  const bytes = new TextEncoder().encode(value);
  if (bytes.length === 0 || bytes.length > 4096) throw new Error("签收策略必须是 1–4096 字节的原始 JSON 文档。");
  return btoa(Array.from(bytes, (byte) => String.fromCharCode(byte)).join(""));
}

export default function CouponCorrectionsPage() {
  const [pageNumber, setPageNumber] = useState(1);
  const [refreshKey, setRefreshKey] = useState(0);
  const [result, setResult] = useState<CouponCorrectionPage | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const snapshot = useRef<string | null>(null);
  const [policyPayload, setPolicyPayload] = useState("");
  const [policySignature, setPolicySignature] = useState("");
  const [policySubmitting, setPolicySubmitting] = useState(false);
  const [policyMessage, setPolicyMessage] = useState("");
  const [policyReceipt, setPolicyReceipt] = useState<CouponPolicyActivationReceipt | null>(null);
  const [sourceCouponID, setSourceCouponID] = useState("");
  const [replenishmentReason, setReplenishmentReason] = useState("");
  const [caseReference, setCaseReference] = useState("");
  const [pendingReplenishment, setPendingReplenishment] = useState<PendingCouponReplenishment | null>(null);
  const [replenishmentSubmitting, setReplenishmentSubmitting] = useState(false);
  const [replenishmentMessage, setReplenishmentMessage] = useState("");
  const [replenishmentReceipt, setReplenishmentReceipt] = useState<CouponReplenishmentReceipt | null>(null);
  const [selectedCorrection, setSelectedCorrection] = useState<CorrectionItem | null>(null);
  const [correctionNote, setCorrectionNote] = useState("");
  const [correctionKind, setCorrectionKind] = useState<"financial" | "entitlement">("financial");
  const [correctionReference, setCorrectionReference] = useState("");
  const [correctionCents, setCorrectionCents] = useState("");
  const [pendingCorrection, setPendingCorrection] = useState<CorrectionAction | null>(null);
  const [correctionSubmitting, setCorrectionSubmitting] = useState(false);
  const [correctionMessage, setCorrectionMessage] = useState("");

  useEffect(() => {
    try {
      const raw = window.sessionStorage.getItem(correctionRecoveryKey);
      const saved = parseCorrectionAction(raw);
      if (saved) {
        setPendingCorrection(saved);
        setCorrectionMessage("上次更正写入结果未知。请原样重试，服务端会返回原回执。");
      } else if (raw) {
        setCorrectionMessage("上次操作恢复记录不完整，请先核对服务端状态再操作。");
        window.sessionStorage.removeItem(correctionRecoveryKey);
      }
    } catch {
      setCorrectionMessage("浏览器无法保存操作恢复记录；请启用会话存储后再提交。");
    }
  }, []);

  useEffect(() => {
    const saved = parsePendingCouponReplenishment(window.sessionStorage.getItem(replenishmentRecoveryKey));
    if (!saved) return;
    setPendingReplenishment(saved);
    setSourceCouponID(saved.source_coupon_id);
    setReplenishmentReason(saved.reason);
    setCaseReference(saved.context);
    setReplenishmentMessage("上次补发结果尚未确认。请原样重试，服务端会返回同一批券的回执。");
  }, []);

  useEffect(() => {
    const request = new AbortController();
    const query = new URLSearchParams({ page: String(pageNumber), page_size: String(pageSize) });
    if (snapshot.current) query.set("as_of", snapshot.current);
    setLoading(true);
    setResult(null);
    setError("");
    void api<CouponCorrectionPage>(`/coupon-corrections?${query}`, { signal: request.signal })
      .then((value) => {
        if (request.signal.aborted) return;
        if (!snapshot.current) snapshot.current = value.as_of;
        setResult(value);
      })
      .catch((reason: unknown) => {
        if (!request.signal.aborted) setError(displayError(reason));
      })
      .finally(() => {
        if (!request.signal.aborted) setLoading(false);
      });
    return () => request.abort();
  }, [pageNumber, refreshKey]);

  function refresh() {
    snapshot.current = null;
    setResult(null);
    setPageNumber(1);
    setRefreshKey((value) => value + 1);
  }

  async function activatePolicy() {
    if (policySubmitting) return;
    setPolicySubmitting(true);
    setPolicyMessage("");
    setPolicyReceipt(null);
    try {
      const receipt = await api<CouponPolicyActivationReceipt>("/coupon-grant-policies", {
        method: "POST",
        body: JSON.stringify({
          payload_base64: base64UTF8(policyPayload),
          signature_base64: policySignature.trim(),
        }),
      });
      setPolicyReceipt(receipt);
      setPolicyMessage(receipt.duplicate ? "已返回这份签收策略的原始登记回执。" : "签收策略已登记；请按生效时间和发券运行配置继续验收。");
    } catch (reason) {
      setPolicyMessage(`${displayError(reason)}；若登记结果未知，请保持原 JSON 和签名不变并重试。`);
    } finally {
      setPolicySubmitting(false);
    }
  }

  async function replenishCoupon() {
    if (replenishmentSubmitting) return;
    const command = pendingReplenishment ?? {
      source_coupon_id: sourceCouponID.trim(),
      operation_id: crypto.randomUUID(),
      reason: replenishmentReason.trim(),
      context: caseReference.trim(),
    };
    if (!parsePendingCouponReplenishment(JSON.stringify(command))) {
      setReplenishmentMessage("请填写有效的来源优惠券 ID、补发原因和工单引用。");
      return;
    }
    setReplenishmentSubmitting(true);
    setReplenishmentMessage("");
    setReplenishmentReceipt(null);
    window.sessionStorage.setItem(replenishmentRecoveryKey, JSON.stringify(command));
    setPendingReplenishment(command);
    try {
      const receipt = await api<CouponReplenishmentReceipt>(
        "/coupon-grants", {
          method: "POST",
          body: JSON.stringify({
            source_coupon_id: command.source_coupon_id,
            operation_id: command.operation_id,
            reason: command.reason,
            context: command.context,
          }),
        });
      window.sessionStorage.removeItem(replenishmentRecoveryKey);
      setPendingReplenishment(null);
      setReplenishmentReceipt(receipt);
      setReplenishmentMessage(receipt.duplicate ? "已找回同一笔补发的原回执。" : "已登记五张补发券；可在用户端核对权益。");
    } catch (reason) {
      if (reason instanceof ApiError && reason.status >= 400 && reason.status < 500) {
        window.sessionStorage.removeItem(replenishmentRecoveryKey);
        setPendingReplenishment(null);
        setReplenishmentMessage(displayError(reason));
      } else {
        setReplenishmentMessage(`${displayError(reason)}；结果未知，请保持原请求并重试。`);
      }
    } finally {
      setReplenishmentSubmitting(false);
    }
  }

  function selectCorrection(item: CorrectionItem) {
    if (pendingCorrection) return;
    setSelectedCorrection(item);
    setCorrectionKind(item.related_entry_type === "redeemed" ? "financial" : "entitlement");
    setCorrectionCents(item.related_entry_type === "redeemed" ? item.face_value_cents : "0");
    setCorrectionReference("");
    setCorrectionNote("");
    setCorrectionMessage("");
  }

  async function submitCorrection() {
    if (correctionSubmitting) return;
    const command = pendingCorrection ?? (selectedCorrection ? {
      entry_id: selectedCorrection.entry_id,
      operation_id: crypto.randomUUID(),
      body: selectedCorrection.handling_status === "pending" ? {
        action: "start" as const,
        expected_version: 0,
        operator_note: correctionNote.trim(),
      } : {
        action: "resolve" as const,
        expected_version: 1,
        evidence_kind: correctionKind,
        evidence_reference: correctionReference.trim(),
        adjustment_cents: correctionKind === "financial" ? correctionCents.trim() : "0",
        operator_note: correctionNote.trim(),
      },
    } : null);
    if (!command || !parseCorrectionAction(JSON.stringify(command))) {
      setCorrectionMessage("请填写有效的处理说明；结案时还需填写凭证引用和整数金额（分）。");
      return;
    }
    try {
      window.sessionStorage.setItem(correctionRecoveryKey, JSON.stringify(command));
    } catch {
      setCorrectionMessage("浏览器无法保存操作恢复记录，本次未提交。请启用会话存储后重试。");
      return;
    }
    setCorrectionSubmitting(true);
    setCorrectionMessage("");
    setPendingCorrection(command);
    try {
      const receipt = await api<{ status: string; version: number }>(
        `/coupon-corrections/${command.entry_id}/actions`, {
          method: "POST",
          headers: { "Idempotency-Key": command.operation_id },
          body: JSON.stringify(command.body),
        });
      window.sessionStorage.removeItem(correctionRecoveryKey);
      setPendingCorrection(null);
      setSelectedCorrection(null);
      setCorrectionNote("");
      setCorrectionMessage(receipt.status === "resolved"
        ? "人工处理证据已登记；原始异常券和交易历史保持可追溯。"
        : "已开始处理；请由另一名超级管理员核验后登记最终凭证。");
      refresh();
    } catch (reason) {
      if (reason instanceof ApiError && reason.status >= 400 && reason.status < 500) {
        window.sessionStorage.removeItem(correctionRecoveryKey);
        setPendingCorrection(null);
        setSelectedCorrection(null);
        setCorrectionMessage(displayError(reason));
        refresh();
      } else {
        setCorrectionMessage(`${displayError(reason)}；结果未知，请保持原操作并重试。`);
      }
    } finally {
      setCorrectionSubmitting(false);
    }
  }

  return <div className="page-content">
    <section className="panel no-top-margin">
      <div className="panel-head"><div><h2>人工补发优惠券</h2><p>仅超级管理员可凭已有券定位用户。服务端要求历史券余额为零，并按已签收的生效策略补发五张；此操作不能结清异常券的财务复核。</p></div><span className="privacy-badge">需签收策略</span></div>
      <div className="field-grid" style={{ padding: 18 }}>
        <label className="full">用户已有优惠券 ID <span className="required-mark" aria-hidden="true">*</span>
          <input value={sourceCouponID} disabled={replenishmentSubmitting || !!pendingReplenishment} onChange={(event) => setSourceCouponID(event.target.value)} spellCheck={false} placeholder="从用户权益或异常线索中复制券 ID" required={true} aria-required={true} />
        </label>
        <label className="full">补发原因 <span className="required-mark" aria-hidden="true">*</span>
          <textarea value={replenishmentReason} disabled={replenishmentSubmitting || !!pendingReplenishment} onChange={(event) => setReplenishmentReason(event.target.value)} rows={2} maxLength={500} placeholder="写明人工核查依据，不填写手机号等私人信息" required={true} aria-required={true} />
        </label>
        <label className="full">工单引用 <span className="required-mark" aria-hidden="true">*</span>
          <input value={caseReference} disabled={replenishmentSubmitting || !!pendingReplenishment} onChange={(event) => setCaseReference(event.target.value)} maxLength={128} spellCheck={false} placeholder="例如 support-case-001" required={true} aria-required={true} />
        </label>
        <div className="full" style={{ display: "flex", justifyContent: "flex-end" }}>
          <button type="button" className="primary-button" disabled={replenishmentSubmitting || (!pendingReplenishment && (!sourceCouponID || !replenishmentReason || !caseReference))} onClick={() => void replenishCoupon()}>{replenishmentSubmitting ? "正在核对…" : pendingReplenishment ? "原样重试上次补发" : "核对余额并补发"}</button>
        </div>
        {replenishmentMessage && <div className="full inline-message">{replenishmentMessage}</div>}
        {replenishmentReceipt && <p className="full brand-hero-hint">{replenishmentReceipt.coupon_ids.length} 张券 · {formatShanghaiDateTime(replenishmentReceipt.granted_at)} · 操作键 <code>{replenishmentReceipt.business_key}</code></p>}
      </div>
    </section>
    <section className="panel no-top-margin">
      <div className="panel-head"><div><h2>优惠券签收策略登记</h2><p>仅超级管理员可登记客户已签名的面额、有效期和适用范围版本。此处只保存签收事实，不直接发券。</p></div><span className="privacy-badge">客户签名必需</span></div>
      <div className="field-grid" style={{ padding: 18 }}>
        <label className="full">已签名的原始 JSON（字节内容必须与客户签名时一致） <span className="required-mark" aria-hidden="true">*</span>
          <textarea value={policyPayload} disabled={policySubmitting} onChange={(event) => setPolicyPayload(event.target.value)} rows={8} spellCheck={false} placeholder="粘贴客户提供的规范 JSON，不要重新排版" required={true} aria-required={true} />
        </label>
        <label className="full">Ed25519 签名（标准 Base64） <span className="required-mark" aria-hidden="true">*</span>
          <input value={policySignature} disabled={policySubmitting} onChange={(event) => setPolicySignature(event.target.value)} spellCheck={false} autoComplete="off" placeholder="客户对上方原始 JSON 字节的签名" required={true} aria-required={true} />
        </label>
        <div className="full" style={{ display: "flex", justifyContent: "flex-end" }}>
          <button type="button" className="primary-button" disabled={policySubmitting || !policyPayload || !policySignature} onClick={() => void activatePolicy()}>{policySubmitting ? "正在核验签名…" : "核验并登记策略"}</button>
        </div>
        {policyMessage && <div className="full inline-message">{policyMessage}</div>}
        {policyReceipt && <p className="full brand-hero-hint">版本 {policyReceipt.policy_version} · {policyReceipt.enabled ? "启用" : "停用"} · 生效 {formatShanghaiDateTime(policyReceipt.effective_at)} · 登记 {formatShanghaiDateTime(policyReceipt.recorded_at)}</p>}
      </div>
    </section>
    <section className="panel no-top-margin">
      <div className="panel-head"><div><h2>优惠券异常线索</h2><p>签到撤销后，已预占或核销的券保留原始异常标记；人工处理单独记录，不会改写原券或订单。结案需另一名超级管理员核验外部凭证或账本失效事实。</p></div><span className="privacy-badge">仅系统管理员</span></div>
      {(selectedCorrection || pendingCorrection) && <div className="field-grid" style={{ padding: 18 }}>
        <div className="full"><b>{pendingCorrection ? "原样恢复上次操作" : selectedCorrection?.handling_status === "pending" ? "开始人工处理" : "登记人工处理结果"}</b><small style={{ display: "block" }}>异常标记 <code>{pendingCorrection?.entry_id ?? selectedCorrection?.entry_id}</code>；原券继续停用，登记不会发起外部资金操作。</small></div>
        {pendingCorrection && <div className="full inline-message">
          <p>请再次核对原操作：{pendingCorrection.body.action === "start" ? "开始处理" : pendingCorrection.body.evidence_kind === "financial" ? "财务结案" : "权益结案"}。</p>
          {pendingCorrection.body.action === "resolve" && <p>凭证引用：{pendingCorrection.body.evidence_reference} · 金额：{pendingCorrection.body.adjustment_cents} 分</p>}
          <p>处理说明：{pendingCorrection.body.operator_note}</p>
        </div>}
        {!pendingCorrection && selectedCorrection?.handling_status === "processing" && <>
          <label>处理类型 <span className="required-mark" aria-hidden="true">*</span>
            <select value={correctionKind} onChange={(event) => { const kind = event.target.value as "financial" | "entitlement"; setCorrectionKind(kind); setCorrectionCents(kind === "financial" ? selectedCorrection.face_value_cents : "0"); }} required={true} aria-required={true}>
              <option value="financial">核销券：外部财务调整</option><option value="entitlement">未核销券：账本已失效</option>
            </select>
          </label>
          <label>外部凭证或工单引用 <span className="required-mark" aria-hidden="true">*</span><input value={correctionReference} onChange={(event) => setCorrectionReference(event.target.value)} maxLength={128} spellCheck={false} required={true} aria-required={true} /></label>
          {correctionKind === "financial" && <label>调整金额（分，须等于券面额） <span className="required-mark" aria-hidden="true">*</span><input value={correctionCents} onChange={(event) => setCorrectionCents(event.target.value)} inputMode="numeric" spellCheck={false} required={true} aria-required={true} /></label>}
        </>}
        {!pendingCorrection && <label className="full">处理说明 <span className="required-mark" aria-hidden="true">*</span><textarea value={correctionNote} onChange={(event) => setCorrectionNote(event.target.value)} maxLength={500} rows={2} placeholder="说明核对依据，不填写手机号等个人信息" required={true} aria-required={true} /></label>}
        <div className="full" style={{ display: "flex", justifyContent: "flex-end", gap: 8 }}>
          {!pendingCorrection && <button className="secondary-button" type="button" onClick={() => setSelectedCorrection(null)}>取消</button>}
          <button className="primary-button" type="button" disabled={correctionSubmitting} onClick={() => void submitCorrection()}>{correctionSubmitting ? "正在核对…" : pendingCorrection ? "原样重试" : selectedCorrection?.handling_status === "pending" ? "开始处理" : "核验并登记结案"}</button>
        </div>
      </div>}
      {correctionMessage && <div className="inline-message table-message">{correctionMessage}</div>}
      <div className="filter-row"><button className="secondary-button" type="button" disabled={loading} onClick={refresh}><RefreshCw /> 刷新</button></div>
      {error && <div className="inline-message error table-message">{error}</div>}
      <div className="table-wrap"><table>
        <thead><tr><th>记录时间</th><th>优惠券 / 签到事件</th><th>关联交易</th><th>来源状态</th><th>人工处理</th></tr></thead>
        <tbody>{loading && !result ? <tr><td colSpan={5} className="empty-cell">正在读取异常标记…</td></tr> : !result?.items.length ? <tr><td colSpan={5} className="empty-cell">当前范围没有异常标记。</td></tr> : result.items.map((item) => <tr key={item.entry_id}>
          <td>{formatShanghaiDateTime(item.recorded_at)}</td>
          <td><b>券 <code>{item.coupon_id}</code></b><small>面额 {item.face_value_cents} 分 · 签到事件 <code>{item.source_checkin_event_id}</code></small></td>
          <td>{item.order_id ? <Link className="table-link" href={`/orders/${item.order_id}`}>查看订单</Link> : "无关联订单"}{item.registration_id && <small><Link href={`/registrations/${item.registration_id}`}>查看报名</Link></small>}</td>
          <td>{item.related_entry_type === "redeemed" ? "已核销" : item.related_entry_type === "held" ? "已预占" : item.related_entry_type}</td>
          <td>{item.handling_status === "pending" ? "待处理" : item.handling_status === "processing" ? "处理中" : "已登记结案"}{item.handling_status === "resolved" && <small>{item.evidence_kind === "financial" ? `外部调整 ${item.adjustment_cents} 分` : "账本已失效"}</small>}{item.handling_status !== "resolved" && <button className="secondary-button" type="button" disabled={!!pendingCorrection || correctionSubmitting} onClick={() => selectCorrection(item)}>{item.handling_status === "pending" ? "开始处理" : "登记结果"}</button>}</td>
        </tr>)}</tbody>
      </table></div>
      <div className="pagination-row">
        <span>共 {result?.total ?? 0} 条 · 第 {pageNumber} / {Math.max(1, Math.ceil((result?.total ?? 0) / pageSize))} 页</span>
        <div>
          <button className="secondary-button" type="button" disabled={loading || pageNumber <= 1} onClick={() => setPageNumber((value) => Math.max(1, value - 1))}>上一页</button>
          <button className="secondary-button" type="button" disabled={loading || !result || pageNumber * pageSize >= result.total} onClick={() => setPageNumber((value) => value + 1)}>下一页</button>
        </div>
      </div>
    </section>
  </div>;
}
