-- 761_xiangwan_coupon_cancellation_policy.up.sql
-- Converge cancelled zero-settled Orders with one versioned Coupon policy
-- decision, and carry the exact adjustment count through cancellation receipts.

ALTER TABLE xiangwan_session_cancellation_receipts
    ADD COLUMN coupon_adjustment_count INTEGER NOT NULL DEFAULT 0;

ALTER TABLE xiangwan_instance_cancellation_receipts
    ADD COLUMN coupon_adjustment_count INTEGER NOT NULL DEFAULT 0;

ALTER TABLE xiangwan_session_cancellation_previews
    DROP CONSTRAINT xiangwan_session_cancellation_previews_counts_check,
    ADD CONSTRAINT xiangwan_session_cancellation_previews_counts_check
        CHECK (
            expected_session_version >= 1
            AND cancelled_registration_count >= 0
            AND confirmed_registration_count >= 0
            AND active_hold_count >= 0
            AND free_registration_count >= 0
            AND paid_refund_registration_count >= 0
            AND pending_order_count >= 0
            AND unknown_payment_count >= 0
            AND refund_case_count >= 0
            AND requested_refund_cents >= 0
            AND coupon_adjustment_count >= 0
            AND cancelled_registration_count
                = confirmed_registration_count + active_hold_count
            AND confirmed_registration_count
                = free_registration_count
                    + paid_refund_registration_count
                    + coupon_adjustment_count
            AND active_hold_count
                = pending_order_count + unknown_payment_count
            AND refund_case_count = paid_refund_registration_count
            AND (
                (refund_case_count = 0 AND requested_refund_cents = 0)
                OR (refund_case_count > 0 AND requested_refund_cents > 0)
            )
        );

ALTER TABLE xiangwan_instance_cancellation_previews
    DROP CONSTRAINT xiangwan_instance_cancellation_previews_counts_check,
    ADD CONSTRAINT xiangwan_instance_cancellation_previews_counts_check
        CHECK (
            expected_instance_version >= 1
            AND session_count >= 1
            AND target_session_count >= 1
            AND already_cancelled_session_count >= 0
            AND session_count
                = target_session_count + already_cancelled_session_count
            AND cancelled_registration_count >= 0
            AND confirmed_registration_count >= 0
            AND active_hold_count >= 0
            AND free_registration_count >= 0
            AND paid_refund_registration_count >= 0
            AND pending_order_count >= 0
            AND unknown_payment_count >= 0
            AND refund_case_count >= 0
            AND requested_refund_cents >= 0
            AND coupon_adjustment_count >= 0
            AND cancelled_registration_count
                = confirmed_registration_count + active_hold_count
            AND confirmed_registration_count
                = free_registration_count
                    + paid_refund_registration_count
                    + coupon_adjustment_count
            AND active_hold_count
                = pending_order_count + unknown_payment_count
            AND refund_case_count = paid_refund_registration_count
            AND (
                (refund_case_count = 0 AND requested_refund_cents = 0)
                OR (refund_case_count > 0 AND requested_refund_cents > 0)
            )
        );

ALTER TABLE xiangwan_session_cancellation_receipts
    DROP CONSTRAINT xiangwan_session_cancellation_receipts_counts_check,
    ADD CONSTRAINT xiangwan_session_cancellation_receipts_counts_check
        CHECK (
            cancelled_registration_count >= 0
            AND released_confirmed_count >= 0
            AND released_hold_count >= 0
            AND closed_pending_order_count >= 0
            AND refund_case_count >= 0
            AND requested_refund_cents >= 0
            AND coupon_adjustment_count >= 0
            AND cancelled_registration_count
                = released_confirmed_count + released_hold_count
            AND closed_pending_order_count <= released_hold_count
            AND refund_case_count + coupon_adjustment_count
                <= released_confirmed_count
            AND (
                (refund_case_count = 0 AND requested_refund_cents = 0)
                OR (refund_case_count > 0 AND requested_refund_cents > 0)
            )
        );

ALTER TABLE xiangwan_instance_cancellation_receipts
    DROP CONSTRAINT xiangwan_instance_cancellation_receipts_counts_check,
    ADD CONSTRAINT xiangwan_instance_cancellation_receipts_counts_check
        CHECK (
            session_count >= 1
            AND newly_cancelled_session_count >= 1
            AND already_cancelled_session_count >= 0
            AND session_count
                = newly_cancelled_session_count
                    + already_cancelled_session_count
            AND cancelled_registration_count >= 0
            AND released_confirmed_count >= 0
            AND released_hold_count >= 0
            AND closed_pending_order_count >= 0
            AND refund_case_count >= 0
            AND requested_refund_cents >= 0
            AND coupon_adjustment_count >= 0
            AND cancelled_registration_count
                = released_confirmed_count + released_hold_count
            AND closed_pending_order_count <= released_hold_count
            AND refund_case_count + coupon_adjustment_count
                <= released_confirmed_count
            AND (
                (refund_case_count = 0 AND requested_refund_cents = 0)
                OR (refund_case_count > 0 AND requested_refund_cents > 0)
            )
        );

