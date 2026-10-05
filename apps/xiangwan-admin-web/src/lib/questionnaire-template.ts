import type { QuestionnaireFieldType } from "./types";

export type TemplateFieldDraft = {
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

const choiceTypes = new Set<QuestionnaireFieldType>(["single_choice", "multiple_choice", "area"]);

export function isTemplateChoiceFieldType(type: QuestionnaireFieldType) {
  return choiceTypes.has(type);
}

export function parseTemplateOptions(value: string) {
  return value
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean)
    .map((line) => {
      const separator = line.indexOf("=");
      return separator < 1
        ? { code: line.toLowerCase().replace(/[^a-z0-9_]/g, "_") || "option", label: line }
        : { code: line.slice(0, separator).trim(), label: line.slice(separator + 1).trim() };
    });
}

export function buildTemplateFields(fields: TemplateFieldDraft[]) {
  return fields.map((field, index) => ({
    code: field.code.trim(),
    type: field.type,
    label: field.label.trim(),
    help_text: field.helpText.trim(),
    required: field.required,
    sort_order: index,
    min_length: choiceTypes.has(field.type) || !field.minLength ? null : Number(field.minLength),
    max_length: choiceTypes.has(field.type) || !field.maxLength ? null : Number(field.maxLength),
    max_selections: field.type === "multiple_choice" && field.maxSelections ? Number(field.maxSelections) : null,
    options: choiceTypes.has(field.type) ? parseTemplateOptions(field.options) : [],
  }));
}
