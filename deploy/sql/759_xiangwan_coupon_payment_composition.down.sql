-- Development/test rollback reference only. Production rollback is roll-forward.

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
          FROM xiangwan_orders
         WHERE payment_status = 'settled_zero'
            OR payable_cents = 0
    ) THEN
        RAISE EXCEPTION
            'cannot roll back 719 while zero-settled Order facts exist';
    END IF;
END;
$$;

DROP TRIGGER IF EXISTS trg_xw_coupon_entries_order_state
    ON xiangwan_coupon_entries;
DROP TRIGGER IF EXISTS trg_xw_orders_coupon_state
    ON xiangwan_orders;
DROP FUNCTION IF EXISTS xiangwan_enforce_coupon_order_state();
DROP FUNCTION IF EXISTS xiangwan_assert_coupon_order_state(UUID, UUID);

DROP TRIGGER IF EXISTS trg_xw_coupon_payment_entries_validate
    ON xiangwan_coupon_entries;
DROP FUNCTION IF EXISTS xiangwan_validate_coupon_payment_entry();
DROP TRIGGER IF EXISTS trg_xw_coupon_entries_validate
    ON xiangwan_coupon_entries;
CREATE TRIGGER trg_xw_coupon_entries_validate
    BEFORE INSERT ON xiangwan_coupon_entries
    FOR EACH ROW EXECUTE FUNCTION xiangwan_validate_coupon_entry();

ALTER TABLE xiangwan_orders
    DROP CONSTRAINT IF EXISTS xiangwan_orders_payment_status_check,
    DROP CONSTRAINT IF EXISTS xiangwan_orders_amount_snapshot_check,
    DROP CONSTRAINT IF EXISTS xiangwan_orders_state_shape_check,
    ADD CONSTRAINT xiangwan_orders_payment_status_check
        CHECK (
            payment_status IN (
                'pending', 'unknown', 'paid_confirmed', 'closed_unpaid'
            )
        ),
    ADD CONSTRAINT xiangwan_orders_amount_snapshot_check
        CHECK (
            original_price_cents > 0
            AND discount_cents >= 0
            AND discount_cents <= original_price_cents
            AND payable_cents = original_price_cents - discount_cents
            AND payable_cents > 0
            AND (actual_paid_cents IS NULL OR actual_paid_cents >= 0)
        ),
    ADD CONSTRAINT xiangwan_orders_state_shape_check
        CHECK (
            (
                payment_status IN ('pending', 'unknown')
                AND actual_paid_cents IS NULL
                AND wechat_transaction_id IS NULL
                AND paid_at IS NULL
                AND closed_at IS NULL
            )
            OR (
                payment_status = 'paid_confirmed'
                AND actual_paid_cents = payable_cents
                AND BTRIM(COALESCE(wechat_transaction_id, '')) <> ''
                AND paid_at IS NOT NULL
            )
            OR (
                payment_status = 'closed_unpaid'
                AND actual_paid_cents IS NULL
                AND wechat_transaction_id IS NULL
                AND paid_at IS NULL
                AND closed_at IS NOT NULL
            )
        );

CREATE OR REPLACE FUNCTION xiangwan_guard_order_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
        OR NEW.registration_id IS DISTINCT FROM OLD.registration_id
        OR NEW.series_id IS DISTINCT FROM OLD.series_id
        OR NEW.instance_id IS DISTINCT FROM OLD.instance_id
        OR NEW.session_id IS DISTINCT FROM OLD.session_id
        OR NEW.principal_id IS DISTINCT FROM OLD.principal_id
        OR NEW.idempotency_key IS DISTINCT FROM OLD.idempotency_key
        OR NEW.merchant_order_no IS DISTINCT FROM OLD.merchant_order_no
        OR NEW.payment_app_id IS DISTINCT FROM OLD.payment_app_id
        OR NEW.payment_merchant_id IS DISTINCT FROM OLD.payment_merchant_id
        OR NEW.original_price_cents IS DISTINCT FROM OLD.original_price_cents
        OR NEW.discount_cents IS DISTINCT FROM OLD.discount_cents
        OR NEW.payable_cents IS DISTINCT FROM OLD.payable_cents
        OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION
            'xiangwan Order identity and amount snapshot are immutable'
            USING ERRCODE = '23514';
    END IF;
    IF NEW.version <> OLD.version + 1 OR NEW.updated_at < OLD.updated_at THEN
        RAISE EXCEPTION 'xiangwan Order version/time must advance'
            USING ERRCODE = '23514';
    END IF;
    IF NOT (
        (
            OLD.payment_status = 'pending'
            AND NEW.payment_status IN (
                'pending', 'unknown', 'paid_confirmed', 'closed_unpaid'
            )
        )
        OR (
            OLD.payment_status = 'unknown'
            AND NEW.payment_status IN (
                'unknown', 'paid_confirmed', 'closed_unpaid'
            )
        )
        OR (
            OLD.payment_status = 'closed_unpaid'
            AND NEW.payment_status IN ('closed_unpaid', 'paid_confirmed')
        )
        OR (
            OLD.payment_status = 'paid_confirmed'
            AND NEW.payment_status = 'paid_confirmed'
        )
    ) THEN
        RAISE EXCEPTION 'xiangwan Order payment transition is invalid'
            USING ERRCODE = '23514';
    END IF;
    IF OLD.actual_paid_cents IS NOT NULL
        AND (
            NEW.actual_paid_cents IS DISTINCT FROM OLD.actual_paid_cents
            OR NEW.wechat_transaction_id IS DISTINCT FROM
                OLD.wechat_transaction_id
            OR NEW.paid_at IS DISTINCT FROM OLD.paid_at
        ) THEN
        RAISE EXCEPTION 'xiangwan Order paid fact is immutable'
            USING ERRCODE = '23514';
    END IF;
    IF NEW.closed_at IS DISTINCT FROM OLD.closed_at
        AND NOT (
            OLD.closed_at IS NULL
            AND NEW.payment_status = 'closed_unpaid'
            AND NEW.closed_at IS NOT NULL
        ) THEN
        RAISE EXCEPTION 'xiangwan Order closed_at is immutable'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION xiangwan_enforce_paid_order_terminal()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    participation VARCHAR(32);
BEGIN
    IF NEW.payment_status <> 'paid_confirmed' THEN
        RETURN NULL;
    END IF;

    SELECT participation_status
      INTO participation
      FROM xiangwan_registrations
     WHERE tenant_id = NEW.tenant_id
       AND id = NEW.registration_id;

    IF participation <> 'confirmed'
        AND NOT EXISTS (
            SELECT 1
              FROM xiangwan_refund_cases
             WHERE tenant_id = NEW.tenant_id
               AND order_id = NEW.id
        ) THEN
        RAISE EXCEPTION
            'xiangwan paid Order requires confirmed Registration or Refund case'
            USING ERRCODE = '23514';
    END IF;
    RETURN NULL;
END;
$$;
