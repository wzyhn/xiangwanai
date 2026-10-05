package xiangwanapi

import (
	"context"
	"errors"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/contribution"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/people"
	peoplepostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/people/postgres"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/wzyhn/xiangwanai/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type myBenefitsApplication interface {
	Read(context.Context, uuid.UUID) (people.MyBenefits, error)
}

type MyBenefitsHandler struct {
	service   myBenefitsApplication
	principal PrincipalResolver
}

func NewMyBenefitsHandler(
	service myBenefitsApplication,
	principal PrincipalResolver,
) *MyBenefitsHandler {
	return &MyBenefitsHandler{service: service, principal: principal}
}

func (handler *MyBenefitsHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.GET("/me/benefits", handler.GetMyBenefits)
	group.GET("/me/host-applications", handler.GetMyHostApplications)
	group.GET("/me/identity-history", handler.GetMyIdentityHistory)
}

// GetMyBenefits godoc
// @Summary Read the authenticated consumer's Xiangwan identity and benefits
// @Description Returns the caller's trusted profile binding, safe role and host-application summaries, host contributions, and configured host rules from one PostgreSQL snapshot. Submitted contact and narratives, evidence IDs, and operator identities are never exposed.
// @Tags xiangwan
// @Produce json
// @Security BearerAuth
// @Success 200 {object} MyBenefitsResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 500 {object} response.Body
// @Router /xiangwan/me/benefits [get]
func (handler *MyBenefitsHandler) GetMyBenefits(c *gin.Context) {
	if handler == nil || handler.service == nil || handler.principal == nil {
		writeError(c, errx.NewInternal("xiangwan My Benefits is unavailable"))
		return
	}
	if len(c.Request.URL.Query()) != 0 {
		writeError(c, errx.NewBadRequest("invalid My Benefits query"))
		return
	}
	principalID, err := handler.principal(c)
	if err != nil {
		writeError(c, err)
		return
	}
	result, err := handler.service.Read(c.Request.Context(), principalID)
	if err != nil {
		writeMyBenefitsError(c, err)
		return
	}
	response.OK(c, projectMyBenefitsResponse(result))
}

// GetMyHostApplications godoc
// @Summary Read the authenticated consumer's Xiangwan host applications
// @Description Returns only the caller's host-application status history and whether a new application action is currently allowed. Submitted contact and narratives, reviewer identities, and trusted binding evidence are never exposed.
// @Tags xiangwan
// @Produce json
// @Security BearerAuth
// @Success 200 {object} MyHostApplicationsResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 500 {object} response.Body
// @Router /xiangwan/me/host-applications [get]
func (handler *MyBenefitsHandler) GetMyHostApplications(c *gin.Context) {
	if handler == nil || handler.service == nil || handler.principal == nil {
		writeError(c, errx.NewInternal("xiangwan Host Applications are unavailable"))
		return
	}
	if len(c.Request.URL.Query()) != 0 {
		writeError(c, errx.NewBadRequest("invalid Host Applications query"))
		return
	}
	principalID, err := handler.principal(c)
	if err != nil {
		writeError(c, err)
		return
	}
	result, err := handler.service.Read(c.Request.Context(), principalID)
	if err != nil {
		writeMyHostApplicationsError(c, err)
		return
	}
	response.OK(c, projectMyHostApplicationsResponse(result))
}

// GetMyIdentityHistory godoc
// @Summary Read the authenticated consumer's Xiangwan identity history
// @Description Returns only identity facts derived from an explicit trusted People binding, Instance role history, and the append-only host-contribution ledger. Public profile similarity, application narratives, evidence IDs, and operator identities are never treated as identity.
// @Tags xiangwan
// @Produce json
// @Security BearerAuth
// @Success 200 {object} MyIdentityHistoryResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 500 {object} response.Body
// @Router /xiangwan/me/identity-history [get]
func (handler *MyBenefitsHandler) GetMyIdentityHistory(c *gin.Context) {
	if handler == nil || handler.service == nil || handler.principal == nil {
		writeError(c, errx.NewInternal("xiangwan Identity History is unavailable"))
		return
	}
	if len(c.Request.URL.Query()) != 0 {
		writeError(c, errx.NewBadRequest("invalid Identity History query"))
		return
	}
	principalID, err := handler.principal(c)
	if err != nil {
		writeError(c, err)
		return
	}
	result, err := handler.service.Read(c.Request.Context(), principalID)
	if err != nil {
		writeMyIdentityHistoryError(c, err)
		return
	}
	response.OK(c, projectMyIdentityHistoryResponse(result))
}

