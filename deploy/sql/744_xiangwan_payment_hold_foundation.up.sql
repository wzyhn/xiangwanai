-- 744_xiangwan_payment_hold_foundation.up.sql
-- Persist the independent Order/payment axis and ten-minute Session capacity
-- holds. PostgreSQL constraints are the concurrency backstop; Redis is unused.

ALTER TABLE xiangwan_registrations
    DROP CONSTRAINT IF EXISTS xiangwan_registrations_payment_identity_key,
    ADD CONSTRAINT xiangwan_registrations_payment_identity_key
        UNIQUE (tenant_id, series_id, instance_id, session_id, principal_id, id);

CREATE TABLE IF NOT EXISTS xiangwan_orders (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    registration_id UUID NOT NULL,
    series_id UUID NOT NULL,
    instance_id UUID NOT NULL,
    session_id UUID NOT NULL,
    principal_id UUID NOT NULL REFERENCES principals(id),
    payment_status VARCHAR(24) NOT NULL,
    idempotency_key VARCHAR(128) NOT NULL,
    merchant_order_no VARCHAR(64) NOT NULL,
    payment_app_id VARCHAR(64) NOT NULL,
    payment_merchant_id VARCHAR(64) NOT NULL,
    original_price_cents BIGINT NOT NULL,
    discount_cents BIGINT NOT NULL,
    payable_cents BIGINT NOT NULL,
    actual_paid_cents BIGINT,
    wechat_transaction_id VARCHAR(128),
    paid_at TIMESTAMPTZ,
    closed_at TIMESTAMPTZ,
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT xiangwan_orders_payment_status_check
        CHECK (payment_status IN ('pending', 'unknown', 'paid_confirmed', 'closed_unpaid')),
    CONSTRAINT xiangwan_orders_identity_text_check
        CHECK (
            idempotency_key ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$'
            AND BTRIM(merchant_order_no) <> ''
            AND BTRIM(payment_app_id) <> ''
            AND BTRIM(payment_merchant_id) <> ''
        ),
    CONSTRAINT xiangwan_orders_amount_snapshot_check
        CHECK (
            original_price_cents > 0
            AND discount_cents >= 0
            AND discount_cents <= original_price_cents
            AND payable_cents = original_price_cents - discount_cents
            AND payable_cents > 0
            AND (actual_paid_cents IS NULL OR actual_paid_cents >= 0)
        ),
    CONSTRAINT xiangwan_orders_state_shape_check
        CHECK (
            (
                payment_status IN ('pending', 'unknown')
                AND actual_paid_cents IS NULL
                AND wechat_transaction_id IS NULL
                AND paid_at IS NULL
                AND closed_at IS NULL
            )
            OR (
                payment_status = 'paid_confirmed'
                AND actual_paid_cents = payable_cents
                AND BTRIM(COALESCE(wechat_transaction_id, '')) <> ''
                AND paid_at IS NOT NULL
            )
            OR (
                payment_status = 'closed_unpaid'
                AND actual_paid_cents IS NULL
                AND wechat_transaction_id IS NULL
                AND paid_at IS NULL
                AND closed_at IS NOT NULL
            )
        ),
    CONSTRAINT xiangwan_orders_timestamp_order_check
        CHECK (
            updated_at >= created_at
            AND (paid_at IS NULL OR paid_at >= created_at)
            AND (closed_at IS NULL OR closed_at >= created_at)
        ),
    CONSTRAINT xiangwan_orders_version_check
        CHECK (version >= 1),
    CONSTRAINT xiangwan_orders_registration_identity_fkey
        FOREIGN KEY (
            tenant_id, series_id, instance_id, session_id, principal_id, registration_id
        )
        REFERENCES xiangwan_registrations (
            tenant_id, series_id, instance_id, session_id, principal_id, id
        ),
    CONSTRAINT xiangwan_orders_tenant_id_id_key
        UNIQUE (tenant_id, id),
    CONSTRAINT xiangwan_orders_tenant_registration_key
        UNIQUE (tenant_id, registration_id),
    CONSTRAINT xiangwan_orders_tenant_idempotency_key
        UNIQUE (tenant_id, idempotency_key),
    CONSTRAINT xiangwan_orders_merchant_order_key
        UNIQUE (payment_merchant_id, merchant_order_no),
    CONSTRAINT xiangwan_orders_hold_identity_key
        UNIQUE (tenant_id, registration_id, session_id, id)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_xiangwan_orders_wechat_transaction
    ON xiangwan_orders (payment_merchant_id, wechat_transaction_id)
    WHERE wechat_transaction_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_xiangwan_orders_principal_created
    ON xiangwan_orders (tenant_id, principal_id, created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS idx_xiangwan_orders_session_status
    ON xiangwan_orders (tenant_id, session_id, payment_status, created_at, id);

CREATE TABLE IF NOT EXISTS xiangwan_capacity_holds (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    order_id UUID NOT NULL,
    registration_id UUID NOT NULL,
    session_id UUID NOT NULL,
    hold_status VARCHAR(16) NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    converted_at TIMESTAMPTZ,
    released_at TIMESTAMPTZ,
    release_reason VARCHAR(128),
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT xiangwan_capacity_holds_status_check
        CHECK (hold_status IN ('active', 'converted', 'released', 'expired')),
    CONSTRAINT xiangwan_capacity_holds_duration_check
        CHECK (expires_at = created_at + INTERVAL '10 minutes'),
    CONSTRAINT xiangwan_capacity_holds_state_shape_check
        CHECK (
            (
                hold_status = 'active'
                AND converted_at IS NULL
                AND released_at IS NULL
                AND release_reason IS NULL
            )
            OR (
                hold_status = 'converted'
                AND converted_at IS NOT NULL
                AND converted_at < expires_at
                AND released_at IS NULL
                AND release_reason IS NULL
            )
            OR (
                hold_status = 'released'
                AND converted_at IS NULL
                AND released_at IS NOT NULL
                AND BTRIM(COALESCE(release_reason, '')) <> ''
                AND released_at < expires_at
            )
            OR (
                hold_status = 'expired'
                AND converted_at IS NULL
                AND released_at IS NOT NULL
                AND BTRIM(COALESCE(release_reason, '')) <> ''
                AND released_at >= expires_at
            )
        ),
    CONSTRAINT xiangwan_capacity_holds_timestamp_order_check
        CHECK (
            updated_at >= created_at
            AND expires_at > created_at
            AND (converted_at IS NULL OR converted_at >= created_at)
            AND (released_at IS NULL OR released_at >= created_at)
        ),
    CONSTRAINT xiangwan_capacity_holds_version_check
        CHECK (version >= 1),
    CONSTRAINT xiangwan_holds_order_identity_fkey
        FOREIGN KEY (tenant_id, registration_id, session_id, order_id)
        REFERENCES xiangwan_orders (tenant_id, registration_id, session_id, id),
    CONSTRAINT xiangwan_holds_session_fkey
        FOREIGN KEY (tenant_id, session_id)
        REFERENCES xiangwan_activity_sessions (tenant_id, id),
    CONSTRAINT xiangwan_holds_tenant_id_id_key
        UNIQUE (tenant_id, id),
    CONSTRAINT xiangwan_holds_tenant_order_key
        UNIQUE (tenant_id, order_id)
);

CREATE INDEX IF NOT EXISTS idx_xiangwan_capacity_holds_active_expiry
    ON xiangwan_capacity_holds (expires_at, tenant_id, session_id, id)
    WHERE hold_status = 'active';

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
        (OLD.payment_status = 'pending'
            AND NEW.payment_status IN ('pending', 'unknown', 'paid_confirmed', 'closed_unpaid'))
        OR (OLD.payment_status = 'unknown'
            AND NEW.payment_status IN ('unknown', 'paid_confirmed', 'closed_unpaid'))
        OR (OLD.payment_status = 'closed_unpaid'
            AND NEW.payment_status IN ('closed_unpaid', 'paid_confirmed'))
        OR (OLD.payment_status = 'paid_confirmed'
            AND NEW.payment_status = 'paid_confirmed')
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

CREATE OR REPLACE FUNCTION xiangwan_guard_capacity_hold_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
        OR NEW.order_id IS DISTINCT FROM OLD.order_id
        OR NEW.registration_id IS DISTINCT FROM OLD.registration_id
        OR NEW.session_id IS DISTINCT FROM OLD.session_id
        OR NEW.expires_at IS DISTINCT FROM OLD.expires_at
        OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'xiangwan capacity hold identity is immutable'
            USING ERRCODE = '23514';
    END IF;
    IF NEW.version <> OLD.version + 1 OR NEW.updated_at < OLD.updated_at THEN
        RAISE EXCEPTION 'xiangwan capacity hold version/time must advance'
            USING ERRCODE = '23514';
    END IF;
    IF OLD.hold_status <> 'active' AND NEW.hold_status <> OLD.hold_status THEN
        RAISE EXCEPTION 'xiangwan terminal capacity hold cannot transition'
            USING ERRCODE = '23514';
    END IF;
    IF OLD.converted_at IS NOT NULL
        AND NEW.converted_at IS DISTINCT FROM OLD.converted_at THEN
        RAISE EXCEPTION 'xiangwan capacity hold conversion fact is immutable'
            USING ERRCODE = '23514';
    END IF;
    IF OLD.released_at IS NOT NULL
        AND (
            NEW.released_at IS DISTINCT FROM OLD.released_at
            OR NEW.release_reason IS DISTINCT FROM OLD.release_reason
        ) THEN
        RAISE EXCEPTION 'xiangwan capacity hold release fact is immutable'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION xiangwan_reject_commerce_removal()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'xiangwan Order/hold facts cannot be physically removed'
        USING ERRCODE = '23514';
END;
$$;

DROP TRIGGER IF EXISTS trg_xiangwan_orders_guard_mutation ON xiangwan_orders;
CREATE TRIGGER trg_xiangwan_orders_guard_mutation
    BEFORE UPDATE ON xiangwan_orders
    FOR EACH ROW EXECUTE FUNCTION xiangwan_guard_order_mutation();

DROP TRIGGER IF EXISTS trg_xiangwan_holds_guard_mutation ON xiangwan_capacity_holds;
CREATE TRIGGER trg_xiangwan_holds_guard_mutation
    BEFORE UPDATE ON xiangwan_capacity_holds
    FOR EACH ROW EXECUTE FUNCTION xiangwan_guard_capacity_hold_mutation();

DROP TRIGGER IF EXISTS trg_xiangwan_orders_no_delete ON xiangwan_orders;
CREATE TRIGGER trg_xiangwan_orders_no_delete
    BEFORE DELETE ON xiangwan_orders
    FOR EACH ROW EXECUTE FUNCTION xiangwan_reject_commerce_removal();

DROP TRIGGER IF EXISTS trg_xiangwan_orders_no_truncate ON xiangwan_orders;
CREATE TRIGGER trg_xiangwan_orders_no_truncate
    BEFORE TRUNCATE ON xiangwan_orders
    FOR EACH STATEMENT EXECUTE FUNCTION xiangwan_reject_commerce_removal();

DROP TRIGGER IF EXISTS trg_xiangwan_holds_no_delete ON xiangwan_capacity_holds;
CREATE TRIGGER trg_xiangwan_holds_no_delete
    BEFORE DELETE ON xiangwan_capacity_holds
    FOR EACH ROW EXECUTE FUNCTION xiangwan_reject_commerce_removal();

DROP TRIGGER IF EXISTS trg_xiangwan_holds_no_truncate ON xiangwan_capacity_holds;
CREATE TRIGGER trg_xiangwan_holds_no_truncate
    BEFORE TRUNCATE ON xiangwan_capacity_holds
    FOR EACH STATEMENT EXECUTE FUNCTION xiangwan_reject_commerce_removal();
