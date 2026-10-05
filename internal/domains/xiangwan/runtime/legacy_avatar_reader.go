package xiangwanruntime

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/wzyhn/xiangwanai/internal/capabilities/storage/publicobject"
	"github.com/google/uuid"
)

var errLegacyConsumerAvatarUnavailable = errors.New(
	"xiangwan legacy consumer avatar is unavailable",
)

// legacyConsumerAvatarReader is the Xiangwan compatibility bridge for
// platform Auth avatar URLs. It never treats a file UUID as a bearer grant:
// the live Principal row must point at the same file, and the storage reader
// must then confirm the file state and read the exact object from disk.
type legacyConsumerAvatarReader struct {
	database *sql.DB
	objects  publicobject.Reader
}

func newLegacyConsumerAvatarReader(
	database *sql.DB,
	objects publicobject.Reader,
) *legacyConsumerAvatarReader {
	return &legacyConsumerAvatarReader{database: database, objects: objects}
}

func (reader *legacyConsumerAvatarReader) Open(
	ctx context.Context,
	principalID, fileID uuid.UUID,
) (*publicobject.Object, error) {
	if reader == nil || reader.database == nil || reader.objects == nil ||
		ctx == nil || principalID == uuid.Nil || fileID == uuid.Nil {
		return nil, errLegacyConsumerAvatarUnavailable
	}
	var bound bool
	err := reader.database.QueryRowContext(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM principals AS principal
    JOIN files AS file
      ON file.id = principal.avatar_file_id
     AND file.principal_id = principal.id
    WHERE principal.id = $1
      AND principal.status = 'active'
      AND principal.deleted_at IS NULL
      AND principal.avatar_file_id = $2
)
`, principalID, fileID).Scan(&bound)
	if err != nil {
		return nil, fmt.Errorf("check legacy consumer avatar binding: %w", err)
	}
	if !bound {
		return nil, sql.ErrNoRows
	}
	object, err := reader.objects.OpenConfirmedObject(ctx, fileID)
	if err != nil {
		return nil, fmt.Errorf("open legacy consumer avatar object: %w", err)
	}
	return object, nil
}
