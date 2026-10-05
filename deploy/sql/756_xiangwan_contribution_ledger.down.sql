-- 756_xiangwan_contribution_ledger.down.sql

DROP TRIGGER IF EXISTS trg_xw_contribution_no_truncate
    ON xiangwan_contribution_entries;
DROP TRIGGER IF EXISTS trg_xw_contribution_no_delete
    ON xiangwan_contribution_entries;
DROP TRIGGER IF EXISTS trg_xw_contribution_no_update
    ON xiangwan_contribution_entries;
DROP TRIGGER IF EXISTS trg_xw_contribution_validate
    ON xiangwan_contribution_entries;

DROP FUNCTION IF EXISTS xiangwan_reject_contribution_mutation();
DROP FUNCTION IF EXISTS xiangwan_validate_contribution_entry();

DROP TABLE IF EXISTS xiangwan_contribution_entries;
