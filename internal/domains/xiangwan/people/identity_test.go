package people

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/contribution"
	"github.com/google/uuid"
)

func TestBuildMyBenefitsSeparatesTrustedIdentityFromPublicProfile(t *testing.T) {
	t.Parallel()

	tenantID := uuid.New()
	principalID := uuid.New()
	rules := HostRulesPresentation{
		Configured:       true,
		ApplicationCycle: `2026-q4`,
		PolicyVersion:    `host-rules-v3`,
		Requirements:     `Complete an interview.`,
		Benefits:         `Host support and recognition.`,
	}
	unbound, err := BuildMyBenefits(MyBenefitsFacts{
		TenantID:    tenantID,
		PrincipalID: principalID,
		HostRules:   rules,
	})
	if err != nil {
		t.Fatalf(`BuildMyBenefits(unbound) error = %v`, err)
	}
	if unbound.TrustedPeopleProfileID != nil ||
		unbound.HasHostIdentity ||
		!unbound.CanApplyForHost ||
		unbound.HostRulesState != HostRulesStateConfigured {
		t.Fatalf(`BuildMyBenefits(unbound) = %+v`, unbound)
	}

	binding := identityBinding(t, tenantID, principalID)
	bound, err := BuildMyBenefits(MyBenefitsFacts{
		TenantID:       tenantID,
		PrincipalID:    principalID,
		TrustedBinding: &binding,
		HostRules:      rules,
	})
	if err != nil ||
		bound.TrustedPeopleProfileID == nil ||
		*bound.TrustedPeopleProfileID != binding.PeopleProfileID ||
		!bound.CanApplyForHost {
		t.Fatalf(`BuildMyBenefits(bound) = %+v, %v`, bound, err)
	}
}

func TestBuildMyBenefitsDistinguishesCurrentAndHistoricalRoles(t *testing.T) {
	t.Parallel()

	tenantID := uuid.New()
	principalID := uuid.New()
	now := time.Now().UTC()
	currentHost := identityRole(
		t,
		tenantID,
		principalID,
		InstanceRoleHost,
		activity.InstanceStatusPublished,
		now,
	)
	completedGuest := identityRole(
		t,
		tenantID,
		principalID,
		InstanceRoleInvitedGuest,
		activity.InstanceStatusCompleted,
		now.Add(-time.Hour),
	)
	revokedInstructor := identityRole(
		t,
		tenantID,
		principalID,
		InstanceRoleCourseInstructor,
		activity.InstanceStatusPublished,
		now.Add(-2*time.Hour),
	)
	revoked, err := RevokeInstanceRoleBinding(
		revokedInstructor.Binding,
		RevokeInstanceRoleBindingCommand{
			ActorID: uuid.New(),
			Reason:  `corrected`,
			At:      now.Add(time.Minute),
		},
	)
	if err != nil {
		t.Fatalf(`RevokeInstanceRoleBinding() error = %v`, err)
	}
	revokedInstructor.Binding = revoked

	result, err := BuildMyBenefits(MyBenefitsFacts{
		TenantID:    tenantID,
		PrincipalID: principalID,
		Roles: []IdentityRole{
			completedGuest,
			revokedInstructor,
			currentHost,
		},
		HostRules: HostRulesPresentation{},
	})
	if err != nil {
		t.Fatalf(`BuildMyBenefits() error = %v`, err)
	}
	if !result.HasHostIdentity ||
		result.CanApplyForHost ||
		result.HostRulesState != HostRulesStatePending ||
		len(result.CurrentRoles) != 1 ||
		result.CurrentRoles[0].InstanceID != currentHost.Binding.InstanceID ||
		len(result.RoleHistory) != 3 ||
		!result.IdentityHistoryAvailable {
		t.Fatalf(`BuildMyBenefits() = %+v`, result)
	}
	if result.RoleHistory[0].State != IdentityRoleStateCurrent ||
		result.RoleHistory[1].State != IdentityRoleStateHistorical ||
		result.RoleHistory[2].State != IdentityRoleStateHistorical {
		t.Fatalf(`role history = %+v`, result.RoleHistory)
	}
}

func TestBuildMyBenefitsAllowsFormerHostToReapply(t *testing.T) {
	t.Parallel()

	tenantID := uuid.New()
	principalID := uuid.New()
	formerHost := identityRole(
		t,
		tenantID,
		principalID,
		InstanceRoleHost,
		activity.InstanceStatusCompleted,
		time.Now().UTC(),
	)
	result, err := BuildMyBenefits(MyBenefitsFacts{
		TenantID:    tenantID,
		PrincipalID: principalID,
		Roles:       []IdentityRole{formerHost},
		HostRules: HostRulesPresentation{
			Configured:       true,
			ApplicationCycle: `2027-q1`,
			PolicyVersion:    `host-rules-v4`,
			Requirements:     `Requirements`,
			Benefits:         `Benefits`,
		},
	})
	if err != nil || result.HasHostIdentity || !result.CanApplyForHost ||
		len(result.CurrentRoles) != 0 || len(result.RoleHistory) != 1 {
		t.Fatalf(`BuildMyBenefits(former host) = %+v, %v`, result, err)
	}
}

