"use client";

import { CheckCircle2, ImagePlus, Plus, Save, Trash2, Type } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import {
  ApiError, api, apiUpload, definitiveFailure, displayError, operationHeaders, operationKey,
} from "@/lib/api";
import type { BrandProfile, BrandQuickTag } from "@/lib/types";

const API_ORIGIN = process.env.NEXT_PUBLIC_API_ORIGIN || "";
const heroImageAccept = ["image/jpeg", "image/png", "image/webp"];
const heroImageMaxBytes = 5 * 1024 * 1024;

function resolveBrandHeroReference(path: string): string {
  const relative = String(path || "").trim();
  if (!relative) return "";
  // The checked-in dev admin runs on HTTPS while the local API is HTTP.
  // Keep the persisted value HTTPS and same-origin in that one case; Next's
  // rewrite serves the public media route without mixed-content blocking.
  if (
    typeof window !== "undefined" &&
    window.location.protocol === "https:" &&
    API_ORIGIN.startsWith("http://")
  ) {
    return new URL(relative, window.location.origin).toString();
  }
  return `${API_ORIGIN.replace(/\/+$/, "")}${relative}`;
}

type BrandForm = Pick<
  BrandProfile,
  | "community_name"
  | "brand_intro"
  | "hero_mode"
  | "hero_eyebrow"
  | "hero_subtitle"
  | "hero_image_url"
  | "hero_image_alt"
  | "quick_tags"
>;

type PendingBrandWrite = { key: string; body: string };

const pendingStorageKey = "xiangwan.admin.brand.pending.v1";
const operationKeyPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const emptyForm: BrandForm = {
  community_name: "",
  brand_intro: "",
  hero_mode: "text",
  hero_eyebrow: "",
  hero_subtitle: "",
  hero_image_url: "",
  hero_image_alt: "",
  quick_tags: [],
};

function profileToForm(profile: BrandProfile): BrandForm {
  return {
    community_name: profile.community_name || "",
    brand_intro: profile.brand_intro || "",
    hero_mode: profile.hero_mode || "text",
    hero_eyebrow: profile.hero_eyebrow || "",
    hero_subtitle: profile.hero_subtitle || "",
    hero_image_url: profile.hero_image_url || "",
    hero_image_alt: profile.hero_image_alt || "",
    quick_tags: Array.isArray(profile.quick_tags) ? profile.quick_tags : [],
  };
}

function readPendingBrandWrite(): PendingBrandWrite | null {
  try {
    const value: unknown = JSON.parse(sessionStorage.getItem(pendingStorageKey) || "null");
    if (typeof value !== "object" || value === null ||
      !("key" in value) || typeof value.key !== "string" || !operationKeyPattern.test(value.key) ||
      !("body" in value) || typeof value.body !== "string") {
      sessionStorage.removeItem(pendingStorageKey);
      return null;
    }
    return { key: value.key, body: value.body };
  } catch {
    sessionStorage.removeItem(pendingStorageKey);
    return null;
  }
}

function unicodeLength(value: string): number {
  return Array.from(value).length;
}

function validate(form: BrandForm): string {
  if (unicodeLength(form.community_name.trim()) > 100) return "社区名称不能超过 100 个字符";
  if (unicodeLength(form.brand_intro.trim()) > 2000) return "品牌介绍不能超过 2000 个字符";
  if (unicodeLength(form.hero_eyebrow.trim()) > 100) return "眉题不能超过 100 个字符";
  if (unicodeLength(form.hero_subtitle.trim()) > 200) return "副标题不能超过 200 个字符";
  if (form.hero_mode === "image" && form.hero_image_url.trim()) {
    try {
      const url = new URL(form.hero_image_url);
      if (url.protocol !== "https:" || url.username || url.password || url.hash) throw new Error();
    } catch {
      return "图片地址必须是完整、安全的 HTTPS 地址";
    }
  }
  if (unicodeLength(form.hero_image_alt.trim()) > 200) return "图片说明不能超过 200 个字符";
  if (form.quick_tags.length > 20) return "热门主题最多 20 个";
  const seen = new Set<string>();
  for (const rawTag of form.quick_tags) {
    const tag = { code: rawTag.code.trim(), label: rawTag.label.trim() };
    if (!tag.code.trim() && !tag.label.trim()) continue;
    if (!/^[a-z][a-z0-9_]{0,31}$/.test(tag.code)) return "主题编码需以小写字母开头，仅含小写字母、数字和下划线";
    if (!tag.label.trim() || unicodeLength(tag.label.trim()) > 40) return "主题名称需为 1–40 个字符";
    if (seen.has(tag.code)) return "主题编码不能重复";
    seen.add(tag.code);
  }
  return "";
}

