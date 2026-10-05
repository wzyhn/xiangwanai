-- 755_xiangwan_host_applications.up.sql
-- Persist self-service host applications and their terminal review outcome.

CREATE TABLE IF NOT EXISTS xiangwan_host_applications (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    principal_id UUID NOT NULL REFERENCES principals(id),
    application_cycle VARCHAR(100) NOT NULL,
    policy_version VARCHAR(100) NOT NULL,
    personal_introduction TEXT NOT NULL,
    relevant_experience TEXT NOT NULL,
    availability TEXT NOT NULL,
    contact_method VARCHAR(500) NOT NULL,
    application_status VARCHAR(16) NOT NULL,
    reviewed_by UUID REFERENCES principals(id),
    reviewed_at TIMESTAMPTZ,
    review_comment VARCHAR(2000),
    withdrawn_by UUID REFERENCES principals(id),
    withdrawn_at TIMESTAMPTZ,
    version BIGINT NOT NULL,
    submitted_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT xw_host_applications_identity_check
        CHECK (
            application_cycle = BTRIM(application_cycle)
            AND BTRIM(application_cycle) <> ''
            AND CHAR_LENGTH(application_cycle) <= 100
            AND policy_version = BTRIM(policy_version)
            AND BTRIM(policy_version) <> ''
            AND CHAR_LENGTH(policy_version) <= 100
        ),
    CONSTRAINT xw_host_applications_content_check
        CHECK (
            personal_introduction = BTRIM(personal_introduction)
            AND BTRIM(personal_introduction) <> ''
            AND CHAR_LENGTH(personal_introduction) <= 4000
            AND relevant_experience = BTRIM(relevant_experience)
            AND BTRIM(relevant_experience) <> ''
            AND CHAR_LENGTH(relevant_experience) <= 4000
            AND availability = BTRIM(availability)
            AND BTRIM(availability) <> ''
            AND CHAR_LENGTH(availability) <= 1000
            AND contact_method = BTRIM(contact_method)
            AND BTRIM(contact_method) <> ''
            AND CHAR_LENGTH(contact_method) <= 500
        ),
    CONSTRAINT xw_host_applications_status_check
        CHECK (
            application_status IN (
                'pending', 'approved', 'rejected', 'withdrawn'
            )
        ),
    CONSTRAINT xw_host_applications_state_check
        CHECK (
            (
                application_status = 'pending'
                AND reviewed_by IS NULL
                AND reviewed_at IS NULL
                AND review_comment IS NULL
                AND withdrawn_by IS NULL
                AND withdrawn_at IS NULL
                AND version = 1
                AND created_at = submitted_at
                AND updated_at = submitted_at
            )
            OR (
                application_status IN ('approved', 'rejected')
                AND reviewed_by IS NOT NULL
                AND reviewed_at IS NOT NULL
                AND review_comment = BTRIM(review_comment)
                AND BTRIM(COALESCE(review_comment, '')) <> ''
                AND CHAR_LENGTH(review_comment) <= 2000
                AND withdrawn_by IS NULL
                AND withdrawn_at IS NULL
                AND version = 2
                AND created_at = submitted_at
                AND updated_at = reviewed_at
                AND reviewed_at >= submitted_at
            )
            OR (
                application_status = 'withdrawn'
                AND reviewed_by IS NULL
                AND reviewed_at IS NULL
                AND review_comment IS NULL
                AND withdrawn_by = principal_id
                AND withdrawn_at IS NOT NULL
                AND version = 2
                AND created_at = submitted_at
                AND updated_at = withdrawn_at
                AND withdrawn_at >= submitted_at
            )
        ),
    CONSTRAINT xw_host_applications_tenant_id_id_key
        UNIQUE (tenant_id, id)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_xw_host_applications_active_cycle
    ON xiangwan_host_applications (
        tenant_id, principal_id, application_cycle
    )
    WHERE application_status IN ('pending', 'approved');

CREATE UNIQUE INDEX IF NOT EXISTS uq_xw_host_applications_approved_principal
    ON xiangwan_host_applications (tenant_id, principal_id)
    WHERE application_status = 'approved';

CREATE INDEX IF NOT EXISTS idx_xw_host_applications_principal_history
    ON xiangwan_host_applications (
        tenant_id, principal_id, submitted_at DESC, id DESC
    );

CREATE INDEX IF NOT EXISTS idx_xw_host_applications_review_queue
    ON xiangwan_host_applications (
        tenant_id, submitted_at ASC, id ASC
    )
    WHERE application_status = 'pending';

CREATE OR REPLACE FUNCTION xiangwan_guard_host_application()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF NEW.application_status <> 'pending'
            OR NEW.version <> 1
            OR NEW.created_at <> NEW.submitted_at
            OR NEW.updated_at <> NEW.submitted_at THEN
            RAISE EXCEPTION 'invalid initial xiangwan HostApplication'
                USING ERRCODE = '23514';
        END IF;

        IF EXISTS (
            SELECT 1
            FROM xiangwan_host_applications AS existing
            WHERE existing.tenant_id = NEW.tenant_id
              AND existing.principal_id = NEW.principal_id
              AND existing.application_status = 'approved'
        ) OR EXISTS (
            SELECT 1
            FROM xiangwan_instance_role_bindings AS role_binding
            JOIN xiangwan_activity_instances AS activity_instance
              ON activity_instance.tenant_id = role_binding.tenant_id
             AND activity_instance.id = role_binding.instance_id
            WHERE role_binding.tenant_id = NEW.tenant_id
              AND role_binding.principal_id = NEW.principal_id
              AND role_binding.role_code = 'host'
              AND role_binding.role_status = 'active'
              AND activity_instance.status IN (
                  'draft', 'pending_publish', 'published'
              )
        ) THEN
            RAISE EXCEPTION 'xiangwan host cannot submit another application'
                USING ERRCODE = '23514';
        END IF;
        RETURN NEW;
    END IF;

    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
        OR NEW.principal_id IS DISTINCT FROM OLD.principal_id
        OR NEW.application_cycle IS DISTINCT FROM OLD.application_cycle
        OR NEW.policy_version IS DISTINCT FROM OLD.policy_version
        OR NEW.personal_introduction IS DISTINCT
            FROM OLD.personal_introduction
        OR NEW.relevant_experience IS DISTINCT FROM OLD.relevant_experience
        OR NEW.availability IS DISTINCT FROM OLD.availability
        OR NEW.contact_method IS DISTINCT FROM OLD.contact_method
        OR NEW.submitted_at IS DISTINCT FROM OLD.submitted_at
        OR NEW.created_at IS DISTINCT FROM OLD.created_at
        OR OLD.application_status <> 'pending'
        OR NEW.application_status NOT IN (
            'approved', 'rejected', 'withdrawn'
        )
        OR NEW.version <> OLD.version + 1
        OR NEW.updated_at < OLD.updated_at THEN
        RAISE EXCEPTION 'invalid xiangwan HostApplication transition'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION xiangwan_reject_host_application_removal()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'xiangwan HostApplication history cannot be removed'
        USING ERRCODE = '23514';
END;
$$;

DROP TRIGGER IF EXISTS trg_xw_host_applications_guard
    ON xiangwan_host_applications;
CREATE TRIGGER trg_xw_host_applications_guard
    BEFORE INSERT OR UPDATE ON xiangwan_host_applications
    FOR EACH ROW EXECUTE FUNCTION xiangwan_guard_host_application();

DROP TRIGGER IF EXISTS trg_xw_host_applications_no_delete
    ON xiangwan_host_applications;
CREATE TRIGGER trg_xw_host_applications_no_delete
    BEFORE DELETE ON xiangwan_host_applications
    FOR EACH ROW EXECUTE FUNCTION xiangwan_reject_host_application_removal();

DROP TRIGGER IF EXISTS trg_xw_host_applications_no_truncate
    ON xiangwan_host_applications;
CREATE TRIGGER trg_xw_host_applications_no_truncate
    BEFORE TRUNCATE ON xiangwan_host_applications
    FOR EACH STATEMENT
    EXECUTE FUNCTION xiangwan_reject_host_application_removal();
