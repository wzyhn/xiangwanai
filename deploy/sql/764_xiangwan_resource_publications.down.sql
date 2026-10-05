-- 764_xiangwan_resource_publications.down.sql

DROP TRIGGER IF EXISTS trg_xw_resource_publications_no_truncate
    ON xiangwan_resource_publications;
DROP TRIGGER IF EXISTS trg_xw_resource_publications_no_delete
    ON xiangwan_resource_publications;
DROP TRIGGER IF EXISTS trg_xw_resource_publications_no_update
    ON xiangwan_resource_publications;
DROP TRIGGER IF EXISTS trg_xw_resource_publications_validate
    ON xiangwan_resource_publications;

DROP FUNCTION IF EXISTS xiangwan_reject_resource_publication_mutation();
DROP FUNCTION IF EXISTS xiangwan_validate_resource_publication();

DROP TABLE IF EXISTS xiangwan_resource_publications;
