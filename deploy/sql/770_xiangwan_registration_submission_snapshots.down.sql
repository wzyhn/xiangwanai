-- Development/test rollback reference only. Production rollback is roll-forward.

DROP TRIGGER IF EXISTS trg_xw_questionnaire_assignment_instance_lock
    ON xiangwan_instance_questionnaires;
DROP TRIGGER IF EXISTS trg_xw_registration_answer_no_truncate
    ON xiangwan_registration_answers;
DROP TRIGGER IF EXISTS trg_xw_registration_answer_validate
    ON xiangwan_registration_answers;
DROP TRIGGER IF EXISTS trg_xw_registration_answer_immutable
    ON xiangwan_registration_answers;
DROP TRIGGER IF EXISTS trg_xw_registration_snapshot_no_truncate
    ON xiangwan_registration_snapshots;
DROP TRIGGER IF EXISTS trg_xw_registration_snapshot_immutable
    ON xiangwan_registration_snapshots;

DROP FUNCTION IF EXISTS xiangwan_reject_registration_submission_mutation();
DROP FUNCTION IF EXISTS xiangwan_validate_registration_answer_snapshot();
DROP FUNCTION IF EXISTS xiangwan_lock_questionnaire_assignment_instance();
DROP INDEX IF EXISTS idx_xw_registration_answers_version;
DROP TABLE IF EXISTS xiangwan_registration_answers;
DROP TABLE IF EXISTS xiangwan_registration_snapshots;
DROP FUNCTION IF EXISTS xiangwan_valid_questionnaire_answer(
    TEXT,
    BOOLEAN,
    INTEGER,
    INTEGER,
    INTEGER,
    JSONB,
    JSONB
);

ALTER TABLE xiangwan_questionnaire_fields
    DROP CONSTRAINT IF EXISTS xw_questionnaire_field_identity_key;
