-- 781_xiangwan_admin_foundation.up.sql
-- PostgreSQL-only administrator identity, session, authorization, operation,
-- and audit foundation for the independently deployed Xiangwan operator Web.

CREATE TABLE IF NOT EXISTS xiangwan_admin_identity_links (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    issuer TEXT NOT NULL,
    subject VARCHAR(255) NOT NULL,
    principal_id UUID NOT NULL REFERENCES principals(id),
    link_status VARCHAR(16) NOT NULL DEFAULT 'active',
    linked_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    revocation_reason VARCHAR(500),
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT xw_admin_identity_link_text_check CHECK (
        issuer = BTRIM(issuer)
        AND CHAR_LENGTH(issuer) BETWEEN 8 AND 2048
        AND issuer !~ '[[:cntrl:]]'
        AND subject = BTRIM(subject)
        AND CHAR_LENGTH(subject) BETWEEN 1 AND 255
        AND subject !~ '[[:cntrl:]]'
    ),
    CONSTRAINT xw_admin_identity_link_status_check
        CHECK (link_status IN ('active', 'revoked')),
    CONSTRAINT xw_admin_identity_link_state_check CHECK (
        (
            link_status = 'active'
            AND revoked_at IS NULL
            AND revocation_reason IS NULL
        ) OR (
            link_status = 'revoked'
            AND revoked_at IS NOT NULL
            AND revoked_at >= linked_at
            AND revocation_reason = BTRIM(revocation_reason)
            AND CHAR_LENGTH(revocation_reason) BETWEEN 1 AND 500
        )
    ),
    CONSTRAINT xw_admin_identity_link_time_check CHECK (
        created_at = linked_at
        AND updated_at >= created_at
    ),
    CONSTRAINT xw_admin_identity_link_version_check CHECK (version >= 1),
    CONSTRAINT xw_admin_identity_link_tenant_id_id_key UNIQUE (tenant_id, id),
    CONSTRAINT xw_admin_identity_link_subject_key UNIQUE (tenant_id, issuer, subject)
);

CREATE INDEX IF NOT EXISTS idx_xw_admin_identity_link_principal
    ON xiangwan_admin_identity_links (
        tenant_id, principal_id, link_status, id
    );

CREATE TABLE IF NOT EXISTS xiangwan_admin_login_attempts (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    state_hash BYTEA NOT NULL,
    nonce_hash BYTEA NOT NULL,
    encrypted_pkce_verifier BYTEA NOT NULL,
    browser_binding_hash BYTEA NOT NULL,
    client_fingerprint BYTEA NOT NULL,
    return_to VARCHAR(512) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    CONSTRAINT xw_admin_login_attempt_hash_check CHECK (
        OCTET_LENGTH(state_hash) = 32
        AND OCTET_LENGTH(nonce_hash) = 32
        AND OCTET_LENGTH(browser_binding_hash) = 32
        AND OCTET_LENGTH(client_fingerprint) = 32
        AND OCTET_LENGTH(encrypted_pkce_verifier) BETWEEN 45 AND 512
    ),
    CONSTRAINT xw_admin_login_attempt_return_check CHECK (
        return_to LIKE '/%'
        AND return_to NOT LIKE '//%'
        AND return_to !~ '[[:cntrl:]]'
    ),
    CONSTRAINT xw_admin_login_attempt_time_check CHECK (
        expires_at > created_at
        AND expires_at <= created_at + INTERVAL '10 minutes'
        AND (consumed_at IS NULL OR consumed_at >= created_at)
        AND (completed_at IS NULL OR (
            consumed_at IS NOT NULL
            AND completed_at >= consumed_at
        ))
    ),
    CONSTRAINT xw_admin_login_attempt_state_key UNIQUE (tenant_id, state_hash)
);

CREATE INDEX IF NOT EXISTS idx_xw_admin_login_attempt_expiry
    ON xiangwan_admin_login_attempts (tenant_id, expires_at, id);

CREATE INDEX IF NOT EXISTS idx_xw_admin_login_attempt_throttle
    ON xiangwan_admin_login_attempts (
        tenant_id, client_fingerprint, created_at DESC, id
    );

