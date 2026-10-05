"use client";

import { ArrowRight, History, RefreshCw } from "lucide-react";
import Link from "next/link";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { allPages, displayError } from "@/lib/api";
import type { InstanceListItem } from "@/lib/types";

const statusLabel: Record<string, string> = {
  completed: "已结束",
  archived: "已归档",
};

function formatUpdatedAt(value?: string): string {
  if (!value) return "时间待补";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "时间待补";
  return new Intl.DateTimeFormat("zh-CN", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
  }).format(date);
}

export default function PastActivitiesPage() {
  const [items, setItems] = useState<InstanceListItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const generation = useRef(0);

  const load = useCallback(async () => {
    const current = ++generation.current;
    setLoading(true);
    setError("");
    try {
      const page = await allPages<InstanceListItem>("/instances");
      if (current !== generation.current) return;
      setItems(
        page.items.filter((item) => ["completed", "archived"].includes(item.instance.status)),
      );
    } catch (reason) {
      if (current === generation.current) setError(displayError(reason));
    } finally {
      if (current === generation.current) setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
    return () => {
      generation.current += 1;
    };
  }, [load]);

  const groups = useMemo(() => {
    const grouped = new Map<string, { seriesTitle: string; items: InstanceListItem[] }>();
    for (const item of items) {
      const key = item.instance.series_id;
      const existing = grouped.get(key);
      if (existing) existing.items.push(item);
      else grouped.set(key, { seriesTitle: item.series_title || "未命名系列", items: [item] });
    }
    return [...grouped.values()];
  }, [items]);
  const maintainedCount = items.filter(
    (item) =>
      Boolean(String(item.instance.cover_image_url || "").trim()) ||
      (item.instance.detail_blocks?.length || 0) > 0,
  ).length;
  const pendingMaintenanceCount = Math.max(0, items.length - maintainedCount);

  return (
    <div className="page-content">
      <section className="metric-grid compact">
        <article className="metric yellow">
          <span>往期系列</span>
          <strong>{groups.length}</strong>
          <small>全部系列</small>
        </article>
        <article className="metric blue">
          <span>已结束期次</span>
          <strong>{items.length}</strong>
          <small>完成或归档</small>
        </article>
        <article className={`metric ${pendingMaintenanceCount > 0 ? "yellow" : "green"}`}>
          <span>资料维护</span>
          <strong>
            {maintainedCount}/{items.length}
          </strong>
          <small>
            {items.length === 0
              ? "暂无已结束期次"
              : pendingMaintenanceCount > 0
                ? `${pendingMaintenanceCount} 期待完善封面或描述`
                : "封面与描述已维护"}
          </small>
        </article>
      </section>

      <section className="panel">
        <div className="panel-head">
          <div>
            <h2>往期活动</h2>
            <p>按系列查看全部已结束期次；每一期都保留独立回顾入口。</p>
          </div>
          <button className="secondary-button" onClick={() => void load()} disabled={loading}>
            <RefreshCw /> 刷新
          </button>
        </div>
        <div className="inline-message info">
          在「维护与查看」中修改封面和活动描述；在「资料与视频回顾」中补充已发布资料、设置展示开关和视频号回顾；在「整理照片」中隐藏、恢复或调整照片顺序。尚未添加回顾时，请先在期次详情新增本期活动回顾。
        </div>
        {error && <div className="inline-message error table-message">{error}</div>}
        {loading ? (
          <div className="empty-cell">正在加载往期活动…</div>
        ) : groups.length === 0 ? (
          <div className="empty-cell">暂无已结束的往期活动。</div>
        ) : (
          groups.map((group) => (
            <div className="past-activity-group" key={group.items[0].instance.series_id}>
              <div className="past-activity-group-head">
                <h3>{group.seriesTitle}</h3>
                <span>{group.items.length} 期</span>
              </div>
              <div className="table-wrap">
                <table>
                  <thead>
                    <tr>
                      <th>期次</th>
                      <th>状态</th>
                      <th>更新时间</th>
                      <th>封面与描述</th>
                      <th />
                    </tr>
                  </thead>
                  <tbody>
                    {group.items.map((item) => (
                      <tr key={item.instance.id}>
                        <td>
                          <span className="row-title">
                            <History />
                            <b>{item.instance.title}</b>
                          </span>
                        </td>
                        <td>
                          <span className={`status-pill ${item.instance.status}`}>
                            {statusLabel[item.instance.status] || item.instance.status}
                          </span>
                        </td>
                        <td>{formatUpdatedAt(item.instance.updated_at)}</td>
                        <td>
                          {item.instance.cover_image_url ? "封面已设置" : "封面待补"}
                          {" · "}
                          {item.instance.detail_blocks?.length
                            ? `${item.instance.detail_blocks.length} 个内容块`
                            : "描述待补"}
                        </td>
                        <td>
                          <Link className="table-link" href={`/activities/${item.instance.id}`}>
                            维护与查看 <ArrowRight />
                          </Link>
                          <Link
                            className="table-link"
                            href={`/past-activities/${item.instance.id}/resources`}
                          >
                            资料与视频回顾 <ArrowRight />
                          </Link>
                          <Link
                            className="table-link"
                            href={`/past-activities/${item.instance.id}/photos`}
                          >
                            整理照片 <ArrowRight />
                          </Link>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          ))
        )}
      </section>
    </div>
  );
}
