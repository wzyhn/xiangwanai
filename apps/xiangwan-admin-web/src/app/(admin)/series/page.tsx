"use client";

import { Eye, EyeOff, RefreshCw, Save } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { allPages, api, displayError, operationHeaders, operationKey } from "@/lib/api";
import type { Series } from "@/lib/types";

const statusLabel: Record<Series["status"], string> = {
  draft: "草稿",
  active: "启用中",
  archived: "已归档",
};

type SeriesDraft = { title: string; homeVisible: boolean };

export default function SeriesPage() {
  const [series, setSeries] = useState<Series[]>([]);
  const [drafts, setDrafts] = useState<Record<string, SeriesDraft>>({});
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState<string | null>(null);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");

  const load = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const result = await allPages<Series>("/series");
      setSeries(result.items);
      setDrafts(Object.fromEntries(result.items.map((item) => [item.id, {
        title: item.title,
        homeVisible: item.home_visible,
      }])));
    } catch (reason) {
      setError(displayError(reason));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { void load(); }, [load]);

  function setDraft(id: string, patch: Partial<SeriesDraft>) {
    setDrafts((current) => ({ ...current, [id]: { ...current[id], ...patch } }));
    setNotice("");
  }

  async function save(item: Series) {
    const draft = drafts[item.id];
    if (!draft || !draft.title.trim()) {
      setError("系列名称不能为空");
      return;
    }
    if (item.status === "archived" && draft.homeVisible) {
      setError("已归档系列不能重新显示在首页");
      return;
    }
    setSaving(item.id);
    setError("");
    setNotice("");
    try {
      const updated = await api<Series>(`/series/${item.id}`, {
        method: "PATCH",
        headers: operationHeaders(operationKey()),
        body: JSON.stringify({
          expected_version: item.version,
          title: draft.title.trim(),
          home_visible: draft.homeVisible,
        }),
      });
      setSeries((current) => current.map((value) => value.id === updated.id ? updated : value));
      setDrafts((current) => ({ ...current, [updated.id]: { title: updated.title, homeVisible: updated.home_visible } }));
      setNotice(`“${updated.title}”已保存，已有期次的标题快照不会被改写。`);
    } catch (reason) {
      setError(displayError(reason));
    } finally {
      setSaving(null);
    }
  }

  return (
    <div className="page-content">
      <section className="metric-grid compact">
        <article className="metric blue"><span>系列总数</span><strong>{loading ? "—" : series.length}</strong><small>长期活动栏目</small></article>
        <article className="metric green"><span>首页展示</span><strong>{loading ? "—" : series.filter((item) => item.home_visible).length}</strong><small>小程序首页可见</small></article>
        <article className="metric yellow"><span>已归档</span><strong>{loading ? "—" : series.filter((item) => item.status === "archived").length}</strong><small>历史栏目</small></article>
      </section>

      <section className="panel">
        <div className="panel-head">
          <div><h2>系列管理</h2><p>维护长期活动栏目名称和首页曝光。修改系列名称不会回写已经创建的期次标题。</p></div>
          <button className="secondary-button" onClick={() => void load()} disabled={loading}><RefreshCw /> 刷新</button>
        </div>
        {error && <div className="inline-message error table-message">{error}</div>}
        {notice && <div className="inline-message success table-message">{notice}</div>}
        <div className="table-wrap">
          <table>
            <thead><tr><th>系列名称</th><th>状态</th><th>已发布期次</th><th>首页展示</th><th>版本</th><th /></tr></thead>
            <tbody>
              {loading ? <tr><td colSpan={6} className="empty-cell">正在加载系列…</td></tr> : series.length === 0 ? <tr><td colSpan={6} className="empty-cell">暂无系列，创建新一期时可以建立系列。</td></tr> : series.map((item) => {
                const draft = drafts[item.id] || { title: item.title, homeVisible: item.home_visible };
                const changed = draft.title.trim() !== item.title || draft.homeVisible !== item.home_visible;
                return <tr key={item.id}>
                  <td>
                    <label>系列名称 <span className="required-mark" aria-hidden="true">*</span><input required aria-required="true" value={draft.title} maxLength={200} disabled={saving === item.id} onChange={(event) => setDraft(item.id, { title: event.target.value })} aria-label={`${item.title}系列名称`} /></label>
                    <small>{item.is_recurring ? "连续系列" : "单次栏目"}</small>
                  </td>
                  <td><span className={`status-pill ${item.status}`}>{statusLabel[item.status]}</span></td>
                  <td>{item.published_instances}</td>
                  <td>
                    <button type="button" className="table-link" disabled={saving === item.id || item.status === "archived"} onClick={() => setDraft(item.id, { homeVisible: !draft.homeVisible })}>
                      {draft.homeVisible ? <Eye /> : <EyeOff />} {draft.homeVisible ? "展示" : "隐藏"}
                    </button>
                  </td>
                  <td>v{item.version}</td>
                  <td><button className="primary-button" disabled={!changed || saving === item.id} onClick={() => void save(item)}><Save /> {saving === item.id ? "保存中…" : "保存"}</button></td>
                </tr>;
              })}
            </tbody>
          </table>
        </div>
      </section>
    </div>
  );
}
