-- Immutable, administrator-approved rules; no customer wording is seeded.
CREATE TABLE xiangwan_host_rule_versions (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    version BIGINT NOT NULL CHECK (version > 0),
    application_cycle VARCHAR(100) NOT NULL CHECK (application_cycle ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,99}$'),
    policy_version VARCHAR(100) NOT NULL CHECK (policy_version ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,99}$'),
    requirements TEXT NOT NULL CHECK (requirements = BTRIM(requirements) AND CHAR_LENGTH(requirements) BETWEEN 1 AND 4000),
    benefits TEXT NOT NULL CHECK (benefits = BTRIM(benefits) AND CHAR_LENGTH(benefits) BETWEEN 1 AND 4000),
    enabled BOOLEAN NOT NULL,
    approved_by UUID NOT NULL REFERENCES principals(id),
    identity_link_id UUID NOT NULL REFERENCES xiangwan_admin_identity_links(id),
    approval_reason VARCHAR(500) NOT NULL CHECK (approval_reason = BTRIM(approval_reason) AND CHAR_LENGTH(approval_reason) BETWEEN 1 AND 500),
    approved_at TIMESTAMPTZ NOT NULL,
    UNIQUE (tenant_id, version),
    UNIQUE (tenant_id, id)
);
CREATE TABLE xiangwan_host_application_consents (
    application_id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    privacy_policy_version VARCHAR(64) NOT NULL CHECK (privacy_policy_version = BTRIM(privacy_policy_version) AND CHAR_LENGTH(privacy_policy_version) BETWEEN 1 AND 64),
    consented_at TIMESTAMPTZ NOT NULL,
    FOREIGN KEY (tenant_id, application_id) REFERENCES xiangwan_host_applications(tenant_id, id)
);
CREATE FUNCTION xiangwan_reject_host_policy_mutation() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'xiangwan host policy and consent history are immutable' USING ERRCODE = '23514';
END;
$$;
CREATE TRIGGER trg_xw_host_rules_immutable BEFORE UPDATE OR DELETE ON xiangwan_host_rule_versions FOR EACH ROW EXECUTE FUNCTION xiangwan_reject_host_policy_mutation();
CREATE TRIGGER trg_xw_host_rules_no_truncate BEFORE TRUNCATE ON xiangwan_host_rule_versions FOR EACH STATEMENT EXECUTE FUNCTION xiangwan_reject_host_policy_mutation();
CREATE TRIGGER trg_xw_host_consents_immutable BEFORE UPDATE OR DELETE ON xiangwan_host_application_consents FOR EACH ROW EXECUTE FUNCTION xiangwan_reject_host_policy_mutation();
CREATE TRIGGER trg_xw_host_consents_no_truncate BEFORE TRUNCATE ON xiangwan_host_application_consents FOR EACH STATEMENT EXECUTE FUNCTION xiangwan_reject_host_policy_mutation();
