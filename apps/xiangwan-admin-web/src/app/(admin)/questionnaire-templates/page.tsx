"use client";

import { Copy, Edit3, Plus, RefreshCw, Save, Trash2 } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { allPages, api, displayError, operationHeaders, operationKey } from "@/lib/api";
import { buildTemplateFields, isTemplateChoiceFieldType } from "@/lib/questionnaire-template";
import type { InstanceListItem, QuestionnaireFieldType, QuestionnaireTemplate } from "@/lib/types";

type FieldDraft = {
  localId: string;
  code: string;
  type: QuestionnaireFieldType;
  label: string;
  helpText: string;
  required: boolean;
  minLength: string;
  maxLength: string;
  maxSelections: string;
  options: string;
};

type FormState = {
  id: string;
  expectedVersion: number;
  name: string;
  description: string;
  privacyPurpose: string;
  privacyPolicyVersion: string;
  fields: FieldDraft[];
};

const fieldLabels: Record<QuestionnaireFieldType, string> = {
  single_choice: "单选",
  multiple_choice: "多选",
  single_line: "单行文本",
  multiline: "多行文本",
  area: "地区",
};

function newField(index: number): FieldDraft {
  return {
    localId: crypto.randomUUID(), code: `field_${index + 1}`, type: "single_line",
    label: "", helpText: "", required: false, minLength: "", maxLength: "200",
    maxSelections: "", options: "",
  };
}

function blankForm(): FormState {
  return {
    id: "", expectedVersion: 0, name: "", description: "",
    privacyPurpose: "用于本次活动报名与现场组织", privacyPolicyVersion: "",
    fields: [newField(0)],
  };
}

function draftFromTemplate(template: QuestionnaireTemplate): FormState {
  return {
    id: template.id, expectedVersion: template.version, name: template.name,
    description: template.description, privacyPurpose: template.privacy_purpose,
    privacyPolicyVersion: template.privacy_policy_version,
    fields: template.fields.map((field) => ({
      localId: crypto.randomUUID(), code: field.code, type: field.type,
      label: field.label, helpText: field.help_text, required: field.required,
      minLength: field.min_length?.toString() || "",
      maxLength: field.max_length?.toString() || "",
      maxSelections: field.max_selections?.toString() || "",
      options: field.options.map((option) => `${option.code}=${option.label}`).join("\n"),
    })),
  };
}

