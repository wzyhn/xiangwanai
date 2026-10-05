package xiangwanapi

import (
	"time"

	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/wzyhn/xiangwanai/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

// ListPeople godoc
// @Summary List Xiangwan People profiles for activity operations
// @Description Returns tenant-scoped profile facts and whether each profile has an active trusted binding. Principal IDs and contact fields are never returned.
// @Tags xiangwan-admin
// @Produce json
// @Param page query int false "Page number" minimum(1)
// @Param page_size query int false "Page size" minimum(1) maximum(100)
// @Success 200 {object} AdminPeoplePageResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Router /xiangwan/admin/people [get]
func (handler *AdminCatalogHandler) ListPeople(c *gin.Context) {
	peopleCatalog, ok := handler.peopleRoles(c)
	if !ok || !validAdminQuery(c, "page", "page_size") {
		return
	}
	principal, ok := handler.principal(c)
	if !ok {
		return
	}
	page, pageSize, ok := parseAdminPage(c)
	if !ok {
		return
	}
	result, err := peopleCatalog.ListPeople(c.Request.Context(), principal, page, pageSize)
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	items := make([]AdminPersonResponse, 0, len(result.Items))
	for _, value := range result.Items {
		items = append(items, projectAdminPerson(value))
	}
	response.OK(c, AdminPeoplePageResponse{
		Items: items, Page: result.Page, PageSize: result.PageSize, Total: result.Total,
	})
}

// ListInstanceRoles godoc
// @Summary List active People roles for one activity Instance
// @Tags xiangwan-admin
// @Produce json
// @Param instance_id path string true "Instance ID"
// @Success 200 {object} AdminInstanceRolePageResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 404 {object} response.Body
// @Router /xiangwan/admin/instances/{instance_id}/roles [get]
func (handler *AdminCatalogHandler) ListInstanceRoles(c *gin.Context) {
	peopleCatalog, ok := handler.peopleRoles(c)
	if !ok || !validAdminQuery(c) {
		return
	}
	principal, ok := handler.principal(c)
	if !ok {
		return
	}
	instanceID, err := parseCanonicalUUID(c.Param("instance_id"))
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid Instance id"))
		return
	}
	items, err := peopleCatalog.ListInstanceRoles(c.Request.Context(), principal, instanceID)
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	result := make([]AdminInstanceRoleResponse, 0, len(items))
	for _, value := range items {
		result = append(result, projectAdminInstanceRole(value))
	}
	response.OK(c, AdminInstanceRolePageResponse{Items: result})
}

// AssignInstanceRole godoc
// @Summary Assign an approved People profile to an activity role
// @Description The server resolves the profile's active trusted binding in the same transaction; callers cannot submit an arbitrary Principal ID.
// @Tags xiangwan-admin
// @Accept json
// @Produce json
// @Param Idempotency-Key header string true "Canonical lowercase UUIDv4 operation key"
// @Param instance_id path string true "Instance ID"
// @Param request body AssignAdminInstanceRoleRequest true "People profile and role"
// @Success 201 {object} AdminInstanceRoleResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 404 {object} response.Body
// @Failure 409 {object} response.Body
// @Router /xiangwan/admin/instances/{instance_id}/roles [post]
func (handler *AdminCatalogHandler) AssignInstanceRole(c *gin.Context) {
	peopleCatalog, ok := handler.peopleRoles(c)
	if !ok || !validAdminWriteRequest(c) {
		return
	}
	principal, ok := handler.principal(c)
	if !ok {
		return
	}
	operationID, err := parseOperationKey(c)
	if err != nil {
		writeError(c, err)
		return
	}
	instanceID, err := parseCanonicalUUID(c.Param("instance_id"))
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid Instance id"))
		return
	}
	var payload AssignAdminInstanceRoleRequest
	if err := decodeAdminCatalogJSON(c, &payload); err != nil {
		writeError(c, err)
		return
	}
	profileID, err := parseCanonicalUUID(payload.PeopleProfileID)
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid PeopleProfile id"))
		return
	}
	value, err := peopleCatalog.AssignInstanceRole(c.Request.Context(), xiangwanadmin.AssignInstanceRoleCommand{
		ActorID: principal.PrincipalID, IdentityLinkID: principal.IdentityLinkID,
		OperationID: operationID, RequestID: adminRequestID(c), InstanceID: instanceID,
		PeopleProfileID: profileID, RoleCode: payload.RoleCode, GrantReason: payload.GrantReason,
	})
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	response.Created(c, projectAdminInstanceRole(value))
}

