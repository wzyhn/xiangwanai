"use client";
import { useCallback, useEffect, useRef, useState } from "react";
import { api, ApiError, displayError, operationHeaders, operationKey } from "@/lib/api";
import { canReadAuditLog } from "@/lib/admin-permissions";
import { hostPendingKey, parseHostPending, type HostPending } from "@/lib/host-operation";
import type { AuthStatus } from "@/lib/types";

type Rules = {
  version: number;
  application_cycle: string;
  policy_version: string;
  requirements: string;
  benefits: string;
  enabled: boolean;
};
type Item = {
  id: string;
  application_cycle: string;
  policy_version: string;
  status: string;
  version: number;
  submitted_at: string;
  review_comment?: string;
};
type Detail = Item & {
  personal_introduction: string;
  relevant_experience: string;
  availability: string;
  contact_method: string;
};
type Page = { items: Item[]; total: number; page: number; page_size: number };
const labels: Record<string, string> = {
  pending: "待审核",
  approved: "已通过",
  rejected: "已拒绝",
  withdrawn: "已撤回",
};
const empty: Rules = {
  version: 0,
  application_cycle: "",
  policy_version: "",
  requirements: "",
  benefits: "",
  enabled: false,
};
function Mark() {
  return (
    <span className="required-mark" aria-hidden="true">
      *
    </span>
  );
}
export default function HostApplicationsPage() {
  const [auth, setAuth] = useState<AuthStatus | null>(null),
    [rules, setRules] = useState<Rules>(empty),
    [reason, setReason] = useState("");
  const [page, setPage] = useState(1),
    [status, setStatus] = useState("pending"),
    [result, setResult] = useState<Page | null>(null),
    [detail, setDetail] = useState<Detail | null>(null);
  const [comment, setComment] = useState(""),
    [error, setError] = useState(""),
    [notice, setNotice] = useState(""),
    [busy, setBusy] = useState(false),
    [pending, setPending] = useState<HostPending | null>(null);
  const working = useRef(false),
    readVersion = useRef(0),
    active = useRef(true);
  const load = useCallback(async () => {
    const revision = ++readVersion.current;
    setDetail(null);
    setComment("");
    try {
      const [a, r, list] = await Promise.all([
        api<AuthStatus>("/auth/status"),
        api<Rules>("/host-rules"),
        api<Page>(`/host-applications?status=${status}&page=${page}&page_size=20`),
      ]);
      if (!active.current || revision !== readVersion.current) return;
      setAuth(a);
      setRules(r);
      setResult(list);
      setPending(parseHostPending(sessionStorage.getItem(hostPendingKey), a.principal_id || ""));
    } catch (e) {
      if (active.current && revision === readVersion.current) setError(displayError(e));
    }
  }, [page, status]);
  const cancelReads = useCallback(() => {
    active.current = false;
    readVersion.current++;
  }, []);
  useEffect(() => {
    active.current = true;
    void load();
    return cancelReads;
  }, [load, cancelReads]);
  async function open(item: Item) {
    if (working.current || pending) return;
    const revision = ++readVersion.current;
    setError("");
    setDetail(null);
    setComment("");
    try {
      const v = await api<Detail>(`/host-applications/${item.id}?purpose=host_application_review`);
      if (active.current && revision === readVersion.current) {
        if (v.id !== item.id) throw new Error("申请信息已变化，请刷新");
        setDetail(v);
      }
    } catch (e) {
      if (active.current && revision === readVersion.current) setError(displayError(e));
    }
  }
  async function perform(intent: HostPending) {
    if (working.current) return;
    working.current = true;
    setBusy(true);
    setError("");
    setNotice("");
    try {
      const current = await api<AuthStatus>("/auth/status");
      if (current.principal_id !== intent.owner)
        throw new Error("管理员账号已变化，请重新登录核对原操作");
      sessionStorage.setItem(hostPendingKey, JSON.stringify(intent));
      setPending(intent);
      setDetail(null);
      readVersion.current++;
      const v = await api<Rules | Item>(intent.path, {
        method: "POST",
        headers: operationHeaders(intent.operation),
        body: intent.body,
      });
      if (
        intent.path === "/host-rules"
          ? !(
              "application_cycle" in v && v.version === JSON.parse(intent.body).expected_version + 1
            )
          : !(
              "id" in v &&
              intent.path.includes(v.id) &&
              v.status === JSON.parse(intent.body).decision
            )
      )
        throw new Error("操作回执不完整，请重试核对原请求");
      sessionStorage.removeItem(hostPendingKey);
      if (!active.current) return;
      setPending(null);
      setReason("");
      setComment("");
      setNotice(intent.path === "/host-rules" ? "规则版本已保存" : "审核结果已保存");
      await load();
    } catch (e) {
      if (active.current) {
        if (e instanceof ApiError && [400, 403, 404, 409, 422].includes(e.status)) {
          sessionStorage.removeItem(hostPendingKey);
          setPending(null);
        }
        setError(displayError(e));
      }
    } finally {
      working.current = false;
      if (active.current) setBusy(false);
    }
  }
  async function publish(e: React.FormEvent) {
    e.preventDefault();
    if (!auth?.principal_id || pending || working.current) return;
    if (!window.confirm("确认发布此规则版本？申请人将按此版本确认要求与支持。")) return;
    await perform({
      owner: auth.principal_id,
      operation: operationKey(),
      path: "/host-rules",
      body: JSON.stringify({
        expected_version: rules.version,
        application_cycle: rules.application_cycle.trim(),
        policy_version: rules.policy_version.trim(),
        requirements: rules.requirements.trim(),
        benefits: rules.benefits.trim(),
        enabled: rules.enabled,
        reason: reason.trim(),
      }),
    });
  }
  async function review(decision: string) {
    if (!detail || !auth?.principal_id || pending || working.current || !comment.trim()) return;
    if (!window.confirm(`确认${labels[decision]}这份申请？审核意见将对本人显示。`)) return;
    await perform({
      owner: auth.principal_id,
      operation: operationKey(),
      path: `/host-applications/${detail.id}/reviews`,
      body: JSON.stringify({ expected_version: detail.version, decision, comment: comment.trim() }),
    });
  }
  return (
    <div className="page-stack">
      {error && (
        <div role="alert" className="error-banner">
          {error}
        </div>
      )}
      {notice && (
        <div role="status" className="success-banner">
          {notice}
        </div>
      )}
      {pending && (
        <section className="panel">
          <p>上次操作结果未知，请沿用原请求核对。</p>
          <button className="primary-button" disabled={busy} onClick={() => void perform(pending)}>
            重试原操作
          </button>
        </section>
      )}
      <section className="panel">
        <h2>主理人规则</h2>
        <p>修改要求或支持内容时，使用新的规则版本。暂停申请仍保留原规则与已提交申请。</p>
        {auth && canReadAuditLog(auth) ? (
          <form onSubmit={publish}>
            <fieldset disabled={busy || Boolean(pending)} className="form-grid">
              <label>
                申请周期 <Mark />
                <input
                  required
                  aria-required="true"
                  maxLength={100}
                  pattern="[A-Za-z0-9][A-Za-z0-9._:-]{0,99}"
                  value={rules.application_cycle}
                  onChange={(e) => setRules({ ...rules, application_cycle: e.target.value })}
                />
              </label>
              <label>
                规则版本 <Mark />
                <input
                  required
                  aria-required="true"
                  maxLength={100}
                  pattern="[A-Za-z0-9][A-Za-z0-9._:-]{0,99}"
                  value={rules.policy_version}
                  onChange={(e) => setRules({ ...rules, policy_version: e.target.value })}
                />
              </label>
              <label>
                申请要求 <Mark />
                <textarea
                  required
                  aria-required="true"
                  rows={4}
                  maxLength={4000}
                  value={rules.requirements}
                  onChange={(e) => setRules({ ...rules, requirements: e.target.value })}
                />
              </label>
              <label>
                可获支持 <Mark />
                <textarea
                  required
                  aria-required="true"
                  rows={4}
                  maxLength={4000}
                  value={rules.benefits}
                  onChange={(e) => setRules({ ...rules, benefits: e.target.value })}
                />
              </label>
              <label>
                发布原因 <Mark />
                <textarea
                  required
                  aria-required="true"
                  maxLength={500}
                  value={reason}
                  onChange={(e) => setReason(e.target.value)}
                />
              </label>
              <label>
                <input
                  type="checkbox"
                  checked={rules.enabled}
                  onChange={(e) => setRules({ ...rules, enabled: e.target.checked })}
                />
                开放申请
              </label>
              <button className="primary-button" type="submit">
                保存规则版本
              </button>
            </fieldset>
          </form>
        ) : (
          <>
            <p>{rules.requirements || "规则待发布"}</p>
            <p>{rules.benefits}</p>
            <p>{rules.enabled ? "申请已开放" : "申请未开放"}</p>
          </>
        )}
      </section>
      <section className="panel">
        <h2>主理人申请</h2>
        <label>
          审核状态{" "}
          <select
            disabled={busy || Boolean(pending)}
            value={status}
            onChange={(e) => {
              setPage(1);
              setStatus(e.target.value);
            }}
          >
            <option value="">全部</option>
            {Object.entries(labels).map(([v, l]) => (
              <option key={v} value={v}>
                {l}
              </option>
            ))}
          </select>
        </label>
        <p>共 {result?.total || 0} 份。查看申请时记录审核用途，联系方式仅在本页临时展示。</p>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>申请时间</th>
                <th>周期</th>
                <th>规则版本</th>
                <th>状态</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              {result?.items.map((i) => (
                <tr key={i.id}>
                  <td>{new Date(i.submitted_at).toLocaleString("zh-CN")}</td>
                  <td>{i.application_cycle}</td>
                  <td>{i.policy_version}</td>
                  <td>{labels[i.status]}</td>
                  <td>
                    <button disabled={busy || Boolean(pending)} onClick={() => void open(i)}>
                      查看申请
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <button disabled={page === 1 || busy || Boolean(pending)} onClick={() => setPage(page - 1)}>
          上一页
        </button>
        <span> 第 {page} 页 </span>
        <button
          disabled={!result || page * 20 >= result.total || busy || Boolean(pending)}
          onClick={() => setPage(page + 1)}
        >
          下一页
        </button>
      </section>
      {detail && (
        <section className="panel">
          <h2>申请详情 · {labels[detail.status]}</h2>
          <button
            onClick={() => {
              setDetail(null);
              setComment("");
            }}
          >
            关闭详情
          </button>
          <dl>
            <dt>个人介绍</dt>
            <dd className="host-private-text">{detail.personal_introduction}</dd>
            <dt>相关经验</dt>
            <dd className="host-private-text">{detail.relevant_experience}</dd>
            <dt>可参与时间</dt>
            <dd>{detail.availability}</dd>
            <dt>联系方式</dt>
            <dd>{detail.contact_method}</dd>
          </dl>
          {detail.status === "pending" ? (
            <form
              onSubmit={(e) => {
                e.preventDefault();
                void review("approved");
              }}
            >
              <label>
                审核意见 <Mark />
                <textarea
                  required
                  aria-required="true"
                  maxLength={2000}
                  rows={4}
                  value={comment}
                  onChange={(e) => setComment(e.target.value)}
                />
              </label>
              <button className="primary-button" type="submit" disabled={busy}>
                通过申请
              </button>
              <button
                type="button"
                disabled={busy || !comment.trim()}
                onClick={() => void review("rejected")}
              >
                拒绝申请
              </button>
            </form>
          ) : (
            <p>审核意见：{detail.review_comment || "无"}</p>
          )}
        </section>
      )}
    </div>
  );
}
