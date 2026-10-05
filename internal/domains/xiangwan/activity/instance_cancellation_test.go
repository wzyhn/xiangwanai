package activity

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestInstanceCancellationPreviewAggregatesEverySession(t *testing.T) {
	t.Parallel()

	snapshot := instanceCancellationSnapshot(t)
	createdAt := snapshot.InstanceUpdatedAt.Add(time.Minute)
	preview, err := NewInstanceCancellationPreview(
		InstanceCancellationPreviewCommand{
			RequestedBy:    uuid.New(),
			IdempotencyKey: "instance-cancel-preview:test",
			Reason:         "Venue unavailable",
			At:             createdAt,
		},
		snapshot,
	)
	if err != nil {
		t.Fatalf("NewInstanceCancellationPreview() error = %v", err)
	}
	if preview.ID == uuid.Nil ||
		preview.SessionCount != 3 ||
		preview.TargetSessionCount != 2 ||
		preview.AlreadyCancelledSessionCount != 1 ||
		preview.CancelledRegistrationCount != 4 ||
		preview.ConfirmedRegistrationCount != 2 ||
		preview.ActiveHoldCount != 2 ||
		preview.FreeRegistrationCount != 1 ||
		preview.PaidRefundRegistrationCount != 1 ||
		preview.PendingOrderCount != 1 ||
		preview.UnknownPaymentCount != 1 ||
		preview.RefundCaseCount != 1 ||
		preview.RequestedRefundCents != 9_000 ||
		preview.CancellationReason != "Venue unavailable" ||
		len(preview.SessionImpacts) != 3 ||
		!validSessionCancellationPreviewDigest(preview.SnapshotDigest) {
		t.Fatalf("Instance preview = %+v", preview)
	}
	matches, err := InstanceCancellationPreviewMatchesSnapshot(preview, snapshot)
	if err != nil || !matches {
		t.Fatalf("InstanceCancellationPreviewMatchesSnapshot() = %t, %v", matches, err)
	}
}

func TestInstanceCancellationDigestIsOrderIndependentAndFactSensitive(t *testing.T) {
	t.Parallel()

	snapshot := instanceCancellationSnapshot(t)
	_, digest, err := AssessInstanceCancellation(snapshot)
	if err != nil {
		t.Fatalf("AssessInstanceCancellation() error = %v", err)
	}
	reordered := snapshot
	reordered.Sessions = append(
		[]InstanceCancellationSessionSnapshot(nil),
		snapshot.Sessions...,
	)
	reordered.Sessions[0], reordered.Sessions[2] =
		reordered.Sessions[2], reordered.Sessions[0]
	_, reorderedDigest, err := AssessInstanceCancellation(reordered)
	if err != nil {
		t.Fatalf("AssessInstanceCancellation(reordered) error = %v", err)
	}
	if reorderedDigest != digest {
		t.Fatalf("reordered digest = %q, want %q", reorderedDigest, digest)
	}

	changed := snapshot
	changed.Sessions = append(
		[]InstanceCancellationSessionSnapshot(nil),
		snapshot.Sessions...,
	)
	changedCancellation := *snapshot.Sessions[0].Cancellation
	changedCancellation.Registrations = append(
		[]SessionCancellationRegistrationSnapshot(nil),
		snapshot.Sessions[0].Cancellation.Registrations...,
	)
	changed.Sessions[0].Cancellation = &changedCancellation
	changed.Sessions[0].Cancellation.Registrations[0].Version++
	_, changedDigest, err := AssessInstanceCancellation(changed)
	if err != nil {
		t.Fatalf("AssessInstanceCancellation(changed) error = %v", err)
	}
	if changedDigest == digest {
		t.Fatal("changed child fact did not change Instance digest")
	}
}

func TestInstanceCancellationRejectsTerminalOrEmptyTargets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(*InstanceCancellationSnapshot)
		wantErr error
	}{
		{
			name: "ended child",
			mutate: func(snapshot *InstanceCancellationSnapshot) {
				snapshot.Sessions[0].SessionStatus = SessionStatusEnded
				snapshot.Sessions[0].Cancellation = nil
			},
			wantErr: ErrInstanceNotCancellable,
		},
		{
			name: "all already cancelled",
			mutate: func(snapshot *InstanceCancellationSnapshot) {
				for index := range snapshot.Sessions {
					snapshot.Sessions[index].SessionStatus = SessionStatusCancelled
					snapshot.Sessions[index].Cancellation = nil
				}
			},
			wantErr: ErrInstanceNotCancellable,
		},
		{
			name: "duplicate child",
			mutate: func(snapshot *InstanceCancellationSnapshot) {
				snapshot.Sessions[1].SessionID = snapshot.Sessions[0].SessionID
			},
			wantErr: ErrInvalidInstanceCancellation,
		},
		{
			name: "wrong hierarchy",
			mutate: func(snapshot *InstanceCancellationSnapshot) {
				snapshot.Sessions[0].Cancellation.InstanceID = uuid.New()
			},
			wantErr: ErrInvalidInstanceCancellation,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			snapshot := instanceCancellationSnapshot(t)
			test.mutate(&snapshot)
			if _, _, err := AssessInstanceCancellation(snapshot); !errors.Is(
				err,
				test.wantErr,
			) {
				t.Fatalf("AssessInstanceCancellation() error = %v", err)
			}
		})
	}
}

