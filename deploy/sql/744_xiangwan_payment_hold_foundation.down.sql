-- Development/test rollback reference only. Production rollback is roll-forward.

DROP TRIGGER IF EXISTS trg_xiangwan_holds_no_truncate ON xiangwan_capacity_holds;
DROP TRIGGER IF EXISTS trg_xiangwan_holds_no_delete ON xiangwan_capacity_holds;
DROP TRIGGER IF EXISTS trg_xiangwan_orders_no_truncate ON xiangwan_orders;
DROP TRIGGER IF EXISTS trg_xiangwan_orders_no_delete ON xiangwan_orders;
DROP TRIGGER IF EXISTS trg_xiangwan_holds_guard_mutation ON xiangwan_capacity_holds;
DROP TRIGGER IF EXISTS trg_xiangwan_orders_guard_mutation ON xiangwan_orders;

DROP TABLE IF EXISTS xiangwan_capacity_holds;
DROP TABLE IF EXISTS xiangwan_orders;

DROP FUNCTION IF EXISTS xiangwan_reject_commerce_removal();
DROP FUNCTION IF EXISTS xiangwan_guard_capacity_hold_mutation();
DROP FUNCTION IF EXISTS xiangwan_guard_order_mutation();

ALTER TABLE IF EXISTS xiangwan_registrations
    DROP CONSTRAINT IF EXISTS xiangwan_registrations_payment_identity_key;
