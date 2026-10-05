-- 750_xiangwan_checkin_foundation.down.sql

DROP TRIGGER IF EXISTS trg_xiangwan_checkin_events_no_truncate
    ON xiangwan_checkin_events;
DROP TRIGGER IF EXISTS trg_xiangwan_checkin_events_no_delete
    ON xiangwan_checkin_events;
DROP TRIGGER IF EXISTS trg_xiangwan_checkin_events_no_update
    ON xiangwan_checkin_events;
DROP TRIGGER IF EXISTS trg_xiangwan_checkin_events_validate
    ON xiangwan_checkin_events;
DROP TRIGGER IF EXISTS trg_xiangwan_checkins_no_truncate
    ON xiangwan_checkins;
DROP TRIGGER IF EXISTS trg_xiangwan_checkins_no_delete
    ON xiangwan_checkins;
DROP TRIGGER IF EXISTS trg_xiangwan_checkins_mutation_guard
    ON xiangwan_checkins;
DROP TRIGGER IF EXISTS trg_xiangwan_checkins_insert_guard
    ON xiangwan_checkins;

DROP FUNCTION IF EXISTS xiangwan_reject_checkin_removal();
DROP FUNCTION IF EXISTS xiangwan_validate_checkin_event();
DROP FUNCTION IF EXISTS xiangwan_guard_checkin_mutation();
DROP FUNCTION IF EXISTS xiangwan_guard_checkin_insert();

DROP TABLE IF EXISTS xiangwan_checkin_events;
DROP TABLE IF EXISTS xiangwan_checkins;

ALTER TABLE xiangwan_activity_sessions
    DROP CONSTRAINT IF EXISTS
        xiangwan_activity_sessions_checked_in_registration_count_check,
    DROP COLUMN IF EXISTS checked_in_registration_count;
