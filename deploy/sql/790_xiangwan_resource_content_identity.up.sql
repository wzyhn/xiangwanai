-- 790_xiangwan_resource_content_identity.up.sql
--
-- Xiangwan resource relations are the product-owned publication boundary for
-- shared Content.  Older valid relations predate the product-code convention
-- and may therefore point at Content whose metadata has no product_code.  Do
-- not rewrite those immutable rows.  A non-empty foreign product_code is a
-- contradictory identity, however, and must fail closed both for existing
-- data and for new bindings.
--
-- The public readers intentionally rely on the tenant + relation/publication /
-- snapshot chain below, rather than on mutable Content metadata.  The relation
-- table is already Xiangwan-owned; this trigger only rejects an explicit
-- cross-product classification while preserving legacy rows with no key.

-- The runner applies each ordinary migration atomically. Take the same table
-- lock CREATE TRIGGER needs before inspecting existing bindings: every earlier
-- insert must commit first, and every later insert sees the new identity guard.
-- Content bound before this lock is already immutable under migration 765.
LOCK TABLE xiangwan_resource_relations IN SHARE ROW EXCLUSIVE MODE;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM xiangwan_resource_relations AS relation
        JOIN contents AS content
          ON content.id = relation.content_id
         AND content.tenant_id = relation.tenant_id
        WHERE NULLIF(BTRIM(content.metadata->>'product_code'), '') IS NOT NULL
          AND NULLIF(BTRIM(content.metadata->>'product_code'), '')
                <> 'wq-xiangwan'
    ) THEN
        RAISE EXCEPTION
            'existing xiangwan ResourceRelation has a contradictory Content product_code; remediate before applying migration 790'
            USING ERRCODE = '23514';
    END IF;
END;
$$;

CREATE OR REPLACE FUNCTION xiangwan_validate_resource_content_product()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    content_product_code TEXT;
BEGIN
    -- Match migration 765's Content/Block mutation and snapshot lock protocol.
    -- Hold it from the first identity read through the relation and snapshot
    -- commit so a metadata-only update cannot pass between validation/capture.
    PERFORM pg_advisory_xact_lock(
        hashtextextended(NEW.content_id::text, 725)
    );

    SELECT NULLIF(BTRIM(content.metadata->>'product_code'), '')
    INTO content_product_code
    FROM contents AS content
    WHERE content.id = NEW.content_id
      AND content.tenant_id = NEW.tenant_id
      AND content.deleted_at IS NULL
    FOR SHARE;

    -- The existing relation trigger owns missing-content and tenant checks.
    -- This trigger only adds the cross-product contradiction check.
    IF content_product_code IS NOT NULL
       AND content_product_code <> 'wq-xiangwan' THEN
        RAISE EXCEPTION
            'xiangwan ResourceRelation Content product_code mismatch'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_xw_resource_relation_content_product
    ON xiangwan_resource_relations;
-- PostgreSQL runs same-event triggers by name. "relation_content" sorts before
-- "relations_validate", so identity is locked before the 762 candidate checks;
-- the 765 AFTER snapshot trigger reuses the transaction's advisory lock.
CREATE TRIGGER trg_xw_resource_relation_content_product
    BEFORE INSERT ON xiangwan_resource_relations
    FOR EACH ROW EXECUTE FUNCTION xiangwan_validate_resource_content_product();
