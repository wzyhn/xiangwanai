-- Development/test rollback reference only. Production rollback is roll-forward.

ALTER TABLE IF EXISTS xiangwan_activity_instances
    DROP CONSTRAINT IF EXISTS xiangwan_activity_instances_cover_image_url_check,
    DROP COLUMN IF EXISTS cover_image_url;
