-- 770_xiangwan_registration_submission_snapshots.up.sql
-- Immutable contact, price, questionnaire, and answer evidence committed in
-- the same PostgreSQL transaction as a consumer Registration.

ALTER TABLE xiangwan_questionnaire_fields
    ADD CONSTRAINT xw_questionnaire_field_identity_key
        UNIQUE (
            tenant_id,
            instance_id,
            questionnaire_version_id,
            field_id
        );

CREATE OR REPLACE FUNCTION xiangwan_valid_questionnaire_answer(
    field_type TEXT,
    is_required BOOLEAN,
    min_length INTEGER,
    max_length INTEGER,
    max_selections INTEGER,
    options JSONB,
    answer_values JSONB
)
RETURNS BOOLEAN
LANGUAGE plpgsql
IMMUTABLE
PARALLEL SAFE
AS $$
DECLARE
    answer_value JSONB;
    answer_text TEXT;
    answer_count INTEGER;
    seen_values TEXT[] := ARRAY[]::TEXT[];
BEGIN
    IF JSONB_TYPEOF(answer_values) <> 'array' THEN
        RETURN FALSE;
    END IF;
    answer_count := JSONB_ARRAY_LENGTH(answer_values);
    IF answer_count > 100 OR (is_required AND answer_count = 0) THEN
        RETURN FALSE;
    END IF;
    IF field_type IN ('single_choice', 'area', 'single_line', 'multiline')
       AND answer_count > 1 THEN
        RETURN FALSE;
    END IF;
    IF field_type = 'multiple_choice'
       AND (max_selections IS NULL OR answer_count > max_selections) THEN
        RETURN FALSE;
    END IF;

    FOR answer_value IN SELECT value FROM JSONB_ARRAY_ELEMENTS(answer_values)
    LOOP
        IF JSONB_TYPEOF(answer_value) <> 'string' THEN
            RETURN FALSE;
        END IF;
        answer_text := answer_value #>> '{}';
        IF answer_text = ANY(seen_values) THEN
            RETURN FALSE;
        END IF;
        seen_values := ARRAY_APPEND(seen_values, answer_text);

        IF field_type IN ('single_choice', 'multiple_choice', 'area') THEN
            IF NOT EXISTS (
                SELECT 1
                FROM JSONB_ARRAY_ELEMENTS(options) AS option(value)
                WHERE option.value ->> 'code' = answer_text
            ) THEN
                RETURN FALSE;
            END IF;
        ELSIF field_type IN ('single_line', 'multiline') THEN
            IF BTRIM(answer_text) = ''
               OR CHAR_LENGTH(answer_text) < COALESCE(min_length, 0)
               OR CHAR_LENGTH(answer_text) > max_length
               OR (
                   field_type = 'single_line'
                   AND answer_text ~ E'[\\r\\n]'
               ) THEN
                RETURN FALSE;
            END IF;
        ELSE
            RETURN FALSE;
        END IF;
    END LOOP;
    RETURN TRUE;
END;
$$;

