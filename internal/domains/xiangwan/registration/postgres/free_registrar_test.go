package registrationpostgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestFreeRegistrarConfirmsAndCountsAtomically(t *testing.T) {
	t.Parallel()

	command := validFreeRegistrationCommand()
	registrar, tx, capture := newFreeRegistrarHarness(t, command, freeRegistrarScenario{})

	created, err := registrar.Confirm(context.Background(), command)
	if err != nil {
		t.Fatalf("Confirm() error = %v", err)
	}
	if !tx.committed || tx.rolledBack {
		t.Fatalf("transaction committed=%t rolledBack=%t", tx.committed, tx.rolledBack)
	}
	if tx.generationLockCalls != 1 {
		t.Fatalf("generation lock calls = %d", tx.generationLockCalls)
	}
	if created.ParticipationStatus != registration.ParticipationStatusConfirmed ||
		created.SessionID != command.SessionID ||
		created.PrincipalID != command.PrincipalID ||
		created.IdempotencyKey != command.IdempotencyKey {
		t.Fatalf("Confirm() = %+v", created)
	}
	if !reflect.DeepEqual(capture.lockOrder, []string{"series", "instance", "session"}) {
		t.Fatalf("lock order = %v", capture.lockOrder)
	}
	if capture.creates != 1 || capture.sessionIncrements != 1 || capture.seriesIncrements != 1 {
		t.Fatalf("write counts = %+v", capture)
	}
	if capture.isolation != sql.LevelSerializable {
		t.Fatalf("isolation = %v, want serializable", capture.isolation)
	}
	if !reflect.DeepEqual(capture.sessionIncrementArgs[:3], []any{
		command.TenantID,
		command.InstanceID,
		command.SessionID,
	}) {
		t.Fatalf("Session increment args = %#v", capture.sessionIncrementArgs)
	}
	if !reflect.DeepEqual(capture.seriesIncrementArgs[:2], []any{command.TenantID, command.SeriesID}) {
		t.Fatalf("Series increment args = %#v", capture.seriesIncrementArgs)
	}
}

func TestFreeRegistrarReplaysMatchingIdempotencyKeyWithoutWriting(t *testing.T) {
	t.Parallel()

	command := validFreeRegistrationCommand()
	existing := registration.Registration{
		ID:                  uuid.New(),
		TenantID:            command.TenantID,
		SeriesID:            command.SeriesID,
		InstanceID:          command.InstanceID,
		SessionID:           command.SessionID,
		PrincipalID:         command.PrincipalID,
		ParticipationStatus: registration.ParticipationStatusCancelled,
		IdempotencyKey:      command.IdempotencyKey,
		Version:             2,
		CreatedAt:           time.Now().UTC(),
		UpdatedAt:           time.Now().UTC(),
	}
	cancelledAt := existing.UpdatedAt
	reason := "user_cancelled"
	existing.CancelledAt = &cancelledAt
	existing.CancellationReason = &reason
	registrar, tx, capture := newFreeRegistrarHarness(t, command, freeRegistrarScenario{
		existingByIdempotency: &existing,
	})

	got, err := registrar.Confirm(context.Background(), command)
	if err != nil {
		t.Fatalf("Confirm() error = %v", err)
	}
	if !reflect.DeepEqual(got, existing) || !tx.committed || tx.rolledBack {
		t.Fatalf("Confirm(replay) = %+v, transaction=%+v", got, tx)
	}
	if len(capture.lockOrder) != 0 || capture.creates != 0 ||
		capture.sessionIncrements != 0 || capture.seriesIncrements != 0 {
		t.Fatalf("replay performed writes/locks: %+v", capture)
	}
}

