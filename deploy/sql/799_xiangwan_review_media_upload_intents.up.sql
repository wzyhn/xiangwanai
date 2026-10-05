-- 799_xiangwan_review_media_upload_intents.up.sql
-- Keep an exact target/owner/checksum intent next to Storage's pending File.
-- Only confirmed_at may transition, once, after provider byte inspection.

CREATE TABLE IF NOT EXISTS xiangwan_review_media_uploads (
    file_id UUID PRIMARY KEY REFERENCES files(id),
    tenant_id UUID NOT NULL,
    actor_id UUID NOT NULL,
    identity_link_id UUID NOT NULL,
    instance_id UUID NOT NULL,
    session_id UUID,
    media_kind VARCHAR(16) NOT NULL,
    mime VARCHAR(100) NOT NULL,
    expected_size BIGINT NOT NULL,
    expected_sha256 CHAR(64) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    confirmed_at TIMESTAMPTZ,
    CONSTRAINT xw_review_media_kind_check
        CHECK (media_kind IN ('photo', 'video', 'audio', 'material')),
    CONSTRAINT xw_review_media_size_check
        CHECK (expected_size > 0 AND expected_size <= 209715200),
    CONSTRAINT xw_review_media_sha_check
        CHECK (expected_sha256 ~ '^[0-9a-f]{64}$'),
    CONSTRAINT xw_review_media_time_check
        CHECK (expires_at > created_at AND
               (confirmed_at IS NULL OR confirmed_at >= created_at)),
    CONSTRAINT xw_review_media_tenant_file_key UNIQUE (tenant_id, file_id),
    CONSTRAINT xw_review_media_instance_fkey
        FOREIGN KEY (tenant_id, instance_id)
        REFERENCES xiangwan_activity_instances (tenant_id, id),
    CONSTRAINT xw_review_media_session_fkey
        FOREIGN KEY (tenant_id, instance_id, session_id)
        REFERENCES xiangwan_activity_sessions (tenant_id, instance_id, id),
    CONSTRAINT xw_review_media_actor_fkey
        FOREIGN KEY (actor_id) REFERENCES principals (id),
    CONSTRAINT xw_review_media_identity_fkey
        FOREIGN KEY (tenant_id, identity_link_id)
        REFERENCES xiangwan_admin_identity_links (tenant_id, id)
);

CREATE INDEX IF NOT EXISTS idx_xw_review_media_target
    ON xiangwan_review_media_uploads (tenant_id, instance_id, session_id, created_at DESC);

CREATE OR REPLACE FUNCTION xiangwan_guard_review_media_confirmation()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.file_id IS DISTINCT FROM NEW.file_id OR
       OLD.tenant_id IS DISTINCT FROM NEW.tenant_id OR
       OLD.actor_id IS DISTINCT FROM NEW.actor_id OR
       OLD.identity_link_id IS DISTINCT FROM NEW.identity_link_id OR
       OLD.instance_id IS DISTINCT FROM NEW.instance_id OR
       OLD.session_id IS DISTINCT FROM NEW.session_id OR
       OLD.media_kind IS DISTINCT FROM NEW.media_kind OR
       OLD.mime IS DISTINCT FROM NEW.mime OR
       OLD.expected_size IS DISTINCT FROM NEW.expected_size OR
       OLD.expected_sha256 IS DISTINCT FROM NEW.expected_sha256 OR
       OLD.created_at IS DISTINCT FROM NEW.created_at OR
       OLD.expires_at IS DISTINCT FROM NEW.expires_at OR
       OLD.confirmed_at IS NOT NULL OR NEW.confirmed_at IS NULL THEN
        RAISE EXCEPTION 'review media intent facts are immutable'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'xw_review_media_confirmation_only';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_xw_review_media_confirmation_only
    ON xiangwan_review_media_uploads;
CREATE TRIGGER trg_xw_review_media_confirmation_only
    BEFORE UPDATE ON xiangwan_review_media_uploads
    FOR EACH ROW EXECUTE FUNCTION xiangwan_guard_review_media_confirmation();
