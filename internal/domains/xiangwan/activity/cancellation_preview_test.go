package activity

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNewSessionCancellationPreviewBuildsCompleteImpact(t *testing.T) {
	t.Parallel()

	snapshot := cancellationPreviewSnapshot(t)
	createdAt := time.Date(2026, time.September, 13, 8, 0, 0, 0, time.UTC)
	preview, err := NewSessionCancellationPreview(
		SessionCancellationPreviewCommand{
			RequestedBy:    uuid.New(),
			IdempotencyKey: "session-cancel-preview:test",
			At:             createdAt,
		},
		snapshot,
	)
	if err != nil {
		t.Fatalf("NewSessionCancellationPreview() error = %v", err)
	}
	if preview.ID == uuid.Nil ||
		preview.ExpectedSessionVersion != snapshot.SessionVersion ||
		preview.CancelledRegistrationCount != 4 ||
		preview.ConfirmedRegistrationCount != 2 ||
		preview.ActiveHoldCount != 2 ||
		preview.FreeRegistrationCount != 1 ||
		preview.PaidRefundRegistrationCount != 1 ||
		preview.PendingOrderCount != 1 ||
		preview.UnknownPaymentCount != 1 ||
		preview.RefundCaseCount != 1 ||
		preview.RequestedRefundCents != 9_000 ||
		preview.CouponAdjustmentCount != 0 ||
		preview.NotificationStrategy != CancellationNotificationManualRequired ||
		preview.ExpiresAt.Sub(preview.CreatedAt) != SessionCancellationPreviewLifetime ||
		!validSessionCancellationPreviewDigest(preview.SnapshotDigest) {
		t.Fatalf("preview = %+v", preview)
	}
	matches, err := SessionCancellationPreviewMatchesSnapshot(preview, snapshot)
	if err != nil || !matches {
		t.Fatalf("SessionCancellationPreviewMatchesSnapshot() = %t, %v", matches, err)
	}
}

func TestSessionCancellationPreviewDigestIsOrderIndependentAndFactSensitive(t *testing.T) {
	t.Parallel()

	snapshot := cancellationPreviewSnapshot(t)
	_, digest, err := AssessSessionCancellation(snapshot)
	if err != nil {
		t.Fatalf("AssessSessionCancellation() error = %v", err)
	}
	reordered := snapshot
	reordered.Registrations = append(
		[]SessionCancellationRegistrationSnapshot(nil),
		snapshot.Registrations...,
	)
	for left, right := 0, len(reordered.Registrations)-1; left < right; left, right = left+1, right-1 {
		reordered.Registrations[left], reordered.Registrations[right] =
			reordered.Registrations[right], reordered.Registrations[left]
	}
	_, reorderedDigest, err := AssessSessionCancellation(reordered)
	if err != nil {
		t.Fatalf("AssessSessionCancellation(reordered) error = %v", err)
	}
	if reorderedDigest != digest {
		t.Fatalf("reordered digest = %q, want %q", reorderedDigest, digest)
	}

	changed := cancellationPreviewSnapshot(t)
	changed.Registrations[1].Order.Version++
	_, changedDigest, err := AssessSessionCancellation(changed)
	if err != nil {
		t.Fatalf("AssessSessionCancellation(changed) error = %v", err)
	}
	if changedDigest == digest {
		t.Fatal("changed Order version did not change digest")
	}
}

func TestSessionCancellationPreviewRejectsUnconvergedFacts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*SessionCancellationSnapshot)
	}{
		{
			name: "counter mismatch",
			mutate: func(snapshot *SessionCancellationSnapshot) {
				snapshot.ActiveHoldCount++
			},
		},
		{
			name: "duplicate registration",
			mutate: func(snapshot *SessionCancellationSnapshot) {
				snapshot.Registrations[1].RegistrationID =
					snapshot.Registrations[0].RegistrationID
			},
		},
		{
			name: "pending without order",
			mutate: func(snapshot *SessionCancellationSnapshot) {
				snapshot.Registrations[2].Order = nil
			},
		},
		{
			name: "unknown without hold",
			mutate: func(snapshot *SessionCancellationSnapshot) {
				snapshot.Registrations[3].Order.Hold.HoldStatus = "released"
			},
		},
		{
			name: "paid amount absent",
			mutate: func(snapshot *SessionCancellationSnapshot) {
				snapshot.Registrations[1].Order.ActualPaidCents = nil
			},
		},
		{
			name: "wrong refund reason",
			mutate: func(snapshot *SessionCancellationSnapshot) {
				snapshot.Registrations[1].Order.Refund.ReasonCode = "user_cancelled"
			},
		},
		{
			name: "cancelled session",
			mutate: func(snapshot *SessionCancellationSnapshot) {
				snapshot.SessionStatus = SessionStatusCancelled
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			snapshot := cancellationPreviewSnapshot(t)
			test.mutate(&snapshot)
			if _, _, err := AssessSessionCancellation(snapshot); !errors.Is(
				err,
				ErrInvalidSessionCancellationPreview,
			) {
				t.Fatalf("AssessSessionCancellation() error = %v", err)
			}
		})
	}
}

