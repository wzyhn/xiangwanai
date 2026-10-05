// Package consumerprofile owns Xiangwan-specific consumer profile fields and
// their versioned candidate, moderation, and publication contracts. Nickname,
// avatar, account status, and provider identity remain Auth-owned facts.
package consumerprofile

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	MaxOccupationRunes                = 80
	MaxIntroductionRunes              = 500
	MaxTags                           = 8
	MaxTagRunes                       = 30
	WeChatTextModerationPolicyVersion = "wechat_msg_sec_check_v2.scene_1"
)

var (
	ErrInvalidFields          = errors.New("invalid xiangwan ConsumerProfile fields")
	ErrInvalidPatch           = errors.New("invalid xiangwan ConsumerProfile patch")
	ErrInvalidSnapshot        = errors.New("invalid xiangwan ConsumerProfile snapshot")
	ErrInvalidMutationReceipt = errors.New("invalid xiangwan ConsumerProfile mutation receipt")
)

type ModerationStatus string

const (
	ModerationStatusApproved      ModerationStatus = "approved"
	ModerationStatusPendingReview ModerationStatus = "pending_review"
	ModerationStatusRejected      ModerationStatus = "rejected"
)

type ModerationSource string

const (
	ModerationSourceEmptyContent        ModerationSource = "empty_content"
	ModerationSourceWeChatTextV2        ModerationSource = "wechat_text_v2"
	ModerationSourceProviderUnavailable ModerationSource = "provider_unavailable"
)

type ProviderObservation struct {
	TraceID       string
	ObservedAt    time.Time
	PolicyVersion string
	Label         int
	Suggest       string
}

type ModerationOutcome struct {
	Status      ModerationStatus
	Source      ModerationSource
	OccurredAt  time.Time
	Observation *ProviderObservation
}

type ModerationTask struct {
	CandidateID uuid.UUID
	PrincipalID uuid.UUID
	LeaseToken  uuid.UUID
	Fields      Fields
	Attempt     int
}

type Visibility struct {
	Occupation   bool
	Introduction bool
	Tags         bool
}

type Fields struct {
	Occupation   string
	Introduction string
	Tags         []string
	Visibility   Visibility
}

type PrincipalProfile struct {
	Nickname     string
	AvatarURL    string
	AvatarFileID *uuid.UUID
	ETag         string
}

type PublishedProfile struct {
	Fields               Fields
	PrivacyPolicyVersion string
	Version              int64
	UpdatedAt            time.Time
}

type PendingUpdate struct {
	Fields           Fields
	CandidateVersion int64
	SubmittedAt      time.Time
}

type Snapshot struct {
	Principal PrincipalProfile
	Published PublishedProfile
	Pending   *PendingUpdate
}

type Patch struct {
	TenantID             uuid.UUID
	PrincipalID          uuid.UUID
	OperationKey         uuid.UUID
	ExpectedVersion      int64
	Fields               Fields
	PrivacyPolicyVersion string
}

type MutationReceipt struct {
	CandidateID      uuid.UUID
	Fields           Fields
	BaseVersion      int64
	CandidateVersion int64
	ModerationStatus ModerationStatus
	PublishedVersion int64
	PrivacyVersion   string
	SubmittedAt      time.Time
	Replayed         bool
}

func NormalizeFields(value Fields) (Fields, error) {
	value.Occupation = strings.TrimSpace(value.Occupation)
	value.Introduction = normalizeIntroduction(value.Introduction)
	if !validText(value.Occupation, MaxOccupationRunes, false) ||
		!validText(value.Introduction, MaxIntroductionRunes, true) ||
		len(value.Tags) > MaxTags {
		return Fields{}, ErrInvalidFields
	}
	tags := make([]string, 0, len(value.Tags))
	seen := make(map[string]struct{}, len(value.Tags))
	for _, raw := range value.Tags {
		tag := strings.TrimSpace(raw)
		if !validText(tag, MaxTagRunes, false) || tag == "" {
			return Fields{}, ErrInvalidFields
		}
		if _, duplicate := seen[tag]; duplicate {
			return Fields{}, ErrInvalidFields
		}
		seen[tag] = struct{}{}
		tags = append(tags, tag)
	}
	value.Tags = tags
	return value, nil
}

