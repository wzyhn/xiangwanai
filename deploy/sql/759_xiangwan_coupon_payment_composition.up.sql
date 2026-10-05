-- 759_xiangwan_coupon_payment_composition.up.sql
-- Compose Coupon selection with Order/capacity facts, including local zero
-- settlement. All coordination remains in PostgreSQL; Redis is unused.

ALTER TABLE xiangwan_orders
    DROP CONSTRAINT IF EXISTS xiangwan_orders_payment_status_check,
    DROP CONSTRAINT IF EXISTS xiangwan_orders_amount_snapshot_check,
    DROP CONSTRAINT IF EXISTS xiangwan_orders_state_shape_check,
    ADD CONSTRAINT xiangwan_orders_payment_status_check
        CHECK (
            payment_status IN (
                'pending',
                'unknown',
                'paid_confirmed',
                'settled_zero',
                'closed_unpaid'
            )
        ),
    ADD CONSTRAINT xiangwan_orders_amount_snapshot_check
        CHECK (
            original_price_cents > 0
            AND discount_cents >= 0
            AND discount_cents <= original_price_cents
            AND payable_cents = original_price_cents - discount_cents
            AND payable_cents >= 0
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
                AND payable_cents > 0
                AND actual_paid_cents = payable_cents
                AND BTRIM(COALESCE(wechat_transaction_id, '')) <> ''
                AND paid_at IS NOT NULL
            )
            OR (
                payment_status = 'settled_zero'
                AND payable_cents = 0
                AND discount_cents = original_price_cents
                AND actual_paid_cents IS NULL
                AND wechat_transaction_id IS NULL
                AND paid_at IS NULL
                AND closed_at IS NULL
            )
            OR (
                payment_status = 'closed_unpaid'
                AND payable_cents > 0
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
                'pending',
                'unknown',
                'paid_confirmed',
                'settled_zero',
                'closed_unpaid'
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
        OR (
            OLD.payment_status = 'settled_zero'
            AND NEW.payment_status = 'settled_zero'
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
    IF NEW.payment_status NOT IN ('paid_confirmed', 'settled_zero') THEN
        RETURN NULL;
    END IF;

    SELECT participation_status
      INTO participation
      FROM xiangwan_registrations
     WHERE tenant_id = NEW.tenant_id
       AND id = NEW.registration_id;

    IF NEW.payment_status = 'settled_zero'
        AND participation <> 'confirmed' THEN
        RAISE EXCEPTION
            'xiangwan zero-settled Order requires confirmed Registration'
            USING ERRCODE = '23514';
    END IF;
    IF NEW.payment_status = 'paid_confirmed'
        AND participation <> 'confirmed'
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

DROP TRIGGER IF EXISTS trg_xw_coupon_entries_validate
    ON xiangwan_coupon_entries;
CREATE TRIGGER trg_xw_coupon_entries_validate
    BEFORE INSERT ON xiangwan_coupon_entries
    FOR EACH ROW
    WHEN (NEW.entry_type NOT IN ('released', 'redeemed'))
    EXECUTE FUNCTION xiangwan_validate_coupon_entry();

CREATE OR REPLACE FUNCTION xiangwan_validate_coupon_payment_entry()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    instrument xiangwan_coupons%ROWTYPE;
    related_entry xiangwan_coupon_entries%ROWTYPE;
BEGIN
    SELECT *
      INTO instrument
      FROM xiangwan_coupons
     WHERE tenant_id = NEW.tenant_id
       AND id = NEW.coupon_id
       AND principal_id = NEW.principal_id;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'Coupon ledger instrument identity mismatch'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'xw_coupon_entries_instrument_identity';
    END IF;

    IF NEW.entry_sequence <> (
        SELECT COALESCE(MAX(existing.entry_sequence), 0) + 1
          FROM xiangwan_coupon_entries AS existing
         WHERE existing.tenant_id = NEW.tenant_id
           AND existing.coupon_id = NEW.coupon_id
    ) OR NEW.occurred_at < instrument.granted_at THEN
        RAISE EXCEPTION 'Coupon ledger sequence/time is invalid'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'xw_coupon_entries_sequence_facts';
    END IF;

    IF NOT EXISTS (
        SELECT 1
          FROM xiangwan_orders AS order_record
         WHERE order_record.tenant_id = NEW.tenant_id
           AND order_record.id = NEW.order_id
           AND order_record.principal_id = NEW.principal_id
           AND order_record.registration_id = NEW.registration_id
    ) THEN
        RAISE EXCEPTION 'Coupon ledger Order identity mismatch'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'xw_coupon_entries_order_identity';
    END IF;

    SELECT *
      INTO related_entry
      FROM xiangwan_coupon_entries
     WHERE tenant_id = NEW.tenant_id
       AND coupon_id = NEW.coupon_id
       AND id = NEW.related_entry_id;
    IF NOT FOUND
        OR related_entry.entry_type <> 'held'
        OR related_entry.order_id <> NEW.order_id
        OR related_entry.registration_id <> NEW.registration_id
        OR NEW.occurred_at < related_entry.occurred_at
        OR EXISTS (
            SELECT 1
              FROM xiangwan_coupon_entries AS closing_entry
             WHERE closing_entry.tenant_id = NEW.tenant_id
               AND closing_entry.coupon_id = NEW.coupon_id
               AND closing_entry.related_entry_id = related_entry.id
               AND closing_entry.entry_type IN ('released', 'redeemed')
        ) THEN
        RAISE EXCEPTION 'Coupon hold is not open'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'xw_coupon_entries_open_hold';
    END IF;

    IF NEW.entry_type = 'released' THEN
        IF NEW.business_key <> ('release:' || NEW.order_id::TEXT)
            OR NOT EXISTS (
                SELECT 1
                  FROM xiangwan_orders AS order_record
                  JOIN xiangwan_capacity_holds AS capacity_hold
                    ON capacity_hold.tenant_id = order_record.tenant_id
                   AND capacity_hold.order_id = order_record.id
                 WHERE order_record.tenant_id = NEW.tenant_id
                   AND order_record.id = NEW.order_id
                   AND capacity_hold.hold_status IN ('released', 'expired')
                   AND (
                       order_record.payment_status <> 'paid_confirmed'
                       OR (
                           EXISTS (
                               SELECT 1
                                 FROM xiangwan_refund_cases AS refund_case
                                WHERE refund_case.tenant_id =
                                    order_record.tenant_id
                                  AND refund_case.order_id = order_record.id
                           )
                           AND EXISTS (
                               SELECT 1
                                 FROM xiangwan_registrations AS registration
                                WHERE registration.tenant_id =
                                    order_record.tenant_id
                                  AND registration.id =
                                    order_record.registration_id
                                  AND registration.participation_status =
                                      'cancelled'
                           )
                       )
                   )
            ) THEN
            RAISE EXCEPTION
                'Coupon release requires terminal unpaid/refund capacity hold'
                USING ERRCODE = '23514',
                      CONSTRAINT = 'xw_coupon_entries_release_facts';
        END IF;
        RETURN NEW;
    END IF;

    IF NEW.business_key <> ('redeem:' || NEW.order_id::TEXT)
        OR NOT EXISTS (
            SELECT 1
              FROM xiangwan_orders AS order_record
              JOIN xiangwan_registrations AS registration_record
                ON registration_record.tenant_id = order_record.tenant_id
               AND registration_record.id = order_record.registration_id
              JOIN xiangwan_capacity_holds AS capacity_hold
                ON capacity_hold.tenant_id = order_record.tenant_id
               AND capacity_hold.order_id = order_record.id
             WHERE order_record.tenant_id = NEW.tenant_id
               AND order_record.id = NEW.order_id
               AND registration_record.participation_status = 'confirmed'
               AND capacity_hold.hold_status = 'converted'
               AND (
                   (
                       order_record.payment_status = 'paid_confirmed'
                       AND order_record.paid_at <= NEW.occurred_at
                   )
                   OR order_record.payment_status = 'settled_zero'
               )
        ) THEN
        RAISE EXCEPTION 'Coupon redemption requires settled Order'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'xw_coupon_entries_redemption_facts';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_xw_coupon_payment_entries_validate
    ON xiangwan_coupon_entries;
CREATE TRIGGER trg_xw_coupon_payment_entries_validate
    BEFORE INSERT ON xiangwan_coupon_entries
    FOR EACH ROW
    WHEN (NEW.entry_type IN ('released', 'redeemed'))
    EXECUTE FUNCTION xiangwan_validate_coupon_payment_entry();

CREATE OR REPLACE FUNCTION xiangwan_assert_coupon_order_state(
    target_tenant_id UUID,
    target_order_id UUID
)
RETURNS VOID
LANGUAGE plpgsql
AS $$
DECLARE
    order_record xiangwan_orders%ROWTYPE;
    registration_status VARCHAR(32);
    capacity_status VARCHAR(16);
    held_count BIGINT;
    released_count BIGINT;
    redeemed_count BIGINT;
    selected_face_value BIGINT;
    has_refund BOOLEAN;
BEGIN
    SELECT *
      INTO order_record
      FROM xiangwan_orders
     WHERE tenant_id = target_tenant_id
       AND id = target_order_id;
    IF NOT FOUND THEN
        RETURN;
    END IF;

    SELECT
        COUNT(*) FILTER (WHERE entry.entry_type = 'held'),
        COUNT(*) FILTER (WHERE entry.entry_type = 'released'),
        COUNT(*) FILTER (WHERE entry.entry_type = 'redeemed'),
        MAX(instrument.face_value_cents)
            FILTER (WHERE entry.entry_type = 'held')
      INTO held_count, released_count, redeemed_count, selected_face_value
      FROM xiangwan_coupon_entries AS entry
      JOIN xiangwan_coupons AS instrument
        ON instrument.tenant_id = entry.tenant_id
       AND instrument.id = entry.coupon_id
     WHERE entry.tenant_id = target_tenant_id
       AND entry.order_id = target_order_id;

    SELECT participation_status
      INTO registration_status
      FROM xiangwan_registrations
     WHERE tenant_id = target_tenant_id
       AND id = order_record.registration_id;
    SELECT hold_status
      INTO capacity_status
      FROM xiangwan_capacity_holds
     WHERE tenant_id = target_tenant_id
       AND order_id = target_order_id;
    SELECT EXISTS (
        SELECT 1
          FROM xiangwan_refund_cases
         WHERE tenant_id = target_tenant_id
           AND order_id = target_order_id
    ) INTO has_refund;

    IF order_record.discount_cents = 0 THEN
        IF held_count <> 0 OR order_record.payable_cents = 0 THEN
            RAISE EXCEPTION
                'xiangwan undiscounted Order has Coupon/zero-settlement facts'
                USING ERRCODE = '23514',
                      CONSTRAINT = 'xw_coupon_order_discount_facts';
        END IF;
        RETURN;
    END IF;

    IF held_count <> 1
        OR selected_face_value IS DISTINCT FROM order_record.discount_cents THEN
        RAISE EXCEPTION
            'xiangwan discounted Order lacks one matching Coupon hold'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'xw_coupon_order_discount_facts';
    END IF;

    IF order_record.payment_status IN ('pending', 'unknown')
        AND (
            order_record.payable_cents <= 0
            OR released_count <> 0
            OR redeemed_count <> 0
            OR capacity_status <> 'active'
        ) THEN
        RAISE EXCEPTION 'xiangwan pending discounted Order is not held'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'xw_coupon_order_pending_facts';
    ELSIF order_record.payment_status = 'closed_unpaid'
        AND (
            released_count <> 1
            OR redeemed_count <> 0
            OR capacity_status NOT IN ('released', 'expired')
        ) THEN
        RAISE EXCEPTION 'xiangwan closed discounted Order did not release Coupon'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'xw_coupon_order_closed_facts';
    ELSIF order_record.payment_status = 'settled_zero'
        AND (
            registration_status <> 'confirmed'
            OR capacity_status <> 'converted'
            OR released_count <> 0
            OR redeemed_count <> 1
        ) THEN
        RAISE EXCEPTION 'xiangwan zero-settled Order did not redeem Coupon'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'xw_coupon_order_zero_facts';
    ELSIF order_record.payment_status = 'paid_confirmed'
        AND registration_status = 'confirmed'
        AND (
            capacity_status <> 'converted'
            OR released_count <> 0
            OR redeemed_count <> 1
        ) THEN
        RAISE EXCEPTION 'xiangwan confirmed paid Order did not redeem Coupon'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'xw_coupon_order_paid_facts';
    ELSIF order_record.payment_status = 'paid_confirmed'
        AND registration_status <> 'confirmed'
        AND (
            NOT has_refund
            OR (
                (released_count = 1 AND redeemed_count = 1)
                OR (released_count = 0 AND redeemed_count = 0)
            )
        ) THEN
        RAISE EXCEPTION
            'xiangwan refunded paid Order has ambiguous Coupon disposition'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'xw_coupon_order_refund_facts';
    END IF;
END;
$$;

-- Constraint triggers govern future writes but do not retroactively inspect
-- rows. Refuse activation when legacy discounted Orders cannot be tied to one
-- durable Coupon lifecycle; fabricating that association would corrupt money
-- facts.
DO $$
DECLARE
    existing_order RECORD;
BEGIN
    FOR existing_order IN
        SELECT DISTINCT order_record.tenant_id, order_record.id
          FROM xiangwan_orders AS order_record
         WHERE order_record.discount_cents > 0
            OR EXISTS (
                SELECT 1
                  FROM xiangwan_coupon_entries AS entry
                 WHERE entry.tenant_id = order_record.tenant_id
                   AND entry.order_id = order_record.id
            )
    LOOP
        PERFORM xiangwan_assert_coupon_order_state(
            existing_order.tenant_id,
            existing_order.id
        );
    END LOOP;
END;
$$;

CREATE OR REPLACE FUNCTION xiangwan_enforce_coupon_order_state()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_TABLE_NAME = 'xiangwan_orders' THEN
        PERFORM xiangwan_assert_coupon_order_state(NEW.tenant_id, NEW.id);
    ELSIF NEW.order_id IS NOT NULL THEN
        PERFORM xiangwan_assert_coupon_order_state(
            NEW.tenant_id,
            NEW.order_id
        );
    END IF;
    RETURN NULL;
END;
$$;

DROP TRIGGER IF EXISTS trg_xw_orders_coupon_state
    ON xiangwan_orders;
CREATE CONSTRAINT TRIGGER trg_xw_orders_coupon_state
    AFTER INSERT OR UPDATE OF payment_status ON xiangwan_orders
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION xiangwan_enforce_coupon_order_state();

DROP TRIGGER IF EXISTS trg_xw_coupon_entries_order_state
    ON xiangwan_coupon_entries;
CREATE CONSTRAINT TRIGGER trg_xw_coupon_entries_order_state
    AFTER INSERT ON xiangwan_coupon_entries
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW
    WHEN (NEW.order_id IS NOT NULL)
    EXECUTE FUNCTION xiangwan_enforce_coupon_order_state();
