package people

import (
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/contribution"
	"github.com/google/uuid"
)

const MaxHostRulesNarrativeRunes = 4000

type IdentityRoleState string

const (
	IdentityRoleStateCurrent    IdentityRoleState = `current`
	IdentityRoleStateHistorical IdentityRoleState = `historical`
)

type HostRulesState string

const (
	HostRulesStatePending    HostRulesState = `pending`
	HostRulesStateConfigured HostRulesState = `configured`
)

type IdentityRole struct {
	Binding        InstanceRoleBinding
	SeriesTitle    string
	InstanceTitle  string
	InstanceStatus activity.InstanceStatus
	State          IdentityRoleState
}

type HostRulesPresentation struct {
	Configured       bool
	ApplicationCycle string
	PolicyVersion    string
	Requirements     string
	Benefits         string
}

type HostApplicationSummary struct {
	ID                uuid.UUID
	ApplicationCycle  string
	PolicyVersion     string
	ApplicationStatus HostApplicationStatus
	ReviewComment     *string
	Version           int64
	SubmittedAt       time.Time
	UpdatedAt         time.Time
}

type IdentityRoleSummary struct {
	SeriesID       uuid.UUID
	InstanceID     uuid.UUID
	RoleCode       InstanceRoleCode
	RoleStatus     RoleStatus
	InstanceStatus activity.InstanceStatus
	State          IdentityRoleState
	SeriesTitle    string
	InstanceTitle  string
	GrantedAt      time.Time
	RevokedAt      *time.Time
}

type MyBenefitsFacts struct {
	TenantID            uuid.UUID
	PrincipalID         uuid.UUID
	TrustedBinding      *Binding
	Roles               []IdentityRole
	HostApplications    []HostApplication
	ContributionEntries []contribution.Entry
	HostRules           HostRulesPresentation
}

type MyBenefits struct {
	TrustedPeopleProfileID   *uuid.UUID
	HasHostIdentity          bool
	CurrentRoles             []IdentityRoleSummary
	RoleHistory              []IdentityRoleSummary
	HostRulesState           HostRulesState
	HostApplicationCycle     string
	HostPolicyVersion        string
	HostRequirements         *string
	HostBenefits             *string
	CanApplyForHost          bool
	CurrentHostApplication   *HostApplicationSummary
	HostApplicationHistory   []HostApplicationSummary
	HostContributionCount    int
	HostContributionHistory  []contribution.HistoryItem
	IdentityHistoryAvailable bool
}

var ErrInvalidMyBenefitsFacts = errors.New(
	`invalid xiangwan MyBenefits facts`,
)

