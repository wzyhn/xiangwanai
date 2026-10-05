-- 761_xiangwan_coupon_cancellation_policy.down.sql
-- Refuse to discard receipt counts once zero-settlement cancellation used them.

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
          FROM xiangwan_session_cancellation_previews
         WHERE coupon_adjustment_count > 0
    ) OR EXISTS (
        SELECT 1
          FROM xiangwan_instance_cancellation_previews
         WHERE coupon_adjustment_count > 0
    ) OR EXISTS (
        SELECT 1
          FROM xiangwan_session_cancellation_receipts
         WHERE coupon_adjustment_count > 0
    ) OR EXISTS (
        SELECT 1
          FROM xiangwan_instance_cancellation_receipts
         WHERE coupon_adjustment_count > 0
    ) THEN
        RAISE EXCEPTION
            'cannot roll back Coupon cancellation policy with recorded facts';
    END IF;
END;
$$;

DROP TRIGGER IF EXISTS trg_xw_coupon_refund_state_on_registration
    ON xiangwan_registrations;
DROP FUNCTION IF EXISTS
    xiangwan_enforce_coupon_refund_state_from_registration();
DROP TRIGGER IF EXISTS trg_xw_session_cancel_coupon_receipt
    ON xiangwan_session_cancellation_receipts;
DROP TRIGGER IF EXISTS trg_xw_instance_cancel_coupon_receipt
    ON xiangwan_instance_cancellation_receipts;
DROP FUNCTION IF EXISTS xiangwan_validate_coupon_cancellation_receipt();

DROP TRIGGER IF EXISTS trg_xw_coupon_entries_order_state
    ON xiangwan_coupon_entries;
CREATE CONSTRAINT TRIGGER trg_xw_coupon_entries_order_state
    AFTER INSERT ON xiangwan_coupon_entries
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW
    WHEN (NEW.order_id IS NOT NULL)
    EXECUTE FUNCTION xiangwan_enforce_coupon_order_state();

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
    IF full_refund AND redeemed_count = 1 AND adjustment_count <> 1 THEN
        RAISE EXCEPTION 'full refund lacks Coupon policy adjustment'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'xw_coupon_full_refund_adjustment';
    END IF;
END;
$$;

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
                = free_registration_count + paid_refund_registration_count
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
                = free_registration_count + paid_refund_registration_count
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
            AND cancelled_registration_count
                = released_confirmed_count + released_hold_count
            AND closed_pending_order_count <= released_hold_count
            AND refund_case_count <= released_confirmed_count
            AND (
                (refund_case_count = 0 AND requested_refund_cents = 0)
                OR (refund_case_count > 0 AND requested_refund_cents > 0)
            )
        ),
    DROP COLUMN coupon_adjustment_count;

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
            AND cancelled_registration_count
                = released_confirmed_count + released_hold_count
            AND closed_pending_order_count <= released_hold_count
            AND refund_case_count <= released_confirmed_count
            AND (
                (refund_case_count = 0 AND requested_refund_cents = 0)
                OR (refund_case_count > 0 AND requested_refund_cents > 0)
            )
        ),
    DROP COLUMN coupon_adjustment_count;
