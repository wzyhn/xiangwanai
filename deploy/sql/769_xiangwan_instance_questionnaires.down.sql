-- Development/test rollback reference only. Production rollback is roll-forward.

DROP TRIGGER IF EXISTS trg_xw_questionnaire_field_no_truncate
    ON xiangwan_questionnaire_fields;
DROP TRIGGER IF EXISTS trg_xw_questionnaire_version_no_truncate
    ON xiangwan_questionnaire_versions;
DROP TRIGGER IF EXISTS trg_xw_questionnaire_assignment_no_truncate
    ON xiangwan_instance_questionnaires;
DROP TRIGGER IF EXISTS trg_xw_questionnaire_assignment_immutable
    ON xiangwan_instance_questionnaires;
DROP TRIGGER IF EXISTS trg_xw_questionnaire_assignment_validate
    ON xiangwan_instance_questionnaires;
DROP TRIGGER IF EXISTS trg_xw_questionnaire_version_guard
    ON xiangwan_questionnaire_versions;
DROP TRIGGER IF EXISTS trg_xw_questionnaire_field_guard
    ON xiangwan_questionnaire_fields;

DROP FUNCTION IF EXISTS xiangwan_reject_questionnaire_assignment_mutation();
DROP FUNCTION IF EXISTS xiangwan_reject_questionnaire_truncate();
DROP FUNCTION IF EXISTS xiangwan_validate_questionnaire_assignment();
DROP FUNCTION IF EXISTS xiangwan_guard_questionnaire_version_mutation();
DROP FUNCTION IF EXISTS xiangwan_guard_questionnaire_field_mutation();

DROP INDEX IF EXISTS idx_xw_instance_questionnaires_latest;
DROP INDEX IF EXISTS idx_xw_questionnaire_fields_version_order;
DROP TABLE IF EXISTS xiangwan_instance_questionnaires;
DROP TABLE IF EXISTS xiangwan_questionnaire_fields;
DROP TABLE IF EXISTS xiangwan_questionnaire_versions;
DROP FUNCTION IF EXISTS xiangwan_valid_questionnaire_options(JSONB);