func TestFreeRegistrarPersistsContactAndQuestionnaireSnapshotAtomically(
	t *testing.T,
) {
	t.Parallel()

	command := validFreeRegistrationCommand()
	questionnaire := freeRegistrationQuestionnaire(command)
	questionnaireID := questionnaire.QuestionnaireVersionID
	command.Submission = &registration.RegistrationSubmission{
		InstancePublicationVersion: 1,
		PriceCents:                 0,
		PrivacyPolicyVersion:       "privacy-v1",
		Contact:                    validRegistrationContact(),
		QuestionnaireVersionID:     &questionnaireID,
		Answers: []activity.QuestionnaireAnswer{{
			FieldID: questionnaire.Fields[0].FieldID,
			Values:  []string{"beginner"},
		}},
	}
	registrar, tx, capture := newFreeRegistrarHarness(
		t,
		command,
		freeRegistrarScenario{questionnaire: &questionnaire},
	)

	created, err := registrar.Confirm(context.Background(), command)
	if err != nil {
		t.Fatalf("Confirm() error = %v", err)
	}
	if !tx.committed || tx.rolledBack ||
		capture.creates != 1 || capture.snapshots != 1 ||
		capture.snapshotPrivacyPolicyVersion != "privacy-v1" ||
		capture.answers != 1 || capture.sessionIncrements != 1 ||
		capture.seriesIncrements != 1 {
		t.Fatalf("transaction=%+v capture=%+v", tx, capture)
	}
	if !reflect.DeepEqual(
		capture.lockOrder,
		[]string{"brand", "series", "instance", "session"},
	) {
		t.Fatalf("submission lock order = %v", capture.lockOrder)
	}
	if created.ParticipationStatus != registration.ParticipationStatusConfirmed {
		t.Fatalf("created Registration = %+v", created)
	}
}

func TestFreeRegistrarAcceptsExplicitNoQuestionnaireSubmission(t *testing.T) {
	t.Parallel()

	command := validFreeRegistrationCommand()
	command.Submission = &registration.RegistrationSubmission{
		InstancePublicationVersion: 1,
		PriceCents:                 0,
		PrivacyPolicyVersion:       "privacy-v1",
		Contact:                    validRegistrationContact(),
	}
	registrar, tx, capture := newFreeRegistrarHarness(
		t,
		command,
		freeRegistrarScenario{},
	)
	if _, err := registrar.Confirm(context.Background(), command); err != nil {
		t.Fatalf("Confirm() error = %v", err)
	}
	if !tx.committed || capture.snapshots != 1 || capture.answers != 0 {
		t.Fatalf("transaction=%+v capture=%+v", tx, capture)
	}
}

func TestFreeRegistrarRejectsStaleOrInvalidQuestionnaireBeforeWrites(
	t *testing.T,
) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		mutate func(*registration.RegistrationSubmission)
		want   error
	}{
		{
			name: "privacy policy changed",
			mutate: func(value *registration.RegistrationSubmission) {
				value.PrivacyPolicyVersion = "privacy-v2"
			},
			want: ErrRegistrationPrivacyPolicyConflict,
		},
		{
			name: "stale version",
			mutate: func(value *registration.RegistrationSubmission) {
				other := uuid.New()
				value.QuestionnaireVersionID = &other
			},
			want: ErrRegistrationQuestionnaireConflict,
		},
		{
			name: "invalid answer",
			mutate: func(value *registration.RegistrationSubmission) {
				value.Answers[0].Values = []string{"unknown"}
			},
			want: ErrRegistrationAnswersInvalid,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			command := validFreeRegistrationCommand()
			questionnaire := freeRegistrationQuestionnaire(command)
			questionnaireID := questionnaire.QuestionnaireVersionID
			submission := registration.RegistrationSubmission{
				InstancePublicationVersion: 1,
				PriceCents:                 0,
				PrivacyPolicyVersion:       "privacy-v1",
				Contact:                    validRegistrationContact(),
				QuestionnaireVersionID:     &questionnaireID,
				Answers: []activity.QuestionnaireAnswer{{
					FieldID: questionnaire.Fields[0].FieldID,
					Values:  []string{"beginner"},
				}},
			}
			test.mutate(&submission)
			command.Submission = &submission
			registrar, tx, capture := newFreeRegistrarHarness(
				t,
				command,
				freeRegistrarScenario{questionnaire: &questionnaire},
			)
			_, err := registrar.Confirm(context.Background(), command)
			if !errors.Is(err, test.want) {
				t.Fatalf("Confirm() error = %v, want %v", err, test.want)
			}
			if tx.committed || !tx.rolledBack || capture.creates != 0 ||
				capture.snapshots != 0 || capture.answers != 0 {
				t.Fatalf("transaction=%+v capture=%+v", tx, capture)
			}
		})
	}
}

