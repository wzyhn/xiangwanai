-- 746_xiangwan_refund_events.up.sql
-- Preserve every operator-driven Refund transition as an immutable,
-- idempotent fact. The mutable Refund case remains the current projection.

ALTER TABLE xiangwan_refund_cases
    DROP CONSTRAINT IF EXISTS xiangwan_refund_cases_event_identity_key,
    ADD CONSTRAINT xiangwan_refund_cases_event_identity_key
        UNIQUE (tenant_id, order_id, id);

CREATE TABLE IF NOT EXISTS xiangwan_refund_events (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    refund_case_id UUID NOT NULL,
    order_id UUID NOT NULL,
    event_sequence BIGINT NOT NULL,
    event_type VARCHAR(32) NOT NULL,
    idempotency_key VARCHAR(128) NOT NULL,
    from_status VARCHAR(24) NOT NULL,
    to_status VARCHAR(24) NOT NULL,
    actor_id UUID NOT NULL REFERENCES principals(id),
    successful_refund_cents BIGINT NOT NULL DEFAULT 0,
    external_refund_id VARCHAR(128),
    evidence_reference VARCHAR(500),
    operator_note VARCHAR(1000),
    failure_reason VARCHAR(500),
    occurred_at TIMESTAMPTZ NOT NULL,
    resulting_refund_version BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT xiangwan_refund_events_type_check
        CHECK (
            event_type IN (
                'processing_started',
                'refund_completed',
                'refund_failed',
                'refund_rejected'
            )
        ),
    CONSTRAINT xiangwan_refund_events_status_check
        CHECK (
            from_status IN (
                'pending_manual', 'processing', 'failed'
            )
            AND to_status IN (
                'processing', 'refunded', 'failed', 'rejected'
            )
            AND from_status <> to_status
            AND (
                (to_status = 'processing' AND from_status IN ('pending_manual', 'failed'))
                OR (
                    to_status IN ('refunded', 'failed', 'rejected')
                    AND from_status IN ('pending_manual', 'processing', 'failed')
                )
            )
        ),
    CONSTRAINT xiangwan_refund_events_type_status_check
        CHECK (
            (event_type = 'processing_started' AND to_status = 'processing')
            OR (event_type = 'refund_completed' AND to_status = 'refunded')
            OR (event_type = 'refund_failed' AND to_status = 'failed')
            OR (event_type = 'refund_rejected' AND to_status = 'rejected')
        ),
    CONSTRAINT xiangwan_refund_events_text_check
        CHECK (
            idempotency_key ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$'
            AND (external_refund_id IS NULL OR BTRIM(external_refund_id) <> '')
            AND (evidence_reference IS NULL OR BTRIM(evidence_reference) <> '')
            AND (operator_note IS NULL OR BTRIM(operator_note) <> '')
            AND (failure_reason IS NULL OR BTRIM(failure_reason) <> '')
        ),
    CONSTRAINT xiangwan_refund_events_shape_check
        CHECK (
            (
                event_type = 'processing_started'
                AND successful_refund_cents = 0
                AND external_refund_id IS NULL
                AND evidence_reference IS NULL
                AND failure_reason IS NULL
            )
            OR (
                event_type = 'refund_completed'
                AND successful_refund_cents > 0
                AND (
                    external_refund_id IS NOT NULL
                    OR evidence_reference IS NOT NULL
                )
                AND failure_reason IS NULL
            )
            OR (
                event_type IN ('refund_failed', 'refund_rejected')
                AND successful_refund_cents = 0
                AND external_refund_id IS NULL
                AND evidence_reference IS NULL
                AND failure_reason IS NOT NULL
            )
        ),
    CONSTRAINT xiangwan_refund_events_sequence_check
        CHECK (
            event_sequence >= 1
            AND resulting_refund_version = event_sequence + 1
        ),
    CONSTRAINT xiangwan_refund_events_timestamp_check
        CHECK (created_at >= occurred_at),
    CONSTRAINT xiangwan_refund_events_case_identity_fkey
        FOREIGN KEY (tenant_id, order_id, refund_case_id)
        REFERENCES xiangwan_refund_cases (tenant_id, order_id, id),
    CONSTRAINT xiangwan_refund_events_tenant_id_id_key
        UNIQUE (tenant_id, id),
    CONSTRAINT xiangwan_refund_events_case_sequence_key
        UNIQUE (tenant_id, refund_case_id, event_sequence),
    CONSTRAINT xiangwan_refund_events_case_idempotency_key
        UNIQUE (tenant_id, refund_case_id, idempotency_key)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_xiangwan_refund_events_external_refund
    ON xiangwan_refund_events (tenant_id, external_refund_id)
    WHERE external_refund_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_xiangwan_refund_events_case_timeline
    ON xiangwan_refund_events (
        tenant_id, refund_case_id, event_sequence DESC
    );

CREATE OR REPLACE FUNCTION xiangwan_validate_refund_event()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    current_case xiangwan_refund_cases%ROWTYPE;
BEGIN
    SELECT *
      INTO current_case
      FROM xiangwan_refund_cases
     WHERE tenant_id = NEW.tenant_id
       AND order_id = NEW.order_id
       AND id = NEW.refund_case_id;

    IF NOT FOUND
        OR current_case.refund_status <> NEW.to_status
        OR current_case.version <> NEW.resulting_refund_version
        OR current_case.handled_by IS DISTINCT FROM NEW.actor_id
        OR current_case.updated_at IS DISTINCT FROM NEW.occurred_at
        OR current_case.successful_refund_cents <> NEW.successful_refund_cents
        OR current_case.external_refund_id IS DISTINCT FROM NEW.external_refund_id
        OR current_case.evidence_reference IS DISTINCT FROM NEW.evidence_reference
        OR current_case.operator_note IS DISTINCT FROM NEW.operator_note
        OR current_case.failure_reason IS DISTINCT FROM NEW.failure_reason
        OR NEW.occurred_at < current_case.created_at THEN
        RAISE EXCEPTION 'xiangwan Refund event does not match current case transition'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_xiangwan_refund_events_validate
    ON xiangwan_refund_events;
CREATE TRIGGER trg_xiangwan_refund_events_validate
    BEFORE INSERT ON xiangwan_refund_events
    FOR EACH ROW EXECUTE FUNCTION xiangwan_validate_refund_event();

CREATE OR REPLACE FUNCTION xiangwan_reject_refund_event_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'xiangwan Refund events are immutable'
        USING ERRCODE = '23514';
END;
$$;

DROP TRIGGER IF EXISTS trg_xiangwan_refund_events_no_update_delete
    ON xiangwan_refund_events;
CREATE TRIGGER trg_xiangwan_refund_events_no_update_delete
    BEFORE UPDATE OR DELETE ON xiangwan_refund_events
    FOR EACH ROW EXECUTE FUNCTION xiangwan_reject_refund_event_mutation();

DROP TRIGGER IF EXISTS trg_xiangwan_refund_events_no_truncate
    ON xiangwan_refund_events;
CREATE TRIGGER trg_xiangwan_refund_events_no_truncate
    BEFORE TRUNCATE ON xiangwan_refund_events
    FOR EACH STATEMENT EXECUTE FUNCTION xiangwan_reject_refund_event_mutation();
