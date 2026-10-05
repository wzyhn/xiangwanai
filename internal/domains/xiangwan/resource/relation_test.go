package resource

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNewDraftPinsExactInstanceReviewContent(t *testing.T) {
	t.Parallel()

	command := validDraftCommand()
	relation, err := NewDraft(command)
	if err != nil {
		t.Fatalf("NewDraft() error = %v", err)
	}
	if relation.ID == uuid.Nil || relation.SessionID != nil ||
		relation.Kind != RelationKindInstanceReview ||
		relation.AccessPolicy != AccessPolicyPublic ||
		relation.ContentID != command.ContentID ||
		!relation.ContentRevision.Equal(command.ContentRevision) ||
		relation.ExpectedTargetVersion != command.ExpectedTargetVersion {
		t.Fatalf("NewDraft() = %+v", relation)
	}
}

func TestNewDraftRequiresSessionForSessionResources(t *testing.T) {
	t.Parallel()

	command := validDraftCommand()
	command.Kind = RelationKindSessionResources
	command.AccessPolicy = AccessPolicyConfirmedRegistration
	sessionID := uuid.New()
	command.SessionID = &sessionID
	relation, err := NewDraft(command)
	if err != nil {
		t.Fatalf("NewDraft(Session) error = %v", err)
	}
	if relation.SessionID == nil || *relation.SessionID != sessionID ||
		relation.AccessPolicy != AccessPolicyConfirmedRegistration {
		t.Fatalf("Session relation = %+v", relation)
	}
	*relation.SessionID = uuid.New()
	if *command.SessionID != sessionID {
		t.Fatal("NewDraft() aliased the Session identity")
	}
}

func TestNewDraftRejectsAmbiguousOrUnsafeBindings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*CreateDraftCommand)
	}{
		{
			name: "Instance review carries Session",
			mutate: func(command *CreateDraftCommand) {
				sessionID := uuid.New()
				command.SessionID = &sessionID
			},
		},
		{
			name: "Instance review is private",
			mutate: func(command *CreateDraftCommand) {
				command.AccessPolicy = AccessPolicyConfirmedRegistration
			},
		},
		{
			name: "Session resource has no Session",
			mutate: func(command *CreateDraftCommand) {
				command.Kind = RelationKindSessionResources
			},
		},
		{
			name: "content revision is newer than binding",
			mutate: func(command *CreateDraftCommand) {
				command.ContentRevision = command.CreatedAt.Add(time.Second)
			},
		},
		{
			name: "target version absent",
			mutate: func(command *CreateDraftCommand) {
				command.ExpectedTargetVersion = 0
			},
		},
		{
			name: "idempotency key malformed",
			mutate: func(command *CreateDraftCommand) {
				command.IdempotencyKey = "spaces are forbidden"
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			command := validDraftCommand()
			test.mutate(&command)
			if _, err := NewDraft(command); !errors.Is(
				err,
				ErrInvalidRelation,
			) {
				t.Fatalf("NewDraft() error = %v", err)
			}
		})
	}
}

func TestSameCreateIntentIgnoresGeneratedReceiptFacts(t *testing.T) {
	t.Parallel()

	command := validDraftCommand()
	first, err := NewDraft(command)
	if err != nil {
		t.Fatalf("NewDraft(first) error = %v", err)
	}
	second, err := NewDraft(command)
	if err != nil {
		t.Fatalf("NewDraft(second) error = %v", err)
	}
	second.CreatedAt = second.CreatedAt.Add(time.Minute)
	if !SameCreateIntent(first, second) {
		t.Fatal("same command was not recognized as the same intent")
	}
	second.ContentID = uuid.New()
	if SameCreateIntent(first, second) {
		t.Fatal("different Content was accepted as the same intent")
	}
}

func validDraftCommand() CreateDraftCommand {
	now := time.Date(2026, time.September, 13, 9, 0, 0, 0, time.UTC)
	return CreateDraftCommand{
		TenantID:              uuid.New(),
		SeriesID:              uuid.New(),
		InstanceID:            uuid.New(),
		Kind:                  RelationKindInstanceReview,
		ContentID:             uuid.New(),
		ContentRevision:       now.Add(-time.Minute),
		AccessPolicy:          AccessPolicyPublic,
		SortOrder:             1,
		ExpectedTargetVersion: 3,
		CreatedBy:             uuid.New(),
		IdempotencyKey:        "review-draft:operation-001",
		CreatedAt:             now,
	}
}
