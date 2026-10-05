// Package standalonepg exposes the narrow PostgreSQL identity writer used by
// independently deployed products. It deliberately has no dependency on the
// Auth HTTP package, Gin middleware, cache, event bus, or Redis.
package standalonepg

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const weChatProvider = "wechat"

var (
	ErrInvalidWriter        = errors.New("invalid Auth standalone PostgreSQL writer")
	ErrIdentityNotFound     = errors.New("Auth identity link not found")
	ErrPrincipalUnavailable = errors.New("Auth Principal unavailable")
	ErrWriteConflict        = errors.New("Auth identity write conflict")
)

type Principal struct {
	ID      uuid.UUID
	Status  string
	Deleted bool
}

type IdentityLink struct {
	ID        uuid.UUID
	Principal Principal
	UnionID   string
}

// Writer is bound to a caller-owned SQL transaction. The caller can therefore
// acquire its product generation fence before composing Auth facts with its
// own operation/audit facts in one commit.
type Writer struct {
	tx *sql.Tx
}

func NewWriter(tx *sql.Tx) (*Writer, error) {
	if tx == nil {
		return nil, ErrInvalidWriter
	}
	return &Writer{tx: tx}, nil
}

func (writer *Writer) LockWeChatIdentity(
	ctx context.Context,
	appID string,
	openID string,
	unionID string,
) error {
	if !writer.valid(ctx) || appID == "" || openID == "" {
		return ErrInvalidWriter
	}
	if unionID != "" {
		if _, err := writer.tx.ExecContext(
			ctx,
			"SELECT pg_advisory_xact_lock(hashtext($1), hashtext($2))",
			"wechat-union",
			unionID,
		); err != nil {
			return fmt.Errorf("lock Auth WeChat union identity: %w", err)
		}
	}
	if _, err := writer.tx.ExecContext(
		ctx,
		"SELECT pg_advisory_xact_lock(hashtext($1), hashtext($2))",
		"wechat-openid:"+appID,
		openID,
	); err != nil {
		return fmt.Errorf("lock Auth WeChat app identity: %w", err)
	}
	return nil
}

func (writer *Writer) FindWeChatIdentity(
	ctx context.Context,
	appID string,
	openID string,
) (IdentityLink, error) {
	if !writer.valid(ctx) || appID == "" || openID == "" {
		return IdentityLink{}, ErrInvalidWriter
	}
	var value IdentityLink
	var unionID sql.NullString
	var deletedAt sql.NullTime
	err := writer.tx.QueryRowContext(ctx, `
SELECT identity_link.id, principal.id, principal.status,
       principal.deleted_at, identity_link.union_id
FROM identity_links AS identity_link
JOIN principals AS principal ON principal.id = identity_link.principal_id
WHERE identity_link.provider = $1
  AND identity_link.provider_id = $2
  AND identity_link.app_id = $3
FOR UPDATE OF identity_link, principal
`, weChatProvider, openID, appID).Scan(
		&value.ID,
		&value.Principal.ID,
		&value.Principal.Status,
		&deletedAt,
		&unionID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return IdentityLink{}, ErrIdentityNotFound
	}
	if err != nil {
		return IdentityLink{}, fmt.Errorf("read Auth WeChat identity: %w", err)
	}
	value.Principal.Deleted = deletedAt.Valid
	value.UnionID = unionID.String
	return value, nil
}

