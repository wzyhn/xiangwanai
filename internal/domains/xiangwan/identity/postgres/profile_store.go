package identitypostgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/consumerprofile"
	"github.com/google/uuid"
)

var ErrProfileETagConflict = errors.New("xiangwan Principal profile etag conflict")

// ProfileStore maintains the Auth-owned identity projection (nickname and
// avatar) that the standalone Xiangwan runtime lets the consumer edit. It
// writes the shared principals row directly — the same table the Resolver
// creates at login — and never touches identity links, roles, or status.
type ProfileStore struct {
	database *sql.DB
}

func NewProfileStore(database *sql.DB) (*ProfileStore, error) {
	if database == nil {
		return nil, ErrInvalidResolver
	}
	return &ProfileStore{database: database}, nil
}

// ReadConsumerIdentity returns the nickname and avatar URL of one active
// Principal; suspended or deleted accounts are unavailable.
func (store *ProfileStore) ReadConsumerIdentity(
	ctx context.Context,
	principalID uuid.UUID,
) (nickname string, avatarURL string, err error) {
	if store == nil || store.database == nil || ctx == nil || principalID == uuid.Nil {
		return "", "", ErrInvalidResolver
	}
	err = store.database.QueryRowContext(ctx, `
SELECT nickname, avatar_url
FROM principals
WHERE id = $1 AND status = 'active' AND deleted_at IS NULL
`, principalID).Scan(&nickname, &avatarURL)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrPrincipalUnavailable
	}
	if err != nil {
		return "", "", fmt.Errorf("read xiangwan Principal identity: %w", err)
	}
	return nickname, avatarURL, nil
}

// UpdateNickname atomically replaces one active Principal's nickname and
// returns the refreshed identity projection for the response.
func (store *ProfileStore) UpdateNickname(
	ctx context.Context,
	principalID uuid.UUID,
	nickname string,
	expectedETag string,
) (string, string, string, error) {
	if store == nil || store.database == nil || ctx == nil || principalID == uuid.Nil {
		return "", "", "", ErrInvalidResolver
	}
	if expectedETag == "" {
		return "", "", "", ErrProfileETagConflict
	}
	tx, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return "", "", "", fmt.Errorf("begin xiangwan Principal nickname update: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	var currentNickname, avatarURL string
	var avatarFileID uuid.NullUUID
	err = tx.QueryRowContext(ctx, `
SELECT nickname, avatar_url, avatar_file_id
FROM principals
WHERE id = $1 AND status = 'active' AND deleted_at IS NULL
FOR UPDATE
`, principalID).Scan(&currentNickname, &avatarURL, &avatarFileID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", "", ErrPrincipalUnavailable
	}
	if err != nil {
		return "", "", "", fmt.Errorf("lock xiangwan Principal nickname: %w", err)
	}
	var currentFileID *uuid.UUID
	if avatarFileID.Valid {
		value := avatarFileID.UUID
		currentFileID = &value
	}
	currentETag, etagErr := consumerprofile.PrincipalProfileETag(
		principalID, currentNickname, avatarURL, currentFileID,
	)
	if etagErr != nil || currentETag != expectedETag {
		return "", "", "", ErrProfileETagConflict
	}
	var updatedNickname string
	err = tx.QueryRowContext(ctx, `
UPDATE principals
SET nickname = $2, updated_at = clock_timestamp()
WHERE id = $1 AND status = 'active' AND deleted_at IS NULL
RETURNING nickname, avatar_url
	`, principalID, nickname).Scan(&updatedNickname, &avatarURL)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", "", ErrPrincipalUnavailable
	}
	if err != nil {
		return "", "", "", fmt.Errorf("update xiangwan Principal nickname: %w", err)
	}
	updatedETag, etagErr := consumerprofile.PrincipalProfileETag(
		principalID, updatedNickname, avatarURL, currentFileID,
	)
	if etagErr != nil {
		return "", "", "", fmt.Errorf("build xiangwan Principal profile etag: %w", etagErr)
	}
	if err := tx.Commit(); err != nil {
		return "", "", "", fmt.Errorf("commit xiangwan Principal nickname update: %w", err)
	}
	committed = true
	return updatedNickname, avatarURL, updatedETag, nil
}

// UpdateAvatarURL atomically replaces one active Principal's avatar and
// returns the replaced value so the caller can best-effort remove the
// previous object. The central-files backpointer is cleared: the new avatar
// is served from the Xiangwan-local store, not from a platform file.
func (store *ProfileStore) UpdateAvatarURL(
	ctx context.Context,
	principalID uuid.UUID,
	avatarURL string,
) (previous string, err error) {
	if store == nil || store.database == nil || ctx == nil ||
		principalID == uuid.Nil || avatarURL == "" {
		return "", ErrInvalidResolver
	}
	tx, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin xiangwan Principal avatar update: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	err = tx.QueryRowContext(ctx, `
SELECT avatar_url
FROM principals
WHERE id = $1 AND status = 'active' AND deleted_at IS NULL
FOR UPDATE
`, principalID).Scan(&previous)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrPrincipalUnavailable
	}
	if err != nil {
		return "", fmt.Errorf("lock xiangwan Principal avatar: %w", err)
	}
	result, err := tx.ExecContext(ctx, `
UPDATE principals
SET avatar_url = $2, avatar_file_id = NULL, updated_at = clock_timestamp()
WHERE id = $1
`, principalID, avatarURL)
	if err != nil {
		return "", fmt.Errorf("update xiangwan Principal avatar: %w", err)
	}
	if rows, rowsErr := result.RowsAffected(); rowsErr != nil || rows != 1 {
		return "", ErrIdentityTransactionConflict
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit xiangwan Principal avatar update: %w", err)
	}
	committed = true
	return previous, nil
}
