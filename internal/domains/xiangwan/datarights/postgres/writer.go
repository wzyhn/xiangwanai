// Package datarightspostgres persists Xiangwan personal-data rights facts in
// the independently deployed customer PostgreSQL database.
package datarightspostgres

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/datarights"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrInvalidSubmissionCommand = errors.New(
		"invalid xiangwan data-rights submission command",
	)
	ErrDataRightsPrincipalUnavailable = errors.New(
		"xiangwan data-rights principal unavailable",
	)
	ErrDataRightsOperationNotFound = errors.New(
		"xiangwan data-rights operation not found",
	)
	ErrDataRightsOperationConflict = errors.New(
		"xiangwan data-rights operation conflict",
	)
	ErrDataRightsTransactionConflict = errors.New(
		"xiangwan data-rights transaction conflict",
	)
)

type SubmissionResult struct {
	Case     datarights.Case
	Replayed bool
}

type Writer struct {
	transactions submissionTransactionStarter
	now          func() time.Time
}

func NewWriter(db *sql.DB) *Writer {
	return &Writer{
		transactions: sqlSubmissionTransactionStarter{db: db},
		now:          time.Now,
	}
}

func (writer *Writer) Submit(
	ctx context.Context,
	submission datarights.Submission,
) (SubmissionResult, error) {
	if writer == nil || writer.transactions == nil || writer.now == nil ||
		ctx == nil {
		return SubmissionResult{}, ErrInvalidSubmissionCommand
	}
	if err := datarights.ValidateSubmission(submission); err != nil {
		return SubmissionResult{}, fmt.Errorf(
			"%w: %v",
			ErrInvalidSubmissionCommand,
			err,
		)
	}
	fingerprint, err := datarights.SubmissionFingerprint(submission)
	if err != nil {
		return SubmissionResult{}, fmt.Errorf(
			"%w: %v",
			ErrInvalidSubmissionCommand,
			err,
		)
	}
	tx, err := writer.transactions.beginSubmissionTx(
		ctx,
		&sql.TxOptions{Isolation: sql.LevelSerializable},
	)
	if err != nil {
		return SubmissionResult{}, fmt.Errorf(
			"begin xiangwan data-rights submission: %w",
			err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if err := tx.lockActivePrincipal(ctx, submission.PrincipalID); err != nil {
		return SubmissionResult{}, err
	}
	existing, err := tx.findByOperation(
		ctx,
		submission.TenantID,
		submission.PrincipalID,
		submission.OperationKey,
	)
	switch {
	case err == nil:
		if err := datarights.ValidateCase(existing); err != nil {
			return SubmissionResult{}, ErrDataRightsTransactionConflict
		}
		if subtle.ConstantTimeCompare(
			existing.RequestFingerprint[:],
			fingerprint[:],
		) != 1 {
			return SubmissionResult{}, ErrDataRightsOperationConflict
		}
		if err := tx.Commit(); err != nil {
			return SubmissionResult{}, classifyDataRightsWriteError(err)
		}
		committed = true
		return SubmissionResult{Case: existing, Replayed: true}, nil
	case !errors.Is(err, ErrDataRightsOperationNotFound):
		return SubmissionResult{}, err
	}

	occurredAt := writer.now().UTC().Truncate(time.Microsecond)
	created := datarights.Case{
		ID:                   uuid.New(),
		TenantID:             submission.TenantID,
		PrincipalID:          submission.PrincipalID,
		OperationKey:         submission.OperationKey,
		RequestFingerprint:   fingerprint,
		RequestType:          submission.RequestType,
		RequestScope:         submission.RequestScope,
		PrivacyPolicyVersion: submission.PrivacyPolicyVersion,
		Status:               datarights.CaseStatusSubmitted,
		Version:              1,
		SubmittedAt:          occurredAt,
		UpdatedAt:            occurredAt,
	}
	if err := datarights.ValidateCase(created); err != nil {
		return SubmissionResult{}, ErrDataRightsTransactionConflict
	}
	created, err = tx.insertCase(ctx, created)
	if err != nil {
		return SubmissionResult{}, classifyDataRightsWriteError(err)
	}
	actorID := submission.PrincipalID
	event := datarights.CaseEvent{
		ID:                 uuid.New(),
		TenantID:           submission.TenantID,
		CaseID:             created.ID,
		CaseVersion:        1,
		EventType:          datarights.EventTypeSubmitted,
		ResultingStatus:    datarights.CaseStatusSubmitted,
		ActorPrincipalID:   &actorID,
		PolicyBasisVersion: submission.PrivacyPolicyVersion,
		OccurredAt:         occurredAt,
	}
	if err := datarights.ValidateCaseEvent(event); err != nil {
		return SubmissionResult{}, ErrDataRightsTransactionConflict
	}
	if err := tx.insertEvent(ctx, event); err != nil {
		return SubmissionResult{}, classifyDataRightsWriteError(err)
	}
	if err := tx.Commit(); err != nil {
		return SubmissionResult{}, classifyDataRightsWriteError(err)
	}
	committed = true
	return SubmissionResult{Case: created}, nil
}

type submissionTransactionStarter interface {
	beginSubmissionTx(
		context.Context,
		*sql.TxOptions,
	) (submissionTransaction, error)
}

type submissionTransaction interface {
	lockActivePrincipal(context.Context, uuid.UUID) error
	findByOperation(
		context.Context,
		uuid.UUID,
		uuid.UUID,
		uuid.UUID,
	) (datarights.Case, error)
	insertCase(context.Context, datarights.Case) (datarights.Case, error)
	insertEvent(context.Context, datarights.CaseEvent) error
	Commit() error
	Rollback() error
}

type sqlSubmissionTransactionStarter struct {
	db *sql.DB
}

func (starter sqlSubmissionTransactionStarter) beginSubmissionTx(
	ctx context.Context,
	options *sql.TxOptions,
) (submissionTransaction, error) {
	if starter.db == nil {
		return nil, ErrInvalidSubmissionCommand
	}
	tx, err := starter.db.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return &sqlSubmissionTransaction{tx: tx}, nil
}

type sqlSubmissionTransaction struct {
	tx *sql.Tx
}

func (tx *sqlSubmissionTransaction) lockActivePrincipal(
	ctx context.Context,
	principalID uuid.UUID,
) error {
	var lockedID uuid.UUID
	err := tx.tx.QueryRowContext(ctx, `
SELECT id
FROM principals
WHERE id = $1
  AND status = 'active'
  AND deleted_at IS NULL
FOR UPDATE
`, principalID).Scan(&lockedID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrDataRightsPrincipalUnavailable
	}
	if err != nil {
		return fmt.Errorf("lock xiangwan data-rights Principal: %w", err)
	}
	if lockedID != principalID {
		return ErrDataRightsTransactionConflict
	}
	return nil
}

func (tx *sqlSubmissionTransaction) findByOperation(
	ctx context.Context,
	tenantID uuid.UUID,
	principalID uuid.UUID,
	operationKey uuid.UUID,
) (datarights.Case, error) {
	value, err := scanCase(tx.tx.QueryRowContext(ctx, `
SELECT
    id, tenant_id, principal_id, operation_key, request_fingerprint,
    request_type, request_scope, privacy_policy_version, status, version,
    submitted_at, updated_at, completed_at
FROM xiangwan_data_rights_cases
WHERE tenant_id = $1 AND principal_id = $2 AND operation_key = $3
FOR UPDATE
`, tenantID, principalID, operationKey))
	if errors.Is(err, sql.ErrNoRows) {
		return datarights.Case{}, ErrDataRightsOperationNotFound
	}
	if err != nil {
		return datarights.Case{}, fmt.Errorf(
			"read xiangwan data-rights operation: %w",
			err,
		)
	}
	return value, nil
}

func (tx *sqlSubmissionTransaction) insertCase(
	ctx context.Context,
	value datarights.Case,
) (datarights.Case, error) {
	created, err := scanCase(tx.tx.QueryRowContext(ctx, `
INSERT INTO xiangwan_data_rights_cases (
    id, tenant_id, principal_id, operation_key, request_fingerprint,
    request_type, request_scope, privacy_policy_version, status, version,
    submitted_at, updated_at, completed_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
RETURNING
    id, tenant_id, principal_id, operation_key, request_fingerprint,
    request_type, request_scope, privacy_policy_version, status, version,
    submitted_at, updated_at, completed_at
`,
		value.ID,
		value.TenantID,
		value.PrincipalID,
		value.OperationKey,
		value.RequestFingerprint[:],
		value.RequestType,
		value.RequestScope,
		value.PrivacyPolicyVersion,
		value.Status,
		value.Version,
		value.SubmittedAt,
		value.UpdatedAt,
		value.CompletedAt,
	))
	if err != nil {
		return datarights.Case{}, fmt.Errorf(
			"create xiangwan data-rights case: %w",
			err,
		)
	}
	return created, nil
}

func (tx *sqlSubmissionTransaction) insertEvent(
	ctx context.Context,
	value datarights.CaseEvent,
) error {
	result, err := tx.tx.ExecContext(ctx, `
INSERT INTO xiangwan_data_rights_case_events (
    id, tenant_id, case_id, case_version, event_type, resulting_status,
    actor_principal_id, policy_basis_version, delivery_kind,
    delivery_status, evidence_digest, occurred_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
`,
		value.ID,
		value.TenantID,
		value.CaseID,
		value.CaseVersion,
		value.EventType,
		value.ResultingStatus,
		value.ActorPrincipalID,
		nullableString(value.PolicyBasisVersion),
		nullableString(string(value.DeliveryKind)),
		nullableString(string(value.DeliveryStatus)),
		nullableBytes(value.EvidenceDigest),
		value.OccurredAt,
	)
	if err != nil {
		return fmt.Errorf("append xiangwan data-rights event: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return ErrDataRightsTransactionConflict
	}
	return nil
}

func (tx *sqlSubmissionTransaction) Commit() error {
	return tx.tx.Commit()
}

func (tx *sqlSubmissionTransaction) Rollback() error {
	return tx.tx.Rollback()
}

type rowScanner interface {
	Scan(...any) error
}

func scanCase(row rowScanner) (datarights.Case, error) {
	var value datarights.Case
	var fingerprint []byte
	var completedAt sql.NullTime
	err := row.Scan(
		&value.ID,
		&value.TenantID,
		&value.PrincipalID,
		&value.OperationKey,
		&fingerprint,
		&value.RequestType,
		&value.RequestScope,
		&value.PrivacyPolicyVersion,
		&value.Status,
		&value.Version,
		&value.SubmittedAt,
		&value.UpdatedAt,
		&completedAt,
	)
	if err != nil {
		return datarights.Case{}, err
	}
	if len(fingerprint) != len(value.RequestFingerprint) {
		return datarights.Case{}, ErrDataRightsTransactionConflict
	}
	copy(value.RequestFingerprint[:], fingerprint)
	value.SubmittedAt = value.SubmittedAt.UTC()
	value.UpdatedAt = value.UpdatedAt.UTC()
	if completedAt.Valid {
		completed := completedAt.Time.UTC()
		value.CompletedAt = &completed
	}
	if err := datarights.ValidateCase(value); err != nil {
		return datarights.Case{}, ErrDataRightsTransactionConflict
	}
	return value, nil
}

func classifyDataRightsWriteError(err error) error {
	if errors.Is(err, ErrDataRightsOperationConflict) ||
		errors.Is(err, ErrDataRightsTransactionConflict) {
		return err
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		switch postgresError.Code {
		case "23503", "23505", "23514", "40001", "40P01":
			return fmt.Errorf("%w: %v", ErrDataRightsTransactionConflict, err)
		}
	}
	return err
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableBytes(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}
