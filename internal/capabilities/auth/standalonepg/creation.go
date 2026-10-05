package standalonepg

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// PrincipalCreation contains only Auth's initial WeChat principal facts.
// Tenant assignment and the initial activity observation remain caller policy.
type PrincipalCreation struct {
	ID              uuid.UUID
	PrimaryTenantID *uuid.UUID
	CreatedAt       time.Time
	LastActiveAt    *time.Time
}

// CreatePrincipal inserts on the supplied connection or caller transaction.
// It never begins or commits a transaction; callers compose the identity link
// and their product generation fence in the same atomic boundary.
func CreatePrincipal(ctx context.Context, executor sqlExecutor, in PrincipalCreation) error {
	if ctx == nil || executor == nil || in.ID == uuid.Nil || in.CreatedAt.IsZero() ||
		(in.PrimaryTenantID != nil && *in.PrimaryTenantID == uuid.Nil) ||
		(in.LastActiveAt != nil && in.LastActiveAt.IsZero()) {
		return ErrInvalidWriter
	}
	result, err := executor.ExecContext(ctx, `
INSERT INTO principals (
    id, nickname, avatar_url, primary_tenant_id, role, status,
    last_active_at, created_at, updated_at
) VALUES ($1, '', '', $2, 'student', 'active', $3, $4, $4)
`, in.ID, in.PrimaryTenantID, in.LastActiveAt, in.CreatedAt)
	if err != nil {
		return fmt.Errorf("create Auth Principal: %w", err)
	}
	return requireOneRow(result)
}

type WeChatIdentityCreation struct {
	ID          uuid.UUID
	PrincipalID uuid.UUID
	AppID       string
	OpenID      string
	UnionID     string
	CreatedAt   time.Time
}

// CreateWeChatIdentity stores already-normalized identity keys. It preserves
// PostgreSQL uniqueness errors so the caller can roll back and retry resolution.
// Union ownership checks and advisory locks still belong to login orchestration.
func CreateWeChatIdentity(ctx context.Context, executor sqlExecutor, in WeChatIdentityCreation) error {
	if ctx == nil || executor == nil || in.ID == uuid.Nil || in.PrincipalID == uuid.Nil ||
		in.CreatedAt.IsZero() || in.AppID == "" || in.OpenID == "" ||
		strings.TrimSpace(in.AppID) != in.AppID || strings.TrimSpace(in.OpenID) != in.OpenID ||
		strings.TrimSpace(in.UnionID) != in.UnionID {
		return ErrInvalidWriter
	}
	result, err := executor.ExecContext(ctx, `
INSERT INTO identity_links (
    id, principal_id, provider, provider_id, app_id, union_id,
    metadata, created_at
) VALUES ($1, $2, $3, $4, $5, $6, '{}'::jsonb, $7)
`, in.ID, in.PrincipalID, weChatProvider, in.OpenID, in.AppID,
		nullableString(in.UnionID), in.CreatedAt)
	if err != nil {
		return fmt.Errorf("create Auth WeChat identity link: %w", err)
	}
	return requireOneRow(result)
}
