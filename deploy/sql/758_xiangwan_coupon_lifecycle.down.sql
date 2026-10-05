-- 758_xiangwan_coupon_lifecycle.down.sql

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM xiangwan_coupon_entries
        WHERE entry_type <> 'granted'
    ) THEN
        RAISE EXCEPTION
            'cannot roll back Coupon lifecycle while lifecycle facts exist';
    END IF;
END;
$$;

DROP TRIGGER IF EXISTS trg_xw_coupon_entries_validate
    ON xiangwan_coupon_entries;
DROP FUNCTION IF EXISTS xiangwan_validate_coupon_entry();

DROP INDEX IF EXISTS idx_xw_coupon_entries_correction;
DROP INDEX IF EXISTS uq_xw_coupon_entries_order_hold;
DROP INDEX IF EXISTS idx_xw_coupon_entries_order;

ALTER TABLE xiangwan_coupon_entries
    DROP CONSTRAINT IF EXISTS xw_coupon_entries_business_key,
    DROP CONSTRAINT IF EXISTS xw_coupon_entries_sequence_key,
    DROP CONSTRAINT IF EXISTS xw_coupon_entries_source_event_fkey,
    DROP CONSTRAINT IF EXISTS xw_coupon_entries_related_fkey,
    DROP CONSTRAINT IF EXISTS xw_coupon_entries_tenant_coupon_id_key,
    DROP CONSTRAINT IF EXISTS xw_coupon_entries_registration_fkey,
    DROP CONSTRAINT IF EXISTS xw_coupon_entries_order_fkey,
    DROP CONSTRAINT IF EXISTS xw_coupon_entries_reason_length_check,
    DROP CONSTRAINT IF EXISTS xw_coupon_entries_lifecycle_shape_check,
    DROP CONSTRAINT IF EXISTS xw_coupon_entries_sequence_check,
    DROP CONSTRAINT IF EXISTS xw_coupon_entries_type_check,
    DROP COLUMN IF EXISTS reason,
    DROP COLUMN IF EXISTS source_checkin_event_id,
    DROP COLUMN IF EXISTS related_entry_id,
    DROP COLUMN IF EXISTS registration_id,
    DROP COLUMN IF EXISTS order_id,
    DROP COLUMN IF EXISTS entry_sequence,
    ADD CONSTRAINT xw_coupon_entries_type_check
        CHECK (entry_type = 'granted'),
    ADD CONSTRAINT xw_coupon_entries_coupon_grant_key
        UNIQUE (tenant_id, coupon_id, entry_type);

ALTER TABLE xiangwan_orders
    DROP CONSTRAINT IF EXISTS xw_orders_tenant_principal_id_key;

CREATE OR REPLACE FUNCTION xiangwan_validate_coupon_entry()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM xiangwan_coupons AS coupon
        WHERE coupon.tenant_id = NEW.tenant_id
          AND coupon.id = NEW.coupon_id
          AND coupon.principal_id = NEW.principal_id
          AND coupon.grant_business_key = NEW.business_key
          AND coupon.granted_by IS NOT DISTINCT FROM NEW.actor_id
          AND coupon.granted_at = NEW.occurred_at
          AND coupon.created_at = NEW.recorded_at
    ) THEN
        RAISE EXCEPTION 'Coupon grant entry does not match its instrument'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'xw_coupon_entries_instrument_facts';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER trg_xw_coupon_entries_validate
    BEFORE INSERT ON xiangwan_coupon_entries
    FOR EACH ROW EXECUTE FUNCTION xiangwan_validate_coupon_entry();
