-- 785_xiangwan_activity_cover.up.sql
-- Add the optional public cover image to one activity Instance. The stored
-- value is either '' (card renders without a cover) or an absolute https URL;
-- the admin web composes the absolute URL from the upload response and the
-- site origin before submitting, mirroring the 783 brand hero contract. The
-- Go write path (admin Instance create) enforces the same shape, and public
-- reads emit the stored value unchanged.
-- New column with a constant default: instant on PG 11+, no backfill and no
-- external call.

ALTER TABLE xiangwan_activity_instances
    ADD COLUMN IF NOT EXISTS cover_image_url TEXT NOT NULL DEFAULT '';

ALTER TABLE xiangwan_activity_instances
    DROP CONSTRAINT IF EXISTS xiangwan_activity_instances_cover_image_url_check,
    ADD CONSTRAINT xiangwan_activity_instances_cover_image_url_check
        CHECK (
            cover_image_url = ''
            OR (
                CHAR_LENGTH(cover_image_url) BETWEEN 9 AND 2048
                AND cover_image_url ~ '^https://[^[:space:]]+$'
                AND POSITION('#' IN cover_image_url) = 0
            )
        );