func writeMyBenefitsError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidMyBenefitsRequest):
		writeError(c, errx.NewBadRequest("invalid My Benefits request"))
	case errors.Is(err, peoplepostgres.ErrHostApplicationForbidden),
		errors.Is(err, peoplepostgres.ErrHostApplicationApplicantUnavailable):
		writeError(c, errx.NewForbidden("My Benefits is unavailable"))
	default:
		_ = c.Error(err)
		writeError(c, errx.NewInternal("xiangwan My Benefits failed"))
	}
}

func writeMyHostApplicationsError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidMyBenefitsRequest):
		writeError(c, errx.NewBadRequest("invalid Host Applications request"))
	case errors.Is(err, peoplepostgres.ErrHostApplicationForbidden),
		errors.Is(err, peoplepostgres.ErrHostApplicationApplicantUnavailable):
		writeError(c, errx.NewForbidden("Host Applications are unavailable"))
	default:
		_ = c.Error(err)
		writeError(c, errx.NewInternal("xiangwan Host Applications failed"))
	}
}

func writeMyIdentityHistoryError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidMyBenefitsRequest):
		writeError(c, errx.NewBadRequest("invalid Identity History request"))
	case errors.Is(err, peoplepostgres.ErrHostApplicationForbidden),
		errors.Is(err, peoplepostgres.ErrHostApplicationApplicantUnavailable):
		writeError(c, errx.NewForbidden("Identity History is unavailable"))
	default:
		_ = c.Error(err)
		writeError(c, errx.NewInternal("xiangwan Identity History failed"))
	}
}

type MyBenefitsResponse struct {
	TrustedPeopleProfileID   string                       `json:"trusted_people_profile_id,omitempty"`
	HasHostIdentity          bool                         `json:"has_host_identity"`
	CurrentRoles             []MyIdentityRoleResponse     `json:"current_roles"`
	RoleHistory              []MyIdentityRoleResponse     `json:"role_history"`
	HostRules                MyHostRulesResponse          `json:"host_rules"`
	CanApplyForHost          bool                         `json:"can_apply_for_host"`
	CurrentHostApplication   *MyHostApplicationResponse   `json:"current_host_application,omitempty"`
	HostApplicationHistory   []MyHostApplicationResponse  `json:"host_application_history"`
	HostContributionCount    int                          `json:"host_contribution_count"`
	HostContributionHistory  []MyHostContributionResponse `json:"host_contribution_history"`
	IdentityHistoryAvailable bool                         `json:"identity_history_available"`
}

type MyIdentityRoleResponse struct {
	SeriesID       string                   `json:"series_id"`
	InstanceID     string                   `json:"instance_id"`
	RoleCode       people.InstanceRoleCode  `json:"role_code"`
	RoleStatus     people.RoleStatus        `json:"role_status"`
	InstanceStatus activity.InstanceStatus  `json:"instance_status"`
	State          people.IdentityRoleState `json:"state"`
	SeriesTitle    string                   `json:"series_title"`
	InstanceTitle  string                   `json:"instance_title"`
	GrantedAt      string                   `json:"granted_at"`
	RevokedAt      string                   `json:"revoked_at,omitempty"`
}

type MyHostRulesResponse struct {
	ApplicationCycle string                `json:"application_cycle,omitempty"`
	PolicyVersion    string                `json:"policy_version,omitempty"`
	State            people.HostRulesState `json:"state"`
	Requirements     string                `json:"requirements,omitempty"`
	Benefits         string                `json:"benefits,omitempty"`
}

