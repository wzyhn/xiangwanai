-- 741_xiangwan_activity_publication.up.sql
-- Persist Session delivery facts and immutable Instance publication receipts.

ALTER TABLE xiangwan_activity_sessions
    ADD COLUMN IF NOT EXISTS delivery_mode VARCHAR(16),
    ADD COLUMN IF NOT EXISTS venue_name VARCHAR(200),
    ADD COLUMN IF NOT EXISTS address TEXT,
    ADD COLUMN IF NOT EXISTS longitude DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS latitude DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS online_participation_mode VARCHAR(80),
    ADD COLUMN IF NOT EXISTS online_participation_compliant BOOLEAN;

ALTER TABLE xiangwan_activity_sessions
    DROP CONSTRAINT IF EXISTS xiangwan_activity_sessions_delivery_mode_check,
    ADD CONSTRAINT xiangwan_activity_sessions_delivery_mode_check
        CHECK (delivery_mode IS NULL OR delivery_mode IN ('offline', 'online')),
    DROP CONSTRAINT IF EXISTS xiangwan_activity_sessions_longitude_check,
    ADD CONSTRAINT xiangwan_activity_sessions_longitude_check
        CHECK (longitude IS NULL OR longitude BETWEEN -180 AND 180),
    DROP CONSTRAINT IF EXISTS xiangwan_activity_sessions_latitude_check,
    ADD CONSTRAINT xiangwan_activity_sessions_latitude_check
        CHECK (latitude IS NULL OR latitude BETWEEN -90 AND 90),
    DROP CONSTRAINT IF EXISTS xiangwan_activity_sessions_delivery_shape_check,
    ADD CONSTRAINT xiangwan_activity_sessions_delivery_shape_check
        CHECK (
            status NOT IN ('published', 'cancelled', 'ended')
            OR (
                (
                    delivery_mode = 'offline'
                    AND BTRIM(COALESCE(venue_name, '')) <> ''
                    AND BTRIM(COALESCE(address, '')) <> ''
                    AND longitude IS NOT NULL
                    AND latitude IS NOT NULL
                )
                OR (
                    delivery_mode = 'online'
                    AND BTRIM(COALESCE(online_participation_mode, '')) <> ''
                    AND online_participation_compliant IS TRUE
                )
            )
        ) NOT VALID;

CREATE TABLE IF NOT EXISTS xiangwan_publication_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL,
    series_id UUID NOT NULL,
    instance_id UUID NOT NULL,
    publication_version BIGINT NOT NULL,
    session_count INTEGER NOT NULL,
    candidate_digest CHAR(64) NOT NULL,
    published_by UUID NOT NULL REFERENCES principals(id),
    published_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT xiangwan_publication_events_version_check
        CHECK (publication_version >= 1),
    CONSTRAINT xiangwan_publication_events_session_count_check
        CHECK (session_count >= 1),
    CONSTRAINT xiangwan_publication_events_candidate_digest_check
        CHECK (candidate_digest ~ '^[0-9a-f]{64}$'),
    CONSTRAINT xiangwan_publication_events_instance_fkey
        FOREIGN KEY (tenant_id, series_id, instance_id)
        REFERENCES xiangwan_activity_instances (tenant_id, series_id, id),
    CONSTRAINT xiangwan_publication_events_instance_version_key
        UNIQUE (tenant_id, instance_id, publication_version)
);

CREATE INDEX IF NOT EXISTS idx_xiangwan_publication_events_series_published
    ON xiangwan_publication_events (tenant_id, series_id, published_at DESC, id);

CREATE OR REPLACE FUNCTION xiangwan_reject_publication_event_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'xiangwan publication events are append-only'
        USING ERRCODE = '0A000';
END;
$$;

DROP TRIGGER IF EXISTS trg_xiangwan_publication_events_append_only
    ON xiangwan_publication_events;
CREATE TRIGGER trg_xiangwan_publication_events_append_only
    BEFORE UPDATE OR DELETE ON xiangwan_publication_events
    FOR EACH ROW EXECUTE FUNCTION xiangwan_reject_publication_event_mutation();

DROP TRIGGER IF EXISTS trg_xiangwan_publication_events_no_truncate
    ON xiangwan_publication_events;
CREATE TRIGGER trg_xiangwan_publication_events_no_truncate
    BEFORE TRUNCATE ON xiangwan_publication_events
    FOR EACH STATEMENT EXECUTE FUNCTION xiangwan_reject_publication_event_mutation();
