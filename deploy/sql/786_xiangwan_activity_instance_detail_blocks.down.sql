-- Development/test rollback reference only. Production rollback is roll-forward.

ALTER TABLE IF EXISTS xiangwan_activity_instances
    DROP CONSTRAINT IF EXISTS xiangwan_activity_instances_detail_blocks_check,
    DROP COLUMN IF EXISTS detail_blocks;
