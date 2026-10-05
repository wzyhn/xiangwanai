package paymentpostgres

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
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon"
	couponpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon/postgres"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestPaidRegistrarStartsRegistrationOrderAndHoldAtomically(t *testing.T) {
	t.Parallel()

	command := validPaidRegistrationCommand()
	registrar, tx, capture := newPaidRegistrarHarness(t, command, paidRegistrarScenario{})

	created, err := registrar.Start(context.Background(), command)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if !tx.committed || tx.rolledBack {
		t.Fatalf("transaction committed=%t rolledBack=%t", tx.committed, tx.rolledBack)
	}
	if tx.generationLockCalls != 1 {
		t.Fatalf("generation lock calls = %d", tx.generationLockCalls)
	}
	if capture.isolation != sql.LevelSerializable {
		t.Fatalf("isolation = %v, want serializable", capture.isolation)
	}
	if !reflect.DeepEqual(capture.lockOrder, []string{"series", "instance", "session"}) {
		t.Fatalf("lock order = %v", capture.lockOrder)
	}
	if capture.registrationCreates != 1 ||
		capture.orderCreates != 1 ||
		capture.holdCreates != 1 ||
		capture.holdIncrements != 1 {
		t.Fatalf("write counts = %+v", capture)
	}
	if created.Registration.ParticipationStatus != registration.ParticipationStatusPendingPayment ||
		created.Payment.Order.RegistrationID != created.Registration.ID ||
		created.Payment.Order.PayableCents != 10_000 ||
		created.Payment.Hold.OrderID != created.Payment.Order.ID ||
		created.Payment.Hold.ExpiresAt.Sub(created.Payment.Hold.CreatedAt) != payment.CapacityHoldDuration {
		t.Fatalf("Start() = %+v", created)
	}
	if !reflect.DeepEqual(capture.holdIncrementArgs[:3], []any{
		command.TenantID,
		command.InstanceID,
		command.SessionID,
	}) {
		t.Fatalf("Session hold increment args = %#v", capture.holdIncrementArgs)
	}
}

func TestPaidRegistrarPersistsSubmissionWithPendingOrderAndHold(t *testing.T) {
	t.Parallel()

	command := validPaidRegistrationCommand()
	questionnaire := paidRegistrationQuestionnaire(command)
	questionnaireID := questionnaire.QuestionnaireVersionID
	command.Submission = &registration.RegistrationSubmission{
		InstancePublicationVersion: 1,
		PriceCents:                 10_000,
		PrivacyPolicyVersion:       "privacy-v1",
		Contact:                    validPaidRegistrationContact(),
		QuestionnaireVersionID:     &questionnaireID,
		Answers: []activity.QuestionnaireAnswer{{
			FieldID: questionnaire.Fields[0].FieldID,
			Values:  []string{"production"},
		}},
	}
	registrar, tx, capture := newPaidRegistrarHarness(
		t,
		command,
		paidRegistrarScenario{questionnaire: &questionnaire},
	)

	created, err := registrar.Start(context.Background(), command)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if !tx.committed || tx.rolledBack ||
		created.Registration.ParticipationStatus !=
			registration.ParticipationStatusPendingPayment ||
		created.Payment.Order.PaymentStatus != payment.OrderStatusPending ||
		created.Payment.Hold.HoldStatus != payment.CapacityHoldStatusActive ||
		capture.registrationCreates != 1 || capture.orderCreates != 1 ||
		capture.holdCreates != 1 || capture.snapshots != 1 ||
		capture.snapshotPrivacyPolicyVersion != "privacy-v1" ||
		capture.answers != 1 || capture.holdIncrements != 1 {
		t.Fatalf("Start()=%+v transaction=%+v capture=%+v", created, tx, capture)
	}
	if !reflect.DeepEqual(
		capture.lockOrder,
		[]string{"brand", "series", "instance", "session"},
	) {
		t.Fatalf("submission lock order = %v", capture.lockOrder)
	}
}

func TestPaidRegistrarSubmissionReplayRequiresExactFingerprint(t *testing.T) {
	t.Parallel()

	command := validPaidRegistrationCommand()
	submission := registration.RegistrationSubmission{
		InstancePublicationVersion: 1,
		PriceCents:                 10_000,
		PrivacyPolicyVersion:       "privacy-v1",
		Contact:                    validPaidRegistrationContact(),
	}
	command.Submission = &submission
	prepared, err := registration.PrepareRegistrationSubmission(
		command.SessionID,
		submission,
	)
	if err != nil {
		t.Fatalf("PrepareRegistrationSubmission() error = %v", err)
	}
	now := time.Date(2026, time.September, 12, 6, 0, 0, 0, time.UTC)
	existing := existingPaidRegistrationContext(t, command, now)
	registrar, tx, capture := newPaidRegistrarHarness(
		t,
		command,
		paidRegistrarScenario{
			existingContext:     &existing,
			existingFingerprint: &prepared.RequestFingerprint,
		},
	)
	// The exact stored submission wins over a later mutable policy version.
	command.ContactPolicyVersion = "contact-v2"
	if _, err := registrar.Start(context.Background(), command); err != nil {
		t.Fatalf("Start(replay) error = %v", err)
	}
	if !tx.committed || len(capture.lockOrder) != 0 ||
		capture.registrationCreates != 0 || capture.snapshots != 0 {
		t.Fatalf("replay transaction=%+v capture=%+v", tx, capture)
	}

	changed := command
	changedSubmission := submission
	changedSubmission.PriceCents++
	changed.Submission = &changedSubmission
	registrar, tx, _ = newPaidRegistrarHarness(
		t,
		changed,
		paidRegistrarScenario{
			existingContext:     &existing,
			existingFingerprint: &prepared.RequestFingerprint,
		},
	)
	if _, err := registrar.Start(context.Background(), changed); !errors.Is(
		err,
		ErrPaidRegistrationIdempotencyConflict,
	) {
		t.Fatalf("Start(changed replay) error = %v", err)
	}
	if tx.committed || !tx.rolledBack {
		t.Fatalf("changed replay transaction=%+v", tx)
	}
}