func TestFreeRegistrarRejectsStaleConfirmationFactsBeforeWrites(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		scenario freeRegistrarScenario
	}{
		{
			name:     "Instance publication changed",
			scenario: freeRegistrarScenario{publicationVersion: 2},
		},
		{
			name: "price changed",
			scenario: freeRegistrarScenario{
				priceCents: int64Pointer(1_000),
			},
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			command := validFreeRegistrationCommand()
			command.Submission = &registration.RegistrationSubmission{
				InstancePublicationVersion: 1,
				PriceCents:                 0,
				PrivacyPolicyVersion:       "privacy-v1",
				Contact:                    validRegistrationContact(),
			}
			registrar, tx, capture := newFreeRegistrarHarness(
				t,
				command,
				test.scenario,
			)
			_, err := registrar.Confirm(context.Background(), command)
			if !errors.Is(err, ErrRegistrationTransactionConflict) {
				t.Fatalf("Confirm() error = %v", err)
			}
			if tx.committed || !tx.rolledBack || capture.creates != 0 ||
				capture.snapshots != 0 {
				t.Fatalf("transaction=%+v capture=%+v", tx, capture)
			}
		})
	}
}

func TestFreeRegistrarSubmissionReplayRequiresExactFingerprint(t *testing.T) {
	t.Parallel()

	command := validFreeRegistrationCommand()
	submission := registration.RegistrationSubmission{
		InstancePublicationVersion: 1,
		PriceCents:                 0,
		PrivacyPolicyVersion:       "privacy-v1",
		Contact:                    validRegistrationContact(),
	}
	command.Submission = &submission
	prepared, err := registration.PrepareRegistrationSubmission(
		command.SessionID,
		submission,
	)
	if err != nil {
		t.Fatalf("PrepareRegistrationSubmission() error = %v", err)
	}
	existing := registration.Registration{
		ID:                  uuid.New(),
		TenantID:            command.TenantID,
		SeriesID:            command.SeriesID,
		InstanceID:          command.InstanceID,
		SessionID:           command.SessionID,
		PrincipalID:         command.PrincipalID,
		ParticipationStatus: registration.ParticipationStatusConfirmed,
		IdempotencyKey:      command.IdempotencyKey,
		Version:             1,
		CreatedAt:           time.Now().UTC(),
		UpdatedAt:           time.Now().UTC(),
	}
	confirmedAt := existing.CreatedAt
	existing.ConfirmedAt = &confirmedAt
	registrar, tx, capture := newFreeRegistrarHarness(
		t,
		command,
		freeRegistrarScenario{
			existingByIdempotency: &existing,
			existingFingerprint:   &prepared.RequestFingerprint,
		},
	)
	// Exact replay is immutable client intent and remains valid after the
	// server's mutable contact policy advances.
	command.ContactPolicyVersion = "contact-v2"
	if _, err := registrar.Confirm(context.Background(), command); err != nil {
		t.Fatalf("Confirm(replay) error = %v", err)
	}
	if !tx.committed || len(capture.lockOrder) != 0 || capture.creates != 0 {
		t.Fatalf("replay transaction=%+v capture=%+v", tx, capture)
	}

	changed := command
	changedSubmission := submission
	changedSubmission.Contact.Name = "Another Name"
	changed.Submission = &changedSubmission
	registrar, tx, _ = newFreeRegistrarHarness(
		t,
		changed,
		freeRegistrarScenario{
			existingByIdempotency: &existing,
			existingFingerprint:   &prepared.RequestFingerprint,
		},
	)
	if _, err := registrar.Confirm(context.Background(), changed); !errors.Is(
		err,
		ErrRegistrationIdempotencyConflict,
	) {
		t.Fatalf("Confirm(changed replay) error = %v", err)
	}
	if tx.committed || !tx.rolledBack {
		t.Fatalf("changed replay transaction=%+v", tx)
	}
}

