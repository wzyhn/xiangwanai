-- 819_xiangwan_coupon_grant_policy_versions.up.sql
-- Append-only, customer-evidenced policy snapshots for Coupon issuance. No
-- policy is seeded: absent signed customer values continue to fail closed.

CREATE TABLE IF NOT EXISTS xiangwan_coupon_grant_policy_versions (
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    policy_version VARCHAR(128) NOT NULL,
    enabled BOOLEAN NOT NULL,
    face_value_cents BIGINT,
    validity_seconds BIGINT,
    scope_type VARCHAR(24),
    scope_activity_type VARCHAR(32),
    scope_series_id UUID,
    minimum_order_cents BIGINT,
    evidence_ref VARCHAR(512) NOT NULL,
    approved_at TIMESTAMPTZ NOT NULL,
    effective_at TIMESTAMPTZ NOT NULL,
    recorded_by UUID NOT NULL REFERENCES principals(id),
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT xw_coupon_grant_policy_versions_pkey
        PRIMARY KEY (tenant_id, policy_version),
    CONSTRAINT xw_coupon_grant_policy_effective_key
        UNIQUE (tenant_id, effective_at),
    CONSTRAINT xw_coupon_grant_policy_version_check
        CHECK (policy_version ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$'),
    CONSTRAINT xw_coupon_grant_policy_evidence_check
        CHECK (evidence_ref = BTRIM(evidence_ref) AND CHAR_LENGTH(evidence_ref) BETWEEN 1 AND 512),
    CONSTRAINT xw_coupon_grant_policy_time_check
        CHECK (approved_at <= recorded_at AND recorded_at <= effective_at),
    CONSTRAINT xw_coupon_grant_policy_shape_check
        CHECK (
            (
                enabled
                AND face_value_cents IS NOT NULL
                AND face_value_cents > 0
                AND validity_seconds IS NOT NULL
                AND validity_seconds BETWEEN 1 AND 158112000
                AND minimum_order_cents IS NOT NULL
                AND minimum_order_cents >= 0
                AND scope_type IS NOT NULL
                AND (
                    (
                        scope_type = 'activity_type'
                        AND scope_activity_type IS NOT NULL
                        AND scope_activity_type IN (
                            'ai_roundtable', 'special_event', 'course',
                            'competition', 'custom'
                        )
                        AND scope_series_id IS NULL
                    )
                    OR (
                        scope_type = 'series'
                        AND scope_activity_type IS NULL
                        AND scope_series_id IS NOT NULL
                    )
                )
            )
            OR (
                NOT enabled
                AND face_value_cents IS NULL
                AND validity_seconds IS NULL
                AND scope_type IS NULL
                AND scope_activity_type IS NULL
                AND scope_series_id IS NULL
                AND minimum_order_cents IS NULL
            )
        ),
    CONSTRAINT xw_coupon_grant_policy_series_fkey
        FOREIGN KEY (tenant_id, scope_series_id)
        REFERENCES xiangwan_activity_series (tenant_id, id)
);

CREATE INDEX IF NOT EXISTS idx_xw_coupon_grant_policy_latest
    ON xiangwan_coupon_grant_policy_versions (tenant_id, effective_at DESC);

CREATE OR REPLACE FUNCTION xiangwan_reject_coupon_grant_policy_mutation()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'xiangwan Coupon grant policy versions are immutable';
END;
$$;

DROP TRIGGER IF EXISTS xw_coupon_grant_policy_immutable
    ON xiangwan_coupon_grant_policy_versions;
CREATE TRIGGER xw_coupon_grant_policy_immutable
    BEFORE UPDATE OR DELETE ON xiangwan_coupon_grant_policy_versions
    FOR EACH ROW EXECUTE FUNCTION xiangwan_reject_coupon_grant_policy_mutation();
