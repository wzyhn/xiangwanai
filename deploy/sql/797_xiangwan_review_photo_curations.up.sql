-- 797_xiangwan_review_photo_curations.up.sql
-- Append-only presentation choices for already-published review images.
-- The original Content, moderation observation and publication remain immutable.

CREATE TABLE IF NOT EXISTS xiangwan_review_photo_curations (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    relation_id UUID NOT NULL,
    version BIGINT NOT NULL CHECK (version >= 1),
    expected_version BIGINT NOT NULL CHECK (expected_version >= 0),
    operation_id UUID NOT NULL,
    actor_id UUID NOT NULL REFERENCES principals(id),
    ordered_block_ids JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT xw_review_photo_curation_relation_fkey
        FOREIGN KEY (tenant_id, relation_id)
        REFERENCES xiangwan_resource_relations (tenant_id, id),
    CONSTRAINT xw_review_photo_curation_version_key
        UNIQUE (tenant_id, relation_id, version),
    CONSTRAINT xw_review_photo_curation_operation_key
        UNIQUE (tenant_id, actor_id, operation_id),
    CONSTRAINT xw_review_photo_curation_sequence_check
        CHECK (version = expected_version + 1),
    CONSTRAINT xw_review_photo_curation_shape_check
        CHECK (jsonb_typeof(ordered_block_ids) = 'array'
            AND jsonb_array_length(ordered_block_ids) <= 30)
);

CREATE INDEX IF NOT EXISTS idx_xw_review_photo_curation_latest
    ON xiangwan_review_photo_curations (tenant_id, relation_id, version DESC);

CREATE OR REPLACE FUNCTION xiangwan_reject_review_photo_curation_mutation()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'published review photo curation is append-only'
        USING ERRCODE = '23514';
END;
$$;

CREATE TRIGGER xw_review_photo_curation_immutable
    BEFORE UPDATE OR DELETE ON xiangwan_review_photo_curations
    FOR EACH ROW EXECUTE FUNCTION xiangwan_reject_review_photo_curation_mutation();
