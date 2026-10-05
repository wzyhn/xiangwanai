-- 776_xiangwan_data_rights_cases.up.sql
-- Owner-scoped personal-data rights intake plus immutable lifecycle and
-- delivery evidence. PostgreSQL is the only idempotency and history boundary.

CREATE TABLE IF NOT EXISTS xiangwan_data_rights_cases (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    principal_id UUID NOT NULL REFERENCES principals(id),
    operation_key UUID NOT NULL,
    request_fingerprint BYTEA NOT NULL,
    request_type TEXT NOT NULL,
    request_scope TEXT NOT NULL,
    privacy_policy_version TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'submitted',
    version BIGINT NOT NULL DEFAULT 1,
    submitted_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    CONSTRAINT xiangwan_data_rights_cases_tenant_id_id_key
        UNIQUE (tenant_id, id),
    CONSTRAINT xiangwan_data_rights_cases_owner_operation_key
        UNIQUE (tenant_id, principal_id, operation_key),
    CONSTRAINT xiangwan_data_rights_cases_fingerprint_len_check
        CHECK (octet_length(request_fingerprint) = 32),
    CONSTRAINT xiangwan_data_rights_cases_type_check
        CHECK (request_type IN ('access', 'correction', 'export', 'deletion')),
    CONSTRAINT xiangwan_data_rights_cases_scope_check
        CHECK (request_scope IN (
            'all_xiangwan_data',
            'profile',
            'activity_participation',
            'payments_and_refunds',
            'published_content'
        )),
    CONSTRAINT xiangwan_data_rights_cases_policy_version_check
        CHECK (
            privacy_policy_version ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$'
        ),
    CONSTRAINT xiangwan_data_rights_cases_status_check
        CHECK (status IN (
            'submitted',
            'identity_verification',
            'in_review',
            'approved',
            'partially_approved',
            'rejected',
            'fulfilled'
        )),
    CONSTRAINT xiangwan_data_rights_cases_version_check
        CHECK (version >= 1),
    CONSTRAINT xiangwan_data_rights_cases_time_check
        CHECK (updated_at >= submitted_at),
    CONSTRAINT xiangwan_data_rights_cases_completion_check
        CHECK (
            (status IN ('rejected', 'fulfilled')) = (completed_at IS NOT NULL)
            AND (completed_at IS NULL OR completed_at >= submitted_at)
        )
);

CREATE INDEX IF NOT EXISTS idx_xiangwan_data_rights_cases_owner_submitted
    ON xiangwan_data_rights_cases (
        tenant_id, principal_id, submitted_at DESC, id DESC
    );

