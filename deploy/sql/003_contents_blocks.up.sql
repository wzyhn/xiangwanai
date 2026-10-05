-- 003: contents + blocks + content_relations
-- 统一内容模型

CREATE TABLE contents (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    principal_id  UUID NOT NULL REFERENCES principals(id),
    space_id      UUID REFERENCES spaces(id),
    type          VARCHAR(50) NOT NULL,
    title         VARCHAR(500),
    status        VARCHAR(20) DEFAULT 'active',
    visibility    VARCHAR(20) DEFAULT 'private',
    owner_type    VARCHAR(20) DEFAULT 'user',
    metadata      JSONB DEFAULT '{}',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at    TIMESTAMPTZ
);

CREATE INDEX idx_contents_principal ON contents(principal_id);
CREATE INDEX idx_contents_space ON contents(space_id);
CREATE INDEX idx_contents_type ON contents(type);
CREATE INDEX idx_contents_visibility ON contents(visibility);
CREATE INDEX idx_contents_owner_type ON contents(owner_type);

CREATE TABLE blocks (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    content_id    UUID NOT NULL REFERENCES contents(id) ON DELETE CASCADE,
    type          VARCHAR(50) NOT NULL,
    sort_order    INT NOT NULL DEFAULT 0,
    data          JSONB NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_blocks_content ON blocks(content_id);
CREATE INDEX idx_blocks_sort ON blocks(content_id, sort_order);

CREATE TABLE content_relations (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source_id     UUID NOT NULL REFERENCES contents(id) ON DELETE CASCADE,
    target_id     UUID NOT NULL REFERENCES contents(id) ON DELETE CASCADE,
    relation_type VARCHAR(50) NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_content_relations_source ON content_relations(source_id);
CREATE INDEX idx_content_relations_target ON content_relations(target_id);
