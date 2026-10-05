-- 751_xiangwan_checkin_credentials.up.sql
-- Persist only keyed digests and public JTI metadata for short-lived Checkin
-- credentials. Presented plaintext never crosses the PostgreSQL boundary.

CREATE TABLE IF NOT EXISTS xiangwan_checkin_credentials (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    registration_id UUID NOT NULL,
    series_id UUID NOT NULL,
    instance_id UUID NOT NULL,
    session_id UUID NOT NULL,
    principal_id UUID NOT NULL REFERENCES principals(id),
    credential_jti UUID NOT NULL,
    qr_token_hash BYTEA NOT NULL,
    backup_code_hash BYTEA NOT NULL,
    credential_epoch BIGINT NOT NULL,
    credential_status VARCHAR(16) NOT NULL,
    issued_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    revocation_reason VARCHAR(500),
    version BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT xw_checkin_credentials_hash_shape_check
        CHECK (
            OCTET_LENGTH(qr_token_hash) = 32
            AND OCTET_LENGTH(backup_code_hash) = 32
            AND qr_token_hash <> backup_code_hash
        ),
    CONSTRAINT xw_checkin_credentials_epoch_check
        CHECK (credential_epoch > 0),
    CONSTRAINT xw_checkin_credentials_status_check
        CHECK (credential_status IN ('active', 'revoked')),
    CONSTRAINT xw_checkin_credentials_state_shape_check
        CHECK (
            (
                credential_status = 'active'
                AND version = 1
                AND revoked_at IS NULL
                AND revocation_reason IS NULL
                AND updated_at = issued_at
            )
            OR (
                credential_status = 'revoked'
                AND version = 2
                AND revoked_at IS NOT NULL
                AND updated_at = revoked_at
                AND revocation_reason = BTRIM(revocation_reason)
                AND BTRIM(COALESCE(revocation_reason, '')) <> ''
            )
        ),
    CONSTRAINT xw_checkin_credentials_time_check
        CHECK (
            created_at = issued_at
            AND expires_at > issued_at
            AND expires_at <= issued_at + INTERVAL '15 minutes'
            AND (revoked_at IS NULL OR revoked_at >= issued_at)
        ),
    CONSTRAINT xw_checkin_credentials_registration_fkey
        FOREIGN KEY (
            tenant_id, series_id, instance_id,
            session_id, principal_id, registration_id
        )
        REFERENCES xiangwan_registrations (
            tenant_id, series_id, instance_id,
            session_id, principal_id, id
        ),
    CONSTRAINT xw_checkin_credentials_tenant_id_id_key
        UNIQUE (tenant_id, id),
    CONSTRAINT xw_checkin_credentials_jti_key
        UNIQUE (tenant_id, credential_jti),
    CONSTRAINT xw_checkin_credentials_qr_hash_key
        UNIQUE (tenant_id, qr_token_hash),
    CONSTRAINT xw_checkin_credentials_code_hash_key
        UNIQUE (tenant_id, backup_code_hash),
    CONSTRAINT xw_checkin_credentials_epoch_key
        UNIQUE (tenant_id, registration_id, credential_epoch),
    CONSTRAINT xw_checkin_credentials_attempt_identity_key
        UNIQUE (
            tenant_id, id, credential_jti,
            registration_id, principal_id
        )
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_xw_checkin_credentials_one_active
    ON xiangwan_checkin_credentials (tenant_id, registration_id)
    WHERE credential_status = 'active';

CREATE INDEX IF NOT EXISTS idx_xw_checkin_credentials_expiry
    ON xiangwan_checkin_credentials (
        tenant_id, credential_status, expires_at, id
    );

CREATE TABLE IF NOT EXISTS xiangwan_checkin_verification_attempts (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    requested_series_id UUID NOT NULL,
    requested_instance_id UUID NOT NULL,
    requested_session_id UUID NOT NULL,
    actor_id UUID NOT NULL REFERENCES principals(id),
    presented_kind VARCHAR(16) NOT NULL,
    decision_code VARCHAR(32) NOT NULL,
    idempotency_key VARCHAR(128) NOT NULL,
    credential_id UUID,
    credential_jti UUID,
    registration_id UUID,
    principal_id UUID REFERENCES principals(id),
    checkin_id UUID,
    occurred_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT xw_checkin_attempts_kind_check
        CHECK (presented_kind IN ('qr_token', 'backup_code')),
    CONSTRAINT xw_checkin_attempts_decision_check
        CHECK (
            decision_code IN (
                'valid', 'already_checked_in', 'invalid_credential',
                'expired', 'revoked', 'registration_ineligible',
                'wrong_context'
            )
        ),
    CONSTRAINT xw_checkin_attempts_idempotency_check
        CHECK (idempotency_key ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$'),
    CONSTRAINT xw_checkin_attempts_known_shape_check
        CHECK (
            (
                decision_code = 'invalid_credential'
                AND credential_id IS NULL
                AND credential_jti IS NULL
                AND registration_id IS NULL
                AND principal_id IS NULL
                AND checkin_id IS NULL
            )
            OR (
                decision_code <> 'invalid_credential'
                AND credential_id IS NOT NULL
                AND credential_jti IS NOT NULL
                AND registration_id IS NOT NULL
                AND principal_id IS NOT NULL
                AND (
                    (decision_code = 'already_checked_in' AND checkin_id IS NOT NULL)
                    OR (decision_code <> 'already_checked_in' AND checkin_id IS NULL)
                )
            )
        ),
    CONSTRAINT xw_checkin_attempts_time_check
        CHECK (created_at = occurred_at),
    CONSTRAINT xw_checkin_attempts_requested_instance_fkey
        FOREIGN KEY (
            tenant_id, requested_series_id, requested_instance_id
        )
        REFERENCES xiangwan_activity_instances (
            tenant_id, series_id, id
        ),
    CONSTRAINT xw_checkin_attempts_requested_session_fkey
        FOREIGN KEY (
            tenant_id, requested_instance_id, requested_session_id
        )
        REFERENCES xiangwan_activity_sessions (
            tenant_id, instance_id, id
        ),
    CONSTRAINT xw_checkin_attempts_credential_fkey
        FOREIGN KEY (
            tenant_id, credential_id, credential_jti,
            registration_id, principal_id
        )
        REFERENCES xiangwan_checkin_credentials (
            tenant_id, id, credential_jti,
            registration_id, principal_id
        ),
    CONSTRAINT xw_checkin_attempts_checkin_fkey
        FOREIGN KEY (tenant_id, checkin_id)
        REFERENCES xiangwan_checkins (tenant_id, id),
    CONSTRAINT xw_checkin_attempts_tenant_id_id_key
        UNIQUE (tenant_id, id),
    CONSTRAINT xw_checkin_attempts_idempotency_key
        UNIQUE (tenant_id, actor_id, idempotency_key)
);

CREATE INDEX IF NOT EXISTS idx_xw_checkin_attempts_session_timeline
    ON xiangwan_checkin_verification_attempts (
        tenant_id, requested_session_id, occurred_at DESC, id DESC
    );

CREATE INDEX IF NOT EXISTS idx_xw_checkin_attempts_registration
    ON xiangwan_checkin_verification_attempts (
        tenant_id, registration_id, occurred_at DESC, id DESC
    )
    WHERE registration_id IS NOT NULL;

CREATE OR REPLACE FUNCTION xiangwan_guard_checkin_credential_insert()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    current_registration xiangwan_registrations%ROWTYPE;
BEGIN
    IF NEW.credential_status <> 'active'
        OR NEW.version <> 1
        OR NEW.revoked_at IS NOT NULL
        OR NEW.revocation_reason IS NOT NULL THEN
        RAISE EXCEPTION 'new xiangwan Checkin credential must be active'
            USING ERRCODE = '23514';
    END IF;

    SELECT *
      INTO current_registration
      FROM xiangwan_registrations
     WHERE tenant_id = NEW.tenant_id
       AND id = NEW.registration_id;

    IF NOT FOUND
        OR current_registration.series_id <> NEW.series_id
        OR current_registration.instance_id <> NEW.instance_id
        OR current_registration.session_id <> NEW.session_id
        OR current_registration.principal_id <> NEW.principal_id
        OR current_registration.participation_status <> 'confirmed'
        OR current_registration.confirmed_at IS NULL
        OR NEW.issued_at < current_registration.confirmed_at THEN
        RAISE EXCEPTION
            'xiangwan Checkin credential requires current confirmed Registration'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'xw_checkin_credentials_confirmed_registration';
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION xiangwan_guard_checkin_credential_update()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
        OR NEW.registration_id IS DISTINCT FROM OLD.registration_id
        OR NEW.series_id IS DISTINCT FROM OLD.series_id
        OR NEW.instance_id IS DISTINCT FROM OLD.instance_id
        OR NEW.session_id IS DISTINCT FROM OLD.session_id
        OR NEW.principal_id IS DISTINCT FROM OLD.principal_id
        OR NEW.credential_jti IS DISTINCT FROM OLD.credential_jti
        OR NEW.qr_token_hash IS DISTINCT FROM OLD.qr_token_hash
        OR NEW.backup_code_hash IS DISTINCT FROM OLD.backup_code_hash
        OR NEW.credential_epoch IS DISTINCT FROM OLD.credential_epoch
        OR NEW.issued_at IS DISTINCT FROM OLD.issued_at
        OR NEW.expires_at IS DISTINCT FROM OLD.expires_at
        OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'xiangwan Checkin credential identity is immutable'
            USING ERRCODE = '23514';
    END IF;
    IF OLD.credential_status <> 'active'
        OR NEW.credential_status <> 'revoked'
        OR NEW.version <> OLD.version + 1
        OR NEW.updated_at < OLD.updated_at THEN
        RAISE EXCEPTION 'invalid xiangwan Checkin credential transition'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER trg_xw_checkin_credentials_insert_guard
    BEFORE INSERT ON xiangwan_checkin_credentials
    FOR EACH ROW EXECUTE FUNCTION xiangwan_guard_checkin_credential_insert();

CREATE TRIGGER trg_xw_checkin_credentials_update_guard
    BEFORE UPDATE ON xiangwan_checkin_credentials
    FOR EACH ROW EXECUTE FUNCTION xiangwan_guard_checkin_credential_update();

CREATE TRIGGER trg_xw_checkin_credentials_no_delete
    BEFORE DELETE ON xiangwan_checkin_credentials
    FOR EACH ROW EXECUTE FUNCTION xiangwan_reject_checkin_removal();

CREATE TRIGGER trg_xw_checkin_credentials_no_truncate
    BEFORE TRUNCATE ON xiangwan_checkin_credentials
    FOR EACH STATEMENT EXECUTE FUNCTION xiangwan_reject_checkin_removal();

CREATE TRIGGER trg_xw_checkin_attempts_no_update
    BEFORE UPDATE ON xiangwan_checkin_verification_attempts
    FOR EACH ROW EXECUTE FUNCTION xiangwan_reject_checkin_removal();

CREATE TRIGGER trg_xw_checkin_attempts_no_delete
    BEFORE DELETE ON xiangwan_checkin_verification_attempts
    FOR EACH ROW EXECUTE FUNCTION xiangwan_reject_checkin_removal();

CREATE TRIGGER trg_xw_checkin_attempts_no_truncate
    BEFORE TRUNCATE ON xiangwan_checkin_verification_attempts
    FOR EACH STATEMENT EXECUTE FUNCTION xiangwan_reject_checkin_removal();
