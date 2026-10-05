-- 753_xiangwan_checkin_verification_fingerprint.up.sql
-- Store a keyed, domain-separated fingerprint of each verification request.
-- The presented QR token or backup code never enters PostgreSQL.

ALTER TABLE xiangwan_checkin_verification_attempts
    ADD COLUMN IF NOT EXISTS request_fingerprint BYTEA;

ALTER TABLE xiangwan_checkin_verification_attempts
    DROP CONSTRAINT IF EXISTS xw_checkin_attempts_request_fingerprint_check,
    ADD CONSTRAINT xw_checkin_attempts_request_fingerprint_check
        CHECK (
            request_fingerprint IS NOT NULL
            AND OCTET_LENGTH(request_fingerprint) = 32
        ) NOT VALID;

COMMENT ON COLUMN
    xiangwan_checkin_verification_attempts.request_fingerprint
IS 'Keyed request fingerprint only; never a presented credential or reusable token';
