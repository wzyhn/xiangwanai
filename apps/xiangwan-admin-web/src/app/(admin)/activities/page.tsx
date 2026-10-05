"use client";

import { ArrowRight, CalendarPlus, CircleDot, RefreshCw } from "lucide-react";
import Link from "next/link";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { allPages, displayError } from "@/lib/api";
import type { InstanceListItem, Series } from "@/lib/types";

const statusLabel: Record<string, string> = {
  draft: "草稿",
  pending_publish: "待发布",
  published: "已发布",
  completed: "已结束",
  cancelled: "已取消",
  archived: "已归档",
};

export default function ActivitiesPage() {
  const [series, setSeries] = useState<Series[]>([]);
  const [seriesTotal, setSeriesTotal] = useState(0);
  const [instances, setInstances] = useState<InstanceListItem[]>([]);
  const [selectedSeries, setSelectedSeries] = useState("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const loadGeneration = useRef(0);

  const load = useCallback(async () => {
    const generation = ++loadGeneration.current;
    setLoading(true);
    setError("");
    try {
      const suffix = selectedSeries ? `?series_id=${selectedSeries}` : "";
      const [seriesPage, instancePage] = await Promise.all([
        allPages<Series>("/series"),
        allPages<InstanceListItem>(`/instances${suffix}`),
      ]);
      if (generation !== loadGeneration.current) return;
      setSeries(seriesPage.items);
      setSeriesTotal(seriesPage.total);
      setInstances(instancePage.items);
    } catch (reason) {
      if (generation !== loadGeneration.current) return;
      setError(displayError(reason));
    } finally {
      if (generation === loadGeneration.current) setLoading(false);
    }
  }, [selectedSeries]);

  useEffect(() => {
    void load();
    return () => { loadGeneration.current += 1; };
  }, [load]);

  const metrics = useMemo(() => ({
    published: instances.filter((item) => item.instance.status === "published").length,
    draft: instances.filter((item) => item.instance.status === "draft").length,
    sessions: instances.reduce((total, item) => total + item.session_count, 0),
  }), [instances]);

  return (
    <div className="page-content">
      <section className="metric-grid compact">
        <article className="metric yellow"><span>活动系列</span><strong>{seriesTotal}</strong><small>长期栏目</small></article>
        <article className="metric blue"><span>已发布期次</span><strong>{metrics.published}</strong><small>小程序可见</small></article>
        <article className="metric pink"><span>待完善草稿</span><strong>{metrics.draft}</strong><small>尚未公开</small></article>
        <article className="metric green"><span>场次总数</span><strong>{metrics.sessions}</strong><small>当前筛选</small></article>
      </section>

      <section className="panel">
        <div className="panel-head">
          <div><h2>活动期次</h2><p>按长期活动系列管理每一期内容与场次。</p></div>
          <Link className="primary-button" href="/activities/new"><CalendarPlus /> 创建新一期</Link>
        </div>
        <div className="filter-row">
          <label>活动系列
            <select value={selectedSeries} onChange={(event) => setSelectedSeries(event.target.value)}>
              <option value="">全部系列</option>
              {series.map((item) => <option key={item.id} value={item.id}>{item.title}</option>)}
            </select>
          </label>
          <button className="secondary-button" onClick={() => void load()}><RefreshCw /> 刷新</button>
        </div>
        {error && <div className="inline-message error table-message">{error}</div>}
        <div className="table-wrap">
          <table>
            <thead><tr><th>活动期次</th><th>活动系列</th><th>场次</th><th>发布次数</th><th>状态</th><th /></tr></thead>
            <tbody>
              {loading ? <tr><td colSpan={6} className="empty-cell">正在加载活动…</td></tr> : instances.length === 0 ? (
                <tr><td colSpan={6} className="empty-cell">暂无活动期次，先创建第一期。</td></tr>
              ) : instances.map((item) => (
                <tr key={item.instance.id}>
                  <td><span className="row-title"><CircleDot /><b>{item.instance.title}</b></span></td>
                  <td>{item.series_title}</td>
                  <td>{item.session_count}</td>
                  <td>{item.instance.publication_version} 次</td>
                  <td><span className={`status-pill ${item.instance.status}`}>{statusLabel[item.instance.status] || item.instance.status}</span></td>
                  <td><Link className="table-link" href={`/activities/${item.instance.id}`}>查看 <ArrowRight /></Link></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>
    </div>
  );
}