export default function BrandPage() {
  const [profile, setProfile] = useState<BrandProfile | null>(null);
  const [form, setForm] = useState<BrandForm>(emptyForm);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [pendingWrite, setPendingWrite] = useState<PendingBrandWrite | null>(null);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [uploadingHero, setUploadingHero] = useState(false);
  const heroFileInput = useRef<HTMLInputElement | null>(null);

  useEffect(() => {
    let active = true;
    async function load() {
      const pending = readPendingBrandWrite();
      if (pending) {
        try {
          const value = await api<BrandProfile>("/brand-profile", {
            method: "PATCH",
            headers: operationHeaders(pending.key),
            body: pending.body,
          });
          if (!active) return;
          sessionStorage.removeItem(pendingStorageKey);
          setProfile(value);
          setForm(profileToForm(value));
          setMessage(`已确认上次请求，首页品牌区为第 ${value.publication_version} 版`);
          setLoading(false);
          return;
        } catch (reason) {
          if (!active) return;
          if (!definitiveFailure(reason)) {
            setPendingWrite(pending);
            setError("上次发布请求未成功且结果未知。可重试原请求，或点「放弃上次发布」改用当前表单。");
            setLoading(false);
            return;
          }
          sessionStorage.removeItem(pendingStorageKey);
          setError(displayError(reason));
        }
      }
      try {
        const value = await api<BrandProfile>("/brand-profile");
        if (!active) return;
        setProfile(value);
        setForm(profileToForm(value));
      } catch (reason) {
        if (active) setError(displayError(reason));
      } finally {
        if (active) setLoading(false);
      }
    }
    void load();
    return () => { active = false; };
  }, []);

  const previewImage = useMemo(
    () => form.hero_mode === "image" && form.hero_image_url.startsWith("https://"),
    [form.hero_image_url, form.hero_mode],
  );

  function update<K extends keyof BrandForm>(key: K, value: BrandForm[K]) {
    setForm((current) => ({ ...current, [key]: value }));
    setMessage("");
  }

  // 横幅图片通过真正的文件上传进入服务端存储;运营不再手填外链。
  // 服务端只接受 JPEG/PNG/WebP(魔数校验)、5 MiB 以内,返回相对路径,
  // 这里拼成品牌档案要求的绝对 HTTPS URL。
  async function uploadHeroImage(file: File) {
    if (!heroImageAccept.includes(file.type)) {
      setError("横幅图片仅支持 JPG、PNG 或 WebP 格式。");
      return;
    }
    if (file.size > heroImageMaxBytes) {
      setError("横幅图片不能超过 5 MiB。");
      return;
    }
    setUploadingHero(true);
    setError("");
    try {
      const payload = new FormData();
      payload.append("image", file);
      const result = await apiUpload<{ url: string }>("/brand-hero-images", payload);
      update("hero_image_url", resolveBrandHeroReference(result.url));
      setMessage("横幅图片已上传，发布后生效。");
    } catch (reason) {
      setError(reason instanceof ApiError
        ? "横幅图片上传失败，请确认格式与大小后重试。"
        : "横幅图片上传失败，请稍后重试。");
    } finally {
      setUploadingHero(false);
      if (heroFileInput.current) heroFileInput.current.value = "";
    }
  }

  function updateTag(index: number, field: keyof BrandQuickTag, value: string) {
    update("quick_tags", form.quick_tags.map((tag, itemIndex) =>
      itemIndex === index ? { ...tag, [field]: value } : tag,
    ));
  }

  // 放弃一条「结果未知」的旧发布请求:重放失败(如校验被服务端拒绝但按非终态
  // 处理)时,冻结的 payload 会让表单永久锁死在必败重试上(2026-09-19 生产
  // 死锁)。放弃后用当前表单重新发布会生成新的幂等键,不会与旧请求冲突。
  // 旧请求若实际已提交,放弃后立刻对账最新版本,避免下次发布带着陈旧的
  // expected_version 撞 409(codex review 2026-09-19)。
  async function discardPendingWrite() {
    try {
      sessionStorage.removeItem(pendingStorageKey);
    } catch {
      // Storage may be unavailable in hardened browser modes.
    }
    setPendingWrite(null);
    setError("");
    try {
      const current = await api<BrandProfile>("/brand-profile");
      setProfile(current);
      setMessage("已放弃上次未确认的发布请求，最新版本已同步，请检查内容后重新发布。");
    } catch {
      setMessage("已放弃上次未确认的发布请求。暂无法读取最新版本，发布时会重新校验。");
    }
  }

  async function publish() {
    let pending = pendingWrite || readPendingBrandWrite();
    if (!pending) {
      const validation = validate(form);
      if (validation) {
        setError(validation);
        return;
      }
      const payload = {
        expected_version: profile?.version || 0,
        community_name: form.community_name.trim(),
        brand_intro: form.brand_intro.trim(),
        hero_mode: form.hero_mode,
        hero_eyebrow: form.hero_eyebrow.trim(),
        hero_subtitle: form.hero_subtitle.trim(),
        hero_image_url: form.hero_mode === "image" ? form.hero_image_url.trim() : "",
        hero_image_alt: form.hero_mode === "image" ? form.hero_image_alt.trim() : "",
        quick_tags: form.quick_tags.filter((tag) => tag.code.trim() || tag.label.trim()).map((tag) => ({ code: tag.code.trim(), label: tag.label.trim() })),
      };
      pending = { key: operationKey(), body: JSON.stringify(payload) };
      sessionStorage.setItem(pendingStorageKey, JSON.stringify(pending));
      setPendingWrite(pending);
    }
    setSaving(true);
    setError("");
    setMessage("");
    try {
      const value = await api<BrandProfile>("/brand-profile", {
        method: "PATCH",
        headers: operationHeaders(pending.key),
        body: pending.body,
      });
      sessionStorage.removeItem(pendingStorageKey);
      setPendingWrite(null);
      setProfile(value);
      setForm(profileToForm(value));
      setMessage(`首页品牌区已发布为第 ${value.publication_version} 版`);
    } catch (reason) {
      if (definitiveFailure(reason)) {
        sessionStorage.removeItem(pendingStorageKey);
        setPendingWrite(null);
        setError(displayError(reason));
      } else if (pendingWrite) {
        setError("发布未能完成，结果未知。可重试原请求，或点「放弃上次发布」改用当前表单。");
      } else {
        setError(displayError(reason));
      }
    } finally {
      setSaving(false);
    }
  }

  if (loading) return <section className="page-content"><div className="state-card inline">正在读取首页配置…</div></section>;

  return (
    <section className="brand-editor-page">
      <div className="brand-editor-form">
        {error && <div className="inline-message error">{error}</div>}
        {message && <div className="inline-message success"><CheckCircle2 /> {message}</div>}

        <fieldset className="editor-fieldset" disabled={saving || pendingWrite !== null}>
          <section className="form-section">
            <div className="form-title"><span>01</span><div><h2>品牌展示方式</h2><p>图片模式显示横幅及已填写文案。所有内容均可留空，留空的内容不显示。</p></div></div>
            <div className="segmented">
              <button type="button" className={form.hero_mode === "text" ? "active" : ""} onClick={() => update("hero_mode", "text")}><Type /> 文字</button>
              <button type="button" className={form.hero_mode === "image" ? "active" : ""} onClick={() => update("hero_mode", "image")}><ImagePlus /> 图片</button>
            </div>
          </section>

          <section className="form-section">
            <div className="form-title"><span>02</span><div><h2>品牌内容</h2><p>填写的内容显示在小程序首页；未配置图片或图片加载失败时仍显示已填写文案。</p></div></div>
            <div className="field-grid">
              <label>社区名称<input maxLength={100} value={form.community_name} onChange={(event) => update("community_name", event.target.value)} /></label>
              <label>眉题<input maxLength={100} value={form.hero_eyebrow} onChange={(event) => update("hero_eyebrow", event.target.value)} /></label>
              <label className="full">副标题<input maxLength={200} value={form.hero_subtitle} onChange={(event) => update("hero_subtitle", event.target.value)} /></label>
              <label className="full">品牌介绍<textarea maxLength={2000} rows={4} value={form.brand_intro} onChange={(event) => update("brand_intro", event.target.value)} /></label>
              {form.hero_mode === "image" && <>
                <div className="full">
                  <label>横幅图片(JPG / PNG / WebP,最大 5 MiB)
                    <input
                      ref={heroFileInput}
                      type="file"
                      accept={heroImageAccept.join(",")}
                      disabled={uploadingHero || saving}
                      onChange={(event) => {
                        const file = event.target.files?.[0];
                        if (file) void uploadHeroImage(file);
                      }}
                    />
                  </label>
                  {form.hero_image_url && <p className="brand-hero-hint"><ImagePlus /> 已上传横幅，选择新文件可替换。发布前可在右侧预览确认。</p>}
                  {form.hero_image_url && <button type="button" className="secondary-button" onClick={() => { update("hero_image_url", ""); update("hero_image_alt", ""); }}>移除横幅图片</button>}
                </div>
                <label className="full">图片说明<input maxLength={200} placeholder="供无障碍阅读和图片异常时识别" value={form.hero_image_alt} onChange={(event) => update("hero_image_alt", event.target.value)} /></label>
              </>}
            </div>
          </section>

          <section className="form-section">
            <div className="form-title brand-tag-title"><span>03</span><div><h2>热门主题</h2><p>用于小程序首页快速筛选，编码发布后会被活动期次引用。</p></div><button type="button" className="secondary-button" disabled={form.quick_tags.length >= 20} onClick={() => update("quick_tags", [...form.quick_tags, { code: "", label: "" }])}><Plus /> 添加主题</button></div>
            <div className="brand-tag-list">
              {form.quick_tags.length === 0 && <p className="brand-empty-tags">暂未配置热门主题</p>}
              {form.quick_tags.map((tag, index) => <div className="brand-tag-row" key={index}>
                <label>编码 {(tag.code.trim() || tag.label.trim()) && <span className="required-mark" aria-hidden="true">*</span>}<input aria-label={`主题 ${index + 1} 编码`} required={Boolean(tag.code.trim() || tag.label.trim())} aria-required={Boolean(tag.code.trim() || tag.label.trim())} maxLength={32} placeholder="ai_roundtable" value={tag.code} onChange={(event) => updateTag(index, "code", event.target.value)} /></label>
                <label>名称 {(tag.code.trim() || tag.label.trim()) && <span className="required-mark" aria-hidden="true">*</span>}<input aria-label={`主题 ${index + 1} 名称`} required={Boolean(tag.code.trim() || tag.label.trim())} aria-required={Boolean(tag.code.trim() || tag.label.trim())} maxLength={40} placeholder="AI 圆桌派" value={tag.label} onChange={(event) => updateTag(index, "label", event.target.value)} /></label>
                <button type="button" aria-label={`删除主题 ${index + 1}`} onClick={() => update("quick_tags", form.quick_tags.filter((_, itemIndex) => itemIndex !== index))}><Trash2 /></button>
              </div>)}
            </div>
          </section>
        </fieldset>
      </div>

      <aside className="brand-preview-card">
        <div className="preview-head"><b>小程序预览</b><span>{profile?.configured ? `已发布 v${profile.publication_version}` : "尚未发布"}</span></div>
        <div className="brand-phone-preview">
          <div className={`brand-preview-hero${previewImage ? " brand-preview-hero-image" : ""}`}>
          {previewImage && <div className="brand-preview-image" role="img" aria-label={form.hero_image_alt || "品牌横幅预览"} style={{ backgroundImage: `url(${JSON.stringify(form.hero_image_url)})` }} />}
          {(form.hero_eyebrow || form.community_name || form.hero_subtitle || form.brand_intro) && <div className="brand-preview-copy">
            {form.hero_eyebrow && <small>{form.hero_eyebrow}</small>}
            {form.community_name && <h3>{form.community_name}</h3>}
            {form.hero_subtitle && <b>{form.hero_subtitle}</b>}
            {form.brand_intro && <p>{form.brand_intro}</p>}
          </div>}
          </div>
          {!previewImage && !form.hero_eyebrow && !form.community_name && !form.hero_subtitle && !form.brand_intro && <p className="brand-empty-preview">未配置首页品牌内容</p>}
        </div>
        <p>保存即生成新的不可变发布版本；小程序只读取当前已发布版本。</p>
      </aside>

      <div className="editor-actions">
        <span>{profile?.updated_at ? `上次发布：${new Date(profile.updated_at).toLocaleString("zh-CN")}` : "首次发布"}</span>
        {pendingWrite && !saving && <button type="button" className="secondary-button" onClick={discardPendingWrite}>放弃上次发布</button>}
        <button className="primary-button" disabled={saving || uploadingHero} onClick={publish}><Save /> {saving ? "正在发布…" : uploadingHero ? "正在上传横幅…" : pendingWrite ? "重试上次发布" : "发布首页配置"}</button>
      </div>
    </section>
  );
}
