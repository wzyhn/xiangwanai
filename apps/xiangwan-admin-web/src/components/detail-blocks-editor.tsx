"use client";

import { ArrowDown, ArrowUp, ImagePlus, Info, Trash2, Type } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import type { CSSProperties } from "react";
import { ApiError } from "@/lib/api";
import { activityImageAccept, resolveActivityImageUrl, uploadActivityImage, validateActivityImage } from "@/lib/images";
import type { InstanceDetailBlock } from "@/lib/types";

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

export function isDetailBlock(value: unknown): value is InstanceDetailBlock {
  if (!isRecord(value)) return false;
  if (value.type === "text") {
    return typeof value.title === "string" && typeof value.body === "string";
  }
  if (value.type === "image") {
    return typeof value.url === "string" &&
      (value.caption === undefined || typeof value.caption === "string");
  }
  return false;
}

export function isDetailBlockList(value: unknown): value is InstanceDetailBlock[] {
  return Array.isArray(value) && value.every(isDetailBlock);
}

// 长度上限与服务端按 Unicode 码点(rune)计数保持一致;输入框不用原生
// maxLength,它按 UTF-16 码元计数(一个 emoji 计 2),同一字符串两种口径
// 结果不同(codex review 2026-09-20)。超限由 validateDetailBlocks 拦截。
export const detailBlockTitleMaxRunes = 120;
export const detailBlockBodyMaxRunes = 4000;
export const detailBlockCaptionMaxRunes = 200;
// 与服务端 MaxInstanceDetailBlocks(internal/domains/xiangwan/activity)保持一致。
export const detailBlockMaxCount = 20;

export function validateDetailBlocks(blocks: InstanceDetailBlock[]): string {
  if (blocks.length > detailBlockMaxCount) {
    return `图文详情最多 ${detailBlockMaxCount} 个内容块，请删除多余的内容块`;
  }
  for (let index = 0; index < blocks.length; index += 1) {
    const block = blocks[index];
    if (block.type === "text") {
      if (!block.title.trim() || !block.body.trim()) {
        return `第 ${index + 1} 个图文详情文字块需要填写标题和正文，不需要的块可以删除`;
      }
      // 服务端按 rune 计数限制长度,这里用相同口径校验([...str].length)。
      if ([...block.title].length > detailBlockTitleMaxRunes) {
        return `第 ${index + 1} 个图文详情文字块标题最长 ${detailBlockTitleMaxRunes} 字`;
      }
      if ([...block.body].length > detailBlockBodyMaxRunes) {
        return `第 ${index + 1} 个图文详情文字块正文最长 ${detailBlockBodyMaxRunes} 字`;
      }
    } else {
      if (!block.url.trim()) {
        return `第 ${index + 1} 个图文详情图片块尚未上传图片`;
      }
      if ([...(block.caption || "")].length > detailBlockCaptionMaxRunes) {
        return `第 ${index + 1} 个图文详情图片块说明文字最长 ${detailBlockCaptionMaxRunes} 字`;
      }
    }
  }
  return "";
}

export function serializeDetailBlocks(blocks: InstanceDetailBlock[]): InstanceDetailBlock[] {
  return blocks.map((block) => {
    if (block.type === "text") {
      return { type: "text", title: block.title.trim(), body: block.body.trim() };
    }
    const caption = (block.caption || "").trim();
    return caption
      ? { type: "image", url: block.url.trim(), caption }
      : { type: "image", url: block.url.trim() };
  });
}

const blockItemStyle: CSSProperties = {
  border: "1px solid var(--line)",
  borderRadius: 8,
  padding: 12,
  marginTop: 10,
  display: "grid",
  gap: 10,
};

const blockHeadStyle: CSSProperties = {
  display: "flex",
  alignItems: "center",
  gap: 8,
  flexWrap: "wrap",
};

const blockButtonStyle: CSSProperties = {
  minHeight: 0,
  padding: "6px 10px",
};

