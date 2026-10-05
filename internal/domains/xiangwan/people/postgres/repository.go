// Package peoplepostgres persists Xiangwan PeopleProfile, trusted binding, and
// Instance role facts in the customer PostgreSQL database.
package peoplepostgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/people"
	"github.com/google/uuid"
)

var (
	ErrProfileNotFound        = errors.New(`xiangwan PeopleProfile not found`)
	ErrProfileVersionConflict = errors.New(
		`xiangwan PeopleProfile version conflict`,
	)
	ErrBindingNotFound        = errors.New(`xiangwan People binding not found`)
	ErrBindingVersionConflict = errors.New(
		`xiangwan People binding version conflict`,
	)
	ErrRoleBindingNotFound = errors.New(
		`xiangwan Instance role binding not found`,
	)
	ErrRoleBindingVersionConflict = errors.New(
		`xiangwan Instance role binding version conflict`,
	)
)

type DBTX interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type Repository struct {
	db queryExecutor
}

func NewRepository(db DBTX) *Repository {
	return &Repository{db: sqlQueryExecutor{db: db}}
}

type rowScanner interface {
	Scan(...any) error
}

type rowsScanner interface {
	Next() bool
	Scan(...any) error
	Err() error
	Close() error
}

type queryExecutor interface {
	queryContext(context.Context, string, ...any) (rowsScanner, error)
	queryRowContext(context.Context, string, ...any) rowScanner
}

type sqlQueryExecutor struct {
	db DBTX
}

func (executor sqlQueryExecutor) queryContext(
	ctx context.Context,
	query string,
	args ...any,
) (rowsScanner, error) {
	return executor.db.QueryContext(ctx, query, args...)
}

func (executor sqlQueryExecutor) queryRowContext(
	ctx context.Context,
	query string,
	args ...any,
) rowScanner {
	return executor.db.QueryRowContext(ctx, query, args...)
}

const profileProjection = `
    id, tenant_id, display_name, headline, introduction,
    profile_status, moderation_status, moderated_by, moderated_at,
    created_by, updated_by, version, created_at, updated_at
`

func (repository *Repository) CreateProfile(
	ctx context.Context,
	value people.Profile,
) (people.Profile, error) {
	if err := people.ValidateProfile(value); err != nil {
		return people.Profile{}, err
	}
	if value.ProfileStatus != people.ProfileStatusDraft ||
		value.ModerationStatus != people.ModerationStatusPending ||
		value.Version != 1 ||
		!value.CreatedAt.Equal(value.UpdatedAt) ||
		value.CreatedBy != value.UpdatedBy {
		return people.Profile{}, people.ErrInvalidProfile
	}
	created, err := scanProfile(repository.db.queryRowContext(ctx, `
INSERT INTO xiangwan_people_profiles (
    id, tenant_id, display_name, headline, introduction,
    profile_status, moderation_status, moderated_by, moderated_at,
    created_by, updated_by, version, created_at, updated_at
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9,
    $10, $11, $12, $13, $14
)
RETURNING`+profileProjection,
		value.ID,
		value.TenantID,
		value.DisplayName,
		value.Headline,
		value.Introduction,
		value.ProfileStatus,
		value.ModerationStatus,
		value.ModeratedBy,
		value.ModeratedAt,
		value.CreatedBy,
		value.UpdatedBy,
		value.Version,
		value.CreatedAt,
		value.UpdatedAt,
	))
	if err != nil {
		return people.Profile{}, fmt.Errorf(
			`create xiangwan PeopleProfile: %w`,
			err,
		)
	}
	return created, nil
}

func (repository *Repository) GetProfile(
	ctx context.Context,
	tenantID uuid.UUID,
	profileID uuid.UUID,
) (people.Profile, error) {
	value, err := scanProfile(repository.db.queryRowContext(ctx, `
SELECT`+profileProjection+`
FROM xiangwan_people_profiles
WHERE tenant_id = $1 AND id = $2
`, tenantID, profileID))
	if errors.Is(err, sql.ErrNoRows) {
		return people.Profile{}, ErrProfileNotFound
	}
	if err != nil {
		return people.Profile{}, fmt.Errorf(
			`get xiangwan PeopleProfile: %w`,
			err,
		)
	}
	return value, nil
}

