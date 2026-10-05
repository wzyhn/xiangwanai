-- 743_xiangwan_registration_foundation.up.sql
-- Persist Session-bound participation facts and enforce one open Registration
-- per principal and Session without relying on Redis locks.

ALTER TABLE xiangwan_activity_sessions
    DROP CONSTRAINT IF EXISTS xiangwan_activity_sessions_tenant_instance_id_key,
    ADD CONSTRAINT xiangwan_activity_sessions_tenant_instance_id_key
        UNIQUE (tenant_id, instance_id, id);

CREATE TABLE IF NOT EXISTS xiangwan_registrations (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    series_id UUID NOT NULL,
    instance_id UUID NOT NULL,
    session_id UUID NOT NULL,
    principal_id UUID NOT NULL REFERENCES principals(id),
    participation_status VARCHAR(24) NOT NULL,
    idempotency_key VARCHAR(128) NOT NULL,
    confirmed_at TIMESTAMPTZ,
    cancelled_at TIMESTAMPTZ,
    cancellation_reason VARCHAR(500),
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT xiangwan_registrations_participation_status_check
        CHECK (participation_status IN ('pending_payment', 'confirmed', 'cancelled')),
    CONSTRAINT xiangwan_registrations_idempotency_key_check
        CHECK (idempotency_key ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$'),
    CONSTRAINT xiangwan_registrations_version_check
        CHECK (version >= 1),
    CONSTRAINT xiangwan_registrations_state_shape_check
        CHECK (
            (
                participation_status = 'pending_payment'
                AND confirmed_at IS NULL
                AND cancelled_at IS NULL
                AND cancellation_reason IS NULL
            )
            OR (
                participation_status = 'confirmed'
                AND confirmed_at IS NOT NULL
                AND cancelled_at IS NULL
                AND cancellation_reason IS NULL
            )
            OR (
                participation_status = 'cancelled'
                AND cancelled_at IS NOT NULL
                AND BTRIM(COALESCE(cancellation_reason, '')) <> ''
            )
        ),
    CONSTRAINT xiangwan_registrations_timestamp_order_check
        CHECK (
            updated_at >= created_at
            AND (confirmed_at IS NULL OR confirmed_at >= created_at)
            AND (
                cancelled_at IS NULL
                OR cancelled_at >= COALESCE(confirmed_at, created_at)
            )
        ),
    CONSTRAINT xiangwan_registrations_tenant_series_instance_fkey
        FOREIGN KEY (tenant_id, series_id, instance_id)
        REFERENCES xiangwan_activity_instances (tenant_id, series_id, id),
    CONSTRAINT xiangwan_registrations_tenant_instance_session_fkey
        FOREIGN KEY (tenant_id, instance_id, session_id)
        REFERENCES xiangwan_activity_sessions (tenant_id, instance_id, id),
    CONSTRAINT xiangwan_registrations_tenant_id_id_key
        UNIQUE (tenant_id, id),
    CONSTRAINT xiangwan_registrations_tenant_idempotency_key_key
        UNIQUE (tenant_id, idempotency_key)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_xiangwan_registrations_open_principal_session
    ON xiangwan_registrations (tenant_id, principal_id, session_id)
    WHERE participation_status IN ('pending_payment', 'confirmed');

CREATE INDEX IF NOT EXISTS idx_xiangwan_registrations_principal_created
    ON xiangwan_registrations (tenant_id, principal_id, created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS idx_xiangwan_registrations_session_status
    ON xiangwan_registrations (tenant_id, session_id, participation_status, created_at, id);

CREATE OR REPLACE FUNCTION xiangwan_guard_registration_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
        OR NEW.series_id IS DISTINCT FROM OLD.series_id
        OR NEW.instance_id IS DISTINCT FROM OLD.instance_id
        OR NEW.session_id IS DISTINCT FROM OLD.session_id
        OR NEW.principal_id IS DISTINCT FROM OLD.principal_id
        OR NEW.idempotency_key IS DISTINCT FROM OLD.idempotency_key
        OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'xiangwan Registration identity is immutable'
            USING ERRCODE = '23514';
    END IF;

    IF OLD.participation_status = 'confirmed'
        AND NEW.participation_status NOT IN ('confirmed', 'cancelled') THEN
        RAISE EXCEPTION 'xiangwan confirmed Registration cannot regress'
            USING ERRCODE = '23514';
    END IF;
    IF OLD.participation_status = 'cancelled'
        AND NEW.participation_status <> 'cancelled' THEN
        RAISE EXCEPTION 'xiangwan cancelled Registration is terminal'
            USING ERRCODE = '23514';
    END IF;
    IF OLD.confirmed_at IS NOT NULL
        AND NEW.confirmed_at IS DISTINCT FROM OLD.confirmed_at THEN
        RAISE EXCEPTION 'xiangwan Registration confirmed_at is immutable'
            USING ERRCODE = '23514';
    END IF;
    IF OLD.cancelled_at IS NOT NULL
        AND (
            NEW.cancelled_at IS DISTINCT FROM OLD.cancelled_at
            OR NEW.cancellation_reason IS DISTINCT FROM OLD.cancellation_reason
        ) THEN
        RAISE EXCEPTION 'xiangwan Registration cancellation fact is immutable'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_xiangwan_registrations_guard_mutation
    ON xiangwan_registrations;
CREATE TRIGGER trg_xiangwan_registrations_guard_mutation
    BEFORE UPDATE ON xiangwan_registrations
    FOR EACH ROW EXECUTE FUNCTION xiangwan_guard_registration_mutation();

CREATE OR REPLACE FUNCTION xiangwan_reject_registration_removal()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'xiangwan Registrations cannot be physically removed'
        USING ERRCODE = '23514';
END;
$$;

DROP TRIGGER IF EXISTS trg_xiangwan_registrations_no_delete
    ON xiangwan_registrations;
CREATE TRIGGER trg_xiangwan_registrations_no_delete
    BEFORE DELETE ON xiangwan_registrations
    FOR EACH ROW EXECUTE FUNCTION xiangwan_reject_registration_removal();

DROP TRIGGER IF EXISTS trg_xiangwan_registrations_no_truncate
    ON xiangwan_registrations;
CREATE TRIGGER trg_xiangwan_registrations_no_truncate
    BEFORE TRUNCATE ON xiangwan_registrations
    FOR EACH STATEMENT EXECUTE FUNCTION xiangwan_reject_registration_removal();
