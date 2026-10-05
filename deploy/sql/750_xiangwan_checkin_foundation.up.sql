-- 750_xiangwan_checkin_foundation.up.sql
-- Persist one current Checkin fact per Registration and an immutable event
-- timeline. PostgreSQL is the only coordination boundary.

ALTER TABLE xiangwan_activity_sessions
    ADD COLUMN IF NOT EXISTS checked_in_registration_count INTEGER
        NOT NULL DEFAULT 0;

ALTER TABLE xiangwan_activity_sessions
    DROP CONSTRAINT IF EXISTS
        xiangwan_activity_sessions_checked_in_registration_count_check,
    ADD CONSTRAINT
        xiangwan_activity_sessions_checked_in_registration_count_check
        CHECK (checked_in_registration_count >= 0);

CREATE TABLE IF NOT EXISTS xiangwan_checkins (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    registration_id UUID NOT NULL,
    series_id UUID NOT NULL,
    instance_id UUID NOT NULL,
    session_id UUID NOT NULL,
    principal_id UUID NOT NULL REFERENCES principals(id),
    checkin_status VARCHAR(16) NOT NULL,
    checked_in_by UUID NOT NULL REFERENCES principals(id),
    checked_in_at TIMESTAMPTZ NOT NULL,
    revoked_by UUID REFERENCES principals(id),
    revoked_at TIMESTAMPTZ,
    revocation_reason VARCHAR(500),
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT xiangwan_checkins_status_check
        CHECK (checkin_status IN ('checked_in', 'revoked')),
    CONSTRAINT xiangwan_checkins_state_shape_check
        CHECK (
            (
                checkin_status = 'checked_in'
                AND version = 1
                AND updated_at = checked_in_at
                AND revoked_by IS NULL
                AND revoked_at IS NULL
                AND revocation_reason IS NULL
            )
            OR (
                checkin_status = 'revoked'
                AND version = 2
                AND revoked_by IS NOT NULL
                AND revoked_at IS NOT NULL
                AND updated_at = revoked_at
                AND BTRIM(COALESCE(revocation_reason, '')) <> ''
                AND revocation_reason = BTRIM(revocation_reason)
            )
        ),
    CONSTRAINT xiangwan_checkins_time_check
        CHECK (
            updated_at >= created_at
            AND created_at = checked_in_at
            AND (revoked_at IS NULL OR revoked_at >= checked_in_at)
        ),
    CONSTRAINT xiangwan_checkins_registration_identity_fkey
        FOREIGN KEY (
            tenant_id, series_id, instance_id,
            session_id, principal_id, registration_id
        )
        REFERENCES xiangwan_registrations (
            tenant_id, series_id, instance_id,
            session_id, principal_id, id
        ),
    CONSTRAINT xiangwan_checkins_tenant_id_id_key
        UNIQUE (tenant_id, id),
    CONSTRAINT xiangwan_checkins_tenant_registration_key
        UNIQUE (tenant_id, registration_id),
    CONSTRAINT xiangwan_checkins_event_identity_key
        UNIQUE (tenant_id, registration_id, session_id, id)
);

CREATE INDEX IF NOT EXISTS idx_xiangwan_checkins_session_status
    ON xiangwan_checkins (
        tenant_id, session_id, checkin_status, checked_in_at, id
    );

CREATE INDEX IF NOT EXISTS idx_xiangwan_checkins_principal
    ON xiangwan_checkins (
        tenant_id, principal_id, checked_in_at DESC, id DESC
    );

