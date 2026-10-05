-- 788_xiangwan_cover_gc_markers.up.sql
-- Two-phase orphan GC for the activity cover objects stored under the local
-- media root. The upload-time GC used to delete any cover file older than
-- 24h with no Instance reference; a cover that had been referenced for a
-- long time and was just replaced lost its reference at the same age, so the
-- old rule deleted it immediately while clients could still hold the
-- immutable one-year-cached URL. The GC now records the first
-- reference-loss observation in xiangwan_cover_gc_markers and only deletes
-- after the retention window (orphanCoverRetention in the api package, 7
-- days) has passed; a file that becomes referenced again clears its marker.
-- Filename is the server-generated cover name (<32hex>.<ext>), unique per
-- stored object, so it is the primary key.
-- New table: instant on PG 11+, no backfill and no external call.

CREATE TABLE IF NOT EXISTS xiangwan_cover_gc_markers (
    filename TEXT PRIMARY KEY,
    orphaned_at TIMESTAMPTZ NOT NULL
);
