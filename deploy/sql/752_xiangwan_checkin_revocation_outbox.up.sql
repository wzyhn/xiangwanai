-- 752_xiangwan_checkin_revocation_outbox.up.sql
-- Make Checkin revocation publish one durable correction task in the same
-- PostgreSQL transaction. Future contribution/coupon consumers reverse their
-- own append-only ledgers from this source fact.

DO $$
DECLARE
    checkins_oid OID := 'xiangwan_checkins'::regclass;
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'xiangwan_checkins_correction_identity_key'
          AND conrelid = checkins_oid
    ) THEN
        ALTER TABLE xiangwan_checkins
            ADD CONSTRAINT xiangwan_checkins_correction_identity_key
            UNIQUE (
                tenant_id, series_id, instance_id, session_id,
                registration_id, principal_id, id
            );
    END IF;
END
$$;

CREATE TABLE IF NOT EXISTS xiangwan_checkin_correction_outbox (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    checkin_id UUID NOT NULL,
    checkin_event_id UUID NOT NULL,
    registration_id UUID NOT NULL,
    series_id UUID NOT NULL,
    instance_id UUID NOT NULL,
    session_id UUID NOT NULL,
    principal_id UUID NOT NULL REFERENCES principals(id),
    outbox_status VARCHAR(16) NOT NULL DEFAULT 'pending',
    available_at TIMESTAMPTZ NOT NULL,
    attempt_count INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 25,
    lease_token UUID,
    lease_expires_at TIMESTAMPTZ,
    last_error_class VARCHAR(64),
    completed_at TIMESTAMPTZ,
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT xiangwan_checkin_correction_outbox_status_check
        CHECK (outbox_status IN (
            'pending', 'processing', 'completed', 'dead_letter'
        )),
    CONSTRAINT xiangwan_checkin_correction_outbox_attempt_check
        CHECK (
            attempt_count >= 0
            AND max_attempts > 0
            AND attempt_count <= max_attempts
        ),
    CONSTRAINT xiangwan_checkin_correction_outbox_lease_check
        CHECK (
            (lease_token IS NULL) = (lease_expires_at IS NULL)
            AND (
                (
                    outbox_status = 'pending'
                    AND lease_token IS NULL
                    AND completed_at IS NULL
                )
                OR (
                    outbox_status = 'processing'
                    AND lease_token IS NOT NULL
                    AND lease_expires_at > updated_at
                    AND completed_at IS NULL
                    AND attempt_count > 0
                )
                OR (
                    outbox_status = 'completed'
                    AND lease_token IS NULL
                    AND completed_at IS NOT NULL
                )
                OR (
                    outbox_status = 'dead_letter'
                    AND lease_token IS NULL
                    AND completed_at IS NULL
                    AND last_error_class IS NOT NULL
                    AND attempt_count = max_attempts
                )
            )
        ),
    CONSTRAINT xiangwan_checkin_correction_outbox_time_check
        CHECK (
            version >= 1
            AND updated_at >= created_at
            AND available_at >= created_at
            AND (completed_at IS NULL OR completed_at >= created_at)
        ),
    CONSTRAINT xiangwan_checkin_correction_outbox_error_check
        CHECK (
            last_error_class IS NULL
            OR (
                BTRIM(last_error_class) <> ''
                AND last_error_class = BTRIM(last_error_class)
            )
        ),
    CONSTRAINT xiangwan_checkin_correction_outbox_checkin_fkey
        FOREIGN KEY (
            tenant_id, series_id, instance_id, session_id,
            registration_id, principal_id, checkin_id
        )
        REFERENCES xiangwan_checkins (
            tenant_id, series_id, instance_id, session_id,
            registration_id, principal_id, id
        ),
    CONSTRAINT xiangwan_checkin_correction_outbox_event_fkey
        FOREIGN KEY (tenant_id, checkin_event_id)
        REFERENCES xiangwan_checkin_events (tenant_id, id),
    CONSTRAINT xiangwan_checkin_correction_outbox_tenant_id_id_key
        UNIQUE (tenant_id, id),
    CONSTRAINT xiangwan_checkin_correction_outbox_checkin_key
        UNIQUE (tenant_id, checkin_id),
    CONSTRAINT xiangwan_checkin_correction_outbox_event_key
        UNIQUE (tenant_id, checkin_event_id)
);

CREATE INDEX IF NOT EXISTS idx_xiangwan_checkin_correction_outbox_claim
    ON xiangwan_checkin_correction_outbox (
        available_at, created_at, id
    )
    WHERE outbox_status = 'pending';

CREATE UNIQUE INDEX IF NOT EXISTS
    uq_xiangwan_checkin_correction_outbox_lease_token
    ON xiangwan_checkin_correction_outbox (lease_token)
    WHERE lease_token IS NOT NULL;

