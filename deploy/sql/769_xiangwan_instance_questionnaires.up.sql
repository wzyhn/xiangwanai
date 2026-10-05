-- 769_xiangwan_instance_questionnaires.up.sql
-- Versioned, Instance-scoped questionnaire contracts. Runtime reads the latest
-- append-only assignment; published versions and their fields are immutable.

CREATE OR REPLACE FUNCTION xiangwan_valid_questionnaire_options(options JSONB)
RETURNS BOOLEAN
LANGUAGE plpgsql
IMMUTABLE
STRICT
PARALLEL SAFE
AS $$
DECLARE
    option_value JSONB;
    option_code TEXT;
    option_label TEXT;
    seen_codes TEXT[] := ARRAY[]::TEXT[];
BEGIN
    IF JSONB_TYPEOF(options) <> 'array' OR JSONB_ARRAY_LENGTH(options) > 100 THEN
        RETURN FALSE;
    END IF;

    FOR option_value IN SELECT value FROM JSONB_ARRAY_ELEMENTS(options)
    LOOP
        IF JSONB_TYPEOF(option_value) <> 'object'
           OR NOT (option_value ? 'code')
           OR NOT (option_value ? 'label')
           OR (SELECT COUNT(*) FROM JSONB_OBJECT_KEYS(option_value)) <> 2
           OR JSONB_TYPEOF(option_value -> 'code') <> 'string'
           OR JSONB_TYPEOF(option_value -> 'label') <> 'string' THEN
            RETURN FALSE;
        END IF;

        option_code := option_value ->> 'code';
        option_label := option_value ->> 'label';
        IF option_code !~ '^[a-z][a-z0-9_]{0,63}$'
           OR option_label <> BTRIM(option_label)
           OR CHAR_LENGTH(option_label) NOT BETWEEN 1 AND 100
           OR option_code = ANY(seen_codes) THEN
            RETURN FALSE;
        END IF;
        seen_codes := ARRAY_APPEND(seen_codes, option_code);
    END LOOP;
    RETURN TRUE;
END;
$$;

CREATE TABLE xiangwan_questionnaire_versions (
    questionnaire_version_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    instance_id UUID NOT NULL,
    version BIGINT NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'draft',
    privacy_purpose VARCHAR(500) NOT NULL,
    privacy_policy_version VARCHAR(100) NOT NULL,
    created_by UUID NOT NULL REFERENCES principals(id),
    published_by UUID REFERENCES principals(id),
    published_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT xw_questionnaire_version_number_check CHECK (version >= 1),
    CONSTRAINT xw_questionnaire_version_status_check
        CHECK (status IN ('draft', 'published')),
    CONSTRAINT xw_questionnaire_privacy_purpose_check
        CHECK (
            privacy_purpose = BTRIM(privacy_purpose)
            AND CHAR_LENGTH(privacy_purpose) BETWEEN 1 AND 500
        ),
    CONSTRAINT xw_questionnaire_privacy_policy_check
        CHECK (
            privacy_policy_version = BTRIM(privacy_policy_version)
            AND CHAR_LENGTH(privacy_policy_version) BETWEEN 1 AND 100
        ),
    CONSTRAINT xw_questionnaire_publication_shape_check
        CHECK (
            (status = 'draft' AND published_by IS NULL AND published_at IS NULL)
            OR (
                status = 'published'
                AND published_by IS NOT NULL
                AND published_at IS NOT NULL
                AND published_at <= updated_at
            )
        ),
    CONSTRAINT xw_questionnaire_version_instance_fkey
        FOREIGN KEY (tenant_id, instance_id)
        REFERENCES xiangwan_activity_instances (tenant_id, id),
    CONSTRAINT xw_questionnaire_tenant_instance_version_key
        UNIQUE (tenant_id, instance_id, version),
    CONSTRAINT xw_questionnaire_tenant_instance_id_key
        UNIQUE (tenant_id, instance_id, questionnaire_version_id)
);

