package people

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestProfileLifecycleRequiresModerationBeforePublication(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 13, 9, 0, 0, 0, time.UTC)
	creatorID := uuid.New()
	profile, err := NewProfile(NewProfileCommand{
		TenantID:     uuid.New(),
		DisplayName:  `  Ada Lovelace  `,
		Headline:     `  AI facilitator  `,
		Introduction: `  Builds careful communities.  `,
		ActorID:      creatorID,
		At:           now,
	})
	if err != nil {
		t.Fatalf(`NewProfile() error = %v`, err)
	}
	if profile.ProfileStatus != ProfileStatusDraft ||
		profile.ModerationStatus != ModerationStatusPending ||
		profile.DisplayName != `Ada Lovelace` ||
		profile.Headline == nil ||
		*profile.Headline != `AI facilitator` ||
		profile.Introduction != `Builds careful communities.` ||
		profile.CreatedBy != creatorID ||
		profile.UpdatedBy != creatorID ||
		ValidateProfile(profile) != nil {
		t.Fatalf(`NewProfile() = %+v`, profile)
	}

	reviewerID := uuid.New()
	published, err := ReviewProfile(profile, ReviewProfileCommand{
		Decision: ModerationStatusApproved,
		ActorID:  reviewerID,
		At:       now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf(`ReviewProfile(approved) error = %v`, err)
	}
	if published.ProfileStatus != ProfileStatusPublished ||
		published.ModerationStatus != ModerationStatusApproved ||
		published.ModeratedBy == nil ||
		*published.ModeratedBy != reviewerID ||
		published.Version != 2 ||
		profile.ModeratedBy != nil {
		t.Fatalf(`published=%+v source=%+v`, published, profile)
	}
	if _, err := ReviewProfile(published, ReviewProfileCommand{
		Decision: ModerationStatusApproved,
		ActorID:  reviewerID,
		At:       now.Add(2 * time.Minute),
	}); !errors.Is(err, ErrInvalidProfileReview) {
		t.Fatalf(`ReviewProfile(already published) error = %v`, err)
	}

	revised, err := ReviseProfile(published, ReviseProfileCommand{
		DisplayName:  published.DisplayName,
		Headline:     ``,
		Introduction: `New reviewed content.`,
		ActorID:      creatorID,
		At:           now.Add(2 * time.Minute),
	})
	if err != nil {
		t.Fatalf(`ReviseProfile() error = %v`, err)
	}
	if revised.ProfileStatus != ProfileStatusDraft ||
		revised.ModerationStatus != ModerationStatusPending ||
		revised.ModeratedBy != nil ||
		revised.ModeratedAt != nil ||
		revised.Headline != nil ||
		revised.Version != 3 {
		t.Fatalf(`ReviseProfile() = %+v`, revised)
	}

	rejected, err := ReviewProfile(revised, ReviewProfileCommand{
		Decision: ModerationStatusRejected,
		ActorID:  reviewerID,
		At:       now.Add(3 * time.Minute),
	})
	if err != nil ||
		rejected.ProfileStatus != ProfileStatusDraft ||
		rejected.ModerationStatus != ModerationStatusRejected {
		t.Fatalf(`ReviewProfile(rejected) = %+v, %v`, rejected, err)
	}
	if _, err := ReviewProfile(rejected, ReviewProfileCommand{
		Decision: ModerationStatusApproved,
		ActorID:  reviewerID,
		At:       now.Add(4 * time.Minute),
	}); !errors.Is(err, ErrInvalidProfileReview) {
		t.Fatalf(`ReviewProfile(already rejected) error = %v`, err)
	}

	archived, err := ArchiveProfile(
		rejected,
		creatorID,
		now.Add(4*time.Minute),
	)
	if err != nil ||
		archived.ProfileStatus != ProfileStatusArchived ||
		archived.Version != 5 {
		t.Fatalf(`ArchiveProfile() = %+v, %v`, archived, err)
	}
	if _, err := ReviseProfile(archived, ReviseProfileCommand{
		DisplayName: `cannot revive`,
		ActorID:     creatorID,
		At:          now.Add(5 * time.Minute),
	}); !errors.Is(err, ErrProfileTerminal) {
		t.Fatalf(`ReviseProfile(archived) error = %v`, err)
	}
}

