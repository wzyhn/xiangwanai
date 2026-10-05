-- Development/test rollback reference only. Production rollback is roll-forward.

DO $$
DECLARE
    has_publication_events BOOLEAN;
BEGIN
    IF to_regclass('xiangwan_publication_events') IS NOT NULL THEN
        EXECUTE 'SELECT EXISTS (SELECT 1 FROM xiangwan_publication_events)'
            INTO has_publication_events;
        IF has_publication_events THEN
            RAISE EXCEPTION 'cannot remove xiangwan publication events after writes exist';
        END IF;
    END IF;
END
$$;

DROP TABLE IF EXISTS xiangwan_publication_events;
DROP FUNCTION IF EXISTS xiangwan_reject_publication_event_mutation();

ALTER TABLE IF EXISTS xiangwan_activity_sessions
    DROP CONSTRAINT IF EXISTS xiangwan_activity_sessions_delivery_shape_check,
    DROP CONSTRAINT IF EXISTS xiangwan_activity_sessions_latitude_check,
    DROP CONSTRAINT IF EXISTS xiangwan_activity_sessions_longitude_check,
    DROP CONSTRAINT IF EXISTS xiangwan_activity_sessions_delivery_mode_check;

ALTER TABLE IF EXISTS xiangwan_activity_sessions
    DROP COLUMN IF EXISTS online_participation_compliant,
    DROP COLUMN IF EXISTS online_participation_mode,
    DROP COLUMN IF EXISTS latitude,
    DROP COLUMN IF EXISTS longitude,
    DROP COLUMN IF EXISTS address,
    DROP COLUMN IF EXISTS venue_name,
    DROP COLUMN IF EXISTS delivery_mode;