CREATE TABLE IF NOT EXISTS xiangwan_data_rights_case_events (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    case_id UUID NOT NULL,
    case_version BIGINT NOT NULL,
    event_type TEXT NOT NULL,
    resulting_status TEXT NOT NULL,
    actor_principal_id UUID REFERENCES principals(id),
    policy_basis_version TEXT,
    delivery_kind TEXT,
    delivery_status TEXT,
    evidence_digest BYTEA,
    occurred_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT xiangwan_data_rights_case_events_tenant_id_id_key
        UNIQUE (tenant_id, id),
    CONSTRAINT xiangwan_data_rights_case_events_case_version_key
        UNIQUE (tenant_id, case_id, case_version),
    CONSTRAINT xiangwan_data_rights_case_events_case_fkey
        FOREIGN KEY (tenant_id, case_id)
        REFERENCES xiangwan_data_rights_cases (tenant_id, id),
    CONSTRAINT xiangwan_data_rights_case_events_version_check
        CHECK (case_version >= 1),
    CONSTRAINT xiangwan_data_rights_case_events_type_check
        CHECK (event_type IN (
            'submitted',
            'identity_verification_requested',
            'review_started',
            'approved',
            'partially_approved',
            'rejected',
            'delivery_prepared',
            'delivery_succeeded',
            'delivery_failed',
            'fulfilled'
        )),
    CONSTRAINT xiangwan_data_rights_case_events_status_check
        CHECK (resulting_status IN (
            'submitted',
            'identity_verification',
            'in_review',
            'approved',
            'partially_approved',
            'rejected',
            'fulfilled'
        )),
    CONSTRAINT xiangwan_data_rights_case_events_result_shape_check
        CHECK (
            (event_type = 'submitted' AND resulting_status = 'submitted')
            OR (
                event_type = 'identity_verification_requested'
                AND resulting_status = 'identity_verification'
            )
            OR (event_type = 'review_started' AND resulting_status = 'in_review')
            OR (event_type = 'approved' AND resulting_status = 'approved')
            OR (
                event_type = 'partially_approved'
                AND resulting_status = 'partially_approved'
            )
            OR (event_type = 'rejected' AND resulting_status = 'rejected')
            OR (
                event_type IN (
                    'delivery_prepared',
                    'delivery_succeeded',
                    'delivery_failed'
                )
                AND resulting_status IN ('approved', 'partially_approved')
            )
            OR (event_type = 'fulfilled' AND resulting_status = 'fulfilled')
        ),
    CONSTRAINT xiangwan_data_rights_case_events_policy_check
        CHECK (
            policy_basis_version IS NULL
            OR policy_basis_version ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$'
        ),
    CONSTRAINT xiangwan_data_rights_case_events_delivery_check
        CHECK (
            (
                event_type IN (
                    'delivery_prepared',
                    'delivery_succeeded',
                    'delivery_failed'
                )
                AND delivery_kind IN (
                    'access_copy',
                    'export_archive',
                    'correction_notice',
                    'deletion_disposition'
                )
                AND (
                    (event_type = 'delivery_prepared' AND delivery_status = 'prepared')
                    OR (event_type = 'delivery_succeeded' AND delivery_status = 'delivered')
                    OR (event_type = 'delivery_failed' AND delivery_status = 'failed')
                )
            )
            OR (
                event_type NOT IN (
                    'delivery_prepared',
                    'delivery_succeeded',
                    'delivery_failed'
                )
                AND delivery_kind IS NULL
                AND delivery_status IS NULL
            )
        ),
    CONSTRAINT xiangwan_data_rights_case_events_evidence_check
        CHECK (evidence_digest IS NULL OR octet_length(evidence_digest) = 32)
);

CREATE INDEX IF NOT EXISTS idx_xiangwan_data_rights_case_events_case_time
    ON xiangwan_data_rights_case_events (
        tenant_id, case_id, occurred_at, id
    );

CREATE OR REPLACE FUNCTION xiangwan_guard_data_rights_case_update()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
        OR NEW.principal_id IS DISTINCT FROM OLD.principal_id
        OR NEW.operation_key IS DISTINCT FROM OLD.operation_key
        OR NEW.request_fingerprint IS DISTINCT FROM OLD.request_fingerprint
        OR NEW.request_type IS DISTINCT FROM OLD.request_type
        OR NEW.request_scope IS DISTINCT FROM OLD.request_scope
        OR NEW.privacy_policy_version IS DISTINCT FROM OLD.privacy_policy_version
        OR NEW.submitted_at IS DISTINCT FROM OLD.submitted_at THEN
        RAISE EXCEPTION 'xiangwan data-rights request identity is immutable'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'xiangwan_data_rights_case_identity_immutable';
    END IF;
    IF OLD.status IN ('rejected', 'fulfilled') THEN
        RAISE EXCEPTION 'xiangwan terminal data-rights case is immutable'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'xiangwan_data_rights_case_terminal_immutable';
    END IF;
    IF NEW.version <> OLD.version + 1 OR NEW.updated_at < OLD.updated_at THEN
        RAISE EXCEPTION 'invalid xiangwan data-rights case version transition'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'xiangwan_data_rights_case_version_transition';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_xiangwan_data_rights_case_update
    ON xiangwan_data_rights_cases;
CREATE TRIGGER trg_xiangwan_data_rights_case_update
BEFORE UPDATE ON xiangwan_data_rights_cases
FOR EACH ROW EXECUTE FUNCTION xiangwan_guard_data_rights_case_update();

CREATE OR REPLACE FUNCTION xiangwan_reject_data_rights_case_delete()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'xiangwan data-rights cases cannot be deleted'
        USING ERRCODE = '23514',
              CONSTRAINT = 'xiangwan_data_rights_cases_no_delete';
END;
$$;