func TestFreeRegistrarReplayDoesNotConsultMutableSessionFacts(t *testing.T) {
	t.Parallel()

	command := validFreeRegistrationCommand()
	submission := registration.RegistrationSubmission{
		InstancePublicationVersion: 1,
		PriceCents:                 0,
		PrivacyPolicyVersion:       "privacy-v1",
		Contact:                    validRegistrationContact(),
	}
	prepared, err := registration.PrepareRegistrationSubmission(
		command.SessionID,
		submission,
	)
	if err != nil {
		t.Fatalf("PrepareRegistrationSubmission() error = %v", err)
	}
	confirmedAt := time.Now().UTC()
	existing := registration.Registration{
		ID:                  uuid.New(),
		TenantID:            command.TenantID,
		SeriesID:            command.SeriesID,
		InstanceID:          command.InstanceID,
		SessionID:           command.SessionID,
		PrincipalID:         command.PrincipalID,
		ParticipationStatus: registration.ParticipationStatusConfirmed,
		IdempotencyKey:      command.IdempotencyKey,
		ConfirmedAt:         &confirmedAt,
		Version:             1,
		CreatedAt:           confirmedAt,
		UpdatedAt:           confirmedAt,
	}
	registrar, tx, capture := newFreeRegistrarHarness(
		t,
		command,
		freeRegistrarScenario{
			existingByIdempotency: &existing,
			existingFingerprint:   &prepared.RequestFingerprint,
			seriesStatus:          activity.SeriesStatusArchived,
			sessionStatus:         activity.SessionStatusCancelled,
		},
	)

	got, found, err := registrar.Replay(
		context.Background(),
		ReplayFreeRegistrationCommand{
			TenantID:       command.TenantID,
			SessionID:      command.SessionID,
			PrincipalID:    command.PrincipalID,
			IdempotencyKey: command.IdempotencyKey,
			Submission:     submission,
		},
	)
	if err != nil || !found || got.ID != existing.ID {
		t.Fatalf("Replay() = %+v, %t, %v", got, found, err)
	}
	if !tx.committed || tx.rolledBack || tx.generationLockCalls != 0 ||
		len(capture.lockOrder) != 0 || capture.creates != 0 {
		t.Fatalf("replay transaction=%+v capture=%+v", tx, capture)
	}
}

func TestFreeRegistrarFencesStaleGenerationBeforeBusinessLocks(t *testing.T) {
	t.Parallel()

	command := validFreeRegistrationCommand()
	registrar, tx, capture := newFreeRegistrarHarness(t, command, freeRegistrarScenario{})
	tx.generationErr = ErrRegistrationGenerationInactive

	_, err := registrar.Confirm(context.Background(), command)
	if !errors.Is(err, ErrRegistrationGenerationInactive) ||
		tx.generationLockCalls != 1 || len(capture.lockOrder) != 0 ||
		capture.creates != 0 || tx.committed || !tx.rolledBack {
		t.Fatalf("Confirm(stale generation) error=%v tx=%+v capture=%+v", err, tx, capture)
	}
}

func TestFreeRegistrarRejectsIdempotencyReuseForAnotherTarget(t *testing.T) {
	t.Parallel()

	command := validFreeRegistrationCommand()
	existing := registration.Registration{
		TenantID:       command.TenantID,
		SeriesID:       command.SeriesID,
		InstanceID:     command.InstanceID,
		SessionID:      uuid.New(),
		PrincipalID:    command.PrincipalID,
		IdempotencyKey: command.IdempotencyKey,
	}
	registrar, tx, _ := newFreeRegistrarHarness(t, command, freeRegistrarScenario{
		existingByIdempotency: &existing,
	})

	if _, err := registrar.Confirm(context.Background(), command); !errors.Is(
		err,
		ErrRegistrationIdempotencyConflict,
	) {
		t.Fatalf("Confirm() error = %v", err)
	}
	if tx.committed || !tx.rolledBack {
		t.Fatalf("transaction committed=%t rolledBack=%t", tx.committed, tx.rolledBack)
	}
}

func TestFreeRegistrarRejectsAnotherOpenRegistration(t *testing.T) {
	t.Parallel()

	command := validFreeRegistrationCommand()
	open := registration.Registration{
		ID:                  uuid.New(),
		TenantID:            command.TenantID,
		SeriesID:            command.SeriesID,
		InstanceID:          command.InstanceID,
		SessionID:           command.SessionID,
		PrincipalID:         command.PrincipalID,
		ParticipationStatus: registration.ParticipationStatusConfirmed,
		IdempotencyKey:      "registration:other",
	}
	registrar, tx, capture := newFreeRegistrarHarness(t, command, freeRegistrarScenario{
		openRegistration: &open,
	})

	if _, err := registrar.Confirm(context.Background(), command); !errors.Is(err, ErrRegistrationAlreadyOpen) {
		t.Fatalf("Confirm() error = %v", err)
	}
	if tx.committed || !tx.rolledBack || capture.creates != 0 {
		t.Fatalf("transaction=%+v capture=%+v", tx, capture)
	}
}

