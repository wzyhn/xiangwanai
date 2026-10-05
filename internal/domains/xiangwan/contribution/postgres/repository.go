package contributionpostgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/contribution"
	"github.com/google/uuid"
)

var (
	ErrEntryNotFound = errors.New(`xiangwan Contribution entry not found`)
	ErrEntryExists   = errors.New(`xiangwan Contribution entry already exists`)
)

type DBTX interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type Repository struct {
	db queryExecutor
}

type PendingSource struct {
	TenantID  uuid.UUID
	CheckinID uuid.UUID
}

func NewRepository(db DBTX) *Repository {
	return &Repository{db: sqlQueryExecutor{db: db}}
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
	arguments ...any,
) (rowsScanner, error) {
	return executor.db.QueryContext(ctx, query, arguments...)
}

func (executor sqlQueryExecutor) queryRowContext(
	ctx context.Context,
	query string,
	arguments ...any,
) rowScanner {
	return executor.db.QueryRowContext(ctx, query, arguments...)
}

func (repository *Repository) Create(
	ctx context.Context,
	value contribution.Entry,
) (contribution.Entry, error) {
	if repository == nil || repository.db == nil ||
		contribution.Validate(value) != nil {
		return contribution.Entry{}, contribution.ErrInvalidEntry
	}
	row := repository.db.queryRowContext(ctx, `
INSERT INTO xiangwan_contribution_entries (
    id, tenant_id, people_profile_id, people_binding_id, principal_id,
    series_id, instance_id, registration_id, session_id,
    checkin_id, checkin_event_id, role_binding_id,
    contribution_type, entry_kind, units, reversal_of_entry_id,
    occurred_at, recorded_at
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9,
    $10, $11, $12,
    $13, $14, $15, $16,
    $17, $18
)
ON CONFLICT DO NOTHING
RETURNING
    id, tenant_id, people_profile_id, people_binding_id, principal_id,
    series_id, instance_id, registration_id, session_id,
    checkin_id, checkin_event_id, role_binding_id,
    contribution_type, entry_kind, units, reversal_of_entry_id,
    occurred_at, recorded_at
`,
		value.ID,
		value.TenantID,
		value.PeopleProfileID,
		value.PeopleBindingID,
		value.PrincipalID,
		value.SeriesID,
		value.InstanceID,
		value.RegistrationID,
		value.SessionID,
		value.CheckinID,
		value.CheckinEventID,
		value.RoleBindingID,
		value.ContributionType,
		value.EntryKind,
		value.Units,
		value.ReversalOfEntryID,
		value.OccurredAt,
		value.RecordedAt,
	)
	created, err := scanEntry(row)
	if errors.Is(err, sql.ErrNoRows) {
		return contribution.Entry{}, ErrEntryExists
	}
	if err != nil {
		return contribution.Entry{}, fmt.Errorf(
			`create xiangwan Contribution entry: %w`,
			err,
		)
	}
	return created, nil
}

func (repository *Repository) GetEarnedForUpdate(
	ctx context.Context,
	tenantID uuid.UUID,
	principalID uuid.UUID,
	instanceID uuid.UUID,
	contributionType contribution.Type,
) (contribution.Entry, error) {
	return repository.getOne(ctx, `
SELECT
    id, tenant_id, people_profile_id, people_binding_id, principal_id,
    series_id, instance_id, registration_id, session_id,
    checkin_id, checkin_event_id, role_binding_id,
    contribution_type, entry_kind, units, reversal_of_entry_id,
    occurred_at, recorded_at
FROM xiangwan_contribution_entries
WHERE tenant_id = $1
  AND principal_id = $2
  AND instance_id = $3
  AND contribution_type = $4
  AND entry_kind = 'earned'
FOR UPDATE
`, tenantID, principalID, instanceID, contributionType)
}

func (repository *Repository) GetReversalForUpdate(
	ctx context.Context,
	tenantID uuid.UUID,
	earnedEntryID uuid.UUID,
) (contribution.Entry, error) {
	return repository.getOne(ctx, `
SELECT
    id, tenant_id, people_profile_id, people_binding_id, principal_id,
    series_id, instance_id, registration_id, session_id,
    checkin_id, checkin_event_id, role_binding_id,
    contribution_type, entry_kind, units, reversal_of_entry_id,
    occurred_at, recorded_at
FROM xiangwan_contribution_entries
WHERE tenant_id = $1
  AND reversal_of_entry_id = $2
  AND entry_kind = 'reversed'
FOR UPDATE
`, tenantID, earnedEntryID)
}

