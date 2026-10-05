-- 747_xiangwan_session_cancellation.up.sql
-- Cancel one published Session and converge every participation/payment/refund
-- consequence in one PostgreSQL transaction with an immutable result receipt.

ALTER TABLE xiangwan_activity_sessions
    DROP CONSTRAINT IF EXISTS xiangwan_activity_sessions_cancellation_identity_key,
    ADD CONSTRAINT xiangwan_activity_sessions_cancellation_identity_key
        UNIQUE (tenant_id, instance_id, id);

CREATE TABLE IF NOT EXISTS xiangwan_session_cancellation_receipts (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    series_id UUID NOT NULL,
    instance_id UUID NOT NULL,
    session_id UUID NOT NULL,
    idempotency_key VARCHAR(128) NOT NULL,
    cancelled_by UUID NOT NULL REFERENCES principals(id),
    cancellation_reason VARCHAR(500) NOT NULL,
    cancelled_registration_count INTEGER NOT NULL,
    released_confirmed_count INTEGER NOT NULL,
    released_hold_count INTEGER NOT NULL,
    closed_pending_order_count INTEGER NOT NULL,
    refund_case_count INTEGER NOT NULL,
    requested_refund_cents BIGINT NOT NULL,
    cancelled_at TIMESTAMPTZ NOT NULL,
    resulting_session_version BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT xiangwan_session_cancellation_receipts_text_check
        CHECK (
            idempotency_key ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$'
            AND BTRIM(cancellation_reason) <> ''
        ),
    CONSTRAINT xiangwan_session_cancellation_receipts_counts_check
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
    CONSTRAINT xiangwan_session_cancellation_receipts_time_check
        CHECK (created_at >= cancelled_at),
    CONSTRAINT xiangwan_session_cancellation_receipts_version_check
        CHECK (resulting_session_version >= 2),
    CONSTRAINT xiangwan_session_cancellation_receipts_instance_fkey
        FOREIGN KEY (tenant_id, series_id, instance_id)
        REFERENCES xiangwan_activity_instances (tenant_id, series_id, id),
    CONSTRAINT xiangwan_session_cancellation_receipts_session_fkey
        FOREIGN KEY (tenant_id, instance_id, session_id)
        REFERENCES xiangwan_activity_sessions (tenant_id, instance_id, id),
    CONSTRAINT xiangwan_session_cancellation_receipts_tenant_id_id_key
        UNIQUE (tenant_id, id),
    CONSTRAINT xiangwan_session_cancellation_receipts_tenant_session_key
        UNIQUE (tenant_id, session_id),
    CONSTRAINT xiangwan_session_cancellation_receipts_tenant_idempotency_key
        UNIQUE (tenant_id, idempotency_key)
);

CREATE INDEX IF NOT EXISTS idx_xiangwan_session_cancellations_instance
    ON xiangwan_session_cancellation_receipts (
        tenant_id, instance_id, cancelled_at, session_id
    );

CREATE OR REPLACE FUNCTION xiangwan_validate_session_cancellation_receipt()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    current_session xiangwan_activity_sessions%ROWTYPE;
BEGIN
    SELECT *
      INTO current_session
      FROM xiangwan_activity_sessions
     WHERE tenant_id = NEW.tenant_id
       AND instance_id = NEW.instance_id
       AND id = NEW.session_id;

    IF NOT FOUND
        OR current_session.status <> 'cancelled'
        OR current_session.confirmed_registration_count <> 0
        OR current_session.active_hold_count <> 0
        OR current_session.version <> NEW.resulting_session_version
        OR current_session.updated_at IS DISTINCT FROM NEW.cancelled_at
        OR EXISTS (
            SELECT 1
              FROM xiangwan_registrations AS registration
             WHERE registration.tenant_id = NEW.tenant_id
               AND registration.session_id = NEW.session_id
               AND registration.participation_status IN (
                   'pending_payment', 'confirmed'
               )
        )
        OR EXISTS (
            SELECT 1
              FROM xiangwan_capacity_holds AS capacity_hold
             WHERE capacity_hold.tenant_id = NEW.tenant_id
               AND capacity_hold.session_id = NEW.session_id
               AND capacity_hold.hold_status = 'active'
        ) THEN
        RAISE EXCEPTION
            'xiangwan Session cancellation receipt does not match terminal state'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'xiangwan_session_cancellation_receipt_state';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_xiangwan_session_cancellation_receipts_validate
    ON xiangwan_session_cancellation_receipts;
CREATE TRIGGER trg_xiangwan_session_cancellation_receipts_validate
    BEFORE INSERT ON xiangwan_session_cancellation_receipts
    FOR EACH ROW
    EXECUTE FUNCTION xiangwan_validate_session_cancellation_receipt();

CREATE OR REPLACE FUNCTION xiangwan_reject_session_cancellation_receipt_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'xiangwan Session cancellation receipts are immutable'
        USING ERRCODE = '23514',
              CONSTRAINT = 'xiangwan_session_cancellation_receipt_immutable';
END;
$$;

