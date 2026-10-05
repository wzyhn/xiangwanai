"use client";

import { ArrowLeft, RefreshCw } from "lucide-react";
import Link from "next/link";
import { useParams } from "next/navigation";
import { useEffect, useState } from "react";
import { api, displayError } from "@/lib/api";
import { formatCentsAsYuan } from "@/lib/money";
import { formatShanghaiDateTime } from "@/lib/time";
import type { OrderListItem } from "@/lib/types";

const paymentLabels: Record<string, string> = {
  pending: "待支付",
  unknown: "支付确认中",
  paid_confirmed: "已支付",
  settled_zero: "优惠券结算",
  closed_unpaid: "未付款关闭",
};

const refundLabels: Record<string, string> = {
  pending_manual: "待人工处理",
  processing: "处理中",
  failed: "处理失败",
  refunded: "已退款",
  rejected: "不予退款",
};

export default function OrderDetailPage() {
  const params = useParams<{ orderId: string }>();
  const [order, setOrder] = useState<OrderListItem | null>(null);
  const [refreshKey, setRefreshKey] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  useEffect(() => {
    const request = new AbortController();
    setOrder(null);
    setLoading(true);
    setError("");
    void api<OrderListItem>(`/orders/${encodeURIComponent(params.orderId)}`, { signal: request.signal })
      .then((value) => {
        if (request.signal.aborted) return;
        if (value.order_id !== params.orderId) throw new Error("订单详情与当前记录不匹配");
        setOrder(value);
      })
      .catch((reason: unknown) => {
        if (!request.signal.aborted) setError(displayError(reason));
      })
      .finally(() => {
        if (!request.signal.aborted) setLoading(false);
      });
    return () => request.abort();
  }, [params.orderId, refreshKey]);

  return <div className="page-content detail-page">
    <Link className="back-link" href="/orders"><ArrowLeft /> 返回订单查询</Link>
    {loading ? <div className="inline-message">正在读取订单详情…</div> : error ? <div className="inline-message error">{error}</div> : order && <>
      <section className="detail-hero"><div><span className="eyebrow">订单记录</span><h2>{order.instance_title}</h2><p>{order.series_title} · {order.session_title}</p></div><span className={`status-pill ${order.payment_status}`}>{paymentLabels[order.payment_status] || order.payment_status}</span></section>
      <div className="fact-grid">
        <article><span>原价</span><b>{formatCentsAsYuan(order.original_price_cents)}</b><small>下单时价格快照</small></article>
        <article><span>优惠抵扣</span><b>{formatCentsAsYuan(order.discount_cents)}</b><small>下单时抵扣快照</small></article>
        <article><span>应付金额</span><b>{formatCentsAsYuan(order.payable_cents)}</b><small>原价减抵扣</small></article>
        <article><span>确认实付</span><b>{order.actual_paid_cents === null ? "—" : formatCentsAsYuan(order.actual_paid_cents)}</b><small>仅服务端支付事实确认后计入</small></article>
      </div>
      <section className="panel">
        <div className="panel-head"><div><h3>支付与退款</h3><p>订单支付、报名参与、退款处理是独立状态，不能互相替代。</p></div><button className="secondary-button" type="button" onClick={() => setRefreshKey((value) => value + 1)}><RefreshCw /> 刷新</button></div>
        <p>支付状态：<b>{paymentLabels[order.payment_status] || order.payment_status}</b></p>
        <p>退款状态：<b>{order.refund_case_id ? refundLabels[order.refund_status] || order.refund_status : "无退款记录"}</b></p>
        {order.refund_case_id && <><p>退款单号：{order.refund_case_id}</p><p>应退金额：{formatCentsAsYuan(order.requested_refund_cents)}</p><p>已退金额：{formatCentsAsYuan(order.successful_refund_cents)}</p></>}
      </section>
      <section className="panel"><h3>记录时间</h3><p>创建：{formatShanghaiDateTime(order.created_at)}</p><p>更新：{formatShanghaiDateTime(order.updated_at)}</p>{order.paid_at && <p>支付确认：{formatShanghaiDateTime(order.paid_at)}</p>}{order.closed_at && <p>未付款关闭：{formatShanghaiDateTime(order.closed_at)}</p>}</section>
      <section className="panel"><h3>关联报名</h3><p>订单号：{order.order_id}</p><p>报名号：{order.registration_id}</p></section>
    </>}
  </div>;
}
