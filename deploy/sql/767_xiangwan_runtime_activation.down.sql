-- 767_xiangwan_runtime_activation.down.sql

DROP TRIGGER IF EXISTS trg_xw_runtime_activation_no_truncate
    ON xiangwan_runtime_generation_activations;
DROP TRIGGER IF EXISTS trg_xw_runtime_activation_immutable
    ON xiangwan_runtime_generation_activations;
DROP FUNCTION IF EXISTS xiangwan_reject_runtime_activation_mutation();

DROP TRIGGER IF EXISTS trg_xw_runtime_generation_activation_receipt
    ON xiangwan_runtime_generations;
DROP FUNCTION IF EXISTS xiangwan_record_runtime_generation_activation();

DROP TABLE IF EXISTS xiangwan_runtime_generation_activations;

DROP TRIGGER IF EXISTS trg_xw_runtime_generation_guard
    ON xiangwan_runtime_generations;
DROP TRIGGER IF EXISTS trg_xw_runtime_generation_insert_guard
    ON xiangwan_runtime_generations;

ALTER TABLE xiangwan_runtime_generations
    DROP CONSTRAINT IF EXISTS xw_runtime_generation_activation_check,
    DROP CONSTRAINT IF EXISTS xw_runtime_generation_write_epoch_check,
    DROP CONSTRAINT IF EXISTS xw_runtime_generation_scope_check,
    DROP COLUMN IF EXISTS activation_reason,
    DROP COLUMN IF EXISTS activated_by,
    DROP COLUMN IF EXISTS previous_generation_id,
    DROP COLUMN IF EXISTS scope_key;

ALTER TABLE xiangwan_runtime_generations
    RENAME COLUMN write_epoch TO generation_epoch;

ALTER TABLE xiangwan_runtime_generations
    ADD CONSTRAINT xw_runtime_generation_epoch_check
        CHECK (generation_epoch >= 0),
    ADD CONSTRAINT xw_runtime_generation_active_check
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

CREATE TRIGGER trg_xw_runtime_generation_insert_guard
    BEFORE INSERT ON xiangwan_runtime_generations
    FOR EACH ROW
    EXECUTE FUNCTION xiangwan_guard_runtime_generation_insert();

CREATE TRIGGER trg_xw_runtime_generation_guard
    BEFORE UPDATE OR DELETE ON xiangwan_runtime_generations
    FOR EACH ROW EXECUTE FUNCTION xiangwan_guard_runtime_generation();
