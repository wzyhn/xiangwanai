"use client";

import { ArrowRight, ClipboardList, Download, Filter, FileJson, RefreshCw, Search } from "lucide-react";
import Link from "next/link";
import { useCallback, useEffect, useRef, useState } from "react";
import { api, apiDownload, displayError } from "@/lib/api";
import { canOperateActivities, canReadAllRegistrations } from "@/lib/admin-permissions";
import { listAllCheckinTargets } from "@/lib/checkin-targets";
import { formatShanghaiDateTime } from "@/lib/time";
import type { AuthStatus, CheckinTarget, Page, Registration, RegistrationAnswerSummary } from "@/lib/types";

const participationLabels: Record<string, string> = {
  pending_payment: "待支付",
  confirmed: "已确认",
  cancelled: "已取消",
};

const paymentLabels: Record<string, string> = {
  pending: "待支付",
  unknown: "确认中",
  paid_confirmed: "已支付",
  settled_zero: "无需支付",
  closed_unpaid: "已关闭",
};

const answerTypeLabels: Record<string, string> = {
  single_choice: "单选",
  multiple_choice: "多选",
  single_line: "单行文本",
  multiline: "多行文本",
  area: "区域",
};

export default function RegistrationsPage() {
  const [rows, setRows] = useState<Registration[]>([]);
  const [targets, setTargets] = useState<CheckinTarget[]>([]);
  const [sessionId, setSessionId] = useState("");
  const [status, setStatus] = useState("");
  const [search, setSearch] = useState("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [ready, setReady] = useState(false);
  const [allSessionsAllowed, setAllSessionsAllowed] = useState(false);
  const [answerAccess, setAnswerAccess] = useState(false);
  const [pageNumber, setPageNumber] = useState(1);
  const [total, setTotal] = useState(0);
  const [answerRows, setAnswerRows] = useState<RegistrationAnswerSummary[]>([]);
  const [answerPageNumber, setAnswerPageNumber] = useState(1);
  const [answerTotal, setAnswerTotal] = useState(0);
  const [answerLoading, setAnswerLoading] = useState(false);
  const [answerError, setAnswerError] = useState("");
  const [answerVisible, setAnswerVisible] = useState(false);
  const pageSize = 50;
  const answerPageSize = 50;
  const loadGeneration = useRef(0);
  const answerGeneration = useRef(0);
  const previousAnswerPageNumber = useRef(1);
  const initializationGeneration = useRef(0);

  const initialize = useCallback(async () => {
    const generation = ++initializationGeneration.current;
    answerGeneration.current += 1;
    setAnswerAccess(false);
    setAnswerVisible(false);
    setAnswerRows([]);
    setAnswerTotal(0);
    setAnswerError("");
    setAnswerLoading(false);
    setLoading(true);
    setError("");
    try {
      const [auth, availableTargets] = await Promise.all([
        api<AuthStatus>("/auth/status"),
        listAllCheckinTargets(),
      ]);
      if (generation !== initializationGeneration.current) return;
      setTargets(availableTargets);
      const canReadEverySession = canReadAllRegistrations(auth);
      setAllSessionsAllowed(canReadEverySession);
      setAnswerAccess(canOperateActivities(auth));
      if (!canReadEverySession) {
        setSessionId((current) =>
          availableTargets.some((target) => target.session_id === current)
            ? current
            : availableTargets[0]?.session_id || "",
        );
      }
      setPageNumber(1);
      setReady(true);
    } catch (reason) {
      if (generation !== initializationGeneration.current) return;
      setReady(false);
      setAnswerAccess(false);
      setRows([]);
      setTotal(0);
      setError(displayError(reason));
    } finally {
      if (generation === initializationGeneration.current) setLoading(false);
    }
  }, []);

  useEffect(() => {
    void initialize();
    return () => { initializationGeneration.current += 1; };
  }, [initialize]);

  const load = useCallback(async () => {
    if (!ready) return;
    const generation = ++loadGeneration.current;
    setLoading(true);
    setError("");
    if (!allSessionsAllowed && !sessionId) {
      setRows([]);
      setTotal(0);
      setLoading(false);
      return;
    }
    const query = new URLSearchParams({ page: String(pageNumber), page_size: String(pageSize) });
    if (sessionId) query.set("session_id", sessionId);
    if (status) query.set("participation_status", status);
    try {
      const page = await api<Page<Registration>>(`/registrations?${query}`);
      if (generation !== loadGeneration.current) return;
      setRows(page.items);
      setTotal(page.total);
    } catch (reason) {
      if (generation !== loadGeneration.current) return;
      setError(displayError(reason));
    } finally {
      if (generation === loadGeneration.current) setLoading(false);
    }
  }, [allSessionsAllowed, pageNumber, ready, sessionId, status]);

  useEffect(() => {
    void load();
    return () => { loadGeneration.current += 1; };
  }, [load]);

  const answerPath = useCallback((format: "json" | "csv") => {
    const query = new URLSearchParams({
      session_id: sessionId,
      page: String(answerPageNumber),
      page_size: String(answerPageSize),
      format,
    });
    return `/registrations/answer-summaries?${query.toString()}`;
  }, [answerPageNumber, sessionId]);

  const loadAnswerSummaries = useCallback(async () => {
    if (!sessionId) {
      setAnswerVisible(true);
      setAnswerRows([]);
      setAnswerTotal(0);
      setAnswerError("请先选择一个场次，再查看答卷摘要。");
      return;
    }
    const generation = ++answerGeneration.current;
    setAnswerVisible(true);
    setAnswerLoading(true);
    setAnswerError("");
    try {
      const page = await api<Page<RegistrationAnswerSummary>>(answerPath("json"));
      if (generation !== answerGeneration.current) return;
      setAnswerRows(page.items);
      setAnswerTotal(page.total);
    } catch (reason) {
      if (generation !== answerGeneration.current) return;
      setAnswerRows([]);
      setAnswerTotal(0);
      setAnswerError(displayError(reason));
    } finally {
      if (generation === answerGeneration.current) setAnswerLoading(false);
    }
  }, [answerPath, sessionId]);

  const downloadAnswerExport = useCallback(async (format: "json" | "csv") => {
    if (!sessionId) {
      setAnswerVisible(true);
      setAnswerError("请先选择一个场次，再导出答卷摘要。");
      return;
    }
    setAnswerError("");
    try {
      let blob: Blob;
      if (format === "json") {
        const page = await api<Page<RegistrationAnswerSummary>>(answerPath("json"));
        blob = new Blob([JSON.stringify(page, null, 2)], { type: "application/json;charset=utf-8" });
      } else {
        blob = await apiDownload(answerPath("csv"));
      }
      const objectURL = URL.createObjectURL(blob);
      const link = document.createElement("a");
      link.href = objectURL;
      link.download = `xiangwan-answer-summary-${sessionId}-page-${answerPageNumber}.${format}`;
      document.body.appendChild(link);
      link.click();
      link.remove();
      window.setTimeout(() => URL.revokeObjectURL(objectURL), 0);
    } catch (reason) {
      setAnswerError(displayError(reason));
    }
  }, [answerPageNumber, answerPath, sessionId]);

  useEffect(() => {
    if (!answerVisible || previousAnswerPageNumber.current === answerPageNumber) return;
    previousAnswerPageNumber.current = answerPageNumber;
    void loadAnswerSummaries();
  }, [answerPageNumber, answerVisible, loadAnswerSummaries]);

  const shown = rows.filter((row) => {
    const value = search.trim().toLowerCase();
    return !value || row.contact_name.toLowerCase().includes(value) ||
      row.contact_phone.toLowerCase().includes(value) || row.instance_title.toLowerCase().includes(value);
  });

  return (
    <div className="page-content">
      <section className="panel no-top-margin">
        <div className="panel-head"><div><h2>报名记录</h2><p>查看联系人、参与状态和签到进度。</p></div><span className="privacy-badge">敏感信息已保护</span></div>
        <div className="filter-row registration-filters">
          <label><Filter /> 场次<select value={sessionId} disabled={!ready || (!allSessionsAllowed && targets.length === 0)} onChange={(event) => { setSessionId(event.target.value); setPageNumber(1); setAnswerPageNumber(1); previousAnswerPageNumber.current = 1; setAnswerVisible(false); setAnswerError(""); }}>{allSessionsAllowed ? <option value="">全部授权场次</option> : !ready ? <option value="">{error ? "场次加载失败" : "正在读取授权场次"}</option> : targets.length === 0 ? <option value="">暂无授权场次</option> : null}{targets.map((target) => <option key={target.session_id} value={target.session_id}>{target.instance_title} · {target.session_title}</option>)}</select></label>
          <label>参与状态<select value={status} onChange={(event) => { setStatus(event.target.value); setPageNumber(1); }}><option value="">全部</option><option value="confirmed">已确认</option><option value="pending_payment">待支付</option><option value="cancelled">已取消</option></select></label>
          <label className="search-field"><Search /><input value={search} onChange={(event) => setSearch(event.target.value)} placeholder="筛选当前结果" /></label>
          <button className="secondary-button" onClick={() => { if (ready) void load(); else void initialize(); }}><RefreshCw /> 刷新</button>
          {answerAccess && <button className="secondary-button" disabled={!sessionId || answerLoading} onClick={() => void loadAnswerSummaries()}><ClipboardList /> 查看答卷摘要</button>}
        </div>
        {error && <div className="inline-message error table-message">{error}</div>}
        <div className="table-wrap">
          <table>
            <thead><tr><th>报名用户</th><th>手机号</th><th>活动 / 场次</th><th>报名时间</th><th>参与</th><th>支付</th><th>签到</th><th /></tr></thead>
            <tbody>{loading ? <tr><td colSpan={8} className="empty-cell">正在加载报名记录…</td></tr> : shown.length === 0 ? <tr><td colSpan={8} className="empty-cell">当前筛选下没有报名。</td></tr> : shown.map((row) => <tr key={row.id}>
              <td><b>{row.contact_name}</b></td><td>{row.contact_phone}</td><td><b>{row.instance_title}</b><small>{row.session_title}</small></td><td>{formatShanghaiDateTime(row.created_at)}</td><td><span className={`status-pill ${row.participation_status}`}>{participationLabels[row.participation_status] || "状态待确认"}</span></td><td>{paymentLabels[row.payment_status || ""] || "—"}</td><td><span className={`status-pill ${row.checkin_status}`}>{row.checkin_status === "checked_in" ? "已签到" : row.checkin_status === "revoked" ? "已撤销" : "待签到"}</span></td><td><Link className="table-link" href={`/registrations/${row.id}`}>详情 <ArrowRight /></Link></td>
            </tr>)}</tbody>
          </table>
        </div>
        <div className="pagination-row">
          <span>共 {total} 条 · 第 {pageNumber} / {Math.max(1, Math.ceil(total / pageSize))} 页</span>
          <div>
            <button className="secondary-button" disabled={loading || pageNumber <= 1} onClick={() => setPageNumber((value) => Math.max(1, value - 1))}>上一页</button>
            <button className="secondary-button" disabled={loading || pageNumber * pageSize >= total} onClick={() => setPageNumber((value) => value + 1)}>下一页</button>
          </div>
        </div>
      </section>
      {answerAccess && answerVisible && <section className="panel">
        <div className="panel-head"><div><h2>问卷答卷摘要</h2><p>此处仅显示字段完成情况和答案项数，下载文件不含原始答案或联系人；单笔答卷需进入报名详情并指定查看用途。</p></div><span className="privacy-badge">脱敏只读</span></div>
        <div className="filter-row registration-filters">
          <button className="secondary-button" disabled={answerLoading} onClick={() => void downloadAnswerExport("json")}><FileJson /> 导出 JSON</button>
          <button className="secondary-button" disabled={answerLoading} onClick={() => void downloadAnswerExport("csv")}><Download /> 导出 CSV</button>
          <button className="secondary-button" disabled={answerLoading} onClick={() => void loadAnswerSummaries()}><RefreshCw /> 刷新摘要</button>
        </div>
        {answerError && <div className="inline-message error table-message">{answerError}</div>}
        <div className="table-wrap">
          <table>
            <thead><tr><th>报名记录</th><th>字段</th><th>类型</th><th>必填</th><th>填写状态</th><th>答案项数</th><th>提交时间</th></tr></thead>
            <tbody>{answerLoading ? <tr><td colSpan={7} className="empty-cell">正在读取脱敏答卷摘要…</td></tr> : answerRows.length === 0 ? <tr><td colSpan={7} className="empty-cell">当前场次没有可显示的答卷摘要。</td></tr> : answerRows.map((row) => <tr key={`${row.registration_id}-${row.field_id}`}>
              <td title={row.registration_id}>{row.registration_id.slice(0, 8)}…</td><td><b>{row.field_label}</b><small>{row.field_code}</small></td><td>{answerTypeLabels[row.field_type] || row.field_type}</td><td>{row.required ? "是" : "否"}</td><td><span className={`status-pill ${row.answered ? "confirmed" : "pending_payment"}`}>{row.answered ? "已填写" : "未填写"}</span></td><td>{row.value_count}</td><td>{formatShanghaiDateTime(row.created_at)}</td>
            </tr>)}</tbody>
          </table>
        </div>
        <div className="pagination-row">
          <span>共 {answerTotal} 条 · 第 {answerPageNumber} / {Math.max(1, Math.ceil(answerTotal / answerPageSize))} 页</span>
          <div>
            <button className="secondary-button" disabled={answerLoading || answerPageNumber <= 1} onClick={() => setAnswerPageNumber((value) => Math.max(1, value - 1))}>上一页</button>
            <button className="secondary-button" disabled={answerLoading || answerPageNumber * answerPageSize >= answerTotal} onClick={() => setAnswerPageNumber((value) => value + 1)}>下一页</button>
          </div>
        </div>
      </section>}
    </div>
  );
}