func TestCancelInstanceCreatesTerminalAggregateReceipt(t *testing.T) {
	t.Parallel()

	snapshot := instanceCancellationSnapshot(t)
	assessment, _, err := AssessInstanceCancellation(snapshot)
	if err != nil {
		t.Fatalf("AssessInstanceCancellation() error = %v", err)
	}
	now := snapshot.InstanceUpdatedAt
	current := Instance{
		ID:        snapshot.InstanceID,
		TenantID:  snapshot.TenantID,
		SeriesID:  snapshot.SeriesID,
		Title:     "September gathering",
		Status:    InstanceStatusPublished,
		Version:   snapshot.InstanceVersion,
		CreatedAt: now.Add(-time.Hour),
		UpdatedAt: now,
	}
	before := current
	command := CancelInstanceCommand{
		PreviewID:            uuid.New(),
		CancelledBy:          uuid.New(),
		IdempotencyKey:       "instance-cancel:test",
		Reason:               "Venue unavailable",
		NotificationStrategy: CancellationNotificationManualRequired,
		At:                   now.Add(time.Minute),
		RecordedAt:           now.Add(time.Minute),
	}
	updated, receipt, err := CancelInstance(current, command, assessment)
	if err != nil {
		t.Fatalf("CancelInstance() error = %v", err)
	}
	if updated.Status != InstanceStatusCancelled ||
		updated.Version != current.Version+1 ||
		updated.CompletedAt != nil ||
		!updated.UpdatedAt.Equal(command.At) {
		t.Fatalf("cancelled Instance = %+v", updated)
	}
	if receipt.ID == uuid.Nil ||
		receipt.PreviewID != command.PreviewID ||
		receipt.NewlyCancelledSessionCount != 2 ||
		receipt.AlreadyCancelledSessionCount != 1 ||
		receipt.CancelledRegistrationCount != 4 ||
		receipt.ReleasedConfirmedCount != 2 ||
		receipt.ReleasedHoldCount != 2 ||
		receipt.ClosedPendingOrderCount != 1 ||
		receipt.RefundCaseCount != 1 ||
		receipt.RequestedRefundCents != 9_000 ||
		receipt.ResultingInstanceVersion != updated.Version {
		t.Fatalf("Instance receipt = %+v", receipt)
	}
	if !reflect.DeepEqual(current, before) {
		t.Fatalf("CancelInstance() mutated current: %+v", current)
	}
}

func instanceCancellationSnapshot(t *testing.T) InstanceCancellationSnapshot {
	t.Helper()
	first := cancellationPreviewSnapshot(t)
	tenantID := first.TenantID
	seriesID := first.SeriesID
	instanceID := first.InstanceID
	first.RefundReasonCode = "instance_cancelled"
	first.Registrations[1].Order.Refund.ReasonCode = "instance_cancelled"

	secondSessionID := uuid.New()
	second := SessionCancellationSnapshot{
		TenantID:                   tenantID,
		SeriesID:                   seriesID,
		InstanceID:                 instanceID,
		SessionID:                  secondSessionID,
		SessionStatus:              SessionStatusPublished,
		SessionVersion:             4,
		SessionUpdatedAt:           first.SessionUpdatedAt,
		RefundReasonCode:           "instance_cancelled",
		ConfirmedRegistrationCount: 0,
		ActiveHoldCount:            0,
		Registrations:              []SessionCancellationRegistrationSnapshot{},
	}
	cancelledSessionID := uuid.New()
	return InstanceCancellationSnapshot{
		TenantID:          tenantID,
		SeriesID:          seriesID,
		InstanceID:        instanceID,
		InstanceStatus:    InstanceStatusPublished,
		InstanceVersion:   8,
		InstanceUpdatedAt: first.SessionUpdatedAt,
		Sessions: []InstanceCancellationSessionSnapshot{
			{
				SessionID:        first.SessionID,
				SessionStatus:    SessionStatusPublished,
				SessionVersion:   first.SessionVersion,
				SessionUpdatedAt: first.SessionUpdatedAt,
				Cancellation:     &first,
			},
			{
				SessionID:        secondSessionID,
				SessionStatus:    SessionStatusPublished,
				SessionVersion:   second.SessionVersion,
				SessionUpdatedAt: second.SessionUpdatedAt,
				Cancellation:     &second,
			},
			{
				SessionID:        cancelledSessionID,
				SessionStatus:    SessionStatusCancelled,
				SessionVersion:   3,
				SessionUpdatedAt: first.SessionUpdatedAt,
			},
		},
	}
}
