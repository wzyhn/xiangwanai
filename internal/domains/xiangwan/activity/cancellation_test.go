package activity

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCancelSessionCreatesTerminalProjectionAndReceipt(t *testing.T) {
	t.Parallel()

	current := cancellableSession()
	before := current
	at := current.UpdatedAt.Add(time.Minute)
	command := CancelSessionCommand{
		SeriesID:             uuid.New(),
		PreviewID:            uuid.New(),
		CancelledBy:          uuid.New(),
		IdempotencyKey:       "session-cancel:operator:1",
		Reason:               "Host is unavailable",
		NotificationStrategy: CancellationNotificationManualRequired,
		At:                   at,
		RecordedAt:           at.Add(time.Second),
	}
	impact := SessionCancellationImpact{
		CancelledRegistrationCount: 5,
		ReleasedConfirmedCount:     3,
		ReleasedHoldCount:          2,
		ClosedPendingOrderCount:    1,
		RefundCaseCount:            2,
		RequestedRefundCents:       19_800,
	}

	updated, receipt, err := CancelSession(current, command, impact)
	if err != nil {
		t.Fatalf("CancelSession() error = %v", err)
	}
	if updated.Status != SessionStatusCancelled ||
		updated.ConfirmedRegistrationCount != 0 ||
		updated.ActiveHoldCount != 0 ||
		updated.Version != current.Version+1 ||
		!updated.UpdatedAt.Equal(at) {
		t.Fatalf("CancelSession() projection = %+v", updated)
	}
	if receipt.ID == uuid.Nil ||
		receipt.TenantID != current.TenantID ||
		receipt.SeriesID != command.SeriesID ||
		receipt.InstanceID != current.InstanceID ||
		receipt.SessionID != current.ID ||
		receipt.PreviewID != command.PreviewID ||
		receipt.IdempotencyKey != command.IdempotencyKey ||
		receipt.CancelledBy != command.CancelledBy ||
		receipt.CancellationReason != command.Reason ||
		receipt.NotificationStrategy != command.NotificationStrategy ||
		receipt.CancelledRegistrationCount != impact.CancelledRegistrationCount ||
		receipt.ReleasedConfirmedCount != impact.ReleasedConfirmedCount ||
		receipt.ReleasedHoldCount != impact.ReleasedHoldCount ||
		receipt.ClosedPendingOrderCount != impact.ClosedPendingOrderCount ||
		receipt.RefundCaseCount != impact.RefundCaseCount ||
		receipt.RequestedRefundCents != impact.RequestedRefundCents ||
		receipt.ResultingSessionVersion != updated.Version ||
		!receipt.CancelledAt.Equal(at) ||
		!receipt.CreatedAt.Equal(command.RecordedAt) {
		t.Fatalf("CancelSession() receipt = %+v", receipt)
	}
	if !reflect.DeepEqual(current, before) {
		t.Fatalf("CancelSession() mutated current: got %+v want %+v", current, before)
	}
}

func TestCancelSessionRejectsNonPublishedLifecycle(t *testing.T) {
	t.Parallel()

	current := cancellableSession()
	current.Status = SessionStatusCancelled
	current.ConfirmedRegistrationCount = 0
	current.ActiveHoldCount = 0
	at := current.UpdatedAt.Add(time.Minute)
	updated, receipt, err := CancelSession(
		current,
		CancelSessionCommand{
			SeriesID:             uuid.New(),
			PreviewID:            uuid.New(),
			CancelledBy:          uuid.New(),
			IdempotencyKey:       "session-cancel:replay",
			Reason:               "Already cancelled",
			NotificationStrategy: CancellationNotificationManualRequired,
			At:                   at,
			RecordedAt:           at,
		},
		SessionCancellationImpact{},
	)
	if !errors.Is(err, ErrSessionNotCancellable) {
		t.Fatalf("CancelSession() error = %v", err)
	}
	if updated != (Session{}) || receipt != (SessionCancellationReceipt{}) {
		t.Fatalf("CancelSession() returned partial result = %+v %+v", updated, receipt)
	}
}

