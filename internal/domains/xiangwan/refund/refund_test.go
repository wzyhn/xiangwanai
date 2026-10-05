package refund

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNewCaseCreatesPendingManualFinancialSnapshot(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 12, 8, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	created, err := NewCase(validNewCaseCommand(now))
	if err != nil {
		t.Fatalf("NewCase() error = %v", err)
	}
	if created.RefundStatus != StatusPendingManual ||
		created.RequestedRefundCents != 9_000 ||
		created.SuccessfulRefundCents != 0 ||
		created.Version != 1 ||
		!created.CreatedAt.Equal(now.UTC()) {
		t.Fatalf("NewCase() = %+v", created)
	}
}

func TestNewCaseRejectsInvalidIdentityAmountAndReason(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 12, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		mutate func(*NewCaseCommand)
	}{
		{name: "missing Order", mutate: func(command *NewCaseCommand) {
			command.OrderID = uuid.Nil
		}},
		{name: "unknown reason", mutate: func(command *NewCaseCommand) {
			command.ReasonCode = "unknown"
		}},
		{name: "invalid idempotency", mutate: func(command *NewCaseCommand) {
			command.IdempotencyKey = "invalid key"
		}},
		{name: "zero request", mutate: func(command *NewCaseCommand) {
			command.RequestedRefundCents = 0
		}},
		{name: "more than paid", mutate: func(command *NewCaseCommand) {
			command.RequestedRefundCents = command.ActualPaidCents + 1
		}},
		{name: "surrounding note whitespace", mutate: func(command *NewCaseCommand) {
			command.OperatorNote = " note "
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			command := validNewCaseCommand(now)
			test.mutate(&command)
			if _, err := NewCase(command); !errors.Is(err, ErrInvalidRefund) {
				t.Fatalf("NewCase() error = %v, want ErrInvalidRefund", err)
			}
		})
	}
}

func TestRefundProcessingFailureRetryAndCompletion(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 12, 0, 0, 0, 0, time.UTC)
	current, err := NewCase(validNewCaseCommand(now))
	if err != nil {
		t.Fatalf("NewCase() error = %v", err)
	}
	operatorID := uuid.New()
	processingCommand := StartProcessingCommand{
		HandledBy:    operatorID,
		OperatorNote: "opened merchant console",
		At:           now.Add(time.Minute),
	}
	processing, changed, err := StartProcessing(current, processingCommand)
	if err != nil || !changed || processing.RefundStatus != StatusProcessing {
		t.Fatalf("StartProcessing() = %+v, %v, %v", processing, changed, err)
	}
	replayed, changed, err := StartProcessing(processing, processingCommand)
	if err != nil || changed || replayed.HandledBy == processing.HandledBy {
		t.Fatalf("StartProcessing(replay) = %+v, %v, %v", replayed, changed, err)
	}

	failed, changed, err := Fail(processing, ResolveFailureCommand{
		HandledBy:     operatorID,
		FailureReason: "merchant console unavailable",
		OperatorNote:  "retry later",
		At:            now.Add(2 * time.Minute),
	})
	if err != nil || !changed || failed.RefundStatus != StatusFailed {
		t.Fatalf("Fail() = %+v, %v, %v", failed, changed, err)
	}
	retrying, changed, err := StartProcessing(failed, StartProcessingCommand{
		HandledBy:    operatorID,
		OperatorNote: "second attempt",
		At:           now.Add(3 * time.Minute),
	})
	if err != nil || !changed || retrying.FailureReason != nil || retrying.ResolvedAt != nil {
		t.Fatalf("StartProcessing(retry) = %+v, %v, %v", retrying, changed, err)
	}
	refunded, changed, err := Complete(retrying, CompleteCommand{
		HandledBy:         operatorID,
		ExternalRefundID:  "wx-refund-1",
		EvidenceReference: "merchant://refund/1",
		OperatorNote:      "completed",
		At:                now.Add(4 * time.Minute),
	})
	if err != nil || !changed ||
		refunded.RefundStatus != StatusRefunded ||
		refunded.SuccessfulRefundCents != refunded.RequestedRefundCents {
		t.Fatalf("Complete() = %+v, %v, %v", refunded, changed, err)
	}
	replayed, changed, err = Complete(refunded, CompleteCommand{
		HandledBy:         operatorID,
		ExternalRefundID:  "wx-refund-1",
		EvidenceReference: "merchant://refund/1",
		OperatorNote:      "completed",
		At:                now.Add(4 * time.Minute),
	})
	if err != nil || changed || replayed.ExternalRefundID == refunded.ExternalRefundID {
		t.Fatalf("Complete(replay) = %+v, %v, %v", replayed, changed, err)
	}
}

