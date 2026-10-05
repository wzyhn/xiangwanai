-- 764_xiangwan_resource_publications.up.sql
-- Immutable availability facts fenced by exact moderation and current state.
-- Content stays private in shared storage; this fact controls product access.

CREATE TABLE IF NOT EXISTS xiangwan_resource_publications (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    relation_id UUID NOT NULL,
    content_id UUID NOT NULL REFERENCES contents(id),
    content_revision_at TIMESTAMPTZ NOT NULL,
    approval_observation_id UUID NOT NULL,
    access_policy VARCHAR(32) NOT NULL,
    expected_target_version BIGINT NOT NULL,
    published_by UUID NOT NULL REFERENCES principals(id),
    idempotency_key VARCHAR(128) NOT NULL,
    published_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT xw_resource_publications_relation_fkey
        FOREIGN KEY (tenant_id, relation_id)
        REFERENCES xiangwan_resource_relations (tenant_id, id),
    CONSTRAINT xw_resource_publications_approval_fkey
        FOREIGN KEY (tenant_id, approval_observation_id)
        REFERENCES xiangwan_resource_moderation_observations (tenant_id, id),
    CONSTRAINT xw_resource_publications_tenant_id_id_key
        UNIQUE (tenant_id, id),
    CONSTRAINT xw_resource_publications_relation_key
        UNIQUE (tenant_id, relation_id),
    CONSTRAINT xw_resource_publications_idempotency_key
        UNIQUE (tenant_id, published_by, idempotency_key),
    CONSTRAINT xw_resource_publications_access_check
        CHECK (access_policy IN ('public', 'confirmed_registration')),
    CONSTRAINT xw_resource_publications_target_version_check
        CHECK (expected_target_version >= 1),
    CONSTRAINT xw_resource_publications_idempotency_format_check
        CHECK (
            idempotency_key ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$'
        ),
    CONSTRAINT xw_resource_publications_time_check
        CHECK (content_revision_at <= published_at)
);

CREATE INDEX IF NOT EXISTS idx_xw_resource_publications_content
    ON xiangwan_resource_publications (
        tenant_id, content_id, content_revision_at, published_at DESC, id DESC
    );

CREATE OR REPLACE FUNCTION xiangwan_validate_resource_publication()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    bound_instance_id UUID;
    bound_session_id UUID;
    bound_relation_kind VARCHAR(32);
    bound_content_id UUID;
    bound_content_revision TIMESTAMPTZ;
    bound_access_policy VARCHAR(32);
    bound_target_version BIGINT;
    bound_created_by UUID;
    approval_relation_id UUID;
    approval_content_id UUID;
    approval_content_revision TIMESTAMPTZ;
    approval_decision VARCHAR(16);
    approval_recorded_at TIMESTAMPTZ;
    current_instance_status VARCHAR(24);
    current_instance_version BIGINT;
    current_session_instance_id UUID;
    current_session_status VARCHAR(24);
    current_session_version BIGINT;
    current_content_tenant_id UUID;
    current_content_principal_id UUID;
    current_content_type VARCHAR(50);
    current_content_status VARCHAR(20);
    current_content_visibility VARCHAR(20);
    current_content_revision TIMESTAMPTZ;
