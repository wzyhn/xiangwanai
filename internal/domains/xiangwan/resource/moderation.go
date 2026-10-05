package resource

import (
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	DigestSize                  = 32
	MaxModerationNarrativeRunes = 500
)

type ModerationSource string

const (
	ModerationSourceSignedCallback ModerationSource = "signed_callback"
	ModerationSourceProviderQuery  ModerationSource = "provider_query"
	ModerationSourceManualReview   ModerationSource = "manual_review"
)

type ModerationDecision string

const (
	ModerationDecisionApproved ModerationDecision = "approved"
	ModerationDecisionRejected ModerationDecision = "rejected"
	ModerationDecisionReview   ModerationDecision = "review"
	ModerationDecisionUnknown  ModerationDecision = "unknown"
)

type Digest [DigestSize]byte

type ModerationObservation struct {
	ID                uuid.UUID
	TenantID          uuid.UUID
	RelationID        uuid.UUID
	ContentID         uuid.UUID
	ContentRevision   time.Time
	Provider          string
	ProviderReference string
	PolicyVersion     string
	Source            ModerationSource
	Decision          ModerationDecision
	SubjectDigest     Digest
	PayloadDigest     Digest
	ActorID           *uuid.UUID
	Reason            *string
	ObservedAt        time.Time
	RecordedAt        time.Time
}

type RecordModerationCommand struct {
	TenantID          uuid.UUID
	RelationID        uuid.UUID
	ContentID         uuid.UUID
	ContentRevision   time.Time
	Provider          string
	ProviderReference string
	PolicyVersion     string
	Source            ModerationSource
	Decision          ModerationDecision
	SubjectDigest     []byte
	PayloadDigest     []byte
	ActorID           *uuid.UUID
	Reason            *string
	ObservedAt        time.Time
	RecordedAt        time.Time
}

var (
	ErrInvalidModerationObservation = errors.New(
		"invalid xiangwan moderation observation",
	)
	ErrModerationIntentConflict = errors.New(
		"xiangwan moderation provider reference intent conflict",
	)
)

var moderationReferencePattern = regexp.MustCompile(
	`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,255}$`,
)

func NewModerationObservation(
	command RecordModerationCommand,
) (ModerationObservation, error) {
	subjectDigest, subjectOK := newDigest(command.SubjectDigest)
	payloadDigest, payloadOK := newDigest(command.PayloadDigest)
	value := ModerationObservation{
		ID:                uuid.New(),
		TenantID:          command.TenantID,
		RelationID:        command.RelationID,
		ContentID:         command.ContentID,
		ContentRevision:   command.ContentRevision.UTC(),
		Provider:          strings.TrimSpace(command.Provider),
		ProviderReference: strings.TrimSpace(command.ProviderReference),
		PolicyVersion:     strings.TrimSpace(command.PolicyVersion),
		Source:            command.Source,
		Decision:          command.Decision,
		SubjectDigest:     subjectDigest,
		PayloadDigest:     payloadDigest,
		ActorID:           cloneUUID(command.ActorID),
		Reason:            cloneString(command.Reason),
		ObservedAt:        command.ObservedAt.UTC(),
		RecordedAt:        command.RecordedAt.UTC(),
	}
	if !subjectOK || !payloadOK || ValidateModerationObservation(value) != nil {
		return ModerationObservation{}, ErrInvalidModerationObservation
	}
	return value, nil
}

func ValidateModerationObservation(value ModerationObservation) error {
	if value.ID == uuid.Nil || value.TenantID == uuid.Nil ||
		value.RelationID == uuid.Nil || value.ContentID == uuid.Nil ||
		value.ContentRevision.IsZero() ||
		!moderationReferencePattern.MatchString(value.Provider) ||
		!moderationReferencePattern.MatchString(value.ProviderReference) ||
		!moderationReferencePattern.MatchString(value.PolicyVersion) ||
		zeroDigest(value.SubjectDigest) || zeroDigest(value.PayloadDigest) ||
		value.ObservedAt.IsZero() || value.RecordedAt.IsZero() ||
		value.ContentRevision.After(value.ObservedAt) ||
		value.ObservedAt.After(value.RecordedAt) {
		return ErrInvalidModerationObservation
	}
	switch value.Decision {
	case ModerationDecisionApproved,
		ModerationDecisionRejected,
		ModerationDecisionReview,
		ModerationDecisionUnknown:
	default:
		return ErrInvalidModerationObservation
	}
	switch value.Source {
	case ModerationSourceSignedCallback,
		ModerationSourceProviderQuery:
		if value.ActorID != nil || value.Reason != nil {
			return ErrInvalidModerationObservation
		}
	case ModerationSourceManualReview:
		if value.Decision != ModerationDecisionApproved &&
			value.Decision != ModerationDecisionRejected {
			return ErrInvalidModerationObservation
		}
		if value.ActorID == nil || *value.ActorID == uuid.Nil ||
			!validModerationNarrative(value.Reason) {
			return ErrInvalidModerationObservation
		}
	default:
		return ErrInvalidModerationObservation
	}
	return nil
}

func SameModerationIntent(
	left ModerationObservation,
	right ModerationObservation,
) bool {
	if ValidateModerationObservation(left) != nil ||
		ValidateModerationObservation(right) != nil {
		return false
	}
	return left.TenantID == right.TenantID &&
		left.RelationID == right.RelationID &&
		left.ContentID == right.ContentID &&
		left.ContentRevision.Equal(right.ContentRevision) &&
		left.Provider == right.Provider &&
		left.ProviderReference == right.ProviderReference &&
		left.PolicyVersion == right.PolicyVersion &&
		left.Source == right.Source &&
		left.Decision == right.Decision &&
		left.SubjectDigest == right.SubjectDigest &&
		left.PayloadDigest == right.PayloadDigest &&
		equalUUID(left.ActorID, right.ActorID) &&
		equalString(left.Reason, right.Reason) &&
		left.ObservedAt.Equal(right.ObservedAt)
}

func newDigest(value []byte) (Digest, bool) {
	var result Digest
	if len(value) != DigestSize {
		return result, false
	}
	copy(result[:], value)
	return result, true
}

func zeroDigest(value Digest) bool {
	return value == Digest{}
}

func validModerationNarrative(value *string) bool {
	return value != nil && *value != "" &&
		*value == strings.TrimSpace(*value) &&
		len([]rune(*value)) <= MaxModerationNarrativeRunes
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

func equalString(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