func TestRefundCompletionRequiresEvidence(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 12, 0, 0, 0, 0, time.UTC)
	current, err := NewCase(validNewCaseCommand(now))
	if err != nil {
		t.Fatalf("NewCase() error = %v", err)
	}
	if _, _, err := Complete(current, CompleteCommand{
		HandledBy: uuid.New(),
		At:        now.Add(time.Minute),
	}); !errors.Is(err, ErrInvalidRefund) {
		t.Fatalf("Complete() error = %v, want ErrInvalidRefund", err)
	}
}

func TestRefundRejectedIsTerminalAndReplaySafe(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 12, 0, 0, 0, 0, time.UTC)
	current, err := NewCase(validNewCaseCommand(now))
	if err != nil {
		t.Fatalf("NewCase() error = %v", err)
	}
	command := ResolveFailureCommand{
		HandledBy:     uuid.New(),
		FailureReason: "policy rejected",
		OperatorNote:  "manual decision",
		At:            now.Add(time.Minute),
	}
	rejected, changed, err := Reject(current, command)
	if err != nil || !changed || rejected.RefundStatus != StatusRejected {
		t.Fatalf("Reject() = %+v, %v, %v", rejected, changed, err)
	}
	replayed, changed, err := Reject(rejected, command)
	if err != nil || changed || replayed.FailureReason == rejected.FailureReason {
		t.Fatalf("Reject(replay) = %+v, %v, %v", replayed, changed, err)
	}
	if _, _, err := StartProcessing(rejected, StartProcessingCommand{
		HandledBy: uuid.New(),
		At:        now.Add(2 * time.Minute),
	}); !errors.Is(err, ErrRefundTerminal) {
		t.Fatalf("StartProcessing(rejected) error = %v, want ErrRefundTerminal", err)
	}
}

func TestRefundTerminalReplayConflictIsRejected(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 12, 0, 0, 0, 0, time.UTC)
	current, err := NewCase(validNewCaseCommand(now))
	if err != nil {
		t.Fatalf("NewCase() error = %v", err)
	}
	operatorID := uuid.New()
	refunded, _, err := Complete(current, CompleteCommand{
		HandledBy:        operatorID,
		ExternalRefundID: "wx-refund-1",
		At:               now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if _, _, err := Complete(refunded, CompleteCommand{
		HandledBy:        operatorID,
		ExternalRefundID: "wx-refund-2",
		At:               now.Add(time.Minute),
	}); !errors.Is(err, ErrRefundReplayConflict) {
		t.Fatalf("Complete(conflict) error = %v, want ErrRefundReplayConflict", err)
	}
	if _, _, err := Fail(refunded, ResolveFailureCommand{
		HandledBy:     operatorID,
		FailureReason: "cannot rewrite",
		At:            now.Add(2 * time.Minute),
	}); !errors.Is(err, ErrRefundTerminal) {
		t.Fatalf("Fail(refunded) error = %v, want ErrRefundTerminal", err)
	}
}

func validNewCaseCommand(now time.Time) NewCaseCommand {
	return NewCaseCommand{
		TenantID:             uuid.New(),
		OrderID:              uuid.New(),
		RegistrationID:       uuid.New(),
		SeriesID:             uuid.New(),
		InstanceID:           uuid.New(),
		SessionID:            uuid.New(),
		PrincipalID:          uuid.New(),
		ReasonCode:           ReasonHoldExpiredAfterPayment,
		IdempotencyKey:       "refund:order:1",
		ActualPaidCents:      9_000,
		RequestedRefundCents: 9_000,
		Now:                  now,
	}
}
