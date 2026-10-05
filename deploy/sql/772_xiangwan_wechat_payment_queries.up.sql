-- 772_xiangwan_wechat_payment_queries.up.sql
-- PostgreSQL-coordinated merchant-order queries and immutable trusted payment
-- observations. Provider calls run outside database transactions; no Redis is
-- used for leases, throttling, or financial truth.

CREATE TABLE IF NOT EXISTS xiangwan_payment_query_leases (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    order_id UUID NOT NULL,
    principal_id UUID NOT NULL REFERENCES principals(id),
    generation_id UUID NOT NULL,
    payment_app_id VARCHAR(64) NOT NULL,
    payment_merchant_id VARCHAR(64) NOT NULL,
    out_trade_no VARCHAR(32) NOT NULL,
    amount_cents BIGINT NOT NULL,
    currency CHAR(3) NOT NULL,
    query_status VARCHAR(16) NOT NULL,
    owner_token UUID,
    lease_expires_at TIMESTAMPTZ,
    next_query_at TIMESTAMPTZ,
    last_trade_state VARCHAR(16),
    last_error_class VARCHAR(64),
    last_provider_request_id VARCHAR(128),
    completed_at TIMESTAMPTZ,
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT xiangwan_payment_query_leases_identity_check
        CHECK (
            out_trade_no ~ '^[0-9A-Za-z_|*-]{6,32}$'
            AND BTRIM(payment_app_id) = payment_app_id
            AND CHAR_LENGTH(payment_app_id) BETWEEN 3 AND 64
            AND BTRIM(payment_merchant_id) = payment_merchant_id
            AND CHAR_LENGTH(payment_merchant_id) BETWEEN 6 AND 64
            AND amount_cents > 0
            AND currency = 'CNY'
        ),
    CONSTRAINT xiangwan_payment_query_leases_status_check
        CHECK (query_status IN ('in_progress', 'pending', 'unknown', 'converged')),
    CONSTRAINT xiangwan_payment_query_leases_state_shape_check
        CHECK (
            (
                query_status = 'in_progress'
                AND owner_token IS NOT NULL
                AND lease_expires_at IS NOT NULL
                AND next_query_at IS NULL
                AND last_trade_state IS NULL
                AND last_error_class IS NULL
                AND last_provider_request_id IS NULL
                AND completed_at IS NULL
            )
            OR (
                query_status = 'pending'
                AND owner_token IS NULL
                AND lease_expires_at IS NULL
                AND next_query_at IS NOT NULL
                AND last_trade_state IN (
                    'NOTPAY', 'USERPAYING', 'CLOSED', 'REVOKED', 'PAYERROR', 'REFUND'
                )
                AND last_error_class IS NULL
                AND completed_at IS NULL
            )
            OR (
                query_status = 'unknown'
                AND owner_token IS NULL
                AND lease_expires_at IS NULL
                AND next_query_at IS NOT NULL
                AND last_trade_state IS NULL
                AND last_error_class ~ '^[a-z][a-z0-9_]{0,63}$'
                AND completed_at IS NULL
            )
            OR (
                query_status = 'converged'
                AND owner_token IS NULL
                AND lease_expires_at IS NULL
                AND next_query_at IS NULL
                AND last_trade_state = 'SUCCESS'
                AND last_error_class IS NULL
                AND completed_at IS NOT NULL
            )
        ),
    CONSTRAINT xiangwan_payment_query_leases_provider_request_check
        CHECK (
            last_provider_request_id IS NULL
            OR (
                BTRIM(last_provider_request_id) = last_provider_request_id
                AND CHAR_LENGTH(last_provider_request_id) BETWEEN 1 AND 128
            )
        ),
    CONSTRAINT xiangwan_payment_query_leases_time_check
        CHECK (
            updated_at >= created_at
            AND (lease_expires_at IS NULL OR lease_expires_at > updated_at)
            AND (next_query_at IS NULL OR next_query_at >= updated_at)
            AND (
                completed_at IS NULL
                OR (completed_at >= created_at AND updated_at >= completed_at)
            )
        ),
    CONSTRAINT xiangwan_payment_query_leases_version_check
        CHECK (version >= 1),
    CONSTRAINT xiangwan_payment_query_leases_order_identity_fkey
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
    CONSTRAINT xiangwan_payment_query_leases_tenant_order_key
        UNIQUE (tenant_id, order_id)
);

