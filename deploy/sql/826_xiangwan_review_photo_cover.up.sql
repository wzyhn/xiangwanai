-- Independently select a cover without changing approved Content or photo order.
-- Existing append-only revisions retain NULL (automatic first visible photo).
ALTER TABLE xiangwan_review_photo_curations
    ADD COLUMN IF NOT EXISTS cover_block_id UUID,
    ADD COLUMN IF NOT EXISTS cover_specified BOOLEAN NOT NULL DEFAULT FALSE;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'xiangwan_review_photo_curations'::regclass
          AND conname = 'xw_review_photo_cover_visible_check'
    ) THEN
        ALTER TABLE xiangwan_review_photo_curations
            ADD CONSTRAINT xw_review_photo_cover_visible_check
            CHECK (cover_block_id IS NULL OR ordered_block_ids ? cover_block_id::TEXT);
    END IF;
END $$;
