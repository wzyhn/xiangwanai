package xiangwanapi

import (
	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/people"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/wzyhn/xiangwanai/internal/pkg/response"
	"context"
	"github.com/gin-gonic/gin"
	"strconv"
	"strings"
)

type AdminHostRulesRequest struct {
	ExpectedVersion  int64  `json:"expected_version"`
	ApplicationCycle string `json:"application_cycle"`
	PolicyVersion    string `json:"policy_version"`
	Requirements     string `json:"requirements"`
	Benefits         string `json:"benefits"`
	Enabled          bool   `json:"enabled"`
	Reason           string `json:"reason"`
}
type AdminHostApplicationReviewRequest struct {
	ExpectedVersion int64                        `json:"expected_version"`
	Decision        people.HostApplicationStatus `json:"decision"`
	Comment         string                       `json:"comment"`
}

func (h *AdminCatalogHandler) SetHostRulesModerator(check func(context.Context, string) error) {
	h.hostRulesModerator = check
}

// GetHostRules godoc
// @Summary Read the latest operator host rules
// @Tags xiangwan-admin
// @Produce json
// @Success 200 {object} xiangwanadmin.HostRuleVersion
// @Router /xiangwan/admin/host-rules [get]
func (h *AdminCatalogHandler) GetHostRules(c *gin.Context) {
	p, ok := h.principal(c)
	if !ok || !validAdminQuery(c) {
		return
	}
	if h.hostApplications == nil {
		writeError(c, errx.NewInternal("Host rules unavailable"))
		return
	}
	v, err := h.hostApplications.GetHostRules(c.Request.Context(), p)
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	response.OK(c, v)
}

// PublishHostRules godoc
// @Summary Publish immutable customer-approved host rules
// @Description Super-admin OP-KEY. Existing wording keeps its policy identity; changed wording requires a new policy version. No default customer rules are invented.
// @Tags xiangwan-admin
// @Accept json
// @Produce json
// @Param Idempotency-Key header string true "UUIDv4 operation key"
// @Param request body AdminHostRulesRequest true "Rules and required approval reason"
// @Success 200 {object} xiangwanadmin.HostRuleVersion
// @Router /xiangwan/admin/host-rules [post]
func (h *AdminCatalogHandler) PublishHostRules(c *gin.Context) {
	p, ok := h.principal(c)
	if !ok || !validAdminWriteRequest(c) {
		return
	}
	if h.hostApplications == nil || h.hostRulesModerator == nil {
		writeError(c, errx.NewInternal("Host rules unavailable"))
		return
	}
	op, err := parseOperationKey(c)
	if err != nil {
		writeError(c, err)
		return
	}
	var payload AdminHostRulesRequest
	if err = decodeAdminCatalogJSON(c, &payload); err != nil {
		writeError(c, err)
		return
	}
	if strings.TrimSpace(payload.Requirements) == "" || strings.TrimSpace(payload.Benefits) == "" || len([]rune(payload.Requirements)) > 4000 || len([]rune(payload.Benefits)) > 4000 {
		writeError(c, errx.NewBadRequest("invalid host rules"))
		return
	}
	if err = h.hostRulesModerator(c.Request.Context(), payload.Requirements+"\n"+payload.Benefits); err != nil {
		writeError(c, err)
		return
	}
	v, err := h.hostApplications.PublishHostRules(c.Request.Context(), xiangwanadmin.PublishHostRulesCommand{ActorID: p.PrincipalID, IdentityLinkID: p.IdentityLinkID, OperationID: op, RequestID: adminRequestID(c), ExpectedVersion: payload.ExpectedVersion, Reason: payload.Reason, Rules: xiangwanadmin.HostRuleVersion{ApplicationCycle: payload.ApplicationCycle, PolicyVersion: payload.PolicyVersion, Requirements: payload.Requirements, Benefits: payload.Benefits, Enabled: payload.Enabled}})
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	response.OK(c, v)
}

