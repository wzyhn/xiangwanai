-- 765_xiangwan_resource_content_snapshots.up.sql
-- Freeze the exact shared Content payload assessed by moderation. Generic
-- Content and Block mutation must not alter a bound Xiangwan candidate.

CREATE TABLE IF NOT EXISTS xiangwan_resource_content_snapshots (
    relation_id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    content_id UUID NOT NULL REFERENCES contents(id),
    content_revision_at TIMESTAMPTZ NOT NULL,
    snapshot_schema VARCHAR(64) NOT NULL,
    subject_digest BYTEA NOT NULL,
    captured_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT xw_resource_snapshots_relation_fkey
        FOREIGN KEY (tenant_id, relation_id)
        REFERENCES xiangwan_resource_relations (tenant_id, id),
    CONSTRAINT xw_resource_snapshots_tenant_relation_key
        UNIQUE (tenant_id, relation_id),
    CONSTRAINT xw_resource_snapshots_content_key
        UNIQUE (tenant_id, content_id),
    CONSTRAINT xw_resource_snapshots_schema_check
        CHECK (snapshot_schema = 'xiangwan-resource-snapshot-v1'),
    CONSTRAINT xw_resource_snapshots_digest_check
        CHECK (
            octet_length(subject_digest) = 32
            AND subject_digest <> decode(repeat('00', 32), 'hex')
        ),
    CONSTRAINT xw_resource_snapshots_time_check
        CHECK (content_revision_at <= captured_at)
);

CREATE OR REPLACE FUNCTION xiangwan_guard_bound_resource_content()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    PERFORM pg_advisory_xact_lock(hashtextextended(OLD.id::text, 725));
    IF EXISTS (
        SELECT 1
        FROM xiangwan_resource_relations AS relation
        WHERE relation.content_id = OLD.id
    ) THEN
        RAISE EXCEPTION 'bound xiangwan resource Content is immutable'
            USING ERRCODE = '23514';
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_xw_bound_resource_content_guard ON contents;
CREATE TRIGGER trg_xw_bound_resource_content_guard
    BEFORE UPDATE OR DELETE ON contents
    FOR EACH ROW EXECUTE FUNCTION xiangwan_guard_bound_resource_content();

CREATE OR REPLACE FUNCTION xiangwan_guard_bound_resource_block()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    old_content_id UUID;
    new_content_id UUID;
    first_content_id UUID;
    second_content_id UUID;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        old_content_id := OLD.content_id;
    END IF;
    IF TG_OP <> 'DELETE' THEN
        new_content_id := NEW.content_id;
    END IF;

    IF old_content_id IS NULL OR new_content_id IS NULL
       OR old_content_id = new_content_id THEN
        first_content_id := COALESCE(old_content_id, new_content_id);
    ELSIF old_content_id::text < new_content_id::text THEN
        first_content_id := old_content_id;
        second_content_id := new_content_id;
    ELSE
        first_content_id := new_content_id;
        second_content_id := old_content_id;
    END IF;

    PERFORM pg_advisory_xact_lock(
        hashtextextended(first_content_id::text, 725)
    );
    IF second_content_id IS NOT NULL THEN
        PERFORM pg_advisory_xact_lock(
            hashtextextended(second_content_id::text, 725)
        );
    END IF;

    IF EXISTS (
        SELECT 1
        FROM xiangwan_resource_relations AS relation
        WHERE relation.content_id = old_content_id
           OR relation.content_id = new_content_id
    ) THEN
        RAISE EXCEPTION 'bound xiangwan resource Blocks are immutable'
            USING ERRCODE = '23514';
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_xw_bound_resource_block_guard ON blocks;
CREATE TRIGGER trg_xw_bound_resource_block_guard
    BEFORE INSERT OR UPDATE OR DELETE ON blocks
    FOR EACH ROW EXECUTE FUNCTION xiangwan_guard_bound_resource_block();

CREATE OR REPLACE FUNCTION xiangwan_guard_bound_resource_block_truncate()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF EXISTS (SELECT 1 FROM xiangwan_resource_relations) THEN
        RAISE EXCEPTION 'cannot truncate Blocks with bound xiangwan resources'
            USING ERRCODE = '23514';
    END IF;
    RETURN NULL;
