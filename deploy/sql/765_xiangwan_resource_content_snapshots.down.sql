-- 765_xiangwan_resource_content_snapshots.down.sql

DROP TRIGGER IF EXISTS trg_xw_resource_moderation_snapshot_validate
    ON xiangwan_resource_moderation_observations;
DROP FUNCTION IF EXISTS xiangwan_validate_moderation_snapshot_digest();

DROP TRIGGER IF EXISTS trg_xw_resource_relations_capture_snapshot
    ON xiangwan_resource_relations;
DROP FUNCTION IF EXISTS xiangwan_capture_resource_content_snapshot();

DROP TRIGGER IF EXISTS trg_xw_bound_resource_block_truncate ON blocks;
DROP TRIGGER IF EXISTS trg_xw_bound_resource_block_guard ON blocks;
DROP FUNCTION IF EXISTS xiangwan_guard_bound_resource_block_truncate();
DROP FUNCTION IF EXISTS xiangwan_guard_bound_resource_block();

DROP TRIGGER IF EXISTS trg_xw_bound_resource_content_guard ON contents;
DROP FUNCTION IF EXISTS xiangwan_guard_bound_resource_content();

DROP TRIGGER IF EXISTS trg_xw_resource_snapshots_no_truncate
    ON xiangwan_resource_content_snapshots;
DROP TRIGGER IF EXISTS trg_xw_resource_snapshots_no_delete
    ON xiangwan_resource_content_snapshots;
DROP TRIGGER IF EXISTS trg_xw_resource_snapshots_no_update
    ON xiangwan_resource_content_snapshots;
DROP FUNCTION IF EXISTS xiangwan_reject_resource_snapshot_mutation();

DROP TABLE IF EXISTS xiangwan_resource_content_snapshots;
