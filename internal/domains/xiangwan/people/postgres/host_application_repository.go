package peoplepostgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/people"
	"github.com/google/uuid"
)

var (
	ErrHostApplicationNotFound = errors.New(
		`xiangwan HostApplication not found`,
	)
	ErrHostApplicationVersionConflict = errors.New(
		`xiangwan HostApplication version conflict`,
	)
)

const hostApplicationProjection = `
    id, tenant_id, principal_id, application_cycle, policy_version,
    personal_introduction, relevant_experience, availability, contact_method,
    application_status, reviewed_by, reviewed_at, review_comment,
    withdrawn_by, withdrawn_at,
    version, submitted_at, created_at, updated_at
`

func (repository *Repository) CreateHostApplication(
	ctx context.Context,
	value people.HostApplication,
) (people.HostApplication, error) {
	if err := people.ValidateHostApplication(value); err != nil {
		return people.HostApplication{}, err
	}
	if value.ApplicationStatus != people.HostApplicationStatusPending ||
		value.Version != 1 {
		return people.HostApplication{}, people.ErrInvalidHostApplication
	}
	created, err := scanHostApplication(repository.db.queryRowContext(ctx, `
INSERT INTO xiangwan_host_applications (
    id, tenant_id, principal_id, application_cycle, policy_version,
    personal_introduction, relevant_experience, availability, contact_method,
    application_status, reviewed_by, reviewed_at, review_comment,
    withdrawn_by, withdrawn_at,
    version, submitted_at, created_at, updated_at
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9,
    $10, $11, $12, $13,
    $14, $15,
    $16, $17, $18, $19
)
RETURNING`+hostApplicationProjection,
		value.ID,
		value.TenantID,
		value.PrincipalID,
		value.ApplicationCycle,
		value.PolicyVersion,
		value.PersonalIntroduction,
		value.RelevantExperience,
		value.Availability,
		value.ContactMethod,
		value.ApplicationStatus,
		value.ReviewedBy,
		value.ReviewedAt,
		value.ReviewComment,
		value.WithdrawnBy,
		value.WithdrawnAt,
		value.Version,
		value.SubmittedAt,
		value.CreatedAt,
		value.UpdatedAt,
	))
	if err != nil {
		return people.HostApplication{}, fmt.Errorf(
			`create xiangwan HostApplication: %w`,
			err,
		)
	}
	return created, nil
}

func (repository *Repository) GetHostApplication(
	ctx context.Context,
	tenantID uuid.UUID,
	applicationID uuid.UUID,
) (people.HostApplication, error) {
	return repository.getHostApplication(ctx, `
SELECT`+hostApplicationProjection+`
FROM xiangwan_host_applications
WHERE tenant_id = $1 AND id = $2
`, tenantID, applicationID)
}

func (repository *Repository) GetHostApplicationForUpdate(
	ctx context.Context,
	tenantID uuid.UUID,
	applicationID uuid.UUID,
) (people.HostApplication, error) {
	return repository.getHostApplication(ctx, `
SELECT`+hostApplicationProjection+`
FROM xiangwan_host_applications
WHERE tenant_id = $1 AND id = $2
FOR UPDATE
`, tenantID, applicationID)
}

func (repository *Repository) GetHostApplicationForPrincipalForUpdate(
	ctx context.Context,
	tenantID uuid.UUID,
	applicationID uuid.UUID,
	principalID uuid.UUID,
) (people.HostApplication, error) {
	return repository.getHostApplication(ctx, `
SELECT`+hostApplicationProjection+`
FROM xiangwan_host_applications
WHERE tenant_id = $1
  AND id = $2
  AND principal_id = $3
FOR UPDATE
`, tenantID, applicationID, principalID)
}

func (repository *Repository) GetActiveHostApplicationByCycleForUpdate(
	ctx context.Context,
	tenantID uuid.UUID,
	principalID uuid.UUID,
	applicationCycle string,
) (people.HostApplication, error) {
	return repository.getHostApplication(ctx, `
SELECT`+hostApplicationProjection+`
FROM xiangwan_host_applications
WHERE tenant_id = $1
  AND principal_id = $2
  AND application_cycle = $3
  AND application_status IN ('pending', 'approved')
FOR UPDATE
`, tenantID, principalID, applicationCycle)
}

func (repository *Repository) ListHostApplicationsByPrincipal(
	ctx context.Context,
	tenantID uuid.UUID,
	principalID uuid.UUID,
) ([]people.HostApplication, error) {
	return repository.queryHostApplications(ctx, `
SELECT`+hostApplicationProjection+`
FROM xiangwan_host_applications
WHERE tenant_id = $1 AND principal_id = $2
ORDER BY submitted_at DESC, id DESC
`, tenantID, principalID)
}

