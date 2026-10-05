-- Development/test rollback reference only. Production rollback is roll-forward.

DROP INDEX IF EXISTS uq_xiangwan_activity_instances_issue_no;

ALTER TABLE IF EXISTS xiangwan_activity_instances
    DROP CONSTRAINT IF EXISTS xiangwan_activity_instances_issue_no_check,
    DROP COLUMN IF EXISTS issue_no;

DROP TRIGGER IF EXISTS trg_xiangwan_assign_instance_issue_no
    ON xiangwan_activity_instances;
DROP FUNCTION IF EXISTS xiangwan_assign_instance_issue_no();
