-- 766_xiangwan_runtime_generation.up.sql
-- Single-customer authority used by every Xiangwan Runtime readiness and
-- business-route gate. Activation remains an explicit deployment operation.

CREATE TABLE xiangwan_runtime_generations (
    singleton_id SMALLINT PRIMARY KEY DEFAULT 1,
    tenant_id UUID NOT NULL UNIQUE REFERENCES tenants(id),
    active_generation_id UUID,
    generation_epoch BIGINT NOT NULL DEFAULT 0,
    bootstrap_completed_at TIMESTAMPTZ,
    activated_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT xw_runtime_generation_singleton_check
        CHECK (singleton_id = 1),
    CONSTRAINT xw_runtime_generation_epoch_check
        CHECK (generation_epoch >= 0),
    CONSTRAINT xw_runtime_generation_active_check
        CHECK (
            (
                active_generation_id IS NULL
                AND generation_epoch = 0
                AND activated_at IS NULL
            )
            OR (
                active_generation_id IS NOT NULL
                AND generation_epoch > 0
                AND bootstrap_completed_at IS NOT NULL
                AND activated_at IS NOT NULL
            )
        ),
    CONSTRAINT xw_runtime_generation_time_check
        CHECK (
            (
                bootstrap_completed_at IS NULL
                OR bootstrap_completed_at <= updated_at
            )
            AND (
                activated_at IS NULL
                OR (
                    bootstrap_completed_at IS NOT NULL
                    AND bootstrap_completed_at <= activated_at
                    AND activated_at <= updated_at
                )
            )
        )
);

CREATE OR REPLACE FUNCTION xiangwan_guard_runtime_generation_insert()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.active_generation_id IS NOT NULL
       OR NEW.generation_epoch <> 0
       OR NEW.activated_at IS NOT NULL THEN
        RAISE EXCEPTION 'xiangwan Runtime generation must be activated by update'
            USING ERRCODE = '23514';
    END IF;

    NEW.updated_at := clock_timestamp();
    RETURN NEW;
END;
$$;

CREATE TRIGGER trg_xw_runtime_generation_insert_guard
    BEFORE INSERT ON xiangwan_runtime_generations
    FOR EACH ROW
    EXECUTE FUNCTION xiangwan_guard_runtime_generation_insert();

CREATE OR REPLACE FUNCTION xiangwan_guard_runtime_generation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'xiangwan Runtime generation authority cannot be deleted'
            USING ERRCODE = '23514';
    END IF;

    IF NEW.singleton_id <> OLD.singleton_id
       OR NEW.tenant_id <> OLD.tenant_id THEN
        RAISE EXCEPTION 'xiangwan Runtime generation scope is immutable'
            USING ERRCODE = '23514';
    END IF;

    IF OLD.bootstrap_completed_at IS NOT NULL
       AND NEW.bootstrap_completed_at IS DISTINCT FROM OLD.bootstrap_completed_at THEN
        RAISE EXCEPTION 'xiangwan bootstrap completion is immutable'
            USING ERRCODE = '23514';
    END IF;

    IF NEW.active_generation_id IS DISTINCT FROM OLD.active_generation_id THEN
        IF NEW.active_generation_id IS NULL
           OR NEW.generation_epoch <> OLD.generation_epoch + 1
           OR NEW.activated_at IS NULL
           OR NEW.activated_at IS NOT DISTINCT FROM OLD.activated_at THEN
            RAISE EXCEPTION 'invalid xiangwan Runtime generation activation'
                USING ERRCODE = '23514';
        END IF;
    ELSIF NEW.generation_epoch <> OLD.generation_epoch
          OR NEW.activated_at IS DISTINCT FROM OLD.activated_at THEN
        RAISE EXCEPTION 'xiangwan Runtime epoch requires generation activation'
            USING ERRCODE = '23514';
    END IF;

    NEW.updated_at := clock_timestamp();
    RETURN NEW;
END;
$$;

CREATE TRIGGER trg_xw_runtime_generation_guard
    BEFORE UPDATE OR DELETE ON xiangwan_runtime_generations
    FOR EACH ROW EXECUTE FUNCTION xiangwan_guard_runtime_generation();

CREATE OR REPLACE FUNCTION xiangwan_reject_runtime_generation_truncate()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF EXISTS (SELECT 1 FROM xiangwan_runtime_generations) THEN
        RAISE EXCEPTION 'xiangwan Runtime generation authority cannot be truncated'
            USING ERRCODE = '23514';
    END IF;
    RETURN NULL;
END;
$$;

CREATE TRIGGER trg_xw_runtime_generation_no_truncate
    BEFORE TRUNCATE ON xiangwan_runtime_generations
    FOR EACH STATEMENT
    EXECUTE FUNCTION xiangwan_reject_runtime_generation_truncate();
