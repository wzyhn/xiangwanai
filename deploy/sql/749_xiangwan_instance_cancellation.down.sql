-- Development/test rollback reference only. Production rollback is roll-forward.

DROP TRIGGER IF EXISTS trg_xiangwan_activity_instances_cancellation_guard
    ON xiangwan_activity_instances;
DROP TRIGGER IF EXISTS trg_xiangwan_instance_cancellation_receipts_no_truncate
    ON xiangwan_instance_cancellation_receipts;
DROP TRIGGER IF EXISTS trg_xiangwan_instance_cancellation_receipts_no_update_delete
    ON xiangwan_instance_cancellation_receipts;
DROP TRIGGER IF EXISTS trg_xiangwan_instance_cancellation_receipts_validate
    ON xiangwan_instance_cancellation_receipts;
DROP FUNCTION IF EXISTS xiangwan_enforce_instance_cancellation();
DROP FUNCTION IF EXISTS xiangwan_reject_instance_cancellation_receipt_mutation();
DROP FUNCTION IF EXISTS xiangwan_validate_instance_cancellation_receipt();

DROP TABLE IF EXISTS xiangwan_instance_cancellation_receipts;

DROP TRIGGER IF EXISTS trg_xiangwan_session_cancellation_previews_parent_guard
    ON xiangwan_session_cancellation_previews;
DROP FUNCTION IF EXISTS xiangwan_guard_session_preview_parent_update();
ALTER TABLE IF EXISTS xiangwan_session_cancellation_previews
    DROP CONSTRAINT IF EXISTS xiangwan_session_cancellation_previews_parent_fkey,
    DROP COLUMN IF EXISTS parent_instance_preview_id;

DROP TRIGGER IF EXISTS trg_xiangwan_instance_cancellation_previews_update_guard
    ON xiangwan_instance_cancellation_previews;
DROP FUNCTION IF EXISTS xiangwan_guard_instance_cancellation_preview_update();
DROP TABLE IF EXISTS xiangwan_instance_cancellation_previews;