CREATE OR REPLACE FUNCTION xiangwan_validate_checkin_correction_outbox()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM xiangwan_checkins AS checkin
        JOIN xiangwan_checkin_events AS event
          ON event.tenant_id = checkin.tenant_id
         AND event.checkin_id = checkin.id
         AND event.id = NEW.checkin_event_id
        WHERE checkin.tenant_id = NEW.tenant_id
          AND checkin.id = NEW.checkin_id
          AND checkin.registration_id = NEW.registration_id
          AND checkin.series_id = NEW.series_id
          AND checkin.instance_id = NEW.instance_id
          AND checkin.session_id = NEW.session_id
          AND checkin.principal_id = NEW.principal_id
          AND checkin.checkin_status = 'revoked'
          AND event.event_type = 'revoked'
          AND event.event_sequence = 2
          AND event.resulting_checkin_version = checkin.version
          AND event.occurred_at = checkin.revoked_at
    ) THEN
        RAISE EXCEPTION
            'xiangwan Checkin correction outbox requires exact revocation facts'
            USING ERRCODE = '23514',
                  CONSTRAINT =
                    'xiangwan_checkin_correction_outbox_revocation_facts';
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION xiangwan_guard_checkin_correction_outbox()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
        OR NEW.checkin_id IS DISTINCT FROM OLD.checkin_id
        OR NEW.checkin_event_id IS DISTINCT FROM OLD.checkin_event_id
        OR NEW.registration_id IS DISTINCT FROM OLD.registration_id
        OR NEW.series_id IS DISTINCT FROM OLD.series_id
        OR NEW.instance_id IS DISTINCT FROM OLD.instance_id
        OR NEW.session_id IS DISTINCT FROM OLD.session_id
        OR NEW.principal_id IS DISTINCT FROM OLD.principal_id
        OR NEW.max_attempts IS DISTINCT FROM OLD.max_attempts
        OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'xiangwan Checkin correction identity is immutable'
            USING ERRCODE = '23514';
    END IF;
    IF NEW.version <> OLD.version + 1
        OR NEW.updated_at < OLD.updated_at
        OR NEW.attempt_count < OLD.attempt_count
        OR NEW.attempt_count > OLD.attempt_count + 1 THEN
        RAISE EXCEPTION
            'xiangwan Checkin correction progress must advance once'
            USING ERRCODE = '23514';
    END IF;
    IF NOT (
        (
            OLD.outbox_status = 'pending'
            AND NEW.outbox_status = 'processing'
            AND NEW.attempt_count = OLD.attempt_count + 1
        )
        OR (
            OLD.outbox_status = 'processing'
            AND NEW.outbox_status IN (
                'pending', 'completed', 'dead_letter'
            )
            AND NEW.attempt_count = OLD.attempt_count
        )
    ) THEN
        RAISE EXCEPTION
            'xiangwan Checkin correction transition is invalid'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION xiangwan_reject_checkin_correction_removal()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'xiangwan Checkin correction tasks cannot be removed'
        USING ERRCODE = '23514';
END;
$$;

DROP TRIGGER IF EXISTS trg_xiangwan_checkin_correction_validate
    ON xiangwan_checkin_correction_outbox;
CREATE TRIGGER trg_xiangwan_checkin_correction_validate
    BEFORE INSERT ON xiangwan_checkin_correction_outbox
    FOR EACH ROW
    EXECUTE FUNCTION xiangwan_validate_checkin_correction_outbox();

DROP TRIGGER IF EXISTS trg_xiangwan_checkin_correction_guard
    ON xiangwan_checkin_correction_outbox;
CREATE TRIGGER trg_xiangwan_checkin_correction_guard
    BEFORE UPDATE ON xiangwan_checkin_correction_outbox
    FOR EACH ROW
    EXECUTE FUNCTION xiangwan_guard_checkin_correction_outbox();

DROP TRIGGER IF EXISTS trg_xiangwan_checkin_correction_no_delete
    ON xiangwan_checkin_correction_outbox;
CREATE TRIGGER trg_xiangwan_checkin_correction_no_delete
    BEFORE DELETE ON xiangwan_checkin_correction_outbox
    FOR EACH ROW
    EXECUTE FUNCTION xiangwan_reject_checkin_correction_removal();

DROP TRIGGER IF EXISTS trg_xiangwan_checkin_correction_no_truncate
    ON xiangwan_checkin_correction_outbox;
CREATE TRIGGER trg_xiangwan_checkin_correction_no_truncate
    BEFORE TRUNCATE ON xiangwan_checkin_correction_outbox
    FOR EACH STATEMENT
    EXECUTE FUNCTION xiangwan_reject_checkin_correction_removal();
