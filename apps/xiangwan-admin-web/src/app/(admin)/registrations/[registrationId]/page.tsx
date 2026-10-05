"use client";

import {
  ArrowLeft,
  CalendarCheck,
  CircleDollarSign,
  ClipboardCheck,
  ShieldCheck,
} from "lucide-react";
import Link from "next/link";
import { useParams } from "next/navigation";
import { useCallback, useEffect, useRef, useState } from "react";
import { api, displayError } from "@/lib/api";
import { canOperateActivities, canReadAuditLog } from "@/lib/admin-permissions";
import { CheckinRevocation } from "@/components/checkin-revocation";
import { formatShanghaiDateTime } from "@/lib/time";
import type { AuthStatus, RegistrationAnswerDetailSet, RegistrationDetail } from "@/lib/types";

const statusLabel: Record<string, string> = {
  pending_payment: "待支付",
  confirmed: "已确认",
  cancelled: "已取消",
  pending: "待支付",
  unknown: "确认中",
  paid_confirmed: "已支付",
  settled_zero: "无需支付",
  closed_unpaid: "已关闭",
  pending_manual: "待处理",
  processing: "退款中",
  failed: "处理失败",
  rejected: "未通过",
  refunded: "已退款",
  checked_in: "已签到",
  revoked: "已撤销",
  not_checked_in: "待签到",
};

function label(value?: string): string {
  return value ? statusLabel[value] || "状态待确认" : "—";
}

