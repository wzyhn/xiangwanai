-- Merchant credential/configuration generations are deployment metadata only.
-- This migration stores opaque references and immutable order/close-job
-- identity snapshots; it does not load secrets or call WeChat.

CREATE TABLE IF NOT EXISTS xiangwan_payment_merchant_config_generations (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    provider VARCHAR(16) NOT NULL,
    payment_app_id VARCHAR(64) NOT NULL,
    payment_merchant_id VARCHAR(64) NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'revoked',
    secret_bundle_ref VARCHAR(512) NOT NULL,
    provider_certificate_serial VARCHAR(64),
    provider_public_key_id VARCHAR(128),
    notify_url VARCHAR(2048),
    authorization_version VARCHAR(128) NOT NULL,
    valid_from TIMESTAMPTZ,
    valid_until TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT xiangwan_payment_merchant_config_generation_provider_check
        CHECK (provider = 'wechat'),
    CONSTRAINT xiangwan_payment_merchant_config_generation_id_check
        CHECK (id <> '00000000-0000-0000-0000-000000000000'::UUID),
    CONSTRAINT xiangwan_payment_merchant_config_generation_status_check
        CHECK (status IN ('active', 'draining', 'revoked')),
    CONSTRAINT xiangwan_payment_merchant_config_generation_identity_check
        CHECK (
            BTRIM(payment_app_id) = payment_app_id
            AND CHAR_LENGTH(payment_app_id) BETWEEN 3 AND 64
            AND BTRIM(payment_merchant_id) = payment_merchant_id
            AND CHAR_LENGTH(payment_merchant_id) BETWEEN 6 AND 64
            AND BTRIM(secret_bundle_ref) = secret_bundle_ref
            AND CHAR_LENGTH(secret_bundle_ref) BETWEEN 1 AND 512
            AND BTRIM(authorization_version) = authorization_version
            AND CHAR_LENGTH(authorization_version) BETWEEN 1 AND 128
        ),
    CONSTRAINT xiangwan_payment_merchant_config_generation_time_check
        CHECK (
            updated_at >= created_at
            AND (valid_until IS NULL OR valid_from IS NULL OR valid_until > valid_from)
        ),
    CONSTRAINT xiangwan_payment_merchant_config_generation_tenant_id_key
        UNIQUE (tenant_id, id)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_xiangwan_payment_merchant_config_generation_active
    ON xiangwan_payment_merchant_config_generations (tenant_id, provider)
    WHERE status = 'active';

ALTER TABLE xiangwan_orders
    ADD COLUMN IF NOT EXISTS merchant_config_generation_id UUID;

ALTER TABLE xiangwan_payment_close_jobs
    ADD COLUMN IF NOT EXISTS merchant_config_generation_id UUID;

ALTER TABLE xiangwan_orders
    DROP CONSTRAINT IF EXISTS xiangwan_orders_merchant_config_generation_fkey,
    ADD CONSTRAINT xiangwan_orders_merchant_config_generation_fkey
        FOREIGN KEY (tenant_id, merchant_config_generation_id)
        REFERENCES xiangwan_payment_merchant_config_generations (tenant_id, id);

ALTER TABLE xiangwan_payment_close_jobs
    DROP CONSTRAINT IF EXISTS xiangwan_payment_close_jobs_merchant_config_generation_fkey,
    ADD CONSTRAINT xiangwan_payment_close_jobs_merchant_config_generation_fkey
        FOREIGN KEY (tenant_id, merchant_config_generation_id)
        REFERENCES xiangwan_payment_merchant_config_generations (tenant_id, id);

CREATE OR REPLACE FUNCTION xiangwan_validate_payment_merchant_config_generation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.merchant_config_generation_id IS NULL THEN
        RETURN NEW;
    END IF;
    IF NOT EXISTS (
        SELECT 1
        FROM xiangwan_payment_merchant_config_generations AS merchant_generation
        WHERE merchant_generation.tenant_id = NEW.tenant_id
          AND merchant_generation.id = NEW.merchant_config_generation_id
          AND merchant_generation.provider = 'wechat'
          AND merchant_generation.payment_app_id = NEW.payment_app_id
          AND merchant_generation.payment_merchant_id = NEW.payment_merchant_id
          AND (
              (TG_TABLE_NAME = 'xiangwan_orders'
                  AND merchant_generation.status = 'active')
              OR (TG_TABLE_NAME = 'xiangwan_payment_close_jobs'
                  AND merchant_generation.status IN ('active', 'draining', 'revoked'))
          )
    ) THEN
        RAISE EXCEPTION 'xiangwan merchant config generation identity is not active for payment facts'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_xiangwan_orders_merchant_config_generation
    ON xiangwan_orders;
CREATE TRIGGER trg_xiangwan_orders_merchant_config_generation
    BEFORE INSERT OR UPDATE OF merchant_config_generation_id,
        payment_app_id, payment_merchant_id ON xiangwan_orders
    FOR EACH ROW EXECUTE FUNCTION
        xiangwan_validate_payment_merchant_config_generation();

DROP TRIGGER IF EXISTS trg_xiangwan_payment_close_jobs_merchant_config_generation
    ON xiangwan_payment_close_jobs;
CREATE TRIGGER trg_xiangwan_payment_close_jobs_merchant_config_generation
    BEFORE INSERT OR UPDATE OF merchant_config_generation_id,
        payment_app_id, payment_merchant_id ON xiangwan_payment_close_jobs
    FOR EACH ROW EXECUTE FUNCTION
        xiangwan_validate_payment_merchant_config_generation();

CREATE OR REPLACE FUNCTION xiangwan_guard_payment_merchant_config_generation_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
        OR NEW.id IS DISTINCT FROM OLD.id
        OR NEW.provider IS DISTINCT FROM OLD.provider
        OR NEW.payment_app_id IS DISTINCT FROM OLD.payment_app_id
        OR NEW.payment_merchant_id IS DISTINCT FROM OLD.payment_merchant_id
        OR NEW.secret_bundle_ref IS DISTINCT FROM OLD.secret_bundle_ref
        OR NEW.provider_certificate_serial IS DISTINCT FROM OLD.provider_certificate_serial
        OR NEW.provider_public_key_id IS DISTINCT FROM OLD.provider_public_key_id
        OR NEW.notify_url IS DISTINCT FROM OLD.notify_url
        OR NEW.authorization_version IS DISTINCT FROM OLD.authorization_version
        OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'xiangwan merchant config generation identity is immutable'
            USING ERRCODE = '23514';
    END IF;
    IF NEW.updated_at < OLD.updated_at THEN
        RAISE EXCEPTION 'xiangwan merchant config generation update time must advance'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_xiangwan_payment_merchant_config_generation_guard
    ON xiangwan_payment_merchant_config_generations;
CREATE TRIGGER trg_xiangwan_payment_merchant_config_generation_guard
    BEFORE UPDATE ON xiangwan_payment_merchant_config_generations
    FOR EACH ROW EXECUTE FUNCTION
        xiangwan_guard_payment_merchant_config_generation_mutation();

CREATE OR REPLACE FUNCTION xiangwan_validate_payment_close_job_order_generation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM xiangwan_orders AS payment_order
        WHERE payment_order.tenant_id = NEW.tenant_id
          AND payment_order.id = NEW.order_id
          AND payment_order.merchant_config_generation_id
              IS NOT DISTINCT FROM NEW.merchant_config_generation_id
    ) THEN
        RAISE EXCEPTION 'xiangwan payment close job generation does not match Order'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_xiangwan_payment_close_job_order_generation
    ON xiangwan_payment_close_jobs;
