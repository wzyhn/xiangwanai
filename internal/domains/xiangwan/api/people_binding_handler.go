package xiangwanapi

import (
	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	peoplepostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/people/postgres"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/wzyhn/xiangwanai/internal/pkg/response"
	"context"
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type AdminPeopleBindingInvitationRequest struct {
	ExpectedVersion int64  `json:"expected_version"`
	Reason          string `json:"reason"`
}
type AdminPeopleBindingRevocationRequest struct {
	BindingID       string `json:"binding_id"`
	ExpectedVersion int64  `json:"expected_version"`
	Reason          string `json:"reason"`
}

// CreatePeopleBindingInvitation godoc
// @Summary Create a 24-hour one-use invitation for an approved People profile
// @Description Super-admin OP-KEY command with live generation, identity and Grant. Returns a purpose-bound invitation code only to the operator; PostgreSQL and audit never retain plaintext codes. Reissuing revokes the old pending invitation.
// @Tags xiangwan-admin
// @Accept json
// @Produce json
// @Param people_id path string true "People profile ID"
// @Param Idempotency-Key header string true "Canonical UUIDv4 operation key"
// @Param request body AdminPeopleBindingInvitationRequest true "Version and required reason"
// @Success 200 {object} xiangwanadmin.PeopleBindingInvitation
// @Failure 400 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 409 {object} response.Body
// @Router /xiangwan/admin/people/{people_id}/binding-invitations [post]
func (h *AdminCatalogHandler) CreatePeopleBindingInvitation(c *gin.Context) {
	p, ok := h.principal(c)
	if !ok || !validAdminWriteRequest(c) {
		return
	}
	if h.peopleBindingAdministration == nil {
		writeError(c, errx.NewInternal("People binding unavailable"))
		return
	}
	id, err := parseCanonicalUUID(c.Param("people_id"))
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid People profile"))
		return
	}
	operation, err := parseOperationKey(c)
	if err != nil {
		writeError(c, err)
		return
	}
	var payload AdminPeopleBindingInvitationRequest
	if err := decodeAdminCatalogJSON(c, &payload); err != nil {
		writeError(c, err)
		return
	}
	value, err := h.peopleBindingAdministration.CreatePeopleBindingInvitation(c.Request.Context(), xiangwanadmin.CreatePeopleBindingInvitationCommand{ActorID: p.PrincipalID, IdentityLinkID: p.IdentityLinkID, OperationID: operation, PeopleProfileID: id, ExpectedVersion: payload.ExpectedVersion, Reason: payload.Reason, RequestID: adminRequestID(c)})
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	response.OK(c, value)
}

// GetPeopleBinding godoc
// @Summary Read the exact active binding version for super-admin correction
// @Tags xiangwan-admin
// @Produce json
// @Param people_id path string true "People profile ID"
// @Success 200 {object} xiangwanadmin.PeopleBindingDetail
// @Failure 403 {object} response.Body
// @Failure 404 {object} response.Body
// @Router /xiangwan/admin/people/{people_id}/binding [get]
func (h *AdminCatalogHandler) GetPeopleBinding(c *gin.Context) {
	p, ok := h.principal(c)
	if !ok || !validAdminQuery(c) {
		return
	}
	if h.peopleBindingAdministration == nil {
		writeError(c, errx.NewInternal("People binding unavailable"))
		return
	}
	id, err := parseCanonicalUUID(c.Param("people_id"))
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid People profile"))
		return
	}
	value, err := h.peopleBindingAdministration.GetPeopleBinding(c.Request.Context(), p, id, adminRequestID(c))
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	response.OK(c, value)
}