func BuildMyBenefits(facts MyBenefitsFacts) (MyBenefits, error) {
	if facts.TenantID == uuid.Nil || facts.PrincipalID == uuid.Nil {
		return MyBenefits{}, ErrInvalidMyBenefitsFacts
	}
	result := MyBenefits{
		CurrentRoles: make([]IdentityRoleSummary, 0),
		RoleHistory:  make([]IdentityRoleSummary, 0, len(facts.Roles)),
		HostApplicationHistory: make(
			[]HostApplicationSummary,
			0,
			len(facts.HostApplications),
		),
	}
	if facts.TrustedBinding != nil {
		if ValidateBinding(*facts.TrustedBinding) != nil ||
			facts.TrustedBinding.TenantID != facts.TenantID ||
			facts.TrustedBinding.PrincipalID != facts.PrincipalID ||
			facts.TrustedBinding.BindingStatus != BindingStatusActive {
			return MyBenefits{}, ErrInvalidMyBenefitsFacts
		}
		profileID := facts.TrustedBinding.PeopleProfileID
		result.TrustedPeopleProfileID = &profileID
	}

	roles := append([]IdentityRole(nil), facts.Roles...)
	roleIDs := make(map[uuid.UUID]struct{}, len(roles))
	for index := range roles {
		role := roles[index]
		if _, exists := roleIDs[role.Binding.ID]; exists {
			return MyBenefits{}, ErrInvalidMyBenefitsFacts
		}
		roleIDs[role.Binding.ID] = struct{}{}
		state, err := ClassifyIdentityRole(role.Binding, role.InstanceStatus)
		if err != nil ||
			role.Binding.TenantID != facts.TenantID ||
			role.Binding.PrincipalID != facts.PrincipalID ||
			strings.TrimSpace(role.SeriesTitle) == `` ||
			strings.TrimSpace(role.InstanceTitle) == `` ||
			role.SeriesTitle != strings.TrimSpace(role.SeriesTitle) ||
			role.InstanceTitle != strings.TrimSpace(role.InstanceTitle) ||
			len([]rune(role.SeriesTitle)) > 200 ||
			len([]rune(role.InstanceTitle)) > 200 {
			return MyBenefits{}, ErrInvalidMyBenefitsFacts
		}
		roles[index].State = state
	}
	sort.Slice(roles, func(left, right int) bool {
		if !roles[left].Binding.GrantedAt.Equal(
			roles[right].Binding.GrantedAt,
		) {
			return roles[left].Binding.GrantedAt.After(
				roles[right].Binding.GrantedAt,
			)
		}
		return roles[left].Binding.ID.String() >
			roles[right].Binding.ID.String()
	})
	for _, role := range roles {
		summary := summarizeIdentityRole(role)
		result.RoleHistory = append(result.RoleHistory, summary)
		if role.State == IdentityRoleStateCurrent {
			result.CurrentRoles = append(result.CurrentRoles, summary)
			if role.Binding.RoleCode == InstanceRoleHost {
				result.HasHostIdentity = true
			}
		}
	}

	applications := append(
		[]HostApplication(nil),
		facts.HostApplications...,
	)
	applicationIDs := make(map[uuid.UUID]struct{}, len(applications))
	approvedCount := 0
	for _, application := range applications {
		if _, exists := applicationIDs[application.ID]; exists {
			return MyBenefits{}, ErrInvalidMyBenefitsFacts
		}
		applicationIDs[application.ID] = struct{}{}
		if ValidateHostApplication(application) != nil ||
			application.TenantID != facts.TenantID ||
			application.PrincipalID != facts.PrincipalID {
			return MyBenefits{}, ErrInvalidMyBenefitsFacts
		}
		if application.ApplicationStatus == HostApplicationStatusApproved {
			approvedCount++
		}
	}
	if approvedCount > 1 {
		return MyBenefits{}, ErrInvalidMyBenefitsFacts
	}
	sort.Slice(applications, func(left, right int) bool {
		if !applications[left].SubmittedAt.Equal(
			applications[right].SubmittedAt,
		) {
			return applications[left].SubmittedAt.After(
				applications[right].SubmittedAt,
			)
		}
		return applications[left].ID.String() >
			applications[right].ID.String()
	})
	for _, application := range applications {
		summary := summarizeHostApplication(application)
		result.HostApplicationHistory = append(
			result.HostApplicationHistory,
			summary,
		)
		if application.ApplicationStatus ==
			HostApplicationStatusApproved {
			result.HasHostIdentity = true
		}
		if result.CurrentHostApplication == nil &&
			(application.ApplicationStatus ==
				HostApplicationStatusPending ||
				application.ApplicationStatus ==
					HostApplicationStatusApproved) {
			current := summary
			result.CurrentHostApplication = &current
		}
	}
	for _, entry := range facts.ContributionEntries {
		if contribution.Validate(entry) != nil ||
			entry.TenantID != facts.TenantID ||
			entry.PrincipalID != facts.PrincipalID {
			return MyBenefits{}, ErrInvalidMyBenefitsFacts
		}
	}
	contributionHistory, err := contribution.BuildHistory(
		facts.ContributionEntries,
	)
	if err != nil {
		return MyBenefits{}, ErrInvalidMyBenefitsFacts
	}
	result.HostContributionCount = contributionHistory.ActiveCount
	result.HostContributionHistory = contributionHistory.Items

	if err := applyHostRulesPresentation(&result, facts.HostRules); err != nil {
		return MyBenefits{}, err
	}
	result.CanApplyForHost =
		result.HostRulesState == HostRulesStateConfigured &&
			!result.HasHostIdentity &&
			result.CurrentHostApplication == nil
	result.IdentityHistoryAvailable =
		result.TrustedPeopleProfileID != nil ||
			len(result.RoleHistory) > 0 ||
			len(result.HostApplicationHistory) > 0 ||
			len(result.HostContributionHistory) > 0
	return result, nil
}