CREATE TABLE IF NOT EXISTS xiangwan_admin_sessions (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL,
    principal_id UUID NOT NULL REFERENCES principals(id),
    identity_link_id UUID NOT NULL,
    token_hash BYTEA NOT NULL,
    csrf_token_hash BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    last_seen_at TIMESTAMPTZ NOT NULL,
    idle_expires_at TIMESTAMPTZ NOT NULL,
    absolute_expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    revocation_reason VARCHAR(500),
    version BIGINT NOT NULL DEFAULT 1,
    CONSTRAINT xw_admin_session_identity_fkey
        FOREIGN KEY (tenant_id, identity_link_id)
        REFERENCES xiangwan_admin_identity_links (tenant_id, id),
    CONSTRAINT xw_admin_session_hash_check CHECK (
        OCTET_LENGTH(token_hash) = 32
        AND OCTET_LENGTH(csrf_token_hash) = 32
        AND token_hash <> csrf_token_hash
    ),
    CONSTRAINT xw_admin_session_time_check CHECK (
        last_seen_at >= created_at
        AND idle_expires_at > last_seen_at
        AND absolute_expires_at > created_at
        AND idle_expires_at <= absolute_expires_at
        AND (revoked_at IS NULL OR revoked_at >= created_at)
    ),
    CONSTRAINT xw_admin_session_revocation_check CHECK (
        (revoked_at IS NULL AND revocation_reason IS NULL)
        OR (
            revoked_at IS NOT NULL
            AND revocation_reason = BTRIM(revocation_reason)
            AND CHAR_LENGTH(revocation_reason) BETWEEN 1 AND 500
        )
    ),
    CONSTRAINT xw_admin_session_version_check CHECK (version >= 1),
    CONSTRAINT xw_admin_session_tenant_id_id_key UNIQUE (tenant_id, id),
    CONSTRAINT xw_admin_session_token_key UNIQUE (token_hash)
);

CREATE INDEX IF NOT EXISTS idx_xw_admin_session_principal
    ON xiangwan_admin_sessions (
        tenant_id, principal_id, absolute_expires_at DESC, id
    ) WHERE revoked_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_xw_admin_session_expiry
    ON xiangwan_admin_sessions (
        tenant_id, idle_expires_at, absolute_expires_at, id
    ) WHERE revoked_at IS NULL;

