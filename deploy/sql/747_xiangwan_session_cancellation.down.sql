-- Development/test rollback reference only. Production rollback is roll-forward.

DROP TRIGGER IF EXISTS trg_xiangwan_capacity_holds_active_session_guard
    ON xiangwan_capacity_holds;
DROP TRIGGER IF EXISTS trg_xiangwan_registrations_open_session_guard
    ON xiangwan_registrations;
DROP TRIGGER IF EXISTS trg_xiangwan_activity_sessions_cancellation_guard
    ON xiangwan_activity_sessions;
DROP TRIGGER IF EXISTS trg_xiangwan_session_cancellation_receipts_no_truncate
    ON xiangwan_session_cancellation_receipts;
DROP TRIGGER IF EXISTS trg_xiangwan_session_cancellation_receipts_no_update_delete
    ON xiangwan_session_cancellation_receipts;
DROP TRIGGER IF EXISTS trg_xiangwan_session_cancellation_receipts_validate
    ON xiangwan_session_cancellation_receipts;

DROP FUNCTION IF EXISTS xiangwan_enforce_active_hold_session();
DROP FUNCTION IF EXISTS xiangwan_enforce_open_registration_session();
DROP FUNCTION IF EXISTS xiangwan_enforce_session_cancellation();
DROP FUNCTION IF EXISTS xiangwan_reject_session_cancellation_receipt_mutation();
DROP FUNCTION IF EXISTS xiangwan_validate_session_cancellation_receipt();

DROP TABLE IF EXISTS xiangwan_session_cancellation_receipts;

ALTER TABLE IF EXISTS xiangwan_activity_sessions
    DROP CONSTRAINT IF EXISTS xiangwan_activity_sessions_cancellation_identity_key;