func TestFreeRegistrarRejectsPaidOrUnavailableSession(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		scenario freeRegistrarScenario
		wantErr  error
	}{
		{
			name:     "paid",
			scenario: freeRegistrarScenario{priceCents: int64Pointer(100)},
			wantErr:  ErrRegistrationPaymentRequired,
		},
		{
			name:     "full",
			scenario: freeRegistrarScenario{confirmedCount: intPointer(10)},
			wantErr:  ErrRegistrationUnavailable,
		},
		{
			name:     "cancelled Session",
			scenario: freeRegistrarScenario{sessionStatus: activity.SessionStatusCancelled},
			wantErr:  ErrRegistrationUnavailable,
		},
		{
			name:     "completed Instance",
			scenario: freeRegistrarScenario{instanceStatus: activity.InstanceStatusCompleted},
			wantErr:  ErrRegistrationUnavailable,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			command := validFreeRegistrationCommand()
			registrar, tx, capture := newFreeRegistrarHarness(t, command, test.scenario)
			if _, err := registrar.Confirm(context.Background(), command); !errors.Is(err, test.wantErr) {
				t.Fatalf("Confirm() error = %v, want %v", err, test.wantErr)
			}
			if tx.committed || !tx.rolledBack || capture.creates != 0 {
				t.Fatalf("transaction=%+v capture=%+v", tx, capture)
			}
		})
	}
}

func TestFreeRegistrarRollsBackCapacityConflict(t *testing.T) {
	t.Parallel()

	command := validFreeRegistrationCommand()
	registrar, tx, capture := newFreeRegistrarHarness(t, command, freeRegistrarScenario{
		sessionIncrementErr: sql.ErrNoRows,
	})
	if _, err := registrar.Confirm(context.Background(), command); !errors.Is(
		err,
		ErrRegistrationCapacityConflict,
	) {
		t.Fatalf("Confirm() error = %v", err)
	}
	if tx.committed || !tx.rolledBack || capture.creates != 1 || capture.seriesIncrements != 0 {
		t.Fatalf("transaction=%+v capture=%+v", tx, capture)
	}
}

func TestFreeRegistrarValidatesBeforeBeginningTransaction(t *testing.T) {
	t.Parallel()

	command := validFreeRegistrationCommand()
	command.SessionID = uuid.Nil
	starter := &fakeRegistrationTransactionStarter{}
	registrar := &FreeRegistrar{
		transactions: starter,
		now:          time.Now,
		generationID: uuid.New(),
	}
	if _, err := registrar.Confirm(context.Background(), command); !errors.Is(
		err,
		ErrInvalidFreeRegistrationCommand,
	) {
		t.Fatalf("Confirm() error = %v", err)
	}
	if starter.begins != 0 {
		t.Fatalf("transaction began %d time(s)", starter.begins)
	}
}

func TestFreeRegistrarPropagatesBeginAndCommitFailures(t *testing.T) {
	t.Parallel()

	command := validFreeRegistrationCommand()
	beginFailure := errors.New("begin failed")
	starter := &fakeRegistrationTransactionStarter{err: beginFailure}
	registrar := &FreeRegistrar{
		transactions: starter,
		now:          time.Now,
		generationID: uuid.New(),
	}
	if _, err := registrar.Confirm(context.Background(), command); !errors.Is(err, beginFailure) {
		t.Fatalf("Confirm(begin failure) error = %v", err)
	}
	if starter.begins != 1 {
		t.Fatalf("transaction begins = %d", starter.begins)
	}

	commitFailure := errors.New("commit failed")
	registrar, tx, capture := newFreeRegistrarHarness(t, command, freeRegistrarScenario{
		commitErr: commitFailure,
	})
	if _, err := registrar.Confirm(context.Background(), command); !errors.Is(err, commitFailure) {
		t.Fatalf("Confirm(commit failure) error = %v", err)
	}
	if tx.committed || !tx.rolledBack || capture.seriesIncrements != 1 {
		t.Fatalf("transaction=%+v capture=%+v", tx, capture)
	}
}

func TestFreeRegistrarRollsBackSeriesProjectionFailure(t *testing.T) {
	t.Parallel()

	command := validFreeRegistrationCommand()
	seriesFailure := errors.New("series update failed")
	registrar, tx, capture := newFreeRegistrarHarness(t, command, freeRegistrarScenario{
		seriesIncrementErr: seriesFailure,
	})
	if _, err := registrar.Confirm(context.Background(), command); !errors.Is(err, seriesFailure) {
		t.Fatalf("Confirm() error = %v", err)
	}
	if tx.committed || !tx.rolledBack ||
		capture.creates != 1 || capture.sessionIncrements != 1 || capture.seriesIncrements != 1 {
		t.Fatalf("transaction=%+v capture=%+v", tx, capture)
	}
}

