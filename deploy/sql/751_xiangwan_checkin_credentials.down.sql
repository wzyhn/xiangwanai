-- 751_xiangwan_checkin_credentials.down.sql

DROP TRIGGER IF EXISTS trg_xw_checkin_attempts_no_truncate
    ON xiangwan_checkin_verification_attempts;
DROP TRIGGER IF EXISTS trg_xw_checkin_attempts_no_delete
    ON xiangwan_checkin_verification_attempts;
DROP TRIGGER IF EXISTS trg_xw_checkin_attempts_no_update
    ON xiangwan_checkin_verification_attempts;
DROP TRIGGER IF EXISTS trg_xw_checkin_credentials_no_truncate
    ON xiangwan_checkin_credentials;
DROP TRIGGER IF EXISTS trg_xw_checkin_credentials_no_delete
    ON xiangwan_checkin_credentials;
DROP TRIGGER IF EXISTS trg_xw_checkin_credentials_update_guard
    ON xiangwan_checkin_credentials;
DROP TRIGGER IF EXISTS trg_xw_checkin_credentials_insert_guard
    ON xiangwan_checkin_credentials;

DROP FUNCTION IF EXISTS xiangwan_guard_checkin_credential_update();
DROP FUNCTION IF EXISTS xiangwan_guard_checkin_credential_insert();

DROP TABLE IF EXISTS xiangwan_checkin_verification_attempts;
DROP TABLE IF EXISTS xiangwan_checkin_credentials;
