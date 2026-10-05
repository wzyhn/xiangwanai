-- Development/test rollback reference only. Production rollback is roll-forward.

DROP TRIGGER IF EXISTS trg_xiangwan_session_cancellation_receipts_preview
    ON xiangwan_session_cancellation_receipts;
DROP FUNCTION IF EXISTS xiangwan_validate_session_cancellation_preview_receipt();

ALTER TABLE IF EXISTS xiangwan_session_cancellation_receipts
    DROP CONSTRAINT IF EXISTS xiangwan_session_cancellation_receipts_notification_check,
    DROP CONSTRAINT IF EXISTS xiangwan_session_cancellation_receipts_preview_required,
    DROP CONSTRAINT IF EXISTS xiangwan_session_cancellation_receipts_preview_key,
    DROP CONSTRAINT IF EXISTS xiangwan_session_cancellation_receipts_preview_fkey,
    DROP COLUMN IF EXISTS notification_strategy,
    DROP COLUMN IF EXISTS preview_id;

DROP TRIGGER IF EXISTS trg_xiangwan_session_cancellation_previews_update_guard
    ON xiangwan_session_cancellation_previews;
DROP FUNCTION IF EXISTS xiangwan_guard_session_cancellation_preview_update();

DROP TABLE IF EXISTS xiangwan_session_cancellation_previews;
