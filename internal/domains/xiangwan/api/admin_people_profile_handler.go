package xiangwanapi

import (
	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/wzyhn/xiangwanai/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

// CreatePeopleProfile godoc
// @Summary Create a Xiangwan PeopleProfile draft
// @Description Creates public PeopleProfile content in draft/pending state. It does not create or infer a Principal binding.
// @Tags xiangwan-admin
// @Accept json
// @Produce json
// @Param Idempotency-Key header string true "Canonical lowercase UUIDv4 operation key"
// @Param request body CreateAdminPeopleProfileRequest true "PeopleProfile public content"
// @Success 201 {object} AdminPersonResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Router /xiangwan/admin/people [post]
func (handler *AdminCatalogHandler) CreatePeopleProfile(c *gin.Context) {
	profiles, ok := handler.peopleProfiles(c)
	if !ok || !validAdminWriteRequest(c) {
		return
	}
	operationID, err := parseOperationKey(c)
	if err != nil {
		writeError(c, err)
		return
	}
	principal, ok := handler.principal(c)
	if !ok {
		return
	}
	var payload CreateAdminPeopleProfileRequest
	if err := decodeAdminCatalogJSON(c, &payload); err != nil {
		writeError(c, err)
		return
	}
	value, err := profiles.CreatePeopleProfile(c.Request.Context(), xiangwanadmin.CreatePeopleProfileCommand{
		ActorID: principal.PrincipalID, IdentityLinkID: principal.IdentityLinkID,
		OperationID: operationID, RequestID: adminRequestID(c),
		DisplayName: payload.DisplayName, Headline: payload.Headline,
		Introduction: payload.Introduction,
	})
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	response.Created(c, projectAdminPerson(value))
}

// UpdatePeopleProfile godoc
// @Summary Revise a Xiangwan PeopleProfile
// @Description Revising public PeopleProfile content resets it to draft/pending moderation and preserves the trusted binding separately.
// @Tags xiangwan-admin
// @Accept json
// @Produce json
// @Param Idempotency-Key header string true "Canonical lowercase UUIDv4 operation key"
// @Param people_id path string true "PeopleProfile ID"
// @Param request body UpdateAdminPeopleProfileRequest true "PeopleProfile public content and expected version"
// @Success 200 {object} AdminPersonResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 404 {object} response.Body
// @Failure 409 {object} response.Body
// @Router /xiangwan/admin/people/{people_id} [patch]
func (handler *AdminCatalogHandler) UpdatePeopleProfile(c *gin.Context) {
	profiles, ok := handler.peopleProfiles(c)
	if !ok || !validAdminWriteRequest(c) {
		return
	}
	operationID, err := parseOperationKey(c)
	if err != nil {
		writeError(c, err)
		return
	}
	principal, ok := handler.principal(c)
	if !ok {
		return
	}
	profileID, err := parseCanonicalUUID(c.Param("people_id"))
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid PeopleProfile id"))
		return
	}
	var payload UpdateAdminPeopleProfileRequest
	if err := decodeAdminCatalogJSON(c, &payload); err != nil {
		writeError(c, err)
		return
	}
	value, err := profiles.UpdatePeopleProfile(c.Request.Context(), xiangwanadmin.UpdatePeopleProfileCommand{
		ActorID: principal.PrincipalID, IdentityLinkID: principal.IdentityLinkID,
		OperationID: operationID, RequestID: adminRequestID(c),
		PeopleProfileID: profileID, ExpectedVersion: payload.ExpectedVersion,
		DisplayName: payload.DisplayName, Headline: payload.Headline,
		Introduction: payload.Introduction,
	})
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	response.OK(c, projectAdminPerson(value))
}

// ReviewPeopleProfile godoc
// @Summary Approve or reject a Xiangwan PeopleProfile
// @Description Moves only a draft/pending PeopleProfile to published/approved or rejected/draft; the decision is version fenced and audited.
// @Tags xiangwan-admin
// @Accept json
// @Produce json
// @Param Idempotency-Key header string true "Canonical lowercase UUIDv4 operation key"
// @Param people_id path string true "PeopleProfile ID"
// @Param request body ReviewAdminPeopleProfileRequest true "Moderation decision and expected version"
// @Success 200 {object} AdminPersonResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 404 {object} response.Body
// @Failure 409 {object} response.Body
// @Router /xiangwan/admin/people/{people_id}/reviews [post]
func (handler *AdminCatalogHandler) ReviewPeopleProfile(c *gin.Context) {
	profiles, ok := handler.peopleProfiles(c)
	if !ok || !validAdminWriteRequest(c) {
		return
	}
	operationID, err := parseOperationKey(c)
	if err != nil {
		writeError(c, err)
		return
	}
	principal, ok := handler.principal(c)
	if !ok {
		return
	}
	profileID, err := parseCanonicalUUID(c.Param("people_id"))
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid PeopleProfile id"))
		return
	}
	var payload ReviewAdminPeopleProfileRequest
	if err := decodeAdminCatalogJSON(c, &payload); err != nil {
		writeError(c, err)
		return
	}
	value, err := profiles.ReviewPeopleProfile(c.Request.Context(), xiangwanadmin.ReviewPeopleProfileCommand{
		ActorID: principal.PrincipalID, IdentityLinkID: principal.IdentityLinkID,
		OperationID: operationID, RequestID: adminRequestID(c),
		PeopleProfileID: profileID, ExpectedVersion: payload.ExpectedVersion,
		Decision: payload.Decision,
	})
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	response.OK(c, projectAdminPerson(value))
}

func (handler *AdminCatalogHandler) peopleProfiles(c *gin.Context) (xiangwanadmin.PeopleProfileCatalog, bool) {
	if handler != nil && handler.catalog != nil {
		if value, ok := handler.catalog.(xiangwanadmin.PeopleProfileCatalog); ok {
			return value, true
		}
	}
	writeError(c, errx.NewInternal("people profile catalog is unavailable"))
	return nil, false
}

type CreateAdminPeopleProfileRequest struct {
	DisplayName  string `json:"display_name"`
	Headline     string `json:"headline"`
	Introduction string `json:"introduction"`
}

type UpdateAdminPeopleProfileRequest struct {
	ExpectedVersion int64  `json:"expected_version"`
	DisplayName     string `json:"display_name"`
	Headline        string `json:"headline"`
	Introduction    string `json:"introduction"`
}

type ReviewAdminPeopleProfileRequest struct {
	ExpectedVersion int64  `json:"expected_version"`
	Decision        string `json:"decision" enums:"approved,rejected"`
}
