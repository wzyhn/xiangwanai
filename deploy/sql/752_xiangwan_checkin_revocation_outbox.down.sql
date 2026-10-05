-- 752_xiangwan_checkin_revocation_outbox.down.sql

DROP TRIGGER IF EXISTS trg_xiangwan_checkin_correction_no_truncate
    ON xiangwan_checkin_correction_outbox;
DROP TRIGGER IF EXISTS trg_xiangwan_checkin_correction_no_delete
    ON xiangwan_checkin_correction_outbox;
DROP TRIGGER IF EXISTS trg_xiangwan_checkin_correction_guard
    ON xiangwan_checkin_correction_outbox;
DROP TRIGGER IF EXISTS trg_xiangwan_checkin_correction_validate
    ON xiangwan_checkin_correction_outbox;

DROP FUNCTION IF EXISTS xiangwan_reject_checkin_correction_removal();
DROP FUNCTION IF EXISTS xiangwan_guard_checkin_correction_outbox();
DROP FUNCTION IF EXISTS xiangwan_validate_checkin_correction_outbox();

DROP TABLE IF EXISTS xiangwan_checkin_correction_outbox;

ALTER TABLE xiangwan_checkins
    DROP CONSTRAINT IF EXISTS xiangwan_checkins_correction_identity_key;
