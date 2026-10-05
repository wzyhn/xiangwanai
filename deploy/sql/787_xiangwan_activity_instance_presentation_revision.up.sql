-- 787_xiangwan_activity_instance_presentation_revision.up.sql
-- Give every activity Instance a dedicated presentation revision so the
-- administrator presentational PATCH (cover_image_url / detail_blocks) no
-- longer has to bump the operational `version` that public instance_review
-- resources pin as expected_target_version: a cover touch on a completed
-- Instance used to invalidate its published review documents. PATCH now
-- fences on presentation_revision (request key expected_presentation_revision)
-- and advances it only when cover_image_url or detail_blocks actually change;
-- create/publish/complete keep their existing version semantics untouched.
-- The migration also relaxes the 785 cover_image_url CHECK the way the Go
-- write path already accepts: '' or an absolute https URL or the public
-- relative cover path /api/v1/xiangwan/covers/<32hex>.<ext> the upload
-- endpoint returns (dev http origins store the relative form as-is).
-- New column with a constant default: instant on PG 11+, no backfill and no
-- external call.

ALTER TABLE xiangwan_activity_instances
    ADD COLUMN IF NOT EXISTS presentation_revision INTEGER NOT NULL DEFAULT 1;

ALTER TABLE xiangwan_activity_instances
    DROP CONSTRAINT IF EXISTS xiangwan_activity_instances_presentation_revision_check,
    ADD CONSTRAINT xiangwan_activity_instances_presentation_revision_check
        CHECK (presentation_revision >= 1);

ALTER TABLE xiangwan_activity_instances
    DROP CONSTRAINT IF EXISTS xiangwan_activity_instances_cover_image_url_check,
    ADD CONSTRAINT xiangwan_activity_instances_cover_image_url_check
        CHECK (
            cover_image_url = ''
            OR cover_image_url ~ '^/api/v1/xiangwan/covers/[0-9a-f]{32}\.(jpg|png|webp)$'
            OR (
                CHAR_LENGTH(cover_image_url) BETWEEN 9 AND 2048
                AND cover_image_url ~ '^https://[^[:space:]]+$'
                AND POSITION('#' IN cover_image_url) = 0
            )
        );
