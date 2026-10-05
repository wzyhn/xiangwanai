import type { QuestionnaireFieldType, QuestionnaireOption } from "./types";

export type QuestionnaireConfigField = {
  code: string;
  type: QuestionnaireFieldType;
  label: string;
  maxLength: number;
  maxSelections: number;
  options: QuestionnaireOption[];
};

function choiceField(type: QuestionnaireFieldType): boolean {
  return type === "single_choice" || type === "multiple_choice" || type === "area";
}

/**
 * Validate the extra registration fields shared by every activity type.
 * Nickname and phone are platform-owned fields and therefore are not part of
 * this operator-authored list.
 */
export function validateQuestionnaireConfig(input: {
  privacyPurpose: string;
  privacyPolicyVersion: string;
  fields: QuestionnaireConfigField[];
}): string {
  const fields = Array.isArray(input.fields) ? input.fields : [];
  if (fields.length === 0) return "";
  if (!input.privacyPurpose.trim()) return "请填写问卷信息用途说明";
  if (!input.privacyPolicyVersion.trim() ||
    !/^[A-Za-z0-9][A-Za-z0-9._:-]{0,99}$/.test(input.privacyPolicyVersion.trim())) {
    return "请填写有效的隐私政策版本（例如 privacy-v1）";
  }
  if (fields.length > 100) return "报名信息模板最多 100 个字段";
  const codes = new Set<string>();
  for (const [index, field] of fields.entries()) {
    const number = index + 1;
    if (!/^[a-z][a-z0-9_]{0,63}$/.test(field.code.trim())) return `第 ${number} 个报名字段编码无效`;
    if (codes.has(field.code.trim())) return "报名字段编码不能重复";
    codes.add(field.code.trim());
    if (!field.label.trim()) return `请填写第 ${number} 个报名字段标题`;
    if (choiceField(field.type)) {
      if (field.options.length < 1 || field.options.length > 100) return `第 ${number} 个选择字段至少需要 1 个选项`;
      const optionCodes = new Set<string>();
      for (const option of field.options) {
        if (!/^[a-z][a-z0-9_]{0,63}$/.test(option.code.trim()) || optionCodes.has(option.code.trim())) return `第 ${number} 个字段的选项编码无效或重复`;
        if (!option.label.trim()) return `请填写第 ${number} 个字段的选项名称`;
        optionCodes.add(option.code.trim());
      }
      if (field.type === "multiple_choice" &&
        (!Number.isInteger(field.maxSelections) || field.maxSelections < 1 || field.maxSelections > field.options.length)) {
        return `第 ${number} 个多选字段的最多选择数无效`;
      }
    } else if (!Number.isInteger(field.maxLength) || field.maxLength < 1 ||
      field.maxLength > (field.type === "single_line" ? 200 : 2000)) {
      return `第 ${number} 个文本字段的字数上限无效`;
    }
  }
  return "";
}
