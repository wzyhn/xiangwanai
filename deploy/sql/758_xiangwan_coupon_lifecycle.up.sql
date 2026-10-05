-- 758_xiangwan_coupon_lifecycle.up.sql
-- Extend the immutable Coupon ledger with ordered hold, release, redemption,
-- and Checkin-revocation correction facts.

ALTER TABLE xiangwan_orders
    ADD CONSTRAINT xw_orders_tenant_principal_id_key
        UNIQUE (tenant_id, principal_id, id);

ALTER TABLE xiangwan_coupon_entries
    DROP CONSTRAINT xw_coupon_entries_type_check,
    DROP CONSTRAINT xw_coupon_entries_coupon_grant_key,
    ADD COLUMN entry_sequence BIGINT NOT NULL DEFAULT 1,
    ADD COLUMN order_id UUID,
    ADD COLUMN registration_id UUID,
    ADD COLUMN related_entry_id UUID,
    ADD COLUMN source_checkin_event_id UUID,
    ADD COLUMN reason VARCHAR(500),
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
    ADD CONSTRAINT xw_coupon_entries_sequence_check
        CHECK (entry_sequence >= 1),
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
    ADD CONSTRAINT xw_coupon_entries_reason_length_check
        CHECK (reason IS NULL OR CHAR_LENGTH(reason) <= 500),
    ADD CONSTRAINT xw_coupon_entries_order_fkey
        FOREIGN KEY (tenant_id, principal_id, order_id)
        REFERENCES xiangwan_orders (tenant_id, principal_id, id),
    ADD CONSTRAINT xw_coupon_entries_registration_fkey
        FOREIGN KEY (tenant_id, registration_id)
        REFERENCES xiangwan_registrations (tenant_id, id),
    ADD CONSTRAINT xw_coupon_entries_tenant_coupon_id_key
        UNIQUE (tenant_id, coupon_id, id),
    ADD CONSTRAINT xw_coupon_entries_source_event_fkey
        FOREIGN KEY (tenant_id, source_checkin_event_id)
        REFERENCES xiangwan_checkin_events (tenant_id, id),
    ADD CONSTRAINT xw_coupon_entries_sequence_key
        UNIQUE (tenant_id, coupon_id, entry_sequence),
    ADD CONSTRAINT xw_coupon_entries_business_key
        UNIQUE (tenant_id, coupon_id, entry_type, business_key);

ALTER TABLE xiangwan_coupon_entries
    ADD CONSTRAINT xw_coupon_entries_related_fkey
        FOREIGN KEY (tenant_id, coupon_id, related_entry_id)
        REFERENCES xiangwan_coupon_entries (tenant_id, coupon_id, id);

CREATE INDEX idx_xw_coupon_entries_order
    ON xiangwan_coupon_entries (
        tenant_id, order_id, entry_type, occurred_at, id
    )
    WHERE order_id IS NOT NULL;

CREATE UNIQUE INDEX uq_xw_coupon_entries_order_hold
    ON xiangwan_coupon_entries (tenant_id, order_id)
    WHERE entry_type = 'held';

CREATE INDEX idx_xw_coupon_entries_correction
    ON xiangwan_coupon_entries (
        tenant_id, source_checkin_event_id, entry_type, occurred_at, id
    )
    WHERE source_checkin_event_id IS NOT NULL;