export default function RegistrationDetailPage() {
  const params = useParams<{ registrationId: string }>();
  const [detail, setDetail] = useState<RegistrationDetail | null>(null);
  const [error, setError] = useState("");
  const [canRevokeCheckin, setCanRevokeCheckin] = useState(false);
  const [checkinNotice, setCheckinNotice] = useState("");
  const [contact, setContact] = useState<RegistrationDetail["contact"]>();
  const [contactPurpose, setContactPurpose] = useState("activity_coordination");
  const [contactLoading, setContactLoading] = useState(false);
  const [contactError, setContactError] = useState("");
  const [canRevealContact, setCanRevealContact] = useState<boolean | null>(null);
  const contactRequest = useRef<AbortController | null>(null);
  const contactTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const [answers, setAnswers] = useState<RegistrationAnswerDetailSet | null>(null);
  const [answerPurpose, setAnswerPurpose] = useState("activity_coordination");
  const [answerLoading, setAnswerLoading] = useState(false);
  const [answerError, setAnswerError] = useState("");
  const answerRequest = useRef<AbortController | null>(null);
  const answerTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  const hideContact = useCallback(() => {
    contactRequest.current?.abort();
    contactRequest.current = null;
    if (contactTimer.current) clearTimeout(contactTimer.current);
    contactTimer.current = null;
    setContact(undefined);
    setContactLoading(false);
  }, []);

  const hideAnswers = useCallback(() => {
    answerRequest.current?.abort();
    answerRequest.current = null;
    if (answerTimer.current) clearTimeout(answerTimer.current);
    answerTimer.current = null;
    setAnswers(null);
    setAnswerLoading(false);
  }, []);

  async function revealAnswers() {
    hideAnswers();
    const request = new AbortController();
    answerRequest.current = request;
    setAnswerLoading(true);
    setAnswerError("");
    try {
      const value = await api<RegistrationAnswerDetailSet>(
        `/registrations/${params.registrationId}/answers?purpose=${encodeURIComponent(answerPurpose)}`,
        { signal: request.signal },
      );
      if (request.signal.aborted) return;
      if (value.registration_id !== params.registrationId || !Array.isArray(value.items))
        throw new Error("问卷答卷与当前报名记录不匹配");
      setAnswers(value);
      answerTimer.current = setTimeout(hideAnswers, 60_000);
    } catch (reason) {
      if (!request.signal.aborted) setAnswerError(displayError(reason));
    } finally {
      if (!request.signal.aborted) setAnswerLoading(false);
    }
  }

  async function revealContact() {
    hideContact();
    const request = new AbortController();
    contactRequest.current = request;
    setContactLoading(true);
    setContactError("");
    try {
      const value = await api<RegistrationDetail>(
        `/registrations/${params.registrationId}?contact_purpose=${encodeURIComponent(contactPurpose)}`,
        { signal: request.signal },
      );
      if (request.signal.aborted) return;
      if (value.id !== params.registrationId || !value.contact)
        throw new Error("当前账号无法查看完整联系人");
      setContact(value.contact);
      contactTimer.current = setTimeout(hideContact, 60_000);
    } catch (reason) {
      if (!request.signal.aborted) setContactError(displayError(reason));
    } finally {
      if (!request.signal.aborted) setContactLoading(false);
    }
  }

  useEffect(() => {
    let active = true;
    hideContact();
    hideAnswers();
    setContactError("");
    setAnswerError("");
    setCanRevealContact(null);
    setCanRevokeCheckin(false);
    setCheckinNotice("");
    void Promise.all([
      api<RegistrationDetail>(`/registrations/${params.registrationId}`),
      api<AuthStatus>("/auth/status"),
    ])
      .then(([value, auth]) => {
        if (!active) return;
        setDetail(value);
        setCanRevealContact(canOperateActivities(auth));
        setCanRevokeCheckin(canReadAuditLog(auth));
      })
      .catch((reason: unknown) => active && setError(displayError(reason)));
    const hideWhenBackgrounded = () => {
      if (document.visibilityState !== "visible") {
        hideContact();
        hideAnswers();
      }
    };
    document.addEventListener("visibilitychange", hideWhenBackgrounded);
    return () => {
      active = false;
      hideContact();
      hideAnswers();
      document.removeEventListener("visibilitychange", hideWhenBackgrounded);
    };
  }, [hideAnswers, hideContact, params.registrationId]);

  if (!detail || detail.id !== params.registrationId)
    return (
      <div className="page-content">
        <Link className="back-link" href="/registrations">
          <ArrowLeft /> 返回报名管理
        </Link>
        <div className={`inline-message ${error ? "error" : ""}`}>
          {error || "正在读取报名详情…"}
        </div>
      </div>
    );

  return (
    <div className="page-content detail-page registration-detail">
      <Link className="back-link" href="/registrations">
        <ArrowLeft /> 返回报名管理
      </Link>
      <section className="detail-hero">
        <div>
          <span className="eyebrow">报名记录</span>
          <h2>
            {detail.contact_name} · {detail.contact_phone}
          </h2>
          <p>
            {detail.instance_title} / {detail.session_title}
          </p>
        </div>
        <span className={`status-pill ${detail.participation_status}`}>
          {label(detail.participation_status)}
        </span>
      </section>
      <div className="fact-grid">
        <article>
          <CalendarCheck />
          <span>参与状态</span>
          <b>{label(detail.participation_status)}</b>
          <small>
            {detail.confirmed_at
              ? `确认于 ${formatShanghaiDateTime(detail.confirmed_at)}`
              : "尚未确认"}
          </small>
        </article>
        {detail.payment_status !== undefined && (
          <article>
            <CircleDollarSign />
            <span>支付 / 退款</span>
            <b>{label(detail.payment_status)}</b>
            <small>
              {label(detail.refund_status)} · 已退 ¥
              {((detail.successful_refund_cents || 0) / 100).toFixed(2)}
            </small>
          </article>
        )}
        <article>
          <ClipboardCheck />
          <span>签到状态</span>
          <b>{label(detail.checkin_status)}</b>
          <small>
            {detail.checked_in_at ? formatShanghaiDateTime(detail.checked_in_at) : "尚未签到"}
          </small>
        </article>
        <article>
          <ShieldCheck />
          <span>报名信息</span>
          <b>已完整记录</b>
          <small>隐私说明 {detail.privacy_policy_version || "未记录"}</small>
        </article>
      </div>
      {checkinNotice && (
        <p role="status" className="inline-message success">
          {checkinNotice}
        </p>
      )}
      {canRevokeCheckin && detail.checkin_id && (
        <CheckinRevocation
          detail={detail}
          onComplete={async (notice) => {
            hideContact();
            hideAnswers();
            const value = await api<RegistrationDetail>(`/registrations/${params.registrationId}`);
            if (value.id !== params.registrationId) throw new Error("报名记录已变化，请刷新后核对");
            setDetail(value);
            setCheckinNotice(notice);
          }}
        />
      )}
      <section className="panel">
        <h3>报名联系人</h3>
        {canRevealContact === false ? (
          <>
            <p className="field-help">
              当前账号只能查看脱敏报名信息。查看姓名和电话需要活动运营权限。
            </p>
          </>
        ) : canRevealContact === null ? (
          <p className="field-help">正在确认联系人查看权限…</p>
        ) : contact ? (
          <>
            <p>姓名 / 昵称：{contact.name || "未填写"}</p>
            <p>
              联系电话：
              {contact.phone ? <a href={`tel:${contact.phone}`}>{contact.phone}</a> : "未填写"}
            </p>
            <button className="button secondary" type="button" onClick={hideContact}>
              收起联系人
            </button>
            <p className="field-help">1 分钟后自动收起。</p>
          </>
        ) : (
          <>
            <label>
              查看用途{" "}
              <span className="required-mark" aria-hidden="true">
                *
              </span>{" "}
              <select
                value={contactPurpose}
                onChange={(event) => setContactPurpose(event.target.value)}
                disabled={contactLoading}
                required={true}
                aria-required={true}
              >
                <option value="activity_coordination">活动联络</option>
                <option value="onsite_verification">现场核对</option>
              </select>
            </label>
            <button
              className="button secondary"
              type="button"
              disabled={contactLoading}
              onClick={() => void revealContact()}
            >
              {contactLoading ? "正在读取…" : "查看姓名与电话"}
            </button>
          </>
        )}
        {contactError && <p className="inline-message error">{contactError}</p>}
      </section>
      <section className="panel">
        <h3>报名问卷答卷</h3>
        {canRevealContact === false ? (
          <p className="field-help">查看原始答卷需要活动运营权限。</p>
        ) : canRevealContact === null ? (
          <p className="field-help">正在确认答卷查看权限…</p>
        ) : answers ? (
          <>
            {answers.items.length === 0 ? (
              <p className="field-help">这笔报名没有问卷答卷。</p>
            ) : (
              <dl className="identity-list">
                {answers.items.map((item) => {
                  const labels = new Map(item.options.map((option) => [option.code, option.label]));
                  return (
                    <div key={item.field_id}>
                      <dt>
                        {item.field_label}
                        {item.required ? " *" : ""}
                      </dt>
                      <dd>
                        {item.answer_values.length
                          ? item.answer_values
                              .map((answer) => labels.get(answer) || answer)
                              .join("、")
                          : "未填写"}
                      </dd>
                    </div>
                  );
                })}
              </dl>
            )}
            <button className="button secondary" type="button" onClick={hideAnswers}>
              收起答卷
            </button>
            <p className="field-help">1 分钟后或切出页面时自动收起。</p>
          </>
        ) : (
          <>
            <label>
              查看用途{" "}
              <span className="required-mark" aria-hidden="true">
                *
              </span>{" "}
              <select
                value={answerPurpose}
                onChange={(event) => setAnswerPurpose(event.target.value)}
                disabled={answerLoading}
                required={true}
                aria-required={true}
              >
                <option value="activity_coordination">活动联络</option>
                <option value="event_followup">活动回访</option>
              </select>
            </label>
            <button
              className="button secondary"
              type="button"
              disabled={answerLoading}
              onClick={() => void revealAnswers()}
            >
              {answerLoading ? "正在读取…" : "查看本笔答卷"}
            </button>
          </>
        )}
        {answerError && <p className="inline-message error">{answerError}</p>}
      </section>
      <details className="panel technical-details">
        <summary>查看记录编号</summary>
        <dl className="identity-list">
          <div>
            <dt>报名记录</dt>
            <dd>{detail.id}</dd>
          </div>
          <div>
            <dt>活动系列</dt>
            <dd>{detail.series_id}</dd>
          </div>
          <div>
            <dt>活动期次</dt>
            <dd>{detail.instance_id}</dd>
          </div>
          <div>
            <dt>活动场次</dt>
            <dd>{detail.session_id}</dd>
          </div>
          {detail.checkin_id && (
            <div>
              <dt>签到记录</dt>
              <dd>{detail.checkin_id}</dd>
            </div>
          )}
        </dl>
      </details>
    </div>
  );
}