CREATE TABLE xiangwan_registration_snapshots (
    registration_id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL,
    series_id UUID NOT NULL,
    instance_id UUID NOT NULL,
    session_id UUID NOT NULL,
    principal_id UUID NOT NULL,
    instance_publication_version BIGINT NOT NULL,
    session_version BIGINT NOT NULL,
    price_cents BIGINT NOT NULL,
    contact_source VARCHAR(24) NOT NULL,
    contact_name VARCHAR(100) NOT NULL,
    contact_phone_e164 VARCHAR(16) NOT NULL,
    contact_policy_version VARCHAR(100) NOT NULL,
    questionnaire_version_id UUID,
    questionnaire_version BIGINT,
    questionnaire_privacy_purpose VARCHAR(500),
    questionnaire_privacy_policy_version VARCHAR(100),
    request_fingerprint CHAR(64) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT xw_registration_snapshot_versions_check
        CHECK (
            instance_publication_version >= 1
            AND session_version >= 1
        ),
    CONSTRAINT xw_registration_snapshot_price_check
        CHECK (price_cents >= 0),
    CONSTRAINT xw_registration_snapshot_contact_check
        CHECK (
            contact_source = 'manual'
            AND contact_name = BTRIM(contact_name)
            AND CHAR_LENGTH(contact_name) BETWEEN 1 AND 100
            AND contact_phone_e164 ~ '^\+[1-9][0-9]{7,14}$'
            AND contact_policy_version = BTRIM(contact_policy_version)
            AND CHAR_LENGTH(contact_policy_version) BETWEEN 1 AND 100
        ),
    CONSTRAINT xw_registration_snapshot_questionnaire_shape_check
        CHECK (
            (
                questionnaire_version_id IS NULL
                AND questionnaire_version IS NULL
                AND questionnaire_privacy_purpose IS NULL
                AND questionnaire_privacy_policy_version IS NULL
            )
            OR (
                questionnaire_version_id IS NOT NULL
                AND questionnaire_version >= 1
                AND questionnaire_privacy_purpose =
                    BTRIM(questionnaire_privacy_purpose)
                AND CHAR_LENGTH(questionnaire_privacy_purpose)
                    BETWEEN 1 AND 500
                AND questionnaire_privacy_policy_version =
                    BTRIM(questionnaire_privacy_policy_version)
                AND CHAR_LENGTH(questionnaire_privacy_policy_version)
                    BETWEEN 1 AND 100
            )
        ),
    CONSTRAINT xw_registration_snapshot_fingerprint_check
        CHECK (request_fingerprint ~ '^[0-9a-f]{64}$'),
    CONSTRAINT xw_registration_snapshot_registration_fkey
        FOREIGN KEY (
            tenant_id,
            series_id,
            instance_id,
            session_id,
            principal_id,
            registration_id
        )
        REFERENCES xiangwan_registrations (
            tenant_id,
            series_id,
            instance_id,
            session_id,
            principal_id,
            id
        ),
    CONSTRAINT xw_registration_snapshot_questionnaire_fkey
        FOREIGN KEY (
            tenant_id,
            instance_id,
            questionnaire_version_id
        )
        REFERENCES xiangwan_questionnaire_versions (
            tenant_id,
            instance_id,
            questionnaire_version_id
        ),
    CONSTRAINT xw_registration_snapshot_tenant_registration_key
        UNIQUE (tenant_id, registration_id),
    CONSTRAINT xw_registration_snapshot_questionnaire_key
        UNIQUE (tenant_id, registration_id, questionnaire_version_id)
);

CREATE TABLE xiangwan_registration_answers (
    tenant_id UUID NOT NULL,
    instance_id UUID NOT NULL,
    registration_id UUID NOT NULL,
    questionnaire_version_id UUID NOT NULL,
    field_id UUID NOT NULL,
    field_code VARCHAR(64) NOT NULL,
    field_type VARCHAR(24) NOT NULL,
    field_label VARCHAR(200) NOT NULL,
    field_help_text VARCHAR(500) NOT NULL,
    is_required BOOLEAN NOT NULL,
    sort_order INTEGER NOT NULL,
    min_length INTEGER,
    max_length INTEGER,
    max_selections INTEGER,
    options JSONB NOT NULL,
    answer_values JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT xw_registration_answer_field_code_check
        CHECK (field_code ~ '^[a-z][a-z0-9_]{0,63}$'),
    CONSTRAINT xw_registration_answer_field_type_check
        CHECK (
            field_type IN (
                'single_choice',
                'multiple_choice',
                'single_line',
                'multiline',
                'area'
            )
        ),
    CONSTRAINT xw_registration_answer_text_check
        CHECK (
            field_label = BTRIM(field_label)
            AND CHAR_LENGTH(field_label) BETWEEN 1 AND 200
            AND field_help_text = BTRIM(field_help_text)
            AND CHAR_LENGTH(field_help_text) <= 500
        ),
    CONSTRAINT xw_registration_answer_order_check CHECK (sort_order >= 0),
    CONSTRAINT xw_registration_answer_options_check
        CHECK (xiangwan_valid_questionnaire_options(options)),
    CONSTRAINT xw_registration_answer_value_check
        CHECK (
            xiangwan_valid_questionnaire_answer(
                field_type,
                is_required,
                min_length,
                max_length,
                max_selections,
                options,
                answer_values
            )
        ),
    CONSTRAINT xw_registration_answer_snapshot_fkey
        FOREIGN KEY (
            tenant_id,
            registration_id,
            questionnaire_version_id
        )
        REFERENCES xiangwan_registration_snapshots (
            tenant_id,
            registration_id,
            questionnaire_version_id
        ),
    CONSTRAINT xw_registration_answer_field_fkey
        FOREIGN KEY (
            tenant_id,
            instance_id,
            questionnaire_version_id,
            field_id
        )
        REFERENCES xiangwan_questionnaire_fields (
            tenant_id,
            instance_id,
            questionnaire_version_id,
            field_id
        ),
    CONSTRAINT xw_registration_answer_registration_field_key
        PRIMARY KEY (registration_id, field_id),
    CONSTRAINT xw_registration_answer_registration_order_key
        UNIQUE (registration_id, sort_order)
);

