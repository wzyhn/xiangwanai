-- 762_xiangwan_resource_relations.up.sql
-- Immutable draft bindings over the shared Content storage primitive.
-- A binding is not a publication fact and therefore never makes Content public.

CREATE TABLE IF NOT EXISTS xiangwan_resource_relations (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    series_id UUID NOT NULL,
    instance_id UUID NOT NULL,
    session_id UUID,
    relation_kind VARCHAR(32) NOT NULL,
    content_id UUID NOT NULL REFERENCES contents(id),
    content_revision_at TIMESTAMPTZ NOT NULL,
    access_policy VARCHAR(32) NOT NULL,
    sort_order INTEGER NOT NULL,
    expected_target_version BIGINT NOT NULL,
    created_by UUID NOT NULL REFERENCES principals(id),
    idempotency_key VARCHAR(128) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT xw_resource_relations_tenant_instance_fkey
        FOREIGN KEY (tenant_id, series_id, instance_id)
        REFERENCES xiangwan_activity_instances (tenant_id, series_id, id),
    CONSTRAINT xw_resource_relations_tenant_session_fkey
        FOREIGN KEY (tenant_id, session_id)
        REFERENCES xiangwan_activity_sessions (tenant_id, id),
    CONSTRAINT xw_resource_relations_tenant_id_id_key
        UNIQUE (tenant_id, id),
    CONSTRAINT xw_resource_relations_content_key
        UNIQUE (tenant_id, content_id),
    CONSTRAINT xw_resource_relations_idempotency_key
        UNIQUE (tenant_id, created_by, idempotency_key),
    CONSTRAINT xw_resource_relations_kind_check
        CHECK (relation_kind IN ('instance_review', 'session_resources')),
    CONSTRAINT xw_resource_relations_access_check
        CHECK (access_policy IN ('public', 'confirmed_registration')),
    CONSTRAINT xw_resource_relations_shape_check
        CHECK (
            (
                relation_kind = 'instance_review'
                AND session_id IS NULL
                AND access_policy = 'public'
            )
            OR (
                relation_kind = 'session_resources'
                AND session_id IS NOT NULL
            )
        ),
    CONSTRAINT xw_resource_relations_sort_check
        CHECK (sort_order >= 0 AND sort_order <= 10000),
    CONSTRAINT xw_resource_relations_target_version_check
        CHECK (expected_target_version >= 1),
    CONSTRAINT xw_resource_relations_idempotency_format_check
        CHECK (
            idempotency_key ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$'
        ),
    CONSTRAINT xw_resource_relations_time_check
        CHECK (content_revision_at <= created_at)
);

CREATE INDEX IF NOT EXISTS idx_xw_resource_relations_instance
    ON xiangwan_resource_relations (
        tenant_id, series_id, instance_id, relation_kind, sort_order, id
    );

CREATE INDEX IF NOT EXISTS idx_xw_resource_relations_session
    ON xiangwan_resource_relations (
        tenant_id, session_id, relation_kind, sort_order, id
    )
    WHERE session_id IS NOT NULL;

