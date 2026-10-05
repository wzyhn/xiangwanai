package resource

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNewPublicationPinsApprovalAndCurrentVersions(t *testing.T) {
	t.Parallel()

	command := validPublishCommand()
	publication, err := NewPublication(command)
	if err != nil {
		t.Fatalf("NewPublication() error = %v", err)
	}
	if publication.ID == uuid.Nil ||
		publication.RelationID != command.RelationID ||
		publication.ContentID != command.ContentID ||
		publication.ApprovalObservationID != command.ApprovalObservationID ||
		publication.AccessPolicy != AccessPolicyPublic ||
		publication.ExpectedTargetVersion != command.ExpectedTargetVersion {
		t.Fatalf("NewPublication() = %+v", publication)
	}
}

func TestNewPublicationFailsClosed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*PublishCommand)
	}{
		{
			name: "approval observation absent",
			mutate: func(command *PublishCommand) {
				command.ApprovalObservationID = uuid.Nil
			},
		},
		{
			name: "unknown access policy",
			mutate: func(command *PublishCommand) {
				command.AccessPolicy = "authenticated"
			},
		},
		{
			name: "target version absent",
			mutate: func(command *PublishCommand) {
				command.ExpectedTargetVersion = 0
			},
		},
		{
			name: "operation key malformed",
			mutate: func(command *PublishCommand) {
				command.IdempotencyKey = "spaces are forbidden"
			},
		},
		{
			name: "publication predates Content revision",
			mutate: func(command *PublishCommand) {
				command.PublishedAt = command.ContentRevision.Add(-time.Second)
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			command := validPublishCommand()
			test.mutate(&command)
			if _, err := NewPublication(command); !errors.Is(
				err,
				ErrInvalidPublication,
			) {
				t.Fatalf("NewPublication() error = %v", err)
			}
		})
	}
}

func TestSamePublicationIntentRejectsChangedApproval(t *testing.T) {
	t.Parallel()

	command := validPublishCommand()
	first, err := NewPublication(command)
	if err != nil {
		t.Fatalf("NewPublication(first) error = %v", err)
	}
	second, err := NewPublication(command)
	if err != nil {
		t.Fatalf("NewPublication(second) error = %v", err)
	}
	second.PublishedAt = second.PublishedAt.Add(time.Minute)
	if !SamePublicationIntent(first, second) {
		t.Fatal("same publication command was not recognized as a replay")
	}
	second.ApprovalObservationID = uuid.New()
	if SamePublicationIntent(first, second) {
		t.Fatal("changed approval was accepted as a replay")
	}
}

func validPublishCommand() PublishCommand {
	now := time.Date(2026, time.September, 13, 13, 0, 0, 0, time.UTC)
	return PublishCommand{
		TenantID:              uuid.New(),
		RelationID:            uuid.New(),
		ContentID:             uuid.New(),
		ContentRevision:       now.Add(-time.Minute),
		ApprovalObservationID: uuid.New(),
		AccessPolicy:          AccessPolicyPublic,
		ExpectedTargetVersion: 2,
		PublishedBy:           uuid.New(),
		IdempotencyKey:        "resource-publication:operation-001",
		PublishedAt:           now,
	}
}
