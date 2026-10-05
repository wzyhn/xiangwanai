package registration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/google/uuid"
)

type ContactSource string

const ContactSourceManual ContactSource = "manual"

var (
	contactPhoneE164Pattern     = regexp.MustCompile(`^\+[1-9][0-9]{7,14}$`)
	privacyPolicyVersionPattern = regexp.MustCompile(
		`^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$`,
	)
)

type ContactSnapshot struct {
	Source        ContactSource
	Name          string
	PhoneE164     string
	PolicyVersion string
}

type RegistrationSubmission struct {
	InstancePublicationVersion int64
	PriceCents                 int64
	PrivacyPolicyVersion       string
	Contact                    ContactSnapshot
	QuestionnaireVersionID     *uuid.UUID
	Answers                    []activity.QuestionnaireAnswer
}

type PreparedRegistrationSubmission struct {
	InstancePublicationVersion int64
	PriceCents                 int64
	PrivacyPolicyVersion       string
	Contact                    ContactSnapshot
	QuestionnaireVersionID     *uuid.UUID
	Answers                    []activity.QuestionnaireAnswer
	RequestFingerprint         string
}

var ErrInvalidRegistrationSubmission = errors.New(
	"invalid xiangwan Registration submission",
)

func PrepareRegistrationSubmission(
	sessionID uuid.UUID,
	submission RegistrationSubmission,
) (PreparedRegistrationSubmission, error) {
	contact := ContactSnapshot{
		Source:        submission.Contact.Source,
		Name:          strings.TrimSpace(submission.Contact.Name),
		PhoneE164:     strings.TrimSpace(submission.Contact.PhoneE164),
		PolicyVersion: strings.TrimSpace(submission.Contact.PolicyVersion),
	}
	privacyPolicyVersion := strings.TrimSpace(submission.PrivacyPolicyVersion)
	if sessionID == uuid.Nil || submission.InstancePublicationVersion < 1 ||
		submission.PriceCents < 0 ||
		(privacyPolicyVersion != "" &&
			!privacyPolicyVersionPattern.MatchString(privacyPolicyVersion)) ||
		contact.Source != ContactSourceManual ||
		contact.Name == "" || len([]rune(contact.Name)) > 100 ||
		!contactPhoneE164Pattern.MatchString(contact.PhoneE164) ||
		contact.PolicyVersion == "" ||
		len([]rune(contact.PolicyVersion)) > 100 ||
		len(submission.Answers) > activity.MaxQuestionnaireFields {
		return PreparedRegistrationSubmission{},
			ErrInvalidRegistrationSubmission
	}
	if submission.QuestionnaireVersionID != nil &&
		*submission.QuestionnaireVersionID == uuid.Nil {
		return PreparedRegistrationSubmission{},
			ErrInvalidRegistrationSubmission
	}
	if submission.QuestionnaireVersionID == nil && len(submission.Answers) != 0 {
		return PreparedRegistrationSubmission{},
			ErrInvalidRegistrationSubmission
	}

	answers := make([]activity.QuestionnaireAnswer, 0, len(submission.Answers))
	seenFields := make(map[uuid.UUID]struct{}, len(submission.Answers))
	for _, answer := range submission.Answers {
		if answer.FieldID == uuid.Nil ||
			len(answer.Values) > activity.MaxQuestionnaireOptions {
			return PreparedRegistrationSubmission{},
				ErrInvalidRegistrationSubmission
		}
		if _, duplicate := seenFields[answer.FieldID]; duplicate {
			return PreparedRegistrationSubmission{},
				ErrInvalidRegistrationSubmission
		}
		seenFields[answer.FieldID] = struct{}{}
		values := append([]string(nil), answer.Values...)
		for _, value := range values {
			if len([]rune(value)) > 2000 {
				return PreparedRegistrationSubmission{},
					ErrInvalidRegistrationSubmission
			}
		}
		if len(values) == 0 {
			continue
		}
		sort.Strings(values)
		answers = append(answers, activity.QuestionnaireAnswer{
			FieldID: answer.FieldID,
			Values:  values,
		})
	}
	sort.Slice(answers, func(left, right int) bool {
		return answers[left].FieldID.String() < answers[right].FieldID.String()
	})

	prepared := PreparedRegistrationSubmission{
		InstancePublicationVersion: submission.InstancePublicationVersion,
		PriceCents:                 submission.PriceCents,
		PrivacyPolicyVersion:       privacyPolicyVersion,
		Contact:                    contact,
		QuestionnaireVersionID: cloneSubmissionUUID(
			submission.QuestionnaireVersionID,
		),
		Answers: answers,
	}
	fingerprint, err := registrationSubmissionFingerprint(sessionID, prepared)
	if err != nil {
		return PreparedRegistrationSubmission{}, fmt.Errorf(
			"%w: fingerprint failed",
			ErrInvalidRegistrationSubmission,
		)
	}
	prepared.RequestFingerprint = fingerprint
	return prepared, nil
}

func registrationSubmissionFingerprint(
	sessionID uuid.UUID,
	submission PreparedRegistrationSubmission,
) (string, error) {
	type canonicalAnswer struct {
		FieldID string   `json:"field_id"`
		Values  []string `json:"values"`
	}
	type canonicalSubmission struct {
		SessionID                  string `json:"session_id"`
		InstancePublicationVersion int64  `json:"instance_publication_version"`
		PriceCents                 int64  `json:"price_cents"`
		// Omitting an absent version preserves the pre-field canonical payload,
		// allowing N-1 clients to recover an already committed operation.
		PrivacyPolicyVersion   string            `json:"privacy_policy_version,omitempty"`
		ContactSource          ContactSource     `json:"contact_source"`
		ContactName            string            `json:"contact_name"`
		ContactPhoneE164       string            `json:"contact_phone_e164"`
		ContactPolicyVersion   string            `json:"contact_policy_version"`
		QuestionnaireVersionID string            `json:"questionnaire_version_id,omitempty"`
		Answers                []canonicalAnswer `json:"answers"`
	}
	questionnaireVersionID := ""
	if submission.QuestionnaireVersionID != nil {
		questionnaireVersionID = submission.QuestionnaireVersionID.String()
	}
	answers := make([]canonicalAnswer, 0, len(submission.Answers))
	for _, answer := range submission.Answers {
		answers = append(answers, canonicalAnswer{
			FieldID: answer.FieldID.String(),
			Values:  append([]string(nil), answer.Values...),
		})
	}
	payload, err := json.Marshal(canonicalSubmission{
		SessionID:                  sessionID.String(),
		InstancePublicationVersion: submission.InstancePublicationVersion,
		PriceCents:                 submission.PriceCents,
		PrivacyPolicyVersion:       submission.PrivacyPolicyVersion,
		ContactSource:              submission.Contact.Source,
		ContactName:                submission.Contact.Name,
		ContactPhoneE164:           submission.Contact.PhoneE164,
		ContactPolicyVersion:       submission.Contact.PolicyVersion,
		QuestionnaireVersionID:     questionnaireVersionID,
		Answers:                    answers,
	})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func cloneSubmissionUUID(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
