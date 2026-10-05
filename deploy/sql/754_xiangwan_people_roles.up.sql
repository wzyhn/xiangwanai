-- 754_xiangwan_people_roles.up.sql
-- Keep public PeopleProfile content, trusted Principal binding, and historical
-- Instance roles as separate tenant-scoped facts.

CREATE TABLE IF NOT EXISTS xiangwan_people_profiles (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    display_name VARCHAR(120) NOT NULL,
    headline VARCHAR(200),
    introduction TEXT NOT NULL DEFAULT '',
    profile_status VARCHAR(16) NOT NULL,
    moderation_status VARCHAR(16) NOT NULL,
    moderated_by UUID REFERENCES principals(id),
    moderated_at TIMESTAMPTZ,
    created_by UUID NOT NULL REFERENCES principals(id),
    updated_by UUID NOT NULL REFERENCES principals(id),
    version BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT xw_people_profiles_content_check
        CHECK (
            display_name = BTRIM(display_name)
            AND BTRIM(display_name) <> ''
            AND CHAR_LENGTH(display_name) <= 120
            AND (
                headline IS NULL
                OR (
                    headline = BTRIM(headline)
                    AND BTRIM(headline) <> ''
                    AND CHAR_LENGTH(headline) <= 200
                )
            )
            AND introduction = BTRIM(introduction)
            AND CHAR_LENGTH(introduction) <= 4000
        ),
    CONSTRAINT xw_people_profiles_status_check
        CHECK (profile_status IN ('draft', 'published', 'archived')),
    CONSTRAINT xw_people_profiles_moderation_check
        CHECK (
            (
                moderation_status = 'pending'
                AND moderated_by IS NULL
                AND moderated_at IS NULL
            )
            OR (
                moderation_status IN ('approved', 'rejected')
                AND moderated_by IS NOT NULL
                AND moderated_at IS NOT NULL
            )
        ),
    CONSTRAINT xw_people_profiles_publication_check
        CHECK (
            (
                profile_status = 'draft'
                AND moderation_status IN ('pending', 'rejected')
            )
            OR (
                profile_status = 'published'
                AND moderation_status = 'approved'
            )
            OR profile_status = 'archived'
        ),
    CONSTRAINT xw_people_profiles_time_check
        CHECK (
            version >= 1
            AND updated_at >= created_at
            AND (moderated_at IS NULL OR moderated_at >= created_at)
            AND (moderated_at IS NULL OR moderated_at <= updated_at)
        ),
    CONSTRAINT xw_people_profiles_tenant_id_id_key
        UNIQUE (tenant_id, id)
);

CREATE INDEX IF NOT EXISTS idx_xw_people_profiles_public
    ON xiangwan_people_profiles (tenant_id, updated_at DESC, id DESC)
    WHERE profile_status = 'published'
      AND moderation_status = 'approved';

CREATE TABLE IF NOT EXISTS xiangwan_people_bindings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    people_profile_id UUID NOT NULL,
    principal_id UUID NOT NULL REFERENCES principals(id),
    evidence_digest BYTEA NOT NULL,
    binding_status VARCHAR(16) NOT NULL,
    bound_by UUID NOT NULL REFERENCES principals(id),
    bound_at TIMESTAMPTZ NOT NULL,
    revoked_by UUID REFERENCES principals(id),
    revoked_at TIMESTAMPTZ,
    revocation_reason VARCHAR(500),
    version BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT xw_people_bindings_profile_fkey
        FOREIGN KEY (tenant_id, people_profile_id)
        REFERENCES xiangwan_people_profiles (tenant_id, id),
    CONSTRAINT xw_people_bindings_evidence_check
        CHECK (OCTET_LENGTH(evidence_digest) = 32),
    CONSTRAINT xw_people_bindings_status_check
        CHECK (binding_status IN ('active', 'revoked')),
    CONSTRAINT xw_people_bindings_state_check
        CHECK (
            (
                binding_status = 'active'
                AND revoked_by IS NULL
                AND revoked_at IS NULL
                AND revocation_reason IS NULL
                AND version = 1
                AND created_at = bound_at
                AND updated_at = bound_at
            )
            OR (
                binding_status = 'revoked'
                AND revoked_by IS NOT NULL
                AND revoked_at IS NOT NULL
                AND revocation_reason = BTRIM(revocation_reason)
                AND BTRIM(COALESCE(revocation_reason, '')) <> ''
                AND CHAR_LENGTH(revocation_reason) <= 500
                AND version = 2
                AND created_at = bound_at
                AND updated_at = revoked_at
                AND revoked_at >= bound_at
            )
        ),
    CONSTRAINT xw_people_bindings_tenant_id_id_key
        UNIQUE (tenant_id, id)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_xw_people_bindings_active_profile
    ON xiangwan_people_bindings (tenant_id, people_profile_id)
    WHERE binding_status = 'active';