func (repository *Repository) GetPublishedProfile(
	ctx context.Context,
	tenantID uuid.UUID,
	profileID uuid.UUID,
) (people.Profile, error) {
	value, err := scanProfile(repository.db.queryRowContext(ctx, `
SELECT`+profileProjection+`
FROM xiangwan_people_profiles
WHERE tenant_id = $1
  AND id = $2
  AND profile_status = 'published'
  AND moderation_status = 'approved'
`, tenantID, profileID))
	if errors.Is(err, sql.ErrNoRows) {
		return people.Profile{}, ErrProfileNotFound
	}
	if err != nil {
		return people.Profile{}, fmt.Errorf(
			`get published xiangwan PeopleProfile: %w`,
			err,
		)
	}
	return value, nil
}

func (repository *Repository) GetProfileForUpdate(
	ctx context.Context,
	tenantID uuid.UUID,
	profileID uuid.UUID,
) (people.Profile, error) {
	value, err := scanProfile(repository.db.queryRowContext(ctx, `
SELECT`+profileProjection+`
FROM xiangwan_people_profiles
WHERE tenant_id = $1 AND id = $2
FOR UPDATE
`, tenantID, profileID))
	if errors.Is(err, sql.ErrNoRows) {
		return people.Profile{}, ErrProfileNotFound
	}
	if err != nil {
		return people.Profile{}, fmt.Errorf(
			`lock xiangwan PeopleProfile: %w`,
			err,
		)
	}
	return value, nil
}

func (repository *Repository) UpdateProfile(
	ctx context.Context,
	value people.Profile,
	expectedVersion int64,
) (people.Profile, error) {
	if err := people.ValidateProfile(value); err != nil {
		return people.Profile{}, err
	}
	if expectedVersion < 1 || value.Version != expectedVersion+1 {
		return people.Profile{}, people.ErrInvalidProfile
	}
	updated, err := scanProfile(repository.db.queryRowContext(ctx, `
UPDATE xiangwan_people_profiles
SET display_name = $3,
    headline = $4,
    introduction = $5,
    profile_status = $6,
    moderation_status = $7,
    moderated_by = $8,
    moderated_at = $9,
    updated_by = $10,
    version = version + 1,
    updated_at = $11
WHERE tenant_id = $1
  AND id = $2
  AND version = $12
RETURNING`+profileProjection,
		value.TenantID,
		value.ID,
		value.DisplayName,
		value.Headline,
		value.Introduction,
		value.ProfileStatus,
		value.ModerationStatus,
		value.ModeratedBy,
		value.ModeratedAt,
		value.UpdatedBy,
		value.UpdatedAt,
		expectedVersion,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return people.Profile{}, ErrProfileVersionConflict
	}
	if err != nil {
		return people.Profile{}, fmt.Errorf(
			`update xiangwan PeopleProfile: %w`,
			err,
		)
	}
	return updated, nil
}

func scanProfile(row rowScanner) (people.Profile, error) {
	var value people.Profile
	var headline sql.NullString
	var moderatedBy uuid.NullUUID
	var moderatedAt sql.NullTime
	err := row.Scan(
		&value.ID,
		&value.TenantID,
		&value.DisplayName,
		&headline,
		&value.Introduction,
		&value.ProfileStatus,
		&value.ModerationStatus,
		&moderatedBy,
		&moderatedAt,
		&value.CreatedBy,
		&value.UpdatedBy,
		&value.Version,
		&value.CreatedAt,
		&value.UpdatedAt,
	)
	if err != nil {
		return people.Profile{}, err
	}
	value.Headline = nullStringPointer(headline)
	value.ModeratedBy = nullUUIDPointer(moderatedBy)
	value.ModeratedAt = nullTimePointer(moderatedAt)
	if err := people.ValidateProfile(value); err != nil {
		return people.Profile{}, err
	}
	return value, nil
}

func nullUUIDPointer(value uuid.NullUUID) *uuid.UUID {
	if !value.Valid {
		return nil
	}
	result := value.UUID
	return &result
}

func nullTimePointer(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time
	return &result
}

func nullStringPointer(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	result := value.String
	return &result
}
