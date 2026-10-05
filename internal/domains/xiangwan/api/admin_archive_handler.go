package xiangwanapi

import (
	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/wzyhn/xiangwanai/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

// ArchiveInstance godoc
// @Summary Archive one completed Xiangwan Instance
// @Description ADMIN OP-KEY command. Changes only the lifecycle status and keeps publication, registration, order, refund, and completion facts immutable.
// @Tags xiangwan-admin
// @Accept json
// @Produce json
// @Param instance_id path string true "Instance ID"
// @Param Idempotency-Key header string true "Canonical lowercase UUIDv4 operation key"
// @Param request body ArchiveAdminInstanceRequest true "Expected Instance version"
// @Success 200 {object} AdminInstanceResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 404 {object} response.Body
// @Failure 409 {object} response.Body
// @Router /xiangwan/admin/instances/{instance_id}/archives [post]
func (handler *AdminCatalogHandler) ArchiveInstance(c *gin.Context) {
	principal, ok := handler.principal(c)
	if !ok || !handler.requireLifecycleArchive(c) || !validAdminWriteRequest(c) {
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
	var payload ArchiveAdminInstanceRequest
	if err := decodeAdminCatalogJSON(c, &payload); err != nil {
		writeError(c, err)
		return
	}
	lifecycle := handler.catalog.(xiangwanadmin.LifecycleArchiveCatalog)
	value, err := lifecycle.ArchiveInstance(c.Request.Context(), xiangwanadmin.ArchiveInstanceCommand{
		ActorID: principal.PrincipalID, IdentityLinkID: principal.IdentityLinkID,
		OperationID: operationID, RequestID: adminRequestID(c),
		InstanceID: instanceID, ExpectedInstanceVersion: payload.ExpectedInstanceVersion,
	})
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	response.OK(c, projectAdminInstance(value))
}

// ArchiveSession godoc
// @Summary Archive one ended Xiangwan Session
// @Description ADMIN OP-KEY command. Changes only the lifecycle status and keeps cancellation, registration, order, refund, and check-in facts immutable.
// @Tags xiangwan-admin
// @Accept json
// @Produce json
// @Param session_id path string true "Session ID"
// @Param Idempotency-Key header string true "Canonical lowercase UUIDv4 operation key"
// @Param request body ArchiveAdminSessionRequest true "Expected Session version"
// @Success 200 {object} AdminSessionResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 404 {object} response.Body
// @Failure 409 {object} response.Body
// @Router /xiangwan/admin/sessions/{session_id}/archives [post]
func (handler *AdminCatalogHandler) ArchiveSession(c *gin.Context) {
	principal, ok := handler.principal(c)
	if !ok || !handler.requireLifecycleArchive(c) || !validAdminWriteRequest(c) {
		return
	}
	operationID, err := parseOperationKey(c)
	if err != nil {
		writeError(c, err)
		return
	}
	sessionID, err := parseCanonicalUUID(c.Param("session_id"))
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid Session id"))
		return
	}
	var payload ArchiveAdminSessionRequest
	if err := decodeAdminCatalogJSON(c, &payload); err != nil {
		writeError(c, err)
		return
	}
	lifecycle := handler.catalog.(xiangwanadmin.LifecycleArchiveCatalog)
	value, err := lifecycle.ArchiveSession(c.Request.Context(), xiangwanadmin.ArchiveSessionCommand{
		ActorID: principal.PrincipalID, IdentityLinkID: principal.IdentityLinkID,
		OperationID: operationID, RequestID: adminRequestID(c),
		SessionID: sessionID, ExpectedSessionVersion: payload.ExpectedSessionVersion,
	})
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	response.OK(c, projectAdminSession(value))
}

func (handler *AdminCatalogHandler) requireLifecycleArchive(c *gin.Context) bool {
	if handler != nil {
		if _, ok := handler.catalog.(xiangwanadmin.LifecycleArchiveCatalog); ok {
			return true
		}
	}
	writeError(c, errx.NewInternal("lifecycle archive catalog is unavailable"))
	return false
}

type ArchiveAdminInstanceRequest struct {
	ExpectedInstanceVersion int64 `json:"expected_instance_version"`
}

type ArchiveAdminSessionRequest struct {
	ExpectedSessionVersion int64 `json:"expected_session_version"`
}
