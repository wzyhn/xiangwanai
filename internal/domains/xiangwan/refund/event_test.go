package refund

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNewEventSnapshotsEveryOperatorTransition(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 12, 12, 0, 0, 0, time.UTC)
	operatorID := uuid.New()
	tests := []struct {
		name     string
		apply    func(Case) (Case, bool, error)
		wantType EventType
	}{
		{
			name: "processing",
			apply: func(current Case) (Case, bool, error) {
				return StartProcessing(current, StartProcessingCommand{
					HandledBy:    operatorID,
					OperatorNote: "opened merchant console",
					At:           now.Add(time.Minute),
				})
			},
			wantType: EventTypeProcessingStarted,
		},
		{
			name: "completed",
			apply: func(current Case) (Case, bool, error) {
				return Complete(current, CompleteCommand{
					HandledBy:         operatorID,
					ExternalRefundID:  "wx-refund-event-1",
					EvidenceReference: "merchant-console/refunds/1",
					OperatorNote:      "verified",
					At:                now.Add(time.Minute),
				})
			},
			wantType: EventTypeRefundCompleted,
		},
		{
			name: "failed",
			apply: func(current Case) (Case, bool, error) {
				return Fail(current, ResolveFailureCommand{
					HandledBy:     operatorID,
					FailureReason: "merchant console unavailable",
					OperatorNote:  "retry later",
					At:            now.Add(time.Minute),
				})
			},
			wantType: EventTypeRefundFailed,
		},
		{
			name: "rejected",
			apply: func(current Case) (Case, bool, error) {
				return Reject(current, ResolveFailureCommand{
					HandledBy:     operatorID,
					FailureReason: "evidence rejected",
					OperatorNote:  "escalated",
					At:            now.Add(time.Minute),
				})
			},
			wantType: EventTypeRefundRejected,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			current, err := NewCase(validNewCaseCommand(now))
			if err != nil {
				t.Fatalf("NewCase() error = %v", err)
			}
			updated, changed, err := test.apply(current)
			if err != nil || !changed {
				t.Fatalf("apply() = %+v, %t, %v", updated, changed, err)
			}
			event, err := NewEvent(
				current,
				updated,
				"refund-event:"+test.name,
				updated.UpdatedAt.Add(time.Second),
			)
			if err != nil {
				t.Fatalf("NewEvent() error = %v", err)
			}
			if event.EventType != test.wantType ||
				event.EventSequence != updated.Version-1 ||
				event.ResultingRefundVersion != updated.Version ||
				event.FromStatus != current.RefundStatus ||
				event.ToStatus != updated.RefundStatus ||
				event.ActorID != operatorID ||
				event.SuccessfulRefundCents != updated.SuccessfulRefundCents ||
				!event.OccurredAt.Equal(updated.UpdatedAt) {
				t.Fatalf("NewEvent() = %+v", event)
			}
			if updated.OperatorNote != nil && event.OperatorNote == updated.OperatorNote {
				t.Fatal("NewEvent() retained mutable OperatorNote pointer")
			}
		})
	}
}

func TestNewEventRejectsInvalidMetadataIdentityAndTransition(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 12, 12, 0, 0, 0, time.UTC)
	current, err := NewCase(validNewCaseCommand(now))
	if err != nil {
		t.Fatalf("NewCase() error = %v", err)
	}
	updated, _, err := StartProcessing(current, StartProcessingCommand{
		HandledBy: uuid.New(),
		At:        now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("StartProcessing() error = %v", err)
	}
	tests := []struct {
		name       string
		before     Case
		after      Case
		key        string
		recordedAt time.Time
		wantErr    error
	}{
		{
			name:       "bad key",
			before:     current,
			after:      updated,
			key:        "bad key",
			recordedAt: updated.UpdatedAt,
			wantErr:    ErrInvalidRefundEvent,
		},
		{
			name:       "recorded before occurrence",
			before:     current,
			after:      updated,
			key:        "refund-event:valid",
			recordedAt: updated.UpdatedAt.Add(-time.Second),
			wantErr:    ErrInvalidRefundEvent,
		},
		{
			name:   "identity changed",
			before: current,
			after: func() Case {
				changed := updated
				changed.OrderID = uuid.New()
				return changed
			}(),
			key:        "refund-event:valid",
			recordedAt: updated.UpdatedAt,
			wantErr:    ErrInvalidRefundEvent,
		},
		{
			name:   "unsupported transition",
			before: current,
			after: func() Case {
				changed := updated
				changed.RefundStatus = StatusPendingManual
				return changed
			}(),
			key:        "refund-event:valid",
			recordedAt: updated.UpdatedAt,
			wantErr:    ErrRefundEventConflict,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := NewEvent(
				test.before,
				test.after,
				test.key,
				test.recordedAt,
			); !errors.Is(err, test.wantErr) {
				t.Fatalf("NewEvent() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}