func TestBuildMyBenefitsUsesApplicationsWithoutExposingSubmittedContact(
	t *testing.T,
) {
	t.Parallel()

	tenantID := uuid.New()
	principalID := uuid.New()
	pending := identityHostApplication(t, tenantID, principalID)
	result, err := BuildMyBenefits(MyBenefitsFacts{
		TenantID:         tenantID,
		PrincipalID:      principalID,
		HostApplications: []HostApplication{pending},
		HostRules: HostRulesPresentation{
			Configured:       true,
			ApplicationCycle: pending.ApplicationCycle,
			PolicyVersion:    pending.PolicyVersion,
			Requirements:     `Requirements`,
			Benefits:         `Benefits`,
		},
	})
	if err != nil {
		t.Fatalf(`BuildMyBenefits() error = %v`, err)
	}
	if result.CanApplyForHost ||
		result.HasHostIdentity ||
		result.CurrentHostApplication == nil ||
		result.CurrentHostApplication.ID != pending.ID ||
		len(result.HostApplicationHistory) != 1 {
		t.Fatalf(`BuildMyBenefits() = %+v`, result)
	}

	approved, _, err := ReviewHostApplication(
		pending,
		ReviewHostApplicationCommand{
			Decision: HostApplicationStatusApproved,
			ActorID:  uuid.New(),
			Comment:  `approved`,
			At:       pending.UpdatedAt.Add(time.Minute),
		},
	)
	if err != nil {
		t.Fatalf(`ReviewHostApplication() error = %v`, err)
	}
	host, err := BuildMyBenefits(MyBenefitsFacts{
		TenantID:         tenantID,
		PrincipalID:      principalID,
		HostApplications: []HostApplication{approved},
		HostRules: HostRulesPresentation{
			Configured:       true,
			ApplicationCycle: approved.ApplicationCycle,
			PolicyVersion:    approved.PolicyVersion,
			Requirements:     `Requirements`,
			Benefits:         `Benefits`,
		},
	})
	if err != nil || !host.HasHostIdentity || host.CanApplyForHost {
		t.Fatalf(`BuildMyBenefits(approved) = %+v, %v`, host, err)
	}
}

