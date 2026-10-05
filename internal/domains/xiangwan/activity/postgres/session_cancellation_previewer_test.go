package activitypostgres

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/google/uuid"
)

func TestSessionCancellationPreviewerBuildsAndStoresSnapshot(t *testing.T) {
	t.Parallel()

	fixture := newSessionCancellationFixture(t)
	command := PreviewSessionCancellationCommand{
		TenantID:       fixture.command.TenantID,
		SessionID:      fixture.command.SessionID,
		RequestedBy:    fixture.command.ActorID,
		IdempotencyKey: "session-cancel-preview:new",
	}
	previewer, tx, starter := newSessionCancellationPreviewerHarness(
		fixture,
		fixture.processedAt.Add(-30*time.Second),
	)

	created, err := previewer.Preview(context.Background(), command)
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	if starter.isolation != sql.LevelSerializable ||
		!tx.committed ||
		tx.rolledBack {
		t.Fatalf("preview transaction = %+v", tx)
	}
	if got, want := tx.lockOrder[:4], []string{
		"series",
		"instance",
		"session",
		"registrations",
	}; !sameStrings(got, want) {
		t.Fatalf("preview lock prefix = %v, want %v", got, want)
	}
	if created.ID == uuid.Nil ||
		created.ID != tx.previewCreated.ID ||
		created.CancelledRegistrationCount != 3 ||
		created.ConfirmedRegistrationCount != 2 ||
		created.ActiveHoldCount != 1 ||
		created.FreeRegistrationCount != 1 ||
		created.PaidRefundRegistrationCount != 1 ||
		created.PendingOrderCount != 1 ||
		created.UnknownPaymentCount != 0 ||
		created.RefundCaseCount != 1 ||
		created.RequestedRefundCents != 9_000 ||
		created.NotificationStrategy !=
			activity.CancellationNotificationManualRequired {
		t.Fatalf("created preview = %+v", created)
	}
	if len(tx.registrationUpdates) != 0 ||
		len(tx.orderUpdates) != 0 ||
		len(tx.holdUpdates) != 0 ||
		len(tx.refundCreates) != 0 {
		t.Fatalf("preview mutated business facts: %+v", tx)
	}
}

func TestSessionCancellationPreviewerExactReplay(t *testing.T) {
	t.Parallel()

	fixture := newSessionCancellationFixture(t)
	command := PreviewSessionCancellationCommand{
		TenantID:       fixture.preview.TenantID,
		SessionID:      fixture.preview.SessionID,
		RequestedBy:    fixture.preview.RequestedBy,
		IdempotencyKey: fixture.preview.IdempotencyKey,
	}
	previewer, tx, _ := newSessionCancellationPreviewerHarness(
		fixture,
		fixture.processedAt,
	)

	replayed, err := previewer.Preview(context.Background(), command)
	if err != nil {
		t.Fatalf("Preview(replay) error = %v", err)
	}
	if replayed.ID != fixture.preview.ID ||
		!tx.committed ||
		tx.listRegistrationsCalled ||
		tx.previewCreated.ID != uuid.Nil {
		t.Fatalf("replayed preview=%+v tx=%+v", replayed, tx)
	}
}

func TestSessionCancellationPreviewerRejectsConflictingReplay(t *testing.T) {
	t.Parallel()

	fixture := newSessionCancellationFixture(t)
	command := PreviewSessionCancellationCommand{
		TenantID:       fixture.preview.TenantID,
		SessionID:      fixture.preview.SessionID,
		RequestedBy:    uuid.New(),
		IdempotencyKey: fixture.preview.IdempotencyKey,
	}
	previewer, tx, _ := newSessionCancellationPreviewerHarness(
		fixture,
		fixture.processedAt,
	)

	_, err := previewer.Preview(context.Background(), command)
	if !errors.Is(err, ErrSessionCancellationPreviewConflict) {
		t.Fatalf("Preview(conflict) error = %v", err)
	}
	if tx.committed || !tx.rolledBack || tx.listRegistrationsCalled {
		t.Fatalf("conflicting preview transaction = %+v", tx)
	}
}

