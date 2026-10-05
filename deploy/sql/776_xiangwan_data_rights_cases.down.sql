DROP TRIGGER IF EXISTS trg_xiangwan_data_rights_case_event_required
    ON xiangwan_data_rights_cases;
DROP TRIGGER IF EXISTS trg_xiangwan_data_rights_events_append_only
    ON xiangwan_data_rights_case_events;
DROP TRIGGER IF EXISTS trg_xiangwan_data_rights_event_insert
    ON xiangwan_data_rights_case_events;
DROP TRIGGER IF EXISTS trg_xiangwan_data_rights_case_no_delete
    ON xiangwan_data_rights_cases;
DROP TRIGGER IF EXISTS trg_xiangwan_data_rights_case_update
    ON xiangwan_data_rights_cases;
DROP FUNCTION IF EXISTS xiangwan_reject_data_rights_event_mutation();
DROP FUNCTION IF EXISTS xiangwan_assert_data_rights_case_event();
DROP FUNCTION IF EXISTS xiangwan_guard_data_rights_event_insert();
DROP FUNCTION IF EXISTS xiangwan_reject_data_rights_case_delete();
DROP FUNCTION IF EXISTS xiangwan_guard_data_rights_case_update();
DROP INDEX IF EXISTS idx_xiangwan_data_rights_case_events_case_time;
DROP TABLE IF EXISTS xiangwan_data_rights_case_events;
DROP INDEX IF EXISTS idx_xiangwan_data_rights_cases_owner_submitted;
DROP TABLE IF EXISTS xiangwan_data_rights_cases;
