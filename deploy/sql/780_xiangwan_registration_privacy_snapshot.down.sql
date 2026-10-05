-- Development/test rollback reference only. Production rollback is roll-forward.

ALTER TABLE xiangwan_registration_snapshots
    DROP CONSTRAINT IF EXISTS xw_registration_snapshot_privacy_policy_check;

ALTER TABLE xiangwan_registration_snapshots
    DROP COLUMN IF EXISTS privacy_policy_version;
