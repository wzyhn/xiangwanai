-- 742_xiangwan_activity_catalog_snapshots.up.sql
-- Persist the public catalog snapshot and transaction-maintained capacity
-- counters required by the Xiangwan Session home read model.

ALTER TABLE xiangwan_activity_series
    ADD COLUMN IF NOT EXISTS home_visible BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN IF NOT EXISTS favorite_count BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS historical_registration_count BIGINT NOT NULL DEFAULT 0;

ALTER TABLE xiangwan_activity_series
    DROP CONSTRAINT IF EXISTS xiangwan_activity_series_favorite_count_check,
    ADD CONSTRAINT xiangwan_activity_series_favorite_count_check
        CHECK (favorite_count >= 0),
    DROP CONSTRAINT IF EXISTS xiangwan_activity_series_historical_registration_count_check,
    ADD CONSTRAINT xiangwan_activity_series_historical_registration_count_check
        CHECK (historical_registration_count >= 0);

CREATE OR REPLACE FUNCTION xiangwan_valid_quick_tag_codes(codes TEXT[])
RETURNS BOOLEAN
LANGUAGE sql
IMMUTABLE
PARALLEL SAFE
AS $$
    SELECT COALESCE(
        CARDINALITY(codes) <= 20
        AND NOT EXISTS (
            SELECT 1
            FROM UNNEST(codes) AS tag(code)
            WHERE code !~ '^[a-z][a-z0-9_]{0,31}$'
        )
        AND CARDINALITY(codes) = (
            SELECT COUNT(DISTINCT code)
            FROM UNNEST(codes) AS tag(code)
        ),
        FALSE
    )
$$;

ALTER TABLE xiangwan_activity_instances
    ADD COLUMN IF NOT EXISTS activity_type VARCHAR(32),
    ADD COLUMN IF NOT EXISTS quick_tag_codes TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[];

ALTER TABLE xiangwan_activity_instances
    DROP CONSTRAINT IF EXISTS xiangwan_activity_instances_activity_type_check,
    ADD CONSTRAINT xiangwan_activity_instances_activity_type_check
        CHECK (
            activity_type IS NULL
            OR activity_type IN ('ai_roundtable', 'special_event', 'course', 'competition')
        ),
    DROP CONSTRAINT IF EXISTS xiangwan_activity_instances_quick_tag_codes_check,
    ADD CONSTRAINT xiangwan_activity_instances_quick_tag_codes_check
        CHECK (xiangwan_valid_quick_tag_codes(quick_tag_codes)),
    DROP CONSTRAINT IF EXISTS xiangwan_activity_instances_public_catalog_shape_check,
    ADD CONSTRAINT xiangwan_activity_instances_public_catalog_shape_check
        CHECK (
            status NOT IN ('published', 'completed')
            OR activity_type IS NOT NULL
        ) NOT VALID;

ALTER TABLE xiangwan_activity_sessions
    ADD COLUMN IF NOT EXISTS area_code VARCHAR(16),
    ADD COLUMN IF NOT EXISTS confirmed_registration_count INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS active_hold_count INTEGER NOT NULL DEFAULT 0;

ALTER TABLE xiangwan_activity_sessions
    DROP CONSTRAINT IF EXISTS xiangwan_activity_sessions_area_code_check,
    ADD CONSTRAINT xiangwan_activity_sessions_area_code_check
        CHECK (
            area_code IS NULL
            OR area_code IN (
                'heping', 'hexi', 'hebei', 'nankai', 'hongqiao',
                'hedong', 'wuqing', 'dongli', 'binhai', 'online'
            )
        ),
    DROP CONSTRAINT IF EXISTS xiangwan_activity_sessions_confirmed_registration_count_check,
    ADD CONSTRAINT xiangwan_activity_sessions_confirmed_registration_count_check
        CHECK (confirmed_registration_count >= 0),
    DROP CONSTRAINT IF EXISTS xiangwan_activity_sessions_active_hold_count_check,
    ADD CONSTRAINT xiangwan_activity_sessions_active_hold_count_check
        CHECK (active_hold_count >= 0),
    DROP CONSTRAINT IF EXISTS xiangwan_activity_sessions_capacity_commitment_check,
    ADD CONSTRAINT xiangwan_activity_sessions_capacity_commitment_check
        CHECK (
            capacity IS NULL
            OR confirmed_registration_count + active_hold_count <= capacity
        ),
    DROP CONSTRAINT IF EXISTS xiangwan_activity_sessions_public_area_shape_check,
    ADD CONSTRAINT xiangwan_activity_sessions_public_area_shape_check
        CHECK (
            status NOT IN ('published', 'cancelled', 'ended')
            OR (
                (delivery_mode = 'online' AND area_code = 'online')
                OR (
                    delivery_mode = 'offline'
                    AND area_code IS NOT NULL
                    AND area_code <> 'online'
                )
            )
        ) NOT VALID;

CREATE INDEX IF NOT EXISTS idx_xiangwan_activity_series_home
    ON xiangwan_activity_series (tenant_id, current_public_instance_id, id)
    WHERE status = 'active' AND home_visible;

CREATE INDEX IF NOT EXISTS idx_xiangwan_activity_instances_quick_tags
    ON xiangwan_activity_instances USING GIN (quick_tag_codes);

CREATE INDEX IF NOT EXISTS idx_xiangwan_activity_sessions_public_area_start
    ON xiangwan_activity_sessions (tenant_id, area_code, session_start_at, sort_order, id)
    WHERE status IN ('published', 'cancelled', 'ended');
