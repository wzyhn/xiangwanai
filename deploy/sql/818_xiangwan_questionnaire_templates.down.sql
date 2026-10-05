-- Template versions are append-only in production. This down file exists for
-- migration round-trip verification only; production uses a forward archive.
DROP TRIGGER IF EXISTS trg_xw_questionnaire_template_versions_publication_lock
    ON xiangwan_questionnaire_template_versions;
DROP TRIGGER IF EXISTS trg_xw_questionnaire_templates_publication_lock
    ON xiangwan_questionnaire_templates;
DROP TRIGGER IF EXISTS trg_xw_questionnaire_template_no_delete
    ON xiangwan_questionnaire_templates;
DROP TRIGGER IF EXISTS trg_xw_questionnaire_template_mutation
    ON xiangwan_questionnaire_templates;
DROP TRIGGER IF EXISTS trg_xw_questionnaire_template_version_no_truncate
    ON xiangwan_questionnaire_template_versions;
DROP TRIGGER IF EXISTS trg_xw_questionnaire_template_version_immutable
    ON xiangwan_questionnaire_template_versions;
DROP FUNCTION IF EXISTS xiangwan_guard_questionnaire_template_mutation();
DROP FUNCTION IF EXISTS xiangwan_reject_questionnaire_template_version_mutation();
DROP INDEX IF EXISTS idx_xw_questionnaire_template_list;
DROP INDEX IF EXISTS uq_xw_questionnaire_template_active_name;
DROP TABLE IF EXISTS xiangwan_questionnaire_template_versions;
DROP TABLE IF EXISTS xiangwan_questionnaire_templates;
