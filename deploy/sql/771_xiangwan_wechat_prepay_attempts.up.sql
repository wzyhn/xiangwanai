-- 771_xiangwan_wechat_prepay_attempts.up.sql
-- Recoverable three-phase WeChat prepay operations. PostgreSQL owns the
-- single-winner lease and immutable observations; provider calls happen with
-- no business row lock held and no Redis coordination.

ALTER TABLE xiangwan_orders
    DROP CONSTRAINT IF EXISTS xiangwan_orders_prepay_identity_key,
    ADD CONSTRAINT xiangwan_orders_prepay_identity_key
        UNIQUE (
            tenant_id,
            id,
            principal_id,
            payment_app_id,
            payment_merchant_id,
            merchant_order_no,
            payable_cents
        );

CREATE TABLE IF NOT EXISTS xiangwan_payment_attempts (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    order_id UUID NOT NULL,
    principal_id UUID NOT NULL REFERENCES principals(id),
    generation_id UUID NOT NULL,
    operation_kind VARCHAR(32) NOT NULL,
    idempotency_key UUID NOT NULL,
    request_fingerprint CHAR(64) NOT NULL,
    provider VARCHAR(16) NOT NULL,
    out_trade_no VARCHAR(32) NOT NULL,
    payment_app_id VARCHAR(64) NOT NULL,
    payment_merchant_id VARCHAR(64) NOT NULL,
    description VARCHAR(127) NOT NULL,
    notify_url VARCHAR(2048) NOT NULL,
    order_version BIGINT NOT NULL,
    amount_cents BIGINT NOT NULL,
    currency CHAR(3) NOT NULL,
    attempt_status VARCHAR(16) NOT NULL,
    owner_token UUID,
    lease_expires_at TIMESTAMPTZ,
    prepay_id VARCHAR(64),
    client_timestamp VARCHAR(16),
    client_nonce VARCHAR(64),
    client_package VARCHAR(80),
    client_sign_type VARCHAR(16),
    client_pay_sign TEXT,
    provider_request_id VARCHAR(128),
    last_error_class VARCHAR(64),
    completed_at TIMESTAMPTZ,
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT xiangwan_payment_attempts_kind_check
        CHECK (operation_kind = 'wechat_prepay'),
    CONSTRAINT xiangwan_payment_attempts_provider_check
        CHECK (provider = 'wechat'),
    CONSTRAINT xiangwan_payment_attempts_identity_check
        CHECK (
            request_fingerprint ~ '^[0-9a-f]{64}$'
            AND out_trade_no ~ '^[0-9A-Za-z_|*-]{6,32}$'
            AND BTRIM(payment_app_id) = payment_app_id
            AND CHAR_LENGTH(payment_app_id) BETWEEN 3 AND 64
            AND BTRIM(payment_merchant_id) = payment_merchant_id
            AND CHAR_LENGTH(payment_merchant_id) BETWEEN 6 AND 64
            AND BTRIM(description) = description
            AND CHAR_LENGTH(description) BETWEEN 1 AND 127
            AND BTRIM(notify_url) = notify_url
            AND CHAR_LENGTH(notify_url) BETWEEN 1 AND 2048
            AND order_version >= 1
            AND amount_cents > 0
            AND currency = 'CNY'
        ),
    CONSTRAINT xiangwan_payment_attempts_status_check
        CHECK (attempt_status IN ('in_progress', 'ready', 'unknown')),
    CONSTRAINT xiangwan_payment_attempts_state_shape_check
        CHECK (
            (
                attempt_status = 'in_progress'
                AND owner_token IS NOT NULL
                AND lease_expires_at IS NOT NULL
                AND prepay_id IS NULL
                AND client_timestamp IS NULL
                AND client_nonce IS NULL
                AND client_package IS NULL
                AND client_sign_type IS NULL
                AND client_pay_sign IS NULL
                AND provider_request_id IS NULL
                AND last_error_class IS NULL
                AND completed_at IS NULL
            )
            OR (
                attempt_status = 'ready'
                AND owner_token IS NULL
                AND lease_expires_at IS NULL
                AND BTRIM(COALESCE(prepay_id, '')) <> ''
                AND client_timestamp ~ '^[0-9]{10,13}$'
                AND BTRIM(COALESCE(client_nonce, '')) <> ''
                AND client_package = 'prepay_id=' || prepay_id
                AND client_sign_type = 'RSA'
                AND BTRIM(COALESCE(client_pay_sign, '')) <> ''
                AND last_error_class IS NULL
                AND completed_at IS NOT NULL
            )
            OR (
                attempt_status = 'unknown'
                AND owner_token IS NULL
                AND lease_expires_at IS NULL
                AND prepay_id IS NULL
                AND client_timestamp IS NULL
                AND client_nonce IS NULL
                AND client_package IS NULL
                AND client_sign_type IS NULL
                AND client_pay_sign IS NULL
                AND BTRIM(COALESCE(last_error_class, '')) <> ''
                AND completed_at IS NOT NULL
            )
        ),
    CONSTRAINT xiangwan_payment_attempts_text_bounds_check
        CHECK (
            (prepay_id IS NULL OR CHAR_LENGTH(prepay_id) <= 64)
            AND (client_nonce IS NULL OR CHAR_LENGTH(client_nonce) <= 64)
            AND (client_pay_sign IS NULL OR CHAR_LENGTH(client_pay_sign) <= 1024)
            AND (
                provider_request_id IS NULL
                OR (
                    BTRIM(provider_request_id) = provider_request_id
                    AND CHAR_LENGTH(provider_request_id) BETWEEN 1 AND 128
                )
            )
            AND (
                last_error_class IS NULL
                OR last_error_class ~ '^[a-z][a-z0-9_]{0,63}$'
            )
        ),
    CONSTRAINT xiangwan_payment_attempts_time_check
        CHECK (
            updated_at >= created_at
            AND (
                lease_expires_at IS NULL
                OR lease_expires_at > updated_at
            )
            AND (
                completed_at IS NULL
                OR (
                    completed_at >= created_at
                    AND updated_at >= completed_at
                )
            )
        ),
    CONSTRAINT xiangwan_payment_attempts_version_check
        CHECK (version >= 1),
    CONSTRAINT xiangwan_payment_attempts_order_identity_fkey
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
    CONSTRAINT xiangwan_payment_attempts_tenant_id_id_order_key
        UNIQUE (tenant_id, id, order_id),
    CONSTRAINT xiangwan_payment_attempts_tenant_order_key
        UNIQUE (tenant_id, order_id),
    CONSTRAINT xiangwan_payment_attempts_operation_key
        UNIQUE (tenant_id, principal_id, operation_kind, idempotency_key),
    CONSTRAINT xiangwan_payment_attempts_provider_business_key
        UNIQUE (payment_merchant_id, out_trade_no)
);

