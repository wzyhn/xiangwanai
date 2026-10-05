-- A replacement is a new Content -> snapshot -> approval -> publication chain.
-- Old bodies remain immutable; public readers exclude replaced relations.
CREATE TABLE IF NOT EXISTS xiangwan_review_resource_replacements (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    previous_relation_id UUID NOT NULL,
    replacement_relation_id UUID NOT NULL,
    photo_curation_version BIGINT NOT NULL CHECK (photo_curation_version >= 0),
    actor_id UUID NOT NULL REFERENCES principals(id),
    operation_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE (tenant_id, previous_relation_id),
    UNIQUE (tenant_id, replacement_relation_id),
    UNIQUE (tenant_id, actor_id, operation_id),
    CHECK (previous_relation_id <> replacement_relation_id),
    FOREIGN KEY (tenant_id, previous_relation_id)
        REFERENCES xiangwan_resource_relations (tenant_id, id),
    FOREIGN KEY (tenant_id, replacement_relation_id)
        REFERENCES xiangwan_resource_relations (tenant_id, id)
);

CREATE OR REPLACE FUNCTION xiangwan_guard_review_resource_replacement()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE
    previous xiangwan_resource_relations%ROWTYPE;
    replacement xiangwan_resource_relations%ROWTYPE;
BEGIN
    IF TG_OP <> 'INSERT' THEN
        RAISE EXCEPTION 'review resource replacements are append-only' USING ERRCODE = '23514';
    END IF;
    SELECT * INTO STRICT previous FROM xiangwan_resource_relations
      WHERE tenant_id = NEW.tenant_id AND id = NEW.previous_relation_id FOR UPDATE;
    SELECT * INTO STRICT replacement FROM xiangwan_resource_relations
      WHERE tenant_id = NEW.tenant_id AND id = NEW.replacement_relation_id FOR SHARE;
    IF previous.series_id <> replacement.series_id
       OR previous.instance_id <> replacement.instance_id
       OR previous.session_id IS DISTINCT FROM replacement.session_id
       OR previous.relation_kind <> replacement.relation_kind
       OR previous.access_policy <> 'public' OR replacement.access_policy <> 'public'
       OR previous.sort_order <> replacement.sort_order
       OR replacement.created_by <> NEW.actor_id
       OR replacement.idempotency_key <> NEW.operation_id::TEXT
       OR EXISTS (SELECT 1 FROM xiangwan_review_resource_replacements
                  WHERE tenant_id = NEW.tenant_id AND previous_relation_id = NEW.replacement_relation_id)
       OR NOT EXISTS (SELECT 1 FROM xiangwan_resource_publications
                      WHERE tenant_id = NEW.tenant_id AND relation_id = NEW.previous_relation_id)
       OR NOT EXISTS (SELECT 1 FROM xiangwan_resource_publications
                      WHERE tenant_id = NEW.tenant_id AND relation_id = NEW.replacement_relation_id)
       OR NEW.photo_curation_version <> COALESCE((SELECT MAX(version)
           FROM xiangwan_review_photo_curations WHERE tenant_id = NEW.tenant_id
             AND relation_id = NEW.previous_relation_id), 0) THEN
        RAISE EXCEPTION 'review resource replacement facts conflict' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS xw_review_resource_replacement_guard ON xiangwan_review_resource_replacements;
CREATE TRIGGER xw_review_resource_replacement_guard
BEFORE INSERT OR UPDATE OR DELETE ON xiangwan_review_resource_replacements
FOR EACH ROW EXECUTE FUNCTION xiangwan_guard_review_resource_replacement();
