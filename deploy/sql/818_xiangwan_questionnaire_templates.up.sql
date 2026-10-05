-- 818_xiangwan_questionnaire_templates.up.sql
-- Reusable questionnaire definitions. Template versions are append-only;
-- assigning a template copies its fields into the existing immutable Instance
-- questionnaire tables and never creates a live reference.

CREATE TABLE IF NOT EXISTS xiangwan_questionnaire_templates (
    template_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    name VARCHAR(200) NOT NULL,
    description VARCHAR(500) NOT NULL DEFAULT '',
    status VARCHAR(16) NOT NULL DEFAULT 'active',
    current_version BIGINT NOT NULL,
    created_by UUID NOT NULL REFERENCES principals(id),
    updated_by UUID NOT NULL REFERENCES principals(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT xw_questionnaire_template_name_check CHECK (
        name = BTRIM(name) AND CHAR_LENGTH(name) BETWEEN 1 AND 200
    ),
    CONSTRAINT xw_questionnaire_template_description_check CHECK (
        description = BTRIM(description) AND CHAR_LENGTH(description) <= 500
    ),
    CONSTRAINT xw_questionnaire_template_status_check CHECK (
        status IN ('active', 'archived')
    ),
    CONSTRAINT xw_questionnaire_template_version_check CHECK (current_version >= 1),
    CONSTRAINT xw_questionnaire_template_tenant_id_key UNIQUE (tenant_id, template_id)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_xw_questionnaire_template_active_name
    ON xiangwan_questionnaire_templates (tenant_id, lower(name))
    WHERE status = 'active';

CREATE TABLE IF NOT EXISTS xiangwan_questionnaire_template_versions (
    template_version_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,
    template_id UUID NOT NULL,
    version BIGINT NOT NULL,
    privacy_purpose VARCHAR(500) NOT NULL,
    privacy_policy_version VARCHAR(100) NOT NULL,
    fields JSONB NOT NULL,
    created_by UUID NOT NULL REFERENCES principals(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT xw_questionnaire_template_version_number_check CHECK (version >= 1),
    CONSTRAINT xw_questionnaire_template_version_purpose_check CHECK (
        privacy_purpose = BTRIM(privacy_purpose)
        AND CHAR_LENGTH(privacy_purpose) BETWEEN 1 AND 500
    ),
    CONSTRAINT xw_questionnaire_template_version_policy_check CHECK (
        privacy_policy_version = BTRIM(privacy_policy_version)
        AND CHAR_LENGTH(privacy_policy_version) BETWEEN 1 AND 100
    ),
    CONSTRAINT xw_questionnaire_template_version_fields_check CHECK (
        JSONB_TYPEOF(fields) = 'array'
        AND JSONB_ARRAY_LENGTH(fields) BETWEEN 1 AND 100
    ),
    CONSTRAINT xw_questionnaire_template_version_template_fkey
        FOREIGN KEY (tenant_id, template_id)
        REFERENCES xiangwan_questionnaire_templates (tenant_id, template_id),
    CONSTRAINT xw_questionnaire_template_version_key
        UNIQUE (tenant_id, template_id, version),
    CONSTRAINT xw_questionnaire_template_version_id_key
        UNIQUE (tenant_id, template_id, template_version_id)
);

CREATE INDEX IF NOT EXISTS idx_xw_questionnaire_template_list
    ON xiangwan_questionnaire_templates (tenant_id, status, updated_at DESC, template_id);

CREATE OR REPLACE FUNCTION xiangwan_reject_questionnaire_template_version_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'Xiangwan questionnaire template versions are append-only'
        USING ERRCODE = '0A000';
END;
$$;

DROP TRIGGER IF EXISTS trg_xw_questionnaire_template_version_immutable
    ON xiangwan_questionnaire_template_versions;
CREATE TRIGGER trg_xw_questionnaire_template_version_immutable
    BEFORE UPDATE OR DELETE ON xiangwan_questionnaire_template_versions
    FOR EACH ROW EXECUTE FUNCTION xiangwan_reject_questionnaire_template_version_mutation();

DROP TRIGGER IF EXISTS trg_xw_questionnaire_template_version_no_truncate
    ON xiangwan_questionnaire_template_versions;
CREATE TRIGGER trg_xw_questionnaire_template_version_no_truncate
    BEFORE TRUNCATE ON xiangwan_questionnaire_template_versions
    FOR EACH STATEMENT EXECUTE FUNCTION xiangwan_reject_questionnaire_template_version_mutation();

CREATE OR REPLACE FUNCTION xiangwan_guard_questionnaire_template_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
       OR NEW.template_id IS DISTINCT FROM OLD.template_id
       OR NEW.created_by IS DISTINCT FROM OLD.created_by
       OR NEW.created_at IS DISTINCT FROM OLD.created_at
       OR NEW.current_version < OLD.current_version THEN
        RAISE EXCEPTION 'invalid Xiangwan questionnaire template transition'
            USING ERRCODE = '23514';
    END IF;
    IF NEW.updated_at < OLD.updated_at THEN
        RAISE EXCEPTION 'Xiangwan questionnaire template updated_at moved backwards'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_xw_questionnaire_template_mutation
    ON xiangwan_questionnaire_templates;
CREATE TRIGGER trg_xw_questionnaire_template_mutation
    BEFORE UPDATE ON xiangwan_questionnaire_templates
    FOR EACH ROW EXECUTE FUNCTION xiangwan_guard_questionnaire_template_mutation();

DROP TRIGGER IF EXISTS trg_xw_questionnaire_template_no_delete
    ON xiangwan_questionnaire_templates;
CREATE TRIGGER trg_xw_questionnaire_template_no_delete
    BEFORE DELETE OR TRUNCATE ON xiangwan_questionnaire_templates
    FOR EACH STATEMENT EXECUTE FUNCTION xiangwan_reject_questionnaire_template_version_mutation();

-- Publication-reference locking keeps template edits and instance assignment
-- in the same tenant-scoped aggregate as the existing questionnaire tables.
DROP TRIGGER IF EXISTS trg_xw_questionnaire_templates_publication_lock
    ON xiangwan_questionnaire_templates;
CREATE TRIGGER trg_xw_questionnaire_templates_publication_lock
    BEFORE INSERT OR UPDATE ON xiangwan_questionnaire_templates
    FOR EACH ROW EXECUTE FUNCTION xiangwan_lock_publication_reference_aggregate();
DROP TRIGGER IF EXISTS trg_xw_questionnaire_template_versions_publication_lock
    ON xiangwan_questionnaire_template_versions;
CREATE TRIGGER trg_xw_questionnaire_template_versions_publication_lock
    BEFORE INSERT ON xiangwan_questionnaire_template_versions
    FOR EACH ROW EXECUTE FUNCTION xiangwan_lock_publication_reference_aggregate();