func TestSessionCancellationPreviewerRejectsInvalidAndUnconvergedFacts(t *testing.T) {
	t.Parallel()

	fixture := newSessionCancellationFixture(t)
	previewer, _, starter := newSessionCancellationPreviewerHarness(
		fixture,
		fixture.processedAt,
	)
	_, err := previewer.Preview(
		context.Background(),
		PreviewSessionCancellationCommand{
			TenantID:       fixture.command.TenantID,
			SessionID:      fixture.command.SessionID,
			RequestedBy:    fixture.command.ActorID,
			IdempotencyKey: " invalid ",
		},
	)
	if !errors.Is(err, ErrInvalidSessionCancellationPreviewCommand) {
		t.Fatalf("Preview(invalid) error = %v", err)
	}
	if starter.started {
		t.Fatal("invalid preview started transaction")
	}

	unconverged := newSessionCancellationFixture(t)
	pending := unconverged.orders[unconverged.pendingRegistration.ID]
	pending.PaymentStatus = payment.OrderStatusClosedUnpaid
	unconverged.orders[unconverged.pendingRegistration.ID] = pending
	previewer, tx, _ := newSessionCancellationPreviewerHarness(
		unconverged,
		unconverged.processedAt,
	)
	_, err = previewer.Preview(
		context.Background(),
		PreviewSessionCancellationCommand{
			TenantID:       unconverged.command.TenantID,
			SessionID:      unconverged.command.SessionID,
			RequestedBy:    unconverged.command.ActorID,
			IdempotencyKey: "session-cancel-preview:unconverged",
		},
	)
	if !errors.Is(err, ErrSessionCancellationTransaction) {
		t.Fatalf("Preview(unconverged) error = %v", err)
	}
	if tx.committed || !tx.rolledBack {
		t.Fatalf("unconverged preview transaction = %+v", tx)
	}
}

func TestSessionCancellerRejectsExpiredConsumedAndChangedPreviews(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*sessionCancellationFixture)
	}{
		{
			name: "expired",
			mutate: func(fixture *sessionCancellationFixture) {
				fixture.preview.ExpiresAt = fixture.processedAt.Add(-time.Microsecond)
			},
		},
		{
			name: "consumed",
			mutate: func(fixture *sessionCancellationFixture) {
				consumedAt := fixture.processedAt.Add(-time.Second)
				fixture.preview.ConsumedAt = &consumedAt
			},
		},
		{
			name: "changed child fact",
			mutate: func(fixture *sessionCancellationFixture) {
				order := fixture.orders[fixture.pendingRegistration.ID]
				order.Version++
				fixture.orders[fixture.pendingRegistration.ID] = order
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newSessionCancellationFixture(t)
			test.mutate(&fixture)
			canceller, tx, _ := newSessionCancellerHarness(fixture)
			_, err := canceller.Cancel(context.Background(), fixture.command)
			if !errors.Is(err, ErrSessionCancellationPreviewConflict) {
				t.Fatalf("Cancel() error = %v", err)
			}
			if tx.committed ||
				!tx.rolledBack ||
				len(tx.registrationUpdates) != 0 {
				t.Fatalf("rejected cancellation transaction = %+v", tx)
			}
		})
	}
}

func newSessionCancellationPreviewerHarness(
	fixture sessionCancellationFixture,
	now time.Time,
) (
	*SessionCancellationPreviewer,
	*fakeSessionCancellationTransaction,
	*fakeSessionCancellationStarter,
) {
	_, tx, starter := newSessionCancellerHarness(fixture)
	return &SessionCancellationPreviewer{
		resolver: fakeSessionCancellationResolver{
			target: fixture.target,
			err:    fixture.resolveErr,
		},
		transactions: starter,
		now:          func() time.Time { return now },
	}, tx, starter
}
