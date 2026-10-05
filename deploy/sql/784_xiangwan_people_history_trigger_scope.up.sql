-- 784_xiangwan_people_history_trigger_scope.up.sql
-- Keep the shared People/Instance-role history guard from resolving columns
-- that do not exist on the other trigger table.

CREATE OR REPLACE FUNCTION xiangwan_guard_people_history()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF TG_TABLE_NAME = 'xiangwan_people_bindings' THEN
            IF NEW.binding_status <> 'active' OR NEW.version <> 1 THEN
                RAISE EXCEPTION 'invalid initial xiangwan People binding'
                    USING ERRCODE = '23514';
            END IF;
        ELSIF TG_TABLE_NAME = 'xiangwan_instance_role_bindings' THEN
            IF NEW.role_status <> 'active' OR NEW.version <> 1 THEN
                RAISE EXCEPTION 'invalid initial xiangwan Instance role'
                    USING ERRCODE = '23514';
            END IF;
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
