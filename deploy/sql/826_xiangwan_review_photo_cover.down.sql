ALTER TABLE xiangwan_review_photo_curations
    DROP CONSTRAINT IF EXISTS xw_review_photo_cover_visible_check,
    DROP COLUMN IF EXISTS cover_block_id,
    DROP COLUMN IF EXISTS cover_specified;