// ListHostApplications godoc
// @Summary Page safe host-application summaries without applicant contact
// @Tags xiangwan-admin
// @Produce json
// @Param status query string false "Status"
// @Param page query int false "Page"
// @Param page_size query int false "Page size"
// @Success 200 {object} xiangwanadmin.HostApplicationPage
// @Router /xiangwan/admin/host-applications [get]
func (h *AdminCatalogHandler) ListHostApplications(c *gin.Context) {
	p, ok := h.principal(c)
	if !ok || !validAdminQuery(c, "status", "page", "page_size") {
		return
	}
	if h.hostApplications == nil {
		writeError(c, errx.NewInternal("Host applications unavailable"))
		return
	}
	page, size := 1, 20
	var err error
	if c.Query("page") != "" {
		page, err = strconv.Atoi(c.Query("page"))
		if err != nil {
			writeError(c, errx.NewBadRequest("invalid page"))
			return
		}
	}
	if c.Query("page_size") != "" {
		size, err = strconv.Atoi(c.Query("page_size"))
		if err != nil {
			writeError(c, errx.NewBadRequest("invalid size"))
			return
		}
	}
	v, err := h.hostApplications.ListHostApplications(c.Request.Context(), p, c.Query("status"), page, size)
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	response.OK(c, v)
}

// GetHostApplication godoc
// @Summary Read one private application with an audited review purpose
// @Tags xiangwan-admin
// @Produce json
// @Param application_id path string true "Application UUID"
// @Param purpose query string true "host_application_review"
// @Success 200 {object} xiangwanadmin.HostApplicationDetail
// @Router /xiangwan/admin/host-applications/{application_id} [get]
func (h *AdminCatalogHandler) GetHostApplication(c *gin.Context) {
	p, ok := h.principal(c)
	if !ok || !validAdminQuery(c, "purpose") {
		return
	}
	if h.hostApplications == nil {
		writeError(c, errx.NewInternal("Host application unavailable"))
		return
	}
	id, err := parseCanonicalUUID(c.Param("application_id"))
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid application"))
		return
	}
	v, err := h.hostApplications.GetHostApplication(c.Request.Context(), p, id, c.Query("purpose"), adminRequestID(c))
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	response.OK(c, v)
}

// ReviewHostApplication godoc
// @Summary Approve or reject one pending host application
// @Description Activity operator OP-KEY with live authority and exact version. Original private narratives remain unchanged; receipt and audit exclude them. Approval does not grant an Instance role.
// @Tags xiangwan-admin
// @Accept json
// @Produce json
// @Param application_id path string true "Application UUID"
// @Param Idempotency-Key header string true "UUIDv4 operation key"
// @Param request body AdminHostApplicationReviewRequest true "Decision and required comment"
// @Success 200 {object} xiangwanadmin.HostApplicationItem
// @Router /xiangwan/admin/host-applications/{application_id}/reviews [post]
func (h *AdminCatalogHandler) ReviewHostApplication(c *gin.Context) {
	p, ok := h.principal(c)
	if !ok || !validAdminWriteRequest(c) {
		return
	}
	if h.hostApplications == nil {
		writeError(c, errx.NewInternal("Host application unavailable"))
		return
	}
	id, err := parseCanonicalUUID(c.Param("application_id"))
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid application"))
		return
	}
	op, err := parseOperationKey(c)
	if err != nil {
		writeError(c, err)
		return
	}
	var payload AdminHostApplicationReviewRequest
	if err = decodeAdminCatalogJSON(c, &payload); err != nil {
		writeError(c, err)
		return
	}
	v, err := h.hostApplications.ReviewHostApplication(c.Request.Context(), xiangwanadmin.ReviewHostApplicationCommand{ActorID: p.PrincipalID, IdentityLinkID: p.IdentityLinkID, OperationID: op, ApplicationID: id, RequestID: adminRequestID(c), ExpectedVersion: payload.ExpectedVersion, Decision: payload.Decision, Comment: payload.Comment})
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	response.OK(c, v)
}