// RevokePeopleBinding godoc
// @Summary Revoke the exact trusted People binding with retained history
// @Description Super-admin OP-KEY command; no consumer identity input, generation and live authorization checked before receipt replay. Original binding and historical role/ledger facts remain unchanged.
// @Tags xiangwan-admin
// @Accept json
// @Produce json
// @Param people_id path string true "People profile ID"
// @Param Idempotency-Key header string true "Canonical UUIDv4 operation key"
// @Param request body AdminPeopleBindingRevocationRequest true "Exact binding, version and reason"
// @Success 200 {object} xiangwanadmin.PeopleBindingDetail
// @Failure 400 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 409 {object} response.Body
// @Router /xiangwan/admin/people/{people_id}/binding-revocations [post]
func (h *AdminCatalogHandler) RevokePeopleBinding(c *gin.Context) {
	h.revokePeopleBinding(c, false)
}

// RevokePeopleBindingInvitation godoc
// @Summary Revoke an unused People invitation with immutable audit
// @Tags xiangwan-admin
// @Accept json
// @Produce json
// @Param people_id path string true "People profile ID"
// @Param Idempotency-Key header string true "Canonical UUIDv4 operation key"
// @Param request body AdminPeopleBindingRevocationRequest true "binding_id is the exact unused invitation ID; version and reason required"
// @Success 200 {object} xiangwanadmin.PeopleBindingInvitation
// @Failure 400 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 409 {object} response.Body
// @Router /xiangwan/admin/people/{people_id}/binding-invitation-revocations [post]
func (h *AdminCatalogHandler) RevokePeopleBindingInvitation(c *gin.Context) {
	h.revokePeopleBinding(c, true)
}
func (h *AdminCatalogHandler) revokePeopleBinding(c *gin.Context, invitation bool) {
	p, ok := h.principal(c)
	if !ok || !validAdminWriteRequest(c) {
		return
	}
	if h.peopleBindingAdministration == nil {
		writeError(c, errx.NewInternal("People binding unavailable"))
		return
	}
	id, err := parseCanonicalUUID(c.Param("people_id"))
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid People profile"))
		return
	}
	operation, err := parseOperationKey(c)
	if err != nil {
		writeError(c, err)
		return
	}
	var payload AdminPeopleBindingRevocationRequest
	if err := decodeAdminCatalogJSON(c, &payload); err != nil {
		writeError(c, err)
		return
	}
	binding, err := parseCanonicalUUID(payload.BindingID)
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid binding"))
		return
	}
	command := xiangwanadmin.RevokePeopleBindingCommand{ActorID: p.PrincipalID, IdentityLinkID: p.IdentityLinkID, OperationID: operation, PeopleProfileID: id, BindingID: binding, ExpectedVersion: payload.ExpectedVersion, Reason: payload.Reason, RequestID: adminRequestID(c)}
	var value any
	if invitation {
		value, err = h.peopleBindingAdministration.RevokePeopleBindingInvitation(c.Request.Context(), command)
	} else {
		value, err = h.peopleBindingAdministration.RevokePeopleBinding(c.Request.Context(), command)
	}
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	response.OK(c, value)
}

type PeopleBindingPreviewRequest struct {
	Code string `json:"code"`
}
type PeopleBindingAcceptRequest struct {
	Code            string `json:"code"`
	ExpectedVersion int64  `json:"expected_version"`
	Consent         bool   `json:"consent"`
}
type peopleBindingAcceptor interface {
	Preview(context.Context, uuid.UUID, string) (peoplepostgres.BindingInvitationPreview, error)
	Accept(context.Context, uuid.UUID, uuid.UUID, string, int64, bool) (peoplepostgres.BindingInvitationConfirmation, error)
}
type PeopleBindingHandler struct {
	service   peopleBindingAcceptor
	principal PrincipalResolver
	throttle  gin.HandlerFunc
}

func NewPeopleBindingHandler(service peopleBindingAcceptor, principal PrincipalResolver, throttle gin.HandlerFunc) *PeopleBindingHandler {
	return &PeopleBindingHandler{service: service, principal: principal, throttle: throttle}
}
func (h *PeopleBindingHandler) RegisterRoutes(group *gin.RouterGroup) {
	if h == nil || h.service == nil || h.principal == nil || h.throttle == nil {
		return
	}
	group.POST("/me/people-binding-preview", h.throttle, h.Preview)
	group.POST("/me/people-bindings", h.throttle, h.Accept)
}