func TestCancelSessionRejectsUnconvergedFacts(t *testing.T) {
	t.Parallel()

	current := cancellableSession()
	at := current.UpdatedAt.Add(time.Minute)
	validCommand := CancelSessionCommand{
		SeriesID:             uuid.New(),
		PreviewID:            uuid.New(),
		CancelledBy:          uuid.New(),
		IdempotencyKey:       "session-cancel:validate",
		Reason:               "Unsafe weather",
		NotificationStrategy: CancellationNotificationManualRequired,
		At:                   at,
		RecordedAt:           at,
	}
	validImpact := SessionCancellationImpact{
		CancelledRegistrationCount: 5,
		ReleasedConfirmedCount:     3,
		ReleasedHoldCount:          2,
		ClosedPendingOrderCount:    2,
		RefundCaseCount:            1,
		RequestedRefundCents:       9_900,
	}
	tests := []struct {
		name    string
		mutate  func(*Session, *CancelSessionCommand, *SessionCancellationImpact)
		wantErr error
	}{
		{
			name: "missing preview",
			mutate: func(_ *Session, command *CancelSessionCommand, _ *SessionCancellationImpact) {
				command.PreviewID = uuid.Nil
			},
			wantErr: ErrInvalidSessionCancellation,
		},
		{
			name: "unsupported notification strategy",
			mutate: func(_ *Session, command *CancelSessionCommand, _ *SessionCancellationImpact) {
				command.NotificationStrategy = "automatic"
			},
			wantErr: ErrInvalidSessionCancellation,
		},
		{
			name: "missing actor",
			mutate: func(_ *Session, command *CancelSessionCommand, _ *SessionCancellationImpact) {
				command.CancelledBy = uuid.Nil
			},
			wantErr: ErrInvalidSessionCancellation,
		},
		{
			name: "invalid operation key",
			mutate: func(_ *Session, command *CancelSessionCommand, _ *SessionCancellationImpact) {
				command.IdempotencyKey = "spaces are invalid"
			},
			wantErr: ErrInvalidSessionCancellation,
		},
		{
			name: "untrimmed reason",
			mutate: func(_ *Session, command *CancelSessionCommand, _ *SessionCancellationImpact) {
				command.Reason = " unsafe weather "
			},
			wantErr: ErrInvalidSessionCancellation,
		},
		{
			name: "time moves backward",
			mutate: func(session *Session, command *CancelSessionCommand, _ *SessionCancellationImpact) {
				command.At = session.UpdatedAt.Add(-time.Second)
			},
			wantErr: ErrInvalidSessionCancellation,
		},
		{
			name: "receipt predates cancellation",
			mutate: func(_ *Session, command *CancelSessionCommand, _ *SessionCancellationImpact) {
				command.RecordedAt = command.At.Add(-time.Second)
			},
			wantErr: ErrInvalidSessionCancellation,
		},
		{
			name: "confirmed count disagrees",
			mutate: func(_ *Session, _ *CancelSessionCommand, impact *SessionCancellationImpact) {
				impact.ReleasedConfirmedCount--
			},
			wantErr: ErrInvalidSessionCancellation,
		},
		{
			name: "hold count disagrees",
			mutate: func(_ *Session, _ *CancelSessionCommand, impact *SessionCancellationImpact) {
				impact.ReleasedHoldCount--
			},
			wantErr: ErrInvalidSessionCancellation,
		},
		{
			name: "registration total disagrees",
			mutate: func(_ *Session, _ *CancelSessionCommand, impact *SessionCancellationImpact) {
				impact.CancelledRegistrationCount++
			},
			wantErr: ErrInvalidSessionCancellation,
		},
		{
			name: "closed orders exceed holds",
			mutate: func(_ *Session, _ *CancelSessionCommand, impact *SessionCancellationImpact) {
				impact.ClosedPendingOrderCount = impact.ReleasedHoldCount + 1
			},
			wantErr: ErrInvalidSessionCancellation,
		},
		{
			name: "refund count exceeds confirmed",
			mutate: func(_ *Session, _ *CancelSessionCommand, impact *SessionCancellationImpact) {
				impact.RefundCaseCount = impact.ReleasedConfirmedCount + 1
			},
			wantErr: ErrInvalidSessionCancellation,
		},
		{
			name: "refund amount missing",
			mutate: func(_ *Session, _ *CancelSessionCommand, impact *SessionCancellationImpact) {
				impact.RequestedRefundCents = 0
			},
			wantErr: ErrInvalidSessionCancellation,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			session := current
			command := validCommand
			impact := validImpact
			test.mutate(&session, &command, &impact)
			updated, receipt, err := CancelSession(session, command, impact)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("CancelSession() error = %v, want %v", err, test.wantErr)
			}
			if updated != (Session{}) || receipt != (SessionCancellationReceipt{}) {
				t.Fatalf("invalid input returned partial result = %+v %+v", updated, receipt)
			}
		})
	}
}

func cancellableSession() Session {
	now := time.Date(2026, time.September, 13, 2, 0, 0, 0, time.UTC)
	capacity := 20
	return Session{
		ID:                         uuid.New(),
		TenantID:                   uuid.New(),
		InstanceID:                 uuid.New(),
		Title:                      "Morning hike",
		Status:                     SessionStatusPublished,
		Capacity:                   &capacity,
		ConfirmedRegistrationCount: 3,
		ActiveHoldCount:            2,
		Version:                    7,
		CreatedAt:                  now.Add(-24 * time.Hour),
		UpdatedAt:                  now,
	}
}
