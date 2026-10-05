-- 766_xiangwan_runtime_generation.down.sql

DROP TRIGGER IF EXISTS trg_xw_runtime_generation_no_truncate
    ON xiangwan_runtime_generations;
DROP FUNCTION IF EXISTS xiangwan_reject_runtime_generation_truncate();

DROP TRIGGER IF EXISTS trg_xw_runtime_generation_guard
    ON xiangwan_runtime_generations;
DROP FUNCTION IF EXISTS xiangwan_guard_runtime_generation();

DROP TRIGGER IF EXISTS trg_xw_runtime_generation_insert_guard
    ON xiangwan_runtime_generations;
DROP FUNCTION IF EXISTS xiangwan_guard_runtime_generation_insert();

DROP TABLE IF EXISTS xiangwan_runtime_generations;
