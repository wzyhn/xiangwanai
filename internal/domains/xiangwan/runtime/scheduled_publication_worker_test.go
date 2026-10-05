package xiangwanruntime

import (
	"context"
	"testing"
	"time"

	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/google/uuid"
)

func TestPublishScheduledPublicationUsesDeterministicOperationAndVersion(t *testing.T) {
	task := ScheduledPublicationTask{
		ID: uuid.New(), TenantID: uuid.New(), InstanceID: uuid.New(),
		ActorID: uuid.New(), IdentityLinkID: uuid.New(),
		ExpectedInstanceVersion: 9, AttemptCount: 2,
	}
	var first, second xiangwanadmin.PublishInstanceCommand
	if err := PublishScheduledPublication(context.Background(), task, func(_ context.Context, command xiangwanadmin.PublishInstanceCommand) error {
		first = command
		return nil
	}); err != nil {
		t.Fatalf("first mapping: %v", err)
	}
	if err := PublishScheduledPublication(context.Background(), task, func(_ context.Context, command xiangwanadmin.PublishInstanceCommand) error {
		second = command
		return nil
	}); err != nil {
		t.Fatalf("second mapping: %v", err)
	}
	if first.OperationID != second.OperationID || first.InstanceID != task.InstanceID ||
		first.ExpectedInstanceVersion != task.ExpectedInstanceVersion ||
		first.ActorID != task.ActorID || first.IdentityLinkID != task.IdentityLinkID {
		t.Fatalf("mapping changed across retry: first=%+v second=%+v", first, second)
	}
	if first.RequestID == "" || first.RequestID != second.RequestID {
		t.Fatalf("request identity changed across retry: %q %q", first.RequestID, second.RequestID)
	}
	task.AttemptCount = 3
	var third xiangwanadmin.PublishInstanceCommand
	if err := PublishScheduledPublication(context.Background(), task, func(_ context.Context, command xiangwanadmin.PublishInstanceCommand) error {
		third = command
		return nil
	}); err != nil {
		t.Fatalf("third mapping: %v", err)
	}
	if third.OperationID != first.OperationID || third.RequestID != first.RequestID {
		t.Fatalf("operation identity changed between lease attempts: first=%+v third=%+v", first, third)
	}
}

func TestScheduledPublicationRetryDelayIsBounded(t *testing.T) {
	if got := retryDelay(1); got != time.Minute {
		t.Fatalf("retryDelay(1) = %s", got)
	}
	if got := retryDelay(99); got != 128*time.Minute {
		t.Fatalf("retryDelay(99) = %s", got)
	}
}
