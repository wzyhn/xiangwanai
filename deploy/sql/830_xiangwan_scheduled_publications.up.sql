-- 830_xiangwan_scheduled_publications.up.sql
-- Durable, tenant-scoped publication intent. The worker only claims these
-- rows; the existing publication transaction remains the sole publisher.

CREATE TABLE IF NOT EXISTS xiangwan_scheduled_publications (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    instance_id UUID NOT NULL,
    actor_id UUID NOT NULL REFERENCES principals(id),
    identity_link_id UUID NOT NULL,
    expected_instance_version BIGINT NOT NULL,
    scheduled_at TIMESTAMPTZ NOT NULL,
    next_attempt_at TIMESTAMPTZ NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'pending',
    attempt_count INTEGER NOT NULL DEFAULT 0,
    lease_token UUID,
    lease_expires_at TIMESTAMPTZ,
    last_error TEXT NOT NULL DEFAULT '',
    completed_at TIMESTAMPTZ,
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT xiangwan_scheduled_publications_instance_fkey
        FOREIGN KEY (tenant_id, instance_id)
        REFERENCES xiangwan_activity_instances (tenant_id, id),
    CONSTRAINT xiangwan_scheduled_publications_status_check
        CHECK (status IN ('pending', 'processing', 'completed', 'failed')),
    CONSTRAINT xiangwan_scheduled_publications_attempt_check
        CHECK (attempt_count >= 0),
    CONSTRAINT xiangwan_scheduled_publications_version_check
        CHECK (version >= 1),
    CONSTRAINT xiangwan_scheduled_publications_lease_shape_check
        CHECK (
            (status = 'processing' AND lease_token IS NOT NULL AND lease_expires_at IS NOT NULL)
            OR (status <> 'processing')
        ),
    CONSTRAINT xiangwan_scheduled_publications_completed_shape_check
        CHECK (status <> 'completed' OR completed_at IS NOT NULL)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_xiangwan_scheduled_publications_active
    ON xiangwan_scheduled_publications (tenant_id, instance_id)
    WHERE status IN ('pending', 'processing');

CREATE INDEX IF NOT EXISTS idx_xiangwan_scheduled_publications_due
    ON xiangwan_scheduled_publications (tenant_id, next_attempt_at, id)
    WHERE status = 'pending';

CREATE INDEX IF NOT EXISTS idx_xiangwan_scheduled_publications_leases
    ON xiangwan_scheduled_publications (tenant_id, lease_expires_at, id)
    WHERE status = 'processing';

CREATE OR REPLACE FUNCTION xiangwan_reject_scheduled_publication_mutation()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'xiangwan scheduled publication rows are recoverable facts; use a terminal status'
            USING ERRCODE = '0A000';
    END IF;
    IF NEW.tenant_id <> OLD.tenant_id OR NEW.instance_id <> OLD.instance_id
       OR NEW.actor_id <> OLD.actor_id OR NEW.identity_link_id <> OLD.identity_link_id
       OR NEW.expected_instance_version <> OLD.expected_instance_version
       OR NEW.scheduled_at <> OLD.scheduled_at OR NEW.created_at <> OLD.created_at THEN
        RAISE EXCEPTION 'xiangwan scheduled publication identity is immutable'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_xiangwan_scheduled_publication_guard ON xiangwan_scheduled_publications;
CREATE TRIGGER trg_xiangwan_scheduled_publication_guard
BEFORE UPDATE OR DELETE ON xiangwan_scheduled_publications
FOR EACH ROW EXECUTE FUNCTION xiangwan_reject_scheduled_publication_mutation();

CREATE OR REPLACE FUNCTION xiangwan_reject_scheduled_publication_truncate()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'xiangwan scheduled publication rows are recoverable facts; TRUNCATE is forbidden'
        USING ERRCODE = '0A000';
END;
$$;

DROP TRIGGER IF EXISTS trg_xiangwan_scheduled_publication_no_truncate ON xiangwan_scheduled_publications;
CREATE TRIGGER trg_xiangwan_scheduled_publication_no_truncate
BEFORE TRUNCATE ON xiangwan_scheduled_publications
FOR EACH STATEMENT EXECUTE FUNCTION xiangwan_reject_scheduled_publication_truncate();
