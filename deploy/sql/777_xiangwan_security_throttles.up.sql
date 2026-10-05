-- 777_xiangwan_security_throttles.up.sql
-- Cross-replica security windows for high-risk Xiangwan actions. Subjects are
-- stored only as product-scoped SHA-256 digests; raw client/channel identifiers
-- must never be written to this table.

CREATE TABLE xiangwan_security_throttles (
    scope_key TEXT NOT NULL,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    action TEXT NOT NULL,
    subject_hash BYTEA NOT NULL,
    window_start TIMESTAMPTZ NOT NULL,
    request_count INTEGER NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT xw_security_throttle_scope_check
        CHECK (scope_key = 'wq-xiangwan'),
    CONSTRAINT xw_security_throttle_action_check
        CHECK (action ~ '^[a-z][a-z0-9_.:-]{0,63}$'),
    CONSTRAINT xw_security_throttle_subject_check
        CHECK (octet_length(subject_hash) = 32),
    CONSTRAINT xw_security_throttle_count_check
        CHECK (request_count BETWEEN 1 AND 1000000000),
    CONSTRAINT xw_security_throttle_time_check
        CHECK (
            expires_at > window_start
            AND updated_at >= window_start
        ),
    PRIMARY KEY (scope_key, action, subject_hash, window_start)
);

CREATE INDEX idx_xw_security_throttles_expiry
    ON xiangwan_security_throttles (expires_at);