type MyHostApplicationResponse struct {
	ApplicationID     string                       `json:"application_id"`
	ApplicationCycle  string                       `json:"application_cycle"`
	PolicyVersion     string                       `json:"policy_version"`
	ApplicationStatus people.HostApplicationStatus `json:"application_status"`
	ReviewComment     string                       `json:"review_comment,omitempty"`
	Version           int64                        `json:"version"`
	SubmittedAt       string                       `json:"submitted_at"`
	UpdatedAt         string                       `json:"updated_at"`
}

type MyHostApplicationsResponse struct {
	HasHostIdentity        bool                        `json:"has_host_identity"`
	CanApplyForHost        bool                        `json:"can_apply_for_host"`
	CurrentHostApplication *MyHostApplicationResponse  `json:"current_host_application,omitempty"`
	Items                  []MyHostApplicationResponse `json:"items"`
}

type MyIdentityHistoryResponse struct {
	TrustedPeopleProfileID   string                       `json:"trusted_people_profile_id,omitempty"`
	HasHostIdentity          bool                         `json:"has_host_identity"`
	CurrentRoles             []MyIdentityRoleResponse     `json:"current_roles"`
	RoleHistory              []MyIdentityRoleResponse     `json:"role_history"`
	HostContributionCount    int                          `json:"host_contribution_count"`
	HostContributionHistory  []MyHostContributionResponse `json:"host_contribution_history"`
	IdentityHistoryAvailable bool                         `json:"identity_history_available"`
}

type MyHostContributionResponse struct {
	SeriesID         string             `json:"series_id"`
	InstanceID       string             `json:"instance_id"`
	ContributionType contribution.Type  `json:"contribution_type"`
	State            contribution.State `json:"state"`
	EarnedAt         string             `json:"earned_at"`
	ReversedAt       string             `json:"reversed_at,omitempty"`
}

func projectMyBenefitsResponse(value people.MyBenefits) MyBenefitsResponse {
	result := MyBenefitsResponse{
		HasHostIdentity:          value.HasHostIdentity,
		CurrentRoles:             make([]MyIdentityRoleResponse, 0, len(value.CurrentRoles)),
		RoleHistory:              make([]MyIdentityRoleResponse, 0, len(value.RoleHistory)),
		HostRules:                projectMyHostRules(value),
		CanApplyForHost:          value.CanApplyForHost,
		HostApplicationHistory:   make([]MyHostApplicationResponse, 0, len(value.HostApplicationHistory)),
		HostContributionCount:    value.HostContributionCount,
		HostContributionHistory:  make([]MyHostContributionResponse, 0, len(value.HostContributionHistory)),
		IdentityHistoryAvailable: value.IdentityHistoryAvailable,
	}
	if value.TrustedPeopleProfileID != nil {
		result.TrustedPeopleProfileID = value.TrustedPeopleProfileID.String()
	}
	for _, role := range value.CurrentRoles {
		result.CurrentRoles = append(result.CurrentRoles, projectMyIdentityRole(role))
	}
	for _, role := range value.RoleHistory {
		result.RoleHistory = append(result.RoleHistory, projectMyIdentityRole(role))
	}
	if value.CurrentHostApplication != nil {
		application := projectMyHostApplication(*value.CurrentHostApplication)
		result.CurrentHostApplication = &application
	}
	for _, application := range value.HostApplicationHistory {
		result.HostApplicationHistory = append(
			result.HostApplicationHistory,
			projectMyHostApplication(application),
		)
	}
	for _, item := range value.HostContributionHistory {
		result.HostContributionHistory = append(
			result.HostContributionHistory,
			projectMyHostContribution(item),
		)
	}
	return result
}

func projectMyHostApplicationsResponse(
	value people.MyBenefits,
) MyHostApplicationsResponse {
	result := MyHostApplicationsResponse{
		HasHostIdentity: value.HasHostIdentity,
		CanApplyForHost: value.CanApplyForHost,
		Items: make(
			[]MyHostApplicationResponse,
			0,
			len(value.HostApplicationHistory),
		),
	}
	if value.CurrentHostApplication != nil {
		current := projectMyHostApplication(*value.CurrentHostApplication)
		result.CurrentHostApplication = &current
	}
	for _, application := range value.HostApplicationHistory {
		result.Items = append(
			result.Items,
			projectMyHostApplication(application),
		)
	}
	return result
}

