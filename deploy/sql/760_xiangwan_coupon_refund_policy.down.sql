-- 760_xiangwan_coupon_refund_policy.down.sql
-- Refuse rollback once a policy decision has entered the immutable ledger.

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
          FROM xiangwan_coupon_entries
         WHERE entry_type IN ('restored', 'forfeited')
    ) THEN
        RAISE EXCEPTION
            'cannot roll back Coupon refund policy with adjustment facts';
    END IF;
END;
$$;

DROP TRIGGER IF EXISTS trg_xw_coupon_refund_state_on_case
    ON xiangwan_refund_cases;
DROP TRIGGER IF EXISTS trg_xw_coupon_refund_state_on_entry
    ON xiangwan_coupon_entries;
DROP TRIGGER IF EXISTS trg_xw_coupon_reusable_entries_validate
    ON xiangwan_coupon_entries;
DROP TRIGGER IF EXISTS trg_xw_coupon_manual_refund_balance
    ON xiangwan_coupons;

DROP FUNCTION IF EXISTS xiangwan_enforce_coupon_refund_state();
DROP FUNCTION IF EXISTS xiangwan_assert_coupon_refund_state(UUID, UUID);
DROP FUNCTION IF EXISTS xiangwan_validate_coupon_reusable_entry();
DROP FUNCTION IF EXISTS xiangwan_validate_coupon_refund_balance();

DROP INDEX IF EXISTS idx_xw_coupon_entries_refund_case;
DROP INDEX IF EXISTS uq_xw_coupon_entries_refund_adjustment;

DROP TRIGGER IF EXISTS trg_xw_coupon_entries_validate
    ON xiangwan_coupon_entries;
CREATE TRIGGER trg_xw_coupon_entries_validate
    BEFORE INSERT ON xiangwan_coupon_entries
    FOR EACH ROW
    WHEN (NEW.entry_type NOT IN ('released', 'redeemed'))
    EXECUTE FUNCTION xiangwan_validate_coupon_entry();

ALTER TABLE xiangwan_coupon_entries
    DROP CONSTRAINT xw_coupon_entries_refund_case_fkey,
    DROP CONSTRAINT xw_coupon_entries_type_check,
    DROP CONSTRAINT xw_coupon_entries_lifecycle_shape_check,
    ADD CONSTRAINT xw_coupon_entries_type_check
        CHECK (
            entry_type IN (
                'granted',
                'held',
                'released',
                'redeemed',
                'invalidated',
                'correction_required'
            )
        ),
    ADD CONSTRAINT xw_coupon_entries_lifecycle_shape_check
        CHECK (
            (
                entry_type = 'granted'
                AND entry_sequence = 1
                AND order_id IS NULL
                AND registration_id IS NULL
                AND related_entry_id IS NULL
                AND source_checkin_event_id IS NULL
                AND reason IS NULL
            )
            OR (
                entry_type = 'held'
                AND entry_sequence > 1
                AND order_id IS NOT NULL
                AND registration_id IS NOT NULL
                AND related_entry_id IS NULL
                AND source_checkin_event_id IS NULL
                AND actor_id IS NULL
                AND reason IS NULL
            )
            OR (
                entry_type = 'released'
                AND entry_sequence > 1
                AND order_id IS NOT NULL
                AND registration_id IS NOT NULL
                AND related_entry_id IS NOT NULL
                AND source_checkin_event_id IS NULL
                AND actor_id IS NULL
                AND BTRIM(COALESCE(reason, '')) <> ''
                AND reason = BTRIM(reason)
            )
            OR (
                entry_type = 'redeemed'
                AND entry_sequence > 1
                AND order_id IS NOT NULL
                AND registration_id IS NOT NULL
                AND related_entry_id IS NOT NULL
                AND source_checkin_event_id IS NULL
                AND actor_id IS NULL
                AND reason IS NULL
            )
            OR (
                entry_type IN ('invalidated', 'correction_required')
                AND entry_sequence > 1
                AND order_id IS NULL
                AND registration_id IS NULL
                AND related_entry_id IS NOT NULL
                AND source_checkin_event_id IS NOT NULL
                AND actor_id IS NOT NULL
                AND BTRIM(COALESCE(reason, '')) <> ''
                AND reason = BTRIM(reason)
            )
        ),
    DROP COLUMN refund_policy_version,
    DROP COLUMN refund_case_id;
