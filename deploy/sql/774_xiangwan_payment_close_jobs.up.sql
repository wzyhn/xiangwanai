-- 774_xiangwan_payment_close_jobs.up.sql
-- Durable, PostgreSQL-leased reconciliation for every locally closed Order
-- that may have reached WeChat Pay. The migration is roll-forward-only because
-- dropping queued or completed financial work would destroy operational truth.

CREATE TABLE IF NOT EXISTS xiangwan_payment_close_jobs (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    order_id UUID NOT NULL,
    principal_id UUID NOT NULL REFERENCES principals(id),
    payment_app_id VARCHAR(64) NOT NULL,
    payment_merchant_id VARCHAR(64) NOT NULL,
    out_trade_no VARCHAR(32) NOT NULL,
    amount_cents BIGINT NOT NULL,
    job_status VARCHAR(16) NOT NULL,
    generation_id UUID,
    owner_token UUID,
    lease_expires_at TIMESTAMPTZ,
    next_attempt_at TIMESTAMPTZ,
    attempt_count INTEGER NOT NULL DEFAULT 0,
    last_trade_state VARCHAR(16),
    last_error_class VARCHAR(64),
    last_error_code VARCHAR(64),
    last_provider_request_id VARCHAR(128),
    resolution VARCHAR(32),
    completed_at TIMESTAMPTZ,
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT xiangwan_payment_close_jobs_identity_check
        CHECK (
            out_trade_no ~ '^[0-9A-Za-z_|*-]{6,32}$'
            AND BTRIM(payment_app_id) = payment_app_id
            AND CHAR_LENGTH(payment_app_id) BETWEEN 3 AND 64
            AND BTRIM(payment_merchant_id) = payment_merchant_id
            AND CHAR_LENGTH(payment_merchant_id) BETWEEN 6 AND 64
            AND amount_cents > 0
        ),
    CONSTRAINT xiangwan_payment_close_jobs_status_check
        CHECK (job_status IN ('pending', 'in_progress', 'retry', 'completed')),
    CONSTRAINT xiangwan_payment_close_jobs_resolution_check
        CHECK (
            resolution IS NULL
            OR resolution IN (
                'provider_closed',
                'provider_terminal',
                'provider_absent',
                'payment_converged',
                'manual_review'
            )
        ),
    CONSTRAINT xiangwan_payment_close_jobs_state_shape_check
        CHECK (
            (
                job_status = 'pending'
                AND generation_id IS NULL
                AND owner_token IS NULL
                AND lease_expires_at IS NULL
                AND next_attempt_at IS NOT NULL
                AND attempt_count = 0
                AND last_trade_state IS NULL
                AND last_error_class IS NULL
                AND last_error_code IS NULL
                AND last_provider_request_id IS NULL
                AND resolution IS NULL
                AND completed_at IS NULL
            )
            OR (
                job_status = 'in_progress'
                AND generation_id IS NOT NULL
                AND owner_token IS NOT NULL
                AND lease_expires_at IS NOT NULL
                AND next_attempt_at IS NULL
                AND attempt_count > 0
                AND last_trade_state IS NULL
                AND last_error_class IS NULL
                AND last_error_code IS NULL
                AND last_provider_request_id IS NULL
                AND resolution IS NULL
                AND completed_at IS NULL
            )
            OR (
                job_status = 'retry'
                AND generation_id IS NOT NULL
                AND owner_token IS NULL
                AND lease_expires_at IS NULL
                AND next_attempt_at IS NOT NULL
                AND attempt_count > 0
                AND (
                    last_trade_state IS NOT NULL
                    OR last_error_class IS NOT NULL
                )
                AND (
                    last_trade_state IS NULL
                    OR last_trade_state IN ('NOTPAY', 'USERPAYING')
                )
                AND resolution IS NULL
                AND completed_at IS NULL
            )
            OR (
                job_status = 'completed'
                AND generation_id IS NOT NULL
                AND owner_token IS NULL
                AND lease_expires_at IS NULL
                AND next_attempt_at IS NULL
                AND attempt_count > 0
                AND resolution IS NOT NULL
                AND completed_at IS NOT NULL
                AND (
                    (
                        resolution = 'provider_closed'
                        AND last_trade_state = 'NOTPAY'
                        AND last_error_class IS NULL
                        AND last_error_code IS NULL
                    )
                    OR (
                        resolution = 'provider_terminal'
                        AND last_trade_state IN ('CLOSED', 'REVOKED', 'PAYERROR')
                        AND last_error_class IS NULL
                        AND last_error_code IS NULL
                    )
                    OR (
                        resolution = 'provider_absent'
                        AND last_trade_state IS NULL
                        AND last_error_class IS NOT NULL
                        AND last_error_code IN ('ORDER_NOT_EXIST', 'ORDERNOTEXIST')
                    )
                    OR (
                        resolution = 'payment_converged'
                        AND (
                            last_trade_state IS NULL
                            OR last_trade_state = 'SUCCESS'
                        )
                        AND last_error_class IS NULL
                        AND last_error_code IS NULL
                    )
                    OR (
                        resolution = 'manual_review'
                        AND last_trade_state = 'REFUND'
                        AND last_error_class IS NULL
                        AND last_error_code IS NULL
                    )
                )
            )
        ),
    CONSTRAINT xiangwan_payment_close_jobs_trade_state_check
        CHECK (
            last_trade_state IS NULL
            OR last_trade_state IN (
                'SUCCESS', 'REFUND', 'NOTPAY', 'CLOSED',
                'REVOKED', 'USERPAYING', 'PAYERROR'
            )
        ),
    CONSTRAINT xiangwan_payment_close_jobs_error_check
        CHECK (
            (
                last_error_class IS NULL
                AND last_error_code IS NULL
            )
            OR (
                last_error_class ~ '^[a-z][a-z0-9_]{0,63}$'
                AND (
                    last_error_code IS NULL
                    OR last_error_code ~ '^[A-Z][A-Z0-9_]{0,63}$'
                )
            )
        ),
    CONSTRAINT xiangwan_payment_close_jobs_provider_request_check
        CHECK (
            last_provider_request_id IS NULL
            OR (
                BTRIM(last_provider_request_id) = last_provider_request_id
                AND CHAR_LENGTH(last_provider_request_id) BETWEEN 1 AND 128
            )
        ),
    CONSTRAINT xiangwan_payment_close_jobs_time_check
        CHECK (
            version >= 1
            AND updated_at >= created_at
            AND (lease_expires_at IS NULL OR lease_expires_at > updated_at)
            AND (next_attempt_at IS NULL OR next_attempt_at >= updated_at)
            AND (
                completed_at IS NULL
                OR (completed_at >= created_at AND updated_at >= completed_at)
            )
        ),
    CONSTRAINT xiangwan_payment_close_jobs_order_identity_fkey
        FOREIGN KEY (
            tenant_id,
            order_id,
            principal_id,
            payment_app_id,
            payment_merchant_id,
            out_trade_no,
            amount_cents
        ) REFERENCES xiangwan_orders (
            tenant_id,
            id,
            principal_id,
            payment_app_id,
            payment_merchant_id,
            merchant_order_no,
            payable_cents
        ),
    CONSTRAINT xiangwan_payment_close_jobs_tenant_order_key
        UNIQUE (tenant_id, order_id)
);

