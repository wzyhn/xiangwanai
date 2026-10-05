-- 786_xiangwan_activity_instance_detail_blocks.up.sql
-- Add the optional public detail content blocks to one activity Instance. The
-- stored value is a JSONB array whose elements are either
-- {"type":"text","title","body"} or {"type":"image","url","caption"}; the Go
-- write path (admin Instance create/update) validates every element, and the
-- public Session detail read projects only valid elements. Element shape stays
-- application-enforced — mirroring the brand quick_tags JSONB precedent, the
-- database guards only the top-level array type.
-- New column with a constant default: instant on PG 11+, no backfill and no
-- external call.

ALTER TABLE xiangwan_activity_instances
    ADD COLUMN IF NOT EXISTS detail_blocks JSONB NOT NULL DEFAULT '[]'::JSONB;

ALTER TABLE xiangwan_activity_instances
    DROP CONSTRAINT IF EXISTS xiangwan_activity_instances_detail_blocks_check,
    ADD CONSTRAINT xiangwan_activity_instances_detail_blocks_check
        CHECK (jsonb_typeof(detail_blocks) = 'array');