CREATE UNIQUE INDEX IF NOT EXISTS uq_xw_people_bindings_active_principal
    ON xiangwan_people_bindings (tenant_id, principal_id)
    WHERE binding_status = 'active';

CREATE INDEX IF NOT EXISTS idx_xw_people_bindings_history
    ON xiangwan_people_bindings (
        tenant_id, principal_id, bound_at DESC, id DESC
    );

CREATE TABLE IF NOT EXISTS xiangwan_instance_role_bindings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    series_id UUID NOT NULL,
    instance_id UUID NOT NULL,
    principal_id UUID NOT NULL REFERENCES principals(id),
    role_code VARCHAR(32) NOT NULL,
    role_status VARCHAR(16) NOT NULL,
    grant_reason VARCHAR(500) NOT NULL,
    granted_by UUID NOT NULL REFERENCES principals(id),
    granted_at TIMESTAMPTZ NOT NULL,
    revoked_by UUID REFERENCES principals(id),
    revoked_at TIMESTAMPTZ,
    revocation_reason VARCHAR(500),
    version BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT xw_instance_roles_instance_fkey
        FOREIGN KEY (tenant_id, series_id, instance_id)
        REFERENCES xiangwan_activity_instances (tenant_id, series_id, id),
    CONSTRAINT xw_instance_roles_code_check
        CHECK (
            role_code IN (
                'host', 'invited_guest',
                'course_instructor', 'event_speaker'
            )
        ),
    CONSTRAINT xw_instance_roles_status_check
        CHECK (role_status IN ('active', 'revoked')),
    CONSTRAINT xw_instance_roles_grant_reason_check
        CHECK (
            grant_reason = BTRIM(grant_reason)
            AND BTRIM(grant_reason) <> ''
            AND CHAR_LENGTH(grant_reason) <= 500
        ),
    CONSTRAINT xw_instance_roles_state_check
        CHECK (
            (
                role_status = 'active'
                AND revoked_by IS NULL
                AND revoked_at IS NULL
                AND revocation_reason IS NULL
                AND version = 1
                AND created_at = granted_at
                AND updated_at = granted_at
            )
            OR (
                role_status = 'revoked'
                AND revoked_by IS NOT NULL
                AND revoked_at IS NOT NULL
                AND revocation_reason = BTRIM(revocation_reason)
                AND BTRIM(COALESCE(revocation_reason, '')) <> ''
                AND CHAR_LENGTH(revocation_reason) <= 500
                AND version = 2
                AND created_at = granted_at
                AND updated_at = revoked_at
                AND revoked_at >= granted_at
            )
        ),
    CONSTRAINT xw_instance_roles_tenant_id_id_key
        UNIQUE (tenant_id, id)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_xw_instance_roles_active
    ON xiangwan_instance_role_bindings (
        tenant_id, instance_id, principal_id, role_code
    )
    WHERE role_status = 'active';

CREATE INDEX IF NOT EXISTS idx_xw_instance_roles_instance
    ON xiangwan_instance_role_bindings (
        tenant_id, instance_id, role_status, granted_at, id
    );

CREATE INDEX IF NOT EXISTS idx_xw_instance_roles_principal_history
    ON xiangwan_instance_role_bindings (
        tenant_id, principal_id, granted_at DESC, id DESC
    );