CREATE INDEX IF NOT EXISTS idx_xiangwan_payment_attempts_in_progress_lease
    ON xiangwan_payment_attempts (lease_expires_at, tenant_id, id)
    WHERE attempt_status = 'in_progress';

CREATE INDEX IF NOT EXISTS idx_xiangwan_payment_attempts_order_status
    ON xiangwan_payment_attempts (tenant_id, order_id, attempt_status, updated_at);

CREATE TABLE IF NOT EXISTS xiangwan_payment_observations (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    attempt_id UUID NOT NULL,
    order_id UUID NOT NULL,
    invocation_token UUID NOT NULL,
    observation_kind VARCHAR(32) NOT NULL,
    provider_request_id VARCHAR(128),
    provider_code VARCHAR(64),
    prepay_id_digest CHAR(64),
    error_class VARCHAR(64),
    observed_at TIMESTAMPTZ NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT xiangwan_payment_observations_kind_check
        CHECK (observation_kind IN ('prepay_ready', 'prepay_ambiguous')),
    CONSTRAINT xiangwan_payment_observations_shape_check
        CHECK (
            (
                observation_kind = 'prepay_ready'
                AND prepay_id_digest ~ '^[0-9a-f]{64}$'
                AND error_class IS NULL
            )
            OR (
                observation_kind = 'prepay_ambiguous'
                AND prepay_id_digest IS NULL
                AND error_class ~ '^[a-z][a-z0-9_]{0,63}$'
            )
        ),
    CONSTRAINT xiangwan_payment_observations_text_check
        CHECK (
            (
                provider_request_id IS NULL
                OR (
                    BTRIM(provider_request_id) = provider_request_id
                    AND CHAR_LENGTH(provider_request_id) BETWEEN 1 AND 128
                )
            )
            AND (
                provider_code IS NULL
                OR provider_code ~ '^[A-Z][A-Z0-9_]{0,63}$'
            )
        ),
    CONSTRAINT xiangwan_payment_observations_time_check
        CHECK (recorded_at >= observed_at),
    CONSTRAINT xiangwan_payment_observations_attempt_fkey
        FOREIGN KEY (tenant_id, attempt_id, order_id)
        REFERENCES xiangwan_payment_attempts (tenant_id, id, order_id),
    CONSTRAINT xiangwan_payment_observations_invocation_key
        UNIQUE (tenant_id, attempt_id, invocation_token)
);

CREATE INDEX IF NOT EXISTS idx_xiangwan_payment_observations_order_time
    ON xiangwan_payment_observations (tenant_id, order_id, observed_at, id);

