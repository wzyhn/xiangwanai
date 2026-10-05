package datarightspostgres

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/datarights"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestWriterSubmitsCaseAndInitialEventAtomically(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 26, 1, 2, 3, 456000000, time.UTC)
	submission := validSubmission()
	tx := &fakeSubmissionTransaction{findErr: ErrDataRightsOperationNotFound}
	starter := &fakeSubmissionTransactionStarter{tx: tx}
	writer := &Writer{transactions: starter, now: func() time.Time { return now }}
	result, err := writer.Submit(context.Background(), submission)
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if result.Replayed || result.Case.ID == uuid.Nil ||
		result.Case.TenantID != submission.TenantID ||
		result.Case.PrincipalID != submission.PrincipalID ||
		result.Case.RequestType != submission.RequestType ||
		result.Case.RequestScope != submission.RequestScope ||
		result.Case.PrivacyPolicyVersion != submission.PrivacyPolicyVersion ||
		result.Case.Status != datarights.CaseStatusSubmitted ||
		result.Case.Version != 1 ||
		!result.Case.SubmittedAt.Equal(now.Truncate(time.Microsecond)) {
		t.Fatalf("Submit() = %+v", result)
	}
	if starter.options == nil ||
		starter.options.Isolation != sql.LevelSerializable ||
		tx.principalID != submission.PrincipalID || tx.insertCaseCalls != 1 ||
		tx.insertEventCalls != 1 || tx.commitCalls != 1 || tx.rollbackCalls != 0 {
		t.Fatalf("starter=%+v tx=%+v", starter, tx)
	}
	if tx.event.ID == uuid.Nil || tx.event.CaseID != result.Case.ID ||
		tx.event.ActorPrincipalID == nil ||
		*tx.event.ActorPrincipalID != submission.PrincipalID ||
		tx.event.EventType != datarights.EventTypeSubmitted ||
		tx.event.PolicyBasisVersion != submission.PrivacyPolicyVersion {
		t.Fatalf("submission event = %+v", tx.event)
	}
}

func TestWriterReplaysMatchingOperationAndRejectsChangedIntent(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC().Truncate(time.Microsecond)
	submission := validSubmission()
	existing := caseForSubmission(t, submission, now)

	replayTx := &fakeSubmissionTransaction{existing: existing}
	replayWriter := &Writer{
		transactions: &fakeSubmissionTransactionStarter{tx: replayTx},
		now:          time.Now,
	}
	result, err := replayWriter.Submit(context.Background(), submission)
	if err != nil || !result.Replayed || result.Case.ID != existing.ID ||
		replayTx.insertCaseCalls != 0 || replayTx.insertEventCalls != 0 ||
		replayTx.commitCalls != 1 {
		t.Fatalf("Submit(replay) = %+v, %v, tx=%+v", result, err, replayTx)
	}

	changed := submission
	changed.RequestType = datarights.RequestTypeDeletion
	conflictTx := &fakeSubmissionTransaction{existing: existing}
	conflictWriter := &Writer{
		transactions: &fakeSubmissionTransactionStarter{tx: conflictTx},
		now:          time.Now,
	}
	if _, err := conflictWriter.Submit(context.Background(), changed); !errors.Is(
		err,
		ErrDataRightsOperationConflict,
	) || conflictTx.commitCalls != 0 || conflictTx.rollbackCalls != 1 {
		t.Fatalf("Submit(conflict) error=%v tx=%+v", err, conflictTx)
	}
}

func TestWriterValidatesAndRollsBackFailedEvent(t *testing.T) {
	t.Parallel()

	writer := &Writer{
		transactions: &fakeSubmissionTransactionStarter{},
		now:          time.Now,
	}
	if _, err := writer.Submit(context.Background(), datarights.Submission{}); !errors.Is(
		err,
		ErrInvalidSubmissionCommand,
	) {
		t.Fatalf("Submit(invalid) error = %v", err)
	}

	wantErr := errors.New("event insert failed")
	tx := &fakeSubmissionTransaction{
		findErr:  ErrDataRightsOperationNotFound,
		eventErr: wantErr,
	}
	writer = &Writer{
		transactions: &fakeSubmissionTransactionStarter{tx: tx},
		now:          time.Now,
	}
	if _, err := writer.Submit(
		context.Background(),
		validSubmission(),
	); !errors.Is(err, wantErr) || tx.commitCalls != 0 || tx.rollbackCalls != 1 {
		t.Fatalf("Submit(event failure) error=%v tx=%+v", err, tx)
	}
	classified := classifyDataRightsWriteError(&pgconn.PgError{Code: "40001"})
	if !errors.Is(classified, ErrDataRightsTransactionConflict) {
		t.Fatalf("classifyDataRightsWriteError() = %v", classified)
	}
}

func validSubmission() datarights.Submission {
	return datarights.Submission{
		TenantID:             uuid.New(),
		PrincipalID:          uuid.New(),
		OperationKey:         uuid.New(),
		RequestType:          datarights.RequestTypeExport,
		RequestScope:         datarights.RequestScopeAll,
		PrivacyPolicyVersion: "privacy-v3",
	}
}

func caseForSubmission(
	t *testing.T,
	submission datarights.Submission,
	now time.Time,
) datarights.Case {
	t.Helper()
	fingerprint, err := datarights.SubmissionFingerprint(submission)
	if err != nil {
		t.Fatalf("SubmissionFingerprint() error = %v", err)
	}
	return datarights.Case{
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
		SubmittedAt:          now,
		UpdatedAt:            now,
	}
}

type fakeSubmissionTransactionStarter struct {
	tx      submissionTransaction
	err     error
	options *sql.TxOptions
}

func (starter *fakeSubmissionTransactionStarter) beginSubmissionTx(
	_ context.Context,
	options *sql.TxOptions,
) (submissionTransaction, error) {
	starter.options = options
	return starter.tx, starter.err
}

type fakeSubmissionTransaction struct {
	existing  datarights.Case
	findErr   error
	caseErr   error
	eventErr  error
	commitErr error

	principalID      uuid.UUID
	insertedCase     datarights.Case
	event            datarights.CaseEvent
	insertCaseCalls  int
	insertEventCalls int
	commitCalls      int
	rollbackCalls    int
}

func (tx *fakeSubmissionTransaction) lockActivePrincipal(
	_ context.Context,
	principalID uuid.UUID,
) error {
	tx.principalID = principalID
	return nil
}

func (tx *fakeSubmissionTransaction) findByOperation(
	_ context.Context,
	_ uuid.UUID,
	_ uuid.UUID,
	_ uuid.UUID,
) (datarights.Case, error) {
	return tx.existing, tx.findErr
}

func (tx *fakeSubmissionTransaction) insertCase(
	_ context.Context,
	value datarights.Case,
) (datarights.Case, error) {
	tx.insertCaseCalls++
	tx.insertedCase = value
	return value, tx.caseErr
}

func (tx *fakeSubmissionTransaction) insertEvent(
	_ context.Context,
	value datarights.CaseEvent,
) error {
	tx.insertEventCalls++
	tx.event = value
	return tx.eventErr
}

func (tx *fakeSubmissionTransaction) Commit() error {
	tx.commitCalls++
	return tx.commitErr
}

func (tx *fakeSubmissionTransaction) Rollback() error {
	tx.rollbackCalls++
	return nil
}