END;
$$;

DROP TRIGGER IF EXISTS trg_xw_bound_resource_block_truncate ON blocks;
CREATE TRIGGER trg_xw_bound_resource_block_truncate
    BEFORE TRUNCATE ON blocks
    FOR EACH STATEMENT
    EXECUTE FUNCTION xiangwan_guard_bound_resource_block_truncate();

CREATE OR REPLACE FUNCTION xiangwan_capture_resource_content_snapshot()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    content_title VARCHAR(500);
    captured_blocks JSONB;
    captured_payload JSONB;
BEGIN
    PERFORM pg_advisory_xact_lock(
        hashtextextended(NEW.content_id::text, 725)
    );

    SELECT COALESCE(content.title, '')
    INTO content_title
    FROM contents AS content
    WHERE content.id = NEW.content_id
      AND content.tenant_id = NEW.tenant_id
      AND content.principal_id = NEW.created_by
      AND content.type = 'review'
      AND content.status IN ('active', 'reviewing')
      AND content.visibility = 'private'
      AND content.updated_at = NEW.content_revision_at
      AND content.deleted_at IS NULL
    FOR SHARE;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'stale xiangwan resource Content snapshot source'
            USING ERRCODE = '40001';
    END IF;

    SELECT COALESCE(
        jsonb_agg(
            jsonb_build_object(
                'id', block.id,
                'type', block.type,
                'sort_order', block.sort_order,
                'data', block.data
            )
            ORDER BY block.sort_order, block.id
        ),
        '[]'::jsonb
    )
    INTO captured_blocks
    FROM blocks AS block
    WHERE block.content_id = NEW.content_id;

    captured_payload := jsonb_build_object(
        'schema', 'xiangwan-resource-snapshot-v1',
        'title', content_title,
        'blocks', captured_blocks
    );

    INSERT INTO xiangwan_resource_content_snapshots (
        relation_id, tenant_id, content_id, content_revision_at,
        snapshot_schema, subject_digest, captured_at
    ) VALUES (
        NEW.id, NEW.tenant_id, NEW.content_id, NEW.content_revision_at,
        'xiangwan-resource-snapshot-v1',
        digest(convert_to(captured_payload::text, 'UTF8'), 'sha256'),
        clock_timestamp()
    );

    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_xw_resource_relations_capture_snapshot
    ON xiangwan_resource_relations;
CREATE TRIGGER trg_xw_resource_relations_capture_snapshot
    AFTER INSERT ON xiangwan_resource_relations
    FOR EACH ROW EXECUTE FUNCTION xiangwan_capture_resource_content_snapshot();

WITH snapshot_candidates AS (
    SELECT
        relation.id AS relation_id,
        relation.tenant_id,
        relation.content_id,
        relation.content_revision_at,
        jsonb_build_object(
            'schema', 'xiangwan-resource-snapshot-v1',
            'title', COALESCE(content.title, ''),
            'blocks', block_set.items
        ) AS snapshot_payload
    FROM xiangwan_resource_relations AS relation
    JOIN contents AS content
      ON content.id = relation.content_id
     AND content.tenant_id = relation.tenant_id
     AND content.principal_id = relation.created_by
     AND content.type = 'review'
     AND content.status IN ('active', 'reviewing')
     AND content.visibility = 'private'
     AND content.updated_at = relation.content_revision_at
     AND content.deleted_at IS NULL
    CROSS JOIN LATERAL (
        SELECT COALESCE(
            jsonb_agg(
                jsonb_build_object(
                    'id', block.id,
                    'type', block.type,
                    'sort_order', block.sort_order,
                    'data', block.data
                )
                ORDER BY block.sort_order, block.id
            ),
            '[]'::jsonb
        ) AS items
        FROM blocks AS block
        WHERE block.content_id = relation.content_id
    ) AS block_set
)
INSERT INTO xiangwan_resource_content_snapshots (
    relation_id, tenant_id, content_id, content_revision_at,
    snapshot_schema, subject_digest, captured_at
)
SELECT
    candidate.relation_id,
    candidate.tenant_id,
    candidate.content_id,
    candidate.content_revision_at,
    'xiangwan-resource-snapshot-v1',
    digest(
        convert_to(candidate.snapshot_payload::text, 'UTF8'),
        'sha256'
    ),
    clock_timestamp()