BEGIN
    -- Exact retries remain readable after later state changes. The unique key
    -- makes the INSERT a no-op; the application compares the stored intent.
    IF EXISTS (
        SELECT 1
        FROM xiangwan_resource_publications AS existing
        WHERE existing.tenant_id = NEW.tenant_id
          AND existing.published_by = NEW.published_by
          AND existing.idempotency_key = NEW.idempotency_key
    ) THEN
        RETURN NEW;
    END IF;

    SELECT
        relation.instance_id,
        relation.session_id,
        relation.relation_kind,
        relation.content_id,
        relation.content_revision_at,
        relation.access_policy,
        relation.expected_target_version,
        relation.created_by
    INTO
        bound_instance_id,
        bound_session_id,
        bound_relation_kind,
        bound_content_id,
        bound_content_revision,
        bound_access_policy,
        bound_target_version,
        bound_created_by
    FROM xiangwan_resource_relations AS relation
    WHERE relation.tenant_id = NEW.tenant_id
      AND relation.id = NEW.relation_id
    FOR SHARE;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'xiangwan publication ResourceRelation is missing'
            USING ERRCODE = '23503';
    END IF;
    IF bound_content_id <> NEW.content_id
       OR bound_content_revision <> NEW.content_revision_at
       OR bound_access_policy <> NEW.access_policy
       OR bound_target_version <> NEW.expected_target_version THEN
        RAISE EXCEPTION 'xiangwan publication relation binding mismatch'
            USING ERRCODE = '23514';
    END IF;

    SELECT
        observation.relation_id,
        observation.content_id,
        observation.content_revision_at,
        observation.decision,
        observation.recorded_at
    INTO
        approval_relation_id,
        approval_content_id,
        approval_content_revision,
        approval_decision,
        approval_recorded_at
    FROM xiangwan_resource_moderation_observations AS observation
    WHERE observation.tenant_id = NEW.tenant_id
      AND observation.id = NEW.approval_observation_id
    FOR SHARE;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'xiangwan publication moderation approval is missing'
            USING ERRCODE = '23503';
    END IF;
    IF approval_relation_id <> NEW.relation_id
       OR approval_content_id <> NEW.content_id
       OR approval_content_revision <> NEW.content_revision_at
       OR approval_decision <> 'approved'
       OR approval_recorded_at > NEW.published_at THEN
        RAISE EXCEPTION 'invalid xiangwan publication moderation approval'
            USING ERRCODE = '23514';
    END IF;

    SELECT instance.status, instance.version
    INTO current_instance_status, current_instance_version
    FROM xiangwan_activity_instances AS instance
    WHERE instance.tenant_id = NEW.tenant_id
      AND instance.id = bound_instance_id
    FOR SHARE;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'xiangwan publication Instance is missing'
            USING ERRCODE = '23503';
    END IF;
    IF bound_relation_kind = 'instance_review' THEN
        IF current_instance_version <> NEW.expected_target_version THEN
            RAISE EXCEPTION 'stale xiangwan review publication target'
                USING ERRCODE = '40001';
        END IF;
        IF current_instance_status NOT IN ('completed', 'archived') THEN
            RAISE EXCEPTION 'unsafe xiangwan review publication target'
                USING ERRCODE = '23514';
        END IF;
    ELSE
        IF current_instance_status NOT IN (
            'published', 'completed', 'archived'
        ) THEN
            RAISE EXCEPTION 'unsafe xiangwan resource publication Instance'
                USING ERRCODE = '23514';
        END IF;

        SELECT session.instance_id, session.status, session.version
        INTO current_session_instance_id,
             current_session_status,
             current_session_version
        FROM xiangwan_activity_sessions AS session
        WHERE session.tenant_id = NEW.tenant_id
          AND session.id = bound_session_id
        FOR SHARE;

        IF NOT FOUND OR current_session_instance_id <> bound_instance_id THEN
            RAISE EXCEPTION 'xiangwan publication Session hierarchy mismatch'
                USING ERRCODE = '23503';
        END IF;
        IF current_session_status NOT IN ('published', 'ended', 'archived') THEN
            RAISE EXCEPTION 'unsafe xiangwan Session resource publication'
                USING ERRCODE = '23514';
        END IF;
        IF current_session_version <> NEW.expected_target_version THEN
            RAISE EXCEPTION 'stale xiangwan Session resource publication'
                USING ERRCODE = '40001';
        END IF;
    END IF;

    SELECT
        content.tenant_id,
        content.principal_id,
        content.type,
        content.status,
        content.visibility,
        content.updated_at
    INTO
        current_content_tenant_id,
        current_content_principal_id,
        current_content_type,
        current_content_status,
        current_content_visibility,
        current_content_revision
    FROM contents AS content
    WHERE content.id = NEW.content_id
      AND content.deleted_at IS NULL
    FOR SHARE;

    IF NOT FOUND OR current_content_tenant_id <> NEW.tenant_id THEN
        RAISE EXCEPTION 'xiangwan publication Content scope mismatch'
            USING ERRCODE = '23503';
    END IF;
    IF current_content_principal_id <> bound_created_by
       OR current_content_type <> 'review'
       OR current_content_status NOT IN ('active', 'reviewing')
       OR current_content_visibility <> 'private'
       OR current_content_revision <> NEW.content_revision_at THEN
        RAISE EXCEPTION 'stale or unsafe xiangwan publication Content'
            USING ERRCODE = '40001';
    END IF;

    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_xw_resource_publications_validate
    ON xiangwan_resource_publications;
CREATE TRIGGER trg_xw_resource_publications_validate
    BEFORE INSERT ON xiangwan_resource_publications
    FOR EACH ROW EXECUTE FUNCTION xiangwan_validate_resource_publication();

CREATE OR REPLACE FUNCTION xiangwan_reject_resource_publication_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'xiangwan resource publications are immutable'
        USING ERRCODE = '23514';
END;
$$;

DROP TRIGGER IF EXISTS trg_xw_resource_publications_no_update
    ON xiangwan_resource_publications;
CREATE TRIGGER trg_xw_resource_publications_no_update
    BEFORE UPDATE ON xiangwan_resource_publications
    FOR EACH ROW EXECUTE FUNCTION xiangwan_reject_resource_publication_mutation();

DROP TRIGGER IF EXISTS trg_xw_resource_publications_no_delete
    ON xiangwan_resource_publications;
CREATE TRIGGER trg_xw_resource_publications_no_delete
    BEFORE DELETE ON xiangwan_resource_publications
    FOR EACH ROW EXECUTE FUNCTION xiangwan_reject_resource_publication_mutation();

DROP TRIGGER IF EXISTS trg_xw_resource_publications_no_truncate
    ON xiangwan_resource_publications;
CREATE TRIGGER trg_xw_resource_publications_no_truncate
    BEFORE TRUNCATE ON xiangwan_resource_publications
    FOR EACH STATEMENT
    EXECUTE FUNCTION xiangwan_reject_resource_publication_mutation();