func TestPaidRegistrarReplayDoesNotConsultMutableSessionFacts(t *testing.T) {
	t.Parallel()

	command := validPaidRegistrationCommand()
	submission := registration.RegistrationSubmission{
		InstancePublicationVersion: 1,
		PriceCents:                 10_000,
		PrivacyPolicyVersion:       "privacy-v1",
		Contact:                    validPaidRegistrationContact(),
	}
	prepared, err := registration.PrepareRegistrationSubmission(
		command.SessionID,
		submission,
	)
	if err != nil {
		t.Fatalf("PrepareRegistrationSubmission() error = %v", err)
	}
	now := time.Date(2026, time.September, 12, 6, 0, 0, 0, time.UTC)
	existing := existingPaidRegistrationContext(t, command, now)
	registrar, tx, capture := newPaidRegistrarHarness(
		t,
		command,
		paidRegistrarScenario{
			existingContext:     &existing,
			existingFingerprint: &prepared.RequestFingerprint,
			seriesStatus:        activity.SeriesStatusArchived,
			sessionStatus:       activity.SessionStatusCancelled,
		},
	)

	got, found, err := registrar.Replay(
		context.Background(),
		ReplayPaidRegistrationCommand{
			TenantID:       command.TenantID,
			SessionID:      command.SessionID,
			PrincipalID:    command.PrincipalID,
			IdempotencyKey: command.IdempotencyKey,
			Submission:     submission,
		},
	)
	if err != nil || !found || !reflect.DeepEqual(got, existing) {
		t.Fatalf("Replay() = %+v, %t, %v", got, found, err)
	}
	if !tx.committed || tx.rolledBack || tx.generationLockCalls != 0 ||
		len(capture.lockOrder) != 0 || capture.registrationCreates != 0 {
		t.Fatalf("replay transaction=%+v capture=%+v", tx, capture)
	}
}

func TestPaidRegistrarFencesStaleGenerationBeforeBusinessLocks(t *testing.T) {
	t.Parallel()

	command := validPaidRegistrationCommand()
	registrar, tx, capture := newPaidRegistrarHarness(t, command, paidRegistrarScenario{})
	tx.generationErr = ErrPaidRegistrationGenerationInactive

	_, err := registrar.Start(context.Background(), command)
	if !errors.Is(err, ErrPaidRegistrationGenerationInactive) ||
		tx.generationLockCalls != 1 || len(capture.lockOrder) != 0 ||
		capture.registrationCreates != 0 || tx.committed || !tx.rolledBack {
		t.Fatalf("Start(stale generation) error=%v tx=%+v capture=%+v", err, tx, capture)
	}
}

func TestPaidRegistrarRejectsStaleSubmissionBeforeWrites(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name          string
		scenario      paidRegistrarScenario
		mutate        func(*registration.RegistrationSubmission)
		questionnaire bool
		want          error
	}{
		{
			name: "privacy policy changed",
			mutate: func(value *registration.RegistrationSubmission) {
				value.PrivacyPolicyVersion = "privacy-v2"
			},
			want: ErrPaidRegistrationPrivacyPolicyConflict,
		},
		{
			name:     "Brand suspended",
			scenario: paidRegistrarScenario{brandStatus: activity.BrandLifecycleSuspended},
			want:     ErrPaidRegistrationUnavailable,
		},
		{
			name:     "Instance publication changed",
			scenario: paidRegistrarScenario{publicationVersion: 2},
			want:     ErrPaidRegistrationTransactionConflict,
		},
		{
			name:     "price changed",
			scenario: paidRegistrarScenario{priceCents: int64TestPointer(11_000)},
			want:     ErrPaidRegistrationTransactionConflict,
		},
		{
			name:          "questionnaire changed",
			questionnaire: true,
			mutate: func(value *registration.RegistrationSubmission) {
				other := uuid.New()
				value.QuestionnaireVersionID = &other
			},
			want: ErrPaidRegistrationQuestionnaireConflict,
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			command := validPaidRegistrationCommand()
			submission := registration.RegistrationSubmission{
				InstancePublicationVersion: 1,
				PriceCents:                 10_000,
				PrivacyPolicyVersion:       "privacy-v1",
				Contact:                    validPaidRegistrationContact(),
			}
			if test.questionnaire {
				questionnaire := paidRegistrationQuestionnaire(command)
				questionnaireID := questionnaire.QuestionnaireVersionID
				submission.QuestionnaireVersionID = &questionnaireID
				submission.Answers = []activity.QuestionnaireAnswer{{
					FieldID: questionnaire.Fields[0].FieldID,
					Values:  []string{"production"},
				}}
				test.scenario.questionnaire = &questionnaire
			}
			if test.mutate != nil {
				test.mutate(&submission)
			}
			command.Submission = &submission
			registrar, tx, capture := newPaidRegistrarHarness(
				t,
				command,
				test.scenario,
			)
			_, err := registrar.Start(context.Background(), command)
			if !errors.Is(err, test.want) {
				t.Fatalf("Start() error = %v, want %v", err, test.want)
			}
			if tx.committed || !tx.rolledBack ||
				capture.registrationCreates != 0 || capture.orderCreates != 0 ||
				capture.holdCreates != 0 || capture.snapshots != 0 {
				t.Fatalf("transaction=%+v capture=%+v", tx, capture)
			}
		})
	}
}

func TestPaidRegistrarRollsBackSubmissionEvidenceFailures(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		scenario paidRegistrarScenario
	}{
		{
			name: "snapshot write",
			scenario: paidRegistrarScenario{
				snapshotErr: errors.New("snapshot unavailable"),
			},
		},
		{
			name: "answer write",
			scenario: paidRegistrarScenario{
				answerErr: errors.New("answer unavailable"),
			},
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			command := validPaidRegistrationCommand()
			questionnaire := paidRegistrationQuestionnaire(command)
			questionnaireID := questionnaire.QuestionnaireVersionID
			command.Submission = &registration.RegistrationSubmission{
				InstancePublicationVersion: 1,
				PriceCents:                 10_000,
				PrivacyPolicyVersion:       "privacy-v1",
				Contact:                    validPaidRegistrationContact(),
				QuestionnaireVersionID:     &questionnaireID,
				Answers: []activity.QuestionnaireAnswer{{
					FieldID: questionnaire.Fields[0].FieldID,
					Values:  []string{"production"},
				}},
			}
			test.scenario.questionnaire = &questionnaire
			registrar, tx, capture := newPaidRegistrarHarness(
				t,
				command,
				test.scenario,
			)
			if _, err := registrar.Start(context.Background(), command); err == nil {
				t.Fatal("Start() error = nil")
			}
			if tx.committed || !tx.rolledBack ||
				capture.registrationCreates != 1 || capture.orderCreates != 1 ||
				capture.holdCreates != 1 || capture.holdIncrements != 0 {
				t.Fatalf("transaction=%+v capture=%+v", tx, capture)
			}
		})
	}
}

