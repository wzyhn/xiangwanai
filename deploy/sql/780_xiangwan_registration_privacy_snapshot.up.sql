-- 780_xiangwan_registration_privacy_snapshot.up.sql
-- Preserve the exact public privacy-policy version acknowledged for every new
-- Registration submission. The column remains nullable for historical rows
-- and N-1 binaries. Current application code writes it atomically when the
-- client supplies an acknowledgement and preserves NULL during rollout.

ALTER TABLE xiangwan_registration_snapshots
    ADD COLUMN IF NOT EXISTS privacy_policy_version VARCHAR(64);

ALTER TABLE xiangwan_registration_snapshots
    DROP CONSTRAINT IF EXISTS xw_registration_snapshot_privacy_policy_check;

ALTER TABLE xiangwan_registration_snapshots
    ADD CONSTRAINT xw_registration_snapshot_privacy_policy_check
        CHECK (
            privacy_policy_version IS NULL
            OR privacy_policy_version ~
                '^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$'
        );
