-- Development/test rollback reference only. Production rollback is roll-forward.

DROP INDEX IF EXISTS idx_xiangwan_activity_sessions_public_area_start;
DROP INDEX IF EXISTS idx_xiangwan_activity_instances_quick_tags;
DROP INDEX IF EXISTS idx_xiangwan_activity_series_home;

ALTER TABLE IF EXISTS xiangwan_activity_sessions
    DROP CONSTRAINT IF EXISTS xiangwan_activity_sessions_public_area_shape_check,
    DROP CONSTRAINT IF EXISTS xiangwan_activity_sessions_capacity_commitment_check,
    DROP CONSTRAINT IF EXISTS xiangwan_activity_sessions_active_hold_count_check,
    DROP CONSTRAINT IF EXISTS xiangwan_activity_sessions_confirmed_registration_count_check,
    DROP CONSTRAINT IF EXISTS xiangwan_activity_sessions_area_code_check,
    DROP COLUMN IF EXISTS active_hold_count,
    DROP COLUMN IF EXISTS confirmed_registration_count,
    DROP COLUMN IF EXISTS area_code;

ALTER TABLE IF EXISTS xiangwan_activity_instances
    DROP CONSTRAINT IF EXISTS xiangwan_activity_instances_public_catalog_shape_check,
    DROP CONSTRAINT IF EXISTS xiangwan_activity_instances_quick_tag_codes_check,
    DROP CONSTRAINT IF EXISTS xiangwan_activity_instances_activity_type_check,
    DROP COLUMN IF EXISTS quick_tag_codes,
    DROP COLUMN IF EXISTS activity_type;

DROP FUNCTION IF EXISTS xiangwan_valid_quick_tag_codes(TEXT[]);

ALTER TABLE IF EXISTS xiangwan_activity_series
    DROP CONSTRAINT IF EXISTS xiangwan_activity_series_historical_registration_count_check,
    DROP CONSTRAINT IF EXISTS xiangwan_activity_series_favorite_count_check,
    DROP COLUMN IF EXISTS historical_registration_count,
    DROP COLUMN IF EXISTS favorite_count,
    DROP COLUMN IF EXISTS home_visible;
