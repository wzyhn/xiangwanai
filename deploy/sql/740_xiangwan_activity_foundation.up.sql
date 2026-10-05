-- 740_xiangwan_activity_foundation.up.sql
-- Xiangwan Activity Series / Instance / Session hierarchy.
-- New empty tables only: no backfill and no external call.

CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS xiangwan_activity_series (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    title VARCHAR(200) NOT NULL DEFAULT '',
    status VARCHAR(24) NOT NULL DEFAULT 'draft',
    is_recurring BOOLEAN NOT NULL DEFAULT FALSE,
    successful_published_instance_count INTEGER NOT NULL DEFAULT 0,
    current_public_instance_id UUID,
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT xiangwan_activity_series_status_check
        CHECK (status IN ('draft', 'active', 'archived')),
    CONSTRAINT xiangwan_activity_series_published_count_check
        CHECK (successful_published_instance_count >= 0),
    CONSTRAINT xiangwan_activity_series_recurring_count_check
        CHECK (successful_published_instance_count < 2 OR is_recurring),
    CONSTRAINT xiangwan_activity_series_version_check
        CHECK (version >= 1),
    CONSTRAINT xiangwan_activity_series_tenant_id_id_key
        UNIQUE (tenant_id, id)
);

CREATE TABLE IF NOT EXISTS xiangwan_activity_instances (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,
    series_id UUID NOT NULL,
    title VARCHAR(200) NOT NULL DEFAULT '',
    status VARCHAR(24) NOT NULL DEFAULT 'draft',
    publication_version BIGINT NOT NULL DEFAULT 0,
    scheduled_at TIMESTAMPTZ,
    published_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT xiangwan_activity_instances_status_check
        CHECK (status IN ('draft', 'pending_publish', 'published', 'completed', 'cancelled', 'archived')),
    CONSTRAINT xiangwan_activity_instances_publication_version_check
        CHECK (publication_version >= 0),
    CONSTRAINT xiangwan_activity_instances_version_check
        CHECK (version >= 1),
    CONSTRAINT xiangwan_activity_instances_published_shape_check
        CHECK (
            status NOT IN ('published', 'completed')
            OR (publication_version >= 1 AND published_at IS NOT NULL)
        ),
    CONSTRAINT xiangwan_activity_instances_completed_shape_check
        CHECK (status <> 'completed' OR completed_at IS NOT NULL),
    CONSTRAINT xiangwan_activity_instances_tenant_series_fkey
        FOREIGN KEY (tenant_id, series_id)
        REFERENCES xiangwan_activity_series (tenant_id, id),
    CONSTRAINT xiangwan_activity_instances_tenant_id_id_key
        UNIQUE (tenant_id, id),
    CONSTRAINT xiangwan_activity_instances_tenant_series_id_id_key
        UNIQUE (tenant_id, series_id, id)
);

DO $$
DECLARE
    series_oid OID := 'xiangwan_activity_series'::regclass;
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'xiangwan_activity_series_current_public_instance_fkey'
          AND conrelid = series_oid
    ) THEN
        ALTER TABLE xiangwan_activity_series
            ADD CONSTRAINT xiangwan_activity_series_current_public_instance_fkey
            FOREIGN KEY (tenant_id, id, current_public_instance_id)
            REFERENCES xiangwan_activity_instances (tenant_id, series_id, id);
    END IF;
END $$;