export function DetailBlocksEditor({
  blocks,
  onChange,
  disabled,
  onError,
}: {
  blocks: InstanceDetailBlock[];
  onChange: (blocks: InstanceDetailBlock[]) => void;
  disabled: boolean;
  onError: (message: string) => void;
}) {
  const [uploadingIndex, setUploadingIndex] = useState<number | null>(null);
  const reachedMaxBlocks = blocks.length >= detailBlockMaxCount;
  // 上传是异步的:continuation 触发时必须基于最新 blocks(用户在途编辑过
  // 标题/正文/说明),否则旧的闭包快照会回滚这些编辑(codex review 2026-09-20)。
  const blocksRef = useRef(blocks);
  useEffect(() => {
    blocksRef.current = blocks;
  }, [blocks]);

  function updateBlock(index: number, block: InstanceDetailBlock) {
    onChange(blocksRef.current.map((item, position) => (position === index ? block : item)));
  }

  function moveBlock(index: number, offset: -1 | 1) {
    const target = index + offset;
    const latest = blocksRef.current;
    if (target < 0 || target >= latest.length) return;
    const next = [...latest];
    [next[index], next[target]] = [next[target], next[index]];
    onChange(next);
  }

  async function uploadBlockImage(index: number, file: File, input: HTMLInputElement) {
    const validationError = validateActivityImage(file);
    if (validationError) {
      onError(validationError);
      input.value = "";
      return;
    }
    setUploadingIndex(index);
    onError("");
    try {
      const url = await uploadActivityImage(file);
      const block = blocksRef.current[index];
      if (block?.type === "image") updateBlock(index, { ...block, url });
    } catch (reason) {
      onError(reason instanceof ApiError
        ? "图片上传失败，请确认格式与大小后重试。"
        : "图片上传失败，请稍后重试。");
    } finally {
      setUploadingIndex(null);
      input.value = "";
    }
  }

  return (
    <div>
      {blocks.length === 0 && (
        <p className="brand-hero-hint"><Info /> 尚未添加图文详情块，可按需添加文字或图片块，保存后按顺序展示在活动详情中。</p>
      )}
      {blocks.map((block, index) => (
        <div style={blockItemStyle} key={index}>
          <div style={blockHeadStyle}>
            <select
              value={block.type}
              disabled={disabled || uploadingIndex !== null}
              aria-label={`第 ${index + 1} 块类型`}
              style={{ padding: "6px 10px", border: "1px solid var(--line)", borderRadius: 5, fontSize: 12, color: "var(--navy)", background: "#fff" }}
              onChange={(event) => {
                updateBlock(index, event.target.value === "image"
                  ? { type: "image", url: "", caption: "" }
                  : { type: "text", title: "", body: "" });
              }}
            >
              <option value="text">文字块</option>
              <option value="image">图片块</option>
            </select>
            <span className="brand-hero-hint" style={{ margin: 0, flex: 1 }}>第 {index + 1} 块</span>
            <button type="button" className="secondary-button" style={blockButtonStyle} disabled={disabled || uploadingIndex !== null || index === 0} onClick={() => moveBlock(index, -1)}><ArrowUp /> 上移</button>
            <button type="button" className="secondary-button" style={blockButtonStyle} disabled={disabled || uploadingIndex !== null || index === blocks.length - 1} onClick={() => moveBlock(index, 1)}><ArrowDown /> 下移</button>
            <button type="button" className="secondary-button" style={blockButtonStyle} disabled={disabled || uploadingIndex !== null} onClick={() => onChange(blocks.filter((_, position) => position !== index))}><Trash2 /> 删除</button>
          </div>
          {block.type === "text" ? (
            <div className="field-grid">
              <label className="full">标题 <span className="required-mark" aria-hidden="true">*</span>
                <input
                  value={block.title}
                  disabled={disabled}
                  aria-invalid={[...block.title].length > detailBlockTitleMaxRunes || undefined}
                  onChange={(event) => updateBlock(index, { ...block, title: event.target.value })}
                  placeholder="例如：本期话题" required={true} aria-required={true}
                />
                <span className="field-count" data-over={[...block.title].length > detailBlockTitleMaxRunes} aria-live="polite">
                  {[...block.title].length}/{detailBlockTitleMaxRunes}
                </span>
              </label>
              <label className="full">正文 <span className="required-mark" aria-hidden="true">*</span>
                <textarea
                  rows={4}
                  value={block.body}
                  disabled={disabled}
                  aria-invalid={[...block.body].length > detailBlockBodyMaxRunes || undefined}
                  onChange={(event) => updateBlock(index, { ...block, body: event.target.value })}
                  placeholder="支持多行文本" required={true} aria-required={true}
                />
                <span className="field-count" data-over={[...block.body].length > detailBlockBodyMaxRunes} aria-live="polite">
                  {[...block.body].length}/{detailBlockBodyMaxRunes}
                </span>
              </label>
            </div>
          ) : (
            <div className="field-grid">
              <label className="full">图片（JPG / PNG / WebP，最大 5 MiB） {!block.url && <span className="required-mark" aria-hidden="true">*</span>}
                <input
                  type="file"
                  accept={activityImageAccept.join(",")}
                  disabled={disabled || uploadingIndex !== null}
                  onChange={(event) => {
                    const file = event.target.files?.[0];
                    if (file) void uploadBlockImage(index, file, event.target);
                  }} required={!block.url} aria-required={!block.url}
                />
              </label>
              {block.url && (
                <div className="cover-preview-row">
                  <div className="cover-preview-thumb" role="img" aria-label={`第 ${index + 1} 块图片预览`} style={{ backgroundImage: `url(${JSON.stringify(resolveActivityImageUrl(block.url))})` }} />
                  <p className="brand-hero-hint"><ImagePlus /> {uploadingIndex === index ? "正在上传…" : "已上传图片，选择新文件可替换。"}</p>
                </div>
              )}
              <label className="full">说明文字（可选）
                <input
                  value={block.caption || ""}
                  disabled={disabled}
                  aria-invalid={[...(block.caption || "")].length > detailBlockCaptionMaxRunes || undefined}
                  onChange={(event) => updateBlock(index, { ...block, caption: event.target.value })}
                  placeholder="图片下方展示的说明"
                />
                <span className="field-count" data-over={[...(block.caption || "")].length > detailBlockCaptionMaxRunes} aria-live="polite">
                  {[...(block.caption || "")].length}/{detailBlockCaptionMaxRunes}
                </span>
              </label>
            </div>
          )}
        </div>
      ))}
      <div style={{ display: "flex", gap: 10, marginTop: 10, flexWrap: "wrap", alignItems: "center" }}>
        <button type="button" className="secondary-button" disabled={disabled || reachedMaxBlocks} onClick={() => onChange([...blocks, { type: "text", title: "", body: "" }])}><Type /> 添加文字块</button>
        <button type="button" className="secondary-button" disabled={disabled || reachedMaxBlocks} onClick={() => onChange([...blocks, { type: "image", url: "", caption: "" }])}><ImagePlus /> 添加图片块</button>
        {reachedMaxBlocks && <span className="brand-hero-hint" style={{ margin: 0 }}><Info /> 最多 {detailBlockMaxCount} 个内容块，如需新增请先删除现有块。</span>}
      </div>
    </div>
  );
}