CREATE INDEX idx_xw_registration_answers_version
    ON xiangwan_registration_answers (
        tenant_id,
        questionnaire_version_id,
        registration_id,
        sort_order
    );

-- Registration locks the exact Instance before selecting its latest
-- questionnaire assignment. Make assignment publication take that same row
-- lock so neither transaction can commit across a stale "current" decision.
CREATE OR REPLACE FUNCTION xiangwan_lock_questionnaire_assignment_instance()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    PERFORM 1
    FROM xiangwan_activity_instances AS instance
    WHERE instance.tenant_id = NEW.tenant_id
      AND instance.id = NEW.instance_id
    FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'Xiangwan questionnaire assignment Instance is unavailable'
            USING ERRCODE = '23503';
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION xiangwan_reject_registration_submission_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'Xiangwan Registration submission snapshots are immutable'
        USING ERRCODE = '23514';
END;
$$;

CREATE OR REPLACE FUNCTION xiangwan_validate_registration_answer_snapshot()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM xiangwan_questionnaire_fields AS field
        WHERE field.tenant_id = NEW.tenant_id
          AND field.instance_id = NEW.instance_id
          AND field.questionnaire_version_id = NEW.questionnaire_version_id
          AND field.field_id = NEW.field_id
          AND field.field_code = NEW.field_code
          AND field.field_type = NEW.field_type
          AND field.label = NEW.field_label
          AND field.help_text = NEW.field_help_text
          AND field.is_required = NEW.is_required
          AND field.sort_order = NEW.sort_order
          AND field.min_length IS NOT DISTINCT FROM NEW.min_length
          AND field.max_length IS NOT DISTINCT FROM NEW.max_length
          AND field.max_selections IS NOT DISTINCT FROM NEW.max_selections
          AND field.options = NEW.options
    ) THEN
        RAISE EXCEPTION 'Xiangwan Registration answer field snapshot is stale'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER trg_xw_registration_snapshot_immutable
    BEFORE UPDATE OR DELETE ON xiangwan_registration_snapshots
    FOR EACH ROW
    EXECUTE FUNCTION xiangwan_reject_registration_submission_mutation();

CREATE TRIGGER trg_xw_registration_snapshot_no_truncate
    BEFORE TRUNCATE ON xiangwan_registration_snapshots
    FOR EACH STATEMENT
    EXECUTE FUNCTION xiangwan_reject_registration_submission_mutation();

CREATE TRIGGER trg_xw_questionnaire_assignment_instance_lock
    BEFORE INSERT ON xiangwan_instance_questionnaires
    FOR EACH ROW
    EXECUTE FUNCTION xiangwan_lock_questionnaire_assignment_instance();

CREATE TRIGGER trg_xw_registration_answer_immutable
    BEFORE UPDATE OR DELETE ON xiangwan_registration_answers
    FOR EACH ROW
    EXECUTE FUNCTION xiangwan_reject_registration_submission_mutation();

CREATE TRIGGER trg_xw_registration_answer_validate
    BEFORE INSERT ON xiangwan_registration_answers
    FOR EACH ROW
    EXECUTE FUNCTION xiangwan_validate_registration_answer_snapshot();

CREATE TRIGGER trg_xw_registration_answer_no_truncate
    BEFORE TRUNCATE ON xiangwan_registration_answers
    FOR EACH STATEMENT
    EXECUTE FUNCTION xiangwan_reject_registration_submission_mutation();
