package resource

import (
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNewModerationObservationBindsVerifiedProviderFact(t *testing.T) {
	t.Parallel()

	command := validModerationCommand()
	observation, err := NewModerationObservation(command)
	if err != nil {
		t.Fatalf("NewModerationObservation() error = %v", err)
	}
	if observation.ID == uuid.Nil ||
		observation.RelationID != command.RelationID ||
		observation.ContentID != command.ContentID ||
		observation.Source != ModerationSourceSignedCallback ||
		observation.Decision != ModerationDecisionApproved ||
		observation.ActorID != nil || observation.Reason != nil ||
		observation.SubjectDigest != sha256.Sum256([]byte("subject")) ||
		observation.PayloadDigest != sha256.Sum256([]byte("payload")) {
		t.Fatalf("Moderation observation = %+v", observation)
	}
	command.SubjectDigest[0]++
	if observation.SubjectDigest != sha256.Sum256([]byte("subject")) {
		t.Fatal("NewModerationObservation() aliased digest bytes")
	}
}

func TestNewModerationObservationRequiresAuditedManualDecision(
	t *testing.T,
) {
	t.Parallel()

	command := validModerationCommand()
	command.Source = ModerationSourceManualReview
	command.Decision = ModerationDecisionRejected
	actorID := uuid.New()
	reason := "operator rejected unsafe media"
	command.ActorID = &actorID
	command.Reason = &reason
	observation, err := NewModerationObservation(command)
	if err != nil {
		t.Fatalf("NewModerationObservation(manual) error = %v", err)
	}
	if observation.ActorID == nil || *observation.ActorID != actorID ||
		observation.Reason == nil || *observation.Reason != reason {
		t.Fatalf("manual observation = %+v", observation)
	}
	*observation.Reason = "changed"
	if *command.Reason != reason {
		t.Fatal("NewModerationObservation() aliased manual evidence")
	}
}

func TestNewModerationObservationFailsClosed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*RecordModerationCommand)
	}{
		{
			name: "unknown source",
			mutate: func(command *RecordModerationCommand) {
				command.Source = "unverified_webhook"
			},
		},
		{
			name: "unknown decision spelling",
			mutate: func(command *RecordModerationCommand) {
				command.Decision = "pass"
			},
		},
		{
			name: "short subject digest",
			mutate: func(command *RecordModerationCommand) {
				command.SubjectDigest = []byte("short")
			},
		},
		{
			name: "zero payload digest",
			mutate: func(command *RecordModerationCommand) {
				command.PayloadDigest = make([]byte, DigestSize)
			},
		},
		{
			name: "provider reference malformed",
			mutate: func(command *RecordModerationCommand) {
				command.ProviderReference = "contains spaces"
			},
		},
		{
			name: "observation predates Content",
			mutate: func(command *RecordModerationCommand) {
				command.ObservedAt = command.ContentRevision.Add(-time.Second)
			},
		},
		{
			name: "provider callback carries actor",
			mutate: func(command *RecordModerationCommand) {
				actorID := uuid.New()
				command.ActorID = &actorID
			},
		},
		{
			name: "manual review has unknown result",
			mutate: func(command *RecordModerationCommand) {
				command.Source = ModerationSourceManualReview
				command.Decision = ModerationDecisionUnknown
				actorID := uuid.New()
				reason := "provider result unavailable"
				command.ActorID = &actorID
				command.Reason = &reason
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			command := validModerationCommand()
			test.mutate(&command)
			if _, err := NewModerationObservation(command); !errors.Is(
				err,
				ErrInvalidModerationObservation,
			) {
				t.Fatalf("NewModerationObservation() error = %v", err)
			}
		})
	}
}

func TestSameModerationIntentRejectsChangedProviderFact(t *testing.T) {
	t.Parallel()

	command := validModerationCommand()
	first, err := NewModerationObservation(command)
	if err != nil {
		t.Fatalf("NewModerationObservation(first) error = %v", err)
	}
	second, err := NewModerationObservation(command)
	if err != nil {
		t.Fatalf("NewModerationObservation(second) error = %v", err)
	}
	second.RecordedAt = second.RecordedAt.Add(time.Minute)
	if !SameModerationIntent(first, second) {
		t.Fatal("same provider fact was not recognized as a replay")
	}
	second.Decision = ModerationDecisionRejected
	if SameModerationIntent(first, second) {
		t.Fatal("changed provider decision was accepted as a replay")
	}
}

func validModerationCommand() RecordModerationCommand {
	now := time.Date(2026, time.September, 13, 11, 0, 0, 0, time.UTC)
	subjectDigest := sha256.Sum256([]byte("subject"))
	payloadDigest := sha256.Sum256([]byte("payload"))
	return RecordModerationCommand{
		TenantID:          uuid.New(),
		RelationID:        uuid.New(),
		ContentID:         uuid.New(),
		ContentRevision:   now.Add(-time.Minute),
		Provider:          "wechat-content-security",
		ProviderReference: "callback:moderation-001",
		PolicyVersion:     "wechat-policy-v1",
		Source:            ModerationSourceSignedCallback,
		Decision:          ModerationDecisionApproved,
		SubjectDigest:     subjectDigest[:],
		PayloadDigest:     payloadDigest[:],
		ObservedAt:        now,
		RecordedAt:        now.Add(time.Second),
	}
}
