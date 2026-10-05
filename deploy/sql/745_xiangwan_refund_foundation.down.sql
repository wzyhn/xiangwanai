-- Development/test rollback reference only. Production rollback is roll-forward.

DROP TRIGGER IF EXISTS trg_xiangwan_registrations_paid_cancellation
    ON xiangwan_registrations;
DROP TRIGGER IF EXISTS trg_xiangwan_orders_paid_terminal
    ON xiangwan_orders;
DROP TRIGGER IF EXISTS trg_xiangwan_refund_cases_no_truncate
    ON xiangwan_refund_cases;
DROP TRIGGER IF EXISTS trg_xiangwan_refund_cases_no_delete
    ON xiangwan_refund_cases;
DROP TRIGGER IF EXISTS trg_xiangwan_refund_cases_guard
    ON xiangwan_refund_cases;

DROP FUNCTION IF EXISTS xiangwan_enforce_cancelled_paid_registration();
DROP FUNCTION IF EXISTS xiangwan_enforce_paid_order_terminal();
DROP FUNCTION IF EXISTS xiangwan_reject_refund_case_removal();
DROP FUNCTION IF EXISTS xiangwan_guard_refund_case_mutation();

DROP TABLE IF EXISTS xiangwan_refund_cases;

ALTER TABLE IF EXISTS xiangwan_orders
    DROP CONSTRAINT IF EXISTS xiangwan_orders_refund_identity_key;
