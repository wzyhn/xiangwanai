"use client";

import { RefreshCw } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { api, displayError } from "@/lib/api";
import { formatShanghaiDateTime } from "@/lib/time";
import type { AuditEventPage } from "@/lib/types";

const pageSize = 50;
const actionLabels: Record<string, string> = {
  "admin.audit_list_read": "查看审计日志",
  "registration.answer_detail_read": "查看单笔问卷答卷",
  "registration.answer_summary_read": "查看问卷摘要",
  "registration.contact_read": "查看报名联系人",
};

export default function AuditEventsPage() {
  const [pageNumber, setPageNumber] = useState(1);
  const [refreshKey, setRefreshKey] = useState(0);
  const [result, setResult] = useState<AuditEventPage | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const snapshot = useRef<string | null>(null);

  useEffect(() => {
    const request = new AbortController();
    const query = new URLSearchParams({ page: String(pageNumber), page_size: String(pageSize) });
    if (snapshot.current) query.set("as_of", snapshot.current);
    setLoading(true);
    setResult(null);
    setError("");
    void api<AuditEventPage>(`/audit-events?${query}`, { signal: request.signal })
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

  return <div className="page-content">
    <section className="panel no-top-margin">
      <div className="panel-head"><div><h2>后台审计日志</h2><p>显示操作类型、执行者和目标记录。详细请求内容及敏感字段不会在此展示。</p></div><span className="privacy-badge">仅系统管理员</span></div>
      <div className="filter-row"><button className="secondary-button" type="button" disabled={loading} onClick={refresh}><RefreshCw /> 刷新</button></div>
      {error && <div className="inline-message error table-message">{error}</div>}
      <div className="table-wrap"><table>
        <thead><tr><th>时间</th><th>操作</th><th>执行者</th><th>目标</th></tr></thead>
        <tbody>{loading && !result ? <tr><td colSpan={4} className="empty-cell">正在读取审计日志…</td></tr> : !result?.items.length ? <tr><td colSpan={4} className="empty-cell">当前范围没有审计记录。</td></tr> : result.items.map((item) => <tr key={item.id}>
          <td>{formatShanghaiDateTime(item.occurred_at)}</td>
          <td><b>{actionLabels[item.action_code] || item.action_code}</b><small>{item.action_code}</small></td>
          <td><code>{item.actor_id}</code></td>
          <td><b>{item.target_type}</b><small><code>{item.target_id}</code></small></td>
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