func TestRegistrationUniqueAndSerializationErrorsAreClassified(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		classifier func(error) error
		want       error
	}{
		{
			name: "idempotency",
			err: &pgconn.PgError{
				Code:           "23505",
				ConstraintName: "xiangwan_registrations_tenant_idempotency_key_key",
			},
			classifier: classifyRegistrationCreateError,
			want:       ErrRegistrationIdempotencyConflict,
		},
		{
			name: "open Registration",
			err: &pgconn.PgError{
				Code:           "23505",
				ConstraintName: "uq_xiangwan_registrations_open_principal_session",
			},
			classifier: classifyRegistrationCreateError,
			want:       ErrRegistrationAlreadyOpen,
		},
		{
			name:       "serialization",
			err:        &pgconn.PgError{Code: "40001"},
			classifier: classifyRegistrationCommitError,
			want:       ErrRegistrationTransactionConflict,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := test.classifier(test.err); !errors.Is(got, test.want) {
				t.Fatalf("classifier() = %v, want %v", got, test.want)
			}
		})
	}
}

type freeRegistrarScenario struct {
	existingByIdempotency *registration.Registration
	existingFingerprint   *string
	openRegistration      *registration.Registration
	brandStatus           activity.BrandLifecycleStatus
	seriesStatus          activity.SeriesStatus
	instanceStatus        activity.InstanceStatus
	publicationVersion    int64
	sessionStatus         activity.SessionStatus
	questionnaire         *activity.SessionQuestionnaire
	priceCents            *int64
	confirmedCount        *int
	sessionIncrementErr   error
	seriesIncrementErr    error
	snapshotErr           error
	answerErr             error
	commitErr             error
}

type freeRegistrarCapture struct {
	isolation                    sql.IsolationLevel
	lockOrder                    []string
	creates                      int
	sessionIncrements            int
	seriesIncrements             int
	snapshots                    int
	snapshotPrivacyPolicyVersion string
	answers                      int
	sessionIncrementArgs         []any
	seriesIncrementArgs          []any
}