// RevokeInstanceRole godoc
// @Summary Revoke one activity People role
// @Tags xiangwan-admin
// @Accept json
// @Produce json
// @Param Idempotency-Key header string true "Canonical lowercase UUIDv4 operation key"
// @Param instance_id path string true "Instance ID"
// @Param role_binding_id path string true "Role binding ID"
// @Param request body RevokeAdminInstanceRoleRequest true "Expected version and reason"
// @Success 200 {object} AdminInstanceRoleResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 404 {object} response.Body
// @Failure 409 {object} response.Body
// @Router /xiangwan/admin/instances/{instance_id}/roles/{role_binding_id}/revocations [post]
func (handler *AdminCatalogHandler) RevokeInstanceRole(c *gin.Context) {
	peopleCatalog, ok := handler.peopleRoles(c)
	if !ok || !validAdminWriteRequest(c) {
		return
	}
	principal, ok := handler.principal(c)
	if !ok {
		return
	}
	operationID, err := parseOperationKey(c)
	if err != nil {
		writeError(c, err)
		return
	}
	instanceID, err := parseCanonicalUUID(c.Param("instance_id"))
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid Instance id"))
		return
	}
	bindingID, err := parseCanonicalUUID(c.Param("role_binding_id"))
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid role binding id"))
		return
	}
	var payload RevokeAdminInstanceRoleRequest
	if err := decodeAdminCatalogJSON(c, &payload); err != nil {
		writeError(c, err)
		return
	}
	value, err := peopleCatalog.RevokeInstanceRole(c.Request.Context(), xiangwanadmin.RevokeInstanceRoleCommand{
		ActorID: principal.PrincipalID, IdentityLinkID: principal.IdentityLinkID,
		OperationID: operationID, RequestID: adminRequestID(c), InstanceID: instanceID,
		RoleBindingID: bindingID, ExpectedVersion: payload.ExpectedVersion, Reason: payload.Reason,
	})
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	response.OK(c, projectAdminInstanceRole(value))
}

func (handler *AdminCatalogHandler) peopleRoles(c *gin.Context) (xiangwanadmin.PeopleRoleCatalog, bool) {
	if handler != nil && handler.catalog != nil {
		if value, ok := handler.catalog.(xiangwanadmin.PeopleRoleCatalog); ok {
			return value, true
		}
	}
	writeError(c, errx.NewInternal("people role catalog is unavailable"))
	return nil, false
}

type AssignAdminInstanceRoleRequest struct {
	PeopleProfileID string `json:"people_profile_id"`
	RoleCode        string `json:"role_code"`
	GrantReason     string `json:"grant_reason"`
}

type RevokeAdminInstanceRoleRequest struct {
	ExpectedVersion int64  `json:"expected_version"`
	Reason          string `json:"reason"`
}

type AdminPersonResponse struct {
	ID               string  `json:"id"`
	DisplayName      string  `json:"display_name"`
	Headline         *string `json:"headline,omitempty"`
	Introduction     string  `json:"introduction"`
	ProfileStatus    string  `json:"profile_status"`
	ModerationStatus string  `json:"moderation_status"`
	Version          int64   `json:"version"`
	UpdatedAt        string  `json:"updated_at"`
	HasActiveBinding bool    `json:"has_active_binding"`
}

type AdminPeoplePageResponse struct {
	Items    []AdminPersonResponse `json:"items"`
	Page     int                   `json:"page"`
	PageSize int                   `json:"page_size"`
	Total    int64                 `json:"total"`
}

type AdminInstanceRoleResponse struct {
	ID              string  `json:"id"`
	InstanceID      string  `json:"instance_id"`
	PeopleProfileID string  `json:"people_profile_id"`
	DisplayName     string  `json:"display_name"`
	Headline        *string `json:"headline,omitempty"`
	RoleCode        string  `json:"role_code"`
	RoleStatus      string  `json:"role_status"`
	GrantReason     string  `json:"grant_reason"`
	Version         int64   `json:"version"`
	GrantedAt       string  `json:"granted_at"`
	RevokedAt       *string `json:"revoked_at,omitempty"`
}

type AdminInstanceRolePageResponse struct {
	Items []AdminInstanceRoleResponse `json:"items"`
}

func projectAdminPerson(value xiangwanadmin.Person) AdminPersonResponse {
	return AdminPersonResponse{
		ID: value.ID.String(), DisplayName: value.DisplayName, Headline: value.Headline,
		Introduction: value.Introduction, ProfileStatus: value.ProfileStatus,
		ModerationStatus: value.ModerationStatus, Version: value.Version,
		UpdatedAt:        value.UpdatedAt.UTC().Format(time.RFC3339Nano),
		HasActiveBinding: value.ActiveBindingID != nil,
	}
}

func projectAdminInstanceRole(value xiangwanadmin.InstanceRole) AdminInstanceRoleResponse {
	result := AdminInstanceRoleResponse{
		ID: value.ID.String(), InstanceID: value.InstanceID.String(),
		PeopleProfileID: value.PeopleProfileID.String(), DisplayName: value.DisplayName,
		Headline: value.Headline, RoleCode: value.RoleCode, RoleStatus: value.RoleStatus,
		GrantReason: value.GrantReason, Version: value.Version,
		GrantedAt: value.GrantedAt.UTC().Format(time.RFC3339Nano),
	}
	if value.RevokedAt != nil {
		formatted := value.RevokedAt.UTC().Format(time.RFC3339Nano)
		result.RevokedAt = &formatted
	}
	return result
}