func projectMyIdentityHistoryResponse(
	value people.MyBenefits,
) MyIdentityHistoryResponse {
	result := MyIdentityHistoryResponse{
		HasHostIdentity: value.HasHostIdentity,
		CurrentRoles: make(
			[]MyIdentityRoleResponse,
			0,
			len(value.CurrentRoles),
		),
		RoleHistory: make(
			[]MyIdentityRoleResponse,
			0,
			len(value.RoleHistory),
		),
		HostContributionCount: value.HostContributionCount,
		HostContributionHistory: make(
			[]MyHostContributionResponse,
			0,
			len(value.HostContributionHistory),
		),
		IdentityHistoryAvailable: value.IdentityHistoryAvailable,
	}
	if value.TrustedPeopleProfileID != nil {
		result.TrustedPeopleProfileID = value.TrustedPeopleProfileID.String()
	}
	for _, role := range value.CurrentRoles {
		result.CurrentRoles = append(
			result.CurrentRoles,
			projectMyIdentityRole(role),
		)
	}
	for _, role := range value.RoleHistory {
		result.RoleHistory = append(
			result.RoleHistory,
			projectMyIdentityRole(role),
		)
	}
	for _, item := range value.HostContributionHistory {
		result.HostContributionHistory = append(
			result.HostContributionHistory,
			projectMyHostContribution(item),
		)
	}
	return result
}

func projectMyIdentityRole(value people.IdentityRoleSummary) MyIdentityRoleResponse {
	result := MyIdentityRoleResponse{
		SeriesID:       value.SeriesID.String(),
		InstanceID:     value.InstanceID.String(),
		RoleCode:       value.RoleCode,
		RoleStatus:     value.RoleStatus,
		InstanceStatus: value.InstanceStatus,
		State:          value.State,
		SeriesTitle:    value.SeriesTitle,
		InstanceTitle:  value.InstanceTitle,
		GrantedAt:      formatMyRegistrationTime(value.GrantedAt),
	}
	if value.RevokedAt != nil {
		result.RevokedAt = formatMyRegistrationTime(*value.RevokedAt)
	}
	return result
}

func projectMyHostRules(value people.MyBenefits) MyHostRulesResponse {
	result := MyHostRulesResponse{ApplicationCycle: value.HostApplicationCycle, PolicyVersion: value.HostPolicyVersion, State: value.HostRulesState}
	if value.HostRequirements != nil {
		result.Requirements = *value.HostRequirements
	}
	if value.HostBenefits != nil {
		result.Benefits = *value.HostBenefits
	}
	return result
}

func projectMyHostApplication(
	value people.HostApplicationSummary,
) MyHostApplicationResponse {
	result := MyHostApplicationResponse{
		ApplicationID:     value.ID.String(),
		ApplicationCycle:  value.ApplicationCycle,
		PolicyVersion:     value.PolicyVersion,
		ApplicationStatus: value.ApplicationStatus,
		Version:           value.Version,
		SubmittedAt:       formatMyRegistrationTime(value.SubmittedAt),
		UpdatedAt:         formatMyRegistrationTime(value.UpdatedAt),
	}
	if value.ReviewComment != nil {
		result.ReviewComment = *value.ReviewComment
	}
	return result
}

func projectMyHostContribution(
	value contribution.HistoryItem,
) MyHostContributionResponse {
	result := MyHostContributionResponse{
		SeriesID:         value.SeriesID.String(),
		InstanceID:       value.InstanceID.String(),
		ContributionType: value.ContributionType,
		State:            value.State,
		EarnedAt:         formatMyRegistrationTime(value.EarnedAt),
	}
	if value.ReversedAt != nil {
		result.ReversedAt = formatMyRegistrationTime(*value.ReversedAt)
	}
	return result
}