CREATE TRIGGER trg_xiangwan_payment_close_job_order_generation
    BEFORE INSERT OR UPDATE OF order_id, tenant_id, merchant_config_generation_id
    ON xiangwan_payment_close_jobs
    FOR EACH ROW EXECUTE FUNCTION
        xiangwan_validate_payment_close_job_order_generation();

CREATE INDEX IF NOT EXISTS idx_xiangwan_orders_merchant_config_generation
    ON xiangwan_orders (tenant_id, merchant_config_generation_id)
    WHERE merchant_config_generation_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_xiangwan_payment_close_jobs_merchant_config_generation
    ON xiangwan_payment_close_jobs (tenant_id, merchant_config_generation_id)
    WHERE merchant_config_generation_id IS NOT NULL;

-- Existing close jobs remain legacy-static when their order has no generation
-- snapshot. If a snapshot already exists, copy it once and never overwrite it.
UPDATE xiangwan_payment_close_jobs AS close_job
SET merchant_config_generation_id = payment_order.merchant_config_generation_id
FROM xiangwan_orders AS payment_order
WHERE close_job.tenant_id = payment_order.tenant_id
  AND close_job.order_id = payment_order.id
  AND close_job.merchant_config_generation_id IS NULL
  AND payment_order.merchant_config_generation_id IS NOT NULL;

