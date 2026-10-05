-- Development/test rollback reference only. Production rollback is roll-forward.

DROP TRIGGER IF EXISTS trg_xiangwan_refund_events_no_truncate
    ON xiangwan_refund_events;
DROP TRIGGER IF EXISTS trg_xiangwan_refund_events_no_update_delete
    ON xiangwan_refund_events;
DROP TRIGGER IF EXISTS trg_xiangwan_refund_events_validate
    ON xiangwan_refund_events;

DROP FUNCTION IF EXISTS xiangwan_reject_refund_event_mutation();
DROP FUNCTION IF EXISTS xiangwan_validate_refund_event();

DROP TABLE IF EXISTS xiangwan_refund_events;

ALTER TABLE IF EXISTS xiangwan_refund_cases
    DROP CONSTRAINT IF EXISTS xiangwan_refund_cases_event_identity_key;
