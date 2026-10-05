"use client";

import { ArrowLeft, RefreshCw } from "lucide-react";
import Link from "next/link";
import { useParams } from "next/navigation";
import { useEffect, useState } from "react";
import {
  ApiError,
  api,
  definitiveFailure,
  displayError,
  operationHeaders,
  operationKey,
} from "@/lib/api";
import {
  clearPendingPhotoCuration,
  readPendingPhotoCuration,
  savePendingPhotoCuration,
  type PendingPhotoCuration,
} from "@/lib/photo-curation-recovery";
import type { InstanceDetail } from "@/lib/types";

type ReviewDocument = { relation_id: string; session_id?: string; title: string };
type Photo = { block_id: string; url?: string; file_id?: string };
type PhotoCurationView = {
  relation_id: string;
  instance_id: string;
  session_id?: string;
  title: string;
  version: number;
  original_photos: Photo[];
  ordered_block_ids: string[];
  cover_block_id?: string;
};
type PhotoCurationReceipt = {
  relation_id: string;
  version: number;
  ordered_block_ids: string[];
  cover_block_id?: string;
};

function move(ids: string[], id: string, direction: -1 | 1): string[] {
  const index = ids.indexOf(id);
  const next = index + direction;
  if (index < 0 || next < 0 || next >= ids.length) return ids;
  const result = [...ids];
  [result[index], result[next]] = [result[next], result[index]];
  return result;
}

