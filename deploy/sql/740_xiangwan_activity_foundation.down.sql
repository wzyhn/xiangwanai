-- Development/test rollback reference only. Production rollback is roll-forward.

DROP TRIGGER IF EXISTS trg_xiangwan_guard_series_recurrence ON xiangwan_activity_series;
DROP FUNCTION IF EXISTS xiangwan_guard_series_recurrence();

ALTER TABLE IF EXISTS xiangwan_activity_series
    DROP CONSTRAINT IF EXISTS xiangwan_activity_series_current_public_instance_fkey;

DROP TABLE IF EXISTS xiangwan_activity_sessions;
DROP TABLE IF EXISTS xiangwan_activity_instances;
DROP TABLE IF EXISTS xiangwan_activity_series;
