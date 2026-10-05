-- 820_xiangwan_review_media_preview_evidence_index.up.sql
-- Publication checks an append-only, successfully opened private preview for
-- the exact File/actor after confirmation. Keep that lookup bounded as the
-- administrator audit ledger grows; failed preview attempts are excluded.

CREATE INDEX IF NOT EXISTS idx_xw_admin_review_media_opened
    ON xiangwan_admin_audit_events (tenant_id, target_id, actor_id, occurred_at DESC)
    WHERE target_type = 'file' AND action_code = 'media.private_preview_opened';