CREATE OR REPLACE FUNCTION xiangwan_validate_coupon_grant()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.grant_kind = 'initial_guest' THEN
        IF NOT EXISTS (
            SELECT 1
            FROM xiangwan_checkins AS current_checkin
            JOIN xiangwan_checkin_events AS checked_in_event
              ON checked_in_event.tenant_id = current_checkin.tenant_id
             AND checked_in_event.checkin_id = current_checkin.id
             AND checked_in_event.id = NEW.source_checkin_event_id
            JOIN xiangwan_people_bindings AS people_binding
              ON people_binding.tenant_id = current_checkin.tenant_id
             AND people_binding.principal_id = current_checkin.principal_id
             AND people_binding.id = NEW.source_people_binding_id
             AND people_binding.people_profile_id =
                 NEW.source_people_profile_id
             AND people_binding.bound_at <= checked_in_event.occurred_at
             AND (
                 people_binding.revoked_at IS NULL
                 OR people_binding.revoked_at > checked_in_event.occurred_at
             )
            JOIN xiangwan_instance_role_bindings AS role_binding
              ON role_binding.tenant_id = current_checkin.tenant_id
             AND role_binding.principal_id = current_checkin.principal_id
             AND role_binding.series_id = current_checkin.series_id
             AND role_binding.instance_id = current_checkin.instance_id
             AND role_binding.id = NEW.source_role_binding_id
             AND role_binding.role_code = 'invited_guest'
             AND role_binding.granted_at <= checked_in_event.occurred_at
             AND (
                 role_binding.revoked_at IS NULL
                 OR role_binding.revoked_at > checked_in_event.occurred_at
             )
            WHERE current_checkin.tenant_id = NEW.tenant_id
              AND current_checkin.id = NEW.source_checkin_id
              AND current_checkin.principal_id = NEW.principal_id
              AND current_checkin.checkin_status = 'checked_in'
              AND checked_in_event.event_type = 'checked_in'
              AND checked_in_event.event_sequence = 1
              AND checked_in_event.occurred_at = NEW.granted_at
        ) THEN
            RAISE EXCEPTION
                'initial Coupon grant requires trusted invited-guest Checkin'
                USING ERRCODE = '23514',
                      CONSTRAINT = 'xw_coupons_initial_guest_facts';
        END IF;
        RETURN NEW;
    END IF;

    IF NOT EXISTS (
        SELECT 1
        FROM xiangwan_coupons AS historical_coupon
        WHERE historical_coupon.tenant_id = NEW.tenant_id
          AND historical_coupon.principal_id = NEW.principal_id
          AND historical_coupon.benefit_type = NEW.benefit_type
          AND (
              historical_coupon.grant_kind <> NEW.grant_kind
              OR historical_coupon.grant_business_key <>
                  NEW.grant_business_key
          )
    ) OR EXISTS (
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
              FROM xiangwan_coupon_entries AS terminal_entry
              WHERE terminal_entry.tenant_id = remaining_coupon.tenant_id
                AND terminal_entry.coupon_id = remaining_coupon.id
                AND terminal_entry.entry_type IN (
                    'redeemed', 'invalidated'
                )
          )
          AND (
              remaining_coupon.expires_at > NEW.granted_at
              OR (
                  SELECT COUNT(*)
                  FROM xiangwan_coupon_entries AS held_entry
                  WHERE held_entry.tenant_id = remaining_coupon.tenant_id
                    AND held_entry.coupon_id = remaining_coupon.id
                    AND held_entry.entry_type = 'held'
              ) > (
                  SELECT COUNT(*)
                  FROM xiangwan_coupon_entries AS released_entry
                  WHERE released_entry.tenant_id = remaining_coupon.tenant_id
                    AND released_entry.coupon_id = remaining_coupon.id
                    AND released_entry.entry_type = 'released'
              )
          )
    ) THEN
        RAISE EXCEPTION
            'manual Coupon grant requires history and zero current balance'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'xw_coupons_manual_balance';
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION xiangwan_validate_coupon_entry()
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

    IF NEW.entry_type = 'granted' THEN
        IF NEW.business_key <> instrument.grant_business_key
            OR NEW.actor_id IS DISTINCT FROM instrument.granted_by
            OR NEW.occurred_at <> instrument.granted_at
            OR NEW.recorded_at <> instrument.created_at THEN
            RAISE EXCEPTION 'Coupon grant entry does not match instrument'
                USING ERRCODE = '23514',
                      CONSTRAINT = 'xw_coupon_entries_instrument_facts';
        END IF;
        RETURN NEW;
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

    IF NEW.order_id IS NOT NULL AND NOT EXISTS (
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
        IF NEW.business_key <> ('hold:' || NEW.order_id::TEXT)
            OR NEW.occurred_at < instrument.valid_from
            OR NEW.occurred_at >= instrument.expires_at
            OR EXISTS (
                SELECT 1
                FROM xiangwan_coupon_entries AS terminal_entry
                WHERE terminal_entry.tenant_id = NEW.tenant_id
                  AND terminal_entry.coupon_id = NEW.coupon_id
                  AND terminal_entry.entry_type IN (
                      'redeemed', 'invalidated', 'correction_required'
                  )
            )
            OR (
                SELECT COUNT(*)
                FROM xiangwan_coupon_entries AS held_entry
                WHERE held_entry.tenant_id = NEW.tenant_id
                  AND held_entry.coupon_id = NEW.coupon_id
                  AND held_entry.entry_type = 'held'
            ) <> (
                SELECT COUNT(*)
                FROM xiangwan_coupon_entries AS released_entry
                WHERE released_entry.tenant_id = NEW.tenant_id
                  AND released_entry.coupon_id = NEW.coupon_id
                  AND released_entry.entry_type = 'released'
            )
            OR NOT EXISTS (
                SELECT 1
                FROM xiangwan_orders AS order_record
                JOIN xiangwan_capacity_holds AS capacity_hold
                  ON capacity_hold.tenant_id = order_record.tenant_id
                 AND capacity_hold.order_id = order_record.id
                 AND capacity_hold.hold_status = 'active'
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
    IF NOT FOUND THEN
        RAISE EXCEPTION 'Coupon related ledger entry is missing'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'xw_coupon_entries_related_facts';
    END IF;

    IF NEW.entry_type IN ('released', 'redeemed') THEN
        IF related_entry.entry_type <> 'held'
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
        IF NEW.entry_type = 'released'
            AND NEW.business_key <> ('release:' || NEW.order_id::TEXT) THEN
            RAISE EXCEPTION 'Coupon release business key is invalid'
                USING ERRCODE = '23514',
                      CONSTRAINT = 'xw_coupon_entries_release_key';
        END IF;
        IF NEW.entry_type = 'released' AND NOT EXISTS (
            SELECT 1
            FROM xiangwan_orders AS order_record
            JOIN xiangwan_capacity_holds AS capacity_hold
              ON capacity_hold.tenant_id = order_record.tenant_id
             AND capacity_hold.order_id = order_record.id
            WHERE order_record.tenant_id = NEW.tenant_id
              AND order_record.id = NEW.order_id
              AND order_record.payment_status <> 'paid_confirmed'
              AND capacity_hold.hold_status IN ('released', 'expired')
        ) THEN
            RAISE EXCEPTION 'Coupon release requires terminal capacity hold'
                USING ERRCODE = '23514',
                      CONSTRAINT = 'xw_coupon_entries_release_facts';
        END IF;
        IF NEW.entry_type = 'redeemed'
            AND (
                NEW.business_key <> ('redeem:' || NEW.order_id::TEXT)
                OR NOT EXISTS (
                    SELECT 1
                    FROM xiangwan_orders AS order_record
                    JOIN xiangwan_registrations AS registration_record
                      ON registration_record.tenant_id =
                          order_record.tenant_id
                     AND registration_record.id =
                          order_record.registration_id
                    WHERE order_record.tenant_id = NEW.tenant_id
                      AND order_record.id = NEW.order_id
                      AND order_record.payment_status = 'paid_confirmed'
                      AND order_record.paid_at <= NEW.occurred_at
                      AND registration_record.participation_status =
                          'confirmed'
                )
            ) THEN
            RAISE EXCEPTION 'Coupon redemption requires settled Order'
                USING ERRCODE = '23514',
                      CONSTRAINT = 'xw_coupon_entries_redemption_facts';
        END IF;
        RETURN NEW;
    END IF;

    IF related_entry.entry_type <> 'granted'
        OR NEW.business_key <>
            ('checkin-correction:' || NEW.source_checkin_event_id::TEXT)
        OR instrument.grant_kind <> 'initial_guest'
        OR NOT EXISTS (
            SELECT 1
            FROM xiangwan_checkin_events AS revoked_event
            WHERE revoked_event.tenant_id = NEW.tenant_id
              AND revoked_event.id = NEW.source_checkin_event_id
              AND revoked_event.checkin_id = instrument.source_checkin_id
              AND revoked_event.event_type = 'revoked'
              AND revoked_event.actor_id = NEW.actor_id
              AND revoked_event.reason = NEW.reason
              AND revoked_event.occurred_at = NEW.occurred_at
        ) THEN
        RAISE EXCEPTION 'Coupon correction source is invalid'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'xw_coupon_entries_correction_source';
    END IF;
    IF NEW.entry_type = 'invalidated' AND (
        EXISTS (
            SELECT 1
            FROM xiangwan_coupon_entries AS terminal_entry
            WHERE terminal_entry.tenant_id = NEW.tenant_id
              AND terminal_entry.coupon_id = NEW.coupon_id
              AND terminal_entry.entry_type IN ('redeemed', 'invalidated')
        )
        OR (
            SELECT COUNT(*)
            FROM xiangwan_coupon_entries AS held_entry
            WHERE held_entry.tenant_id = NEW.tenant_id
              AND held_entry.coupon_id = NEW.coupon_id
              AND held_entry.entry_type = 'held'
        ) > (
            SELECT COUNT(*)
            FROM xiangwan_coupon_entries AS released_entry
            WHERE released_entry.tenant_id = NEW.tenant_id
              AND released_entry.coupon_id = NEW.coupon_id
              AND released_entry.entry_type = 'released'
        )
    ) THEN
        RAISE EXCEPTION 'used Coupon cannot be directly invalidated'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'xw_coupon_entries_invalidation_facts';
    END IF;
    IF NEW.entry_type = 'correction_required' AND NOT (
        EXISTS (
            SELECT 1
            FROM xiangwan_coupon_entries AS redeemed_entry
            WHERE redeemed_entry.tenant_id = NEW.tenant_id
              AND redeemed_entry.coupon_id = NEW.coupon_id
              AND redeemed_entry.entry_type = 'redeemed'
        )
        OR (
            SELECT COUNT(*)
            FROM xiangwan_coupon_entries AS held_entry
            WHERE held_entry.tenant_id = NEW.tenant_id
              AND held_entry.coupon_id = NEW.coupon_id
              AND held_entry.entry_type = 'held'
        ) > (
            SELECT COUNT(*)
            FROM xiangwan_coupon_entries AS released_entry
            WHERE released_entry.tenant_id = NEW.tenant_id
              AND released_entry.coupon_id = NEW.coupon_id
              AND released_entry.entry_type = 'released'
        )
    ) THEN
        RAISE EXCEPTION 'Coupon correction exception requires prior use'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'xw_coupon_entries_exception_facts';
    END IF;
    RETURN NEW;
END;
$$;