CREATE INDEX IF NOT EXISTS idx_xiangwan_payment_close_jobs_due
    ON xiangwan_payment_close_jobs (
        COALESCE(lease_expires_at, next_attempt_at), tenant_id, order_id
    )
    WHERE job_status IN ('pending', 'in_progress', 'retry');

CREATE UNIQUE INDEX IF NOT EXISTS uq_xiangwan_payment_close_jobs_owner
    ON xiangwan_payment_close_jobs (owner_token)
    WHERE owner_token IS NOT NULL;

CREATE OR REPLACE FUNCTION xiangwan_guard_payment_close_job_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
        OR NEW.order_id IS DISTINCT FROM OLD.order_id
        OR NEW.principal_id IS DISTINCT FROM OLD.principal_id
        OR NEW.payment_app_id IS DISTINCT FROM OLD.payment_app_id
        OR NEW.payment_merchant_id IS DISTINCT FROM OLD.payment_merchant_id
        OR NEW.out_trade_no IS DISTINCT FROM OLD.out_trade_no
        OR NEW.amount_cents IS DISTINCT FROM OLD.amount_cents
        OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'xiangwan payment close job identity is immutable'
            USING ERRCODE = '23514';
    END IF;
    IF OLD.job_status = 'completed' THEN
        RAISE EXCEPTION 'xiangwan completed payment close job is immutable'
            USING ERRCODE = '23514';
    END IF;
    IF NEW.version <> OLD.version + 1
        OR NEW.updated_at < OLD.updated_at
        OR NEW.attempt_count < OLD.attempt_count
        OR NEW.attempt_count > OLD.attempt_count + 1 THEN
        RAISE EXCEPTION 'xiangwan payment close job progress must advance once'
            USING ERRCODE = '23514';
    END IF;
    IF NEW.job_status = 'in_progress' AND NOT (
        NEW.attempt_count = OLD.attempt_count + 1
        AND (
            (
                OLD.job_status IN ('pending', 'retry')
                AND NEW.updated_at >= OLD.next_attempt_at
            )
            OR (
                OLD.job_status = 'in_progress'
                AND NEW.updated_at >= OLD.lease_expires_at
            )
        )
    ) THEN
        RAISE EXCEPTION 'xiangwan payment close job is not due'
            USING ERRCODE = '23514';
    END IF;
    IF OLD.job_status = 'in_progress'
        AND NEW.job_status IN ('retry', 'completed')
        AND NEW.attempt_count <> OLD.attempt_count THEN
        RAISE EXCEPTION 'xiangwan payment close completion changed attempt count'
            USING ERRCODE = '23514';
    END IF;
    IF NOT (
        NEW.job_status = 'in_progress'
        OR (
            OLD.job_status = 'in_progress'
            AND NEW.job_status IN ('retry', 'completed')
        )
    ) THEN
        RAISE EXCEPTION 'xiangwan payment close job transition is invalid'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION xiangwan_enqueue_payment_close_job()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    job_created_at TIMESTAMPTZ := clock_timestamp();
    enqueue_at TIMESTAMPTZ := job_created_at;
