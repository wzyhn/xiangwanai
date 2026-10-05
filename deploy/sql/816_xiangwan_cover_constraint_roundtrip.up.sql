-- Normalize the cover constraint after PostgreSQL schema restore.
-- 787 used an explicit parenthesized AND branch. PostgreSQL accepts the same
-- predicate after restore but deparses that branch differently, which makes
-- an otherwise identical scratch restore fail the schema fingerprint gate.
-- Keep the meaning unchanged and use the canonical precedence form instead.

ALTER TABLE xiangwan_activity_instances
    DROP CONSTRAINT IF EXISTS xiangwan_activity_instances_cover_image_url_check,
    ADD CONSTRAINT xiangwan_activity_instances_cover_image_url_check
        CHECK (
            cover_image_url = ''
            OR cover_image_url ~ '^/api/v1/xiangwan/covers/[0-9a-f]{32}\.(jpg|png|webp)$'
            OR CHAR_LENGTH(cover_image_url) >= 9
               AND CHAR_LENGTH(cover_image_url) <= 2048
               AND cover_image_url ~ '^https://[^[:space:]]+$'
               AND POSITION('#' IN cover_image_url) = 0
        );
