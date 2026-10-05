-- 762_xiangwan_resource_relations.down.sql

DROP TRIGGER IF EXISTS trg_xw_resource_relations_no_truncate
    ON xiangwan_resource_relations;
DROP TRIGGER IF EXISTS trg_xw_resource_relations_no_delete
    ON xiangwan_resource_relations;
DROP TRIGGER IF EXISTS trg_xw_resource_relations_no_update
    ON xiangwan_resource_relations;
DROP TRIGGER IF EXISTS trg_xw_resource_relations_validate
    ON xiangwan_resource_relations;

DROP FUNCTION IF EXISTS xiangwan_reject_resource_relation_mutation();
DROP FUNCTION IF EXISTS xiangwan_validate_resource_relation();

DROP TABLE IF EXISTS xiangwan_resource_relations;