CREATE TABLE xiangwan_questionnaire_fields (
    field_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,
    instance_id UUID NOT NULL,
    questionnaire_version_id UUID NOT NULL,
    field_code VARCHAR(64) NOT NULL,
    field_type VARCHAR(24) NOT NULL,
    label VARCHAR(200) NOT NULL,
    help_text VARCHAR(500) NOT NULL DEFAULT '',
    is_required BOOLEAN NOT NULL DEFAULT FALSE,
    sort_order INTEGER NOT NULL,
    min_length INTEGER,
    max_length INTEGER,
    max_selections INTEGER,
    options JSONB NOT NULL DEFAULT '[]'::JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT xw_questionnaire_field_code_check
        CHECK (field_code ~ '^[a-z][a-z0-9_]{0,63}$'),
    CONSTRAINT xw_questionnaire_field_type_check
        CHECK (
            field_type IN (
                'single_choice',
                'multiple_choice',
                'single_line',
                'multiline',
                'area'
            )
        ),
    CONSTRAINT xw_questionnaire_field_label_check
        CHECK (
            label = BTRIM(label)
            AND CHAR_LENGTH(label) BETWEEN 1 AND 200
        ),
    CONSTRAINT xw_questionnaire_field_help_check
        CHECK (
            help_text = BTRIM(help_text)
            AND CHAR_LENGTH(help_text) <= 500
        ),
    CONSTRAINT xw_questionnaire_field_sort_check CHECK (sort_order >= 0),
    CONSTRAINT xw_questionnaire_field_length_check
        CHECK (
            (min_length IS NULL OR min_length >= 0)
            AND (max_length IS NULL OR max_length BETWEEN 1 AND 2000)
            AND (
                min_length IS NULL
                OR max_length IS NULL
                OR min_length <= max_length
            )
        ),
    CONSTRAINT xw_questionnaire_field_options_check
        CHECK (xiangwan_valid_questionnaire_options(options)),
    CONSTRAINT xw_questionnaire_field_shape_check
        CHECK (
            (
                field_type IN ('single_choice', 'area')
                AND JSONB_ARRAY_LENGTH(options) >= 1
                AND min_length IS NULL
                AND max_length IS NULL
                AND max_selections IS NULL
            )
            OR (
                field_type = 'multiple_choice'
                AND JSONB_ARRAY_LENGTH(options) >= 1
                AND min_length IS NULL
                AND max_length IS NULL
                AND max_selections IS NOT NULL
                AND max_selections BETWEEN 1 AND JSONB_ARRAY_LENGTH(options)
            )
            OR (
                field_type = 'single_line'
                AND JSONB_ARRAY_LENGTH(options) = 0
                AND max_selections IS NULL
                AND max_length IS NOT NULL
                AND max_length BETWEEN 1 AND 200
            )
            OR (
                field_type = 'multiline'
                AND JSONB_ARRAY_LENGTH(options) = 0
                AND max_selections IS NULL
                AND max_length IS NOT NULL
                AND max_length BETWEEN 1 AND 2000
            )
        ),
    CONSTRAINT xw_questionnaire_field_version_fkey
        FOREIGN KEY (tenant_id, instance_id, questionnaire_version_id)
        REFERENCES xiangwan_questionnaire_versions (
            tenant_id,
            instance_id,
            questionnaire_version_id
        ),
    CONSTRAINT xw_questionnaire_field_code_key
        UNIQUE (questionnaire_version_id, field_code),
    CONSTRAINT xw_questionnaire_field_order_key
        UNIQUE (questionnaire_version_id, sort_order)
);

CREATE TABLE xiangwan_instance_questionnaires (
    assignment_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,
    instance_id UUID NOT NULL,
    questionnaire_version_id UUID NOT NULL,
    assignment_version BIGINT NOT NULL,
    assigned_by UUID NOT NULL REFERENCES principals(id),
    assigned_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT xw_instance_questionnaire_assignment_version_check
        CHECK (assignment_version >= 1),
    CONSTRAINT xw_instance_questionnaire_version_fkey
        FOREIGN KEY (tenant_id, instance_id, questionnaire_version_id)
        REFERENCES xiangwan_questionnaire_versions (
            tenant_id,
            instance_id,
            questionnaire_version_id
        ),
    CONSTRAINT xw_instance_questionnaire_assignment_key
        UNIQUE (tenant_id, instance_id, assignment_version),
    CONSTRAINT xw_instance_questionnaire_version_key
        UNIQUE (tenant_id, instance_id, questionnaire_version_id)
);

CREATE INDEX idx_xw_questionnaire_fields_version_order
    ON xiangwan_questionnaire_fields (
        questionnaire_version_id,
        sort_order,
        field_id
    );
CREATE INDEX idx_xw_instance_questionnaires_latest
    ON xiangwan_instance_questionnaires (
        tenant_id,
        instance_id,
        assignment_version DESC
    );

