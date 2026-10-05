-- 789_xiangwan_media_gc_state.up.sql
-- Durable tombstones keep a collector from deleting an immutable media object
-- and then allowing a concurrent presentation write to attach the missing URL.
-- Cover markers retain their original orphaned_at for the retention decision;
-- deleted_at distinguishes a collected object from one still available for
-- reattachment. Brand hero uploads use the same two-phase contract in their
-- own table because they are referenced by BrandProfile publications.

ALTER TABLE xiangwan_cover_gc_markers
    ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;

CREATE TABLE IF NOT EXISTS xiangwan_brand_hero_gc_markers (
    filename TEXT PRIMARY KEY,
    orphaned_at TIMESTAMPTZ NOT NULL,
    deleted_at TIMESTAMPTZ
);
