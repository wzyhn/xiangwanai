"use client";

import {
  Ban,
  CheckCircle2,
  Pencil,
  RefreshCw,
  Save,
  ShieldCheck,
  UserPlus,
  Users,
  XCircle,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { allPages, api, displayError, operationHeaders, operationKey } from "@/lib/api";
import type { AuthStatus, InstanceListItem, InstanceRole, Person } from "@/lib/types";
import { canReadAuditLog } from "@/lib/admin-permissions";
import { PeopleBindingControls } from "@/components/people-binding";
import { requestRequiredReason } from "@/lib/required-reason-dialog";

const roleLabels: Record<InstanceRole["role_code"], string> = {
  host: "主理人",
  invited_guest: "特邀嘉宾",
  course_instructor: "课程讲师",
  event_speaker: "活动分享者",
};

const roleOptions = Object.entries(roleLabels) as Array<[InstanceRole["role_code"], string]>;

const statusLabels: Record<string, string> = {
  draft: "草稿",
  published: "已发布",
  archived: "已归档",
  pending: "待审核",
  approved: "已审核",
  rejected: "已退回",
};

function formatTime(value: string): string {
  return new Intl.DateTimeFormat("zh-CN", {
    month: "numeric",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  }).format(new Date(value));
}

type ProfileForm = {
  id: string;
  expectedVersion: number;
  displayName: string;
  headline: string;
  introduction: string;
};

const emptyProfileForm: ProfileForm = {
  id: "",
  expectedVersion: 0,
  displayName: "",
  headline: "",
  introduction: "",
};

export default function PeoplePage() {
  const [people, setPeople] = useState<Person[]>([]);
  const [canManageBindings, setCanManageBindings] = useState(false);
  const [instances, setInstances] = useState<InstanceListItem[]>([]);
  const [roles, setRoles] = useState<InstanceRole[]>([]);
  const [instanceId, setInstanceId] = useState("");
  const [selectedPerson, setSelectedPerson] = useState("");
  const [roleCode, setRoleCode] = useState<InstanceRole["role_code"]>("host");
  const [reason, setReason] = useState("");
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [profileSaving, setProfileSaving] = useState(false);
  const [profileForm, setProfileForm] = useState<ProfileForm>(emptyProfileForm);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");

  const load = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const [personPage, instancePage, auth] = await Promise.all([
        allPages<Person>("/people"),
        allPages<InstanceListItem>("/instances"),
        api<AuthStatus>("/auth/status"),
      ]);
      setPeople(personPage.items);
      setCanManageBindings(canReadAuditLog(auth));
      setInstances(instancePage.items);
      setInstanceId((current) => current || instancePage.items[0]?.instance.id || "");
    } catch (value) {
      setError(displayError(value));
    } finally {
      setLoading(false);
    }
  }, []);

  const loadRoles = useCallback(async () => {
    if (!instanceId) {
      setRoles([]);
      return;
    }
    try {
      const result = await api<{ items: InstanceRole[] }>(`/instances/${instanceId}/roles`);
      setRoles(result.items);
    } catch (value) {
      setError(displayError(value));
    }
  }, [instanceId]);

  useEffect(() => {
    void load();
  }, [load]);
  useEffect(() => {
    void loadRoles();
  }, [loadRoles]);

  const selectedInstance = useMemo(
    () => instances.find((item) => item.instance.id === instanceId),
    [instances, instanceId],
  );
  const eligiblePeople = useMemo(
    () =>
      people.filter(
        (person) =>
          person.profile_status === "published" &&
          person.moderation_status === "approved" &&
          person.has_active_binding,
      ),
    [people],
  );

  async function assignRole() {
    if (!instanceId || !selectedPerson || !reason.trim()) {
      setError("请选择活动、已审核人物并填写绑定原因");
      return;
    }
    setSaving(true);
    setError("");
    setNotice("");
    try {
      await api(`/instances/${instanceId}/roles`, {
        method: "POST",
        headers: operationHeaders(operationKey()),
        body: JSON.stringify({
          people_profile_id: selectedPerson,
          role_code: roleCode,
          grant_reason: reason.trim(),
        }),
      });
      setReason("");
      setNotice("活动角色已绑定，详情页会在公开资料和发布链路满足条件后展示。");
      await loadRoles();
    } catch (value) {
      setError(displayError(value));
    } finally {
      setSaving(false);
    }
  }

  async function revokeRole(role: InstanceRole) {
    const revokeReason = await requestRequiredReason({
      title: `撤销“${role.display_name}”的角色`,
      description: "原因将保留在运营审计记录中。",
    });
    if (!revokeReason?.trim()) return;
    setError("");
    setNotice("");
    try {
      await api(`/instances/${instanceId}/roles/${role.id}/revocations`, {
        method: "POST",
        headers: operationHeaders(operationKey()),
        body: JSON.stringify({ expected_version: role.version, reason: revokeReason.trim() }),
      });
      setNotice("角色已撤销，历史绑定保留用于审计。");
      await loadRoles();
    } catch (value) {
      setError(displayError(value));
    }
  }

  function editProfile(person: Person) {
    setProfileForm({
      id: person.id,
      expectedVersion: person.version,
      displayName: person.display_name,
      headline: person.headline || "",
      introduction: person.introduction,
    });
    setError("");
    setNotice("");
  }

  function newProfile() {
    setProfileForm(emptyProfileForm);
    setError("");
    setNotice("");
  }

  async function saveProfile() {
    const displayName = profileForm.displayName.trim();
    const headline = profileForm.headline.trim();
    const introduction = profileForm.introduction.trim();
    if (!displayName) {
      setError("人物名称不能为空");
      return;
    }
    setProfileSaving(true);
    setError("");
    setNotice("");
    const editing = Boolean(profileForm.id);
    try {
      const value = await api<Person>(editing ? `/people/${profileForm.id}` : "/people", {
        method: editing ? "PATCH" : "POST",
        headers: operationHeaders(operationKey()),
        body: JSON.stringify(
          editing
            ? {
                expected_version: profileForm.expectedVersion,
                display_name: displayName,
                headline,
                introduction,
              }
            : {
                display_name: displayName,
                headline,
                introduction,
              },
        ),
      });
      setPeople((current) =>
        editing
          ? current.map((person) => (person.id === value.id ? value : person))
          : [value, ...current],
      );
      setProfileForm(emptyProfileForm);
      setNotice(
        editing
          ? "人物资料已更新，系统已重新进入待审核状态。"
          : "人物资料已创建，请审核后再绑定活动角色。",
      );
    } catch (value) {
      setError(displayError(value));
    } finally {
      setProfileSaving(false);
    }
  }

  async function reviewProfile(person: Person, decision: "approved" | "rejected") {
    if (person.moderation_status !== "pending") return;
    setProfileSaving(true);
    setError("");
    setNotice("");
    try {
      const value = await api<Person>(`/people/${person.id}/reviews`, {
        method: "POST",
        headers: operationHeaders(operationKey()),
        body: JSON.stringify({ expected_version: person.version, decision }),
      });
      setPeople((current) => current.map((item) => (item.id === value.id ? value : item)));
      setNotice(
        decision === "approved"
          ? "人物资料已审核通过，可继续绑定活动角色。"
          : "人物资料已退回，编辑后可以再次提交审核。",
      );
    } catch (value) {
      setError(displayError(value));
    } finally {
      setProfileSaving(false);
    }
  }

  return (
    <div className="page-content">
      <section className="metric-grid compact">
        <article className="metric blue">
          <span>人物资料</span>
          <strong>{loading ? "—" : people.length}</strong>
          <small>后台可见资料</small>
        </article>
        <article className="metric green">
          <span>可绑定人物</span>
          <strong>{loading ? "—" : eligiblePeople.length}</strong>
          <small>已审核且有可信绑定</small>
        </article>
        <article className="metric yellow">
          <span>当前活动角色</span>
          <strong>{roles.length}</strong>
          <small>{selectedInstance?.instance.title || "请选择活动"}</small>
        </article>
      </section>

      <section className="panel">
        <div className="panel-head">
          <div>
            <h2>人物与活动角色</h2>
            <p>
              角色绑定只接受已审核 PeopleProfile；服务器会从可信绑定解析主体，不允许手填 Principal。
            </p>
          </div>
          <button className="secondary-button" onClick={() => void load()}>
            <RefreshCw /> 刷新
          </button>
        </div>
        {error && <div className="inline-message error table-message">{error}</div>}
        {notice && <div className="inline-message success table-message">{notice}</div>}
        <div className="filter-row">
          <label>
            目标活动
            <select value={instanceId} onChange={(event) => setInstanceId(event.target.value)}>
              <option value="">请选择活动期次</option>
              {instances.map((item) => (
                <option key={item.instance.id} value={item.instance.id}>
                  {item.instance.title} · {item.series_title}
                </option>
              ))}
            </select>
          </label>
        </div>
      </section>

      <div className="content-grid two-column">
        <section className="panel">
          <div className="panel-head">
            <div>
              <h2>
                <Users /> 人物资料
              </h2>
              <p>资料的身份与审核事实来自 People 域；头像归属于可信主体资料。</p>
            </div>
            <button className="primary-button" onClick={newProfile}>
              <UserPlus /> 新增人物
            </button>
          </div>
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>人物</th>
                  <th>资料状态</th>
                  <th>绑定状态</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {people.length === 0 ? (
                  <tr>
                    <td colSpan={4} className="empty-cell">
                      暂无人物资料
                    </td>
                  </tr>
                ) : (
                  people.map((person) => (
                    <tr key={person.id}>
                      <td>
                        <b>{person.display_name}</b>
                        <small>{person.headline || person.introduction || "暂无简介"}</small>
                      </td>
                      <td>
                        <span className={`status-pill ${person.profile_status}`}>
                          {statusLabels[person.profile_status] || person.profile_status} ·{" "}
                          {statusLabels[person.moderation_status] || person.moderation_status}
                        </span>
                      </td>
                      <td>
                        {person.has_active_binding ? (
                          <span className="status-pill published">可信绑定</span>
                        ) : (
                          <span className="status-pill draft">未绑定</span>
                        )}
                        {canManageBindings &&
                          (person.has_active_binding ||
                            (person.profile_status === "published" &&
                              person.moderation_status === "approved")) && (
                            <PeopleBindingControls
                              person={person}
                              onComplete={async (message) => {
                                if (message) setNotice(message);
                                await load();
                              }}
                            />
                          )}
                      </td>
                      <td>
                        <div
                          style={{
                            display: "flex",
                            gap: 6,
                            justifyContent: "flex-end",
                            flexWrap: "wrap",
                          }}
                        >
                          <button
                            className="table-link"
                            onClick={() => editProfile(person)}
                            disabled={profileSaving}
                          >
                            <Pencil /> 编辑
                          </button>
                          {person.moderation_status === "pending" && (
                            <>
                              <button
                                className="table-link"
                                onClick={() => void reviewProfile(person, "approved")}
                                disabled={profileSaving}
                              >
                                <CheckCircle2 /> 通过
                              </button>
                              <button
                                className="table-link"
                                onClick={() => void reviewProfile(person, "rejected")}
                                disabled={profileSaving}
                              >
                                <XCircle /> 退回
                              </button>
                            </>
                          )}
                          <button
                            className="table-link"
                            disabled={
                              !person.has_active_binding ||
                              person.profile_status !== "published" ||
                              person.moderation_status !== "approved"
                            }
                            onClick={() => setSelectedPerson(person.id)}
                          >
                            <UserPlus /> 选择
                          </button>
                        </div>
                      </td>
                    </tr>
                  ))
                )}
              </tbody>
            </table>
          </div>
        </section>

        <section className="panel">
          <div className="panel-head">
            <div>
              <h2>
                <ShieldCheck /> 当前角色
              </h2>
              <p>当前活动的有效角色绑定。</p>
            </div>
          </div>
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>人物</th>
                  <th>角色</th>
                  <th>授权原因</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {roles.length === 0 ? (
                  <tr>
                    <td colSpan={4} className="empty-cell">
                      当前活动暂无角色
                    </td>
                  </tr>
                ) : (
                  roles.map((role) => (
                    <tr key={role.id}>
                      <td>
                        <b>{role.display_name}</b>
                        <small>{formatTime(role.granted_at)}</small>
                      </td>
                      <td>{roleLabels[role.role_code] || role.role_code}</td>
                      <td>{role.grant_reason}</td>
                      <td>
                        <button
                          className="icon-button"
                          aria-label={`撤销${role.display_name}角色`}
                          onClick={() => void revokeRole(role)}
                        >
                          <Ban />
                        </button>
                      </td>
                    </tr>
                  ))
                )}
              </tbody>
            </table>
          </div>
        </section>
      </div>

      <section className="panel editor-form" style={{ marginTop: 20 }}>
        <div className="panel-head">
          <div>
            <h2>
              <Pencil /> {profileForm.id ? "编辑人物资料" : "新增人物资料"}
            </h2>
            <p>
              编辑公开资料会保留可信绑定，但会重新进入待审核状态；此表单不接收 Principal 或手机号。
            </p>
          </div>
          {profileForm.id && (
            <button className="secondary-button" onClick={newProfile}>
              取消编辑
            </button>
          )}
        </div>
        <div className="field-grid">
          <label>
            姓名/显示名称 <span className="required-mark">*</span>
            <input
              value={profileForm.displayName}
              maxLength={200}
              disabled={profileSaving}
              onChange={(event) =>
                setProfileForm((current) => ({ ...current, displayName: event.target.value }))
              }
              placeholder="例如：李老师"
              required={true}
              aria-required={true}
            />
          </label>
          <label>
            一句话介绍
            <input
              value={profileForm.headline}
              maxLength={200}
              disabled={profileSaving}
              onChange={(event) =>
                setProfileForm((current) => ({ ...current, headline: event.target.value }))
              }
              placeholder="例如：AI 产品顾问"
            />
          </label>
          <label className="full">
            人物简介
            <textarea
              rows={4}
              maxLength={4000}
              value={profileForm.introduction}
              disabled={profileSaving}
              onChange={(event) =>
                setProfileForm((current) => ({ ...current, introduction: event.target.value }))
              }
              placeholder="填写公开展示的个人介绍"
            />
          </label>
        </div>
        <div className="editor-actions">
          <button
            className="primary-button"
            disabled={profileSaving || !profileForm.displayName.trim()}
            onClick={() => void saveProfile()}
          >
            <Save /> {profileSaving ? "提交中…" : profileForm.id ? "保存并重新审核" : "创建资料"}
          </button>
        </div>
      </section>

      <section className="panel editor-form" style={{ marginTop: 20 }}>
        <div className="panel-head">
          <div>
            <h2>
              <UserPlus /> 绑定活动角色
            </h2>
            <p>选择人物后提交；同一个活动、人物和角色只能存在一条有效绑定。</p>
          </div>
        </div>
        <div className="field-grid">
          <label>
            人物 <span className="required-mark">*</span>
            <select
              value={selectedPerson}
              onChange={(event) => setSelectedPerson(event.target.value)}
              required={true}
              aria-required={true}
            >
              <option value="">请选择已审核人物</option>
              {eligiblePeople.map((person) => (
                <option key={person.id} value={person.id}>
                  {person.display_name}
                </option>
              ))}
            </select>
          </label>
          <label>
            活动角色 <span className="required-mark">*</span>
            <select
              value={roleCode}
              onChange={(event) => setRoleCode(event.target.value as InstanceRole["role_code"])}
              required={true}
              aria-required={true}
            >
              {roleOptions.map(([value, label]) => (
                <option key={value} value={value}>
                  {label}
                </option>
              ))}
            </select>
          </label>
          <label className="full">
            绑定原因 <span className="required-mark">*</span>
            <textarea
              rows={2}
              maxLength={500}
              value={reason}
              onChange={(event) => setReason(event.target.value)}
              placeholder="例如：本期活动主理人"
              required={true}
              aria-required={true}
            />
          </label>
        </div>
        <div className="editor-actions">
          <button
            className="primary-button"
            disabled={saving || !instanceId}
            onClick={() => void assignRole()}
          >
            <UserPlus /> {saving ? "提交中…" : "绑定角色"}
          </button>
        </div>
      </section>
    </div>
  );
}