CREATE OR REPLACE FUNCTION xiangwan_guard_questionnaire_field_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    old_version_id UUID;
    new_version_id UUID;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        old_version_id := OLD.questionnaire_version_id;
        IF EXISTS (
            SELECT 1
            FROM xiangwan_questionnaire_versions AS version
            WHERE version.questionnaire_version_id = old_version_id
              AND version.status <> 'draft'
        ) THEN
            RAISE EXCEPTION 'published Xiangwan questionnaire fields are immutable'
                USING ERRCODE = '23514';
        END IF;
    END IF;

    IF TG_OP <> 'DELETE' THEN
        new_version_id := NEW.questionnaire_version_id;
        IF EXISTS (
            SELECT 1
            FROM xiangwan_questionnaire_versions AS version
            WHERE version.questionnaire_version_id = new_version_id
              AND version.status <> 'draft'
        ) THEN
            RAISE EXCEPTION 'published Xiangwan questionnaire fields are immutable'
                USING ERRCODE = '23514';
        END IF;
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION xiangwan_guard_questionnaire_version_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF OLD.status <> 'draft'
       OR EXISTS (
           SELECT 1
           FROM xiangwan_instance_questionnaires AS assignment
           WHERE assignment.questionnaire_version_id = OLD.questionnaire_version_id
       ) THEN
        RAISE EXCEPTION 'published or assigned Xiangwan questionnaire versions are immutable'
            USING ERRCODE = '23514';
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION xiangwan_validate_questionnaire_assignment()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    field_count INTEGER;
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM xiangwan_questionnaire_versions AS version
        WHERE version.tenant_id = NEW.tenant_id
          AND version.instance_id = NEW.instance_id
          AND version.questionnaire_version_id = NEW.questionnaire_version_id
          AND version.status = 'published'
          AND version.published_at <= NEW.assigned_at
    ) THEN
        RAISE EXCEPTION 'Xiangwan questionnaire assignment requires a published version'
            USING ERRCODE = '23514';
    END IF;
    SELECT COUNT(*)
    INTO field_count
    FROM xiangwan_questionnaire_fields AS field
    WHERE field.questionnaire_version_id = NEW.questionnaire_version_id;
    IF field_count NOT BETWEEN 1 AND 100 THEN
        RAISE EXCEPTION 'Xiangwan questionnaire assignment requires 1 to 100 fields'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION xiangwan_reject_questionnaire_assignment_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'Xiangwan questionnaire assignments are append-only'
        USING ERRCODE = '23514';
END;
$$;

CREATE OR REPLACE FUNCTION xiangwan_reject_questionnaire_truncate()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'published Xiangwan questionnaire contracts cannot be truncated'
        USING ERRCODE = '23514';
END;
$$;

CREATE TRIGGER trg_xw_questionnaire_field_guard
    BEFORE INSERT OR UPDATE OR DELETE ON xiangwan_questionnaire_fields
    FOR EACH ROW EXECUTE FUNCTION xiangwan_guard_questionnaire_field_mutation();

CREATE TRIGGER trg_xw_questionnaire_version_guard
    BEFORE UPDATE OR DELETE ON xiangwan_questionnaire_versions
    FOR EACH ROW EXECUTE FUNCTION xiangwan_guard_questionnaire_version_mutation();

CREATE TRIGGER trg_xw_questionnaire_assignment_validate
    BEFORE INSERT ON xiangwan_instance_questionnaires
    FOR EACH ROW EXECUTE FUNCTION xiangwan_validate_questionnaire_assignment();

CREATE TRIGGER trg_xw_questionnaire_assignment_immutable
    BEFORE UPDATE OR DELETE ON xiangwan_instance_questionnaires
    FOR EACH ROW EXECUTE FUNCTION xiangwan_reject_questionnaire_assignment_mutation();

CREATE TRIGGER trg_xw_questionnaire_assignment_no_truncate
    BEFORE TRUNCATE ON xiangwan_instance_questionnaires
    FOR EACH STATEMENT EXECUTE FUNCTION xiangwan_reject_questionnaire_assignment_mutation();

CREATE TRIGGER trg_xw_questionnaire_version_no_truncate
    BEFORE TRUNCATE ON xiangwan_questionnaire_versions
    FOR EACH STATEMENT EXECUTE FUNCTION xiangwan_reject_questionnaire_truncate();

CREATE TRIGGER trg_xw_questionnaire_field_no_truncate
    BEFORE TRUNCATE ON xiangwan_questionnaire_fields
    FOR EACH STATEMENT EXECUTE FUNCTION xiangwan_reject_questionnaire_truncate();