DROP TRIGGER IF EXISTS trg_xiangwan_session_cancellation_receipts_no_update_delete
    ON xiangwan_session_cancellation_receipts;
CREATE TRIGGER trg_xiangwan_session_cancellation_receipts_no_update_delete
    BEFORE UPDATE OR DELETE ON xiangwan_session_cancellation_receipts
    FOR EACH ROW
    EXECUTE FUNCTION xiangwan_reject_session_cancellation_receipt_mutation();

DROP TRIGGER IF EXISTS trg_xiangwan_session_cancellation_receipts_no_truncate
    ON xiangwan_session_cancellation_receipts;
CREATE TRIGGER trg_xiangwan_session_cancellation_receipts_no_truncate
    BEFORE TRUNCATE ON xiangwan_session_cancellation_receipts
    FOR EACH STATEMENT
    EXECUTE FUNCTION xiangwan_reject_session_cancellation_receipt_mutation();

CREATE OR REPLACE FUNCTION xiangwan_enforce_session_cancellation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF OLD.status = 'cancelled' AND NEW IS DISTINCT FROM OLD THEN
        RAISE EXCEPTION 'xiangwan cancelled Session is immutable'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'xiangwan_session_cancellation_irreversible';
    END IF;

    IF NEW.status = 'cancelled' THEN
        IF OLD.status <> 'published'
            OR NEW.confirmed_registration_count <> 0
            OR NEW.active_hold_count <> 0
            OR NOT EXISTS (
                SELECT 1
                  FROM xiangwan_session_cancellation_receipts AS receipt
                 WHERE receipt.tenant_id = NEW.tenant_id
                   AND receipt.session_id = NEW.id
                   AND receipt.resulting_session_version = NEW.version
                   AND receipt.cancelled_at = NEW.updated_at
            )
            OR EXISTS (
                SELECT 1
                  FROM xiangwan_registrations AS registration
                 WHERE registration.tenant_id = NEW.tenant_id
                   AND registration.session_id = NEW.id
                   AND registration.participation_status IN (
                       'pending_payment', 'confirmed'
                   )
            )
            OR EXISTS (
                SELECT 1
                  FROM xiangwan_capacity_holds AS capacity_hold
                 WHERE capacity_hold.tenant_id = NEW.tenant_id
                   AND capacity_hold.session_id = NEW.id
                   AND capacity_hold.hold_status = 'active'
            ) THEN
            RAISE EXCEPTION
                'xiangwan cancelled Session has unconverged participation'
                USING ERRCODE = '23514',
                      CONSTRAINT = 'xiangwan_session_cancellation_incomplete';
        END IF;
    END IF;
    RETURN NULL;
END;
$$;

DROP TRIGGER IF EXISTS trg_xiangwan_activity_sessions_cancellation_guard
    ON xiangwan_activity_sessions;
CREATE CONSTRAINT TRIGGER trg_xiangwan_activity_sessions_cancellation_guard
    AFTER UPDATE ON xiangwan_activity_sessions
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW
    EXECUTE FUNCTION xiangwan_enforce_session_cancellation();

CREATE OR REPLACE FUNCTION xiangwan_enforce_open_registration_session()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.participation_status IN ('pending_payment', 'confirmed')
        AND NOT EXISTS (
            SELECT 1
              FROM xiangwan_activity_sessions AS activity_session
             WHERE activity_session.tenant_id = NEW.tenant_id
               AND activity_session.id = NEW.session_id
               AND activity_session.status = 'published'
        ) THEN
        RAISE EXCEPTION
            'xiangwan open Registration requires published Session'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'xiangwan_open_registration_session_published';
    END IF;
    RETURN NULL;
END;
$$;

DROP TRIGGER IF EXISTS trg_xiangwan_registrations_open_session_guard
    ON xiangwan_registrations;
CREATE CONSTRAINT TRIGGER trg_xiangwan_registrations_open_session_guard
    AFTER INSERT OR UPDATE ON xiangwan_registrations
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW
    EXECUTE FUNCTION xiangwan_enforce_open_registration_session();

CREATE OR REPLACE FUNCTION xiangwan_enforce_active_hold_session()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.hold_status = 'active'
        AND NOT EXISTS (
            SELECT 1
              FROM xiangwan_activity_sessions AS activity_session
             WHERE activity_session.tenant_id = NEW.tenant_id
               AND activity_session.id = NEW.session_id
               AND activity_session.status = 'published'
        ) THEN
        RAISE EXCEPTION 'xiangwan active hold requires published Session'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'xiangwan_active_hold_session_published';
    END IF;
    RETURN NULL;
END;
$$;

DROP TRIGGER IF EXISTS trg_xiangwan_capacity_holds_active_session_guard
    ON xiangwan_capacity_holds;
CREATE CONSTRAINT TRIGGER trg_xiangwan_capacity_holds_active_session_guard
    AFTER INSERT OR UPDATE ON xiangwan_capacity_holds
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW
    EXECUTE FUNCTION xiangwan_enforce_active_hold_session();