-- The original Order-state trigger predates terminal zero-settlement
-- cancellation and therefore insists that every settled_zero Registration is
-- still confirmed. Adjustment inserts are governed by the stricter 720/721
-- policy triggers instead.
DROP TRIGGER IF EXISTS trg_xw_coupon_entries_order_state
    ON xiangwan_coupon_entries;
CREATE CONSTRAINT TRIGGER trg_xw_coupon_entries_order_state
    AFTER INSERT ON xiangwan_coupon_entries
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW
    WHEN (
        NEW.order_id IS NOT NULL
        AND NEW.entry_type NOT IN ('restored', 'forfeited')
    )
    EXECUTE FUNCTION xiangwan_enforce_coupon_order_state();

CREATE OR REPLACE FUNCTION xiangwan_validate_coupon_cancellation_receipt()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    adjustment_count BIGINT;
BEGIN
    IF TG_TABLE_NAME = 'xiangwan_session_cancellation_receipts' THEN
        IF NOT EXISTS (
            SELECT 1
              FROM xiangwan_session_cancellation_previews AS preview
             WHERE preview.tenant_id = NEW.tenant_id
               AND preview.id = NEW.preview_id
               AND preview.coupon_adjustment_count =
                   NEW.coupon_adjustment_count
        ) THEN
            RAISE EXCEPTION
                'Session cancellation Coupon count differs from preview'
                USING ERRCODE = '23514',
                      CONSTRAINT = 'xw_session_cancel_coupon_preview';
        END IF;

        SELECT COUNT(*)
          INTO adjustment_count
          FROM xiangwan_coupon_entries AS adjustment
          JOIN xiangwan_orders AS order_record
            ON order_record.tenant_id = adjustment.tenant_id
           AND order_record.id = adjustment.order_id
          JOIN xiangwan_registrations AS registration_record
            ON registration_record.tenant_id = order_record.tenant_id
           AND registration_record.id = order_record.registration_id
          JOIN xiangwan_session_cancellation_previews AS preview
            ON preview.tenant_id = NEW.tenant_id
           AND preview.id = NEW.preview_id
         WHERE adjustment.tenant_id = NEW.tenant_id
           AND order_record.session_id = NEW.session_id
           AND order_record.payment_status = 'settled_zero'
           AND registration_record.participation_status = 'cancelled'
           AND registration_record.cancelled_at = NEW.cancelled_at
           AND adjustment.entry_type IN ('restored', 'forfeited')
           AND adjustment.refund_case_id IS NULL
           AND adjustment.actor_id = NEW.cancelled_by
           AND adjustment.occurred_at = NEW.cancelled_at
           AND adjustment.reason = CASE
               WHEN preview.parent_instance_preview_id IS NULL
                   THEN 'session_cancelled zero-settled Coupon'
               ELSE 'instance_cancelled zero-settled Coupon'
           END;
        IF adjustment_count <> NEW.coupon_adjustment_count THEN
            RAISE EXCEPTION
                'Session cancellation Coupon count differs from ledger'
                USING ERRCODE = '23514',
                      CONSTRAINT = 'xw_session_cancel_coupon_ledger';
        END IF;
        RETURN NEW;
    END IF;

    IF NOT EXISTS (
        SELECT 1
          FROM xiangwan_instance_cancellation_previews AS preview
         WHERE preview.tenant_id = NEW.tenant_id
           AND preview.id = NEW.preview_id
           AND preview.coupon_adjustment_count = NEW.coupon_adjustment_count
    ) OR COALESCE((
        SELECT SUM(receipt.coupon_adjustment_count)
          FROM xiangwan_session_cancellation_receipts AS receipt
          JOIN xiangwan_session_cancellation_previews AS child_preview
            ON child_preview.tenant_id = receipt.tenant_id
           AND child_preview.id = receipt.preview_id
         WHERE receipt.tenant_id = NEW.tenant_id
           AND receipt.instance_id = NEW.instance_id
           AND child_preview.parent_instance_preview_id = NEW.preview_id
    ), 0) <> NEW.coupon_adjustment_count THEN
        RAISE EXCEPTION
            'Instance cancellation Coupon count does not converge'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'xw_instance_cancel_coupon_ledger';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_xw_session_cancel_coupon_receipt
    ON xiangwan_session_cancellation_receipts;
CREATE TRIGGER trg_xw_session_cancel_coupon_receipt
    BEFORE INSERT ON xiangwan_session_cancellation_receipts
    FOR EACH ROW
    EXECUTE FUNCTION xiangwan_validate_coupon_cancellation_receipt();

