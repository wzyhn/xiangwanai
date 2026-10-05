-- 798_xiangwan_coupon_correction_admin_index.up.sql
-- Bound the tenant-scoped, snapshot-paginated administrator correction read.

CREATE INDEX IF NOT EXISTS idx_xw_coupon_entries_admin_corrections
    ON xiangwan_coupon_entries (tenant_id, recorded_at DESC, id DESC)
    WHERE entry_type = 'correction_required';