BEGIN
    IF NEW.payment_status = 'closed_unpaid'
        AND OLD.payment_status IS DISTINCT FROM NEW.payment_status THEN
        SELECT GREATEST(
            enqueue_at,
            COALESCE(
                MAX(payment_attempt.lease_expires_at + INTERVAL '10 seconds')
                    FILTER (
                        WHERE payment_attempt.attempt_status = 'in_progress'
                          AND payment_attempt.lease_expires_at IS NOT NULL
                    ),
                enqueue_at
            )
        )
        INTO enqueue_at
        FROM xiangwan_payment_attempts AS payment_attempt
        WHERE payment_attempt.tenant_id = NEW.tenant_id
          AND payment_attempt.order_id = NEW.id
          AND payment_attempt.principal_id = NEW.principal_id
          AND payment_attempt.payment_app_id = NEW.payment_app_id
          AND payment_attempt.payment_merchant_id = NEW.payment_merchant_id
          AND payment_attempt.out_trade_no = NEW.merchant_order_no
          AND payment_attempt.amount_cents = NEW.payable_cents;

        INSERT INTO xiangwan_payment_close_jobs (
            id,
            tenant_id,
            order_id,
            principal_id,
            payment_app_id,
            payment_merchant_id,
            out_trade_no,
            amount_cents,
            job_status,
            next_attempt_at,
            attempt_count,
            version,
            created_at,
            updated_at
        )
        SELECT
            gen_random_uuid(),
            NEW.tenant_id,
            NEW.id,
            NEW.principal_id,
            NEW.payment_app_id,
            NEW.payment_merchant_id,
            NEW.merchant_order_no,
            NEW.payable_cents,
            'pending',
            enqueue_at,
            0,
            1,
            job_created_at,
            job_created_at
        WHERE EXISTS (
            SELECT 1
            FROM xiangwan_payment_attempts AS payment_attempt
            WHERE payment_attempt.tenant_id = NEW.tenant_id
              AND payment_attempt.order_id = NEW.id
              AND payment_attempt.principal_id = NEW.principal_id
              AND payment_attempt.payment_app_id = NEW.payment_app_id
              AND payment_attempt.payment_merchant_id = NEW.payment_merchant_id
              AND payment_attempt.out_trade_no = NEW.merchant_order_no
              AND payment_attempt.amount_cents = NEW.payable_cents
        )
        ON CONFLICT (tenant_id, order_id) DO NOTHING;
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_xiangwan_payment_close_job_enqueue
    ON xiangwan_orders;
