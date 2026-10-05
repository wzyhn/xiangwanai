-- Development/test rollback reference only. Production rollback is roll-forward.

DROP TABLE IF EXISTS xiangwan_brand_profiles;

DROP TRIGGER IF EXISTS trg_xw_brand_publication_no_truncate
    ON xiangwan_brand_profile_publications;
DROP TRIGGER IF EXISTS trg_xw_brand_publication_immutable
    ON xiangwan_brand_profile_publications;
DROP FUNCTION IF EXISTS xiangwan_reject_brand_publication_mutation();

DROP INDEX IF EXISTS idx_xw_brand_publications_tenant_published;
DROP TABLE IF EXISTS xiangwan_brand_profile_publications;
DROP FUNCTION IF EXISTS xiangwan_valid_brand_quick_tags(JSONB);
