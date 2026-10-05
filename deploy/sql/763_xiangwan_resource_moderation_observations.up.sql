-- 763_xiangwan_resource_moderation_observations.up.sql
-- Immutable evidence about one exact ResourceRelation and Content revision.
-- An approved observation is necessary but not sufficient for publication.

CREATE TABLE IF NOT EXISTS xiangwan_resource_moderation_observations (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    relation_id UUID NOT NULL,
    content_id UUID NOT NULL REFERENCES contents(id),
    content_revision_at TIMESTAMPTZ NOT NULL,
    provider VARCHAR(256) NOT NULL,
    provider_reference VARCHAR(256) NOT NULL,
    policy_version VARCHAR(256) NOT NULL,
    observation_source VARCHAR(32) NOT NULL,
    decision VARCHAR(16) NOT NULL,
    subject_digest BYTEA NOT NULL,
    payload_digest BYTEA NOT NULL,
    actor_id UUID REFERENCES principals(id),
    reason VARCHAR(500),
    observed_at TIMESTAMPTZ NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT xw_resource_moderation_relation_fkey
        FOREIGN KEY (tenant_id, relation_id)
        REFERENCES xiangwan_resource_relations (tenant_id, id),
    CONSTRAINT xw_resource_moderation_tenant_id_id_key
        UNIQUE (tenant_id, id),
    CONSTRAINT xw_resource_moderation_provider_reference_key
        UNIQUE (tenant_id, provider, provider_reference),
    CONSTRAINT xw_resource_moderation_provider_format_check
        CHECK (provider ~ '^[A-Za-z0-9][A-Za-z0-9._:/-]{0,255}$'),
    CONSTRAINT xw_resource_moderation_reference_format_check
        CHECK (
            provider_reference
                ~ '^[A-Za-z0-9][A-Za-z0-9._:/-]{0,255}$'
        ),
    CONSTRAINT xw_resource_moderation_policy_format_check
        CHECK (
            policy_version ~ '^[A-Za-z0-9][A-Za-z0-9._:/-]{0,255}$'
        ),
    CONSTRAINT xw_resource_moderation_source_check
        CHECK (
            observation_source IN (
                'signed_callback', 'provider_query', 'manual_review'
            )
        ),
    CONSTRAINT xw_resource_moderation_decision_check
        CHECK (decision IN ('approved', 'rejected', 'review', 'unknown')),
    CONSTRAINT xw_resource_moderation_evidence_shape_check
        CHECK (
            (
                observation_source IN ('signed_callback', 'provider_query')
                AND actor_id IS NULL
                AND reason IS NULL
            )
            OR (
                observation_source = 'manual_review'
                AND decision IN ('approved', 'rejected')
                AND actor_id IS NOT NULL
                AND reason IS NOT NULL
                AND reason = btrim(reason)
                AND char_length(reason) BETWEEN 1 AND 500
            )
        ),
    CONSTRAINT xw_resource_moderation_subject_digest_check
        CHECK (
            octet_length(subject_digest) = 32
            AND subject_digest <> decode(repeat('00', 32), 'hex')
        ),
    CONSTRAINT xw_resource_moderation_payload_digest_check
        CHECK (
            octet_length(payload_digest) = 32
            AND payload_digest <> decode(repeat('00', 32), 'hex')
        ),
    CONSTRAINT xw_resource_moderation_time_check
        CHECK (
            content_revision_at <= observed_at
            AND observed_at <= recorded_at
        )
);

CREATE INDEX IF NOT EXISTS idx_xw_resource_moderation_relation
    ON xiangwan_resource_moderation_observations (
        tenant_id, relation_id, decision, observed_at DESC, id DESC
    );

CREATE OR REPLACE FUNCTION xiangwan_validate_resource_moderation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    bound_content_id UUID;
    bound_content_revision TIMESTAMPTZ;
BEGIN
    -- Preserve replayability after the relation or Content has aged. The
    -- unique key and application layer compare the immutable stored intent.
    IF EXISTS (
        SELECT 1
        FROM xiangwan_resource_moderation_observations AS existing
        WHERE existing.tenant_id = NEW.tenant_id
          AND existing.provider = NEW.provider
          AND existing.provider_reference = NEW.provider_reference
    ) THEN
        RETURN NEW;
    END IF;

    SELECT relation.content_id, relation.content_revision_at
    INTO bound_content_id, bound_content_revision
    FROM xiangwan_resource_relations AS relation
    WHERE relation.tenant_id = NEW.tenant_id
      AND relation.id = NEW.relation_id;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'xiangwan moderation ResourceRelation is missing'
            USING ERRCODE = '23503';
    END IF;
    IF bound_content_id <> NEW.content_id
       OR bound_content_revision <> NEW.content_revision_at THEN
        RAISE EXCEPTION 'xiangwan moderation Content binding mismatch'
            USING ERRCODE = '23514';
    END IF;

    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_xw_resource_moderation_validate
    ON xiangwan_resource_moderation_observations;
CREATE TRIGGER trg_xw_resource_moderation_validate
    BEFORE INSERT ON xiangwan_resource_moderation_observations
    FOR EACH ROW EXECUTE FUNCTION xiangwan_validate_resource_moderation();

CREATE OR REPLACE FUNCTION xiangwan_reject_resource_moderation_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'xiangwan moderation observations are immutable'
        USING ERRCODE = '23514';
END;
$$;

DROP TRIGGER IF EXISTS trg_xw_resource_moderation_no_update
    ON xiangwan_resource_moderation_observations;
CREATE TRIGGER trg_xw_resource_moderation_no_update
    BEFORE UPDATE ON xiangwan_resource_moderation_observations
    FOR EACH ROW
    EXECUTE FUNCTION xiangwan_reject_resource_moderation_mutation();

DROP TRIGGER IF EXISTS trg_xw_resource_moderation_no_delete
    ON xiangwan_resource_moderation_observations;
CREATE TRIGGER trg_xw_resource_moderation_no_delete
    BEFORE DELETE ON xiangwan_resource_moderation_observations
    FOR EACH ROW
    EXECUTE FUNCTION xiangwan_reject_resource_moderation_mutation();

DROP TRIGGER IF EXISTS trg_xw_resource_moderation_no_truncate
    ON xiangwan_resource_moderation_observations;
CREATE TRIGGER trg_xw_resource_moderation_no_truncate
    BEFORE TRUNCATE ON xiangwan_resource_moderation_observations
    FOR EACH STATEMENT
    EXECUTE FUNCTION xiangwan_reject_resource_moderation_mutation();