func (repository *Repository) ListPendingHostApplications(
	ctx context.Context,
	tenantID uuid.UUID,
) ([]people.HostApplication, error) {
	return repository.queryHostApplications(ctx, `
SELECT`+hostApplicationProjection+`
FROM xiangwan_host_applications
WHERE tenant_id = $1 AND application_status = 'pending'
ORDER BY submitted_at ASC, id ASC
`, tenantID)
}

func (repository *Repository) UpdateHostApplication(
	ctx context.Context,
	value people.HostApplication,
	expectedVersion int64,
) (people.HostApplication, error) {
	if err := people.ValidateHostApplication(value); err != nil {
		return people.HostApplication{}, err
	}
	if value.ApplicationStatus == people.HostApplicationStatusPending ||
		expectedVersion < 1 ||
		value.Version != expectedVersion+1 {
		return people.HostApplication{}, people.ErrInvalidHostApplication
	}
	updated, err := scanHostApplication(repository.db.queryRowContext(ctx, `
UPDATE xiangwan_host_applications
SET application_status = $3,
    reviewed_by = $4,
    reviewed_at = $5,
    review_comment = $6,
    withdrawn_by = $7,
    withdrawn_at = $8,
    version = version + 1,
    updated_at = $9
WHERE tenant_id = $1
  AND id = $2
  AND version = $10
RETURNING`+hostApplicationProjection,
		value.TenantID,
		value.ID,
		value.ApplicationStatus,
		value.ReviewedBy,
		value.ReviewedAt,
		value.ReviewComment,
		value.WithdrawnBy,
		value.WithdrawnAt,
		value.UpdatedAt,
		expectedVersion,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return people.HostApplication{},
			ErrHostApplicationVersionConflict
	}
	if err != nil {
		return people.HostApplication{}, fmt.Errorf(
			`update xiangwan HostApplication: %w`,
			err,
		)
	}
	return updated, nil
}

func (repository *Repository) getHostApplication(
	ctx context.Context,
	query string,
	args ...any,
) (people.HostApplication, error) {
	value, err := scanHostApplication(
		repository.db.queryRowContext(ctx, query, args...),
	)
	if errors.Is(err, sql.ErrNoRows) {
		return people.HostApplication{}, ErrHostApplicationNotFound
	}
	if err != nil {
		return people.HostApplication{}, fmt.Errorf(
			`get xiangwan HostApplication: %w`,
			err,
		)
	}
	return value, nil
}

func (repository *Repository) queryHostApplications(
	ctx context.Context,
	query string,
	args ...any,
) ([]people.HostApplication, error) {
	rows, err := repository.db.queryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf(`list xiangwan HostApplications: %w`, err)
	}
	defer rows.Close()

	result := make([]people.HostApplication, 0)
	for rows.Next() {
		value, scanErr := scanHostApplication(rows)
		if scanErr != nil {
			return nil, fmt.Errorf(
				`scan xiangwan HostApplication: %w`,
				scanErr,
			)
		}
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(
			`iterate xiangwan HostApplications: %w`,
			err,
		)
	}
	return result, nil
}

func scanHostApplication(row rowScanner) (people.HostApplication, error) {
	var value people.HostApplication
	var reviewedBy uuid.NullUUID
	var reviewedAt sql.NullTime
	var reviewComment sql.NullString
	var withdrawnBy uuid.NullUUID
	var withdrawnAt sql.NullTime
	err := row.Scan(
		&value.ID,
		&value.TenantID,
		&value.PrincipalID,
		&value.ApplicationCycle,
		&value.PolicyVersion,
		&value.PersonalIntroduction,
		&value.RelevantExperience,
		&value.Availability,
		&value.ContactMethod,
		&value.ApplicationStatus,
		&reviewedBy,
		&reviewedAt,
		&reviewComment,
		&withdrawnBy,
		&withdrawnAt,
		&value.Version,
		&value.SubmittedAt,
		&value.CreatedAt,
		&value.UpdatedAt,
	)
	if err != nil {
		return people.HostApplication{}, err
	}
	value.ReviewedBy = nullUUIDPointer(reviewedBy)
	value.ReviewedAt = nullTimePointer(reviewedAt)
	value.ReviewComment = nullStringPointer(reviewComment)
	value.WithdrawnBy = nullUUIDPointer(withdrawnBy)
	value.WithdrawnAt = nullTimePointer(withdrawnAt)
	if err := people.ValidateHostApplication(value); err != nil {
		return people.HostApplication{}, err
	}
	return value, nil
}
