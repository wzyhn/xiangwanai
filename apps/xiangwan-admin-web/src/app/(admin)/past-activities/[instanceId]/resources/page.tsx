"use client";

import Link from "next/link";
import { useParams } from "next/navigation";
import { useEffect, useRef, useState } from "react";
import { api, definitiveFailure, displayError, operationHeaders, operationKey } from "@/lib/api";
import {
  parseReviewResourceRecovery,
  type ReviewResourceRecovery,
} from "@/lib/review-resource-recovery";
import type { InstanceDetail } from "@/lib/types";

type ResourceLink = { enabled: boolean; title: string; subtitle: string; url: string };
type Review = {
  relation_id: string;
  instance_id: string;
  session_id?: string;
  editable: boolean;
  expected_target_version: number;
  photo_curation_version: number;
  sort_order: number;
  title: string;
  description: string;
  video_url: string;
  photos: string[];
  video_channel?: { finder_user_name: string; feed_id: string };
  recording?: ResourceLink;
  materials?: ResourceLink;
};
const emptyLink = (title: string): ResourceLink => ({
  enabled: false,
  title,
  subtitle: "",
  url: "",
});

export default function PastActivityResourcesPage() {
  const { instanceId } = useParams<{ instanceId: string }>();
  const [detail, setDetail] = useState<InstanceDetail | null>(null);
  const [items, setItems] = useState<Review[]>([]);
  const preferredRelation = useRef("");
  const [draft, setDraft] = useState<Review | null>(null);
  const [refresh, setRefresh] = useState(0);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [pending, setPending] = useState<ReviewResourceRecovery | null>(null);
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");
  const recoveryKey = `xiangwan-admin-review-editor:${instanceId}`;

  useEffect(() => {
    const request = new AbortController();
    setLoading(true);
    setError("");
    setDraft(null);
    setItems([]);
    try {
      const raw = window.sessionStorage.getItem(recoveryKey);
      setPending(raw ? parseReviewResourceRecovery(JSON.parse(raw)) : null);
    } catch {
      setPending(null);
    }
    void Promise.all([
      api<InstanceDetail>(`/instances/${encodeURIComponent(instanceId)}`, {
        signal: request.signal,
      }),
      api<{ items: Review[] }>(`/instances/${encodeURIComponent(instanceId)}/review-resources`, {
        signal: request.signal,
      }),
    ])
      .then(([instance, reviews]) => {
        if (request.signal.aborted) return;
        if (
          instance.instance.id !== instanceId ||
          reviews.items.some((item) => item.instance_id !== instanceId)
        )
          throw new Error("资料与当前期次不匹配");
        setDetail(instance);
        setItems(reviews.items);
        setDraft(
          reviews.items.find((item) => item.relation_id === preferredRelation.current) ||
            reviews.items[0] ||
            null,
        );
      })
      .catch((reason: unknown) => {
        if (!request.signal.aborted) setError(displayError(reason));
      })
      .finally(() => {
        if (!request.signal.aborted) setLoading(false);
      });
    return () => request.abort();
  }, [instanceId, refresh, recoveryKey]);

  async function save() {
    if (saving || loading || (!pending && (!draft || !draft.editable))) return;
    let recovery = pending;
    if (!recovery && draft) {
      if (!draft.title.trim()) {
        setMessage("请填写回顾标题");
        return;
      }
      if (
        draft.video_channel &&
        (!draft.video_channel.finder_user_name.startsWith("sph") ||
          !draft.video_channel.feed_id ||
          draft.video_url.trim())
      ) {
        setMessage("请填写完整的视频号 ID 与视频 ID，并清空旧网页链接");
        return;
      }
      const link = (entry?: ResourceLink) => {
        if (!entry) return undefined;
        if (!entry.title.trim()) throw new Error("已添加的资料需要填写显示名称");
        return {
          enabled: entry.enabled,
          title: entry.title.trim(),
          subtitle: entry.subtitle.trim(),
          url: entry.url.trim(),
        };
      };
      try {
        recovery = {
          version: 2,
          operation: operationKey(),
          body: JSON.stringify({
            expected_target_version: draft.expected_target_version,
            replaces_relation_id: draft.relation_id,
            expected_photo_curation_version: draft.photo_curation_version,
            session_id: draft.session_id || undefined,
            title: draft.title.trim(),
            description: draft.description.trim(),
            video_url: draft.video_url.trim(),
            video_channel: draft.video_channel,
            photos: draft.photos,
            files: [],
            recording: link(draft.recording),
            materials: link(draft.materials),
            sort_order: draft.sort_order,
          }),
        };
        // Persist before sending. A refresh can then retry the original operation
        // even when the old relation has already been replaced on the server.
        window.sessionStorage.setItem(recoveryKey, JSON.stringify(recovery));
        setPending(recovery);
      } catch (reason) {
        setMessage(displayError(reason));
        return;
      }
    }
    if (!recovery) return;
    setSaving(true);
    setMessage("");
    try {
      const receipt = await api<{ relation_id: string }>(
        `/instances/${encodeURIComponent(instanceId)}/review-resources`,
        {
          method: "POST",
          headers: operationHeaders(recovery.operation),
          body: recovery.body,
        },
      );
      preferredRelation.current = receipt.relation_id;
      window.sessionStorage.removeItem(recoveryKey);
      setPending(null);
      setMessage("本期资料已更新，其他期次的资料保持原样。");
      setRefresh((value) => value + 1);
    } catch (reason) {
      if (definitiveFailure(reason)) {
        window.sessionStorage.removeItem(recoveryKey);
        setPending(null);
        setMessage(displayError(reason));
      } else {
        setMessage("结果尚未确认，请重试上次保存。页面会原样发送同一次操作。");
      }
    } finally {
      setSaving(false);
    }
  }

  const locked = saving || Boolean(pending) || !draft?.editable;
  return (
    <div className="page-content detail-page">
      <div className="page-heading">
        <div>
          <h1>本期资料与视频回顾</h1>
          <p>{detail?.instance.title || "加载活动…"}</p>
        </div>
        <Link className="secondary-button" href={`/activities/${encodeURIComponent(instanceId)}`}>
          返回活动
        </Link>
      </div>
      {message && (
        <div className="inline-message" role="status">
          {message}
        </div>
      )}
      {error && (
        <div className="inline-message" role="alert">
          {error}
        </div>
      )}
      <section className="panel">
        <p>
          录音梳理和活动资料分别设置前台展示。关闭后保留填写内容，重新开启即可恢复。修改只影响当前期次，照片原有顺序和隐藏状态会保留。
        </p>
        <Link
          className="table-link"
          href={`/past-activities/${encodeURIComponent(instanceId)}/photos`}
        >
          管理照片的顺序与展示
        </Link>
        <button
          type="button"
          className="secondary-button"
          disabled={saving}
          onClick={() => setRefresh((value) => value + 1)}
        >
          刷新当前资料
        </button>
        {loading ? (
          <p>正在读取已发布资料…</p>
        ) : items.length === 0 ? (
          <p>本期还没有已发布回顾，请在活动详情中添加。</p>
        ) : (
          <>
            <label>
              选择回顾
              <select
                disabled={saving || Boolean(pending)}
                value={draft?.relation_id || ""}
                onChange={(event) =>
                  setDraft(items.find((item) => item.relation_id === event.target.value) || null)
                }
              >
                {items.map((item) => (
                  <option key={item.relation_id} value={item.relation_id}>
                    {item.title}
                    {item.session_id
                      ? ` · ${detail?.sessions.find((session) => session.id === item.session_id)?.title || "场次资料"}`
                      : " · 整期回顾"}
                  </option>
                ))}
              </select>
            </label>
            {draft && (
              <div className="field-grid">
                {!draft.editable && (
                  <p className="full">
                    此回顾包含平台文件，请使用原有照片管理入口；链接编辑不会移除这些文件。
                  </p>
                )}
                <label>
                  回顾标题 <span className="required-mark">*</span>
                  <input
                    required
                    disabled={locked}
                    value={draft.title}
                    onChange={(event) => setDraft({ ...draft, title: event.target.value })}
                  />
                </label>
                <label>
                  视频号链接（可选）
                  <input
                    inputMode="url"
                    disabled={locked}
                    value={draft.video_url}
                    onChange={(event) => setDraft({ ...draft, video_url: event.target.value })}
                  />
                </label>
                <label>
                  视频号 ID（可选）{" "}
                  {draft.video_channel && <span className="required-mark">*</span>}
                  <input
                    disabled={locked}
                    required={Boolean(draft.video_channel)}
                    value={draft.video_channel?.finder_user_name || ""}
                    placeholder="sph…（视频号助手首页）"
                    onChange={(event) => {
                      const finder_user_name = event.target.value;
                      const feed_id = draft.video_channel?.feed_id || "";
                      setDraft({
                        ...draft,
                        video_channel:
                          finder_user_name || feed_id ? { finder_user_name, feed_id } : undefined,
                      });
                    }}
                  />
                </label>
                <label>
                  视频 ID（可选） {draft.video_channel && <span className="required-mark">*</span>}
                  <input
                    disabled={locked}
                    required={Boolean(draft.video_channel)}
                    value={draft.video_channel?.feed_id || ""}
                    placeholder="feedId（视频号助手内容管理）"
                    onChange={(event) => {
                      const feed_id = event.target.value;
                      const finder_user_name = draft.video_channel?.finder_user_name || "";
                      setDraft({
                        ...draft,
                        video_channel:
                          finder_user_name || feed_id ? { finder_user_name, feed_id } : undefined,
                      });
                    }}
                  />
                </label>
                <p className="full">
                  原生视频回顾请同时填写两个 ID，旧网页链接与这组 ID 选择其一。
                </p>
                <label className="full">
                  回顾描述（可选）
                  <textarea
                    disabled={locked}
                    value={draft.description}
                    onChange={(event) => setDraft({ ...draft, description: event.target.value })}
                  />
                </label>
                {(["recording", "materials"] as const).map((kind) => {
                  const name = kind === "recording" ? "录音梳理" : "活动资料";
                  const entry = draft[kind] || emptyLink(name);
                  const required = Boolean(draft[kind]);
                  const change = (patch: Partial<ResourceLink>) =>
                    setDraft({ ...draft, [kind]: { ...entry, ...patch } });
                  return (
                    <div key={kind} className="full">
                      <h2>{name}</h2>
                      <p>
                        {draft[kind]
                          ? entry.enabled
                            ? "已添加 · 前台展示"
                            : "已添加 · 暂时隐藏"
                          : "未添加"}
                        。链接可以留空；隐藏后仍保留已填写内容。
                      </p>
                      <button
                        type="button"
                        className="secondary-button"
                        disabled={locked}
                        onClick={() =>
                          setDraft({
                            ...draft,
                            [kind]: draft[kind] ? undefined : { ...entry, enabled: true },
                          })
                        }
                      >
                        {draft[kind] ? "移除此资料卡片" : `添加${name}卡片`}
                      </button>
                      <label style={{ display: "inline-flex", alignItems: "center", gap: 8 }}>
                        <input
                          type="checkbox"
                          disabled={locked}
                          checked={entry.enabled}
                          onChange={(event) => change({ enabled: event.target.checked })}
                        />
                        前台展示{name}
                      </label>
                      <div className="field-grid">
                        <label>
                          {name}显示名称 {required && <span className="required-mark">*</span>}
                          <input
                            disabled={locked}
                            required={required}
                            aria-required={required}
                            value={entry.title}
                            onChange={(event) => change({ title: event.target.value })}
                          />
                        </label>
                        <label>
                          {name}副标题（可选）
                          <input
                            disabled={locked}
                            value={entry.subtitle}
                            onChange={(event) => change({ subtitle: event.target.value })}
                          />
                        </label>
                        <label className="full">
                          {name}飞书链接（可选，留空显示“暂未配置链接”）
                          <input
                            inputMode="url"
                            disabled={locked}
                            value={entry.url}
                            onChange={(event) => change({ url: event.target.value })}
                            placeholder="https://feishu.cn/..."
                          />
                        </label>
                      </div>
                    </div>
                  );
                })}
              </div>
            )}
          </>
        )}
        <button
          type="button"
          className="primary-button"
          disabled={saving || loading || (!pending && !draft?.editable)}
          onClick={() => void save()}
        >
          {saving ? "正在保存…" : pending ? "重试上次保存" : "保存本期资料"}
        </button>
      </section>
    </div>
  );
}
