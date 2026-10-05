-- 760_xiangwan_coupon_refund_policy.up.sql
-- Record the signed CONFIG-COUPON-REFUND-POLICY outcome when a redeemed
-- Coupon reaches a full cash refund. PostgreSQL remains the only ledger.

ALTER TABLE xiangwan_coupon_entries
    DROP CONSTRAINT xw_coupon_entries_type_check,
    DROP CONSTRAINT xw_coupon_entries_lifecycle_shape_check,
    ADD COLUMN refund_case_id UUID,
    ADD COLUMN refund_policy_version VARCHAR(64),
    ADD CONSTRAINT xw_coupon_entries_type_check
        CHECK (
            entry_type IN (
                'granted',
                'held',
                'released',
                'redeemed',
                'restored',
                'forfeited',
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
                AND refund_case_id IS NULL
                AND source_checkin_event_id IS NULL
                AND reason IS NULL
                AND refund_policy_version IS NULL
            )
            OR (
                entry_type = 'held'
                AND entry_sequence > 1
                AND order_id IS NOT NULL
                AND registration_id IS NOT NULL
                AND related_entry_id IS NULL
                AND refund_case_id IS NULL
                AND source_checkin_event_id IS NULL
                AND actor_id IS NULL
                AND reason IS NULL
                AND refund_policy_version IS NULL
            )
            OR (
                entry_type = 'released'
                AND entry_sequence > 1
                AND order_id IS NOT NULL
                AND registration_id IS NOT NULL
                AND related_entry_id IS NOT NULL
                AND refund_case_id IS NULL
                AND source_checkin_event_id IS NULL
                AND actor_id IS NULL
                AND BTRIM(COALESCE(reason, '')) <> ''
                AND reason = BTRIM(reason)
                AND refund_policy_version IS NULL
            )
            OR (
                entry_type = 'redeemed'
                AND entry_sequence > 1
                AND order_id IS NOT NULL
                AND registration_id IS NOT NULL
                AND related_entry_id IS NOT NULL
                AND refund_case_id IS NULL
                AND source_checkin_event_id IS NULL
                AND actor_id IS NULL
                AND reason IS NULL
                AND refund_policy_version IS NULL
            )
            OR (
                entry_type IN ('restored', 'forfeited')
                AND entry_sequence > 1
                AND order_id IS NOT NULL
                AND registration_id IS NOT NULL
                AND related_entry_id IS NOT NULL
                AND source_checkin_event_id IS NULL
                AND actor_id IS NOT NULL
                AND BTRIM(COALESCE(reason, '')) <> ''
                AND reason = BTRIM(reason)
                AND refund_policy_version ~
                    '^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$'
            )
            OR (
                entry_type IN ('invalidated', 'correction_required')
                AND entry_sequence > 1
                AND order_id IS NULL
                AND registration_id IS NULL
                AND related_entry_id IS NOT NULL
                AND refund_case_id IS NULL
                AND source_checkin_event_id IS NOT NULL
                AND actor_id IS NOT NULL
                AND BTRIM(COALESCE(reason, '')) <> ''
                AND reason = BTRIM(reason)
                AND refund_policy_version IS NULL
            )
        ),
    ADD CONSTRAINT xw_coupon_entries_refund_case_fkey
        FOREIGN KEY (tenant_id, refund_case_id)
        REFERENCES xiangwan_refund_cases (tenant_id, id);

CREATE UNIQUE INDEX uq_xw_coupon_entries_refund_adjustment
    ON xiangwan_coupon_entries (
        tenant_id, coupon_id, related_entry_id
    )
    WHERE entry_type IN ('restored', 'forfeited');

CREATE INDEX idx_xw_coupon_entries_refund_case
    ON xiangwan_coupon_entries (
        tenant_id, refund_case_id, occurred_at, id
    )
    WHERE refund_case_id IS NOT NULL;

DROP TRIGGER IF EXISTS trg_xw_coupon_entries_validate
    ON xiangwan_coupon_entries;
