-- 002: tenants + spaces + space_members
-- 隔离与空间模型

CREATE TABLE tenants (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name          VARCHAR(200) NOT NULL,
    type          VARCHAR(50) DEFAULT 'school',
    metadata      JSONB DEFAULT '{}',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Now add FK for principals.primary_tenant_id
ALTER TABLE principals
    ADD CONSTRAINT fk_principals_tenant
    FOREIGN KEY (primary_tenant_id) REFERENCES tenants(id);

CREATE TABLE spaces (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id     UUID REFERENCES tenants(id),
    owner_id      UUID NOT NULL REFERENCES principals(id),
    name          VARCHAR(200) NOT NULL,
    type          VARCHAR(50) NOT NULL,
    visibility    VARCHAR(20) DEFAULT 'private',
    metadata      JSONB DEFAULT '{}',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at    TIMESTAMPTZ
);

CREATE INDEX idx_spaces_tenant ON spaces(tenant_id);
CREATE INDEX idx_spaces_owner ON spaces(owner_id);

CREATE TABLE space_members (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    space_id      UUID NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    principal_id  UUID NOT NULL REFERENCES principals(id),
    role          VARCHAR(20) DEFAULT 'member',
    joined_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(space_id, principal_id)
);

CREATE INDEX idx_space_members_space ON space_members(space_id);
CREATE INDEX idx_space_members_principal ON space_members(principal_id);