CREATE OR REPLACE FUNCTION xiangwan_guard_order_mutation()
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
        OR NEW.idempotency_key IS DISTINCT FROM OLD.idempotency_key
        OR NEW.merchant_order_no IS DISTINCT FROM OLD.merchant_order_no
        OR NEW.payment_app_id IS DISTINCT FROM OLD.payment_app_id
        OR NEW.payment_merchant_id IS DISTINCT FROM OLD.payment_merchant_id
        OR NEW.merchant_config_generation_id IS DISTINCT FROM OLD.merchant_config_generation_id
        OR NEW.original_price_cents IS DISTINCT FROM OLD.original_price_cents
        OR NEW.discount_cents IS DISTINCT FROM OLD.discount_cents
        OR NEW.payable_cents IS DISTINCT FROM OLD.payable_cents
        OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'xiangwan Order identity and amount snapshot are immutable'
            USING ERRCODE = '23514';
    END IF;
    IF NEW.version <> OLD.version + 1 OR NEW.updated_at < OLD.updated_at THEN
        RAISE EXCEPTION 'xiangwan Order version/time must advance'
            USING ERRCODE = '23514';
    END IF;
    IF NOT (
        (
            OLD.payment_status = 'pending'
            AND NEW.payment_status IN (
                'pending',
                'unknown',
                'paid_confirmed',
                'settled_zero',
                'closed_unpaid'
            )
        )
        OR (
            OLD.payment_status = 'unknown'
            AND NEW.payment_status IN (
                'unknown', 'paid_confirmed', 'closed_unpaid'
            )
        )
        OR (
            OLD.payment_status = 'closed_unpaid'
            AND NEW.payment_status IN ('closed_unpaid', 'paid_confirmed')
        )
        OR (
            OLD.payment_status = 'paid_confirmed'
            AND NEW.payment_status = 'paid_confirmed'
        )
        OR (
            OLD.payment_status = 'settled_zero'
            AND NEW.payment_status = 'settled_zero'
        )
    ) THEN
        RAISE EXCEPTION 'xiangwan Order payment transition is invalid'
            USING ERRCODE = '23514';
    END IF;
    IF OLD.actual_paid_cents IS NOT NULL
        AND (
            NEW.actual_paid_cents IS DISTINCT FROM OLD.actual_paid_cents
            OR NEW.wechat_transaction_id IS DISTINCT FROM OLD.wechat_transaction_id
            OR NEW.paid_at IS DISTINCT FROM OLD.paid_at
        ) THEN
        RAISE EXCEPTION 'xiangwan Order paid fact is immutable'
            USING ERRCODE = '23514';
    END IF;
    IF NEW.closed_at IS DISTINCT FROM OLD.closed_at
        AND NOT (
            OLD.closed_at IS NULL
            AND NEW.payment_status = 'closed_unpaid'
            AND NEW.closed_at IS NOT NULL
        ) THEN
        RAISE EXCEPTION 'xiangwan Order closed_at is immutable'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

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
        OR NEW.merchant_config_generation_id IS DISTINCT FROM OLD.merchant_config_generation_id
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
            merchant_config_generation_id,
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
            NEW.merchant_config_generation_id,
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
