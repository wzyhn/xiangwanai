-- A cancelled schedule is a recoverable operator fact, distinct from a
-- worker failure. The task row remains immutable in identity and auditable.
ALTER TABLE xiangwan_scheduled_publications
    DROP CONSTRAINT IF EXISTS xiangwan_scheduled_publications_status_check;

ALTER TABLE xiangwan_scheduled_publications
    ADD CONSTRAINT xiangwan_scheduled_publications_status_check
    CHECK (status IN ('pending', 'processing', 'completed', 'failed', 'cancelled'));
