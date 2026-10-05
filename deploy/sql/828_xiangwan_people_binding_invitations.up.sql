-- One approved profile, one short-lived invitation, explicit authenticated consent.
-- Plain invitation codes are never stored in PostgreSQL or audit receipts.
CREATE TABLE xiangwan_people_binding_invitations (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    people_profile_id UUID NOT NULL,
    profile_version BIGINT NOT NULL CHECK (profile_version > 0),
    code_digest BYTEA NOT NULL CHECK (octet_length(code_digest)=32),
    invited_by UUID NOT NULL REFERENCES principals(id),
    identity_link_id UUID NOT NULL,
    reason TEXT NOT NULL CHECK (btrim(reason)=reason AND char_length(reason) BETWEEN 1 AND 500),
    invitation_status VARCHAR(20) NOT NULL DEFAULT 'pending' CHECK (invitation_status IN ('pending','accepted','revoked')),
    expires_at TIMESTAMPTZ NOT NULL,
    accepted_by UUID REFERENCES principals(id),
    binding_id UUID,
    claim_operation_id UUID,
    consent_policy_version VARCHAR(64),
    accepted_at TIMESTAMPTZ,
    revoked_by UUID REFERENCES principals(id),
    revocation_reason TEXT,
    revoked_at TIMESTAMPTZ,
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE (tenant_id,id),
    UNIQUE (tenant_id,code_digest),
    UNIQUE (tenant_id,accepted_by,claim_operation_id),
    FOREIGN KEY (tenant_id,people_profile_id) REFERENCES xiangwan_people_profiles(tenant_id,id),
    FOREIGN KEY (tenant_id,identity_link_id) REFERENCES xiangwan_admin_identity_links(tenant_id,id),
    FOREIGN KEY (tenant_id,binding_id) REFERENCES xiangwan_people_bindings(tenant_id,id),
    CHECK (expires_at > created_at AND expires_at <= created_at+INTERVAL '24 hours' AND updated_at>=created_at),
    CHECK (
      (invitation_status='pending' AND version=1 AND updated_at=created_at AND accepted_by IS NULL AND binding_id IS NULL AND claim_operation_id IS NULL AND consent_policy_version IS NULL AND accepted_at IS NULL AND revoked_by IS NULL AND revoked_at IS NULL AND revocation_reason IS NULL)
      OR (invitation_status='accepted' AND version=2 AND accepted_by IS NOT NULL AND binding_id IS NOT NULL AND claim_operation_id IS NOT NULL AND consent_policy_version IS NOT NULL AND btrim(consent_policy_version)<>'' AND accepted_at IS NOT NULL AND accepted_at>=created_at AND accepted_at<expires_at AND updated_at=accepted_at AND revoked_by IS NULL AND revoked_at IS NULL AND revocation_reason IS NULL)
      OR (invitation_status='revoked' AND version=2 AND accepted_by IS NULL AND binding_id IS NULL AND claim_operation_id IS NULL AND consent_policy_version IS NULL AND accepted_at IS NULL AND revoked_by IS NOT NULL AND revoked_at IS NOT NULL AND revoked_at>=created_at AND updated_at=revoked_at AND revocation_reason IS NOT NULL AND btrim(revocation_reason)<>'' AND char_length(revocation_reason)<=500)
    )
);
CREATE UNIQUE INDEX xw_people_invitation_pending_profile ON xiangwan_people_binding_invitations(tenant_id,people_profile_id) WHERE invitation_status='pending';
CREATE INDEX xw_people_invitation_profile_history ON xiangwan_people_binding_invitations(tenant_id,people_profile_id,created_at DESC);

CREATE FUNCTION xiangwan_guard_people_binding_invitation() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP<>'UPDATE' THEN RAISE EXCEPTION 'People binding invitation history cannot be removed'; END IF;
  IF OLD.invitation_status<>'pending' OR NEW.invitation_status NOT IN ('accepted','revoked')
     OR (to_jsonb(NEW)-ARRAY['invitation_status','accepted_by','binding_id','claim_operation_id','consent_policy_version','accepted_at','revoked_by','revocation_reason','revoked_at','version','updated_at'])
        IS DISTINCT FROM (to_jsonb(OLD)-ARRAY['invitation_status','accepted_by','binding_id','claim_operation_id','consent_policy_version','accepted_at','revoked_by','revocation_reason','revoked_at','version','updated_at']) THEN
    RAISE EXCEPTION 'People binding invitation permits one immutable terminal confirmation';
  END IF;
  IF NEW.invitation_status='accepted' AND NOT EXISTS (
    SELECT 1 FROM xiangwan_people_bindings b JOIN xiangwan_people_profiles p ON p.tenant_id=b.tenant_id AND p.id=b.people_profile_id
    WHERE b.tenant_id=NEW.tenant_id AND b.id=NEW.binding_id AND b.people_profile_id=NEW.people_profile_id AND b.principal_id=NEW.accepted_by
      AND b.bound_by=NEW.invited_by AND b.bound_at=NEW.accepted_at AND b.binding_status='active'
      AND p.version=NEW.profile_version AND p.profile_status='published' AND p.moderation_status='approved'
  ) THEN RAISE EXCEPTION 'People binding confirmation requires the exact approved profile and trusted binding'; END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER xw_people_invitation_progress BEFORE UPDATE OR DELETE ON xiangwan_people_binding_invitations FOR EACH ROW EXECUTE FUNCTION xiangwan_guard_people_binding_invitation();
CREATE TRIGGER xw_people_invitation_no_truncate BEFORE TRUNCATE ON xiangwan_people_binding_invitations FOR EACH STATEMENT EXECUTE FUNCTION xiangwan_guard_people_binding_invitation();