// Preview godoc
// @Summary Privately preview a People binding invitation before explicit consent
// @Description Authenticated, throttled, no-store POST. Invitation never enters a URL. Requires live inviter authority, exact approved profile version and unexpired unused code.
// @Tags xiangwan
// @Accept json
// @Produce json
// @Param request body PeopleBindingPreviewRequest true "Private invitation code"
// @Security BearerAuth
// @Success 200 {object} peoplepostgres.BindingInvitationPreview
// @Failure 400 {object} response.Body
// @Failure 404 {object} response.Body
// @Failure 409 {object} response.Body
// @Router /xiangwan/me/people-binding-preview [post]
func (h *PeopleBindingHandler) Preview(c *gin.Context) {
	owner, err := h.principal(c)
	if err != nil {
		writeError(c, err)
		return
	}
	if c.ContentType() != "application/json" || len(c.Request.URL.Query()) != 0 || len(c.Request.Header.Values("Idempotency-Key")) != 0 {
		writeError(c, errx.NewBadRequest("invalid binding preview"))
		return
	}
	var body PeopleBindingPreviewRequest
	if err := decodeAdminCatalogJSON(c, &body); err != nil {
		writeError(c, err)
		return
	}
	value, err := h.service.Preview(c.Request.Context(), owner, body.Code)
	if err != nil {
		writePeopleBindingError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	response.OK(c, value)
}

// Accept godoc
// @Summary Explicitly bind the authenticated WeChat consumer to an approved People profile
// @Description OP-KEY command. Identity derives from the server token; exact invitation/profile version and explicit consent commit one trusted binding with immutable evidence. Wrong owner, expired/replaced invitation, stale generation or revoked inviter authority fails closed. No phone-based identity lookup.
// @Tags xiangwan
// @Accept json
// @Produce json
// @Param Idempotency-Key header string true "Canonical UUIDv4 operation key"
// @Param request body PeopleBindingAcceptRequest true "Invitation and explicit consent"
// @Security BearerAuth
// @Success 200 {object} peoplepostgres.BindingInvitationConfirmation
// @Failure 400 {object} response.Body
// @Failure 404 {object} response.Body
// @Failure 409 {object} response.Body
// @Router /xiangwan/me/people-bindings [post]
func (h *PeopleBindingHandler) Accept(c *gin.Context) {
	owner, err := h.principal(c)
	if err != nil {
		writeError(c, err)
		return
	}
	if c.ContentType() != "application/json" || len(c.Request.URL.Query()) != 0 {
		writeError(c, errx.NewBadRequest("invalid binding confirmation"))
		return
	}
	operation, err := parseOperationKey(c)
	if err != nil {
		writeError(c, err)
		return
	}
	var body PeopleBindingAcceptRequest
	if err := decodeAdminCatalogJSON(c, &body); err != nil {
		writeError(c, err)
		return
	}
	value, err := h.service.Accept(c.Request.Context(), owner, operation, body.Code, body.ExpectedVersion, body.Consent)
	if err != nil {
		writePeopleBindingError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	response.OK(c, value)
}
func writePeopleBindingError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, peoplepostgres.ErrBindingInvitationInvalid):
		writeError(c, errx.NewBadRequest("invalid People binding request"))
	case errors.Is(err, peoplepostgres.ErrBindingInvitationUnavailable):
		writeError(c, errx.NewNotFound("People binding invitation unavailable"))
	case errors.Is(err, peoplepostgres.ErrBindingInvitationConflict):
		writeError(c, errx.NewConflict("People binding facts changed"))
	case errors.Is(err, peoplepostgres.ErrBindingInvitationGenerationInactive):
		writeError(c, errx.NewConflict("People binding generation changed"))
	default:
		writeError(c, errx.NewInternal("People binding temporarily unavailable"))
	}
}
