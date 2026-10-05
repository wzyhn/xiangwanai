-- 745_xiangwan_refund_foundation.up.sql
-- Keep manual Refund handling orthogonal to Registration participation and
-- Order payment facts. PostgreSQL enforces one traceable case for every paid
-- Order whose Registration is no longer valid.

ALTER TABLE xiangwan_orders
    DROP CONSTRAINT IF EXISTS xiangwan_orders_refund_identity_key,
    ADD CONSTRAINT xiangwan_orders_refund_identity_key
        UNIQUE (
            tenant_id, registration_id, series_id, instance_id,
            session_id, principal_id, id
        );

CREATE TABLE IF NOT EXISTS xiangwan_refund_cases (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    order_id UUID NOT NULL,
    registration_id UUID NOT NULL,
    series_id UUID NOT NULL,
    instance_id UUID NOT NULL,
    session_id UUID NOT NULL,
    principal_id UUID NOT NULL REFERENCES principals(id),
    refund_status VARCHAR(24) NOT NULL,
    reason_code VARCHAR(64) NOT NULL,
    idempotency_key VARCHAR(128) NOT NULL,
    requested_refund_cents BIGINT NOT NULL,
    successful_refund_cents BIGINT NOT NULL DEFAULT 0,
    processing_started_at TIMESTAMPTZ,
    resolved_at TIMESTAMPTZ,
    handled_by UUID REFERENCES principals(id),
    external_refund_id VARCHAR(128),
    evidence_reference VARCHAR(500),
    operator_note VARCHAR(1000),
    failure_reason VARCHAR(500),
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT xiangwan_refund_cases_status_check
        CHECK (
            refund_status IN (
                'pending_manual', 'processing', 'refunded', 'failed', 'rejected'
            )
        ),
    CONSTRAINT xiangwan_refund_cases_reason_check
        CHECK (
            reason_code IN (
                'hold_expired_after_payment',
                'order_closed_after_payment',
                'session_unavailable_after_payment',
                'user_cancelled',
                'session_cancelled',
                'instance_cancelled',
                'operator_adjustment'
            )
        ),
    CONSTRAINT xiangwan_refund_cases_text_check
        CHECK (
            idempotency_key ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$'
            AND (external_refund_id IS NULL OR BTRIM(external_refund_id) <> '')
            AND (evidence_reference IS NULL OR BTRIM(evidence_reference) <> '')
            AND (operator_note IS NULL OR BTRIM(operator_note) <> '')
            AND (failure_reason IS NULL OR BTRIM(failure_reason) <> '')
        ),
    CONSTRAINT xiangwan_refund_cases_amount_check
        CHECK (
            requested_refund_cents > 0
            AND successful_refund_cents >= 0
            AND successful_refund_cents <= requested_refund_cents
        ),
    CONSTRAINT xiangwan_refund_cases_state_shape_check
        CHECK (
            (
                refund_status = 'pending_manual'
                AND successful_refund_cents = 0
                AND processing_started_at IS NULL
                AND resolved_at IS NULL
                AND handled_by IS NULL
                AND external_refund_id IS NULL
                AND evidence_reference IS NULL
                AND failure_reason IS NULL
            )
            OR (
                refund_status = 'processing'
                AND successful_refund_cents = 0
                AND processing_started_at IS NOT NULL
                AND resolved_at IS NULL
                AND handled_by IS NOT NULL
                AND external_refund_id IS NULL
                AND evidence_reference IS NULL
                AND failure_reason IS NULL
            )
            OR (
                refund_status = 'refunded'
                AND successful_refund_cents = requested_refund_cents
                AND resolved_at IS NOT NULL
                AND handled_by IS NOT NULL
                AND (
                    BTRIM(COALESCE(external_refund_id, '')) <> ''
                    OR BTRIM(COALESCE(evidence_reference, '')) <> ''
                )
                AND failure_reason IS NULL
            )
            OR (
                refund_status IN ('failed', 'rejected')
                AND successful_refund_cents = 0
                AND resolved_at IS NOT NULL
                AND handled_by IS NOT NULL
                AND BTRIM(COALESCE(failure_reason, '')) <> ''
            )
        ),
    CONSTRAINT xiangwan_refund_cases_timestamp_order_check
        CHECK (
            updated_at >= created_at
            AND (
                processing_started_at IS NULL
                OR processing_started_at >= created_at
            )
            AND (resolved_at IS NULL OR resolved_at >= created_at)
            AND (
                processing_started_at IS NULL
                OR resolved_at IS NULL
                OR resolved_at >= processing_started_at
            )
        ),
    CONSTRAINT xiangwan_refund_cases_version_check
        CHECK (version >= 1),
    CONSTRAINT xiangwan_refund_cases_order_identity_fkey
        FOREIGN KEY (
            tenant_id, registration_id, series_id, instance_id,
            session_id, principal_id, order_id
        )
        REFERENCES xiangwan_orders (
            tenant_id, registration_id, series_id, instance_id,
            session_id, principal_id, id
        ),
    CONSTRAINT xiangwan_refund_cases_tenant_id_id_key
        UNIQUE (tenant_id, id),
    CONSTRAINT xiangwan_refund_cases_tenant_order_key
        UNIQUE (tenant_id, order_id),
    CONSTRAINT xiangwan_refund_cases_tenant_idempotency_key
        UNIQUE (tenant_id, idempotency_key)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_xiangwan_refund_cases_external_refund
    ON xiangwan_refund_cases (tenant_id, external_refund_id)
    WHERE external_refund_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_xiangwan_refund_cases_manual_queue
    ON xiangwan_refund_cases (
        tenant_id, refund_status, created_at, id
    )
    WHERE refund_status IN ('pending_manual', 'processing', 'failed');

CREATE INDEX IF NOT EXISTS idx_xiangwan_refund_cases_principal
    ON xiangwan_refund_cases (
        tenant_id, principal_id, updated_at DESC, id DESC
    );

CREATE OR REPLACE FUNCTION xiangwan_guard_refund_case_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    paid_amount BIGINT;
BEGIN
    SELECT actual_paid_cents
      INTO paid_amount
      FROM xiangwan_orders
     WHERE tenant_id = NEW.tenant_id
       AND registration_id = NEW.registration_id
       AND series_id = NEW.series_id
       AND instance_id = NEW.instance_id
       AND session_id = NEW.session_id
       AND principal_id = NEW.principal_id
       AND id = NEW.order_id;

    IF paid_amount IS NULL
        OR NEW.requested_refund_cents > paid_amount
        OR NEW.successful_refund_cents > paid_amount THEN
        RAISE EXCEPTION 'xiangwan Refund amount exceeds trusted paid fact'
            USING ERRCODE = '23514';
    END IF;

    IF TG_OP = 'UPDATE' THEN
        IF NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
            OR NEW.order_id IS DISTINCT FROM OLD.order_id
            OR NEW.registration_id IS DISTINCT FROM OLD.registration_id
            OR NEW.series_id IS DISTINCT FROM OLD.series_id
            OR NEW.instance_id IS DISTINCT FROM OLD.instance_id
            OR NEW.session_id IS DISTINCT FROM OLD.session_id
            OR NEW.principal_id IS DISTINCT FROM OLD.principal_id
            OR NEW.reason_code IS DISTINCT FROM OLD.reason_code
            OR NEW.idempotency_key IS DISTINCT FROM OLD.idempotency_key
            OR NEW.requested_refund_cents IS DISTINCT FROM OLD.requested_refund_cents
            OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
            RAISE EXCEPTION 'xiangwan Refund identity/request snapshot is immutable'
                USING ERRCODE = '23514';
        END IF;
        IF NEW.version <> OLD.version + 1 OR NEW.updated_at < OLD.updated_at THEN
            RAISE EXCEPTION 'xiangwan Refund version/time must advance'
                USING ERRCODE = '23514';
        END IF;
        IF NOT (
            (
                OLD.refund_status = 'pending_manual'
                AND NEW.refund_status IN (
                    'pending_manual', 'processing', 'refunded', 'failed', 'rejected'
                )
            )
            OR (
                OLD.refund_status = 'processing'
                AND NEW.refund_status IN (
                    'processing', 'refunded', 'failed', 'rejected'
                )
            )
            OR (
                OLD.refund_status = 'failed'
                AND NEW.refund_status IN (
                    'failed', 'processing', 'refunded', 'rejected'
                )
            )
            OR (
                OLD.refund_status = 'refunded'
                AND NEW.refund_status = 'refunded'
            )
            OR (
                OLD.refund_status = 'rejected'
                AND NEW.refund_status = 'rejected'
            )
        ) THEN
            RAISE EXCEPTION 'xiangwan Refund transition is invalid'
                USING ERRCODE = '23514';
        END IF;
        IF NEW.successful_refund_cents < OLD.successful_refund_cents THEN
            RAISE EXCEPTION 'xiangwan successful Refund total cannot decrease'
                USING ERRCODE = '23514';
        END IF;
        IF OLD.refund_status IN ('refunded', 'rejected')
            AND (
                NEW.successful_refund_cents IS DISTINCT FROM OLD.successful_refund_cents
                OR NEW.processing_started_at IS DISTINCT FROM OLD.processing_started_at
                OR NEW.resolved_at IS DISTINCT FROM OLD.resolved_at
                OR NEW.handled_by IS DISTINCT FROM OLD.handled_by
                OR NEW.external_refund_id IS DISTINCT FROM OLD.external_refund_id
                OR NEW.evidence_reference IS DISTINCT FROM OLD.evidence_reference
                OR NEW.operator_note IS DISTINCT FROM OLD.operator_note
                OR NEW.failure_reason IS DISTINCT FROM OLD.failure_reason
            ) THEN
            RAISE EXCEPTION 'xiangwan terminal Refund fact is immutable'
                USING ERRCODE = '23514';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_xiangwan_refund_cases_guard
    ON xiangwan_refund_cases;
CREATE TRIGGER trg_xiangwan_refund_cases_guard
    BEFORE INSERT OR UPDATE ON xiangwan_refund_cases
    FOR EACH ROW EXECUTE FUNCTION xiangwan_guard_refund_case_mutation();

CREATE OR REPLACE FUNCTION xiangwan_reject_refund_case_removal()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'xiangwan Refund cases cannot be physically removed'
        USING ERRCODE = '23514';
END;
$$;

DROP TRIGGER IF EXISTS trg_xiangwan_refund_cases_no_delete
    ON xiangwan_refund_cases;
CREATE TRIGGER trg_xiangwan_refund_cases_no_delete
    BEFORE DELETE ON xiangwan_refund_cases
    FOR EACH ROW EXECUTE FUNCTION xiangwan_reject_refund_case_removal();

DROP TRIGGER IF EXISTS trg_xiangwan_refund_cases_no_truncate
    ON xiangwan_refund_cases;
CREATE TRIGGER trg_xiangwan_refund_cases_no_truncate
    BEFORE TRUNCATE ON xiangwan_refund_cases
    FOR EACH STATEMENT EXECUTE FUNCTION xiangwan_reject_refund_case_removal();

CREATE OR REPLACE FUNCTION xiangwan_enforce_paid_order_terminal()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    participation VARCHAR(32);
BEGIN
    IF NEW.payment_status <> 'paid_confirmed' THEN
        RETURN NULL;
    END IF;

    SELECT participation_status
      INTO participation
      FROM xiangwan_registrations
     WHERE tenant_id = NEW.tenant_id
       AND id = NEW.registration_id;

    IF participation <> 'confirmed'
        AND NOT EXISTS (
            SELECT 1
              FROM xiangwan_refund_cases
             WHERE tenant_id = NEW.tenant_id
               AND order_id = NEW.id
        ) THEN
        RAISE EXCEPTION
            'xiangwan paid Order requires confirmed Registration or Refund case'
            USING ERRCODE = '23514';
    END IF;
    RETURN NULL;
END;
$$;

DROP TRIGGER IF EXISTS trg_xiangwan_orders_paid_terminal
    ON xiangwan_orders;
CREATE CONSTRAINT TRIGGER trg_xiangwan_orders_paid_terminal
    AFTER INSERT OR UPDATE OF payment_status ON xiangwan_orders
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION xiangwan_enforce_paid_order_terminal();

CREATE OR REPLACE FUNCTION xiangwan_enforce_cancelled_paid_registration()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.participation_status <> 'cancelled' THEN
        RETURN NULL;
    END IF;

    IF EXISTS (
        SELECT 1
          FROM xiangwan_orders AS activity_order
         WHERE activity_order.tenant_id = NEW.tenant_id
           AND activity_order.registration_id = NEW.id
           AND activity_order.payment_status = 'paid_confirmed'
           AND NOT EXISTS (
               SELECT 1
                 FROM xiangwan_refund_cases AS refund_case
                WHERE refund_case.tenant_id = activity_order.tenant_id
                  AND refund_case.order_id = activity_order.id
           )
    ) THEN
        RAISE EXCEPTION
            'xiangwan cancelled paid Registration requires Refund case'
            USING ERRCODE = '23514';
    END IF;
    RETURN NULL;
END;
$$;

DROP TRIGGER IF EXISTS trg_xiangwan_registrations_paid_cancellation
    ON xiangwan_registrations;
CREATE CONSTRAINT TRIGGER trg_xiangwan_registrations_paid_cancellation
    AFTER UPDATE OF participation_status ON xiangwan_registrations
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION xiangwan_enforce_cancelled_paid_registration();