CREATE OR REPLACE FUNCTION xiangwan_guard_people_history()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF TG_TABLE_NAME = 'xiangwan_people_bindings'
            AND (
                NEW.binding_status <> 'active'
                OR NEW.version <> 1
            ) THEN
            RAISE EXCEPTION 'invalid initial xiangwan People binding'
                USING ERRCODE = '23514';
        ELSIF TG_TABLE_NAME = 'xiangwan_instance_role_bindings'
            AND (
                NEW.role_status <> 'active'
                OR NEW.version <> 1
            ) THEN
            RAISE EXCEPTION 'invalid initial xiangwan Instance role'
                USING ERRCODE = '23514';
        END IF;
        RETURN NEW;
    END IF;

    IF TG_TABLE_NAME = 'xiangwan_people_bindings' THEN
        IF NEW.id IS DISTINCT FROM OLD.id
            OR NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
            OR NEW.people_profile_id IS DISTINCT FROM OLD.people_profile_id
            OR NEW.principal_id IS DISTINCT FROM OLD.principal_id
            OR NEW.evidence_digest IS DISTINCT FROM OLD.evidence_digest
            OR NEW.bound_by IS DISTINCT FROM OLD.bound_by
            OR NEW.bound_at IS DISTINCT FROM OLD.bound_at
            OR NEW.created_at IS DISTINCT FROM OLD.created_at
            OR OLD.binding_status <> 'active'
            OR NEW.binding_status <> 'revoked'
            OR NEW.version <> OLD.version + 1
            OR NEW.updated_at < OLD.updated_at THEN
            RAISE EXCEPTION 'invalid xiangwan People binding transition'
                USING ERRCODE = '23514';
        END IF;
    ELSIF TG_TABLE_NAME = 'xiangwan_instance_role_bindings' THEN
        IF NEW.id IS DISTINCT FROM OLD.id
            OR NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
            OR NEW.series_id IS DISTINCT FROM OLD.series_id
            OR NEW.instance_id IS DISTINCT FROM OLD.instance_id
            OR NEW.principal_id IS DISTINCT FROM OLD.principal_id
            OR NEW.role_code IS DISTINCT FROM OLD.role_code
            OR NEW.grant_reason IS DISTINCT FROM OLD.grant_reason
            OR NEW.granted_by IS DISTINCT FROM OLD.granted_by
            OR NEW.granted_at IS DISTINCT FROM OLD.granted_at
            OR NEW.created_at IS DISTINCT FROM OLD.created_at
            OR OLD.role_status <> 'active'
            OR NEW.role_status <> 'revoked'
            OR NEW.version <> OLD.version + 1
            OR NEW.updated_at < OLD.updated_at THEN
            RAISE EXCEPTION 'invalid xiangwan Instance role transition'
                USING ERRCODE = '23514';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION xiangwan_guard_people_profile()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF NEW.profile_status <> 'draft'
            OR NEW.moderation_status <> 'pending'
            OR NEW.version <> 1
            OR NEW.created_at <> NEW.updated_at
            OR NEW.created_by <> NEW.updated_by THEN
            RAISE EXCEPTION 'invalid initial xiangwan PeopleProfile'
                USING ERRCODE = '23514';
        END IF;
        RETURN NEW;
    END IF;

    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
        OR NEW.created_by IS DISTINCT FROM OLD.created_by
        OR NEW.created_at IS DISTINCT FROM OLD.created_at
        OR OLD.profile_status = 'archived'
        OR NEW.version <> OLD.version + 1
        OR NEW.updated_at < OLD.updated_at THEN
        RAISE EXCEPTION 'invalid xiangwan PeopleProfile transition'
            USING ERRCODE = '23514';
    END IF;

    IF NOT (
        (
            NEW.profile_status = 'archived'
            AND NEW.moderation_status = OLD.moderation_status
            AND NEW.moderated_by IS NOT DISTINCT FROM OLD.moderated_by
            AND NEW.moderated_at IS NOT DISTINCT FROM OLD.moderated_at
            AND NEW.display_name = OLD.display_name
            AND NEW.headline IS NOT DISTINCT FROM OLD.headline
            AND NEW.introduction = OLD.introduction
        )
        OR (
            NEW.profile_status = 'draft'
            AND NEW.moderation_status = 'pending'
            AND NEW.moderated_by IS NULL
            AND NEW.moderated_at IS NULL
        )
        OR (
            OLD.profile_status = 'draft'
            AND OLD.moderation_status = 'pending'
            AND (
                (
                    NEW.profile_status = 'published'
                    AND NEW.moderation_status = 'approved'
                )
                OR (
                    NEW.profile_status = 'draft'
                    AND NEW.moderation_status = 'rejected'
                )
            )
            AND NEW.display_name = OLD.display_name
            AND NEW.headline IS NOT DISTINCT FROM OLD.headline
            AND NEW.introduction = OLD.introduction
            AND NEW.updated_by = NEW.moderated_by
        )
    ) THEN
        RAISE EXCEPTION 'unsupported xiangwan PeopleProfile transition'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION xiangwan_reject_people_history_removal()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'xiangwan People and role history cannot be removed'
        USING ERRCODE = '23514';