func TestPaidRegistrarHoldsSelectedCouponAndDerivesDiscount(t *testing.T) {
	t.Parallel()

	command := validPaidRegistrationCommand()
	now := time.Date(2026, time.September, 12, 6, 0, 0, 0, time.UTC)
	couponRepository := &fakeCouponOrderRepository{
		ledger: paidRegistrarCouponLedger(t, command, now, 1_000),
	}
	couponID := couponRepository.ledger.Instrument.ID
	command.CouponID = &couponID
	registrar, tx, capture := newPaidRegistrarHarness(t, command, paidRegistrarScenario{
		couponRepository: couponRepository,
	})

	created, err := registrar.Start(context.Background(), command)
	if err != nil {
		t.Fatalf("Start(Coupon) error = %v", err)
	}
	if !tx.committed || created.Coupon == nil ||
		created.Coupon.Instrument.ID != couponID ||
		created.Coupon.Entry.EntryType != coupon.EntryTypeHeld ||
		created.Payment.Order.DiscountCents != 1_000 ||
		created.Payment.Order.PayableCents != 9_000 ||
		len(couponRepository.appends) != 1 || capture.holdIncrements != 1 {
		t.Fatalf("Start(Coupon) = %+v capture=%+v appends=%+v", created, capture, couponRepository.appends)
	}
}

func TestPaidRegistrarSettlesFullyDiscountedOrderWithoutProviderFact(t *testing.T) {
	t.Parallel()

	command := validPaidRegistrationCommand()
	now := time.Date(2026, time.September, 12, 6, 0, 0, 0, time.UTC)
	couponRepository := &fakeCouponOrderRepository{
		ledger: paidRegistrarCouponLedger(t, command, now, 10_000),
	}
	couponID := couponRepository.ledger.Instrument.ID
	command.CouponID = &couponID
	registrar, tx, capture := newPaidRegistrarHarness(t, command, paidRegistrarScenario{
		couponRepository: couponRepository,
	})

	created, err := registrar.Start(context.Background(), command)
	if err != nil {
		t.Fatalf("Start(settled_zero) error = %v", err)
	}
	if !tx.committed || tx.rolledBack ||
		created.Registration.ParticipationStatus != registration.ParticipationStatusConfirmed ||
		created.Payment.Order.PaymentStatus != payment.OrderStatusSettledZero ||
		created.Payment.Order.PayableCents != 0 ||
		created.Payment.Order.ActualPaidCents != nil ||
		created.Payment.Order.WeChatTransactionID != nil ||
		created.Payment.Hold.HoldStatus != payment.CapacityHoldStatusConverted ||
		created.Coupon == nil ||
		created.Coupon.Entry.EntryType != coupon.EntryTypeRedeemed ||
		len(couponRepository.appends) != 2 ||
		capture.holdIncrements != 0 || capture.zeroCapacityConfirms != 1 ||
		capture.historyIncrements != 1 || capture.registrationUpdates != 1 ||
		capture.orderUpdates != 1 || capture.holdUpdates != 1 {
		t.Fatalf("Start(settled_zero) = %+v capture=%+v appends=%+v", created, capture, couponRepository.appends)
	}
}

func TestPaidRegistrarReplaysCompleteContextWithoutExtendingHold(t *testing.T) {
	t.Parallel()

	command := validPaidRegistrationCommand()
	now := time.Date(2026, time.September, 12, 6, 0, 0, 0, time.UTC)
	existing := existingPaidRegistrationContext(t, command, now)
	registrar, tx, capture := newPaidRegistrarHarness(t, command, paidRegistrarScenario{
		existingContext: &existing,
	})

	got, err := registrar.Start(context.Background(), command)
	if err != nil {
		t.Fatalf("Start(replay) error = %v", err)
	}
	if !reflect.DeepEqual(got, existing) {
		t.Fatalf("Start(replay) = %+v, want %+v", got, existing)
	}
	if !tx.committed || tx.rolledBack ||
		len(capture.lockOrder) != 0 ||
		capture.registrationCreates != 0 ||
		capture.orderCreates != 0 ||
		capture.holdCreates != 0 ||
		capture.holdIncrements != 0 {
		t.Fatalf("replay transaction/capture = %+v %+v", tx, capture)
	}
}

func TestPaidRegistrarRejectsIdempotencyReuse(t *testing.T) {
	t.Parallel()

	command := validPaidRegistrationCommand()
	now := time.Date(2026, time.September, 12, 6, 0, 0, 0, time.UTC)
	existing := existingPaidRegistrationContext(t, command, now)
	existing.Registration.SessionID = uuid.New()
	registrar, tx, _ := newPaidRegistrarHarness(t, command, paidRegistrarScenario{
		existingContext: &existing,
	})

	if _, err := registrar.Start(context.Background(), command); !errors.Is(
		err,
		ErrPaidRegistrationIdempotencyConflict,
	) {
		t.Fatalf("Start() error = %v", err)
	}
	if tx.committed || !tx.rolledBack {
		t.Fatalf("transaction committed=%t rolledBack=%t", tx.committed, tx.rolledBack)
	}

	orphanOrder := existing.Payment.Order
	registrar, tx, _ = newPaidRegistrarHarness(t, command, paidRegistrarScenario{
		orphanOrderByIdempotency: &orphanOrder,
	})
	if _, err := registrar.Start(context.Background(), command); !errors.Is(
		err,
		ErrPaidRegistrationIdempotencyConflict,
	) {
		t.Fatalf("Start(orphan Order) error = %v", err)
	}
	if tx.committed || !tx.rolledBack {
		t.Fatalf("orphan transaction committed=%t rolledBack=%t", tx.committed, tx.rolledBack)
	}
}