CREATE INDEX IF NOT EXISTS idx_xiangwan_payment_query_leases_due
    ON xiangwan_payment_query_leases (
        COALESCE(lease_expires_at, next_query_at), tenant_id, order_id
    )
    WHERE query_status IN ('in_progress', 'pending', 'unknown');

CREATE TABLE IF NOT EXISTS xiangwan_payment_transaction_observations (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    order_id UUID NOT NULL,
    principal_id UUID NOT NULL REFERENCES principals(id),
    observation_source VARCHAR(24) NOT NULL,
    source_key VARCHAR(128) NOT NULL,
    provider_request_id VARCHAR(128),
    payment_app_id VARCHAR(64) NOT NULL,
    payment_merchant_id VARCHAR(64) NOT NULL,
    out_trade_no VARCHAR(32) NOT NULL,
    transaction_id VARCHAR(128),
    trade_type VARCHAR(16) NOT NULL,
    trade_state VARCHAR(16) NOT NULL,
    amount_cents BIGINT NOT NULL,
    currency CHAR(3) NOT NULL,
    success_at TIMESTAMPTZ,
    payload_digest CHAR(64) NOT NULL,
    observed_at TIMESTAMPTZ NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT xiangwan_payment_transaction_observations_source_check
        CHECK (observation_source IN ('merchant_query', 'payment_notification')),
    CONSTRAINT xiangwan_payment_transaction_observations_identity_check
        CHECK (
            BTRIM(source_key) = source_key
            AND CHAR_LENGTH(source_key) BETWEEN 1 AND 128
            AND out_trade_no ~ '^[0-9A-Za-z_|*-]{6,32}$'
            AND BTRIM(payment_app_id) = payment_app_id
            AND CHAR_LENGTH(payment_app_id) BETWEEN 3 AND 64
            AND BTRIM(payment_merchant_id) = payment_merchant_id
            AND CHAR_LENGTH(payment_merchant_id) BETWEEN 6 AND 64
            AND trade_type = 'JSAPI'
            AND trade_state IN (
                'SUCCESS', 'REFUND', 'NOTPAY', 'CLOSED',
                'REVOKED', 'USERPAYING', 'PAYERROR'
            )
            AND amount_cents > 0
            AND currency = 'CNY'
            AND payload_digest ~ '^[0-9a-f]{64}$'
        ),
    CONSTRAINT xiangwan_payment_transaction_observations_payment_shape_check
        CHECK (
            (
                trade_state = 'SUCCESS'
                AND BTRIM(COALESCE(transaction_id, '')) <> ''
                AND success_at IS NOT NULL
            )
            OR (
                trade_state <> 'SUCCESS'
                AND (transaction_id IS NULL) = (success_at IS NULL)
            )
        ),
    CONSTRAINT xiangwan_payment_transaction_observations_text_check
        CHECK (
            (transaction_id IS NULL OR CHAR_LENGTH(transaction_id) <= 128)
            AND (
                provider_request_id IS NULL
                OR (
                    BTRIM(provider_request_id) = provider_request_id
                    AND CHAR_LENGTH(provider_request_id) BETWEEN 1 AND 128
                )
            )
        ),
    CONSTRAINT xiangwan_payment_transaction_observations_time_check
        CHECK (
            recorded_at >= observed_at
            AND (success_at IS NULL OR observed_at >= success_at)
        ),
    CONSTRAINT xiangwan_payment_transaction_observations_order_identity_fkey
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
    CONSTRAINT xiangwan_payment_transaction_observations_source_key
        UNIQUE (observation_source, payment_merchant_id, source_key)
);

CREATE INDEX IF NOT EXISTS idx_xiangwan_payment_transaction_observations_order_time
    ON xiangwan_payment_transaction_observations (
        tenant_id, order_id, observed_at, id
    );

