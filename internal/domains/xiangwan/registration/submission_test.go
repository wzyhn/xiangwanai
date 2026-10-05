package registration

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/google/uuid"
)

func TestPrepareRegistrationSubmissionNormalizesStableFingerprint(t *testing.T) {
	t.Parallel()

	sessionID := uuid.New()
	questionnaireID := uuid.New()
	fieldA := uuid.New()
	fieldB := uuid.New()
	left, err := PrepareRegistrationSubmission(sessionID, RegistrationSubmission{
		InstancePublicationVersion: 3,
		PriceCents:                 9_900,
		PrivacyPolicyVersion:       " privacy-v1 ",
		Contact: ContactSnapshot{
			Source:        ContactSourceManual,
			Name:          "  Wang Wei  ",
			PhoneE164:     " +8613812345678 ",
			PolicyVersion: " contact-v1 ",
		},
		QuestionnaireVersionID: &questionnaireID,
		Answers: []activity.QuestionnaireAnswer{
			{FieldID: fieldB, Values: []string{"z", "a"}},
			{FieldID: fieldA, Values: []string{}},
		},
	})
	if err != nil {
		t.Fatalf("PrepareRegistrationSubmission() error = %v", err)
	}
	right, err := PrepareRegistrationSubmission(sessionID, RegistrationSubmission{
		InstancePublicationVersion: 3,
		PriceCents:                 9_900,
		PrivacyPolicyVersion:       "privacy-v1",
		Contact: ContactSnapshot{
			Source:        ContactSourceManual,
			Name:          "Wang Wei",
			PhoneE164:     "+8613812345678",
			PolicyVersion: "contact-v1",
		},
		QuestionnaireVersionID: &questionnaireID,
		Answers: []activity.QuestionnaireAnswer{
			{FieldID: fieldB, Values: []string{"a", "z"}},
		},
	})
	if err != nil {
		t.Fatalf("PrepareRegistrationSubmission(second) error = %v", err)
	}
	if left.RequestFingerprint != right.RequestFingerprint ||
		len(left.RequestFingerprint) != 64 ||
		left.Contact.Name != "Wang Wei" || len(left.Answers) != 1 ||
		left.Answers[0].Values[0] != "a" {
		t.Fatalf("prepared submissions left=%+v right=%+v", left, right)
	}
	left.Answers[0].Values[0] = "changed"
	if right.Answers[0].Values[0] == "changed" {
		t.Fatal("prepared submissions share answer state")
	}
}

func TestPrepareRegistrationSubmissionRejectsInvalidContactAndShape(
	t *testing.T,
) {
	t.Parallel()

	valid := RegistrationSubmission{
		InstancePublicationVersion: 3,
		PriceCents:                 9_900,
		PrivacyPolicyVersion:       "privacy-v1",
		Contact: ContactSnapshot{
			Source:        ContactSourceManual,
			Name:          "Wang Wei",
			PhoneE164:     "+8613812345678",
			PolicyVersion: "contact-v1",
		},
	}
	tests := []struct {
		name   string
		mutate func(*RegistrationSubmission)
	}{
		{name: "missing Session", mutate: func(*RegistrationSubmission) {}},
		{name: "invalid source", mutate: func(value *RegistrationSubmission) {
			value.Contact.Source = "wechat"
		}},
		{name: "missing publication version", mutate: func(value *RegistrationSubmission) {
			value.InstancePublicationVersion = 0
		}},
		{name: "negative price", mutate: func(value *RegistrationSubmission) {
			value.PriceCents = -1
		}},
		{name: "long privacy policy", mutate: func(value *RegistrationSubmission) {
			value.PrivacyPolicyVersion = strings.Repeat("v", 65)
		}},
		{name: "invalid privacy policy", mutate: func(value *RegistrationSubmission) {
			value.PrivacyPolicyVersion = "privacy/v1"
		}},
		{name: "invalid phone", mutate: func(value *RegistrationSubmission) {
			value.Contact.PhoneE164 = "13812345678"
		}},
		{name: "long name", mutate: func(value *RegistrationSubmission) {
			value.Contact.Name = strings.Repeat("名", 101)
		}},
		{name: "answers without version", mutate: func(value *RegistrationSubmission) {
			value.Answers = []activity.QuestionnaireAnswer{{
				FieldID: uuid.New(), Values: []string{"answer"},
			}}
		}},
		{name: "duplicate field", mutate: func(value *RegistrationSubmission) {
			questionnaireID := uuid.New()
			fieldID := uuid.New()
			value.QuestionnaireVersionID = &questionnaireID
			value.Answers = []activity.QuestionnaireAnswer{
				{FieldID: fieldID, Values: []string{"a"}},
				{FieldID: fieldID, Values: []string{"b"}},
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			value := valid
			test.mutate(&value)
			sessionID := uuid.New()
			if test.name == "missing Session" {
				sessionID = uuid.Nil
			}
			_, err := PrepareRegistrationSubmission(sessionID, value)
			if !errors.Is(err, ErrInvalidRegistrationSubmission) {
				t.Fatalf("PrepareRegistrationSubmission() error = %v", err)
			}
		})
	}
}

func TestPrepareRegistrationSubmissionPreservesLegacyMissingPrivacyVersion(
	t *testing.T,
) {
	t.Parallel()

	sessionID := uuid.MustParse("10000000-0000-4000-8000-000000000001")
	prepared, err := PrepareRegistrationSubmission(sessionID, RegistrationSubmission{
		InstancePublicationVersion: 1,
		Contact: ContactSnapshot{
			Source:        ContactSourceManual,
			Name:          "Wang Wei",
			PhoneE164:     "+8613812345678",
			PolicyVersion: "contact-v1",
		},
	})
	if err != nil {
		t.Fatalf("PrepareRegistrationSubmission(legacy) error = %v", err)
	}
	if prepared.PrivacyPolicyVersion != "" || prepared.RequestFingerprint == "" {
		t.Fatalf("legacy submission = %+v", prepared)
	}
	legacyPayload := []byte(
		`{"session_id":"10000000-0000-4000-8000-000000000001",` +
			`"instance_publication_version":1,"price_cents":0,` +
			`"contact_source":"manual","contact_name":"Wang Wei",` +
			`"contact_phone_e164":"+8613812345678",` +
			`"contact_policy_version":"contact-v1","answers":[]}`,
	)
	legacyDigest := sha256.Sum256(legacyPayload)
	legacyFingerprint := hex.EncodeToString(legacyDigest[:])
	if prepared.RequestFingerprint != legacyFingerprint {
		t.Fatalf(
			"missing-version fingerprint = %s, want legacy %s",
			prepared.RequestFingerprint,
			legacyFingerprint,
		)
	}

	versioned, err := PrepareRegistrationSubmission(sessionID, RegistrationSubmission{
		InstancePublicationVersion: 1,
		PrivacyPolicyVersion:       "privacy-v1",
		Contact: ContactSnapshot{
			Source:        ContactSourceManual,
			Name:          "Wang Wei",
			PhoneE164:     "+8613812345678",
			PolicyVersion: "contact-v1",
		},
	})
	if err != nil {
		t.Fatalf("PrepareRegistrationSubmission(versioned) error = %v", err)
	}
	if versioned.RequestFingerprint == legacyFingerprint {
		t.Fatal("versioned submission matched legacy missing-version fingerprint")
	}
}
