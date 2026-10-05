-- Development/test rollback reference only. Production rollback is roll-forward.

ALTER TABLE IF EXISTS xiangwan_activity_instances
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

ALTER TABLE IF EXISTS xiangwan_activity_instances
    DROP CONSTRAINT IF EXISTS xiangwan_activity_instances_presentation_revision_check;

ALTER TABLE IF EXISTS xiangwan_activity_instances
    DROP COLUMN IF EXISTS presentation_revision;
