-- 790_xiangwan_resource_content_identity.down.sql

DROP TRIGGER IF EXISTS trg_xw_resource_relation_content_product
    ON xiangwan_resource_relations;
DROP FUNCTION IF EXISTS xiangwan_validate_resource_content_product();