export default function QuestionnaireTemplatesPage() {
  const [templates, setTemplates] = useState<QuestionnaireTemplate[]>([]);
  const [instances, setInstances] = useState<InstanceListItem[]>([]);
  const [form, setForm] = useState<FormState>(() => blankForm());
  const [targetInstance, setTargetInstance] = useState("");
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");

  const load = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const [templatePage, instancePage] = await Promise.all([
        allPages<QuestionnaireTemplate>("/questionnaire-templates"),
        allPages<InstanceListItem>("/instances"),
      ]);
      setTemplates(templatePage.items);
      setInstances(instancePage.items);
    } catch (reason) {
      setError(displayError(reason));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { void load(); }, [load]);

  const activeInstanceOptions = useMemo(
    () => instances.filter((item) => ["draft", "pending_publish", "published"].includes(item.instance.status)),
    [instances],
  );

  function updateField(localId: string, patch: Partial<FieldDraft>) {
    setForm((current) => ({
      ...current,
      fields: current.fields.map((field) => field.localId === localId ? { ...field, ...patch } : field),
    }));
  }

  async function saveTemplate() {
    setSaving(true); setError(""); setNotice("");
    try {
      const payload = {
        name: form.name, description: form.description,
        privacy_purpose: form.privacyPurpose,
        privacy_policy_version: form.privacyPolicyVersion,
        fields: buildTemplateFields(form.fields),
      };
      const path = form.id ? `/questionnaire-templates/${form.id}` : "/questionnaire-templates";
      const body = form.id ? { ...payload, expected_version: form.expectedVersion } : payload;
      const saved = await api<QuestionnaireTemplate>(path, {
        method: form.id ? "PATCH" : "POST",
        headers: operationHeaders(operationKey()), body: JSON.stringify(body),
      });
      setForm(draftFromTemplate(saved));
      setNotice(form.id ? "模板已生成新版本，历史期次不受影响。" : "模板已创建，可复制到任意期次。 ");
      await load();
    } catch (reason) {
      setError(displayError(reason));
    } finally {
      setSaving(false);
    }
  }

  async function archiveTemplate(template: QuestionnaireTemplate) {
    if (!window.confirm(`确认归档“${template.name}”？已使用的期次问卷不会改变。`)) return;
    setError(""); setNotice("");
    try {
      await api<QuestionnaireTemplate>(`/questionnaire-templates/${template.id}`, {
        method: "DELETE", headers: operationHeaders(operationKey()),
        body: JSON.stringify({ expected_version: template.version }),
      });
      if (form.id === template.id) setForm(blankForm());
      setNotice("模板已归档。历史报名和已发布问卷保持不变。");
      await load();
    } catch (reason) {
      setError(displayError(reason));
    }
  }

  async function applyTemplate() {
    if (!form.id || !targetInstance) {
      setError("请先选择模板和目标期次");
      return;
    }
    const target = instances.find((item) => item.instance.id === targetInstance);
    if (!target) return;
    setError(""); setNotice("");
    try {
      await api(`/questionnaire-templates/${form.id}/assignments`, {
        method: "POST", headers: operationHeaders(operationKey()),
        body: JSON.stringify({ instance_id: target.instance.id, expected_instance_version: target.instance.version }),
      });
      setNotice(`已将“${form.name}”复制到「${target.instance.title}」，该期已生成独立问卷版本。`);
    } catch (reason) {
      setError(displayError(reason));
    }
  }

  return (
    <div className="editor-page">
      <div className="editor-form">
        <div className="panel-head" style={{ padding: 0, marginBottom: 18 }}>
          <div><h2>报名信息模板</h2><p>模板只用于快速创建；复制到期次后会形成独立快照。</p></div>
          <button className="secondary-button" onClick={() => { setForm(blankForm()); setNotice(""); setError(""); }}><Plus /> 新建模板</button>
        </div>
        {error && <div className="inline-message error">{error}</div>}
        {notice && <div className="inline-message success">{notice}</div>}
        <section className="form-section">
          <div className="form-title"><span>01</span><div><h2>{form.id ? `编辑：${form.name}` : "模板基本信息"}</h2><p>名称和隐私用途会随模板版本保留。</p></div></div>
          <div className="field-grid">
            <label>模板名称 <span className="required-mark">*</span><input value={form.name} onChange={(event) => setForm({ ...form, name: event.target.value })} maxLength={200} required={true} aria-required={true} /></label>
            <label>隐私政策版本 <span className="required-mark">*</span><input value={form.privacyPolicyVersion} onChange={(event) => setForm({ ...form, privacyPolicyVersion: event.target.value })} placeholder="例如 privacy-v1" maxLength={100} required={true} aria-required={true} /></label>
            <label className="full">信息用途说明 <span className="required-mark">*</span><input value={form.privacyPurpose} onChange={(event) => setForm({ ...form, privacyPurpose: event.target.value })} maxLength={500} required={true} aria-required={true} /></label>
            <label className="full">模板说明<textarea rows={2} value={form.description} onChange={(event) => setForm({ ...form, description: event.target.value })} maxLength={500} /></label>
          </div>
        </section>
        <section className="form-section">
          <div className="form-title"><span>02</span><div><h2>报名字段</h2><p>昵称、手机号由小程序平台报名流程自动要求；这里维护活动专属字段。</p></div></div>
          <div style={{ display: "grid", gap: 14 }}>
            {form.fields.map((field, index) => (
              <article key={field.localId} style={{ padding: 14, border: "1px solid var(--line)", borderRadius: 8, background: "#fafbfd" }}>
                <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: 10 }}>
                  <b style={{ color: "var(--navy)" }}>字段 {index + 1}</b>
                  <button className="icon-button" aria-label="删除字段" onClick={() => setForm({ ...form, fields: form.fields.filter((item) => item.localId !== field.localId) })}><Trash2 /></button>
                </div>
                <div className="field-grid">
                  <label>字段标题 <span className="required-mark">*</span><input value={field.label} onChange={(event) => updateField(field.localId, { label: event.target.value })} maxLength={200} required={true} aria-required={true} /></label>
                  <label>字段编码 <span className="required-mark">*</span><input value={field.code} onChange={(event) => updateField(field.localId, { code: event.target.value })} maxLength={64} required={true} aria-required={true} /></label>
                  <label>字段类型 <span className="required-mark" aria-hidden="true">*</span> <select value={field.type} onChange={(event) => {
                    const type = event.target.value as QuestionnaireFieldType;
                    updateField(field.localId, { type, maxLength: type === "single_line" ? "200" : type === "multiline" ? "2000" : "", maxSelections: type === "multiple_choice" ? "1" : "", options: isTemplateChoiceFieldType(type) ? field.options : "" });
                  }} required={true} aria-required={true}>
                    {Object.entries(fieldLabels).map(([value, label]) => <option key={value} value={value}>{label}</option>)}
                  </select></label>
                  <label className="checkbox-field"><input type="checkbox" checked={field.required} onChange={(event) => updateField(field.localId, { required: event.target.checked })} /><span>报名时必填</span></label>
                  {!isTemplateChoiceFieldType(field.type) && <>
                    <label>最少字数<input type="number" min={0} value={field.minLength} onChange={(event) => updateField(field.localId, { minLength: event.target.value })} /></label>
                    <label>最多字数 <span className="required-mark">*</span><input type="number" min={1} value={field.maxLength} onChange={(event) => updateField(field.localId, { maxLength: event.target.value })} required={true} aria-required={true} /></label>
                  </>}
                  {field.type === "multiple_choice" && <label>最多选择项 <span className="required-mark">*</span><input type="number" min={1} value={field.maxSelections} onChange={(event) => updateField(field.localId, { maxSelections: event.target.value })} required={true} aria-required={true} /></label>}
                  <label className="full">说明文案<textarea rows={2} value={field.helpText} onChange={(event) => updateField(field.localId, { helpText: event.target.value })} maxLength={500} /></label>
                  {isTemplateChoiceFieldType(field.type) && <label className="full">选项（每行 `编码=显示名称`） <span className="required-mark">*</span><textarea rows={4} value={field.options} onChange={(event) => updateField(field.localId, { options: event.target.value })} placeholder="beginner=零基础\nadvanced=有经验" required={true} aria-required={true} /></label>}
                </div>
              </article>
            ))}
            <button className="secondary-button" onClick={() => setForm({ ...form, fields: [...form.fields, newField(form.fields.length)] })}><Plus /> 添加报名字段</button>
          </div>
        </section>
        <div className="editor-actions"><button className="primary-button" disabled={saving} onClick={() => void saveTemplate()}><Save /> {saving ? "保存中…" : form.id ? "保存新版本" : "创建模板"}</button></div>
      </div>
      <aside className="editor-side-column">
        <section className="panel" style={{ marginTop: 0, padding: 18 }}>
          <div className="preview-head"><b>复制到活动期次</b><Copy /></div>
          <p className="brand-hero-hint">复制后会生成新的期次问卷版本，模板后续修改不会追改历史。</p>
          <label className="target-select">目标期次 <span className="required-mark" aria-hidden="true">*</span><select value={targetInstance} onChange={(event) => setTargetInstance(event.target.value)} required={true} aria-required={true}><option value="">请选择期次</option>{activeInstanceOptions.map((item) => <option key={item.instance.id} value={item.instance.id}>{item.instance.title} · {item.series_title}</option>)}</select></label>
          <button className="primary-button" style={{ width: "100%", marginTop: 12 }} onClick={() => void applyTemplate()} disabled={!form.id}><Copy /> 复制并启用</button>
        </section>
        <section className="panel" style={{ marginTop: 0 }}>
          <div className="panel-head"><div><h2>模板库</h2><p>{loading ? "加载中…" : `${templates.length} 个活动专属模板`}</p></div><button className="icon-button" onClick={() => void load()} aria-label="刷新"><RefreshCw /></button></div>
          <div className="table-wrap"><table><thead><tr><th>模板</th><th>版本</th><th /></tr></thead><tbody>
            {!loading && templates.length === 0 && <tr><td colSpan={3} className="empty-cell">暂无模板</td></tr>}
            {templates.map((template) => <tr key={template.id}>
              <td><b>{template.name}</b><small>{template.fields.length} 个字段</small></td><td>v{template.version}</td><td><div className="panel-actions"><button className="icon-button" aria-label="编辑模板" onClick={() => { setForm(draftFromTemplate(template)); setNotice(""); setError(""); }}><Edit3 /></button><button className="icon-button" aria-label="归档模板" onClick={() => void archiveTemplate(template)}><Trash2 /></button></div></td>
            </tr>)}
          </tbody></table></div>
        </section>
      </aside>
    </div>
  );
}