func TestPaidRegistrarRejectsIncompleteReplayContext(t *testing.T) {
	t.Parallel()

	command := validPaidRegistrationCommand()
	now := time.Date(2026, time.September, 12, 6, 0, 0, 0, time.UTC)
	existing := existingPaidRegistrationContext(t, command, now)
	registrar, tx, _ := newPaidRegistrarHarness(t, command, paidRegistrarScenario{
		existingContext:    &existing,
		replayOrderMissing: true,
	})
	if _, err := registrar.Start(context.Background(), command); !errors.Is(
		err,
		ErrPaidRegistrationTransactionConflict,
	) {
		t.Fatalf("Start(missing Order) error = %v", err)
	}
	if tx.committed || !tx.rolledBack {
		t.Fatalf("transaction committed=%t rolledBack=%t", tx.committed, tx.rolledBack)
	}

	registrar, tx, _ = newPaidRegistrarHarness(t, command, paidRegistrarScenario{
		existingContext:   &existing,
		replayHoldMissing: true,
	})
	if _, err := registrar.Start(context.Background(), command); !errors.Is(
		err,
		ErrPaidRegistrationTransactionConflict,
	) {
		t.Fatalf("Start(missing hold) error = %v", err)
	}
	if tx.committed || !tx.rolledBack {
		t.Fatalf("transaction committed=%t rolledBack=%t", tx.committed, tx.rolledBack)
	}
}

func TestPaidRegistrarRejectsOpenFreeOrUnavailableSession(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		scenario paidRegistrarScenario
		wantErr  error
	}{
		{
			name:     "open Registration",
			scenario: paidRegistrarScenario{openRegistration: true},
			wantErr:  ErrPaidRegistrationAlreadyOpen,
		},
		{
			name:     "free Session",
			scenario: paidRegistrarScenario{priceCents: int64TestPointer(0)},
			wantErr:  ErrPaidRegistrationFreeSession,
		},
		{
			name:     "missing price",
			scenario: paidRegistrarScenario{missingPrice: true},
			wantErr:  ErrPaidRegistrationUnavailable,
		},
		{
			name:     "full",
			scenario: paidRegistrarScenario{confirmedCount: intTestPointer(9)},
			wantErr:  ErrPaidRegistrationUnavailable,
		},
		{
			name:     "archived Series",
			scenario: paidRegistrarScenario{seriesStatus: activity.SeriesStatusArchived},
			wantErr:  ErrPaidRegistrationUnavailable,
		},
		{
			name:     "completed Instance",
			scenario: paidRegistrarScenario{instanceStatus: activity.InstanceStatusCompleted},
			wantErr:  ErrPaidRegistrationUnavailable,
		},
		{
			name:     "cancelled Session",
			scenario: paidRegistrarScenario{sessionStatus: activity.SessionStatusCancelled},
			wantErr:  ErrPaidRegistrationUnavailable,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			command := validPaidRegistrationCommand()
			registrar, tx, capture := newPaidRegistrarHarness(t, command, test.scenario)
			if _, err := registrar.Start(context.Background(), command); !errors.Is(err, test.wantErr) {
				t.Fatalf("Start() error = %v, want %v", err, test.wantErr)
			}
			if tx.committed || !tx.rolledBack ||
				capture.registrationCreates != 0 ||
				capture.orderCreates != 0 ||
				capture.holdCreates != 0 {
				t.Fatalf("transaction/capture = %+v %+v", tx, capture)
			}
		})
	}
}

func TestPaidRegistrarValidatesCommandAndPriceSnapshot(t *testing.T) {
	t.Parallel()

	command := validPaidRegistrationCommand()
	command.SessionID = uuid.Nil
	starter := &fakePaidRegistrationTransactionStarter{}
	registrar := &PaidRegistrar{
		transactions: starter,
		now:          time.Now,
		generationID: uuid.New(),
	}
	if _, err := registrar.Start(context.Background(), command); !errors.Is(
		err,
		ErrInvalidPaidRegistrationCommand,
	) {
		t.Fatalf("Start(invalid identity) error = %v", err)
	}
	if starter.begins != 0 {
		t.Fatalf("invalid identity began %d transaction(s)", starter.begins)
	}

	command = validPaidRegistrationCommand()
	nilCouponID := uuid.Nil
	command.CouponID = &nilCouponID
	if _, err := registrar.Start(context.Background(), command); !errors.Is(
		err,
		ErrInvalidPaidRegistrationCommand,
	) {
		t.Fatalf("Start(invalid Coupon) error = %v", err)
	}
	if starter.begins != 0 {
		t.Fatalf("invalid Coupon began %d transaction(s)", starter.begins)
	}
}

func TestPaidRegistrarRollsBackCapacityOrWriteFailure(t *testing.T) {
	t.Parallel()

	writeFailure := errors.New("write failed")
	tests := []struct {
		name     string
		scenario paidRegistrarScenario
		wantErr  error
	}{
		{
			name:     "Registration create",
			scenario: paidRegistrarScenario{registrationCreateErr: writeFailure},
			wantErr:  writeFailure,
		},
		{
			name:     "Order create",
			scenario: paidRegistrarScenario{orderCreateErr: writeFailure},
			wantErr:  writeFailure,
		},
		{
			name:     "hold create",
			scenario: paidRegistrarScenario{holdCreateErr: writeFailure},
			wantErr:  writeFailure,
		},
		{
			name:     "capacity",
			scenario: paidRegistrarScenario{holdIncrementErr: sql.ErrNoRows},
			wantErr:  ErrPaidRegistrationCapacityConflict,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			command := validPaidRegistrationCommand()
			registrar, tx, _ := newPaidRegistrarHarness(t, command, test.scenario)
			if _, err := registrar.Start(context.Background(), command); !errors.Is(err, test.wantErr) {
				t.Fatalf("Start() error = %v, want %v", err, test.wantErr)
			}
			if tx.committed || !tx.rolledBack {
				t.Fatalf("transaction committed=%t rolledBack=%t", tx.committed, tx.rolledBack)
			}
		})
	}
}

