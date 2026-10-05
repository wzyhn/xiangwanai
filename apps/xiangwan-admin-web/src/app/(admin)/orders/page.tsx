"use client";

import { ArrowRight, RefreshCw } from "lucide-react";
import Link from "next/link";
import { useEffect, useState } from "react";
import { api, displayError } from "@/lib/api";
import { formatCentsAsYuan } from "@/lib/money";
import { formatShanghaiDateTime } from "@/lib/time";
import type { OrderListPage } from "@/lib/types";

type OrderStatus = OrderListPage["status"];

const statusTabs: Array<{ status: OrderStatus; label: string }> = [
  { status: "all", label: "全部订单" },
  { status: "pending", label: "待支付" },
  { status: "unknown", label: "支付确认中" },
  { status: "paid_confirmed", label: "已支付" },
  { status: "settled_zero", label: "优惠券结算" },
  { status: "closed_unpaid", label: "未付款关闭" },
];

const refundLabels: Record<string, string> = {
  pending_manual: "待人工处理",
  processing: "处理中",
  failed: "处理失败",
  refunded: "已退款",
  rejected: "不予退款",
};

export default function OrdersPage() {
  const [status, setStatus] = useState<OrderStatus>("all");
  const [cursorHistory, setCursorHistory] = useState<string[]>([""]);
  const [pageIndex, setPageIndex] = useState(0);
  const [refreshKey, setRefreshKey] = useState(0);
  const [page, setPage] = useState<OrderListPage | null>(null);
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
    void api<OrderListPage>(`/orders?${query}`, { signal: request.signal })
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

  function changeStatus(next: OrderStatus) {
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
      <div className="panel-head"><div><h2>订单查询</h2><p>核对订单支付事实、价格快照与退款进度；支付确认中不会显示为已付款。</p></div><span className="privacy-badge">活动运营权限</span></div>
      <div className="filter-row">
        <label>支付状态 <select value={status} onChange={(event) => changeStatus(event.target.value as OrderStatus)}>{statusTabs.map((tab) => <option key={tab.status} value={tab.status}>{tab.label}</option>)}</select></label>
        <button className="secondary-button" type="button" disabled={loading} onClick={() => { setCursorHistory([""]); setPageIndex(0); setRefreshKey((value) => value + 1); }}><RefreshCw /> 刷新</button>
      </div>
      {error && <div className="inline-message error table-message">{error}</div>}
      <div className="table-wrap"><table>
        <thead><tr><th>活动 / 场次</th><th>支付状态</th><th>价格快照</th><th>实付金额</th><th>退款状态</th><th>创建时间</th><th /></tr></thead>
        <tbody>{loading ? <tr><td colSpan={7} className="empty-cell">正在读取订单…</td></tr> : !page?.items.length ? <tr><td colSpan={7} className="empty-cell">当前筛选没有订单。</td></tr> : page.items.map((item) => <tr key={item.order_id}>
          <td><b>{item.instance_title}</b><small>{item.series_title} · {item.session_title}</small></td>
          <td><span className={`status-pill ${item.payment_status}`}>{statusTabs.find((tab) => tab.status === item.payment_status)?.label || item.payment_status}</span></td>
          <td><b>{formatCentsAsYuan(item.payable_cents)}</b><small>原价 {formatCentsAsYuan(item.original_price_cents)} · 抵扣 {formatCentsAsYuan(item.discount_cents)}</small></td>
          <td>{item.actual_paid_cents === null ? "—" : formatCentsAsYuan(item.actual_paid_cents)}</td>
          <td>{item.refund_case_id ? refundLabels[item.refund_status] || item.refund_status : "—"}</td>
          <td>{formatShanghaiDateTime(item.created_at)}</td>
          <td><Link className="table-link" href={`/orders/${item.order_id}`}>订单详情 <ArrowRight /></Link></td>
        </tr>)}</tbody>
      </table></div>
      <div className="pagination-row"><span>第 {pageIndex + 1} 页</span><div>
        <button className="secondary-button" type="button" disabled={loading || pageIndex === 0} onClick={() => setPageIndex((current) => current - 1)}>上一页</button>
        <button className="secondary-button" type="button" disabled={loading || !page?.next_cursor} onClick={nextPage}>下一页</button>
      </div></div>
    </section>
  </div>;
}
