-- Append-only, two-operator record of external handling for a revoked-Checkin
-- Coupon exception. These facts do not mutate the original Coupon or Order.
CREATE TABLE IF NOT EXISTS xiangwan_coupon_correction_events (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    correction_entry_id UUID NOT NULL,
    coupon_id UUID NOT NULL,
    actor_id UUID NOT NULL REFERENCES principals(id),
    operation_id UUID NOT NULL,
    event_sequence SMALLINT NOT NULL,
    event_type VARCHAR(16) NOT NULL,
    evidence_kind VARCHAR(24),
    evidence_reference VARCHAR(128),
    adjustment_cents BIGINT NOT NULL DEFAULT 0,
    operator_note VARCHAR(500) NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT xw_coupon_correction_marker_fkey
        FOREIGN KEY (tenant_id, correction_entry_id)
        REFERENCES xiangwan_coupon_entries (tenant_id, id),
    CONSTRAINT xw_coupon_correction_coupon_fkey
        FOREIGN KEY (tenant_id, coupon_id)
        REFERENCES xiangwan_coupons (tenant_id, id),
    CONSTRAINT xw_coupon_correction_sequence_key
        UNIQUE (tenant_id, correction_entry_id, event_sequence),
    CONSTRAINT xw_coupon_correction_operation_key
        UNIQUE (tenant_id, actor_id, operation_id),
    CONSTRAINT xw_coupon_correction_event_shape CHECK (
        (event_sequence = 1 AND event_type = 'started'
         AND evidence_kind IS NULL AND evidence_reference IS NULL
         AND adjustment_cents = 0)
        OR
        (event_sequence = 2 AND event_type = 'resolved'
         AND evidence_kind IS NOT NULL AND evidence_reference IS NOT NULL
         AND evidence_kind IN ('financial', 'entitlement')
         AND evidence_reference = BTRIM(evidence_reference)
         AND CHAR_LENGTH(evidence_reference) BETWEEN 1 AND 128
         AND evidence_reference !~ '[[:cntrl:]]'
         AND ((evidence_kind = 'financial' AND adjustment_cents > 0)
              OR (evidence_kind = 'entitlement' AND adjustment_cents = 0)))
    ),
    CONSTRAINT xw_coupon_correction_note_check CHECK (
        operator_note = BTRIM(operator_note)
        AND CHAR_LENGTH(operator_note) BETWEEN 1 AND 500
        AND operator_note !~ '[[:cntrl:]]'
    )
);

CREATE INDEX IF NOT EXISTS idx_xw_coupon_correction_events_marker
    ON xiangwan_coupon_correction_events
        (tenant_id, correction_entry_id, event_sequence DESC);

CREATE OR REPLACE FUNCTION xiangwan_guard_coupon_correction_event()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE
    marker_coupon UUID;
    marker_recorded_at TIMESTAMPTZ;
    first_actor UUID;
    first_recorded_at TIMESTAMPTZ;
    face_value BIGINT;
    latest_state VARCHAR(32);
    has_redemption BOOLEAN;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'Coupon correction events are immutable'
            USING ERRCODE = '23514';
    END IF;
    SELECT coupon_id, recorded_at INTO marker_coupon, marker_recorded_at
    FROM xiangwan_coupon_entries
    WHERE tenant_id = NEW.tenant_id
      AND id = NEW.correction_entry_id
      AND entry_type = 'correction_required';
    IF marker_coupon IS NULL OR marker_coupon <> NEW.coupon_id
        OR NEW.recorded_at < marker_recorded_at THEN
        RAISE EXCEPTION 'Coupon correction marker mismatch'
            USING ERRCODE = '23514';
    END IF;
    IF NEW.event_sequence = 1 THEN
        IF EXISTS (
            SELECT 1 FROM xiangwan_coupon_correction_events
            WHERE tenant_id = NEW.tenant_id
              AND correction_entry_id = NEW.correction_entry_id
        ) THEN
            RAISE EXCEPTION 'Coupon correction already started'
                USING ERRCODE = '23514';
        END IF;
    ELSE
        SELECT actor_id, recorded_at INTO first_actor, first_recorded_at
        FROM xiangwan_coupon_correction_events
        WHERE tenant_id = NEW.tenant_id
          AND correction_entry_id = NEW.correction_entry_id
          AND event_sequence = 1;
        IF first_actor IS NULL OR first_actor = NEW.actor_id
            OR NEW.recorded_at < first_recorded_at THEN
            RAISE EXCEPTION 'Coupon correction requires a distinct reviewer'
                USING ERRCODE = '23514';
        END IF;
        SELECT face_value_cents INTO face_value
        FROM xiangwan_coupons
        WHERE tenant_id = NEW.tenant_id AND id = NEW.coupon_id
        FOR UPDATE;
        SELECT entry_type INTO latest_state
        FROM xiangwan_coupon_entries
        WHERE tenant_id = NEW.tenant_id AND coupon_id = NEW.coupon_id
          AND entry_type <> 'correction_required'
        ORDER BY entry_sequence DESC LIMIT 1;
        SELECT EXISTS (
            SELECT 1 FROM xiangwan_coupon_entries
            WHERE tenant_id = NEW.tenant_id AND coupon_id = NEW.coupon_id
              AND entry_type = 'redeemed'
        ) INTO has_redemption;
        IF face_value IS NULL OR latest_state IS NULL OR latest_state = 'held'
            OR (NEW.evidence_kind = 'financial'
                AND (NOT has_redemption OR NEW.adjustment_cents <> face_value))
            OR (NEW.evidence_kind = 'entitlement'
                AND (has_redemption OR latest_state <> 'invalidated')) THEN
            RAISE EXCEPTION 'Coupon correction evidence disagrees with ledger'
                USING ERRCODE = '23514';
        END IF;
    END IF;
    RETURN NEW;
END $$;

DROP TRIGGER IF EXISTS trg_xw_coupon_correction_event_guard
    ON xiangwan_coupon_correction_events;
CREATE TRIGGER trg_xw_coupon_correction_event_guard
    BEFORE INSERT OR UPDATE OR DELETE ON xiangwan_coupon_correction_events
    FOR EACH ROW EXECUTE FUNCTION xiangwan_guard_coupon_correction_event();

DROP TRIGGER IF EXISTS trg_xw_coupon_correction_event_no_truncate
    ON xiangwan_coupon_correction_events;
CREATE TRIGGER trg_xw_coupon_correction_event_no_truncate
    BEFORE TRUNCATE ON xiangwan_coupon_correction_events
    FOR EACH STATEMENT EXECUTE FUNCTION xiangwan_guard_coupon_correction_event();
