-- 822_xiangwan_custom_activity_type.up.sql
-- Allow a fully administrator-defined activity to use the existing
-- Series -> Instance -> Session publication and registration chain.

ALTER TABLE xiangwan_activity_instances
    DROP CONSTRAINT IF EXISTS xiangwan_activity_instances_activity_type_check,
    ADD CONSTRAINT xiangwan_activity_instances_activity_type_check
        CHECK (
            activity_type IS NULL
            OR activity_type IN (
                'ai_roundtable', 'special_event', 'course', 'competition', 'custom'
            )
        );

ALTER TABLE xiangwan_coupons
    DROP CONSTRAINT IF EXISTS xw_coupons_scope_check,
    ADD CONSTRAINT xw_coupons_scope_check
        CHECK (
            (
                scope_type = 'activity_type'
                AND scope_activity_type IN (
                    'ai_roundtable', 'special_event', 'course', 'competition', 'custom'
                )
                AND scope_series_id IS NULL
            )
            OR (
                scope_type = 'series'
                AND scope_activity_type IS NULL
                AND scope_series_id IS NOT NULL
            )
        );
