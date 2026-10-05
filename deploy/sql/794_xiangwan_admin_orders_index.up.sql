-- 794_xiangwan_admin_orders_index.up.sql
-- Tenant-wide operator order reads use descending keyset pagination.
-- The existing principal- and session-leading indexes cannot serve that order.
CREATE INDEX IF NOT EXISTS idx_xiangwan_orders_tenant_created
    ON xiangwan_orders (tenant_id, created_at DESC, id DESC);
