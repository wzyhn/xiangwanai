-- 768_xiangwan_brand_profile_publication.up.sql
-- Tenant-owned BrandProfile lifecycle plus immutable, approved public-home
-- snapshots. The Xiangwan Runtime reads only the selected snapshot from PG.

CREATE OR REPLACE FUNCTION xiangwan_valid_brand_quick_tags(tags JSONB)
RETURNS BOOLEAN
LANGUAGE plpgsql
IMMUTABLE
STRICT
PARALLEL SAFE
AS $$
DECLARE
    tag JSONB;
    seen_codes TEXT[] := ARRAY[]::TEXT[];
    code TEXT;
    label TEXT;
BEGIN
    IF JSONB_TYPEOF(tags) <> 'array' THEN
        RETURN FALSE;
    END IF;
    IF JSONB_ARRAY_LENGTH(tags) > 20 THEN
        RETURN FALSE;
    END IF;

    FOR tag IN SELECT value FROM JSONB_ARRAY_ELEMENTS(tags)
    LOOP
        IF JSONB_TYPEOF(tag) <> 'object' THEN
            RETURN FALSE;
        END IF;
        IF NOT (tag ? 'code')
           OR NOT (tag ? 'label')
           OR (SELECT COUNT(*) FROM JSONB_OBJECT_KEYS(tag)) <> 2
           OR JSONB_TYPEOF(tag -> 'code') <> 'string'
           OR JSONB_TYPEOF(tag -> 'label') <> 'string' THEN
            RETURN FALSE;
        END IF;

        code := tag ->> 'code';
        label := tag ->> 'label';
        IF code !~ '^[a-z][a-z0-9_]{0,31}$'
           OR label <> BTRIM(label)
           OR CHAR_LENGTH(label) NOT BETWEEN 1 AND 40
           OR code = ANY(seen_codes) THEN
            RETURN FALSE;
        END IF;
        seen_codes := ARRAY_APPEND(seen_codes, code);
    END LOOP;
    RETURN TRUE;
END;
$$;

CREATE TABLE xiangwan_brand_profile_publications (
    publication_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    publication_version BIGINT NOT NULL,
    community_name VARCHAR(100) NOT NULL,
    brand_intro TEXT NOT NULL,
    quick_tags JSONB NOT NULL DEFAULT '[]'::JSONB,
    published_by UUID NOT NULL REFERENCES principals(id),
    published_at TIMESTAMPTZ NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT xw_brand_publication_version_check
        CHECK (publication_version >= 1),
    CONSTRAINT xw_brand_publication_name_check
        CHECK (
            community_name = BTRIM(community_name)
            AND CHAR_LENGTH(community_name) BETWEEN 1 AND 100
        ),
    CONSTRAINT xw_brand_publication_intro_check
        CHECK (
            brand_intro = BTRIM(brand_intro)
            AND CHAR_LENGTH(brand_intro) BETWEEN 1 AND 2000
        ),
    CONSTRAINT xw_brand_publication_quick_tags_check
        CHECK (xiangwan_valid_brand_quick_tags(quick_tags)),
    CONSTRAINT xw_brand_publication_time_check
        CHECK (published_at <= recorded_at),
    CONSTRAINT xw_brand_publication_tenant_version_key
        UNIQUE (tenant_id, publication_version)
);

CREATE TABLE xiangwan_brand_profiles (
    tenant_id UUID PRIMARY KEY REFERENCES tenants(id),
    lifecycle_status VARCHAR(16) NOT NULL DEFAULT 'draft',
    current_publication_version BIGINT,
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT xw_brand_profile_lifecycle_check
        CHECK (lifecycle_status IN ('draft', 'active', 'suspended')),
    CONSTRAINT xw_brand_profile_version_check
        CHECK (version >= 1),
    CONSTRAINT xw_brand_profile_publication_shape_check
        CHECK (
            (lifecycle_status = 'draft' AND current_publication_version IS NULL)
            OR (
                lifecycle_status IN ('active', 'suspended')
                AND current_publication_version IS NOT NULL
                AND current_publication_version >= 1
            )
        ),
    CONSTRAINT xw_brand_profile_current_publication_fkey
        FOREIGN KEY (tenant_id, current_publication_version)
        REFERENCES xiangwan_brand_profile_publications (
            tenant_id,
            publication_version
        )
);

CREATE INDEX idx_xw_brand_publications_tenant_published
    ON xiangwan_brand_profile_publications (
        tenant_id,
        published_at DESC,
        publication_version DESC
    );

CREATE OR REPLACE FUNCTION xiangwan_reject_brand_publication_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'xiangwan BrandProfile publications are immutable'
        USING ERRCODE = '23514';
END;
$$;

CREATE TRIGGER trg_xw_brand_publication_immutable
    BEFORE UPDATE OR DELETE ON xiangwan_brand_profile_publications
    FOR EACH ROW
    EXECUTE FUNCTION xiangwan_reject_brand_publication_mutation();

CREATE TRIGGER trg_xw_brand_publication_no_truncate
    BEFORE TRUNCATE ON xiangwan_brand_profile_publications
    FOR EACH STATEMENT
    EXECUTE FUNCTION xiangwan_reject_brand_publication_mutation();
