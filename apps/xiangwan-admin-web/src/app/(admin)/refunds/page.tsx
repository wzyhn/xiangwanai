"use client";

import { ArrowRight, RefreshCw } from "lucide-react";
import Link from "next/link";
import { useEffect, useState } from "react";
import { api, displayError } from "@/lib/api";
import { formatShanghaiDateTime } from "@/lib/time";
import { formatCentsAsYuan } from "@/lib/money";
import type { RefundQueuePage } from "@/lib/types";

type RefundQueueStatus = RefundQueuePage["status"];

const statusTabs: Array<{ status: RefundQueueStatus; label: string }> = [
  { status: "pending_manual", label: "待人工处理" },
  { status: "processing", label: "处理中" },
  { status: "failed", label: "处理失败" },
];

const reasonLabels: Record<string, string> = {
  hold_expired_after_payment: "支付后名额已过期",
  order_closed_after_payment: "订单关闭后到账",
  session_unavailable_after_payment: "活动场次不可参加",
  user_cancelled: "用户取消报名",
  session_cancelled: "活动场次取消",
  instance_cancelled: "整期活动下架",
  operator_adjustment: "人工更正",
};

export default function RefundsPage() {
  const [status, setStatus] = useState<RefundQueueStatus>("pending_manual");
  const [cursorHistory, setCursorHistory] = useState<string[]>([""]);
  const [pageIndex, setPageIndex] = useState(0);
  const [refreshKey, setRefreshKey] = useState(0);
  const [page, setPage] = useState<RefundQueuePage | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  useEffect(() => {
    const request = new AbortController();
    const query = new URLSearchParams({ status, limit: "50" });
    const cursor = cursorHistory[pageIndex];
    if (cursor) query.set("cursor", cursor);
    setPage(null);
    setLoading(true);
    setError("");
    void api<RefundQueuePage>(`/refund-cases?${query}`, { signal: request.signal })
      .then((value) => {
        if (!request.signal.aborted) setPage(value);
      })
      .catch((reason: unknown) => {
        if (!request.signal.aborted) setError(displayError(reason));
      })
      .finally(() => {
        if (!request.signal.aborted) setLoading(false);
      });
    return () => request.abort();
  }, [status, cursorHistory, pageIndex, refreshKey]);

  function changeStatus(next: RefundQueueStatus) {
    setStatus(next);
    setCursorHistory([""]);
    setPageIndex(0);
  }

  function nextPage() {
    const nextCursor = page?.next_cursor;
    if (!nextCursor) return;
    setCursorHistory((current) => [...current.slice(0, pageIndex + 1), nextCursor]);
    setPageIndex((current) => current + 1);
  }

  return <div className="page-content">
    <section className="panel no-top-margin">
      <div className="panel-head"><div><h2>退款待办</h2><p>按退款状态核对报名、活动和应退金额；退款处理结果以服务端记录为准。</p></div><span className="privacy-badge">财务权限</span></div>
      <div className="filter-row">
        <label>处理状态 <select value={status} onChange={(event) => changeStatus(event.target.value as RefundQueueStatus)}>{statusTabs.map((tab) => <option key={tab.status} value={tab.status}>{tab.label}</option>)}</select></label>
        <button className="secondary-button" type="button" disabled={loading} onClick={() => { setCursorHistory([""]); setPageIndex(0); setRefreshKey((value) => value + 1); }}><RefreshCw /> 刷新</button>
      </div>
      {error && <div className="inline-message error table-message">{error}</div>}
      <div className="table-wrap"><table>
        <thead><tr><th>活动 / 场次</th><th>退款原因</th><th>应退金额</th><th>状态</th><th>创建时间</th><th /></tr></thead>
        <tbody>{loading ? <tr><td colSpan={6} className="empty-cell">正在读取退款待办…</td></tr> : !page?.items.length ? <tr><td colSpan={6} className="empty-cell">当前状态没有待办记录。</td></tr> : page.items.map((item) => <tr key={item.case_id}>
          <td><b>{item.instance_title}</b><small>{item.series_title} · {item.session_title}</small></td>
          <td>{reasonLabels[item.reason_code] || item.reason_code}</td>
          <td><b>{formatCentsAsYuan(item.requested_refund_cents)}</b></td>
          <td><span className={`status-pill ${item.status}`}>{statusTabs.find((tab) => tab.status === item.status)?.label || item.status}</span></td>
          <td>{formatShanghaiDateTime(item.created_at)}</td>
          <td><Link className="table-link" href={`/refunds/${item.case_id}`}>退款详情 <ArrowRight /></Link></td>
        </tr>)}</tbody>
      </table></div>
      <div className="pagination-row">
        <span>第 {pageIndex + 1} 页</span>
        <div>
          <button className="secondary-button" type="button" disabled={loading || pageIndex === 0} onClick={() => setPageIndex((current) => current - 1)}>上一页</button>
          <button className="secondary-button" type="button" disabled={loading || !page?.next_cursor} onClick={nextPage}>下一页</button>
        </div>
      </div>
    </section>
  </div>;
}