func (writer *Writer) FindWeChatUnionPrincipalIDs(
	ctx context.Context,
	unionID string,
) ([]uuid.UUID, error) {
	if !writer.valid(ctx) || unionID == "" {
		return nil, ErrInvalidWriter
	}
	rows, err := writer.tx.QueryContext(ctx, `
SELECT principal_id
FROM identity_links
WHERE provider = $1 AND union_id = $2
ORDER BY principal_id, id
FOR UPDATE
`, weChatProvider, unionID)
	if err != nil {
		return nil, fmt.Errorf("read Auth WeChat union identity: %w", err)
	}
	defer func() { _ = rows.Close() }()
	owners := make([]uuid.UUID, 0, 1)
	for rows.Next() {
		var principalID uuid.UUID
		if err := rows.Scan(&principalID); err != nil {
			return nil, fmt.Errorf("scan Auth WeChat union identity: %w", err)
		}
		if principalID == uuid.Nil {
			return nil, ErrWriteConflict
		}
		if len(owners) == 0 || owners[len(owners)-1] != principalID {
			owners = append(owners, principalID)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Auth WeChat union identity: %w", err)
	}
	return owners, nil
}

func (writer *Writer) LockPrincipal(
	ctx context.Context,
	principalID uuid.UUID,
) (Principal, error) {
	if !writer.valid(ctx) || principalID == uuid.Nil {
		return Principal{}, ErrInvalidWriter
	}
	value := Principal{ID: principalID}
	var deletedAt sql.NullTime
	err := writer.tx.QueryRowContext(ctx, `
SELECT status, deleted_at
FROM principals
WHERE id = $1
FOR UPDATE
`, principalID).Scan(&value.Status, &deletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Principal{}, ErrPrincipalUnavailable
	}
	if err != nil {
		return Principal{}, fmt.Errorf("lock Auth Principal: %w", err)
	}
	value.Deleted = deletedAt.Valid
	return value, nil
}

func (writer *Writer) CreatePrincipal(
	ctx context.Context,
	principalID uuid.UUID,
	primaryTenantID uuid.UUID,
	occurredAt time.Time,
) error {
	if !writer.valid(ctx) || primaryTenantID == uuid.Nil {
		return ErrInvalidWriter
	}
	return CreatePrincipal(ctx, writer.tx, PrincipalCreation{
		ID: principalID, PrimaryTenantID: &primaryTenantID,
		CreatedAt: occurredAt, LastActiveAt: &occurredAt,
	})
}

func (writer *Writer) CreateWeChatIdentity(
	ctx context.Context,
	principalID uuid.UUID,
	appID string,
	openID string,
	unionID string,
	occurredAt time.Time,
) error {
	if !writer.valid(ctx) {
		return ErrInvalidWriter
	}
	return CreateWeChatIdentity(ctx, writer.tx, WeChatIdentityCreation{
		ID: uuid.New(), PrincipalID: principalID, AppID: appID,
		OpenID: openID, UnionID: unionID, CreatedAt: occurredAt,
	})
}

func (writer *Writer) SetWeChatUnionID(
	ctx context.Context,
	linkID uuid.UUID,
	unionID string,
) error {
	if !writer.valid(ctx) {
		return ErrInvalidWriter
	}
	return BackfillWeChatUnionID(ctx, writer.tx, linkID, unionID)
}

type sqlExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// BackfillWeChatUnionID is the shared atomic binding write for the central
// Auth repository and standalone Writer. It fills NULL or replays the same
// value; an established different value, another provider, or a missing link
// returns ErrWriteConflict without changing the row. Empty input cannot clear
// a binding. Callers still own union-owner validation and its advisory lock.
//
// The executor may be a SQL connection or the caller's existing transaction
// (including a GORM transaction's ConnPool). This single-statement operation
// never begins or commits a transaction, so surrounding product writes and
// generation fences retain their original atomic boundary.
func BackfillWeChatUnionID(ctx context.Context, executor sqlExecutor, linkID uuid.UUID, unionID string) error {
	unionID = strings.TrimSpace(unionID)
	if ctx == nil || executor == nil || linkID == uuid.Nil || unionID == "" {
		return ErrInvalidWriter
	}
	result, err := executor.ExecContext(ctx, `
UPDATE identity_links
SET union_id = $2
WHERE id = $1 AND provider = $3
  AND (union_id IS NULL OR union_id = $2)
`, linkID, unionID, weChatProvider)
	if err != nil {
		return fmt.Errorf("bind Auth WeChat union identity: %w", err)
	}
	return requireOneRow(result)
}

func (writer *Writer) RecordProductLogin(
	ctx context.Context,
	principalID uuid.UUID,
	productCode string,
	appID string,
	occurredAt time.Time,
) error {
	if !writer.valid(ctx) || principalID == uuid.Nil || productCode == "" ||
		appID == "" || occurredAt.IsZero() {
		return ErrInvalidWriter
	}
	result, err := writer.tx.ExecContext(ctx, `
UPDATE principals
SET last_active_at = GREATEST(COALESCE(last_active_at, $2), $2),
    updated_at = GREATEST(updated_at, $2)
WHERE id = $1 AND status = 'active' AND deleted_at IS NULL
`, principalID, occurredAt)
	if err != nil {
		return fmt.Errorf("touch Auth Principal login: %w", err)
	}
	if err := requireOneRow(result); err != nil {
		return ErrPrincipalUnavailable
	}
	result, err = writer.tx.ExecContext(ctx, `
INSERT INTO principal_product_states (
    principal_id, product_code, phone_bound, school_bound,
    profile_completed, last_login_at, last_login_app_id,
    created_at, updated_at
)
SELECT id, $2, phone IS NOT NULL, FALSE,
       nickname <> '' OR avatar_url <> '', $3, $4, $3, $3
FROM principals
WHERE id = $1 AND status = 'active' AND deleted_at IS NULL
ON CONFLICT (principal_id, product_code) DO UPDATE SET
    phone_bound = EXCLUDED.phone_bound,
    profile_completed = principal_product_states.profile_completed
        OR EXCLUDED.profile_completed,
    last_login_at = GREATEST(
        COALESCE(principal_product_states.last_login_at, EXCLUDED.last_login_at),
        EXCLUDED.last_login_at
    ),
    last_login_app_id = CASE
        WHEN principal_product_states.last_login_at IS NULL
          OR EXCLUDED.last_login_at > principal_product_states.last_login_at
        THEN EXCLUDED.last_login_app_id
        ELSE principal_product_states.last_login_app_id
    END,
    updated_at = GREATEST(
        principal_product_states.updated_at,
        EXCLUDED.updated_at
    )
`, principalID, productCode, occurredAt, appID)
	if err != nil {
		return fmt.Errorf("record Auth product login: %w", err)
	}
	return requireOneRow(result)
}

func (writer *Writer) valid(ctx context.Context) bool {
	return writer != nil && writer.tx != nil && ctx != nil
}

func requireOneRow(result sql.Result) error {
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return ErrWriteConflict
	}
	return nil
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