func TestBuildMyBenefitsIncludesOnlyThePrincipalsContributionHistory(
	t *testing.T,
) {
	t.Parallel()

	tenantID := uuid.New()
	principalID := uuid.New()
	earned := identityContribution(t, tenantID, principalID)
	reversal, err := contribution.Reverse(contribution.ReverseCommand{
		Earned:         earned,
		CheckinEventID: uuid.New(),
		OccurredAt:     earned.OccurredAt.Add(time.Minute),
		RecordedAt:     earned.RecordedAt.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf(`contribution.Reverse() error = %v`, err)
	}
	result, err := BuildMyBenefits(MyBenefitsFacts{
		TenantID:            tenantID,
		PrincipalID:         principalID,
		ContributionEntries: []contribution.Entry{reversal, earned},
	})
	if err != nil || result.HostContributionCount != 0 ||
		len(result.HostContributionHistory) != 1 ||
		result.HostContributionHistory[0].State != contribution.StateReversed ||
		!result.IdentityHistoryAvailable {
		t.Fatalf(`BuildMyBenefits(contributions) = %+v, %v`, result, err)
	}

	crossPrincipal := earned
	crossPrincipal.PrincipalID = uuid.New()
	if _, err := BuildMyBenefits(MyBenefitsFacts{
		TenantID:            tenantID,
		PrincipalID:         principalID,
		ContributionEntries: []contribution.Entry{crossPrincipal},
	}); !errors.Is(err, ErrInvalidMyBenefitsFacts) {
		t.Fatalf(`BuildMyBenefits(cross principal contribution) error = %v`, err)
	}
}

func TestBuildMyBenefitsRejectsCrossPrincipalOrInventedRules(t *testing.T) {
	t.Parallel()

	tenantID := uuid.New()
	principalID := uuid.New()
	binding := identityBinding(t, tenantID, uuid.New())
	if _, err := BuildMyBenefits(MyBenefitsFacts{
		TenantID:       tenantID,
		PrincipalID:    principalID,
		TrustedBinding: &binding,
	}); !errors.Is(err, ErrInvalidMyBenefitsFacts) {
		t.Fatalf(`cross-principal binding error = %v`, err)
	}
	if _, err := BuildMyBenefits(MyBenefitsFacts{
		TenantID:    tenantID,
		PrincipalID: principalID,
		HostRules: HostRulesPresentation{
			Configured:   false,
			Requirements: `invented requirement`,
		},
	}); !errors.Is(err, ErrInvalidMyBenefitsFacts) {
		t.Fatalf(`unconfigured rules content error = %v`, err)
	}
}

func TestBuildMyBenefitsRejectsDuplicateFactsAndOversizedTitles(
	t *testing.T,
) {
	t.Parallel()

	tenantID := uuid.New()
	principalID := uuid.New()
	role := identityRole(
		t,
		tenantID,
		principalID,
		InstanceRoleEventSpeaker,
		activity.InstanceStatusPublished,
		time.Now().UTC(),
	)
	application := identityHostApplication(t, tenantID, principalID)
	tests := []struct {
		name  string
		facts MyBenefitsFacts
	}{
		{
			name: `duplicate role`,
			facts: MyBenefitsFacts{
				TenantID:    tenantID,
				PrincipalID: principalID,
				Roles:       []IdentityRole{role, role},
			},
		},
		{
			name: `duplicate application`,
			facts: MyBenefitsFacts{
				TenantID:         tenantID,
				PrincipalID:      principalID,
				HostApplications: []HostApplication{application, application},
			},
		},
		{
			name: `oversized role title`,
			facts: MyBenefitsFacts{
				TenantID:    tenantID,
				PrincipalID: principalID,
				Roles: []IdentityRole{func() IdentityRole {
					oversized := role
					oversized.SeriesTitle = strings.Repeat(`界`, 201)
					return oversized
				}()},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := BuildMyBenefits(test.facts); !errors.Is(
				err,
				ErrInvalidMyBenefitsFacts,
			) {
				t.Fatalf(`BuildMyBenefits() error = %v`, err)
			}
		})
	}
}

func identityBinding(
	t *testing.T,
	tenantID uuid.UUID,
	principalID uuid.UUID,
) Binding {
	t.Helper()
	value, err := NewBinding(NewBindingCommand{
		TenantID:        tenantID,
		PeopleProfileID: uuid.New(),
		PrincipalID:     principalID,
		EvidenceDigest:  EvidenceDigest{1},
		ActorID:         uuid.New(),
		At:              time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf(`NewBinding() error = %v`, err)
	}
	return value
}

func identityRole(
	t *testing.T,
	tenantID uuid.UUID,
	principalID uuid.UUID,
	roleCode InstanceRoleCode,
	instanceStatus activity.InstanceStatus,
	at time.Time,
) IdentityRole {
	t.Helper()
	binding, err := NewInstanceRoleBinding(
		NewInstanceRoleBindingCommand{
			TenantID:    tenantID,
			SeriesID:    uuid.New(),
			InstanceID:  uuid.New(),
			PrincipalID: principalID,
			RoleCode:    roleCode,
			GrantReason: `verified`,
			ActorID:     uuid.New(),
			At:          at,
		},
	)
	if err != nil {
		t.Fatalf(`NewInstanceRoleBinding() error = %v`, err)
	}
	return IdentityRole{
		Binding:        binding,
		SeriesTitle:    `Series`,
		InstanceTitle:  `Instance`,
		InstanceStatus: instanceStatus,
	}
}

func identityHostApplication(
	t *testing.T,
	tenantID uuid.UUID,
	principalID uuid.UUID,
) HostApplication {
	t.Helper()
	value, err := NewHostApplication(NewHostApplicationCommand{
		TenantID:             tenantID,
		PrincipalID:          principalID,
		ApplicationCycle:     `2026-q4`,
		PolicyVersion:        `host-rules-v3`,
		PersonalIntroduction: `Private introduction`,
		RelevantExperience:   `Private experience`,
		Availability:         `Private availability`,
		ContactMethod:        `private contact`,
		SubmittedAt:          time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf(`NewHostApplication() error = %v`, err)
	}
	return value
}

func identityContribution(
	t *testing.T,
	tenantID uuid.UUID,
	principalID uuid.UUID,
) contribution.Entry {
	t.Helper()
	at := time.Now().UTC()
	value, err := contribution.Earn(contribution.EarnCommand{
		TenantID:         tenantID,
		PeopleProfileID:  uuid.New(),
		PeopleBindingID:  uuid.New(),
		PrincipalID:      principalID,
		SeriesID:         uuid.New(),
		InstanceID:       uuid.New(),
		RegistrationID:   uuid.New(),
		SessionID:        uuid.New(),
		CheckinID:        uuid.New(),
		CheckinEventID:   uuid.New(),
		RoleBindingID:    uuid.New(),
		ContributionType: contribution.TypeHostCheckin,
		OccurredAt:       at,
		RecordedAt:       at,
	})
	if err != nil {
		t.Fatalf(`contribution.Earn() error = %v`, err)
	}
	return value
}