func TestPaidRegistrarPropagatesBeginAndCommitFailures(t *testing.T) {
	t.Parallel()

	command := validPaidRegistrationCommand()
	beginFailure := errors.New("begin failed")
	starter := &fakePaidRegistrationTransactionStarter{err: beginFailure}
	registrar := &PaidRegistrar{
		transactions: starter,
		now:          time.Now,
		generationID: uuid.New(),
	}
	if _, err := registrar.Start(context.Background(), command); !errors.Is(err, beginFailure) {
		t.Fatalf("Start(begin failure) error = %v", err)
	}

	commitFailure := errors.New("commit failed")
	registrar, tx, capture := newPaidRegistrarHarness(t, command, paidRegistrarScenario{
		commitErr: commitFailure,
	})
	if _, err := registrar.Start(context.Background(), command); !errors.Is(err, commitFailure) {
		t.Fatalf("Start(commit failure) error = %v", err)
	}
	if tx.committed || !tx.rolledBack || capture.holdIncrements != 1 {
		t.Fatalf("transaction/capture = %+v %+v", tx, capture)
	}
}

func TestPaidRegistrationDatabaseErrorsAreClassified(t *testing.T) {
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
				ConstraintName: "xiangwan_orders_tenant_idempotency_key",
			},
			classifier: classifyPaidRegistrationWriteError,
			want:       ErrPaidRegistrationIdempotencyConflict,
		},
		{
			name: "open Registration",
			err: &pgconn.PgError{
				Code:           "23505",
				ConstraintName: "uq_xiangwan_registrations_open_principal_session",
			},
			classifier: classifyPaidRegistrationWriteError,
			want:       ErrPaidRegistrationAlreadyOpen,
		},
		{
			name: "other unique",
			err: &pgconn.PgError{
				Code:           "23505",
				ConstraintName: "xiangwan_holds_tenant_order_key",
			},
			classifier: classifyPaidRegistrationWriteError,
			want:       ErrPaidRegistrationTransactionConflict,
		},
		{
			name: "merchant config generation trigger",
			err: &pgconn.PgError{
				Code:    "23514",
				Message: "xiangwan merchant config generation identity is not active for payment facts",
			},
			classifier: classifyPaidRegistrationWriteError,
			want:       ErrPaymentMerchantConfigUnavailable,
		},
		{
			name:       "serialization",
			err:        &pgconn.PgError{Code: "40001"},
			classifier: classifyPaidRegistrationCommitError,
			want:       ErrPaidRegistrationTransactionConflict,
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

func TestClassifyPaidRegistrationWriteErrorPreservesUnrelatedCheckViolation(t *testing.T) {
	t.Parallel()

	err := &pgconn.PgError{Code: "23514", Message: "some other invariant"}
	if got := classifyPaidRegistrationWriteError(err); got != err {
		t.Fatalf("classifyPaidRegistrationWriteError() = %v, want original error", got)
	}
}

type paidRegistrarScenario struct {
	existingContext          *PaidRegistrationContext
	existingFingerprint      *string
	orphanOrderByIdempotency *payment.Order
	replayOrderMissing       bool
	replayHoldMissing        bool
	openRegistration         bool
	missingPrice             bool
	priceCents               *int64
	confirmedCount           *int
	brandStatus              activity.BrandLifecycleStatus
	seriesStatus             activity.SeriesStatus
	instanceStatus           activity.InstanceStatus
	publicationVersion       int64
	sessionStatus            activity.SessionStatus
	questionnaire            *activity.SessionQuestionnaire
	registrationCreateErr    error
	orderCreateErr           error
	holdCreateErr            error
	holdIncrementErr         error
	snapshotErr              error
	answerErr                error
	commitErr                error
	couponRepository         couponOrderRepository
}

type paidRegistrarCapture struct {
	isolation                    sql.IsolationLevel
	lockOrder                    []string
	registrationCreates          int
	orderCreates                 int
	holdCreates                  int
	snapshots                    int
	snapshotPrivacyPolicyVersion string
	answers                      int
	holdIncrements               int
	holdIncrementArgs            []any
	zeroCapacityConfirms         int
	historyIncrements            int
	registrationUpdates          int
	orderUpdates                 int
	holdUpdates                  int
	registration                 registration.Registration
	order                        payment.Order
	hold                         payment.CapacityHold
}

func newPaidRegistrarHarness(
	t *testing.T,
	command StartPaidRegistrationCommand,
	scenario paidRegistrarScenario,
) (*PaidRegistrar, *fakePaidRegistrationTransaction, *paidRegistrarCapture) {
	t.Helper()

	now := time.Date(2026, time.September, 12, 6, 0, 0, 0, time.UTC)
	registrationStartAt := now.Add(-time.Hour)
	registrationEndAt := now.Add(time.Hour)
	sessionStartAt := now.Add(2 * time.Hour)
	sessionEndAt := now.Add(4 * time.Hour)
	priceCents := int64(10_000)
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

	capture := &paidRegistrarCapture{}
	tx := &fakePaidRegistrationTransaction{
		commitErr: scenario.commitErr,
		coupons:   scenario.couponRepository,
	}
	tx.fakeQueryExecutor = &fakeQueryExecutor{queryRow: func(query string, args ...any) rowScanner {
		switch {
		case strings.Contains(query, "FROM xiangwan_registrations") &&
			strings.Contains(query, "idempotency_key = $2"):
			if scenario.existingContext == nil {
				return &fakeRow{err: sql.ErrNoRows}
			}
			return &fakeRow{values: paidRegistrationScanValues(scenario.existingContext.Registration)}
		case strings.Contains(query, "SELECT request_fingerprint"):
			if scenario.existingFingerprint == nil {
				return &fakeRow{err: sql.ErrNoRows}
			}
			return &fakeRow{values: []any{*scenario.existingFingerprint}}
		case strings.Contains(query, "FROM xiangwan_orders") &&
			strings.Contains(query, "idempotency_key = $2"):
			if scenario.orphanOrderByIdempotency == nil {
				return &fakeRow{err: sql.ErrNoRows}
			}
			return &fakeRow{values: orderScanValues(*scenario.orphanOrderByIdempotency)}
		case strings.Contains(query, "FROM xiangwan_orders") &&
			strings.Contains(query, "registration_id = $2"):
			if scenario.replayOrderMissing || scenario.existingContext == nil {
				return &fakeRow{err: sql.ErrNoRows}
			}
			return &fakeRow{values: orderScanValues(scenario.existingContext.Payment.Order)}
		case strings.Contains(query, "FROM xiangwan_capacity_holds"):
			if scenario.replayHoldMissing || scenario.existingContext == nil {
				return &fakeRow{err: sql.ErrNoRows}
			}
			return &fakeRow{values: holdScanValues(scenario.existingContext.Payment.Hold)}
		case strings.Contains(query, "FROM xiangwan_brand_profiles"):
			capture.lockOrder = append(capture.lockOrder, "brand")
			return &fakeRow{values: []any{scenario.brandStatus}}
		case strings.Contains(query, "current_public_instance_id"):
			return &fakeRow{values: []any{scenario.publicationVersion}}
		case strings.Contains(query, "FROM xiangwan_activity_series"):
			capture.lockOrder = append(capture.lockOrder, "series")
			return &fakeRow{values: []any{scenario.seriesStatus, int64(4)}}
		case strings.Contains(query, "FROM xiangwan_activity_instances"):
			capture.lockOrder = append(capture.lockOrder, "instance")
			return &fakeRow{values: []any{
				scenario.instanceStatus,
				activity.ActivityTypeAIRoundtable,
			}}
		case strings.Contains(query, "FROM xiangwan_activity_sessions"):
			capture.lockOrder = append(capture.lockOrder, "session")
			price := sql.NullInt64{Int64: priceCents, Valid: !scenario.missingPrice}
			return &fakeRow{values: []any{
				scenario.sessionStatus,
				sql.NullTime{Time: registrationStartAt, Valid: true},
				sql.NullTime{Time: registrationEndAt, Valid: true},
				sql.NullTime{Time: sessionStartAt, Valid: true},
				sql.NullTime{Time: sessionEndAt, Valid: true},
				sql.NullInt64{Int64: 10, Valid: true},
				sql.NullInt64{Int64: 3, Valid: true},
				sql.NullInt64{Int64: 2, Valid: true},
				price,
				confirmedCount,
				1,
				int64(9),
			}}
		case strings.Contains(query, "participation_status IN"):
			if !scenario.openRegistration {
				return &fakeRow{err: sql.ErrNoRows}
			}
			return &fakeRow{values: []any{uuid.New()}}
		case strings.Contains(query, "JSONB_AGG"):
			if scenario.questionnaire == nil {
				return &fakeRow{err: sql.ErrNoRows}
			}
			return &fakeRow{values: paidQuestionnaireRegistrationScanValues(
				t,
				*scenario.questionnaire,
			)}
		case strings.Contains(query, "INSERT INTO xiangwan_registrations"):
			capture.registrationCreates++
			if scenario.registrationCreateErr != nil {
				return &fakeRow{err: scenario.registrationCreateErr}
			}
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
				CancelledAt:         args[9].(*time.Time),
				CancellationReason:  args[10].(*string),
				Version:             args[11].(int64),
				CreatedAt:           args[12].(time.Time),
				UpdatedAt:           args[13].(time.Time),
			}
			capture.registration = created
			return &fakeRow{values: paidRegistrationScanValues(created)}
		case strings.Contains(query, "INSERT INTO xiangwan_orders"):
			capture.orderCreates++
			if scenario.orderCreateErr != nil {
				return &fakeRow{err: scenario.orderCreateErr}
			}
			created := payment.Order{
				ID:                         args[0].(uuid.UUID),
				TenantID:                   args[1].(uuid.UUID),
				RegistrationID:             args[2].(uuid.UUID),
				SeriesID:                   args[3].(uuid.UUID),
				InstanceID:                 args[4].(uuid.UUID),
				SessionID:                  args[5].(uuid.UUID),
				PrincipalID:                args[6].(uuid.UUID),
				PaymentStatus:              args[7].(payment.OrderStatus),
				IdempotencyKey:             args[8].(string),
				MerchantOrderNo:            args[9].(string),
				PaymentAppID:               args[10].(string),
				PaymentMerchantID:          args[11].(string),
				MerchantConfigGenerationID: nullableUUIDValue(args[12]),
				OriginalPriceCents:         args[13].(int64),
				DiscountCents:              args[14].(int64),
				PayableCents:               args[15].(int64),
				ActualPaidCents:            args[16].(*int64),
				WeChatTransactionID:        args[17].(*string),
				PaidAt:                     args[18].(*time.Time),
				ClosedAt:                   args[19].(*time.Time),
				Version:                    args[20].(int64),
				CreatedAt:                  args[21].(time.Time),
				UpdatedAt:                  args[22].(time.Time),
			}
			capture.order = created
			return &fakeRow{values: orderScanValues(created)}
		case strings.Contains(query, "INSERT INTO xiangwan_capacity_holds"):
			capture.holdCreates++
			if scenario.holdCreateErr != nil {
				return &fakeRow{err: scenario.holdCreateErr}
			}
			created := payment.CapacityHold{
				ID:             args[0].(uuid.UUID),
				TenantID:       args[1].(uuid.UUID),
				OrderID:        args[2].(uuid.UUID),
				RegistrationID: args[3].(uuid.UUID),
				SessionID:      args[4].(uuid.UUID),
				HoldStatus:     args[5].(payment.CapacityHoldStatus),
				ExpiresAt:      args[6].(time.Time),
				ConvertedAt:    args[7].(*time.Time),
				ReleasedAt:     args[8].(*time.Time),
				ReleaseReason:  args[9].(*string),
				Version:        args[10].(int64),
				CreatedAt:      args[11].(time.Time),
				UpdatedAt:      args[12].(time.Time),
			}
			capture.hold = created
			return &fakeRow{values: holdScanValues(created)}
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
		case strings.Contains(query, "UPDATE xiangwan_orders"):
			capture.orderUpdates++
			updated := capture.order
			updated.PaymentStatus = args[2].(payment.OrderStatus)
			updated.ActualPaidCents = args[3].(*int64)
			updated.WeChatTransactionID = args[4].(*string)
			updated.PaidAt = args[5].(*time.Time)
			updated.ClosedAt = args[6].(*time.Time)
			updated.Version++
			updated.UpdatedAt = args[7].(time.Time)
			capture.order = updated
			return &fakeRow{values: orderScanValues(updated)}
		case strings.Contains(query, "UPDATE xiangwan_capacity_holds"):
			capture.holdUpdates++
			updated := capture.hold
			updated.HoldStatus = args[2].(payment.CapacityHoldStatus)
			updated.ConvertedAt = args[3].(*time.Time)
			updated.ReleasedAt = args[4].(*time.Time)
			updated.ReleaseReason = args[5].(*string)
			updated.Version++
			updated.UpdatedAt = args[6].(time.Time)
			capture.hold = updated
			return &fakeRow{values: holdScanValues(updated)}
		case strings.Contains(query, "UPDATE xiangwan_registrations"):
			capture.registrationUpdates++
			updated := capture.registration
			updated.ParticipationStatus = args[2].(registration.ParticipationStatus)
			updated.ConfirmedAt = args[3].(*time.Time)
			updated.CancelledAt = args[4].(*time.Time)
			updated.CancellationReason = args[5].(*string)
			updated.Version++
			updated.UpdatedAt = args[6].(time.Time)
			capture.registration = updated
			return &fakeRow{values: paidRegistrationScanValues(updated)}
		case strings.Contains(query, "UPDATE xiangwan_activity_series"):
			capture.historyIncrements++
			return &fakeRow{values: []any{int64(5)}}
		case strings.Contains(query, "UPDATE xiangwan_activity_sessions"):
			if strings.Contains(query, "confirmed_registration_count =") &&
				!strings.Contains(query, "active_hold_count = active_hold_count - 1") {
				capture.zeroCapacityConfirms++
			} else {
				capture.holdIncrements++
			}
			capture.holdIncrementArgs = append([]any(nil), args...)
			if scenario.holdIncrementErr != nil {
				return &fakeRow{err: scenario.holdIncrementErr}
			}
			return &fakeRow{values: []any{int64(10)}}
		default:
			t.Fatalf("unexpected query: %s", query)
			return &fakeRow{}
		}
	}}
	starter := &fakePaidRegistrationTransactionStarter{tx: tx, capture: capture}
	return &PaidRegistrar{
		transactions: starter,
		now:          func() time.Time { return now },
		generationID: uuid.New(),
	}, tx, capture
}

