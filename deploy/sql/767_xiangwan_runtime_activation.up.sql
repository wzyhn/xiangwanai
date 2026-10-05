-- 767_xiangwan_runtime_activation.up.sql
-- Monotonic write-epoch activation and immutable deployment receipts.

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM xiangwan_runtime_generations
        WHERE active_generation_id IS NOT NULL
    ) THEN
        RAISE EXCEPTION 'migration 727 requires an inactive xiangwan Runtime authority'
            USING ERRCODE = '55000';
    END IF;
END;
$$;

DROP TRIGGER trg_xw_runtime_generation_guard
    ON xiangwan_runtime_generations;
DROP TRIGGER trg_xw_runtime_generation_insert_guard
    ON xiangwan_runtime_generations;

ALTER TABLE xiangwan_runtime_generations
    DROP CONSTRAINT xw_runtime_generation_epoch_check,
    DROP CONSTRAINT xw_runtime_generation_active_check;

ALTER TABLE xiangwan_runtime_generations
    RENAME COLUMN generation_epoch TO write_epoch;

ALTER TABLE xiangwan_runtime_generations
    ADD COLUMN scope_key TEXT NOT NULL DEFAULT 'wq-xiangwan',
    ADD COLUMN previous_generation_id UUID,
    ADD COLUMN activated_by TEXT,
    ADD COLUMN activation_reason TEXT,
    ADD CONSTRAINT xw_runtime_generation_scope_check
        CHECK (scope_key = 'wq-xiangwan'),
    ADD CONSTRAINT xw_runtime_generation_write_epoch_check
        CHECK (write_epoch >= 0),
    ADD CONSTRAINT xw_runtime_generation_activation_check
        CHECK (
            (
                active_generation_id IS NULL
                AND write_epoch = 0
                AND previous_generation_id IS NULL
                AND activated_at IS NULL
                AND activated_by IS NULL
                AND activation_reason IS NULL
            )
            OR (
                active_generation_id IS NOT NULL
                AND write_epoch > 0
                AND bootstrap_completed_at IS NOT NULL
                AND activated_at IS NOT NULL
                AND activated_by IS NOT NULL
                AND activated_by = btrim(activated_by)
                AND char_length(activated_by) BETWEEN 1 AND 128
                AND activation_reason IS NOT NULL
                AND activation_reason = btrim(activation_reason)
                AND char_length(activation_reason) BETWEEN 1 AND 500
                AND (
                    (write_epoch = 1 AND previous_generation_id IS NULL)
                    OR (
                        write_epoch > 1
                        AND previous_generation_id IS NOT NULL
                        AND previous_generation_id <> active_generation_id
                    )
                )
            )
        );

CREATE TABLE xiangwan_runtime_generation_activations (
    activation_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    scope_key TEXT NOT NULL,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    write_epoch BIGINT NOT NULL,
    generation_id UUID NOT NULL,
    previous_generation_id UUID,
    activated_at TIMESTAMPTZ NOT NULL,
    activated_by TEXT NOT NULL,
    reason TEXT NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT xw_runtime_activation_scope_check
        CHECK (scope_key = 'wq-xiangwan'),
    CONSTRAINT xw_runtime_activation_epoch_check
        CHECK (write_epoch > 0),
    CONSTRAINT xw_runtime_activation_actor_check
        CHECK (
            activated_by = btrim(activated_by)
            AND char_length(activated_by) BETWEEN 1 AND 128
        ),
    CONSTRAINT xw_runtime_activation_reason_check
        CHECK (
            reason = btrim(reason)
            AND char_length(reason) BETWEEN 1 AND 500
        ),
    CONSTRAINT xw_runtime_activation_previous_check
        CHECK (
            (write_epoch = 1 AND previous_generation_id IS NULL)
            OR (
                write_epoch > 1
                AND previous_generation_id IS NOT NULL
                AND previous_generation_id <> generation_id
            )
        ),
    CONSTRAINT xw_runtime_activation_time_check
        CHECK (activated_at <= recorded_at),
    UNIQUE (scope_key, write_epoch),
    UNIQUE (scope_key, generation_id)
);