CREATE TRIGGER trg_xw_coupon_entries_validate
    BEFORE INSERT ON xiangwan_coupon_entries
    FOR EACH ROW
    WHEN (
        NEW.entry_type NOT IN (
            'held', 'released', 'redeemed', 'restored', 'forfeited'
        )
    )
    EXECUTE FUNCTION xiangwan_validate_coupon_entry();

CREATE OR REPLACE FUNCTION xiangwan_validate_coupon_reusable_entry()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    instrument xiangwan_coupons%ROWTYPE;
    related_entry xiangwan_coupon_entries%ROWTYPE;
    held_count BIGINT;
    closed_hold_count BIGINT;
    redeemed_count BIGINT;
    restored_count BIGINT;
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

    IF NEW.entry_type = 'held' THEN
        SELECT
            COUNT(*) FILTER (WHERE entry_type = 'held'),
            COUNT(*) FILTER (
                WHERE entry_type IN ('released', 'redeemed')
            ),
            COUNT(*) FILTER (WHERE entry_type = 'redeemed'),
            COUNT(*) FILTER (WHERE entry_type = 'restored')
          INTO held_count, closed_hold_count, redeemed_count, restored_count
          FROM xiangwan_coupon_entries
         WHERE tenant_id = NEW.tenant_id
           AND coupon_id = NEW.coupon_id;

        IF NEW.business_key <> ('hold:' || NEW.order_id::TEXT)
            OR NEW.occurred_at < instrument.valid_from
            OR NEW.occurred_at >= instrument.expires_at
            OR held_count <> closed_hold_count
            OR redeemed_count <> restored_count
            OR EXISTS (
                SELECT 1
                  FROM xiangwan_coupon_entries AS blocking_entry
                 WHERE blocking_entry.tenant_id = NEW.tenant_id
                   AND blocking_entry.coupon_id = NEW.coupon_id
                   AND blocking_entry.entry_type IN (
                       'invalidated', 'forfeited', 'correction_required'
                   )
            )
            OR NOT EXISTS (
                SELECT 1
                  FROM xiangwan_orders AS order_record
                  JOIN xiangwan_capacity_holds AS capacity_hold
                    ON capacity_hold.tenant_id = order_record.tenant_id
                   AND capacity_hold.order_id = order_record.id
                  JOIN xiangwan_activity_instances AS activity_instance
                    ON activity_instance.tenant_id = order_record.tenant_id
                   AND activity_instance.id = order_record.instance_id
                 WHERE order_record.tenant_id = NEW.tenant_id
                   AND order_record.id = NEW.order_id
                   AND order_record.registration_id = NEW.registration_id
                   AND order_record.principal_id = NEW.principal_id
                   AND order_record.payment_status IN ('pending', 'unknown')
                   AND order_record.original_price_cents >=
                       instrument.minimum_order_cents
                   AND order_record.discount_cents =
                       instrument.face_value_cents
                   AND capacity_hold.hold_status = 'active'
                   AND capacity_hold.expires_at <= instrument.expires_at
                   AND (
                       (
                           instrument.scope_type = 'activity_type'
                           AND activity_instance.activity_type =
                               instrument.scope_activity_type
                       )
                       OR (
                           instrument.scope_type = 'series'
                           AND order_record.series_id =
                               instrument.scope_series_id
                       )
                   )
            ) THEN
            RAISE EXCEPTION 'Coupon cannot be held for this Order'
                USING ERRCODE = '23514',
                      CONSTRAINT = 'xw_coupon_entries_hold_facts';
        END IF;
        RETURN NEW;
    END IF;

    SELECT *
      INTO related_entry
      FROM xiangwan_coupon_entries
     WHERE tenant_id = NEW.tenant_id
       AND coupon_id = NEW.coupon_id
       AND id = NEW.related_entry_id;
    IF NOT FOUND
        OR related_entry.entry_type <> 'redeemed'
        OR related_entry.order_id <> NEW.order_id
        OR related_entry.registration_id <> NEW.registration_id
        OR NEW.business_key <>
            ('refund-policy:' || related_entry.id::TEXT)
        OR NEW.occurred_at < related_entry.occurred_at
        OR EXISTS (
            SELECT 1
              FROM xiangwan_coupon_entries AS existing_adjustment
             WHERE existing_adjustment.tenant_id = NEW.tenant_id
               AND existing_adjustment.coupon_id = NEW.coupon_id
               AND existing_adjustment.related_entry_id = related_entry.id
               AND existing_adjustment.entry_type IN (
                   'restored', 'forfeited'
               )
        ) THEN
        RAISE EXCEPTION 'Coupon refund adjustment source is invalid'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'xw_coupon_entries_refund_source';
    END IF;

    IF NEW.refund_case_id IS NOT NULL THEN
        IF NOT EXISTS (
            SELECT 1
              FROM xiangwan_refund_cases AS refund_case
              JOIN xiangwan_orders AS order_record
                ON order_record.tenant_id = refund_case.tenant_id
               AND order_record.id = refund_case.order_id
              JOIN xiangwan_registrations AS registration_record
                ON registration_record.tenant_id = order_record.tenant_id
               AND registration_record.id = order_record.registration_id
             WHERE refund_case.tenant_id = NEW.tenant_id
               AND refund_case.id = NEW.refund_case_id
               AND refund_case.order_id = NEW.order_id
               AND refund_case.registration_id = NEW.registration_id
               AND refund_case.principal_id = NEW.principal_id
               AND refund_case.refund_status = 'refunded'
               AND refund_case.successful_refund_cents =
                   order_record.actual_paid_cents
               AND order_record.payment_status = 'paid_confirmed'
               AND registration_record.participation_status = 'cancelled'
               AND refund_case.updated_at <= NEW.occurred_at
        ) THEN
            RAISE EXCEPTION
                'Coupon refund adjustment requires completed full refund'
                USING ERRCODE = '23514',
                      CONSTRAINT = 'xw_coupon_entries_full_refund';
        END IF;
    ELSIF NOT EXISTS (
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
           AND order_record.payment_status = 'settled_zero'
           AND registration_record.participation_status = 'cancelled'
           AND capacity_hold.hold_status = 'converted'
    ) THEN
        RAISE EXCEPTION
            'Coupon adjustment without Refund requires cancelled zero settlement'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'xw_coupon_entries_zero_refund';
    END IF;

    IF NEW.entry_type = 'restored' AND EXISTS (
        SELECT 1
          FROM xiangwan_coupon_entries AS correction
         WHERE correction.tenant_id = NEW.tenant_id
           AND correction.coupon_id = NEW.coupon_id
           AND correction.entry_type = 'correction_required'
    ) THEN
        RAISE EXCEPTION 'Coupon requiring correction cannot be restored'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'xw_coupon_entries_restore_correction';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_xw_coupon_reusable_entries_validate
    ON xiangwan_coupon_entries;