CREATE OR REPLACE FUNCTION xiangwan_require_eligible_prepay_attempt()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM xiangwan_orders AS payment_order
        JOIN xiangwan_registrations AS registration
          ON registration.tenant_id = payment_order.tenant_id
         AND registration.id = payment_order.registration_id
        JOIN xiangwan_capacity_holds AS capacity_hold
          ON capacity_hold.tenant_id = payment_order.tenant_id
         AND capacity_hold.order_id = payment_order.id
        JOIN xiangwan_runtime_generations AS runtime_generation
          ON runtime_generation.tenant_id = payment_order.tenant_id
         AND runtime_generation.singleton_id = 1
         AND runtime_generation.scope_key = 'wq-xiangwan'
        WHERE payment_order.tenant_id = NEW.tenant_id
          AND payment_order.id = NEW.order_id
          AND payment_order.principal_id = NEW.principal_id
          AND payment_order.payment_status = 'pending'
          AND payment_order.actual_paid_cents IS NULL
          AND registration.participation_status = 'pending_payment'
          AND capacity_hold.hold_status = 'active'
          AND capacity_hold.expires_at > clock_timestamp()
          AND runtime_generation.active_generation_id = NEW.generation_id
          AND runtime_generation.write_epoch > 0
    ) THEN
        RAISE EXCEPTION 'xiangwan prepay attempt requires an eligible local payment state'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION xiangwan_guard_payment_attempt_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
        OR NEW.order_id IS DISTINCT FROM OLD.order_id
        OR NEW.principal_id IS DISTINCT FROM OLD.principal_id
        OR NEW.operation_kind IS DISTINCT FROM OLD.operation_kind
        OR NEW.idempotency_key IS DISTINCT FROM OLD.idempotency_key
        OR NEW.request_fingerprint IS DISTINCT FROM OLD.request_fingerprint
        OR NEW.provider IS DISTINCT FROM OLD.provider
        OR NEW.out_trade_no IS DISTINCT FROM OLD.out_trade_no
        OR NEW.payment_app_id IS DISTINCT FROM OLD.payment_app_id
        OR NEW.payment_merchant_id IS DISTINCT FROM OLD.payment_merchant_id
        OR NEW.description IS DISTINCT FROM OLD.description
        OR NEW.notify_url IS DISTINCT FROM OLD.notify_url
        OR NEW.order_version IS DISTINCT FROM OLD.order_version
        OR NEW.amount_cents IS DISTINCT FROM OLD.amount_cents
        OR NEW.currency IS DISTINCT FROM OLD.currency
        OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'xiangwan payment attempt identity is immutable'
            USING ERRCODE = '23514';
    END IF;
    IF OLD.attempt_status <> 'in_progress' THEN
        RAISE EXCEPTION 'xiangwan completed payment attempt is immutable'
            USING ERRCODE = '23514';
    END IF;
    IF NEW.version <> OLD.version + 1 OR NEW.updated_at < OLD.updated_at THEN
        RAISE EXCEPTION 'xiangwan payment attempt version/time must advance'
            USING ERRCODE = '23514';
    END IF;
    IF NEW.attempt_status = 'in_progress' AND (
        NEW.owner_token IS NOT DISTINCT FROM OLD.owner_token
        OR NEW.generation_id IS NULL
        OR NEW.lease_expires_at <= OLD.lease_expires_at
        OR NEW.updated_at < OLD.lease_expires_at
    ) THEN
        RAISE EXCEPTION 'xiangwan payment attempt takeover requires an expired lease'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION xiangwan_reject_payment_evidence_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'xiangwan payment attempt evidence is append-only'
        USING ERRCODE = '23514';
END;
$$;

CREATE TRIGGER trg_xiangwan_payment_attempt_eligibility_insert
    BEFORE INSERT ON xiangwan_payment_attempts
    FOR EACH ROW EXECUTE FUNCTION xiangwan_require_eligible_prepay_attempt();

CREATE TRIGGER trg_xiangwan_payment_attempt_eligibility_takeover
    BEFORE UPDATE ON xiangwan_payment_attempts
    FOR EACH ROW
    WHEN (NEW.attempt_status = 'in_progress')
    EXECUTE FUNCTION xiangwan_require_eligible_prepay_attempt();

CREATE TRIGGER trg_xiangwan_payment_attempt_guard_mutation
    BEFORE UPDATE ON xiangwan_payment_attempts
    FOR EACH ROW EXECUTE FUNCTION xiangwan_guard_payment_attempt_mutation();

CREATE TRIGGER trg_xiangwan_payment_attempt_no_delete
    BEFORE DELETE ON xiangwan_payment_attempts
    FOR EACH ROW EXECUTE FUNCTION xiangwan_reject_payment_evidence_mutation();

CREATE TRIGGER trg_xiangwan_payment_attempt_no_truncate
    BEFORE TRUNCATE ON xiangwan_payment_attempts
    FOR EACH STATEMENT EXECUTE FUNCTION xiangwan_reject_payment_evidence_mutation();

CREATE TRIGGER trg_xiangwan_payment_observation_immutable
    BEFORE UPDATE OR DELETE ON xiangwan_payment_observations
    FOR EACH ROW EXECUTE FUNCTION xiangwan_reject_payment_evidence_mutation();

CREATE TRIGGER trg_xiangwan_payment_observation_no_truncate
    BEFORE TRUNCATE ON xiangwan_payment_observations
    FOR EACH STATEMENT EXECUTE FUNCTION xiangwan_reject_payment_evidence_mutation();
