-- Private account contact defaults; each Registration keeps its own snapshot.
-- Nickname remains Auth-owned in principals. No public profile/people query
-- uses this table. Personal-data cases cover it under the profile scope.
CREATE TABLE IF NOT EXISTS xiangwan_registration_contact_defaults (
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    principal_id UUID NOT NULL REFERENCES principals(id) ON DELETE CASCADE,
    phone_e164 VARCHAR(16) NOT NULL CHECK (phone_e164 ~ '^\+[1-9][0-9]{7,14}$'),
    privacy_policy_version VARCHAR(64) NOT NULL,
    contact_policy_version VARCHAR(100) NOT NULL,
    version BIGINT NOT NULL CHECK (version > 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id, principal_id),
    CHECK (CHAR_LENGTH(privacy_policy_version) > 0),
    CHECK (CHAR_LENGTH(contact_policy_version) > 0)
);

-- Logical account deletion must erase the convenience value as well, so
-- reactivating a deleted account cannot resurrect its stored contact number.
CREATE OR REPLACE FUNCTION xiangwan_erase_deleted_principal_contact_defaults()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.deleted_at IS NOT NULL OR NEW.status = 'deleted' THEN
        DELETE FROM xiangwan_registration_contact_defaults WHERE principal_id = NEW.id;
    END IF;
    RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS trg_xiangwan_erase_deleted_principal_contact_defaults ON principals;
CREATE TRIGGER trg_xiangwan_erase_deleted_principal_contact_defaults
    AFTER UPDATE OF status, deleted_at ON principals
    FOR EACH ROW EXECUTE FUNCTION xiangwan_erase_deleted_principal_contact_defaults();