export default function PastActivityPhotosPage() {
  const { instanceId } = useParams<{ instanceId: string }>();
  const [instance, setInstance] = useState<InstanceDetail | null>(null);
  const [documents, setDocuments] = useState<ReviewDocument[]>([]);
  const [relationId, setRelationId] = useState("");
  const [view, setView] = useState<PhotoCurationView | null>(null);
  const [selected, setSelected] = useState<string[]>([]);
  const [coverBlockID, setCoverBlockID] = useState("");
  const [pending, setPending] = useState<PendingPhotoCuration | null>(null);
  const [refreshKey, setRefreshKey] = useState(0);
  const [loading, setLoading] = useState(true);
  const [viewLoading, setViewLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [messageKind, setMessageKind] = useState<"success" | "error" | "warning">("success");

  useEffect(() => {
    const request = new AbortController();
    setLoading(true);
    setError("");
    void Promise.all([
      api<InstanceDetail>(`/instances/${encodeURIComponent(instanceId)}`, {
        signal: request.signal,
      }),
      api<{ items: PhotoCurationView[] }>(
        `/instances/${encodeURIComponent(instanceId)}/review-photo-curations`,
        { signal: request.signal },
      ),
    ])
      .then(([detail, docs]) => {
        if (request.signal.aborted) return;
        if (detail.instance.id !== instanceId) throw new Error("活动资料与当前期次不匹配");
        setInstance(detail);
        if (
          !Array.isArray(docs.items) ||
          docs.items.some((item) => item.instance_id !== instanceId)
        ) {
          throw new Error("回顾列表与当前期次不匹配");
        }
        setDocuments(
          docs.items.map((item) => ({
            relation_id: item.relation_id,
            session_id: item.session_id,
            title: item.title,
          })),
        );
        setRelationId((current) =>
          docs.items.some((doc) => doc.relation_id === current)
            ? current
            : docs.items[0]?.relation_id || "",
        );
      })
      .catch((reason: unknown) => {
        if (!request.signal.aborted) setError(displayError(reason));
      })
      .finally(() => {
        if (!request.signal.aborted) setLoading(false);
      });
    return () => request.abort();
  }, [instanceId, refreshKey]);

  useEffect(() => {
    if (!relationId) {
      setView(null);
      setSelected([]);
      setCoverBlockID("");
      setPending(null);
      return;
    }
    const request = new AbortController();
    setView(null);
    setViewLoading(true);
    setError("");
    void api<PhotoCurationView>(
      `/review-resources/${encodeURIComponent(relationId)}/photo-curation`,
      {
        signal: request.signal,
      },
    )
      .then((result) => {
        if (request.signal.aborted) return;
        if (
          result.relation_id !== relationId ||
          result.instance_id !== instanceId ||
          !Array.isArray(result.original_photos) ||
          !Array.isArray(result.ordered_block_ids)
        ) {
          throw new Error("照片版本与当前回顾不匹配");
        }
        const restored = readPendingPhotoCuration(instanceId, relationId);
        setView(result);
        setPending(restored);
        setSelected(
          restored?.expectedVersion === result.version
            ? restored.orderedBlockIDs
            : result.ordered_block_ids,
        );
        setCoverBlockID(
          restored?.expectedVersion === result.version && restored.coverBlockID !== undefined
            ? restored.coverBlockID
            : result.cover_block_id || "",
        );
      })
      .catch((reason: unknown) => {
        if (!request.signal.aborted) setError(displayError(reason));
      })
      .finally(() => {
        if (!request.signal.aborted) setViewLoading(false);
      });
    return () => request.abort();
  }, [instanceId, relationId, refreshKey]);

  function toggle(blockId: string) {
    if (pending || saving) return;
    setSelected((current) =>
      current.includes(blockId) ? current.filter((id) => id !== blockId) : [...current, blockId],
    );
    if (coverBlockID === blockId) setCoverBlockID("");
    setMessage("");
  }

  async function save() {
    if (
      !view ||
      saving ||
      (pending?.expectedVersion !== undefined && pending.expectedVersion !== view.version)
    )
      return;
    if (
      selected.length === 0 &&
      !window.confirm("确定隐藏这份回顾的全部照片？文字和视频仍会公开。")
    )
      return;
    const operation = pending || {
      instanceId,
      relationId: view.relation_id,
      expectedVersion: view.version,
      orderedBlockIDs: [...selected],
      coverBlockID,
      operationKey: operationKey(),
    };
    if (
      pending &&
      (selected.join(",") !== pending.orderedBlockIDs.join(",") ||
        (pending.coverBlockID !== undefined && coverBlockID !== pending.coverBlockID))
    ) {
      setMessage("待核对操作只能以原照片顺序重试，请先核对后清除该操作。");
      setMessageKind("warning");
      return;
    }
    try {
      savePendingPhotoCuration(operation);
    } catch {
      setMessage("浏览器无法保存操作键，请启用本会话存储后重试。");
      setMessageKind("error");
      return;
    }
    setPending(operation);
    setSaving(true);
    setMessage("");
    try {
      const receipt = await api<PhotoCurationReceipt>(
        `/review-resources/${encodeURIComponent(view.relation_id)}/photo-curation`,
        {
          method: "POST",
          headers: operationHeaders(operation.operationKey),
          body: JSON.stringify({
            expected_version: operation.expectedVersion,
            ordered_block_ids: operation.orderedBlockIDs,
            ...(operation.coverBlockID !== undefined
              ? { cover_block_id: operation.coverBlockID }
              : {}),
          }),
        },
      );
      if (
        receipt.relation_id !== view.relation_id ||
        receipt.version !== operation.expectedVersion + 1 ||
        receipt.ordered_block_ids.join(",") !== operation.orderedBlockIDs.join(",") ||
        (operation.coverBlockID !== undefined &&
          (receipt.cover_block_id || "") !== operation.coverBlockID)
      ) {
        throw new Error("照片修订回执与当前资料不匹配，请刷新核对。");
      }
      clearPendingPhotoCuration(instanceId, view.relation_id);
      setPending(null);
      setMessage("照片顺序和封面已保存，正在刷新公开回顾。");
      setMessageKind("success");
      setRefreshKey((value) => value + 1);
    } catch (reason: unknown) {
      if (definitiveFailure(reason) && !(reason instanceof ApiError && reason.status === 409)) {
        clearPendingPhotoCuration(instanceId, view.relation_id);
        setPending(null);
        setMessage(displayError(reason));
        setMessageKind("error");
      } else {
        setMessage(`${displayError(reason)}。结果尚未确认，刷新后使用原操作键重试。`);
        setMessageKind("warning");
        setRefreshKey((value) => value + 1);
      }
    } finally {
      setSaving(false);
    }
  }

  const pendingVersionChanged = Boolean(
    pending && view && pending.expectedVersion !== view.version,
  );
  const changed = Boolean(
    view &&
      (selected.join(",") !== view.ordered_block_ids.join(",") ||
        coverBlockID !== (view.cover_block_id || "")),
  );
  const visiblePhotos =
    view?.original_photos
      .filter((photo) => selected.includes(photo.block_id))
      .sort((left, right) => selected.indexOf(left.block_id) - selected.indexOf(right.block_id)) ||
    [];
  const hiddenPhotos =
    view?.original_photos.filter((photo) => !selected.includes(photo.block_id)) || [];

  return (
    <div className="page-content detail-page">
      <Link className="back-link" href={`/activities/${instanceId}`}>
        <ArrowLeft /> 返回活动详情
      </Link>
      <section className="panel">
        <div className="panel-head">
          <div>
            <h2>往期照片整理</h2>
            <p>
              {instance?.instance.title || "正在读取期次…"} ·
              整期及场次已发布照片可隐藏、恢复和调整公开顺序，原始内容与审核记录保持不变。
            </p>
          </div>
          <button
            className="secondary-button"
            type="button"
            disabled={loading || saving}
            onClick={() => setRefreshKey((value) => value + 1)}
          >
            <RefreshCw /> 刷新
          </button>
        </div>
        {loading ? (
          <div className="inline-message table-message">正在读取公开回顾…</div>
        ) : error ? (
          <div className="inline-message error table-message">{error}</div>
        ) : documents.length === 0 ? (
          <div className="inline-message table-message">
            这期尚无公开回顾；请先在活动详情添加本期回顾。
          </div>
        ) : (
          <>
            <div className="filter-row">
              <label>
                回顾资料{" "}
                <select
                  value={relationId}
                  onChange={(event) => {
                    setRelationId(event.target.value);
                    setMessage("");
                  }}
                >
                  {documents.map((document, index) => (
                    <option key={document.relation_id} value={document.relation_id}>
                      {document.session_id ? "场次" : "整期"} ·{" "}
                      {document.title || `回顾资料 ${index + 1}`}
                    </option>
                  ))}
                </select>
              </label>
            </div>
            {viewLoading ? (
              <div className="inline-message table-message">正在读取原始照片与修订版本…</div>
            ) : (
              view && (
                <div className="photo-curation-editor">
                  <p>
                    当前修订版本 {view.version}
                    。仅可整理这份已审核回顾中的照片；新增照片请在活动详情创建新的回顾资料。
                  </p>
                  {pending && (
                    <div className="inline-message warning">
                      上次保存结果未确认。
                      {pendingVersionChanged
                        ? "服务端版本已变化，请先核对公开页面，再清除本地待核对操作。"
                        : "请刷新后用原操作键重试。"}
                      <button
                        className="secondary-button"
                        type="button"
                        disabled={saving}
                        onClick={() => {
                          if (
                            !window.confirm(
                              "已核对公开回顾，确认清除本地待核对操作？此操作不改变服务端内容。",
                            )
                          )
                            return;
                          clearPendingPhotoCuration(instanceId, relationId);
                          setPending(null);
                          setSelected(view.ordered_block_ids);
                          setCoverBlockID(view.cover_block_id || "");
                          setRefreshKey((value) => value + 1);
                        }}
                      >
                        已核对，清除操作键
                      </button>
                    </div>
                  )}
                  {message && <div className={`inline-message ${messageKind}`}>{message}</div>}
                  {view.original_photos.length === 0 ? (
                    <div className="inline-message">这份资料没有可整理的照片。</div>
                  ) : (
                    <>
                      <h3>公开照片（{visiblePhotos.length}）</h3>
                      <p>
                        封面可独立选择，不改变照片展示顺序。未指定时使用第一张可展示的照片；隐藏封面后恢复自动选择。
                      </p>
                      <button
                        className="secondary-button"
                        type="button"
                        disabled={Boolean(pending) || saving || !coverBlockID}
                        onClick={() => setCoverBlockID("")}
                      >
                        {coverBlockID ? "使用自动封面" : "当前使用自动封面"}
                      </button>
                      <div className="photo-curation-list">
                        {visiblePhotos.map((photo, index) => (
                          <div className="photo-curation-row" key={photo.block_id}>
                            <span className="photo-curation-index">{index + 1}</span>
                            <div
                              className="photo-curation-thumb"
                              role="img"
                              aria-label={`第 ${index + 1} 张活动照片`}
                              style={
                                photo.url
                                  ? { backgroundImage: `url(${JSON.stringify(photo.url)})` }
                                  : undefined
                              }
                            />
                            <div className="photo-curation-label">
                              {photo.url ? (
                                <a href={photo.url} target="_blank" rel="noreferrer">
                                  查看原图
                                </a>
                              ) : (
                                `文件照片 ${photo.file_id || photo.block_id}`
                              )}
                            </div>
                            <button
                              className="secondary-button"
                              type="button"
                              aria-pressed={coverBlockID === photo.block_id}
                              disabled={Boolean(pending) || saving}
                              onClick={() => setCoverBlockID(photo.block_id)}
                            >
                              {coverBlockID === photo.block_id ? "当前封面" : "设为封面"}
                            </button>
                            <button
                              className="secondary-button"
                              type="button"
                              disabled={Boolean(pending) || saving || index === 0}
                              onClick={() =>
                                setSelected((current) => move(current, photo.block_id, -1))
                              }
                            >
                              上移
                            </button>
                            <button
                              className="secondary-button"
                              type="button"
                              disabled={
                                Boolean(pending) || saving || index === visiblePhotos.length - 1
                              }
                              onClick={() =>
                                setSelected((current) => move(current, photo.block_id, 1))
                              }
                            >
                              下移
                            </button>
                            <button
                              className="secondary-button danger-button"
                              type="button"
                              disabled={Boolean(pending) || saving}
                              onClick={() => toggle(photo.block_id)}
                            >
                              隐藏
                            </button>
                          </div>
                        ))}
                      </div>
                      {hiddenPhotos.length > 0 && (
                        <>
                          <h3>已隐藏（{hiddenPhotos.length}）</h3>
                          <div className="photo-curation-list">
                            {hiddenPhotos.map((photo) => (
                              <div className="photo-curation-row" key={photo.block_id}>
                                <div
                                  className="photo-curation-thumb"
                                  role="img"
                                  aria-label="已隐藏活动照片"
                                  style={
                                    photo.url
                                      ? { backgroundImage: `url(${JSON.stringify(photo.url)})` }
                                      : undefined
                                  }
                                />
                                <div className="photo-curation-label">
                                  {photo.url ? (
                                    <a href={photo.url} target="_blank" rel="noreferrer">
                                      查看原图
                                    </a>
                                  ) : (
                                    `文件照片 ${photo.file_id || photo.block_id}`
                                  )}
                                </div>
                                <button
                                  className="secondary-button"
                                  type="button"
                                  disabled={Boolean(pending) || saving}
                                  onClick={() => toggle(photo.block_id)}
                                >
                                  恢复公开
                                </button>
                              </div>
                            ))}
                          </div>
                        </>
                      )}
                      <button
                        className="primary-button"
                        type="button"
                        disabled={saving || pendingVersionChanged || (!changed && !pending)}
                        onClick={() => void save()}
                      >
                        {saving ? "正在保存…" : pending ? "用原操作键重试" : "保存照片与封面"}
                      </button>
                    </>
                  )}
                </div>
              )
            )}
          </>
        )}
      </section>
    </div>
  );
}