CREATE TRIGGER trg_xw_coupon_reusable_entries_validate
    BEFORE INSERT ON xiangwan_coupon_entries
    FOR EACH ROW
    WHEN (NEW.entry_type IN ('held', 'restored', 'forfeited'))
    EXECUTE FUNCTION xiangwan_validate_coupon_reusable_entry();

CREATE OR REPLACE FUNCTION xiangwan_validate_coupon_refund_balance()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.grant_kind <> 'manual_replenishment' THEN
        RETURN NEW;
    END IF;
    IF EXISTS (
        SELECT 1
          FROM xiangwan_coupons AS remaining_coupon
         WHERE remaining_coupon.tenant_id = NEW.tenant_id
           AND remaining_coupon.principal_id = NEW.principal_id
           AND remaining_coupon.benefit_type = NEW.benefit_type
           AND (
               remaining_coupon.grant_kind <> NEW.grant_kind
               OR remaining_coupon.grant_business_key <>
                   NEW.grant_business_key
           )
           AND NOT EXISTS (
               SELECT 1
                 FROM xiangwan_coupon_entries AS blocking_entry
                WHERE blocking_entry.tenant_id =
                    remaining_coupon.tenant_id
                  AND blocking_entry.coupon_id = remaining_coupon.id
                  AND blocking_entry.entry_type IN (
                      'invalidated', 'forfeited', 'correction_required'
                  )
           )
           AND (
               (
                   remaining_coupon.expires_at > NEW.granted_at
                   AND (
                       SELECT COUNT(*)
                         FROM xiangwan_coupon_entries AS redeemed_entry
                        WHERE redeemed_entry.tenant_id =
                            remaining_coupon.tenant_id
                          AND redeemed_entry.coupon_id =
                            remaining_coupon.id
                          AND redeemed_entry.entry_type = 'redeemed'
                   ) = (
                       SELECT COUNT(*)
                         FROM xiangwan_coupon_entries AS restored_entry
                        WHERE restored_entry.tenant_id =
                            remaining_coupon.tenant_id
                          AND restored_entry.coupon_id =
                            remaining_coupon.id
                          AND restored_entry.entry_type = 'restored'
                   )
               )
               OR (
                   SELECT COUNT(*)
                     FROM xiangwan_coupon_entries AS held_entry
                    WHERE held_entry.tenant_id = remaining_coupon.tenant_id
                      AND held_entry.coupon_id = remaining_coupon.id
                      AND held_entry.entry_type = 'held'
               ) > (
                   SELECT COUNT(*)
                     FROM xiangwan_coupon_entries AS closing_entry
                    WHERE closing_entry.tenant_id =
                        remaining_coupon.tenant_id
                      AND closing_entry.coupon_id = remaining_coupon.id
                      AND closing_entry.entry_type IN (
                          'released', 'redeemed'
                      )
               )
           )
    ) THEN
        RAISE EXCEPTION
            'manual Coupon grant requires zero reusable balance'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'xw_coupons_manual_refund_balance';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_xw_coupon_manual_refund_balance
    ON xiangwan_coupons;