CREATE TABLE IF NOT EXISTS xiangwan_admin_grants (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    principal_id UUID NOT NULL REFERENCES principals(id),
    domain_code VARCHAR(32) NOT NULL,
    capability VARCHAR(32) NOT NULL,
    scope_type VARCHAR(16) NOT NULL,
    scope_id UUID,
    grant_status VARCHAR(16) NOT NULL DEFAULT 'active',
    granted_by UUID REFERENCES principals(id),
    grant_reason VARCHAR(500) NOT NULL,
    granted_at TIMESTAMPTZ NOT NULL,
    revoked_by UUID REFERENCES principals(id),
    revoked_at TIMESTAMPTZ,
    revocation_reason VARCHAR(500),
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT xw_admin_grant_domain_check CHECK (domain_code = 'xiangwan'),
    CONSTRAINT xw_admin_grant_capability_check CHECK (
        capability IN ('super_admin', 'activity_operator', 'onsite_checkin')
    ),
    CONSTRAINT xw_admin_grant_scope_check CHECK (
        (scope_type = 'tenant' AND scope_id IS NULL)
        OR (
            scope_type = 'session'
            AND scope_id IS NOT NULL
            AND capability = 'onsite_checkin'
        )
    ),
    CONSTRAINT xw_admin_grant_status_check
        CHECK (grant_status IN ('active', 'revoked')),
    CONSTRAINT xw_admin_grant_reason_check CHECK (
        grant_reason = BTRIM(grant_reason)
        AND CHAR_LENGTH(grant_reason) BETWEEN 1 AND 500
    ),
    CONSTRAINT xw_admin_grant_state_check CHECK (
        (
            grant_status = 'active'
            AND revoked_by IS NULL
            AND revoked_at IS NULL
            AND revocation_reason IS NULL
        ) OR (
            grant_status = 'revoked'
            AND revoked_by IS NOT NULL
            AND revoked_at IS NOT NULL
            AND revoked_at >= granted_at
            AND revocation_reason = BTRIM(revocation_reason)
            AND CHAR_LENGTH(revocation_reason) BETWEEN 1 AND 500
        )
    ),
    CONSTRAINT xw_admin_grant_time_check CHECK (
        created_at = granted_at
        AND updated_at >= created_at
    ),
    CONSTRAINT xw_admin_grant_version_check CHECK (version >= 1),
    CONSTRAINT xw_admin_grant_tenant_id_id_key UNIQUE (tenant_id, id)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_xw_admin_grant_active_target
    ON xiangwan_admin_grants (
        tenant_id,
        principal_id,
        domain_code,
        capability,
        scope_type,
        COALESCE(scope_id, '00000000-0000-0000-0000-000000000000'::UUID)
    ) WHERE grant_status = 'active';

CREATE INDEX IF NOT EXISTS idx_xw_admin_grant_authorization
    ON xiangwan_admin_grants (
        tenant_id, principal_id, capability, scope_type, scope_id
    ) WHERE grant_status = 'active';

CREATE TABLE IF NOT EXISTS xiangwan_admin_operations (
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    actor_id UUID NOT NULL REFERENCES principals(id),
    operation_id UUID NOT NULL,
    operation_kind VARCHAR(64) NOT NULL,
    request_digest CHAR(64) NOT NULL,
    result JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT xw_admin_operation_kind_check CHECK (
        operation_kind ~ '^[a-z][a-z0-9_.:-]{0,63}$'
    ),
    CONSTRAINT xw_admin_operation_digest_check CHECK (
        request_digest ~ '^[0-9a-f]{64}$'
    ),
    CONSTRAINT xw_admin_operation_result_check CHECK (
        JSONB_TYPEOF(result) = 'object'
    ),
    CONSTRAINT xw_admin_operation_key
        PRIMARY KEY (tenant_id, actor_id, operation_id)
);

CREATE TABLE IF NOT EXISTS xiangwan_admin_audit_events (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    actor_id UUID NOT NULL REFERENCES principals(id),
    action_code VARCHAR(64) NOT NULL,
    target_type VARCHAR(32) NOT NULL,
    target_id UUID NOT NULL,
    request_id VARCHAR(128) NOT NULL,
    details JSONB NOT NULL DEFAULT '{}'::JSONB,
    occurred_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT xw_admin_audit_action_check CHECK (
        action_code ~ '^[a-z][a-z0-9_.:-]{0,63}$'
    ),
    CONSTRAINT xw_admin_audit_target_check CHECK (
        target_type ~ '^[a-z][a-z0-9_]{0,31}$'
    ),
    CONSTRAINT xw_admin_audit_request_check CHECK (
        request_id = BTRIM(request_id)
        AND CHAR_LENGTH(request_id) BETWEEN 1 AND 128
        AND request_id !~ '[[:cntrl:]]'
    ),
    CONSTRAINT xw_admin_audit_details_check CHECK (
        JSONB_TYPEOF(details) = 'object'
    ),
    CONSTRAINT xw_admin_audit_time_check CHECK (created_at = occurred_at),
    CONSTRAINT xw_admin_audit_tenant_id_id_key UNIQUE (tenant_id, id)
);

CREATE INDEX IF NOT EXISTS idx_xw_admin_audit_timeline
    ON xiangwan_admin_audit_events (
        tenant_id, occurred_at DESC, id DESC
    );

CREATE OR REPLACE FUNCTION xiangwan_guard_admin_identity_link_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
        OR NEW.issuer IS DISTINCT FROM OLD.issuer
        OR NEW.subject IS DISTINCT FROM OLD.subject
        OR NEW.principal_id IS DISTINCT FROM OLD.principal_id
        OR NEW.linked_at IS DISTINCT FROM OLD.linked_at
        OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'xiangwan admin identity is immutable'
            USING ERRCODE = '23514';
    END IF;
    IF OLD.link_status <> 'active'
        OR NEW.link_status <> 'revoked'
        OR NEW.version <> OLD.version + 1
        OR NEW.updated_at < OLD.updated_at THEN
        RAISE EXCEPTION 'invalid xiangwan admin identity transition'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER trg_xw_admin_identity_link_mutation
    BEFORE UPDATE ON xiangwan_admin_identity_links
    FOR EACH ROW EXECUTE FUNCTION xiangwan_guard_admin_identity_link_mutation();

CREATE OR REPLACE FUNCTION xiangwan_guard_admin_session_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
        OR NEW.principal_id IS DISTINCT FROM OLD.principal_id
        OR NEW.identity_link_id IS DISTINCT FROM OLD.identity_link_id
        OR NEW.token_hash IS DISTINCT FROM OLD.token_hash
        OR NEW.csrf_token_hash IS DISTINCT FROM OLD.csrf_token_hash
        OR NEW.created_at IS DISTINCT FROM OLD.created_at
        OR NEW.absolute_expires_at IS DISTINCT FROM OLD.absolute_expires_at THEN
        RAISE EXCEPTION 'xiangwan admin session identity is immutable'
            USING ERRCODE = '23514';
    END IF;
    IF NEW.version <> OLD.version + 1
        OR NEW.last_seen_at < OLD.last_seen_at
        OR NEW.idle_expires_at < OLD.idle_expires_at
        OR (OLD.revoked_at IS NOT NULL AND NEW.revoked_at IS DISTINCT FROM OLD.revoked_at) THEN
        RAISE EXCEPTION 'invalid xiangwan admin session transition'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER trg_xw_admin_session_mutation
    BEFORE UPDATE ON xiangwan_admin_sessions
    FOR EACH ROW EXECUTE FUNCTION xiangwan_guard_admin_session_mutation();

CREATE OR REPLACE FUNCTION xiangwan_guard_admin_grant_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
        OR NEW.principal_id IS DISTINCT FROM OLD.principal_id
        OR NEW.domain_code IS DISTINCT FROM OLD.domain_code
        OR NEW.capability IS DISTINCT FROM OLD.capability
        OR NEW.scope_type IS DISTINCT FROM OLD.scope_type
        OR NEW.scope_id IS DISTINCT FROM OLD.scope_id
        OR NEW.granted_by IS DISTINCT FROM OLD.granted_by
        OR NEW.grant_reason IS DISTINCT FROM OLD.grant_reason
        OR NEW.granted_at IS DISTINCT FROM OLD.granted_at
        OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'xiangwan admin grant identity is immutable'
            USING ERRCODE = '23514';
    END IF;
    IF OLD.grant_status <> 'active'
        OR NEW.grant_status <> 'revoked'
        OR NEW.version <> OLD.version + 1
        OR NEW.updated_at < OLD.updated_at THEN
        RAISE EXCEPTION 'invalid xiangwan admin grant transition'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER trg_xw_admin_grant_mutation
    BEFORE UPDATE ON xiangwan_admin_grants
    FOR EACH ROW EXECUTE FUNCTION xiangwan_guard_admin_grant_mutation();

CREATE OR REPLACE FUNCTION xiangwan_reject_admin_authority_deletion()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'xiangwan admin authority must use an explicit revocation transition'
        USING ERRCODE = '0A000';
END;
$$;

CREATE TRIGGER trg_xw_admin_identity_link_no_delete
    BEFORE DELETE ON xiangwan_admin_identity_links
    FOR EACH ROW EXECUTE FUNCTION xiangwan_reject_admin_authority_deletion();
CREATE TRIGGER trg_xw_admin_identity_link_no_truncate
    BEFORE TRUNCATE ON xiangwan_admin_identity_links
    FOR EACH STATEMENT EXECUTE FUNCTION xiangwan_reject_admin_authority_deletion();
CREATE TRIGGER trg_xw_admin_grant_no_delete
    BEFORE DELETE ON xiangwan_admin_grants
    FOR EACH ROW EXECUTE FUNCTION xiangwan_reject_admin_authority_deletion();
CREATE TRIGGER trg_xw_admin_grant_no_truncate
    BEFORE TRUNCATE ON xiangwan_admin_grants
    FOR EACH STATEMENT EXECUTE FUNCTION xiangwan_reject_admin_authority_deletion();

CREATE OR REPLACE FUNCTION xiangwan_reject_admin_append_only_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'xiangwan admin receipt is append-only'
        USING ERRCODE = '0A000';
END;
$$;

CREATE TRIGGER trg_xw_admin_operations_append_only
    BEFORE UPDATE OR DELETE ON xiangwan_admin_operations
    FOR EACH ROW EXECUTE FUNCTION xiangwan_reject_admin_append_only_mutation();
CREATE TRIGGER trg_xw_admin_operations_no_truncate
    BEFORE TRUNCATE ON xiangwan_admin_operations
    FOR EACH STATEMENT EXECUTE FUNCTION xiangwan_reject_admin_append_only_mutation();
CREATE TRIGGER trg_xw_admin_audit_append_only
    BEFORE UPDATE OR DELETE ON xiangwan_admin_audit_events
    FOR EACH ROW EXECUTE FUNCTION xiangwan_reject_admin_append_only_mutation();
CREATE TRIGGER trg_xw_admin_audit_no_truncate
    BEFORE TRUNCATE ON xiangwan_admin_audit_events
    FOR EACH STATEMENT EXECUTE FUNCTION xiangwan_reject_admin_append_only_mutation();

-- Publication reference mutations and the administrator publisher take the
-- same tenant-scoped transaction lock. This makes the readiness snapshot
-- stable through commit without coupling each domain repository to the
-- administrator package.
CREATE OR REPLACE FUNCTION xiangwan_lock_publication_reference_aggregate()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    reference_tenant_id UUID;
    second_tenant_id UUID;
BEGIN
    IF TG_OP = 'UPDATE' AND OLD.tenant_id IS DISTINCT FROM NEW.tenant_id THEN
        IF OLD.tenant_id::TEXT < NEW.tenant_id::TEXT THEN
            reference_tenant_id := OLD.tenant_id;
            second_tenant_id := NEW.tenant_id;
        ELSE
            reference_tenant_id := NEW.tenant_id;
            second_tenant_id := OLD.tenant_id;
        END IF;
    ELSIF TG_OP = 'DELETE' THEN
        reference_tenant_id := OLD.tenant_id;
    ELSE
        reference_tenant_id := NEW.tenant_id;
    END IF;
    PERFORM pg_advisory_xact_lock(
        hashtextextended(
            'wq-xiangwan:publication-references:' || reference_tenant_id::TEXT,
            0
        )
    );
    IF second_tenant_id IS NOT NULL THEN
        PERFORM pg_advisory_xact_lock(
            hashtextextended(
                'wq-xiangwan:publication-references:' || second_tenant_id::TEXT,
                0
            )
        );
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER trg_xw_brand_profiles_publication_lock
    BEFORE INSERT OR UPDATE OR DELETE ON xiangwan_brand_profiles
    FOR EACH ROW EXECUTE FUNCTION xiangwan_lock_publication_reference_aggregate();
CREATE TRIGGER trg_xw_brand_publications_publication_lock
    BEFORE INSERT OR UPDATE OR DELETE ON xiangwan_brand_profile_publications
    FOR EACH ROW EXECUTE FUNCTION xiangwan_lock_publication_reference_aggregate();
CREATE TRIGGER trg_xw_questionnaires_publication_lock
    BEFORE INSERT OR UPDATE OR DELETE ON xiangwan_instance_questionnaires
    FOR EACH ROW EXECUTE FUNCTION xiangwan_lock_publication_reference_aggregate();
CREATE TRIGGER trg_xw_questionnaire_versions_publication_lock
    BEFORE INSERT OR UPDATE OR DELETE ON xiangwan_questionnaire_versions
    FOR EACH ROW EXECUTE FUNCTION xiangwan_lock_publication_reference_aggregate();
CREATE TRIGGER trg_xw_questionnaire_fields_publication_lock
    BEFORE INSERT OR UPDATE OR DELETE ON xiangwan_questionnaire_fields
    FOR EACH ROW EXECUTE FUNCTION xiangwan_lock_publication_reference_aggregate();
CREATE TRIGGER trg_xw_instance_roles_publication_lock
    BEFORE INSERT OR UPDATE OR DELETE ON xiangwan_instance_role_bindings
    FOR EACH ROW EXECUTE FUNCTION xiangwan_lock_publication_reference_aggregate();
CREATE TRIGGER trg_xw_people_bindings_publication_lock
    BEFORE INSERT OR UPDATE OR DELETE ON xiangwan_people_bindings
    FOR EACH ROW EXECUTE FUNCTION xiangwan_lock_publication_reference_aggregate();
CREATE TRIGGER trg_xw_people_profiles_publication_lock
    BEFORE INSERT OR UPDATE OR DELETE ON xiangwan_people_profiles
    FOR EACH ROW EXECUTE FUNCTION xiangwan_lock_publication_reference_aggregate();
CREATE TRIGGER trg_xw_resource_relations_publication_lock
    BEFORE INSERT OR UPDATE OR DELETE ON xiangwan_resource_relations
    FOR EACH ROW EXECUTE FUNCTION xiangwan_lock_publication_reference_aggregate();
CREATE TRIGGER trg_xw_resource_publications_publication_lock
    BEFORE INSERT OR UPDATE OR DELETE ON xiangwan_resource_publications
    FOR EACH ROW EXECUTE FUNCTION xiangwan_lock_publication_reference_aggregate();