func newFreeRegistrarHarness(
	t *testing.T,
	command ConfirmFreeRegistrationCommand,
	scenario freeRegistrarScenario,
) (*FreeRegistrar, *fakeRegistrationTransaction, *freeRegistrarCapture) {
	t.Helper()

	now := time.Date(2026, time.September, 12, 4, 0, 0, 0, time.UTC)
	registrationStartAt := now.Add(-time.Hour)
	registrationEndAt := now.Add(time.Hour)
	sessionStartAt := now.Add(2 * time.Hour)
	sessionEndAt := now.Add(4 * time.Hour)
	priceCents := int64(0)
	confirmedCount := 5
	if scenario.priceCents != nil {
		priceCents = *scenario.priceCents
	}
	if scenario.confirmedCount != nil {
		confirmedCount = *scenario.confirmedCount
	}
	if scenario.seriesStatus == "" {
		scenario.seriesStatus = activity.SeriesStatusActive
	}
	if scenario.brandStatus == "" {
		scenario.brandStatus = activity.BrandLifecycleActive
	}
	if scenario.publicationVersion == 0 {
		scenario.publicationVersion = 1
	}
	if scenario.instanceStatus == "" {
		scenario.instanceStatus = activity.InstanceStatusPublished
	}
	if scenario.sessionStatus == "" {
		scenario.sessionStatus = activity.SessionStatusPublished
	}

	capture := &freeRegistrarCapture{}
	tx := &fakeRegistrationTransaction{commitErr: scenario.commitErr}
	tx.fakeQueryExecutor = &fakeQueryExecutor{queryRow: func(query string, args ...any) rowScanner {
		switch {
		case strings.Contains(query, "idempotency_key = $2"):
			if scenario.existingByIdempotency == nil {
				return &fakeRow{err: sql.ErrNoRows}
			}
			return &fakeRow{values: registrationScanValues(*scenario.existingByIdempotency)}
		case strings.Contains(query, "SELECT request_fingerprint"):
			if scenario.existingFingerprint == nil {
				return &fakeRow{err: sql.ErrNoRows}
			}
			return &fakeRow{values: []any{*scenario.existingFingerprint}}
		case strings.Contains(query, "FROM xiangwan_brand_profiles"):
			capture.lockOrder = append(capture.lockOrder, "brand")
			return &fakeRow{values: []any{scenario.brandStatus}}
		case strings.Contains(query, "current_public_instance_id"):
			return &fakeRow{values: []any{scenario.publicationVersion}}
		case strings.Contains(query, "FROM xiangwan_activity_series"):
			capture.lockOrder = append(capture.lockOrder, "series")
			return &fakeRow{values: []any{scenario.seriesStatus, int64(7)}}
		case strings.Contains(query, "FROM xiangwan_activity_instances"):
			capture.lockOrder = append(capture.lockOrder, "instance")
			return &fakeRow{values: []any{scenario.instanceStatus}}
		case strings.Contains(query, "FROM xiangwan_activity_sessions"):
			capture.lockOrder = append(capture.lockOrder, "session")
			return &fakeRow{values: []any{
				scenario.sessionStatus,
				sql.NullTime{Time: registrationStartAt, Valid: true},
				sql.NullTime{Time: registrationEndAt, Valid: true},
				sql.NullTime{Time: sessionStartAt, Valid: true},
				sql.NullTime{Time: sessionEndAt, Valid: true},
				sql.NullInt64{Int64: 10, Valid: true},
				sql.NullInt64{Int64: 3, Valid: true},
				sql.NullInt64{Int64: 2, Valid: true},
				sql.NullInt64{Int64: priceCents, Valid: true},
				confirmedCount,
				1,
				int64(9),
			}}
		case strings.Contains(query, "principal_id = $2"):
			if scenario.openRegistration == nil {
				return &fakeRow{err: sql.ErrNoRows}
			}
			return &fakeRow{values: registrationScanValues(*scenario.openRegistration)}
		case strings.Contains(query, "JSONB_AGG"):
			if scenario.questionnaire == nil {
				return &fakeRow{err: sql.ErrNoRows}
			}
			return &fakeRow{values: questionnaireRegistrationScanValues(
				t,
				*scenario.questionnaire,
			)}
		case strings.Contains(query, "INSERT INTO xiangwan_registrations"):
			capture.creates++
			created := registration.Registration{
				ID:                  args[0].(uuid.UUID),
				TenantID:            args[1].(uuid.UUID),
				SeriesID:            args[2].(uuid.UUID),
				InstanceID:          args[3].(uuid.UUID),
				SessionID:           args[4].(uuid.UUID),
				PrincipalID:         args[5].(uuid.UUID),
				ParticipationStatus: args[6].(registration.ParticipationStatus),
				IdempotencyKey:      args[7].(string),
				ConfirmedAt:         args[8].(*time.Time),
				Version:             args[11].(int64),
				CreatedAt:           args[12].(time.Time),
				UpdatedAt:           args[13].(time.Time),
			}
			return &fakeRow{values: registrationScanValues(created)}
		case strings.Contains(query, "INSERT INTO xiangwan_registration_snapshots"):
			capture.snapshots++
			capture.snapshotPrivacyPolicyVersion = args[9].(string)
			if scenario.snapshotErr != nil {
				return &fakeRow{err: scenario.snapshotErr}
			}
			return &fakeRow{values: []any{args[0].(uuid.UUID)}}
		case strings.Contains(query, "INSERT INTO xiangwan_registration_answers"):
			capture.answers++
			if scenario.answerErr != nil {
				return &fakeRow{err: scenario.answerErr}
			}
			return &fakeRow{values: []any{args[4].(uuid.UUID)}}
		case strings.Contains(query, "UPDATE xiangwan_activity_sessions"):
			capture.sessionIncrements++
			capture.sessionIncrementArgs = append([]any(nil), args...)
			if scenario.sessionIncrementErr != nil {
				return &fakeRow{err: scenario.sessionIncrementErr}
			}
			return &fakeRow{values: []any{int64(10)}}
		case strings.Contains(query, "UPDATE xiangwan_activity_series"):
			capture.seriesIncrements++
			capture.seriesIncrementArgs = append([]any(nil), args...)
			if scenario.seriesIncrementErr != nil {
				return &fakeRow{err: scenario.seriesIncrementErr}
			}
			return &fakeRow{values: []any{int64(8)}}
		default:
			t.Fatalf("unexpected query: %s", query)
			return &fakeRow{}
		}
	}}
	starter := &fakeRegistrationTransactionStarter{tx: tx, capture: capture}
	return &FreeRegistrar{
		transactions: starter,
		now:          func() time.Time { return now },
		generationID: uuid.New(),
	}, tx, capture
}