type fakePaidRegistrationTransactionStarter struct {
	tx      paidRegistrationTransaction
	err     error
	begins  int
	capture *paidRegistrarCapture
}

func (starter *fakePaidRegistrationTransactionStarter) beginTx(
	_ context.Context,
	options *sql.TxOptions,
) (paidRegistrationTransaction, error) {
	starter.begins++
	if starter.capture != nil {
		starter.capture.isolation = options.Isolation
	}
	if starter.err != nil {
		return nil, starter.err
	}
	return starter.tx, nil
}

type fakePaidRegistrationTransaction struct {
	*fakeQueryExecutor
	coupons             couponOrderRepository
	commitErr           error
	generationErr       error
	generationLockCalls int
	committed           bool
	rolledBack          bool
}

func (tx *fakePaidRegistrationTransaction) lockActiveGeneration(
	_ context.Context,
	_ uuid.UUID,
	_ uuid.UUID,
) error {
	tx.generationLockCalls++
	return tx.generationErr
}

func (tx *fakePaidRegistrationTransaction) couponOrderRepository() couponOrderRepository {
	if tx.coupons == nil {
		return emptyCouponOrderRepository{}
	}
	return tx.coupons
}

func (tx *fakePaidRegistrationTransaction) Commit() error {
	if tx.commitErr != nil {
		return tx.commitErr
	}
	tx.committed = true
	return nil
}