CREATE OR REPLACE FUNCTION xiangwan_validate_resource_relation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
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
    -- Exact retries read the immutable receipt even when the Content or target
    -- changed later. The unique key will turn this INSERT into a no-op and the
    -- application compares the stored intent; no current fact is re-evaluated.
    IF EXISTS (
        SELECT 1
        FROM xiangwan_resource_relations AS existing
        WHERE existing.tenant_id = NEW.tenant_id
          AND existing.created_by = NEW.created_by
          AND existing.idempotency_key = NEW.idempotency_key
    ) THEN
        RETURN NEW;
    END IF;

    SELECT instance.status, instance.version
    INTO current_instance_status, current_instance_version
    FROM xiangwan_activity_instances AS instance
    WHERE instance.tenant_id = NEW.tenant_id
      AND instance.series_id = NEW.series_id
      AND instance.id = NEW.instance_id;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'xiangwan ResourceRelation Instance is missing'
            USING ERRCODE = '23503';
    END IF;

    IF NEW.relation_kind = 'instance_review' THEN
        IF current_instance_status NOT IN ('completed', 'archived') THEN
            RAISE EXCEPTION
                'xiangwan Instance review requires completed history'
                USING ERRCODE = '23514';
        END IF;
        IF current_instance_version <> NEW.expected_target_version THEN
            RAISE EXCEPTION 'stale xiangwan Instance review target'
                USING ERRCODE = '40001';
        END IF;
    ELSE
        IF current_instance_status NOT IN (
            'published', 'completed', 'archived'
        ) THEN
            RAISE EXCEPTION
                'xiangwan Session resources require a public history target'
                USING ERRCODE = '23514';
        END IF;

        SELECT session.instance_id, session.status, session.version
        INTO current_session_instance_id,
             current_session_status,
             current_session_version
        FROM xiangwan_activity_sessions AS session
        WHERE session.tenant_id = NEW.tenant_id
          AND session.id = NEW.session_id;

        IF NOT FOUND OR current_session_instance_id <> NEW.instance_id THEN
            RAISE EXCEPTION
                'xiangwan ResourceRelation Session hierarchy mismatch'
                USING ERRCODE = '23503';
        END IF;
        IF current_session_status NOT IN ('published', 'ended', 'archived') THEN
            RAISE EXCEPTION
                'xiangwan Session resources cannot target draft or cancelled Session'
                USING ERRCODE = '23514';
        END IF;
        IF current_session_version <> NEW.expected_target_version THEN
            RAISE EXCEPTION 'stale xiangwan Session resource target'
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
      AND content.deleted_at IS NULL;

    IF NOT FOUND OR current_content_tenant_id <> NEW.tenant_id THEN
        RAISE EXCEPTION 'xiangwan ResourceRelation Content scope mismatch'
            USING ERRCODE = '23503';
    END IF;
    IF current_content_principal_id <> NEW.created_by
       OR current_content_type <> 'review'
       OR current_content_status NOT IN ('active', 'reviewing')
       OR current_content_visibility <> 'private' THEN
        RAISE EXCEPTION 'unsafe xiangwan ResourceRelation Content candidate'
            USING ERRCODE = '23514';
    END IF;
    IF current_content_revision <> NEW.content_revision_at THEN
        RAISE EXCEPTION 'stale xiangwan ResourceRelation Content revision'
            USING ERRCODE = '40001';
    END IF;

    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_xw_resource_relations_validate
    ON xiangwan_resource_relations;
CREATE TRIGGER trg_xw_resource_relations_validate
    BEFORE INSERT ON xiangwan_resource_relations
    FOR EACH ROW EXECUTE FUNCTION xiangwan_validate_resource_relation();

CREATE OR REPLACE FUNCTION xiangwan_reject_resource_relation_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'xiangwan ResourceRelation drafts are immutable'
        USING ERRCODE = '23514';
END;
$$;

DROP TRIGGER IF EXISTS trg_xw_resource_relations_no_update
    ON xiangwan_resource_relations;
CREATE TRIGGER trg_xw_resource_relations_no_update
    BEFORE UPDATE ON xiangwan_resource_relations
    FOR EACH ROW EXECUTE FUNCTION xiangwan_reject_resource_relation_mutation();

DROP TRIGGER IF EXISTS trg_xw_resource_relations_no_delete
    ON xiangwan_resource_relations;
CREATE TRIGGER trg_xw_resource_relations_no_delete
    BEFORE DELETE ON xiangwan_resource_relations
    FOR EACH ROW EXECUTE FUNCTION xiangwan_reject_resource_relation_mutation();

DROP TRIGGER IF EXISTS trg_xw_resource_relations_no_truncate
    ON xiangwan_resource_relations;
CREATE TRIGGER trg_xw_resource_relations_no_truncate
    BEFORE TRUNCATE ON xiangwan_resource_relations
    FOR EACH STATEMENT
    EXECUTE FUNCTION xiangwan_reject_resource_relation_mutation();