CREATE TRIGGER trg_xiangwan_payment_close_job_enqueue
    AFTER UPDATE OF payment_status ON xiangwan_orders
    FOR EACH ROW EXECUTE FUNCTION xiangwan_enqueue_payment_close_job();

DROP TRIGGER IF EXISTS trg_xiangwan_payment_close_job_guard
    ON xiangwan_payment_close_jobs;
CREATE TRIGGER trg_xiangwan_payment_close_job_guard
    BEFORE UPDATE ON xiangwan_payment_close_jobs
    FOR EACH ROW EXECUTE FUNCTION xiangwan_guard_payment_close_job_mutation();

DROP TRIGGER IF EXISTS trg_xiangwan_payment_close_job_no_delete
    ON xiangwan_payment_close_jobs;
CREATE TRIGGER trg_xiangwan_payment_close_job_no_delete
    BEFORE DELETE ON xiangwan_payment_close_jobs
    FOR EACH ROW EXECUTE FUNCTION xiangwan_reject_payment_evidence_mutation();

DROP TRIGGER IF EXISTS trg_xiangwan_payment_close_job_no_truncate
    ON xiangwan_payment_close_jobs;
CREATE TRIGGER trg_xiangwan_payment_close_job_no_truncate
    BEFORE TRUNCATE ON xiangwan_payment_close_jobs
    FOR EACH STATEMENT EXECUTE FUNCTION xiangwan_reject_payment_evidence_mutation();

-- Synchronous backfill is bounded to the new Xiangwan payment surface. It
-- creates only one payload-free job per existing closed Order and performs no
-- provider call inside the migration.
WITH backfill_candidates AS (
    SELECT
        payment_order.tenant_id,
        payment_order.id AS order_id,
        payment_order.principal_id,
        payment_order.payment_app_id,
        payment_order.payment_merchant_id,
        payment_order.merchant_order_no,
        payment_order.payable_cents,
        GREATEST(
            statement_timestamp(),
            COALESCE(
                MAX(payment_attempt.lease_expires_at + INTERVAL '10 seconds')
                    FILTER (
                        WHERE payment_attempt.attempt_status = 'in_progress'
                          AND payment_attempt.lease_expires_at IS NOT NULL
                    ),
                statement_timestamp()
            )
        ) AS enqueue_at
    FROM xiangwan_orders AS payment_order
    JOIN xiangwan_payment_attempts AS payment_attempt
      ON payment_attempt.tenant_id = payment_order.tenant_id
     AND payment_attempt.order_id = payment_order.id
     AND payment_attempt.principal_id = payment_order.principal_id
     AND payment_attempt.payment_app_id = payment_order.payment_app_id
     AND payment_attempt.payment_merchant_id = payment_order.payment_merchant_id
     AND payment_attempt.out_trade_no = payment_order.merchant_order_no
     AND payment_attempt.amount_cents = payment_order.payable_cents
    WHERE payment_order.payment_status = 'closed_unpaid'
    GROUP BY
        payment_order.tenant_id,
        payment_order.id,
        payment_order.principal_id,
        payment_order.payment_app_id,
        payment_order.payment_merchant_id,
        payment_order.merchant_order_no,
        payment_order.payable_cents
)
INSERT INTO xiangwan_payment_close_jobs (
    id,
    tenant_id,
    order_id,
    principal_id,
    payment_app_id,
    payment_merchant_id,
    out_trade_no,
    amount_cents,
    job_status,
    next_attempt_at,
    attempt_count,
    version,
    created_at,
    updated_at
)
SELECT
    gen_random_uuid(),
    candidate.tenant_id,
    candidate.order_id,
    candidate.principal_id,
    candidate.payment_app_id,
    candidate.payment_merchant_id,
    candidate.merchant_order_no,
    candidate.payable_cents,
    'pending',
    candidate.enqueue_at,
    0,
    1,
    statement_timestamp(),
    statement_timestamp()
FROM backfill_candidates AS candidate
ON CONFLICT (tenant_id, order_id) DO NOTHING;