func summarizeIdentityRole(role IdentityRole) IdentityRoleSummary {
	return IdentityRoleSummary{
		SeriesID:       role.Binding.SeriesID,
		InstanceID:     role.Binding.InstanceID,
		RoleCode:       role.Binding.RoleCode,
		RoleStatus:     role.Binding.RoleStatus,
		InstanceStatus: role.InstanceStatus,
		State:          role.State,
		SeriesTitle:    role.SeriesTitle,
		InstanceTitle:  role.InstanceTitle,
		GrantedAt:      role.Binding.GrantedAt,
		RevokedAt:      cloneTime(role.Binding.RevokedAt),
	}
}

func ClassifyIdentityRole(
	binding InstanceRoleBinding,
	instanceStatus activity.InstanceStatus,
) (IdentityRoleState, error) {
	if ValidateInstanceRoleBinding(binding) != nil {
		return ``, ErrInvalidMyBenefitsFacts
	}
	switch instanceStatus {
	case activity.InstanceStatusDraft,
		activity.InstanceStatusPendingPublish,
		activity.InstanceStatusPublished:
		if binding.RoleStatus == RoleStatusActive {
			return IdentityRoleStateCurrent, nil
		}
	case activity.InstanceStatusCompleted,
		activity.InstanceStatusCancelled,
		activity.InstanceStatusArchived:
	default:
		return ``, ErrInvalidMyBenefitsFacts
	}
	return IdentityRoleStateHistorical, nil
}

func (MyBenefits) String() string {
	return `xiangwan MyBenefits{sensitive_fields:[REDACTED]}`
}

func (value MyBenefits) GoString() string {
	return value.String()
}

func summarizeHostApplication(
	application HostApplication,
) HostApplicationSummary {
	return HostApplicationSummary{
		ID:                application.ID,
		ApplicationCycle:  application.ApplicationCycle,
		PolicyVersion:     application.PolicyVersion,
		ApplicationStatus: application.ApplicationStatus,
		ReviewComment:     cloneString(application.ReviewComment),
		Version:           application.Version,
		SubmittedAt:       application.SubmittedAt,
		UpdatedAt:         application.UpdatedAt,
	}
}

func applyHostRulesPresentation(
	result *MyBenefits,
	rules HostRulesPresentation,
) error {
	if !rules.Configured {
		if rules.ApplicationCycle != `` ||
			rules.PolicyVersion != `` ||
			rules.Requirements != `` ||
			rules.Benefits != `` {
			return ErrInvalidMyBenefitsFacts
		}
		result.HostRulesState = HostRulesStatePending
		return nil
	}
	requirements := strings.TrimSpace(rules.Requirements)
	benefits := strings.TrimSpace(rules.Benefits)
	if !validHostApplicationReference(rules.ApplicationCycle) ||
		!validHostApplicationReference(rules.PolicyVersion) ||
		requirements == `` ||
		len([]rune(requirements)) > MaxHostRulesNarrativeRunes ||
		benefits == `` ||
		len([]rune(benefits)) > MaxHostRulesNarrativeRunes {
		return ErrInvalidMyBenefitsFacts
	}
	result.HostApplicationCycle = rules.ApplicationCycle
	result.HostPolicyVersion = rules.PolicyVersion
	result.HostRulesState = HostRulesStateConfigured
	result.HostRequirements = &requirements
	result.HostBenefits = &benefits
	return nil
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}
