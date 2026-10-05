-- Standalone distribution prerequisite for the exact shared capabilities used by Xiangwan.
-- Append-only adaptation; existing Xiangwan migration files retain original identities.
ALTER TABLE principals ADD COLUMN IF NOT EXISTS last_active_at TIMESTAMPTZ;
ALTER TABLE contents ADD COLUMN IF NOT EXISTS tenant_id UUID REFERENCES tenants(id);
ALTER TABLE contents ALTER COLUMN tenant_id SET NOT NULL;
CREATE INDEX IF NOT EXISTS idx_contents_tenant_created ON contents(tenant_id, created_at DESC);
ALTER TABLE files
  ADD COLUMN IF NOT EXISTS metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  ADD COLUMN IF NOT EXISTS delete_after TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS deleting_at TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS expired_at TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS retry_count INTEGER NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS last_error TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_files_pending_expiration ON files(status, delete_after)
  WHERE delete_after IS NOT NULL AND status NOT IN ('deleting', 'expired', 'cleanup_failed');
CREATE INDEX IF NOT EXISTS idx_files_deleting_stale ON files(deleting_at) WHERE status='deleting';
CREATE TABLE IF NOT EXISTS content_governance (
  content_id UUID PRIMARY KEY REFERENCES contents(id) ON DELETE CASCADE,
  policy_key VARCHAR(64) NOT NULL CHECK (btrim(policy_key) <> ''),
  mutation_owner VARCHAR(64) NOT NULL CHECK (btrim(mutation_owner) <> ''),
  generic_read_policy VARCHAR(16) NOT NULL CHECK (generic_read_policy IN ('deny','owner_read')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