DROP TRIGGER IF EXISTS trg_xw_instance_cancel_coupon_receipt
    ON xiangwan_instance_cancellation_receipts;
CREATE TRIGGER trg_xw_instance_cancel_coupon_receipt
    BEFORE INSERT ON xiangwan_instance_cancellation_receipts
    FOR EACH ROW
    EXECUTE FUNCTION xiangwan_validate_coupon_cancellation_receipt();

CREATE OR REPLACE FUNCTION xiangwan_assert_coupon_refund_state(
    target_tenant_id UUID,
    target_order_id UUID
)
RETURNS VOID
LANGUAGE plpgsql
AS $$
DECLARE
    order_record xiangwan_orders%ROWTYPE;
    full_refund BOOLEAN;
    zero_cancelled BOOLEAN;
    redeemed_count BIGINT;
    adjustment_count BIGINT;
BEGIN
    SELECT *
      INTO order_record
      FROM xiangwan_orders
     WHERE tenant_id = target_tenant_id
       AND id = target_order_id;
    IF NOT FOUND OR order_record.discount_cents = 0 THEN
        RETURN;
    END IF;

    SELECT EXISTS (
        SELECT 1
          FROM xiangwan_refund_cases AS refund_case
         WHERE refund_case.tenant_id = target_tenant_id
           AND refund_case.order_id = target_order_id
           AND refund_case.refund_status = 'refunded'
           AND refund_case.successful_refund_cents =
               order_record.actual_paid_cents
    ) INTO full_refund;

    SELECT order_record.payment_status = 'settled_zero'
        AND EXISTS (
            SELECT 1
              FROM xiangwan_registrations AS registration_record
             WHERE registration_record.tenant_id = target_tenant_id
               AND registration_record.id = order_record.registration_id
               AND registration_record.participation_status = 'cancelled'
        )
      INTO zero_cancelled;

    SELECT
        COUNT(*) FILTER (WHERE entry_type = 'redeemed'),
        COUNT(*) FILTER (WHERE entry_type IN ('restored', 'forfeited'))
      INTO redeemed_count, adjustment_count
      FROM xiangwan_coupon_entries
     WHERE tenant_id = target_tenant_id
       AND order_id = target_order_id;

    IF adjustment_count > 0
        AND (redeemed_count <> 1 OR adjustment_count <> 1) THEN
        RAISE EXCEPTION 'Coupon refund adjustment is ambiguous'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'xw_coupon_refund_adjustment_facts';
    END IF;
    IF (full_refund OR zero_cancelled)
        AND (redeemed_count <> 1 OR adjustment_count <> 1) THEN
        RAISE EXCEPTION 'terminal Coupon cancellation lacks policy adjustment'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'xw_coupon_terminal_adjustment';
    END IF;
END;
$$;

DROP TRIGGER IF EXISTS trg_xw_coupon_refund_state_on_registration
    ON xiangwan_registrations;
CREATE OR REPLACE FUNCTION
    xiangwan_enforce_coupon_refund_state_from_registration()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    target_order_id UUID;
BEGIN
    SELECT id
      INTO target_order_id
      FROM xiangwan_orders
     WHERE tenant_id = NEW.tenant_id
       AND registration_id = NEW.id;
    IF FOUND THEN
        PERFORM xiangwan_assert_coupon_refund_state(
            NEW.tenant_id,
            target_order_id
        );
    END IF;
    RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER trg_xw_coupon_refund_state_on_registration
    AFTER UPDATE OF participation_status ON xiangwan_registrations
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW
    EXECUTE FUNCTION
        xiangwan_enforce_coupon_refund_state_from_registration();

DO $$
DECLARE
    existing_order RECORD;
BEGIN
    FOR existing_order IN
        SELECT order_record.tenant_id, order_record.id
          FROM xiangwan_orders AS order_record
          JOIN xiangwan_registrations AS registration_record
            ON registration_record.tenant_id = order_record.tenant_id
           AND registration_record.id = order_record.registration_id
         WHERE order_record.discount_cents > 0
           AND (
               (
                   order_record.payment_status = 'settled_zero'
                   AND registration_record.participation_status = 'cancelled'
               )
               OR EXISTS (
                   SELECT 1
                     FROM xiangwan_refund_cases AS refund_case
                    WHERE refund_case.tenant_id = order_record.tenant_id
                      AND refund_case.order_id = order_record.id
                      AND refund_case.refund_status = 'refunded'
                      AND refund_case.successful_refund_cents =
                          order_record.actual_paid_cents
               )
           )
    LOOP
        PERFORM xiangwan_assert_coupon_refund_state(
            existing_order.tenant_id,
            existing_order.id
        );
    END LOOP;
END;
$$;
