-- Existing cancelled rows must be resolved before rolling back this enum
-- expansion; no data is deleted by the rollback.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM xiangwan_scheduled_publications WHERE status = 'cancelled'
    ) THEN
        RAISE EXCEPTION 'cannot roll back cancelled schedule status while rows exist'
            USING ERRCODE = '55006';
    END IF;
END;
$$;

ALTER TABLE xiangwan_scheduled_publications
    DROP CONSTRAINT IF EXISTS xiangwan_scheduled_publications_status_check;

ALTER TABLE xiangwan_scheduled_publications
    ADD CONSTRAINT xiangwan_scheduled_publications_status_check
    CHECK (status IN ('pending', 'processing', 'completed', 'failed'));