func TestProfileRejectsInvalidContentAndStateShapes(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	valid := NewProfileCommand{
		TenantID:     uuid.New(),
		DisplayName:  `Leader`,
		Introduction: `Public introduction`,
		ActorID:      uuid.New(),
		At:           now,
	}
	tests := []struct {
		name   string
		mutate func(*NewProfileCommand)
	}{
		{`tenant`, func(value *NewProfileCommand) { value.TenantID = uuid.Nil }},
		{`name`, func(value *NewProfileCommand) { value.DisplayName = `` }},
		{`name too long`, func(value *NewProfileCommand) {
			value.DisplayName = strings.Repeat(`人`, MaxProfileDisplayNameRunes+1)
		}},
		{`headline too long`, func(value *NewProfileCommand) {
			value.Headline = strings.Repeat(`讲`, MaxProfileHeadlineRunes+1)
		}},
		{`introduction too long`, func(value *NewProfileCommand) {
			value.Introduction = strings.Repeat(
				`介`,
				MaxProfileIntroductionRunes+1,
			)
		}},
		{`actor`, func(value *NewProfileCommand) { value.ActorID = uuid.Nil }},
		{`time`, func(value *NewProfileCommand) { value.At = time.Time{} }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			command := valid
			test.mutate(&command)
			if _, err := NewProfile(command); !errors.Is(err, ErrInvalidProfile) {
				t.Fatalf(`NewProfile() error = %v`, err)
			}
		})
	}

	profile, err := NewProfile(valid)
	if err != nil {
		t.Fatalf(`NewProfile(valid) error = %v`, err)
	}
	impossible := profile
	impossible.ProfileStatus = ProfileStatusPublished
	if !errors.Is(ValidateProfile(impossible), ErrInvalidProfile) {
		t.Fatal(`unapproved published profile validated`)
	}
	if _, err := ReviewProfile(profile, ReviewProfileCommand{
		Decision: ModerationStatusPending,
		ActorID:  uuid.New(),
		At:       now.Add(time.Minute),
	}); !errors.Is(err, ErrInvalidProfileReview) {
		t.Fatalf(`ReviewProfile(pending) error = %v`, err)
	}
}

func TestTrustedBindingLifecycleIsTerminalAndEvidenceOnly(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	evidence := EvidenceDigest{0xA4, 0x5C}
	binding, err := NewBinding(NewBindingCommand{
		TenantID:        uuid.New(),
		PeopleProfileID: uuid.New(),
		PrincipalID:     uuid.New(),
		EvidenceDigest:  evidence,
		ActorID:         uuid.New(),
		At:              now,
	})
	if err != nil ||
		binding.BindingStatus != BindingStatusActive ||
		binding.EvidenceDigest != evidence ||
		ValidateBinding(binding) != nil {
		t.Fatalf(`NewBinding() = %+v, %v`, binding, err)
	}
	revokerID := uuid.New()
	revoked, err := RevokeBinding(binding, RevokeBindingCommand{
		ActorID: revokerID,
		Reason:  `  incorrect identity evidence  `,
		At:      now.Add(time.Minute),
	})
	if err != nil ||
		revoked.BindingStatus != BindingStatusRevoked ||
		revoked.RevokedBy == nil ||
		*revoked.RevokedBy != revokerID ||
		revoked.RevocationReason == nil ||
		*revoked.RevocationReason != `incorrect identity evidence` ||
		revoked.Version != 2 ||
		binding.RevokedAt != nil {
		t.Fatalf(`RevokeBinding() = %+v, source=%+v, error=%v`, revoked, binding, err)
	}
	if _, err := RevokeBinding(revoked, RevokeBindingCommand{
		ActorID: revokerID,
		Reason:  `again`,
		At:      now.Add(2 * time.Minute),
	}); !errors.Is(err, ErrBindingTerminal) {
		t.Fatalf(`RevokeBinding(retry) error = %v`, err)
	}
}

