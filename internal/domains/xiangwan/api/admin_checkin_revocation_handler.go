package xiangwanapi

import (
	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/checkin"
	checkinpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/checkin/postgres"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/wzyhn/xiangwanai/internal/pkg/response"
	"errors"
	"github.com/gin-gonic/gin"
	"time"
)

type AdminCheckinDetailResponse struct {
	CheckinID        string         `json:"checkin_id"`
	RegistrationID   string         `json:"registration_id"`
	Status           checkin.Status `json:"status"`
	Version          int64          `json:"version"`
	CheckedInAt      string         `json:"checked_in_at"`
	RevokedAt        *string        `json:"revoked_at,omitempty"`
	RevocationReason *string        `json:"revocation_reason,omitempty"`
}
type AdminCheckinRevocationRequest struct {
	CheckinID       string `json:"checkin_id"`
	ExpectedVersion int64  `json:"expected_version"`
	Reason          string `json:"reason"`
}
type AdminCheckinRevocationResponse struct {
	Checkin   AdminCheckinDetailResponse `json:"checkin"`
	EventID   string                     `json:"event_id"`
	Duplicate bool                       `json:"duplicate"`
}

func projectAdminCheckin(value checkin.Checkin) AdminCheckinDetailResponse {
	return AdminCheckinDetailResponse{CheckinID: value.ID.String(), RegistrationID: value.RegistrationID.String(), Status: value.CheckinStatus, Version: value.Version, CheckedInAt: value.CheckedInAt.UTC().Format(time.RFC3339Nano), RevokedAt: formatOptionalAdminTime(value.RevokedAt), RevocationReason: value.RevocationReason}
}

// GetRegistrationCheckin godoc
// @Summary Read the current attendance fact for super administrator correction
// @Description Super-admin only, audited and no-store; omits participant and operator identities and credential secrets.
// @Tags xiangwan-admin
// @Produce json
// @Param registration_id path string true "Registration ID"
// @Success 200 {object} AdminCheckinDetailResponse
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 404 {object} response.Body
// @Router /xiangwan/admin/registrations/{registration_id}/checkin [get]
func (handler *AdminCatalogHandler) GetRegistrationCheckin(c *gin.Context) {
	principal, ok := handler.principal(c)
	if !ok || !validAdminQuery(c) {
		return
	}
	if handler.checkinAdministration == nil {
		writeError(c, errx.NewInternal("checkin administration is unavailable"))
		return
	}
	id, err := parseCanonicalUUID(c.Param("registration_id"))
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid Registration id"))
		return
	}
	value, err := handler.checkinAdministration.GetRegistrationCheckin(c.Request.Context(), principal, id, adminRequestID(c))
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	response.OK(c, projectAdminCheckin(value))
}

// RevokeRegistrationCheckin godoc
// @Summary Revoke an attendance fact with reason and expected version
// @Description Super-admin OP-KEY command. Retains original attendance, appends immutable revocation, decrements the attendance counter and creates one contribution/coupon correction task atomically. Supports cancelled and archived activity convergence. Replays require live identity, grant and generation.
// @Tags xiangwan-admin
// @Accept json
// @Produce json
// @Param registration_id path string true "Registration ID"
// @Param Idempotency-Key header string true "Canonical UUIDv4 operation key"
// @Param request body AdminCheckinRevocationRequest true "Exact Checkin, expected version and required reason"
// @Success 200 {object} AdminCheckinRevocationResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 404 {object} response.Body
// @Failure 409 {object} response.Body
// @Router /xiangwan/admin/registrations/{registration_id}/checkin-revocations [post]
func (handler *AdminCatalogHandler) RevokeRegistrationCheckin(c *gin.Context) {
	principal, ok := handler.principal(c)
	if !ok || !validAdminWriteRequest(c) {
		return
	}
	if handler.checkinAdministration == nil {
		writeError(c, errx.NewInternal("checkin administration is unavailable"))
		return
	}
	operation, err := parseOperationKey(c)
	if err != nil {
		writeError(c, err)
		return
	}
	registrationID, err := parseCanonicalUUID(c.Param("registration_id"))
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid Registration id"))
		return
	}
	var payload AdminCheckinRevocationRequest
	if err := decodeAdminCatalogJSON(c, &payload); err != nil {
		writeError(c, err)
		return
	}
	checkinID, err := parseCanonicalUUID(payload.CheckinID)
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid Checkin id"))
		return
	}
	value, err := handler.checkinAdministration.RevokeRegistrationCheckin(c.Request.Context(), xiangwanadmin.RevokeCheckinCommand{ActorID: principal.PrincipalID, IdentityLinkID: principal.IdentityLinkID, OperationID: operation, RequestID: adminRequestID(c), RegistrationID: registrationID, CheckinID: checkinID, ExpectedVersion: payload.ExpectedVersion, Reason: payload.Reason})
	if err != nil {
		writeAdminCheckinRevocationError(c, err)
		return
	}
	response.OK(c, AdminCheckinRevocationResponse{Checkin: projectAdminCheckin(value.Checkin), EventID: value.EventID.String(), Duplicate: value.Duplicate})
}

func writeAdminCheckinRevocationError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, checkinpostgres.ErrInvalidRevokeCheckinCommand):
		writeError(c, errx.NewBadRequest("撤销签到需要有效版本和原因"))
	case errors.Is(err, checkinpostgres.ErrCheckinRevocationForbidden):
		writeError(c, errx.NewForbidden("只有系统管理员可以撤销签到"))
	case errors.Is(err, checkinpostgres.ErrCheckinRevocationVersionConflict), errors.Is(err, checkinpostgres.ErrCheckinAlreadyRevoked), errors.Is(err, checkinpostgres.ErrCheckinRevocationIdempotencyConflict), errors.Is(err, checkinpostgres.ErrCheckinRevocationGenerationInactive):
		writeError(c, errx.NewConflict("签到状态已变化，请刷新后核对"))
	case errors.Is(err, checkinpostgres.ErrCheckinNotFound), errors.Is(err, checkinpostgres.ErrCheckinRevocationTargetNotFound):
		writeError(c, errx.NewNotFound("签到记录不存在"))
	default:
		writeAdminCatalogError(c, err)
	}
}
