package xiangwanapi

import (
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/resource"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/wzyhn/xiangwanai/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

// ListReviewResources godoc
// @Summary Read current published review fields including hidden resource cards for editing
// @Tags xiangwan-admin
// @Produce json
// @Param instance_id path string true "Instance UUID"
// @Success 200 {object} AdminReviewResourceEditPageResponse
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Router /xiangwan/admin/instances/{instance_id}/review-resources [get]
func (handler *AdminCatalogHandler) ListReviewResources(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	principal, ok := handler.principal(c)
	if !ok || !validAdminQuery(c) {
		return
	}
	editor, ok := handler.reviewWriter.(resource.ReviewResourceEditor)
	if !ok {
		writeError(c, errx.NewInternal("review editor is unavailable"))
		return
	}
	instanceID, err := parseCanonicalUUID(c.Param("instance_id"))
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid Instance ID"))
		return
	}
	views, err := editor.ListReviewResources(c.Request.Context(), resource.ReviewResourceListCommand{
		ActorID: principal.PrincipalID, IdentityLinkID: principal.IdentityLinkID,
		InstanceID: instanceID, RequestID: adminRequestID(c),
	})
	if err != nil {
		writeReviewResourceError(c, err)
		return
	}
	items := make([]AdminReviewResourceEditResponse, 0, len(views))
	for _, view := range views {
		item := AdminReviewResourceEditResponse{RelationID: view.RelationID.String(), InstanceID: instanceID.String(),
			PhotoCurationVersion: view.PhotoCurationVersion, Editable: view.Editable,
			CreateAdminReviewResourceRequest: CreateAdminReviewResourceRequest{
				ExpectedTargetVersion: view.ExpectedTargetVersion, Title: view.Title,
				Description: view.Description, VideoURL: view.VideoURL, VideoChannel: view.VideoChannel, Photos: view.Photos,
				SortOrder: view.SortOrder, Files: []AdminReviewFileRequest{},
			},
		}
		if view.SessionID != nil {
			item.SessionID = view.SessionID.String()
		}
		for _, link := range view.Links {
			entry := &AdminReviewLinkRequest{Enabled: !link.Hidden, Title: link.Title, Subtitle: link.Subtitle, URL: link.URL}
			if link.Kind == resource.ReviewResourceLinkRecording {
				item.Recording = entry
			} else if link.Kind == resource.ReviewResourceLinkMaterials {
				item.Materials = entry
			}
		}
		items = append(items, item)
	}
	response.OK(c, AdminReviewResourceEditPageResponse{Items: items})
}

type AdminReviewResourceEditResponse struct {
	CreateAdminReviewResourceRequest
	RelationID           string `json:"relation_id"`
	InstanceID           string `json:"instance_id"`
	PhotoCurationVersion int64  `json:"photo_curation_version"`
	Editable             bool   `json:"editable"`
}

type AdminReviewResourceEditPageResponse struct {
	Items []AdminReviewResourceEditResponse `json:"items"`
}