func ValidatePatch(value Patch) error {
	if value.TenantID == uuid.Nil || value.PrincipalID == uuid.Nil ||
		value.OperationKey == uuid.Nil || value.OperationKey.Version() != 4 ||
		value.OperationKey.Variant() != uuid.RFC4122 ||
		value.ExpectedVersion < 0 ||
		!ValidPolicyVersion(value.PrivacyPolicyVersion) {
		return ErrInvalidPatch
	}
	normalized, err := NormalizeFields(value.Fields)
	if err != nil || !equalFields(normalized, value.Fields) {
		return ErrInvalidPatch
	}
	return nil
}

func PatchFingerprint(value Patch) ([sha256.Size]byte, error) {
	if value.TenantID == uuid.Nil || value.PrincipalID == uuid.Nil ||
		value.OperationKey == uuid.Nil || value.OperationKey.Version() != 4 ||
		value.OperationKey.Variant() != uuid.RFC4122 ||
		value.ExpectedVersion < 0 {
		return [sha256.Size]byte{}, ErrInvalidPatch
	}
	normalized, err := NormalizeFields(value.Fields)
	if err != nil || !equalFields(normalized, value.Fields) {
		return [sha256.Size]byte{}, ErrInvalidPatch
	}
	payload, err := json.Marshal(struct {
		TenantID        string     `json:"tenant_id"`
		PrincipalID     string     `json:"principal_id"`
		ExpectedVersion int64      `json:"expected_version"`
		Occupation      string     `json:"occupation"`
		Introduction    string     `json:"introduction"`
		Tags            []string   `json:"tags"`
		Visibility      Visibility `json:"visibility"`
	}{
		TenantID:        value.TenantID.String(),
		PrincipalID:     value.PrincipalID.String(),
		ExpectedVersion: value.ExpectedVersion,
		Occupation:      value.Fields.Occupation,
		Introduction:    value.Fields.Introduction,
		Tags:            value.Fields.Tags,
		Visibility:      value.Fields.Visibility,
	})
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("fingerprint ConsumerProfile patch: %w", err)
	}
	return sha256.Sum256(payload), nil
}

func ModerationText(value Fields) string {
	parts := make([]string, 0, 3)
	if value.Occupation != "" {
		parts = append(parts, value.Occupation)
	}
	if value.Introduction != "" {
		parts = append(parts, value.Introduction)
	}
	if len(value.Tags) != 0 {
		parts = append(parts, strings.Join(value.Tags, " "))
	}
	return strings.Join(parts, "\n")
}

func PrincipalProfileETag(
	principalID uuid.UUID,
	nickname string,
	avatarURL string,
	avatarFileID *uuid.UUID,
) (string, error) {
	if principalID == uuid.Nil ||
		!validExternalText(nickname) || !validExternalText(avatarURL) ||
		(avatarFileID != nil && *avatarFileID == uuid.Nil) {
		return "", ErrInvalidSnapshot
	}
	fileID := ""
	if avatarFileID != nil {
		fileID = avatarFileID.String()
	}
	payload, err := json.Marshal([]string{
		principalID.String(),
		nickname,
		avatarURL,
		fileID,
	})
	if err != nil {
		return "", ErrInvalidSnapshot
	}
	digest := sha256.Sum256(payload)
	return "pp_" + base64.RawURLEncoding.EncodeToString(digest[:]), nil
}