func TestTrustedBindingRejectsMissingEvidenceAndInvalidRevocation(
	t *testing.T,
) {
	t.Parallel()

	command := NewBindingCommand{
		TenantID:        uuid.New(),
		PeopleProfileID: uuid.New(),
		PrincipalID:     uuid.New(),
		EvidenceDigest:  EvidenceDigest{1},
		ActorID:         uuid.New(),
		At:              time.Now().UTC(),
	}
	missingEvidence := command
	missingEvidence.EvidenceDigest = EvidenceDigest{}
	if _, err := NewBinding(missingEvidence); !errors.Is(
		err,
		ErrInvalidBinding,
	) {
		t.Fatalf(`NewBinding(no evidence) error = %v`, err)
	}
	binding, err := NewBinding(command)
	if err != nil {
		t.Fatalf(`NewBinding() error = %v`, err)
	}
	if _, err := RevokeBinding(binding, RevokeBindingCommand{
		ActorID: uuid.New(),
		Reason:  ``,
		At:      command.At.Add(time.Minute),
	}); !errors.Is(err, ErrInvalidBinding) {
		t.Fatalf(`RevokeBinding(no reason) error = %v`, err)
	}
}

func TestInstanceRoleSupportsExactCanonicalCodesAndTerminalHistory(
	t *testing.T,
) {
	t.Parallel()

	now := time.Now().UTC()
	for _, roleCode := range []InstanceRoleCode{
		InstanceRoleHost,
		InstanceRoleInvitedGuest,
		InstanceRoleCourseInstructor,
		InstanceRoleEventSpeaker,
	} {
		roleCode := roleCode
		t.Run(string(roleCode), func(t *testing.T) {
			t.Parallel()
			binding, err := NewInstanceRoleBinding(
				NewInstanceRoleBindingCommand{
					TenantID:    uuid.New(),
					SeriesID:    uuid.New(),
					InstanceID:  uuid.New(),
					PrincipalID: uuid.New(),
					RoleCode:    roleCode,
					GrantReason: `  verified by operations  `,
					ActorID:     uuid.New(),
					At:          now,
				},
			)
			if err != nil ||
				binding.RoleStatus != RoleStatusActive ||
				binding.GrantReason != `verified by operations` ||
				ValidateInstanceRoleBinding(binding) != nil {
				t.Fatalf(`NewInstanceRoleBinding() = %+v, %v`, binding, err)
			}
			revoked, err := RevokeInstanceRoleBinding(
				binding,
				RevokeInstanceRoleBindingCommand{
					ActorID: uuid.New(),
					Reason:  `  role corrected  `,
					At:      now.Add(time.Minute),
				},
			)
			if err != nil ||
				revoked.RoleStatus != RoleStatusRevoked ||
				revoked.RevocationReason == nil ||
				*revoked.RevocationReason != `role corrected` ||
				revoked.Version != 2 {
				t.Fatalf(`RevokeInstanceRoleBinding() = %+v, %v`, revoked, err)
			}
			if _, err := RevokeInstanceRoleBinding(
				revoked,
				RevokeInstanceRoleBindingCommand{
					ActorID: uuid.New(),
					Reason:  `again`,
					At:      now.Add(2 * time.Minute),
				},
			); !errors.Is(err, ErrInstanceRoleBindingTerminal) {
				t.Fatalf(`second revoke error = %v`, err)
			}
		})
	}
}

func TestInstanceRoleRejectsInventedCodeOrMissingAuditReason(t *testing.T) {
	t.Parallel()

	command := NewInstanceRoleBindingCommand{
		TenantID:    uuid.New(),
		SeriesID:    uuid.New(),
		InstanceID:  uuid.New(),
		PrincipalID: uuid.New(),
		RoleCode:    InstanceRoleHost,
		GrantReason: `verified`,
		ActorID:     uuid.New(),
		At:          time.Now().UTC(),
	}
	invalidCode := command
	invalidCode.RoleCode = InstanceRoleCode(`leader`)
	if _, err := NewInstanceRoleBinding(invalidCode); !errors.Is(
		err,
		ErrInvalidInstanceRoleBinding,
	) {
		t.Fatalf(`NewInstanceRoleBinding(invented) error = %v`, err)
	}
	missingReason := command
	missingReason.GrantReason = ``
	if _, err := NewInstanceRoleBinding(missingReason); !errors.Is(
		err,
		ErrInvalidInstanceRoleBinding,
	) {
		t.Fatalf(`NewInstanceRoleBinding(no reason) error = %v`, err)
	}
}