CREATE OR REPLACE FUNCTION xiangwan_guard_runtime_generation_insert()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.scope_key <> 'wq-xiangwan'
       OR NEW.active_generation_id IS NOT NULL
       OR NEW.write_epoch <> 0
       OR NEW.previous_generation_id IS NOT NULL
       OR NEW.activated_at IS NOT NULL
       OR NEW.activated_by IS NOT NULL
       OR NEW.activation_reason IS NOT NULL THEN
        RAISE EXCEPTION 'xiangwan Runtime generation must be activated by update'
            USING ERRCODE = '23514';
    END IF;

    NEW.updated_at := clock_timestamp();
    RETURN NEW;
END;
$$;

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
       OR NEW.scope_key <> OLD.scope_key
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
           OR NEW.write_epoch <> OLD.write_epoch + 1
           OR NEW.previous_generation_id IS DISTINCT FROM OLD.active_generation_id
           OR NEW.activated_at IS NULL
           OR NEW.activated_at IS NOT DISTINCT FROM OLD.activated_at
           OR NEW.activated_by IS NULL
           OR NEW.activation_reason IS NULL THEN
            RAISE EXCEPTION 'invalid xiangwan Runtime generation activation'
                USING ERRCODE = '23514';
        END IF;
    ELSIF NEW.write_epoch <> OLD.write_epoch
          OR NEW.previous_generation_id IS DISTINCT FROM OLD.previous_generation_id
          OR NEW.activated_at IS DISTINCT FROM OLD.activated_at
          OR NEW.activated_by IS DISTINCT FROM OLD.activated_by
          OR NEW.activation_reason IS DISTINCT FROM OLD.activation_reason THEN
        RAISE EXCEPTION 'xiangwan Runtime activation facts are immutable'
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

CREATE TRIGGER trg_xw_runtime_generation_guard
    BEFORE UPDATE OR DELETE ON xiangwan_runtime_generations
    FOR EACH ROW EXECUTE FUNCTION xiangwan_guard_runtime_generation();

CREATE OR REPLACE FUNCTION xiangwan_record_runtime_generation_activation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.active_generation_id IS DISTINCT FROM OLD.active_generation_id THEN
        INSERT INTO xiangwan_runtime_generation_activations (
            scope_key,
            tenant_id,
            write_epoch,
            generation_id,
            previous_generation_id,
            activated_at,
            activated_by,
            reason
        ) VALUES (
            NEW.scope_key,
            NEW.tenant_id,
            NEW.write_epoch,
            NEW.active_generation_id,
            NEW.previous_generation_id,
            NEW.activated_at,
            NEW.activated_by,
            NEW.activation_reason
        );
    END IF;
    RETURN NULL;
END;
$$;

CREATE TRIGGER trg_xw_runtime_generation_activation_receipt
    AFTER UPDATE ON xiangwan_runtime_generations
    FOR EACH ROW
    EXECUTE FUNCTION xiangwan_record_runtime_generation_activation();

CREATE OR REPLACE FUNCTION xiangwan_reject_runtime_activation_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'xiangwan Runtime activation receipts are immutable'
        USING ERRCODE = '23514';
END;
$$;

CREATE TRIGGER trg_xw_runtime_activation_immutable
    BEFORE UPDATE OR DELETE ON xiangwan_runtime_generation_activations
    FOR EACH ROW
    EXECUTE FUNCTION xiangwan_reject_runtime_activation_mutation();

CREATE TRIGGER trg_xw_runtime_activation_no_truncate
    BEFORE TRUNCATE ON xiangwan_runtime_generation_activations
    FOR EACH STATEMENT
    EXECUTE FUNCTION xiangwan_reject_runtime_activation_mutation();