func TestSessionCancellationPreviewMatchesExactSnapshotOnly(t *testing.T) {
	t.Parallel()

	snapshot := cancellationPreviewSnapshot(t)
	preview, err := NewSessionCancellationPreview(
		SessionCancellationPreviewCommand{
			RequestedBy:    uuid.New(),
			IdempotencyKey: "session-cancel-preview:match",
			At:             snapshot.SessionUpdatedAt.Add(time.Minute),
		},
		snapshot,
	)
	if err != nil {
		t.Fatalf("NewSessionCancellationPreview() error = %v", err)
	}
	preview.UnknownPaymentCount++
	matches, err := SessionCancellationPreviewMatchesSnapshot(preview, snapshot)
	if err != nil {
		t.Fatalf("SessionCancellationPreviewMatchesSnapshot() error = %v", err)
	}
	if matches {
		t.Fatal("changed preview unexpectedly matched")
	}
}

func cancellationPreviewSnapshot(t *testing.T) SessionCancellationSnapshot {
	t.Helper()
	now := time.Date(2026, time.September, 13, 7, 0, 0, 0, time.UTC)
	paid := int64(9_000)
	newHold := func(status string, offset time.Duration) SessionCancellationHoldSnapshot {
		return SessionCancellationHoldSnapshot{
			HoldID:     uuid.New(),
			HoldStatus: status,
			ExpiresAt:  now.Add(offset),
			Version:    2,
			UpdatedAt:  now,
		}
	}
	newOrder := func(status string, amount *int64) *SessionCancellationOrderSnapshot {
		return &SessionCancellationOrderSnapshot{
			OrderID:         uuid.New(),
			PaymentStatus:   status,
			ActualPaidCents: amount,
			Version:         3,
			UpdatedAt:       now,
			Hold:            newHold("active", 5*time.Minute),
		}
	}
	freeRegistration := SessionCancellationRegistrationSnapshot{
		RegistrationID:      uuid.New(),
		ParticipationStatus: "confirmed",
		Version:             1,
		UpdatedAt:           now,
	}
	paidRegistration := SessionCancellationRegistrationSnapshot{
		RegistrationID:      uuid.New(),
		ParticipationStatus: "confirmed",
		Version:             2,
		UpdatedAt:           now,
		Order:               newOrder("paid_confirmed", &paid),
	}
	paidRegistration.Order.Hold = newHold("converted", time.Minute)
	paidRegistration.Order.Refund = &SessionCancellationRefundSnapshot{
		RefundCaseID:          uuid.New(),
		RefundStatus:          "pending_manual",
		ReasonCode:            "session_cancelled",
		RequestedRefundCents:  paid,
		SuccessfulRefundCents: 0,
		Version:               1,
		UpdatedAt:             now,
	}
	pendingRegistration := SessionCancellationRegistrationSnapshot{
		RegistrationID:      uuid.New(),
		ParticipationStatus: "pending_payment",
		Version:             1,
		UpdatedAt:           now,
		Order:               newOrder("pending", nil),
	}
	unknownRegistration := SessionCancellationRegistrationSnapshot{
		RegistrationID:      uuid.New(),
		ParticipationStatus: "pending_payment",
		Version:             1,
		UpdatedAt:           now,
		Order:               newOrder("unknown", nil),
	}
	return SessionCancellationSnapshot{
		TenantID:                   uuid.New(),
		SeriesID:                   uuid.New(),
		InstanceID:                 uuid.New(),
		SessionID:                  uuid.New(),
		SessionStatus:              SessionStatusPublished,
		SessionVersion:             7,
		SessionUpdatedAt:           now,
		RefundReasonCode:           "session_cancelled",
		ConfirmedRegistrationCount: 2,
		ActiveHoldCount:            2,
		Registrations: []SessionCancellationRegistrationSnapshot{
			freeRegistration,
			paidRegistration,
			pendingRegistration,
			unknownRegistration,
		},
	}
}