CREATE OR REPLACE FUNCTION xiangwan_require_eligible_payment_query()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM xiangwan_orders AS payment_order
        JOIN xiangwan_payment_attempts AS payment_attempt
          ON payment_attempt.tenant_id = payment_order.tenant_id
         AND payment_attempt.order_id = payment_order.id
         AND payment_attempt.principal_id = payment_order.principal_id
         AND payment_attempt.payment_app_id = payment_order.payment_app_id
         AND payment_attempt.payment_merchant_id = payment_order.payment_merchant_id
         AND payment_attempt.out_trade_no = payment_order.merchant_order_no
         AND payment_attempt.amount_cents = payment_order.payable_cents
        JOIN xiangwan_runtime_generations AS runtime_generation
          ON runtime_generation.tenant_id = payment_order.tenant_id
         AND runtime_generation.singleton_id = 1
         AND runtime_generation.scope_key = 'wq-xiangwan'
        WHERE payment_order.tenant_id = NEW.tenant_id
          AND payment_order.id = NEW.order_id
          AND payment_order.principal_id = NEW.principal_id
          AND payment_order.payment_status IN ('pending', 'unknown')
          AND payment_attempt.attempt_status IN ('ready', 'unknown')
          AND runtime_generation.active_generation_id = NEW.generation_id
          AND runtime_generation.write_epoch > 0
    ) THEN
        RAISE EXCEPTION 'xiangwan payment query requires an eligible local payment state'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION xiangwan_guard_payment_query_lease_mutation()
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
        OR NEW.currency IS DISTINCT FROM OLD.currency
        OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'xiangwan payment query identity is immutable'
            USING ERRCODE = '23514';
    END IF;
    IF OLD.query_status = 'converged' THEN
        RAISE EXCEPTION 'xiangwan converged payment query is immutable'
            USING ERRCODE = '23514';
    END IF;
    IF NEW.version <> OLD.version + 1 OR NEW.updated_at < OLD.updated_at THEN
        RAISE EXCEPTION 'xiangwan payment query version/time must advance'
            USING ERRCODE = '23514';
    END IF;
    IF NEW.query_status = 'in_progress' AND (
        (OLD.query_status = 'in_progress' AND NEW.updated_at < OLD.lease_expires_at)
        OR (
            OLD.query_status IN ('pending', 'unknown')
            AND NEW.updated_at < OLD.next_query_at
        )
    ) THEN
        RAISE EXCEPTION 'xiangwan payment query lease is not due'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_xiangwan_payment_query_eligibility_insert
    ON xiangwan_payment_query_leases;
CREATE TRIGGER trg_xiangwan_payment_query_eligibility_insert
    BEFORE INSERT ON xiangwan_payment_query_leases
    FOR EACH ROW EXECUTE FUNCTION xiangwan_require_eligible_payment_query();

DROP TRIGGER IF EXISTS trg_xiangwan_payment_query_eligibility_acquire
    ON xiangwan_payment_query_leases;
CREATE TRIGGER trg_xiangwan_payment_query_eligibility_acquire
    BEFORE UPDATE ON xiangwan_payment_query_leases
    FOR EACH ROW
    WHEN (NEW.query_status = 'in_progress')
    EXECUTE FUNCTION xiangwan_require_eligible_payment_query();

DROP TRIGGER IF EXISTS trg_xiangwan_payment_query_guard_mutation
    ON xiangwan_payment_query_leases;
CREATE TRIGGER trg_xiangwan_payment_query_guard_mutation
    BEFORE UPDATE ON xiangwan_payment_query_leases
    FOR EACH ROW EXECUTE FUNCTION xiangwan_guard_payment_query_lease_mutation();

DROP TRIGGER IF EXISTS trg_xiangwan_payment_query_no_delete
    ON xiangwan_payment_query_leases;
CREATE TRIGGER trg_xiangwan_payment_query_no_delete
    BEFORE DELETE ON xiangwan_payment_query_leases
    FOR EACH ROW EXECUTE FUNCTION xiangwan_reject_payment_evidence_mutation();

DROP TRIGGER IF EXISTS trg_xiangwan_payment_query_no_truncate
    ON xiangwan_payment_query_leases;
CREATE TRIGGER trg_xiangwan_payment_query_no_truncate
    BEFORE TRUNCATE ON xiangwan_payment_query_leases
    FOR EACH STATEMENT EXECUTE FUNCTION xiangwan_reject_payment_evidence_mutation();

DROP TRIGGER IF EXISTS trg_xiangwan_payment_transaction_observation_immutable
    ON xiangwan_payment_transaction_observations;
CREATE TRIGGER trg_xiangwan_payment_transaction_observation_immutable
    BEFORE UPDATE OR DELETE ON xiangwan_payment_transaction_observations
    FOR EACH ROW EXECUTE FUNCTION xiangwan_reject_payment_evidence_mutation();

DROP TRIGGER IF EXISTS trg_xiangwan_payment_transaction_observation_no_truncate
    ON xiangwan_payment_transaction_observations;
CREATE TRIGGER trg_xiangwan_payment_transaction_observation_no_truncate
    BEFORE TRUNCATE ON xiangwan_payment_transaction_observations
    FOR EACH STATEMENT EXECUTE FUNCTION xiangwan_reject_payment_evidence_mutation();