CREATE TRIGGER trg_xw_coupon_manual_refund_balance
    BEFORE INSERT ON xiangwan_coupons
    FOR EACH ROW
    WHEN (NEW.grant_kind = 'manual_replenishment')
    EXECUTE FUNCTION xiangwan_validate_coupon_refund_balance();

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

CREATE OR REPLACE FUNCTION xiangwan_enforce_coupon_refund_state()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    PERFORM xiangwan_assert_coupon_refund_state(
        NEW.tenant_id,
        NEW.order_id
    );
    RETURN NULL;
END;
$$;

DROP TRIGGER IF EXISTS trg_xw_coupon_refund_state_on_case
    ON xiangwan_refund_cases;
CREATE CONSTRAINT TRIGGER trg_xw_coupon_refund_state_on_case
    AFTER UPDATE OF refund_status, successful_refund_cents
    ON xiangwan_refund_cases
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW
    EXECUTE FUNCTION xiangwan_enforce_coupon_refund_state();

DROP TRIGGER IF EXISTS trg_xw_coupon_refund_state_on_entry
    ON xiangwan_coupon_entries;
CREATE CONSTRAINT TRIGGER trg_xw_coupon_refund_state_on_entry
    AFTER INSERT ON xiangwan_coupon_entries
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW
    WHEN (NEW.entry_type IN ('restored', 'forfeited'))
    EXECUTE FUNCTION xiangwan_enforce_coupon_refund_state();

DO $$
DECLARE
    existing_refund RECORD;
BEGIN
    FOR existing_refund IN
        SELECT refund_case.tenant_id, refund_case.order_id
          FROM xiangwan_refund_cases AS refund_case
          JOIN xiangwan_orders AS order_record
            ON order_record.tenant_id = refund_case.tenant_id
           AND order_record.id = refund_case.order_id
         WHERE order_record.discount_cents > 0
           AND refund_case.refund_status = 'refunded'
           AND refund_case.successful_refund_cents =
               order_record.actual_paid_cents
    LOOP
        PERFORM xiangwan_assert_coupon_refund_state(
            existing_refund.tenant_id,
            existing_refund.order_id
        );
    END LOOP;
END;
$$;
