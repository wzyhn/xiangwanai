-- 757_xiangwan_coupon_grants.up.sql
-- Immutable Coupon instruments plus append-only grant entries. The first
-- invited-guest grant and every manual replenishment are five-row batches.

CREATE TABLE IF NOT EXISTS xiangwan_coupons (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    principal_id UUID NOT NULL REFERENCES principals(id),
    benefit_type VARCHAR(32) NOT NULL,
    face_value_cents BIGINT NOT NULL,
    scope_type VARCHAR(24) NOT NULL,
    scope_activity_type VARCHAR(32),
    scope_series_id UUID,
    minimum_order_cents BIGINT NOT NULL,
    valid_from TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    grant_kind VARCHAR(32) NOT NULL,
    grant_business_key VARCHAR(128) NOT NULL,
    grant_ordinal SMALLINT NOT NULL,
    policy_version VARCHAR(128) NOT NULL,
    source_people_profile_id UUID,
    source_people_binding_id UUID,
    source_role_binding_id UUID,
    source_checkin_id UUID,
    source_checkin_event_id UUID,
    granted_by UUID REFERENCES principals(id),
    grant_reason VARCHAR(500),
    grant_context VARCHAR(500),
    granted_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT xw_coupons_benefit_type_check
        CHECK (benefit_type = 'roundtable_coupon'),
    CONSTRAINT xw_coupons_value_check
        CHECK (face_value_cents > 0 AND minimum_order_cents >= 0),
    CONSTRAINT xw_coupons_scope_check
        CHECK (
            (
                scope_type = 'activity_type'
                AND scope_activity_type IN (
                    'ai_roundtable', 'special_event', 'course', 'competition'
                )
                AND scope_series_id IS NULL
            )
            OR (
                scope_type = 'series'
                AND scope_activity_type IS NULL
                AND scope_series_id IS NOT NULL
            )
        ),
    CONSTRAINT xw_coupons_grant_kind_check
        CHECK (grant_kind IN ('initial_guest', 'manual_replenishment')),
    CONSTRAINT xw_coupons_business_key_check
        CHECK (
            grant_business_key ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$'
        ),
    CONSTRAINT xw_coupons_policy_version_check
        CHECK (policy_version ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$'),
    CONSTRAINT xw_coupons_ordinal_check
        CHECK (grant_ordinal BETWEEN 1 AND 5),
    CONSTRAINT xw_coupons_time_check
        CHECK (
            valid_from = granted_at
            AND expires_at > valid_from
            AND expires_at <= granted_at + INTERVAL '1830 days'
            AND created_at >= granted_at
        ),
    CONSTRAINT xw_coupons_grant_shape_check
        CHECK (
            (
                grant_kind = 'initial_guest'
                AND grant_business_key = 'initial_guest_grant'
                AND source_people_profile_id IS NOT NULL
                AND source_people_binding_id IS NOT NULL
                AND source_role_binding_id IS NOT NULL
                AND source_checkin_id IS NOT NULL
                AND source_checkin_event_id IS NOT NULL
                AND granted_by IS NULL
                AND grant_reason IS NULL
                AND grant_context IS NULL
            )
            OR (
                grant_kind = 'manual_replenishment'
                AND source_people_profile_id IS NULL
                AND source_people_binding_id IS NULL
                AND source_role_binding_id IS NULL
                AND source_checkin_id IS NULL
                AND source_checkin_event_id IS NULL
                AND granted_by IS NOT NULL
                AND BTRIM(COALESCE(grant_reason, '')) <> ''
                AND grant_reason = BTRIM(grant_reason)
                AND CHAR_LENGTH(grant_reason) <= 500
                AND BTRIM(COALESCE(grant_context, '')) <> ''
                AND grant_context = BTRIM(grant_context)
                AND CHAR_LENGTH(grant_context) <= 500
            )
        ),
    CONSTRAINT xw_coupons_tenant_id_id_key
        UNIQUE (tenant_id, id),
    CONSTRAINT xw_coupons_tenant_principal_id_key
        UNIQUE (tenant_id, principal_id, id),
    CONSTRAINT xw_coupons_scope_series_fkey
        FOREIGN KEY (tenant_id, scope_series_id)
        REFERENCES xiangwan_activity_series (tenant_id, id),
    CONSTRAINT xw_coupons_people_profile_fkey
        FOREIGN KEY (tenant_id, source_people_profile_id)
        REFERENCES xiangwan_people_profiles (tenant_id, id),
    CONSTRAINT xw_coupons_people_binding_fkey
        FOREIGN KEY (tenant_id, source_people_binding_id)
        REFERENCES xiangwan_people_bindings (tenant_id, id),
    CONSTRAINT xw_coupons_role_binding_fkey
        FOREIGN KEY (tenant_id, source_role_binding_id)
        REFERENCES xiangwan_instance_role_bindings (tenant_id, id),
    CONSTRAINT xw_coupons_checkin_fkey
        FOREIGN KEY (tenant_id, source_checkin_id)
        REFERENCES xiangwan_checkins (tenant_id, id),
    CONSTRAINT xw_coupons_checkin_event_fkey
        FOREIGN KEY (tenant_id, source_checkin_event_id)
        REFERENCES xiangwan_checkin_events (tenant_id, id),
    CONSTRAINT xw_coupons_grant_ordinal_key
        UNIQUE (
            tenant_id, principal_id, benefit_type,
            grant_kind, grant_business_key, grant_ordinal
        )
);

CREATE INDEX IF NOT EXISTS idx_xw_coupons_principal_expiry
    ON xiangwan_coupons (
        tenant_id, principal_id, expires_at, granted_at, id
    );

CREATE INDEX IF NOT EXISTS idx_xw_coupons_source_checkin
    ON xiangwan_coupons (tenant_id, source_checkin_id)
    WHERE source_checkin_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS xiangwan_coupon_entries (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    coupon_id UUID NOT NULL,
    principal_id UUID NOT NULL REFERENCES principals(id),
    entry_type VARCHAR(24) NOT NULL,
    business_key VARCHAR(128) NOT NULL,
    actor_id UUID REFERENCES principals(id),
    occurred_at TIMESTAMPTZ NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT xw_coupon_entries_type_check
        CHECK (entry_type = 'granted'),
    CONSTRAINT xw_coupon_entries_business_key_check
        CHECK (business_key ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$'),
    CONSTRAINT xw_coupon_entries_time_check
        CHECK (recorded_at >= occurred_at),
    CONSTRAINT xw_coupon_entries_coupon_fkey
        FOREIGN KEY (tenant_id, principal_id, coupon_id)
        REFERENCES xiangwan_coupons (tenant_id, principal_id, id),
    CONSTRAINT xw_coupon_entries_tenant_id_id_key
        UNIQUE (tenant_id, id),
    CONSTRAINT xw_coupon_entries_coupon_grant_key
        UNIQUE (tenant_id, coupon_id, entry_type)
);

CREATE INDEX IF NOT EXISTS idx_xw_coupon_entries_principal_history
    ON xiangwan_coupon_entries (
        tenant_id, principal_id, occurred_at DESC, id DESC
    );

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
          AND remaining_coupon.expires_at > NEW.granted_at
          AND (
              remaining_coupon.grant_kind <> NEW.grant_kind
              OR remaining_coupon.grant_business_key <>
                  NEW.grant_business_key
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

CREATE OR REPLACE FUNCTION xiangwan_reject_coupon_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'Coupon instruments and ledger entries are append-only'
        USING ERRCODE = '23514';
END;
$$;

DROP TRIGGER IF EXISTS trg_xw_coupons_validate ON xiangwan_coupons;
CREATE TRIGGER trg_xw_coupons_validate
    BEFORE INSERT ON xiangwan_coupons
    FOR EACH ROW EXECUTE FUNCTION xiangwan_validate_coupon_grant();

DROP TRIGGER IF EXISTS trg_xw_coupons_no_update ON xiangwan_coupons;
CREATE TRIGGER trg_xw_coupons_no_update
    BEFORE UPDATE ON xiangwan_coupons
    FOR EACH ROW EXECUTE FUNCTION xiangwan_reject_coupon_mutation();

DROP TRIGGER IF EXISTS trg_xw_coupons_no_delete ON xiangwan_coupons;
CREATE TRIGGER trg_xw_coupons_no_delete
    BEFORE DELETE ON xiangwan_coupons
    FOR EACH ROW EXECUTE FUNCTION xiangwan_reject_coupon_mutation();

DROP TRIGGER IF EXISTS trg_xw_coupons_no_truncate ON xiangwan_coupons;
CREATE TRIGGER trg_xw_coupons_no_truncate
    BEFORE TRUNCATE ON xiangwan_coupons
    FOR EACH STATEMENT EXECUTE FUNCTION xiangwan_reject_coupon_mutation();

DROP TRIGGER IF EXISTS trg_xw_coupon_entries_validate
    ON xiangwan_coupon_entries;
CREATE TRIGGER trg_xw_coupon_entries_validate
    BEFORE INSERT ON xiangwan_coupon_entries
    FOR EACH ROW EXECUTE FUNCTION xiangwan_validate_coupon_entry();

DROP TRIGGER IF EXISTS trg_xw_coupon_entries_no_update
    ON xiangwan_coupon_entries;
CREATE TRIGGER trg_xw_coupon_entries_no_update
    BEFORE UPDATE ON xiangwan_coupon_entries
    FOR EACH ROW EXECUTE FUNCTION xiangwan_reject_coupon_mutation();

DROP TRIGGER IF EXISTS trg_xw_coupon_entries_no_delete
    ON xiangwan_coupon_entries;
CREATE TRIGGER trg_xw_coupon_entries_no_delete
    BEFORE DELETE ON xiangwan_coupon_entries
    FOR EACH ROW EXECUTE FUNCTION xiangwan_reject_coupon_mutation();

DROP TRIGGER IF EXISTS trg_xw_coupon_entries_no_truncate
    ON xiangwan_coupon_entries;
CREATE TRIGGER trg_xw_coupon_entries_no_truncate
    BEFORE TRUNCATE ON xiangwan_coupon_entries
    FOR EACH STATEMENT EXECUTE FUNCTION xiangwan_reject_coupon_mutation();
