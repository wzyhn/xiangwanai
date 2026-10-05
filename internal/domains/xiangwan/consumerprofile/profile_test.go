package consumerprofile

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNormalizeFieldsBuildsCanonicalProfileContent(t *testing.T) {
	t.Parallel()

	got, err := NormalizeFields(Fields{
		Occupation:   "  产品设计  ",
		Introduction: "  第一行\r\n第二行  ",
		Tags:         []string{" 徒步 ", "咖啡"},
		Visibility: Visibility{
			Occupation: true,
			Tags:       true,
		},
	})
	if err != nil || got.Occupation != "产品设计" ||
		got.Introduction != "第一行\n第二行" || len(got.Tags) != 2 ||
		got.Tags[0] != "徒步" || !got.Visibility.Occupation {
		t.Fatalf("NormalizeFields() = %+v, %v", got, err)
	}
	for _, invalid := range []Fields{
		{Occupation: strings.Repeat("x", MaxOccupationRunes+1)},
		{Introduction: "unsafe\ttext"},
		{Tags: []string{"same", "same"}},
		{Tags: []string{""}},
		{Tags: make([]string, MaxTags+1)},
	} {
		if _, err := NormalizeFields(invalid); !errors.Is(err, ErrInvalidFields) {
			t.Fatalf("NormalizeFields(%+v) error = %v", invalid, err)
		}
	}
}

func TestPatchFingerprintBindsClientIntentButNotServerPolicy(t *testing.T) {
	t.Parallel()

	patch := validProfilePatch()
	first, err := PatchFingerprint(patch)
	if err != nil {
		t.Fatalf("PatchFingerprint() error = %v", err)
	}
	second, err := PatchFingerprint(patch)
	if err != nil || first != second {
		t.Fatalf("stable fingerprint = %x / %x, %v", first, second, err)
	}
	changed := patch
	changed.Fields.Tags = append([]string(nil), patch.Fields.Tags...)
	changed.Fields.Tags[0] = "changed"
	other, err := PatchFingerprint(changed)
	if err != nil || first == other {
		t.Fatalf("content-bound fingerprint = %x / %x, %v", first, other, err)
	}
	changedPolicy := patch
	changedPolicy.PrivacyPolicyVersion = "privacy-v2"
	policyFingerprint, err := PatchFingerprint(changedPolicy)
	if err != nil || first != policyFingerprint {
		t.Fatalf("server policy changed replay fingerprint = %x / %x, %v", first, policyFingerprint, err)
	}
	if strings.Contains(patch.String(), patch.Fields.Introduction) {
		t.Fatalf("Patch.String exposed content: %s", patch.String())
	}
}

func TestPrincipalProfileETagTracksOnlyProfileOwnedFields(t *testing.T) {
	t.Parallel()

	principalID := uuid.New()
	first, err := PrincipalProfileETag(
		principalID,
		"昵称",
		"/media/avatar",
		nil,
	)
	if err != nil || !strings.HasPrefix(first, "pp_") ||
		strings.Contains(first, principalID.String()) ||
		strings.Contains(first, "昵称") {
		t.Fatalf("PrincipalProfileETag() = %q, %v", first, err)
	}
	second, err := PrincipalProfileETag(
		principalID,
		"新昵称",
		"/media/avatar",
		nil,
	)
	if err != nil || first == second {
		t.Fatalf("profile-sensitive etag = %q / %q, %v", first, second, err)
	}
	unchanged, err := PrincipalProfileETag(
		principalID,
		"昵称",
		"/media/avatar",
		nil,
	)
	if err != nil || unchanged != first {
		t.Fatalf("unchanged profile etag = %q / %q, %v", first, unchanged, err)
	}
}

func TestMutationReceiptSeparatesPendingAndPublishedVersions(t *testing.T) {
	t.Parallel()

	receipt := MutationReceipt{
		CandidateID:      uuid.New(),
		Fields:           validProfilePatch().Fields,
		BaseVersion:      2,
		CandidateVersion: 3,
		ModerationStatus: ModerationStatusPendingReview,
		PrivacyVersion:   "privacy-v1",
		SubmittedAt:      time.Now(),
	}
	if err := ValidateMutationReceipt(receipt); err != nil {
		t.Fatalf("ValidateMutationReceipt(pending) error = %v", err)
	}
	receipt.ModerationStatus = ModerationStatusApproved
	receipt.PublishedVersion = 3
	if err := ValidateMutationReceipt(receipt); err != nil {
		t.Fatalf("ValidateMutationReceipt(approved) error = %v", err)
	}
	receipt.ModerationStatus = ModerationStatusRejected
	receipt.PublishedVersion = 0
	if err := ValidateMutationReceipt(receipt); err != nil {
		t.Fatalf("ValidateMutationReceipt(rejected) error = %v", err)
	}
	receipt.ModerationStatus = ModerationStatusApproved
	receipt.PublishedVersion = 2
	if err := ValidateMutationReceipt(receipt); !errors.Is(
		err,
		ErrInvalidMutationReceipt,
	) {
		t.Fatalf("ValidateMutationReceipt(mismatch) error = %v", err)
	}
}

func TestWeChatModerationOutcomeRequiresAuditableEvidence(t *testing.T) {
	t.Parallel()

	observedAt := time.Date(2026, 9, 14, 3, 4, 5, 0, time.UTC)
	outcome, err := NewWeChatModerationOutcome(
		"pass",
		0,
		"trace-profile-pass",
		observedAt,
	)
	if err != nil || outcome.Status != ModerationStatusApproved ||
		outcome.Observation == nil ||
		outcome.Observation.PolicyVersion !=
			WeChatTextModerationPolicyVersion ||
		!outcome.Observation.ObservedAt.Equal(observedAt) {
		t.Fatalf("NewWeChatModerationOutcome() = %+v, %v", outcome, err)
	}
	if _, err := NewWeChatModerationOutcome(
		"pass",
		0,
		"",
		observedAt,
	); !errors.Is(err, ErrInvalidMutationReceipt) {
		t.Fatalf("blank provider trace error = %v", err)
	}
	if _, err := NewWeChatModerationOutcome(
		"unknown",
		0,
		"trace-unknown",
		observedAt,
	); !errors.Is(err, ErrInvalidMutationReceipt) {
		t.Fatalf("unknown provider result error = %v", err)
	}
}

func validProfilePatch() Patch {
	return Patch{
		TenantID:             uuid.MustParse("11111111-1111-4111-8111-111111111111"),
		PrincipalID:          uuid.MustParse("22222222-2222-4222-8222-222222222222"),
		OperationKey:         uuid.MustParse("33333333-3333-4333-8333-333333333333"),
		ExpectedVersion:      2,
		PrivacyPolicyVersion: "privacy-v1",
		Fields: Fields{
			Occupation:   "产品设计",
			Introduction: "喜欢线下活动",
			Tags:         []string{"徒步", "咖啡"},
			Visibility:   Visibility{Occupation: true, Tags: true},
		},
	}
}