type fakeRegistrationTransactionStarter struct {
	tx      registrationTransaction
	err     error
	begins  int
	capture *freeRegistrarCapture
}

func (starter *fakeRegistrationTransactionStarter) beginTx(
	_ context.Context,
	options *sql.TxOptions,
) (registrationTransaction, error) {
	starter.begins++
	if starter.capture != nil {
		starter.capture.isolation = options.Isolation
	}
	if starter.err != nil {
		return nil, starter.err
	}
	return starter.tx, nil
}

type fakeRegistrationTransaction struct {
	*fakeQueryExecutor
	commitErr           error
	generationErr       error
	generationLockCalls int
	committed           bool
	rolledBack          bool
}

func (tx *fakeRegistrationTransaction) lockActiveGeneration(
	_ context.Context,
	_ uuid.UUID,
	_ uuid.UUID,
) error {
	tx.generationLockCalls++
	return tx.generationErr
}

func (tx *fakeRegistrationTransaction) Commit() error {
	if tx.commitErr != nil {
		return tx.commitErr
	}
	tx.committed = true
	return nil
}

func (tx *fakeRegistrationTransaction) Rollback() error {
	tx.rolledBack = true
	return nil
}

func validFreeRegistrationCommand() ConfirmFreeRegistrationCommand {
	return ConfirmFreeRegistrationCommand{
		TenantID:             uuid.New(),
		SeriesID:             uuid.New(),
		InstanceID:           uuid.New(),
		SessionID:            uuid.New(),
		PrincipalID:          uuid.New(),
		IdempotencyKey:       "registration:free:1",
		PrivacyPolicyVersion: "privacy-v1",
		ManualContactEnabled: true,
		ContactPolicyVersion: "contact-v1",
	}
}

func validRegistrationContact() registration.ContactSnapshot {
	return registration.ContactSnapshot{
		Source:        registration.ContactSourceManual,
		Name:          "Wang Wei",
		PhoneE164:     "+8613812345678",
		PolicyVersion: "contact-v1",
	}
}

func freeRegistrationQuestionnaire(
	command ConfirmFreeRegistrationCommand,
) activity.SessionQuestionnaire {
	return activity.SessionQuestionnaire{
		QuestionnaireVersionID: uuid.New(),
		InstanceID:             command.InstanceID,
		SessionID:              command.SessionID,
		Version:                1,
		PrivacyPurpose:         "用于报名和现场服务",
		PrivacyPolicyVersion:   "privacy-v1",
		PublishedAt: time.Date(
			2026,
			time.September,
			12,
			3,
			0,
			0,
			0,
			time.UTC,
		),
		Fields: []activity.QuestionnaireField{{
			FieldID:   uuid.New(),
			Code:      "experience",
			Type:      activity.QuestionnaireFieldSingleChoice,
			Label:     "AI 经验",
			Required:  true,
			SortOrder: 0,
			Options: []activity.QuestionnaireOption{
				{Code: "beginner", Label: "刚开始"},
				{Code: "experienced", Label: "有经验"},
			},
		}},
	}
}

func questionnaireRegistrationScanValues(
	t *testing.T,
	questionnaire activity.SessionQuestionnaire,
) []any {
	t.Helper()
	fields := make(
		[]registrationQuestionnaireFieldJSON,
		0,
		len(questionnaire.Fields),
	)
	for _, field := range questionnaire.Fields {
		fields = append(fields, registrationQuestionnaireFieldJSON{
			FieldID:       field.FieldID,
			Code:          field.Code,
			Type:          field.Type,
			Label:         field.Label,
			HelpText:      field.HelpText,
			Required:      field.Required,
			SortOrder:     field.SortOrder,
			MinLength:     field.MinLength,
			MaxLength:     field.MaxLength,
			MaxSelections: field.MaxSelections,
			Options:       field.Options,
		})
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("encode questionnaire fixture: %v", err)
	}
	return []any{
		questionnaire.QuestionnaireVersionID,
		questionnaire.Version,
		questionnaire.PrivacyPurpose,
		questionnaire.PrivacyPolicyVersion,
		questionnaire.PublishedAt,
		encoded,
	}
}

func intPointer(value int) *int {
	return &value
}

func int64Pointer(value int64) *int64 {
	return &value
}
