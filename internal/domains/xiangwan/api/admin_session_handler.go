package xiangwanapi

import (
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/wzyhn/xiangwanai/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

// UpdateSession godoc
// @Summary Edit one draft Xiangwan Session
// @Description ADMIN OP-KEY command. Replaces the complete operational shape of a draft Session while fencing both the parent Instance and Session versions. Published and terminal Session facts remain immutable.
// @Tags xiangwan-admin
// @Accept json
// @Produce json
// @Param instance_id path string true "Instance ID"
// @Param session_id path string true "Session ID"
// @Param Idempotency-Key header string true "Canonical lowercase UUIDv4 operation key"
// @Param request body UpdateAdminSessionRequest true "Complete draft Session fields"
// @Success 200 {object} AdminSessionResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 404 {object} response.Body
// @Failure 409 {object} response.Body
// @Router /xiangwan/admin/instances/{instance_id}/sessions/{session_id} [patch]
func (handler *AdminCatalogHandler) UpdateSession(c *gin.Context) {
	sessions, ok := handler.sessionManagement(c)
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
	sessionID, err := parseCanonicalUUID(c.Param("session_id"))
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid Session id"))
		return
	}
	var payload UpdateAdminSessionRequest
	if err := decodeAdminCatalogJSON(c, &payload); err != nil {
		writeError(c, err)
		return
	}
	registrationStart, registrationEnd, sessionStart, sessionEnd, ok := parseSessionTimes(
		c,
		payload.RegistrationStartAt,
		payload.RegistrationEndAt,
		payload.SessionStartAt,
		payload.SessionEndAt,
	)
	if !ok {
		return
	}
	value, err := sessions.UpdateSession(c.Request.Context(), xiangwanadmin.UpdateSessionCommand{
		ActorID: principal.PrincipalID, IdentityLinkID: principal.IdentityLinkID,
		OperationID: operationID, RequestID: adminRequestID(c),
		InstanceID: instanceID, SessionID: sessionID,
		ExpectedInstanceVersion: payload.ExpectedInstanceVersion,
		ExpectedSessionVersion:  payload.ExpectedSessionVersion,
		Title:                   payload.Title,
		RegistrationStartAt:     registrationStart,
		RegistrationEndAt:       registrationEnd,
		SessionStartAt:          sessionStart,
		SessionEndAt:            sessionEnd,
		Capacity:                payload.Capacity,
		GroupMinimum:            payload.GroupMinimum,
		LowStockThreshold:       payload.LowStockThreshold,
		PriceCents:              payload.PriceCents,
		DeliveryMode:            payload.DeliveryMode,
		Area:                    payload.Area,
		VenueName:               payload.VenueName,
		Address:                 payload.Address,
		Longitude:               payload.Longitude,
		Latitude:                payload.Latitude,
		OnlineParticipationMode: payload.OnlineParticipationMode,
		OnlineCompliant:         payload.OnlineCompliant,
		SortOrder:               payload.SortOrder,
	})
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	response.OK(c, projectAdminSession(value))
}

func (handler *AdminCatalogHandler) sessionManagement(c *gin.Context) (xiangwanadmin.SessionManagementCatalog, bool) {
	if handler != nil && handler.catalog != nil {
		if value, ok := handler.catalog.(xiangwanadmin.SessionManagementCatalog); ok {
			return value, true
		}
	}
	writeError(c, errx.NewInternal("session management catalog is unavailable"))
	return nil, false
}

type UpdateAdminSessionRequest struct {
	ExpectedInstanceVersion int64                 `json:"expected_instance_version"`
	ExpectedSessionVersion  int64                 `json:"expected_session_version"`
	Title                   string                `json:"title"`
	RegistrationStartAt     string                `json:"registration_start_at"`
	RegistrationEndAt       string                `json:"registration_end_at"`
	SessionStartAt          string                `json:"session_start_at"`
	SessionEndAt            string                `json:"session_end_at"`
	Capacity                int                   `json:"capacity"`
	GroupMinimum            int                   `json:"group_minimum"`
	LowStockThreshold       int                   `json:"low_stock_threshold"`
	PriceCents              int64                 `json:"price_cents"`
	DeliveryMode            activity.DeliveryMode `json:"delivery_mode"`
	Area                    activity.AreaCode     `json:"area"`
	VenueName               string                `json:"venue_name"`
	Address                 string                `json:"address"`
	Longitude               *float64              `json:"longitude"`
	Latitude                *float64              `json:"latitude"`
	OnlineParticipationMode string                `json:"online_participation_mode"`
	OnlineCompliant         bool                  `json:"online_compliant"`
	SortOrder               int                   `json:"sort_order"`
}

func parseSessionTimes(c *gin.Context, values ...string) (time.Time, time.Time, time.Time, time.Time, bool) {
	if len(values) != 4 {
		writeError(c, errx.NewBadRequest("invalid Session time"))
		return time.Time{}, time.Time{}, time.Time{}, time.Time{}, false
	}
	parsed := make([]time.Time, len(values))
	for index, value := range values {
		item, err := time.Parse(time.RFC3339, value)
		if err != nil {
			writeError(c, errx.NewBadRequest("invalid Session time"))
			return time.Time{}, time.Time{}, time.Time{}, time.Time{}, false
		}
		parsed[index] = item
	}
	return parsed[0], parsed[1], parsed[2], parsed[3], true
}
