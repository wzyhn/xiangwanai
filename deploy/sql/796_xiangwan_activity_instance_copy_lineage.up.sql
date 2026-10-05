-- 796_xiangwan_activity_instance_copy_lineage.up.sql
-- A copied period owns its own presentation, questionnaire, Sessions and
-- business facts. This immutable link records where its initial editable
-- defaults came from; it never aliases the source Instance or its children.
CREATE TABLE IF NOT EXISTS xiangwan_activity_instance_copies (
    tenant_id UUID NOT NULL,
    series_id UUID NOT NULL,
    instance_id UUID NOT NULL,
    source_instance_id UUID NOT NULL,
    source_instance_version BIGINT NOT NULL,
    source_presentation_revision BIGINT NOT NULL,
    source_questionnaire_version_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT xw_activity_instance_copies_pkey PRIMARY KEY (tenant_id, instance_id),
    CONSTRAINT xw_activity_instance_copies_target_fkey
        FOREIGN KEY (tenant_id, series_id, instance_id)
        REFERENCES xiangwan_activity_instances (tenant_id, series_id, id),
    CONSTRAINT xw_activity_instance_copies_source_fkey
        FOREIGN KEY (tenant_id, series_id, source_instance_id)
        REFERENCES xiangwan_activity_instances (tenant_id, series_id, id),
    CONSTRAINT xw_activity_instance_copies_source_questionnaire_fkey
        FOREIGN KEY (tenant_id, source_instance_id, source_questionnaire_version_id)
        REFERENCES xiangwan_questionnaire_versions
            (tenant_id, instance_id, questionnaire_version_id),
    CONSTRAINT xw_activity_instance_copies_distinct_check
        CHECK (instance_id <> source_instance_id),
    CONSTRAINT xw_activity_instance_copies_versions_check
        CHECK (source_instance_version >= 1 AND source_presentation_revision >= 1)
);

CREATE INDEX IF NOT EXISTS idx_xw_activity_instance_copies_source
    ON xiangwan_activity_instance_copies (tenant_id, source_instance_id);

CREATE OR REPLACE FUNCTION xiangwan_reject_instance_copy_lineage_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'xiangwan Instance copy lineage is append-only'
        USING ERRCODE = '0A000';
END;
$$;

CREATE TRIGGER trg_xw_activity_instance_copies_no_update_delete
    BEFORE UPDATE OR DELETE ON xiangwan_activity_instance_copies
    FOR EACH ROW EXECUTE FUNCTION xiangwan_reject_instance_copy_lineage_mutation();
CREATE TRIGGER trg_xw_activity_instance_copies_no_truncate
    BEFORE TRUNCATE ON xiangwan_activity_instance_copies
    FOR EACH STATEMENT EXECUTE FUNCTION xiangwan_reject_instance_copy_lineage_mutation();