func (repository *Repository) ListByPrincipal(
	ctx context.Context,
	tenantID uuid.UUID,
	principalID uuid.UUID,
) ([]contribution.Entry, error) {
	if repository == nil || repository.db == nil ||
		tenantID == uuid.Nil || principalID == uuid.Nil {
		return nil, contribution.ErrInvalidLedger
	}
	rows, err := repository.db.queryContext(ctx, `
SELECT
    id, tenant_id, people_profile_id, people_binding_id, principal_id,
    series_id, instance_id, registration_id, session_id,
    checkin_id, checkin_event_id, role_binding_id,
    contribution_type, entry_kind, units, reversal_of_entry_id,
    occurred_at, recorded_at
FROM xiangwan_contribution_entries
WHERE tenant_id = $1
  AND principal_id = $2
ORDER BY occurred_at DESC, id DESC
`, tenantID, principalID)
	if err != nil {
		return nil, fmt.Errorf(`list xiangwan Contribution history: %w`, err)
	}
	defer rows.Close()

	result := make([]contribution.Entry, 0)
	for rows.Next() {
		entry, scanErr := scanEntry(rows)
		if scanErr != nil {
			return nil, fmt.Errorf(
				`scan xiangwan Contribution history: %w`,
				scanErr,
			)
		}
		result = append(result, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(
			`iterate xiangwan Contribution history: %w`,
			err,
		)
	}
	return result, nil
}

// ListPendingSources derives retryable work from immutable Checkin events and
// ledger absence. No volatile queue or Redis cursor is required.
func (repository *Repository) ListPendingSources(
	ctx context.Context,
	tenantID uuid.UUID,
	limit int,
) ([]PendingSource, error) {
	if repository == nil || repository.db == nil ||
		tenantID == uuid.Nil || limit < 1 || limit > 500 {
		return nil, ErrInvalidReconcileCommand
	}
	rows, err := repository.db.queryContext(ctx, `
SELECT current_checkin.tenant_id, current_checkin.id
FROM xiangwan_checkins AS current_checkin
JOIN xiangwan_checkin_events AS checked_in_event
  ON checked_in_event.tenant_id = current_checkin.tenant_id
 AND checked_in_event.checkin_id = current_checkin.id
 AND checked_in_event.event_type = 'checked_in'
 AND checked_in_event.event_sequence = 1
JOIN xiangwan_people_bindings AS people_binding
  ON people_binding.tenant_id = current_checkin.tenant_id
 AND people_binding.principal_id = current_checkin.principal_id
 AND people_binding.bound_at <= checked_in_event.occurred_at
 AND (
     people_binding.revoked_at IS NULL
     OR people_binding.revoked_at > checked_in_event.occurred_at
 )
JOIN xiangwan_instance_role_bindings AS role_binding
  ON role_binding.tenant_id = current_checkin.tenant_id
 AND role_binding.series_id = current_checkin.series_id
 AND role_binding.instance_id = current_checkin.instance_id
 AND role_binding.principal_id = current_checkin.principal_id
 AND role_binding.role_code = 'host'
 AND role_binding.granted_at <= checked_in_event.occurred_at
 AND (
     role_binding.revoked_at IS NULL
     OR role_binding.revoked_at > checked_in_event.occurred_at
 )
LEFT JOIN xiangwan_contribution_entries AS earned_entry
  ON earned_entry.tenant_id = current_checkin.tenant_id
 AND earned_entry.principal_id = current_checkin.principal_id
 AND earned_entry.instance_id = current_checkin.instance_id
 AND earned_entry.contribution_type = 'host_checkin'
 AND earned_entry.entry_kind = 'earned'
LEFT JOIN xiangwan_contribution_entries AS reversal_entry
  ON reversal_entry.tenant_id = earned_entry.tenant_id
 AND reversal_entry.reversal_of_entry_id = earned_entry.id
 AND reversal_entry.entry_kind = 'reversed'
WHERE current_checkin.tenant_id = $1
  AND (
      earned_entry.id IS NULL
      OR (
          current_checkin.checkin_status = 'revoked'
          AND earned_entry.checkin_id = current_checkin.id
          AND reversal_entry.id IS NULL
      )
  )
GROUP BY
    current_checkin.tenant_id,
    current_checkin.id,
    checked_in_event.occurred_at
ORDER BY checked_in_event.occurred_at, current_checkin.id
LIMIT $2
`, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf(
			`list pending xiangwan Contribution sources: %w`,
			err,
		)
	}
	defer rows.Close()

	result := make([]PendingSource, 0)
	for rows.Next() {
		var source PendingSource
		if err := rows.Scan(&source.TenantID, &source.CheckinID); err != nil {
			return nil, fmt.Errorf(
				`scan pending xiangwan Contribution source: %w`,
				err,
			)
		}
		if source.TenantID != tenantID || source.CheckinID == uuid.Nil {
			return nil, ErrContributionFactsConflict
		}
		result = append(result, source)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(
			`iterate pending xiangwan Contribution sources: %w`,
			err,
		)
	}
	return result, nil
}

func (repository *Repository) getOne(
	ctx context.Context,
	query string,
	arguments ...any,
) (contribution.Entry, error) {
	if repository == nil || repository.db == nil {
		return contribution.Entry{}, contribution.ErrInvalidLedger
	}
	value, err := scanEntry(
		repository.db.queryRowContext(ctx, query, arguments...),
	)
	if errors.Is(err, sql.ErrNoRows) {
		return contribution.Entry{}, ErrEntryNotFound
	}
	if err != nil {
		return contribution.Entry{}, fmt.Errorf(
			`get xiangwan Contribution entry: %w`,
			err,
		)
	}
	return value, nil
}

type rowScanner interface {
	Scan(...any) error
}

func scanEntry(row rowScanner) (contribution.Entry, error) {
	var value contribution.Entry
	var reversalOf uuid.NullUUID
	err := row.Scan(
		&value.ID,
		&value.TenantID,
		&value.PeopleProfileID,
		&value.PeopleBindingID,
		&value.PrincipalID,
		&value.SeriesID,
		&value.InstanceID,
		&value.RegistrationID,
		&value.SessionID,
		&value.CheckinID,
		&value.CheckinEventID,
		&value.RoleBindingID,
		&value.ContributionType,
		&value.EntryKind,
		&value.Units,
		&reversalOf,
		&value.OccurredAt,
		&value.RecordedAt,
	)
	if err != nil {
		return contribution.Entry{}, err
	}
	if reversalOf.Valid {
		reversalID := reversalOf.UUID
		value.ReversalOfEntryID = &reversalID
	}
	if err := contribution.Validate(value); err != nil {
		return contribution.Entry{}, err
	}
	return value, nil
}