END;
$$;

DROP TRIGGER IF EXISTS trg_xw_people_profiles_guard
    ON xiangwan_people_profiles;
CREATE TRIGGER trg_xw_people_profiles_guard
    BEFORE INSERT OR UPDATE ON xiangwan_people_profiles
    FOR EACH ROW EXECUTE FUNCTION xiangwan_guard_people_profile();

DROP TRIGGER IF EXISTS trg_xw_people_profiles_no_delete
    ON xiangwan_people_profiles;
CREATE TRIGGER trg_xw_people_profiles_no_delete
    BEFORE DELETE ON xiangwan_people_profiles
    FOR EACH ROW EXECUTE FUNCTION xiangwan_reject_people_history_removal();

DROP TRIGGER IF EXISTS trg_xw_people_profiles_no_truncate
    ON xiangwan_people_profiles;
CREATE TRIGGER trg_xw_people_profiles_no_truncate
    BEFORE TRUNCATE ON xiangwan_people_profiles
    FOR EACH STATEMENT EXECUTE FUNCTION xiangwan_reject_people_history_removal();

DROP TRIGGER IF EXISTS trg_xw_people_bindings_guard
    ON xiangwan_people_bindings;
CREATE TRIGGER trg_xw_people_bindings_guard
    BEFORE INSERT OR UPDATE ON xiangwan_people_bindings
    FOR EACH ROW EXECUTE FUNCTION xiangwan_guard_people_history();

DROP TRIGGER IF EXISTS trg_xw_people_bindings_no_delete
    ON xiangwan_people_bindings;
CREATE TRIGGER trg_xw_people_bindings_no_delete
    BEFORE DELETE ON xiangwan_people_bindings
    FOR EACH ROW EXECUTE FUNCTION xiangwan_reject_people_history_removal();

DROP TRIGGER IF EXISTS trg_xw_people_bindings_no_truncate
    ON xiangwan_people_bindings;
CREATE TRIGGER trg_xw_people_bindings_no_truncate
    BEFORE TRUNCATE ON xiangwan_people_bindings
    FOR EACH STATEMENT EXECUTE FUNCTION xiangwan_reject_people_history_removal();

DROP TRIGGER IF EXISTS trg_xw_instance_roles_guard
    ON xiangwan_instance_role_bindings;
CREATE TRIGGER trg_xw_instance_roles_guard
    BEFORE INSERT OR UPDATE ON xiangwan_instance_role_bindings
    FOR EACH ROW EXECUTE FUNCTION xiangwan_guard_people_history();

DROP TRIGGER IF EXISTS trg_xw_instance_roles_no_delete
    ON xiangwan_instance_role_bindings;
CREATE TRIGGER trg_xw_instance_roles_no_delete
    BEFORE DELETE ON xiangwan_instance_role_bindings
    FOR EACH ROW EXECUTE FUNCTION xiangwan_reject_people_history_removal();

DROP TRIGGER IF EXISTS trg_xw_instance_roles_no_truncate
    ON xiangwan_instance_role_bindings;
CREATE TRIGGER trg_xw_instance_roles_no_truncate
    BEFORE TRUNCATE ON xiangwan_instance_role_bindings
    FOR EACH STATEMENT EXECUTE FUNCTION xiangwan_reject_people_history_removal();
