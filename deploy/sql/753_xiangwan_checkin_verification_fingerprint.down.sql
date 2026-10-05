-- 753_xiangwan_checkin_verification_fingerprint.down.sql

ALTER TABLE xiangwan_checkin_verification_attempts
    DROP CONSTRAINT IF EXISTS xw_checkin_attempts_request_fingerprint_check,
    DROP COLUMN IF EXISTS request_fingerprint;
