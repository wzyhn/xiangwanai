-- 775_xiangwan_series_favorites.up.sql
-- Principal-owned Series favorite target state. PostgreSQL is the only
-- uniqueness boundary; the existing Series favorite_count is maintained in
-- the same serializable transaction as relation creation or removal.

CREATE TABLE IF NOT EXISTS xiangwan_series_favorites (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    principal_id UUID NOT NULL REFERENCES principals(id),
    series_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT xiangwan_series_favorites_tenant_id_id_key
        UNIQUE (tenant_id, id),
    CONSTRAINT xiangwan_series_favorites_owner_series_key
        UNIQUE (tenant_id, principal_id, series_id),
    CONSTRAINT xiangwan_series_favorites_series_fkey
        FOREIGN KEY (tenant_id, series_id)
        REFERENCES xiangwan_activity_series (tenant_id, id)
);

CREATE INDEX IF NOT EXISTS idx_xiangwan_series_favorites_owner_created
    ON xiangwan_series_favorites (
        tenant_id, principal_id, created_at DESC, id DESC
    );

CREATE INDEX IF NOT EXISTS idx_xiangwan_series_favorites_series
    ON xiangwan_series_favorites (tenant_id, series_id);
