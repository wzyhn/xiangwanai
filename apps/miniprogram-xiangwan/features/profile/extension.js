"use strict";

function validText(value, maximum, allowNewline = false) {
  return (
    Array.from(value).length <= maximum &&
    !/[\u0000-\u0008\u000b-\u001f\u007f]/.test(value) &&
    (allowNewline || !/[\r\n]/.test(value))
  );
}

function parseTags(input) {
  const tags = String(input || "")
    .split(/[,，、\n]/)
    .map((tag) => tag.trim())
    .filter(Boolean);
  if (
    tags.length > 8 ||
    new Set(tags).size !== tags.length ||
    tags.some((tag) => !validText(tag, 30))
  )
    return null;
  return tags;
}

function readProfileExtension(raw) {
  const profile = raw && raw.profile;
  if (
    !profile ||
    !Number.isSafeInteger(profile.version) ||
    profile.version < 0 ||
    typeof profile.occupation !== "string" ||
    typeof profile.introduction !== "string" ||
    !Array.isArray(profile.tags) ||
    profile.tags.some((tag) => typeof tag !== "string") ||
    !profile.visibility ||
    typeof profile.visibility.occupation !== "boolean" ||
    typeof profile.visibility.introduction !== "boolean" ||
    typeof profile.visibility.tags !== "boolean"
  )
    return null;
  const pending = raw.pending_update;
  if (
    pending &&
    (!Number.isSafeInteger(pending.candidate_version) ||
      typeof pending.introduction !== "string" ||
      !Array.isArray(pending.tags) ||
      pending.tags.some((tag) => typeof tag !== "string"))
  )
    return null;
  return { profile, pending: pending || null };
}

function buildPrivateProfileExtension(raw, introductionInput, tagsInput) {
  const current = readProfileExtension(raw);
  if (!current) return { ok: false, message: "个人资料暂不可读取，请稍后重试" };
  const introduction = String(introductionInput || "")
    .replace(/\r\n/g, "\n")
    .trim();
  const tags = parseTags(tagsInput);
  if (!validText(introduction, 500, true)) {
    return { ok: false, message: "简介不能超过 500 字，且不能包含控制字符" };
  }
  if (!tags) return { ok: false, message: "标签最多 8 个，每个不超过 30 字且不能重复" };
  if (current.pending) {
    const samePending =
      introduction === current.pending.introduction &&
      JSON.stringify(tags) === JSON.stringify(current.pending.tags);
    return samePending
      ? { ok: true, unchanged: true }
      : { ok: false, message: "个人资料正在审核，请稍后再修改标签和简介" };
  }
  const profile = current.profile;
  const samePublished =
    introduction === profile.introduction && JSON.stringify(tags) === JSON.stringify(profile.tags);
  if (samePublished) return { ok: true, unchanged: true };
  return {
    ok: true,
    unchanged: false,
    payload: {
      expected_version: profile.version,
      occupation: profile.occupation,
      introduction,
      tags,
      // A contact setup must never silently publish profile fields.
      visibility: { occupation: profile.visibility.occupation, introduction: false, tags: false },
    },
  };
}

module.exports = { buildPrivateProfileExtension, parseTags, readProfileExtension };
