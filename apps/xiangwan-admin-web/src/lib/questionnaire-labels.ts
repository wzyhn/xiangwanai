import type { QuestionnaireFieldType } from "@/lib/types";

const QUESTIONNAIRE_FIELD_TYPE_LABELS: Record<QuestionnaireFieldType, string> = {
  single_choice: "单选",
  multiple_choice: "多选",
  single_line: "单行文本",
  multiline: "多行文本",
  area: "地区",
};

/** Keep implementation enum values out of operator-facing questionnaire summaries. */
export function questionnaireFieldTypeLabel(value: string): string {
  return QUESTIONNAIRE_FIELD_TYPE_LABELS[value as QuestionnaireFieldType] || "未知字段类型";
}