func (tx *fakePaidRegistrationTransaction) Rollback() error {
	tx.rolledBack = true
	return nil
}

func paidRegistrationScanValues(value registration.Registration) []any {
	return []any{
		value.ID,
		value.TenantID,
		value.SeriesID,
		value.InstanceID,
		value.SessionID,
		value.PrincipalID,
		value.ParticipationStatus,
		value.IdempotencyKey,
		nullTime(value.ConfirmedAt),
		nullTime(value.CancelledAt),
		nullString(value.CancellationReason),
		value.Version,
		value.CreatedAt,
		value.UpdatedAt,
	}
}

func existingPaidRegistrationContext(
	t *testing.T,
	command StartPaidRegistrationCommand,
	now time.Time,
) PaidRegistrationContext {
	t.Helper()

	value, err := registration.NewRegistration(registration.NewRegistrationCommand{
		TenantID:        command.TenantID,
		SeriesID:        command.SeriesID,
		InstanceID:      command.InstanceID,
		SessionID:       command.SessionID,
		PrincipalID:     command.PrincipalID,
		IdempotencyKey:  command.IdempotencyKey,
		RequiresPayment: true,
		Now:             now,
	})
	if err != nil {
		t.Fatalf("NewRegistration() error = %v", err)
	}
	paymentContext, err := payment.NewPaymentContext(payment.NewPaymentContextCommand{
		TenantID:           command.TenantID,
		RegistrationID:     value.ID,
		SeriesID:           command.SeriesID,
		InstanceID:         command.InstanceID,
		SessionID:          command.SessionID,
		PrincipalID:        command.PrincipalID,
		IdempotencyKey:     command.IdempotencyKey,
		MerchantOrderNo:    command.MerchantOrderNo,
		PaymentAppID:       command.PaymentAppID,
		PaymentMerchantID:  command.PaymentMerchantID,
		OriginalPriceCents: 10_000,
		DiscountCents:      0,
		Now:                now,
	})
	if err != nil {
		t.Fatalf("NewPaymentContext() error = %v", err)
	}
	return PaidRegistrationContext{Registration: value, Payment: paymentContext}
}