FROM snapshot_candidates AS candidate
ON CONFLICT (relation_id) DO NOTHING;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM xiangwan_resource_relations AS relation
        LEFT JOIN xiangwan_resource_content_snapshots AS snapshot
          ON snapshot.tenant_id = relation.tenant_id
         AND snapshot.relation_id = relation.id
        WHERE snapshot.relation_id IS NULL
    ) THEN
        RAISE EXCEPTION
            'cannot snapshot every existing xiangwan ResourceRelation'
            USING ERRCODE = '23514';
    END IF;
END;
$$;

CREATE OR REPLACE FUNCTION xiangwan_validate_moderation_snapshot_digest()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    expected_subject_digest BYTEA;
BEGIN
    IF EXISTS (
        SELECT 1
        FROM xiangwan_resource_moderation_observations AS existing
        WHERE existing.tenant_id = NEW.tenant_id
          AND existing.provider = NEW.provider
          AND existing.provider_reference = NEW.provider_reference
    ) THEN
        RETURN NEW;
    END IF;

    SELECT snapshot.subject_digest
    INTO expected_subject_digest
    FROM xiangwan_resource_content_snapshots AS snapshot
    WHERE snapshot.tenant_id = NEW.tenant_id
      AND snapshot.relation_id = NEW.relation_id
      AND snapshot.content_id = NEW.content_id
      AND snapshot.content_revision_at = NEW.content_revision_at
    FOR SHARE;

    IF NOT FOUND OR expected_subject_digest <> NEW.subject_digest THEN
        RAISE EXCEPTION 'xiangwan moderation subject snapshot mismatch'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_xw_resource_moderation_snapshot_validate
    ON xiangwan_resource_moderation_observations;
CREATE TRIGGER trg_xw_resource_moderation_snapshot_validate
    BEFORE INSERT ON xiangwan_resource_moderation_observations
    FOR EACH ROW
    EXECUTE FUNCTION xiangwan_validate_moderation_snapshot_digest();

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM xiangwan_resource_moderation_observations AS observation
        LEFT JOIN xiangwan_resource_content_snapshots AS snapshot
          ON snapshot.tenant_id = observation.tenant_id
         AND snapshot.relation_id = observation.relation_id
         AND snapshot.content_id = observation.content_id
         AND snapshot.content_revision_at = observation.content_revision_at
         AND snapshot.subject_digest = observation.subject_digest
        WHERE snapshot.relation_id IS NULL
    ) THEN
        RAISE EXCEPTION
            'existing xiangwan moderation evidence does not match its snapshot'
            USING ERRCODE = '23514';
    END IF;
END;
$$;

CREATE OR REPLACE FUNCTION xiangwan_reject_resource_snapshot_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'xiangwan resource Content snapshots are immutable'
        USING ERRCODE = '23514';
END;
$$;

DROP TRIGGER IF EXISTS trg_xw_resource_snapshots_no_update
    ON xiangwan_resource_content_snapshots;
CREATE TRIGGER trg_xw_resource_snapshots_no_update
    BEFORE UPDATE ON xiangwan_resource_content_snapshots
    FOR EACH ROW EXECUTE FUNCTION xiangwan_reject_resource_snapshot_mutation();

DROP TRIGGER IF EXISTS trg_xw_resource_snapshots_no_delete
    ON xiangwan_resource_content_snapshots;
CREATE TRIGGER trg_xw_resource_snapshots_no_delete
    BEFORE DELETE ON xiangwan_resource_content_snapshots
    FOR EACH ROW EXECUTE FUNCTION xiangwan_reject_resource_snapshot_mutation();

DROP TRIGGER IF EXISTS trg_xw_resource_snapshots_no_truncate
    ON xiangwan_resource_content_snapshots;
CREATE TRIGGER trg_xw_resource_snapshots_no_truncate
    BEFORE TRUNCATE ON xiangwan_resource_content_snapshots
    FOR EACH STATEMENT
    EXECUTE FUNCTION xiangwan_reject_resource_snapshot_mutation();
