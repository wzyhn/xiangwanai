-- 756_xiangwan_contribution_ledger.up.sql
-- Derive host contributions only from exact trusted identity, Instance-role,
-- and Checkin events. Every correction is a new negative ledger entry.

CREATE TABLE IF NOT EXISTS xiangwan_contribution_entries (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    people_profile_id UUID NOT NULL,
    people_binding_id UUID NOT NULL,
    principal_id UUID NOT NULL REFERENCES principals(id),
    series_id UUID NOT NULL,
    instance_id UUID NOT NULL,
    registration_id UUID NOT NULL,
    session_id UUID NOT NULL,
    checkin_id UUID NOT NULL,
    checkin_event_id UUID NOT NULL,
    role_binding_id UUID NOT NULL,
    contribution_type VARCHAR(32) NOT NULL,
    entry_kind VARCHAR(16) NOT NULL,
    units SMALLINT NOT NULL,
    reversal_of_entry_id UUID,
    occurred_at TIMESTAMPTZ NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT xw_contribution_entries_type_check
        CHECK (contribution_type = 'host_checkin'),
    CONSTRAINT xw_contribution_entries_kind_check
        CHECK (entry_kind IN ('earned', 'reversed')),
    CONSTRAINT xw_contribution_entries_shape_check
        CHECK (
            (
                entry_kind = 'earned'
                AND units = 1
                AND reversal_of_entry_id IS NULL
            )
            OR (
                entry_kind = 'reversed'
                AND units = -1
                AND reversal_of_entry_id IS NOT NULL
                AND reversal_of_entry_id <> id
            )
        ),
    CONSTRAINT xw_contribution_entries_time_check
        CHECK (recorded_at >= occurred_at),
    CONSTRAINT xw_contribution_entries_tenant_id_id_key
        UNIQUE (tenant_id, id),
    CONSTRAINT xw_contribution_entries_people_binding_fkey
        FOREIGN KEY (tenant_id, people_binding_id)
        REFERENCES xiangwan_people_bindings (tenant_id, id),
    CONSTRAINT xw_contribution_entries_role_binding_fkey
        FOREIGN KEY (tenant_id, role_binding_id)
        REFERENCES xiangwan_instance_role_bindings (tenant_id, id),
    CONSTRAINT xw_contribution_entries_checkin_fkey
        FOREIGN KEY (tenant_id, checkin_id)
        REFERENCES xiangwan_checkins (tenant_id, id),
    CONSTRAINT xw_contribution_entries_checkin_event_fkey
        FOREIGN KEY (tenant_id, checkin_event_id)
        REFERENCES xiangwan_checkin_events (tenant_id, id),
    CONSTRAINT xw_contribution_entries_reversal_fkey
        FOREIGN KEY (tenant_id, reversal_of_entry_id)
        REFERENCES xiangwan_contribution_entries (tenant_id, id),
    CONSTRAINT xw_contribution_entries_source_event_key
        UNIQUE (tenant_id, checkin_event_id, contribution_type)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_xw_contribution_earned_business
    ON xiangwan_contribution_entries (
        tenant_id, principal_id, instance_id, contribution_type
    )
    WHERE entry_kind = 'earned';

CREATE UNIQUE INDEX IF NOT EXISTS uq_xw_contribution_reversal
    ON xiangwan_contribution_entries (tenant_id, reversal_of_entry_id)
    WHERE entry_kind = 'reversed';

CREATE INDEX IF NOT EXISTS idx_xw_contribution_principal_history
    ON xiangwan_contribution_entries (
        tenant_id, principal_id, occurred_at DESC, id DESC
    );

CREATE OR REPLACE FUNCTION xiangwan_validate_contribution_entry()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    source_event xiangwan_checkin_events%ROWTYPE;
    source_checkin xiangwan_checkins%ROWTYPE;
    earned_entry xiangwan_contribution_entries%ROWTYPE;
BEGIN
    SELECT *
      INTO source_event
      FROM xiangwan_checkin_events
     WHERE tenant_id = NEW.tenant_id
       AND id = NEW.checkin_event_id;

    SELECT *
      INTO source_checkin
      FROM xiangwan_checkins
     WHERE tenant_id = NEW.tenant_id
       AND id = NEW.checkin_id;

    IF source_event.id IS NULL
        OR source_checkin.id IS NULL
        OR source_event.checkin_id <> source_checkin.id
        OR source_event.registration_id <> NEW.registration_id
        OR source_event.session_id <> NEW.session_id
        OR source_checkin.series_id <> NEW.series_id
        OR source_checkin.instance_id <> NEW.instance_id
        OR source_checkin.registration_id <> NEW.registration_id
        OR source_checkin.session_id <> NEW.session_id
        OR source_checkin.principal_id <> NEW.principal_id
        OR source_event.occurred_at IS DISTINCT FROM NEW.occurred_at THEN
        RAISE EXCEPTION
            'xiangwan Contribution requires an exact Checkin event'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'xw_contribution_entries_source_check';
    END IF;

    IF NEW.entry_kind = 'earned' THEN
        IF source_event.event_type <> 'checked_in'
            OR source_event.event_sequence <> 1
            OR NOT EXISTS (
                SELECT 1
                FROM xiangwan_people_bindings AS people_binding
                WHERE people_binding.tenant_id = NEW.tenant_id
                  AND people_binding.id = NEW.people_binding_id
                  AND people_binding.people_profile_id = NEW.people_profile_id
                  AND people_binding.principal_id = NEW.principal_id
                  AND people_binding.bound_at <= NEW.occurred_at
                  AND (
                      people_binding.revoked_at IS NULL
                      OR people_binding.revoked_at > NEW.occurred_at
                  )
            )
            OR NOT EXISTS (
                SELECT 1
                FROM xiangwan_instance_role_bindings AS role_binding
                WHERE role_binding.tenant_id = NEW.tenant_id
                  AND role_binding.id = NEW.role_binding_id
                  AND role_binding.series_id = NEW.series_id
                  AND role_binding.instance_id = NEW.instance_id
                  AND role_binding.principal_id = NEW.principal_id
                  AND role_binding.role_code = 'host'
                  AND role_binding.granted_at <= NEW.occurred_at
                  AND (
                      role_binding.revoked_at IS NULL
                      OR role_binding.revoked_at > NEW.occurred_at
                  )
            ) THEN
            RAISE EXCEPTION
                'xiangwan earned Contribution lacks trusted host facts'
                USING ERRCODE = '23514',
                      CONSTRAINT = 'xw_contribution_entries_earned_facts';
        END IF;
        RETURN NEW;
    END IF;

    SELECT *
      INTO earned_entry
      FROM xiangwan_contribution_entries
     WHERE tenant_id = NEW.tenant_id
       AND id = NEW.reversal_of_entry_id;

    IF source_event.event_type <> 'revoked'
        OR source_event.event_sequence <> 2
        OR earned_entry.id IS NULL
        OR earned_entry.entry_kind <> 'earned'
        OR earned_entry.people_profile_id <> NEW.people_profile_id
        OR earned_entry.people_binding_id <> NEW.people_binding_id
        OR earned_entry.principal_id <> NEW.principal_id
        OR earned_entry.series_id <> NEW.series_id
        OR earned_entry.instance_id <> NEW.instance_id
        OR earned_entry.registration_id <> NEW.registration_id
        OR earned_entry.session_id <> NEW.session_id
        OR earned_entry.checkin_id <> NEW.checkin_id
        OR earned_entry.role_binding_id <> NEW.role_binding_id
        OR earned_entry.contribution_type <> NEW.contribution_type
        OR NEW.occurred_at < earned_entry.occurred_at THEN
        RAISE EXCEPTION
            'xiangwan reversed Contribution lacks exact earned facts'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'xw_contribution_entries_reversal_facts';
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION xiangwan_reject_contribution_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'xiangwan Contribution ledger is append-only'
        USING ERRCODE = '23514';
END;
$$;

DROP TRIGGER IF EXISTS trg_xw_contribution_validate
    ON xiangwan_contribution_entries;
CREATE TRIGGER trg_xw_contribution_validate
    BEFORE INSERT ON xiangwan_contribution_entries
    FOR EACH ROW EXECUTE FUNCTION xiangwan_validate_contribution_entry();

DROP TRIGGER IF EXISTS trg_xw_contribution_no_update
    ON xiangwan_contribution_entries;
CREATE TRIGGER trg_xw_contribution_no_update
    BEFORE UPDATE ON xiangwan_contribution_entries
    FOR EACH ROW EXECUTE FUNCTION xiangwan_reject_contribution_mutation();

DROP TRIGGER IF EXISTS trg_xw_contribution_no_delete
    ON xiangwan_contribution_entries;
CREATE TRIGGER trg_xw_contribution_no_delete
    BEFORE DELETE ON xiangwan_contribution_entries
    FOR EACH ROW EXECUTE FUNCTION xiangwan_reject_contribution_mutation();

DROP TRIGGER IF EXISTS trg_xw_contribution_no_truncate
    ON xiangwan_contribution_entries;
CREATE TRIGGER trg_xw_contribution_no_truncate
    BEFORE TRUNCATE ON xiangwan_contribution_entries
    FOR EACH STATEMENT EXECUTE FUNCTION xiangwan_reject_contribution_mutation();
