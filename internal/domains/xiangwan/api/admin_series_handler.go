package xiangwanapi

import (
	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/wzyhn/xiangwanai/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

// UpdateSeries godoc
// @Summary Update one Xiangwan activity Series
// @Description ADMIN OP-KEY command. Patches long-lived Series title and/or homepage exposure with an expected-version fence. Existing Instance titles remain immutable snapshots.
// @Tags xiangwan-admin
// @Accept json
// @Produce json
// @Param series_id path string true "Series ID"
// @Param Idempotency-Key header string true "Canonical lowercase UUIDv4 operation key"
// @Param request body UpdateAdminSeriesRequest true "Series fields to patch"
// @Success 200 {object} AdminSeriesResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 404 {object} response.Body
// @Failure 409 {object} response.Body
// @Router /xiangwan/admin/series/{series_id} [patch]
func (handler *AdminCatalogHandler) UpdateSeries(c *gin.Context) {
	seriesCatalog, ok := handler.seriesManagement(c)
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
	seriesID, err := parseCanonicalUUID(c.Param("series_id"))
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid Series id"))
		return
	}
	var payload UpdateAdminSeriesRequest
	if err := decodeAdminCatalogJSON(c, &payload); err != nil {
		writeError(c, err)
		return
	}
	if payload.Title == nil && payload.HomeVisible == nil {
		writeError(c, errx.NewBadRequest("no Series field to update"))
		return
	}
	value, err := seriesCatalog.UpdateSeries(
		c.Request.Context(), xiangwanadmin.UpdateSeriesCommand{
			ActorID: principal.PrincipalID, IdentityLinkID: principal.IdentityLinkID,
			OperationID: operationID, RequestID: adminRequestID(c),
			SeriesID: seriesID, ExpectedVersion: payload.ExpectedVersion,
			Title: payload.Title, HomeVisible: payload.HomeVisible,
		},
	)
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	response.OK(c, projectAdminSeries(value))
}

func (handler *AdminCatalogHandler) seriesManagement(c *gin.Context) (xiangwanadmin.SeriesManagementCatalog, bool) {
	if handler != nil && handler.catalog != nil {
		if value, ok := handler.catalog.(xiangwanadmin.SeriesManagementCatalog); ok {
			return value, true
		}
	}
	writeError(c, errx.NewInternal("series management catalog is unavailable"))
	return nil, false
}

type UpdateAdminSeriesRequest struct {
	ExpectedVersion int64   `json:"expected_version"`
	Title           *string `json:"title"`
	HomeVisible     *bool   `json:"home_visible"`
}