func validPaidRegistrationCommand() StartPaidRegistrationCommand {
	return StartPaidRegistrationCommand{
		TenantID:             uuid.New(),
		SeriesID:             uuid.New(),
		InstanceID:           uuid.New(),
		SessionID:            uuid.New(),
		PrincipalID:          uuid.New(),
		IdempotencyKey:       "registration:paid:1",
		MerchantOrderNo:      "merchant-order-paid-1",
		PaymentAppID:         "wx-app-1",
		PaymentMerchantID:    "wx-merchant-1",
		PrivacyPolicyVersion: "privacy-v1",
		ManualContactEnabled: true,
		ContactPolicyVersion: "contact-v1",
	}
}

func validPaidRegistrationContact() registration.ContactSnapshot {
	return registration.ContactSnapshot{
		Source:        registration.ContactSourceManual,
		Name:          "Wang Wei",
		PhoneE164:     "+8613812345678",
		PolicyVersion: "contact-v1",
	}
}

func paidRegistrationQuestionnaire(
	command StartPaidRegistrationCommand,
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
			5,
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
				{Code: "production", Label: "生产使用"},
			},
		}},
	}
}

func paidQuestionnaireRegistrationScanValues(
	t *testing.T,
	questionnaire activity.SessionQuestionnaire,
) []any {
	t.Helper()
	fields := make(
		[]paidRegistrationQuestionnaireFieldJSON,
		0,
		len(questionnaire.Fields),
	)
	for _, field := range questionnaire.Fields {
		fields = append(fields, paidRegistrationQuestionnaireFieldJSON{
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
		t.Fatalf("encode paid questionnaire fixture: %v", err)
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

func intTestPointer(value int) *int {
	return &value
}

func int64TestPointer(value int64) *int64 {
	return &value
}

type emptyCouponOrderRepository struct{}

func (emptyCouponOrderRepository) GetLedgerForUpdate(
	context.Context,
	uuid.UUID,
	uuid.UUID,
	uuid.UUID,
) (couponpostgres.Ledger, error) {
	return couponpostgres.Ledger{}, couponpostgres.ErrCouponNotFound
}

func (emptyCouponOrderRepository) GetLedgerByOrderForUpdate(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (couponpostgres.Ledger, error) {
	return couponpostgres.Ledger{}, couponpostgres.ErrCouponNotFound
}

func (emptyCouponOrderRepository) AppendLifecycleEntry(
	context.Context,
	coupon.Entry,
) (coupon.Entry, error) {
	return coupon.Entry{}, errors.New("unexpected Coupon ledger write")
}

type fakeCouponOrderRepository struct {
	ledger  couponpostgres.Ledger
	appends []coupon.Entry
}

func (repository *fakeCouponOrderRepository) GetLedgerForUpdate(
	_ context.Context,
	tenantID uuid.UUID,
	principalID uuid.UUID,
	couponID uuid.UUID,
) (couponpostgres.Ledger, error) {
	if repository.ledger.Instrument.TenantID != tenantID ||
		repository.ledger.Instrument.PrincipalID != principalID ||
		repository.ledger.Instrument.ID != couponID {
		return couponpostgres.Ledger{}, couponpostgres.ErrCouponNotFound
	}
	return repository.ledger, nil
}

func (repository *fakeCouponOrderRepository) GetLedgerByOrderForUpdate(
	_ context.Context,
	tenantID uuid.UUID,
	orderID uuid.UUID,
) (couponpostgres.Ledger, error) {
	if repository.ledger.Instrument.TenantID != tenantID {
		return couponpostgres.Ledger{}, couponpostgres.ErrCouponNotFound
	}
	for _, entry := range repository.ledger.Entries {
		if entry.EntryType == coupon.EntryTypeHeld &&
			entry.OrderID != nil && *entry.OrderID == orderID {
			return repository.ledger, nil
		}
	}
	return couponpostgres.Ledger{}, couponpostgres.ErrCouponNotFound
}

func (repository *fakeCouponOrderRepository) AppendLifecycleEntry(
	_ context.Context,
	entry coupon.Entry,
) (coupon.Entry, error) {
	repository.ledger.Entries = append(repository.ledger.Entries, entry)
	repository.appends = append(repository.appends, entry)
	return entry, nil
}

func paidRegistrarCouponLedger(
	t *testing.T,
	command StartPaidRegistrationCommand,
	now time.Time,
	faceValueCents int64,
) couponpostgres.Ledger {
	t.Helper()
	activityType := activity.ActivityTypeAIRoundtable
	grant, err := coupon.NewManualReplenishment(coupon.ManualReplenishmentCommand{
		TenantID:    command.TenantID,
		PrincipalID: command.PrincipalID,
		ActorID:     uuid.New(),
		BusinessKey: "manual:paid-registrar-test",
		Reason:      "paid registrar test Coupon",
		Context:     "paid-registrar-test",
		Policy: coupon.GrantPolicy{
			Configured:        true,
			PolicyVersion:     "paid-registrar-v1",
			FaceValueCents:    faceValueCents,
			Validity:          time.Hour,
			ScopeType:         coupon.ScopeTypeActivityType,
			ScopeActivityType: &activityType,
			MinimumOrderCents: faceValueCents,
		},
		GrantedAt:  now.Add(-time.Minute),
		RecordedAt: now.Add(-time.Minute),
	})
	if err != nil {
		t.Fatalf("NewManualReplenishment() error = %v", err)
	}
	return couponpostgres.Ledger{
		Instrument: grant.Coupons[0],
		Entries:    []coupon.Entry{grant.Entries[0]},
	}
}

func nullableUUIDValue(value any) uuid.UUID {
	switch typed := value.(type) {
	case uuid.NullUUID:
		if typed.Valid {
			return typed.UUID
		}
	case *uuid.NullUUID:
		if typed != nil && typed.Valid {
			return typed.UUID
		}
	case uuid.UUID:
		return typed
	case nil:
		return uuid.Nil
	}
	return uuid.Nil
}
