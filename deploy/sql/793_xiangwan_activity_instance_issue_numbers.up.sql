-- 793_xiangwan_activity_instance_issue_numbers.up.sql
--
-- Give every Activity Instance an immutable, Series-scoped period number.
-- Existing rows are numbered by creation order; new rows are allocated by the
-- administrator Catalog while holding the Series row lock. The trigger keeps
-- older binaries and direct import fixtures safe during a rolling upgrade.

ALTER TABLE xiangwan_activity_instances
    ADD COLUMN IF NOT EXISTS issue_no INTEGER;

CREATE OR REPLACE FUNCTION xiangwan_assign_instance_issue_no()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.issue_no IS NULL THEN
        -- The parent Series is already protected by the FK. Taking its row
        -- lock serializes MAX(issue_no)+1 for legacy callers that do not yet
        -- send the new field; the Catalog takes the same lock explicitly.
        PERFORM 1
        FROM xiangwan_activity_series
        WHERE tenant_id = NEW.tenant_id
          AND id = NEW.series_id
        FOR UPDATE;

        SELECT COALESCE(MAX(issue_no), 0) + 1
          INTO NEW.issue_no
        FROM xiangwan_activity_instances
        WHERE tenant_id = NEW.tenant_id
          AND series_id = NEW.series_id;
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_xiangwan_assign_instance_issue_no
    ON xiangwan_activity_instances;
CREATE TRIGGER trg_xiangwan_assign_instance_issue_no
BEFORE INSERT ON xiangwan_activity_instances
FOR EACH ROW
EXECUTE FUNCTION xiangwan_assign_instance_issue_no();

WITH numbered AS (
    SELECT
        id,
        ROW_NUMBER() OVER (
            PARTITION BY tenant_id, series_id
            ORDER BY created_at ASC, id ASC
        )::INTEGER AS issue_no
    FROM xiangwan_activity_instances
    WHERE issue_no IS NULL
)
UPDATE xiangwan_activity_instances AS instance
SET issue_no = numbered.issue_no
FROM numbered
WHERE instance.id = numbered.id;

-- Existing Instances enqueue deferred FK checks during the backfill. Flush
-- those checks before ALTER TABLE; PostgreSQL otherwise rejects this DDL with
-- pending trigger events. Keep both the backfill and constraints atomic.
SET CONSTRAINTS ALL IMMEDIATE;

ALTER TABLE xiangwan_activity_instances
    ALTER COLUMN issue_no SET NOT NULL,
    DROP CONSTRAINT IF EXISTS xiangwan_activity_instances_issue_no_check,
    ADD CONSTRAINT xiangwan_activity_instances_issue_no_check
        CHECK (issue_no > 0);

CREATE UNIQUE INDEX IF NOT EXISTS uq_xiangwan_activity_instances_issue_no
    ON xiangwan_activity_instances (tenant_id, series_id, issue_no);

-- The public title follows the prototype contract: “第X期系列名”. Existing
-- titles were presentation-only and had no separate issue number, so the
-- deterministic backfill makes historical detail pages consistent too.
UPDATE xiangwan_activity_instances AS instance
SET title = CONCAT('第', instance.issue_no, '期', series.title)
FROM xiangwan_activity_series AS series
WHERE series.tenant_id = instance.tenant_id
  AND series.id = instance.series_id
  AND CHAR_LENGTH(CONCAT('第', instance.issue_no, '期', series.title)) <= 200
  AND instance.title <> CONCAT('第', instance.issue_no, '期', series.title);
