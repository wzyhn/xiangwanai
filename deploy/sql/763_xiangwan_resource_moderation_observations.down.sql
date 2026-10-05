-- 763_xiangwan_resource_moderation_observations.down.sql

DROP TRIGGER IF EXISTS trg_xw_resource_moderation_no_truncate
    ON xiangwan_resource_moderation_observations;
DROP TRIGGER IF EXISTS trg_xw_resource_moderation_no_delete
    ON xiangwan_resource_moderation_observations;
DROP TRIGGER IF EXISTS trg_xw_resource_moderation_no_update
    ON xiangwan_resource_moderation_observations;
DROP TRIGGER IF EXISTS trg_xw_resource_moderation_validate
    ON xiangwan_resource_moderation_observations;

DROP FUNCTION IF EXISTS xiangwan_reject_resource_moderation_mutation();
DROP FUNCTION IF EXISTS xiangwan_validate_resource_moderation();

DROP TABLE IF EXISTS xiangwan_resource_moderation_observations;