CREATE TABLE IF NOT EXISTS xiangwan_activity_sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,
    instance_id UUID NOT NULL,
    title VARCHAR(200) NOT NULL DEFAULT '',
    status VARCHAR(24) NOT NULL DEFAULT 'draft',
    registration_start_at TIMESTAMPTZ,
    registration_end_at TIMESTAMPTZ,
    session_start_at TIMESTAMPTZ,
    session_end_at TIMESTAMPTZ,
    capacity INTEGER,
    group_minimum INTEGER,
    low_stock_threshold INTEGER,
    price_cents BIGINT,
    sort_order INTEGER NOT NULL DEFAULT 0,
    published_at TIMESTAMPTZ,
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT xiangwan_activity_sessions_status_check
        CHECK (status IN ('draft', 'published', 'cancelled', 'ended', 'archived')),
    CONSTRAINT xiangwan_activity_sessions_registration_window_check
        CHECK (
            registration_start_at IS NULL
            OR registration_end_at IS NULL
            OR registration_start_at < registration_end_at
        ),
    CONSTRAINT xiangwan_activity_sessions_registration_before_session_check
        CHECK (
            registration_end_at IS NULL
            OR session_start_at IS NULL
            OR registration_end_at <= session_start_at
        ),
    CONSTRAINT xiangwan_activity_sessions_session_window_check
        CHECK (
            session_start_at IS NULL
            OR session_end_at IS NULL
            OR session_start_at < session_end_at
        ),
    CONSTRAINT xiangwan_activity_sessions_capacity_check
        CHECK (capacity IS NULL OR capacity > 0),
    CONSTRAINT xiangwan_activity_sessions_group_minimum_check
        CHECK (
            group_minimum IS NULL
            OR (group_minimum > 0 AND (capacity IS NULL OR group_minimum <= capacity))
        ),
    CONSTRAINT xiangwan_activity_sessions_low_stock_check
        CHECK (
            low_stock_threshold IS NULL
            OR (
                low_stock_threshold > 0
                AND (capacity IS NULL OR low_stock_threshold < capacity)
            )
        ),
    CONSTRAINT xiangwan_activity_sessions_price_check
        CHECK (price_cents IS NULL OR price_cents >= 0),
    CONSTRAINT xiangwan_activity_sessions_version_check
        CHECK (version >= 1),
    CONSTRAINT xiangwan_activity_sessions_published_shape_check
        CHECK (
            status NOT IN ('published', 'cancelled', 'ended')
            OR (
                BTRIM(title) <> ''
                AND registration_start_at IS NOT NULL
                AND registration_end_at IS NOT NULL
                AND session_start_at IS NOT NULL
                AND session_end_at IS NOT NULL
                AND capacity IS NOT NULL
                AND group_minimum IS NOT NULL
                AND price_cents IS NOT NULL
                AND published_at IS NOT NULL
            )
        ),
    CONSTRAINT xiangwan_activity_sessions_tenant_instance_fkey
        FOREIGN KEY (tenant_id, instance_id)
        REFERENCES xiangwan_activity_instances (tenant_id, id),
    CONSTRAINT xiangwan_activity_sessions_tenant_id_id_key
        UNIQUE (tenant_id, id)
);

CREATE INDEX IF NOT EXISTS idx_xiangwan_activity_series_tenant_status
    ON xiangwan_activity_series (tenant_id, status, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_xiangwan_activity_instances_series_status
    ON xiangwan_activity_instances (tenant_id, series_id, status, created_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS uq_xiangwan_activity_instances_publication_version
    ON xiangwan_activity_instances (tenant_id, series_id, publication_version)
    WHERE publication_version > 0;
CREATE INDEX IF NOT EXISTS idx_xiangwan_activity_sessions_instance_order
    ON xiangwan_activity_sessions (tenant_id, instance_id, sort_order, session_start_at, id);
CREATE INDEX IF NOT EXISTS idx_xiangwan_activity_sessions_public_start
    ON xiangwan_activity_sessions (tenant_id, session_start_at, id)
    WHERE status = 'published';

CREATE OR REPLACE FUNCTION xiangwan_guard_series_recurrence()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF OLD.is_recurring AND NOT NEW.is_recurring THEN
        RAISE EXCEPTION 'xiangwan Series recurrence is irreversible'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'xiangwan_activity_series_recurrence_irreversible';
    END IF;
    IF NEW.successful_published_instance_count < OLD.successful_published_instance_count THEN
        RAISE EXCEPTION 'xiangwan successful publication count cannot decrease'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'xiangwan_activity_series_published_count_monotonic';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_xiangwan_guard_series_recurrence ON xiangwan_activity_series;
CREATE TRIGGER trg_xiangwan_guard_series_recurrence
BEFORE UPDATE OF is_recurring, successful_published_instance_count
ON xiangwan_activity_series
FOR EACH ROW
EXECUTE FUNCTION xiangwan_guard_series_recurrence();