func ValidateSnapshot(value Snapshot) error {
	if value.Principal.ETag == "" ||
		!strings.HasPrefix(value.Principal.ETag, "pp_") ||
		!validExternalText(value.Principal.Nickname) ||
		!validExternalText(value.Principal.AvatarURL) ||
		(value.Principal.AvatarFileID != nil &&
			*value.Principal.AvatarFileID == uuid.Nil) {
		return ErrInvalidSnapshot
	}
	if value.Published.Version == 0 {
		if value.Published.PrivacyPolicyVersion != "" ||
			!value.Published.UpdatedAt.IsZero() ||
			!emptyFields(value.Published.Fields) {
			return ErrInvalidSnapshot
		}
	} else if value.Published.Version < 1 ||
		!ValidPolicyVersion(value.Published.PrivacyPolicyVersion) ||
		value.Published.UpdatedAt.IsZero() ||
		ValidateNormalizedFields(value.Published.Fields) != nil {
		return ErrInvalidSnapshot
	}
	if value.Pending != nil &&
		(value.Pending.CandidateVersion < 1 || value.Pending.SubmittedAt.IsZero() ||
			ValidateNormalizedFields(value.Pending.Fields) != nil) {
		return ErrInvalidSnapshot
	}
	return nil
}

func ValidateMutationReceipt(value MutationReceipt) error {
	if value.CandidateID == uuid.Nil || value.BaseVersion < 0 ||
		value.CandidateVersion != value.BaseVersion+1 ||
		!ValidPolicyVersion(value.PrivacyVersion) || value.SubmittedAt.IsZero() ||
		ValidateNormalizedFields(value.Fields) != nil {
		return ErrInvalidMutationReceipt
	}
	switch value.ModerationStatus {
	case ModerationStatusApproved:
		if value.PublishedVersion != value.CandidateVersion {
			return ErrInvalidMutationReceipt
		}
	case ModerationStatusPendingReview, ModerationStatusRejected:
		if value.PublishedVersion != 0 {
			return ErrInvalidMutationReceipt
		}
	default:
		return ErrInvalidMutationReceipt
	}
	return nil
}

func NewEmptyModerationOutcome(occurredAt time.Time) (ModerationOutcome, error) {
	value := ModerationOutcome{
		Status:     ModerationStatusApproved,
		Source:     ModerationSourceEmptyContent,
		OccurredAt: occurredAt.UTC().Truncate(time.Microsecond),
	}
	if err := ValidateModerationOutcome(value); err != nil {
		return ModerationOutcome{}, err
	}
	return value, nil
}

func NewUnavailableModerationOutcome(
	occurredAt time.Time,
) (ModerationOutcome, error) {
	value := ModerationOutcome{
		Status:     ModerationStatusPendingReview,
		Source:     ModerationSourceProviderUnavailable,
		OccurredAt: occurredAt.UTC().Truncate(time.Microsecond),
	}
	if err := ValidateModerationOutcome(value); err != nil {
		return ModerationOutcome{}, err
	}
	return value, nil
}

func NewWeChatModerationOutcome(
	suggest string,
	label int,
	traceID string,
	observedAt time.Time,
) (ModerationOutcome, error) {
	status := ModerationStatus("")
	switch suggest {
	case "pass":
		status = ModerationStatusApproved
	case "review":
		status = ModerationStatusPendingReview
	case "risky":
		status = ModerationStatusRejected
	default:
		return ModerationOutcome{}, ErrInvalidMutationReceipt
	}
	observedAt = observedAt.UTC().Truncate(time.Microsecond)
	value := ModerationOutcome{
		Status:     status,
		Source:     ModerationSourceWeChatTextV2,
		OccurredAt: observedAt,
		Observation: &ProviderObservation{
			TraceID:       traceID,
			ObservedAt:    observedAt,
			PolicyVersion: WeChatTextModerationPolicyVersion,
			Label:         label,
			Suggest:       suggest,
		},
	}
	if err := ValidateModerationOutcome(value); err != nil {
		return ModerationOutcome{}, err
	}
	return value, nil
}