DROP TRIGGER IF EXISTS trg_xiangwan_data_rights_case_no_delete
    ON xiangwan_data_rights_cases;
CREATE TRIGGER trg_xiangwan_data_rights_case_no_delete
BEFORE DELETE ON xiangwan_data_rights_cases
FOR EACH ROW EXECUTE FUNCTION xiangwan_reject_data_rights_case_delete();

CREATE OR REPLACE FUNCTION xiangwan_guard_data_rights_event_insert()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    current_version BIGINT;
    current_status TEXT;
    owner_id UUID;
    request_policy_version TEXT;
    case_submitted_at TIMESTAMPTZ;
    previous_event_at TIMESTAMPTZ;
BEGIN
    SELECT version, status, principal_id, privacy_policy_version, submitted_at
      INTO current_version, current_status, owner_id,
           request_policy_version, case_submitted_at
      FROM xiangwan_data_rights_cases
     WHERE tenant_id = NEW.tenant_id
       AND id = NEW.case_id
     FOR KEY SHARE;

    IF NOT FOUND
        OR NEW.case_version <> current_version
        OR NEW.resulting_status <> current_status
        OR NEW.occurred_at < case_submitted_at THEN
        RAISE EXCEPTION 'data-rights event does not match current case fact'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'xiangwan_data_rights_event_case_mismatch';
    END IF;
    IF current_version > 1 THEN
        SELECT occurred_at
          INTO previous_event_at
          FROM xiangwan_data_rights_case_events
         WHERE tenant_id = NEW.tenant_id
           AND case_id = NEW.case_id
           AND case_version = current_version - 1;
        IF NOT FOUND OR NEW.occurred_at < previous_event_at THEN
            RAISE EXCEPTION 'data-rights event history is not contiguous'
                USING ERRCODE = '23514',
                      CONSTRAINT = 'xiangwan_data_rights_event_history_gap';
        END IF;
    END IF;
    IF NEW.event_type = 'submitted' AND (
        NEW.case_version <> 1
        OR NEW.resulting_status <> 'submitted'
        OR NEW.actor_principal_id IS DISTINCT FROM owner_id
        OR NEW.policy_basis_version IS DISTINCT FROM request_policy_version
        OR NEW.delivery_kind IS NOT NULL
        OR NEW.delivery_status IS NOT NULL
        OR NEW.evidence_digest IS NOT NULL
    ) THEN
        RAISE EXCEPTION 'invalid data-rights submission event'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'xiangwan_data_rights_submission_event_mismatch';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_xiangwan_data_rights_event_insert
    ON xiangwan_data_rights_case_events;
CREATE TRIGGER trg_xiangwan_data_rights_event_insert
BEFORE INSERT ON xiangwan_data_rights_case_events
FOR EACH ROW EXECUTE FUNCTION xiangwan_guard_data_rights_event_insert();

CREATE OR REPLACE FUNCTION xiangwan_assert_data_rights_case_event()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
          FROM xiangwan_data_rights_case_events
         WHERE tenant_id = NEW.tenant_id
           AND case_id = NEW.id
           AND case_version = NEW.version
           AND resulting_status = NEW.status
    ) THEN
        RAISE EXCEPTION 'data-rights case version requires a matching event'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'xiangwan_data_rights_case_event_required';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_xiangwan_data_rights_case_event_required
    ON xiangwan_data_rights_cases;
CREATE CONSTRAINT TRIGGER trg_xiangwan_data_rights_case_event_required
AFTER INSERT OR UPDATE ON xiangwan_data_rights_cases
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION xiangwan_assert_data_rights_case_event();

CREATE OR REPLACE FUNCTION xiangwan_reject_data_rights_event_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'xiangwan data-rights events are append-only'
        USING ERRCODE = '23514',
              CONSTRAINT = 'xiangwan_data_rights_events_append_only';
END;
$$;

DROP TRIGGER IF EXISTS trg_xiangwan_data_rights_events_append_only
    ON xiangwan_data_rights_case_events;
CREATE TRIGGER trg_xiangwan_data_rights_events_append_only
BEFORE UPDATE OR DELETE ON xiangwan_data_rights_case_events
FOR EACH ROW EXECUTE FUNCTION xiangwan_reject_data_rights_event_mutation();