CREATE TABLE IF NOT EXISTS xiangwan_checkin_events (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    checkin_id UUID NOT NULL,
    registration_id UUID NOT NULL,
    session_id UUID NOT NULL,
    event_sequence BIGINT NOT NULL,
    event_type VARCHAR(16) NOT NULL,
    idempotency_key VARCHAR(128) NOT NULL,
    from_status VARCHAR(16),
    to_status VARCHAR(16) NOT NULL,
    actor_id UUID NOT NULL REFERENCES principals(id),
    reason VARCHAR(500),
    occurred_at TIMESTAMPTZ NOT NULL,
    resulting_checkin_version BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT xiangwan_checkin_events_type_check
        CHECK (event_type IN ('checked_in', 'revoked')),
    CONSTRAINT xiangwan_checkin_events_status_check
        CHECK (
            (from_status IS NULL OR from_status IN ('checked_in', 'revoked'))
            AND to_status IN ('checked_in', 'revoked')
        ),
    CONSTRAINT xiangwan_checkin_events_idempotency_key_check
        CHECK (idempotency_key ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$'),
    CONSTRAINT xiangwan_checkin_events_shape_check
        CHECK (
            (
                event_type = 'checked_in'
                AND event_sequence = 1
                AND from_status IS NULL
                AND to_status = 'checked_in'
                AND reason IS NULL
                AND resulting_checkin_version = 1
            )
            OR (
                event_type = 'revoked'
                AND event_sequence = 2
                AND from_status = 'checked_in'
                AND to_status = 'revoked'
                AND BTRIM(COALESCE(reason, '')) <> ''
                AND reason = BTRIM(reason)
                AND resulting_checkin_version = 2
            )
        ),
    CONSTRAINT xiangwan_checkin_events_time_check
        CHECK (created_at >= occurred_at),
    CONSTRAINT xiangwan_checkin_events_checkin_fkey
        FOREIGN KEY (
            tenant_id, registration_id, session_id, checkin_id
        )
        REFERENCES xiangwan_checkins (
            tenant_id, registration_id, session_id, id
        ),
    CONSTRAINT xiangwan_checkin_events_tenant_id_id_key
        UNIQUE (tenant_id, id),
    CONSTRAINT xiangwan_checkin_events_sequence_key
        UNIQUE (tenant_id, checkin_id, event_sequence),
    CONSTRAINT xiangwan_checkin_events_idempotency_key
        UNIQUE (tenant_id, actor_id, idempotency_key)
);

CREATE INDEX IF NOT EXISTS idx_xiangwan_checkin_events_timeline
    ON xiangwan_checkin_events (
        tenant_id, checkin_id, event_sequence
    );

CREATE OR REPLACE FUNCTION xiangwan_guard_checkin_insert()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    current_registration xiangwan_registrations%ROWTYPE;
BEGIN
    SELECT *
      INTO current_registration
      FROM xiangwan_registrations
     WHERE tenant_id = NEW.tenant_id
       AND id = NEW.registration_id;

    IF NOT FOUND
        OR current_registration.series_id <> NEW.series_id
        OR current_registration.instance_id <> NEW.instance_id
        OR current_registration.session_id <> NEW.session_id
        OR current_registration.principal_id <> NEW.principal_id
        OR current_registration.participation_status <> 'confirmed'
        OR current_registration.confirmed_at IS NULL
        OR NEW.checked_in_at < current_registration.confirmed_at THEN
        RAISE EXCEPTION
            'xiangwan Checkin requires the current confirmed Registration'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'xiangwan_checkins_confirmed_registration';
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION xiangwan_guard_checkin_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
        OR NEW.registration_id IS DISTINCT FROM OLD.registration_id
        OR NEW.series_id IS DISTINCT FROM OLD.series_id
        OR NEW.instance_id IS DISTINCT FROM OLD.instance_id
        OR NEW.session_id IS DISTINCT FROM OLD.session_id
        OR NEW.principal_id IS DISTINCT FROM OLD.principal_id
        OR NEW.checked_in_by IS DISTINCT FROM OLD.checked_in_by
        OR NEW.checked_in_at IS DISTINCT FROM OLD.checked_in_at
        OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'xiangwan Checkin identity is immutable'
            USING ERRCODE = '23514';
    END IF;
    IF NEW.version <> OLD.version + 1 OR NEW.updated_at < OLD.updated_at THEN
        RAISE EXCEPTION 'xiangwan Checkin version/time must advance'
            USING ERRCODE = '23514';
    END IF;
    IF OLD.checkin_status <> 'checked_in'
        OR NEW.checkin_status <> 'revoked' THEN
        RAISE EXCEPTION 'xiangwan Checkin only permits one revocation transition'
            USING ERRCODE = '23514';
    END IF;
    IF OLD.revoked_at IS NOT NULL
        AND (
            NEW.revoked_by IS DISTINCT FROM OLD.revoked_by
            OR NEW.revoked_at IS DISTINCT FROM OLD.revoked_at
            OR NEW.revocation_reason IS DISTINCT FROM OLD.revocation_reason
        ) THEN
        RAISE EXCEPTION 'xiangwan Checkin revocation fact is immutable'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION xiangwan_validate_checkin_event()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    current_checkin xiangwan_checkins%ROWTYPE;
BEGIN
    SELECT *
      INTO current_checkin
      FROM xiangwan_checkins
     WHERE tenant_id = NEW.tenant_id
       AND registration_id = NEW.registration_id
       AND session_id = NEW.session_id
       AND id = NEW.checkin_id;

    IF NOT FOUND
        OR current_checkin.checkin_status <> NEW.to_status
        OR current_checkin.version <> NEW.resulting_checkin_version
        OR current_checkin.updated_at IS DISTINCT FROM NEW.occurred_at THEN
        RAISE EXCEPTION
            'xiangwan Checkin event does not match current fact'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'xiangwan_checkin_events_current_fact';
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION xiangwan_reject_checkin_removal()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'xiangwan Checkin facts and events cannot be removed'
        USING ERRCODE = '23514';
END;
$$;

CREATE TRIGGER trg_xiangwan_checkins_insert_guard
    BEFORE INSERT ON xiangwan_checkins
    FOR EACH ROW EXECUTE FUNCTION xiangwan_guard_checkin_insert();

CREATE TRIGGER trg_xiangwan_checkins_mutation_guard
    BEFORE UPDATE ON xiangwan_checkins
    FOR EACH ROW EXECUTE FUNCTION xiangwan_guard_checkin_mutation();

CREATE TRIGGER trg_xiangwan_checkins_no_delete
    BEFORE DELETE ON xiangwan_checkins
    FOR EACH ROW EXECUTE FUNCTION xiangwan_reject_checkin_removal();

CREATE TRIGGER trg_xiangwan_checkins_no_truncate
    BEFORE TRUNCATE ON xiangwan_checkins
    FOR EACH STATEMENT EXECUTE FUNCTION xiangwan_reject_checkin_removal();

CREATE TRIGGER trg_xiangwan_checkin_events_validate
    BEFORE INSERT ON xiangwan_checkin_events
    FOR EACH ROW EXECUTE FUNCTION xiangwan_validate_checkin_event();

CREATE TRIGGER trg_xiangwan_checkin_events_no_update
    BEFORE UPDATE ON xiangwan_checkin_events
    FOR EACH ROW EXECUTE FUNCTION xiangwan_reject_checkin_removal();

CREATE TRIGGER trg_xiangwan_checkin_events_no_delete
    BEFORE DELETE ON xiangwan_checkin_events
    FOR EACH ROW EXECUTE FUNCTION xiangwan_reject_checkin_removal();

CREATE TRIGGER trg_xiangwan_checkin_events_no_truncate
    BEFORE TRUNCATE ON xiangwan_checkin_events
    FOR EACH STATEMENT EXECUTE FUNCTION xiangwan_reject_checkin_removal();