func ValidateModerationOutcome(value ModerationOutcome) error {
	if value.OccurredAt.IsZero() {
		return ErrInvalidMutationReceipt
	}
	switch value.Source {
	case ModerationSourceEmptyContent:
		if value.Status != ModerationStatusApproved || value.Observation != nil {
			return ErrInvalidMutationReceipt
		}
	case ModerationSourceProviderUnavailable:
		if value.Status != ModerationStatusPendingReview || value.Observation != nil {
			return ErrInvalidMutationReceipt
		}
	case ModerationSourceWeChatTextV2:
		observation := value.Observation
		if observation == nil || observation.TraceID == "" ||
			observation.TraceID != strings.TrimSpace(observation.TraceID) ||
			len(observation.TraceID) > 128 ||
			!validExternalText(observation.TraceID) ||
			observation.ObservedAt.IsZero() ||
			!observation.ObservedAt.Equal(value.OccurredAt) ||
			observation.PolicyVersion != WeChatTextModerationPolicyVersion ||
			observation.Label < 0 || observation.Label > 1_000_000 {
			return ErrInvalidMutationReceipt
		}
		valid := (observation.Suggest == "pass" &&
			value.Status == ModerationStatusApproved) ||
			(observation.Suggest == "review" &&
				value.Status == ModerationStatusPendingReview) ||
			(observation.Suggest == "risky" &&
				value.Status == ModerationStatusRejected)
		if !valid {
			return ErrInvalidMutationReceipt
		}
	default:
		return ErrInvalidMutationReceipt
	}
	return nil
}

func ValidateModerationTask(value ModerationTask) error {
	if value.CandidateID == uuid.Nil || value.PrincipalID == uuid.Nil ||
		value.LeaseToken == uuid.Nil || value.Attempt < 1 ||
		ModerationText(value.Fields) == "" ||
		ValidateNormalizedFields(value.Fields) != nil {
		return ErrInvalidMutationReceipt
	}
	return nil
}

func ValidateNormalizedFields(value Fields) error {
	normalized, err := NormalizeFields(value)
	if err != nil || !equalFields(normalized, value) {
		return ErrInvalidFields
	}
	return nil
}

func ValidPolicyVersion(value string) bool {
	if value == "" || value != strings.TrimSpace(value) || len(value) > 64 {
		return false
	}
	for index, character := range value {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			(index > 0 && strings.ContainsRune("._:-", character)) {
			continue
		}
		return false
	}
	return true
}

func (Snapshot) String() string {
	return "xiangwan ConsumerProfileSnapshot{principal:[REDACTED],profile:[REDACTED]}"
}

func (value Snapshot) GoString() string { return value.String() }

func (Patch) String() string {
	return "xiangwan ConsumerProfilePatch{owner:[REDACTED],operation:[REDACTED],content:[REDACTED]}"
}

func (value Patch) GoString() string { return value.String() }

func (MutationReceipt) String() string {
	return "xiangwan ConsumerProfileReceipt{candidate:[REDACTED],content:[REDACTED]}"
}

func (value MutationReceipt) GoString() string { return value.String() }

func (ModerationOutcome) String() string {
	return "xiangwan ConsumerProfileModerationOutcome{evidence:[REDACTED]}"
}

func (value ModerationOutcome) GoString() string { return value.String() }

func (ModerationTask) String() string {
	return "xiangwan ConsumerProfileModerationTask{candidate:[REDACTED],content:[REDACTED]}"
}

func (value ModerationTask) GoString() string { return value.String() }

func normalizeIntroduction(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	return strings.TrimSpace(value)
}

func validText(value string, maximumRunes int, allowNewline bool) bool {
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > maximumRunes {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) && !(allowNewline && character == '\n') {
			return false
		}
	}
	return true
}

func validExternalText(value string) bool {
	return utf8.ValidString(value) && !strings.ContainsAny(value, "\r\n\x00")
}

func equalFields(left Fields, right Fields) bool {
	if left.Occupation != right.Occupation ||
		left.Introduction != right.Introduction ||
		left.Visibility != right.Visibility || len(left.Tags) != len(right.Tags) {
		return false
	}
	for index := range left.Tags {
		if left.Tags[index] != right.Tags[index] {
			return false
		}
	}
	return true
}

func emptyFields(value Fields) bool {
	return value.Occupation == "" && value.Introduction == "" &&
		len(value.Tags) == 0 && value.Visibility == (Visibility{})
}
