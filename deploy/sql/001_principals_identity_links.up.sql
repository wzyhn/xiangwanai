-- 001: principals + identity_links
-- 身份模型地基

CREATE TABLE principals (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    nickname           VARCHAR(64) NOT NULL DEFAULT '',
    avatar_url         TEXT NOT NULL DEFAULT '',
    phone              VARCHAR(20),
    primary_tenant_id  UUID,
    role               VARCHAR(20) NOT NULL DEFAULT 'student',
    status             VARCHAR(20) NOT NULL DEFAULT 'active',
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at         TIMESTAMPTZ
);

CREATE INDEX idx_principals_phone ON principals(phone) WHERE phone IS NOT NULL;
CREATE INDEX idx_principals_tenant ON principals(primary_tenant_id) WHERE primary_tenant_id IS NOT NULL;

CREATE TABLE identity_links (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    principal_id  UUID NOT NULL REFERENCES principals(id),
    provider      VARCHAR(20) NOT NULL,
    provider_id   VARCHAR(128) NOT NULL,
    app_id        VARCHAR(64),
    union_id      VARCHAR(128),
    metadata      JSONB DEFAULT '{}',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(provider, provider_id, app_id)
);

CREATE INDEX idx_identity_links_principal ON identity_links(principal_id);
CREATE INDEX idx_identity_links_union ON identity_links(union_id) WHERE union_id IS NOT NULL;
