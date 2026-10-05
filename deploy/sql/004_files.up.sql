-- 004: files
-- 文件元数据（storage 模块）

CREATE TABLE files (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    principal_id  UUID NOT NULL REFERENCES principals(id),
    filename      VARCHAR(500) NOT NULL,
    mime          VARCHAR(100) NOT NULL,
    size          BIGINT DEFAULT 0,
    file_key      VARCHAR(500) NOT NULL,
    cdn_url       TEXT DEFAULT '',
    status        VARCHAR(20) DEFAULT 'pending',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_files_principal ON files(principal_id);
CREATE INDEX idx_files_status ON files(status);
