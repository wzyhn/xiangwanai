package xiangwanapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	activitypostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity/postgres"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon"
	peoplepostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/people/postgres"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/resource"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/wzyhn/xiangwanai/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

const maxAdminCatalogBodyBytes = 32 * 1024

type AdminCatalogHandler struct {
	catalog                     xiangwanadmin.Catalog
	hostApplications            xiangwanadmin.HostApplicationAdministration
	scheduledPublications       xiangwanadmin.ScheduledPublicationCatalog
	scheduledPublicationQueries xiangwanadmin.ScheduledPublicationQueryCatalog
	scheduledPublicationCancel  xiangwanadmin.ScheduledPublicationCancellationCatalog
	hostRulesModerator          func(context.Context, string) error
	templates                   xiangwanadmin.QuestionnaireTemplateCatalog
	cancellation                xiangwanadmin.InstanceCancellationService
	sessionCancellation         xiangwanadmin.SessionCancellationService
	checkinAdministration       xiangwanadmin.CheckinAdministration
	peopleBindingAdministration xiangwanadmin.PeopleBindingAdministration
	reviewWriter                resource.ReviewResourceWriter
	photoCuration               resource.PhotoCurationService
	refundOperator              xiangwanadmin.RefundOperator
	couponCorrections           xiangwanadmin.CouponCorrectionReader
	couponCorrectionOperator    xiangwanadmin.CouponCorrectionOperator
	couponPolicy                coupon.PolicyActivationWriter
	couponReplenisher           couponReplenisher
	couponTenantID              uuid.UUID
	mediaUploads                xiangwanadmin.ReviewMediaUploadService
}

func NewAdminCatalogHandler(catalog xiangwanadmin.Catalog) *AdminCatalogHandler {
	value, _ := catalog.(xiangwanadmin.SessionCancellationService)
	checkins, _ := catalog.(xiangwanadmin.CheckinAdministration)
	bindings, _ := catalog.(xiangwanadmin.PeopleBindingAdministration)
	hosts, _ := catalog.(xiangwanadmin.HostApplicationAdministration)
	scheduled, _ := catalog.(xiangwanadmin.ScheduledPublicationCatalog)
	scheduledQueries, _ := catalog.(xiangwanadmin.ScheduledPublicationQueryCatalog)
	scheduledCancel, _ := catalog.(xiangwanadmin.ScheduledPublicationCancellationCatalog)
	return &AdminCatalogHandler{catalog: catalog, hostApplications: hosts, scheduledPublications: scheduled, scheduledPublicationQueries: scheduledQueries, scheduledPublicationCancel: scheduledCancel, templates: questionnaireTemplateCatalog(catalog), sessionCancellation: value, checkinAdministration: checkins, peopleBindingAdministration: bindings}
}

func NewAdminCatalogHandlerWithCancellation(
	catalog xiangwanadmin.Catalog,
	cancellation xiangwanadmin.InstanceCancellationService,
) *AdminCatalogHandler {
	handler := NewAdminCatalogHandler(catalog)
	handler.cancellation = cancellation
	return handler
}

func questionnaireTemplateCatalog(catalog xiangwanadmin.Catalog) xiangwanadmin.QuestionnaireTemplateCatalog {
	if value, ok := catalog.(xiangwanadmin.QuestionnaireTemplateCatalog); ok {
		return value
	}
	return nil
}

// SetReviewResourceWriter wires the provider-owned Content/resource writer
// after the handler is constructed. Keeping it as a narrow interface lets
// route tests use the catalog handler without opening a database transaction.
func (handler *AdminCatalogHandler) SetReviewResourceWriter(
	writer resource.ReviewResourceWriter,
) {
	if handler != nil {
		handler.reviewWriter = writer
	}
}

func (handler *AdminCatalogHandler) SetPhotoCurationService(service resource.PhotoCurationService) {
	if handler != nil {
		handler.photoCuration = service
	}
}

func (handler *AdminCatalogHandler) SetRefundOperator(operator xiangwanadmin.RefundOperator) {
	if handler != nil {
		handler.refundOperator = operator
	}
}

func (handler *AdminCatalogHandler) SetCouponCorrectionReader(reader xiangwanadmin.CouponCorrectionReader) {
	if handler != nil {
		handler.couponCorrections = reader
	}
}

func (handler *AdminCatalogHandler) SetCouponCorrectionOperator(operator xiangwanadmin.CouponCorrectionOperator) {
	if handler != nil {
		handler.couponCorrectionOperator = operator
	}
}

func (handler *AdminCatalogHandler) SetCouponPolicyActivationWriter(writer coupon.PolicyActivationWriter) {
	if handler != nil {
		handler.couponPolicy = writer
	}
}

func (handler *AdminCatalogHandler) SetCouponReplenisher(
	tenantID uuid.UUID, writer couponReplenisher,
) {
	if handler != nil && tenantID != uuid.Nil && writer != nil {
		handler.couponTenantID = tenantID
		handler.couponReplenisher = writer
	}
}

func (handler *AdminCatalogHandler) SetReviewMediaUploadService(service xiangwanadmin.ReviewMediaUploadService) {
	if handler != nil {
		handler.mediaUploads = service
	}
}

func (handler *AdminCatalogHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.GET("/host-rules", handler.GetHostRules)
	group.POST("/host-rules", handler.PublishHostRules)
	group.GET("/host-applications", handler.ListHostApplications)
	group.GET("/host-applications/:application_id", handler.GetHostApplication)
	group.POST("/host-applications/:application_id/reviews", handler.ReviewHostApplication)
	group.GET("/brand-profile", handler.GetBrandProfile)
	group.PATCH("/brand-profile", handler.PublishBrandProfile)
	group.GET("/series", handler.ListSeries)
	group.POST("/series", handler.CreateSeries)
	group.PATCH("/series/:series_id", handler.UpdateSeries)
	group.GET("/instances", handler.ListInstances)
	group.POST("/instances", handler.CreateInstance)
	group.GET("/instances/:instance_id", handler.GetInstance)
	group.POST("/instances/:instance_id/copies", handler.CopyInstance)
	group.GET("/instances/:instance_id/review-status", handler.GetInstanceReviewStatus)
	group.POST("/instances/:instance_id/review-resources", handler.CreateReviewResource)
	group.GET("/instances/:instance_id/review-resources", handler.ListReviewResources)
	group.GET("/instances/:instance_id/review-photo-curations", handler.ListReviewPhotoCurations)
	group.GET("/review-resources/:relation_id/photo-curation", handler.GetReviewPhotoCuration)
	group.POST("/review-resources/:relation_id/photo-curation", handler.WriteReviewPhotoCuration)
	group.GET("/instances/:instance_id/questionnaire", handler.GetInstanceQuestionnaire)
	group.PATCH("/instances/:instance_id", handler.UpdateInstance)
	group.POST("/instances/:instance_id/sessions", handler.CreateSession)
	group.PATCH("/instances/:instance_id/sessions/:session_id", handler.UpdateSession)
	group.POST("/instances/:instance_id/questionnaire", handler.PublishInstanceQuestionnaire)
	group.POST("/instances/:instance_id/questionnaire-assignments", handler.AssignQuestionnaireTemplateToInstance)
	group.GET("/questionnaire-templates", handler.ListQuestionnaireTemplates)
	group.POST("/questionnaire-templates", handler.CreateQuestionnaireTemplate)
	group.GET("/questionnaire-templates/:template_id", handler.GetQuestionnaireTemplate)
	group.PATCH("/questionnaire-templates/:template_id", handler.UpdateQuestionnaireTemplate)
	group.DELETE("/questionnaire-templates/:template_id", handler.ArchiveQuestionnaireTemplate)
	group.POST("/questionnaire-templates/:template_id/assignments", handler.AssignQuestionnaireTemplate)
	group.POST("/instances/:instance_id/publications", handler.PublishInstance)
	if handler.scheduledPublications != nil {
		group.POST("/instances/:instance_id/publication-schedules", handler.ScheduleInstancePublication)
	}
	if handler.scheduledPublicationQueries != nil {
		group.GET("/instances/:instance_id/publication-schedules", handler.ListInstancePublicationSchedules)
	}
	if handler.scheduledPublicationCancel != nil {
		group.POST("/instances/:instance_id/publication-schedules/:schedule_id/cancellation", handler.CancelScheduledPublication)
	}
	group.POST("/instances/:instance_id/completion", handler.CompleteInstance)
	group.POST("/instances/:instance_id/archives", handler.ArchiveInstance)
	group.POST("/sessions/:session_id/archives", handler.ArchiveSession)
	group.POST("/instances/:instance_id/cancellation-previews", handler.PreviewInstanceCancellation)
	group.POST("/instances/:instance_id/cancellation", handler.CancelInstance)
	group.POST("/sessions/:session_id/cancellation-previews", handler.PreviewSessionCancellation)
	group.POST("/sessions/:session_id/cancellation", handler.CancelSession)
	group.GET("/registrations/:registration_id/checkin", handler.GetRegistrationCheckin)
	group.POST("/people/:people_id/binding-invitations", handler.CreatePeopleBindingInvitation)
	group.GET("/people/:people_id/binding", handler.GetPeopleBinding)
	group.POST("/people/:people_id/binding-revocations", handler.RevokePeopleBinding)
	group.POST("/people/:people_id/binding-invitation-revocations", handler.RevokePeopleBindingInvitation)
	group.POST("/registrations/:registration_id/checkin-revocations", handler.RevokeRegistrationCheckin)
	group.GET("/registrations", handler.ListRegistrations)
	group.GET("/registrations/answer-summaries", handler.ListRegistrationAnswerSummaries)
	group.GET("/registrations/:registration_id/answers", handler.GetRegistrationAnswers)
	group.GET("/registrations/:registration_id", handler.GetRegistration)
	group.GET("/audit-events", handler.ListAuditEvents)
	group.GET("/coupon-corrections", handler.ListCouponCorrections)
	if handler.couponCorrectionOperator != nil {
		group.POST("/coupon-corrections/:entry_id/actions", handler.TransitionCouponCorrection)
	}
	if handler.couponPolicy != nil {
		group.POST("/coupon-grant-policies", handler.ActivateCouponGrantPolicy)
	}
	if handler.couponReplenisher != nil && handler.couponTenantID != uuid.Nil {
		group.POST("/coupon-grants", handler.ReplenishCoupon)
	}
	if handler.mediaUploads != nil {
		group.POST("/media-upload-intents", handler.IssueReviewMediaUpload)
		group.PUT("/media-upload-intents/:file_id/bytes", handler.StageReviewMediaBytes)
		group.GET("/media-upload-intents/:file_id/preview", handler.PreviewReviewMediaBytes)
		group.POST("/media-confirmations", handler.ConfirmReviewMediaUpload)
	}
	group.GET("/refund-cases", handler.ListRefundQueue)
	group.GET("/refund-cases/:case_id", handler.GetRefundCase)
	group.POST("/refund-cases/:case_id/actions", handler.TransitionRefund)
	group.GET("/orders", handler.ListOrders)
	group.GET("/orders/:order_id", handler.GetOrder)
	group.GET("/checkin-targets", handler.ListCheckinTargets)
	group.GET("/people", handler.ListPeople)
	group.POST("/people", handler.CreatePeopleProfile)
	group.PATCH("/people/:people_id", handler.UpdatePeopleProfile)
	group.POST("/people/:people_id/reviews", handler.ReviewPeopleProfile)
	group.GET("/instances/:instance_id/roles", handler.ListInstanceRoles)
	group.POST("/instances/:instance_id/roles", handler.AssignInstanceRole)
	group.POST("/instances/:instance_id/roles/:role_binding_id/revocations", handler.RevokeInstanceRole)
}

// GetBrandProfile godoc
// @Summary Read the Xiangwan homepage BrandProfile
// @Tags xiangwan-admin
// @Produce json
// @Success 200 {object} AdminBrandProfileResponse
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Router /xiangwan/admin/brand-profile [get]
func (handler *AdminCatalogHandler) GetBrandProfile(c *gin.Context) {
	principal, ok := handler.principal(c)
	if !ok || !validAdminQuery(c) {
		return
	}
	value, err := handler.catalog.GetBrandProfile(c.Request.Context(), principal)
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	response.OK(c, projectAdminBrandProfile(value))
}

// PublishBrandProfile godoc
// @Summary Publish the Xiangwan homepage BrandProfile
// @Description ADMIN OP-KEY command. Publishes one immutable text/image hero snapshot from PostgreSQL.
// @Tags xiangwan-admin
// @Accept json
// @Produce json
// @Param Idempotency-Key header string true "Canonical lowercase UUIDv4 operation key"
// @Param request body PublishAdminBrandProfileRequest true "Complete homepage brand snapshot"
// @Success 200 {object} AdminBrandProfileResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 409 {object} response.Body
// @Router /xiangwan/admin/brand-profile [patch]
func (handler *AdminCatalogHandler) PublishBrandProfile(c *gin.Context) {
	principal, ok := handler.principal(c)
	if !ok || !validAdminWriteRequest(c) {
		return
	}
	operationID, err := parseOperationKey(c)
	if err != nil {
		writeError(c, err)
		return
	}
	var payload PublishAdminBrandProfileRequest
	if err := decodeAdminCatalogJSON(c, &payload); err != nil {
		writeError(c, err)
		return
	}
	quickTags := make([]activity.HomeQuickTag, 0, len(payload.QuickTags))
	for _, tag := range payload.QuickTags {
		quickTags = append(quickTags, activity.HomeQuickTag{Code: tag.Code, Label: tag.Label})
	}
	value, err := handler.catalog.PublishBrandProfile(
		c.Request.Context(),
		xiangwanadmin.PublishBrandProfileCommand{
			ActorID: principal.PrincipalID, IdentityLinkID: principal.IdentityLinkID,
			OperationID: operationID, RequestID: adminRequestID(c),
			ExpectedVersion: payload.ExpectedVersion,
			CommunityName:   payload.CommunityName, BrandIntro: payload.BrandIntro,
			HeroMode: payload.HeroMode, HeroEyebrow: payload.HeroEyebrow,
			HeroSubtitle: payload.HeroSubtitle, HeroImageURL: payload.HeroImageURL,
			HeroImageAlt: payload.HeroImageAlt, QuickTags: quickTags,
		},
	)
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	response.OK(c, projectAdminBrandProfile(value))
}

// ListSeries godoc
// @Summary List Xiangwan administrator Series
// @Description Reads activity Series visible to the live administrator Grant from customer PostgreSQL.
// @Tags xiangwan-admin
// @Produce json
// @Param page query int false "Page number" minimum(1)
// @Param page_size query int false "Page size" minimum(1) maximum(100)
// @Success 200 {object} AdminSeriesPageResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Router /xiangwan/admin/series [get]
func (handler *AdminCatalogHandler) ListSeries(c *gin.Context) {
	principal, ok := handler.principal(c)
	if !ok || !validAdminQuery(c, "page", "page_size") {
		return
	}
	page, pageSize, ok := parseAdminPage(c)
	if !ok {
		return
	}
	result, err := handler.catalog.ListSeries(
		c.Request.Context(), principal, page, pageSize,
	)
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	items := make([]AdminSeriesResponse, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, projectAdminSeries(item))
	}
	response.OK(c, AdminSeriesPageResponse{
		Items: items, Page: result.Page, PageSize: result.PageSize, Total: result.Total,
	})
}

// CreateSeries godoc
// @Summary Create a Xiangwan Series
// @Description ADMIN OP-KEY command. Rechecks the active generation and administrator Grant in PostgreSQL.
// @Tags xiangwan-admin
// @Accept json
// @Produce json
// @Param Idempotency-Key header string true "Canonical lowercase UUIDv4 operation key"
// @Param request body CreateAdminSeriesRequest true "Series fields"
// @Success 201 {object} AdminSeriesResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 409 {object} response.Body
// @Router /xiangwan/admin/series [post]
func (handler *AdminCatalogHandler) CreateSeries(c *gin.Context) {
	principal, ok := handler.principal(c)
	if !ok || !validAdminWriteRequest(c) {
		return
	}
	operationID, err := parseOperationKey(c)
	if err != nil {
		writeError(c, err)
		return
	}
	var payload CreateAdminSeriesRequest
	if err := decodeAdminCatalogJSON(c, &payload); err != nil {
		writeError(c, err)
		return
	}
	if payload.HomeVisible == nil {
		writeError(c, errx.NewBadRequest("home_visible is required"))
		return
	}
	value, err := handler.catalog.CreateSeries(c.Request.Context(),
		xiangwanadmin.CreateSeriesCommand{
			ActorID: principal.PrincipalID, IdentityLinkID: principal.IdentityLinkID,
			OperationID: operationID,
			RequestID:   adminRequestID(c), Title: payload.Title,
			HomeVisible: *payload.HomeVisible,
		})
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	response.Created(c, projectAdminSeries(value))
}

// ListInstances godoc
// @Summary List Xiangwan administrator Instances
// @Tags xiangwan-admin
// @Produce json
// @Param series_id query string false "Exact Series ID"
// @Param page query int false "Page number" minimum(1)
// @Param page_size query int false "Page size" minimum(1) maximum(100)
// @Success 200 {object} AdminInstancePageResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Router /xiangwan/admin/instances [get]
func (handler *AdminCatalogHandler) ListInstances(c *gin.Context) {
	principal, ok := handler.principal(c)
	if !ok || !validAdminQuery(c, "series_id", "page", "page_size") {
		return
	}
	page, pageSize, ok := parseAdminPage(c)
	if !ok {
		return
	}
	seriesID, ok := parseOptionalAdminUUID(c, "series_id")
	if !ok {
		return
	}
	result, err := handler.catalog.ListInstances(
		c.Request.Context(), principal, seriesID, page, pageSize,
	)
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	items := make([]AdminInstanceListItemResponse, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, AdminInstanceListItemResponse{
			Instance:    projectAdminInstance(item.Instance),
			SeriesTitle: item.SeriesTitle, SessionCount: item.SessionCount,
		})
	}
	response.OK(c, AdminInstancePageResponse{
		Items: items, Page: result.Page, PageSize: result.PageSize, Total: result.Total,
	})
}

// CreateInstance godoc
// @Summary Create a Xiangwan Instance
// @Description ADMIN OP-KEY command bound to one exact Series version.
// @Tags xiangwan-admin
// @Accept json
// @Produce json
// @Param Idempotency-Key header string true "Canonical lowercase UUIDv4 operation key"
// @Param request body CreateAdminInstanceRequest true "Instance fields"
// @Success 201 {object} AdminInstanceResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 409 {object} response.Body
// @Router /xiangwan/admin/instances [post]
func (handler *AdminCatalogHandler) CreateInstance(c *gin.Context) {
	principal, ok := handler.principal(c)
	if !ok || !validAdminWriteRequest(c) {
		return
	}
	operationID, err := parseOperationKey(c)
	if err != nil {
		writeError(c, err)
		return
	}
	var payload CreateAdminInstanceRequest
	if err := decodeAdminCatalogJSON(c, &payload); err != nil {
		writeError(c, err)
		return
	}
	seriesID, err := parseCanonicalUUID(payload.SeriesID)
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid Series id"))
		return
	}
	value, err := handler.catalog.CreateInstance(c.Request.Context(),
		xiangwanadmin.CreateInstanceCommand{
			ActorID: principal.PrincipalID, IdentityLinkID: principal.IdentityLinkID,
			OperationID: operationID,
			RequestID:   adminRequestID(c), SeriesID: seriesID,
			ExpectedSeriesVersion: payload.ExpectedSeriesVersion,
			IssueNo: func() int {
				if payload.IssueNo == nil {
					return 0
				}
				return *payload.IssueNo
			}(),
			Title: payload.Title, ActivityType: payload.ActivityType,
			QuickTagCodes: payload.QuickTagCodes, CoverImageURL: payload.CoverImageURL,
			DetailBlocks: payload.DetailBlocks,
		})
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	response.Created(c, projectAdminInstance(value))
}

// CopyInstance godoc
// @Summary Create an independent Xiangwan period from reviewed source defaults
// @Description ADMIN OP-KEY command. The selected editable fields are explicit in the request; source Instance/presentation/questionnaire and Series versions are checked in the creation transaction. Registration, Order, Checkin, cancellation, publication, resource and Session facts are never copied.
// @Tags xiangwan-admin
// @Accept json
// @Produce json
// @Param instance_id path string true "Source Instance ID"
// @Param Idempotency-Key header string true "Canonical lowercase UUIDv4 operation key"
// @Param request body CopyAdminInstanceRequest true "Independent target fields and source version"
// @Success 201 {object} AdminInstanceCopyResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 404 {object} response.Body
// @Failure 409 {object} response.Body
// @Router /xiangwan/admin/instances/{instance_id}/copies [post]
func (handler *AdminCatalogHandler) CopyInstance(c *gin.Context) {
	principal, ok := handler.principal(c)
	if !ok || !validAdminWriteRequest(c) {
		return
	}
	operationID, err := parseOperationKey(c)
	if err != nil {
		writeError(c, err)
		return
	}
	sourceID, err := parseCanonicalUUID(c.Param("instance_id"))
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid source Instance id"))
		return
	}
	var payload CopyAdminInstanceRequest
	if err := decodeAdminCatalogJSON(c, &payload); err != nil {
		writeError(c, err)
		return
	}
	seriesID, err := parseCanonicalUUID(payload.SeriesID)
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid Series id"))
		return
	}
	var sourceQuestionnaireVersionID uuid.UUID
	if payload.ExpectedSourceQuestionnaireVersionID != "" {
		sourceQuestionnaireVersionID, err = parseCanonicalUUID(payload.ExpectedSourceQuestionnaireVersionID)
		if err != nil {
			writeError(c, errx.NewBadRequest("invalid source questionnaire version id"))
			return
		}
	}
	value, err := handler.catalog.CreateInstance(c.Request.Context(),
		xiangwanadmin.CreateInstanceCommand{
			ActorID: principal.PrincipalID, IdentityLinkID: principal.IdentityLinkID,
			OperationID: operationID, RequestID: adminRequestID(c),
			SeriesID: seriesID, ExpectedSeriesVersion: payload.ExpectedSeriesVersion,
			IssueNo: optionalAdminIssueNo(payload.IssueNo), Title: payload.Title,
			ActivityType: payload.ActivityType, QuickTagCodes: payload.QuickTagCodes,
			CoverImageURL: payload.CoverImageURL, DetailBlocks: payload.DetailBlocks,
			SourceInstanceID: sourceID, ExpectedSourceVersion: payload.ExpectedSourceVersion,
			ExpectedSourcePresentationRevision:   payload.ExpectedSourcePresentationRevision,
			ExpectedSourceQuestionnaireVersionID: sourceQuestionnaireVersionID,
		})
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	response.Created(c, AdminInstanceCopyResponse{
		AdminInstanceResponse: projectAdminInstance(value),
		SourceInstanceID:      sourceID.String(), SourceInstanceVersion: payload.ExpectedSourceVersion,
		SourcePresentationRevision:   payload.ExpectedSourcePresentationRevision,
		SourceQuestionnaireVersionID: payload.ExpectedSourceQuestionnaireVersionID,
	})
}

func optionalAdminIssueNo(issueNo *int) int {
	if issueNo == nil {
		return 0
	}
	return *issueNo
}

func optionalAdminUUIDString(value uuid.UUID) string {
	if value == uuid.Nil {
		return ""
	}
	return value.String()
}

// GetInstance godoc
// @Summary Read one Xiangwan administrator Instance
// @Tags xiangwan-admin
// @Produce json
// @Param instance_id path string true "Instance ID"
// @Success 200 {object} AdminInstanceDetailResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 404 {object} response.Body
// @Router /xiangwan/admin/instances/{instance_id} [get]
func (handler *AdminCatalogHandler) GetInstance(c *gin.Context) {
	principal, ok := handler.principal(c)
	if !ok || !validAdminQuery(c) {
		return
	}
	instanceID, err := parseCanonicalUUID(c.Param("instance_id"))
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid Instance id"))
		return
	}
	value, err := handler.catalog.GetInstance(
		c.Request.Context(), principal, instanceID,
	)
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	sessions := make([]AdminSessionResponse, 0, len(value.Sessions))
	for _, item := range value.Sessions {
		sessions = append(sessions, projectAdminSession(item))
	}
	var copyLineage *AdminInstanceCopyLineageResponse
	if value.CopyLineage != nil {
		copyLineage = &AdminInstanceCopyLineageResponse{
			SourceInstanceID:             value.CopyLineage.SourceInstanceID.String(),
			SourceInstanceVersion:        value.CopyLineage.SourceInstanceVersion,
			SourcePresentationRevision:   value.CopyLineage.SourcePresentationRevision,
			SourceQuestionnaireVersionID: optionalAdminUUIDString(value.CopyLineage.SourceQuestionnaireVersionID),
		}
	}
	response.OK(c, AdminInstanceDetailResponse{
		Series:   projectAdminSeries(value.Series),
		Instance: projectAdminInstance(value.Instance), Sessions: sessions,
		Questionnaire: projectAdminQuestionnaire(value.Questionnaire),
		CopyLineage:   copyLineage,
	})
}

// GetInstanceReviewStatus godoc
// @Summary Read one Xiangwan Instance's published review/resource status
// @Description Read-only administrator projection of the existing approved publication chain. It returns counts and timestamps only; review bodies, storage keys, and raw external URLs remain behind the public review/media readers.
// @Tags xiangwan-admin
// @Produce json
// @Param instance_id path string true "Instance ID"
// @Success 200 {object} AdminInstanceReviewStatusResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 404 {object} response.Body
// @Router /xiangwan/admin/instances/{instance_id}/review-status [get]
func (handler *AdminCatalogHandler) GetInstanceReviewStatus(c *gin.Context) {
	principal, ok := handler.principal(c)
	if !ok || !validAdminQuery(c) {
		return
	}
	instanceID, err := parseCanonicalUUID(c.Param("instance_id"))
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid Instance id"))
		return
	}
	value, err := handler.catalog.GetInstanceReviewStatus(
		c.Request.Context(), principal, instanceID,
	)
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	response.OK(c, projectAdminInstanceReviewStatus(value))
}

// CreateReviewResource godoc
// @Summary Create and publish one Xiangwan administrator review document
// @Description ADMIN OP-KEY command. The server writes private review Content, binds an exact completed Instance/Session, records manual-review evidence, and publishes the immutable resource in one PostgreSQL transaction. Video URLs are optional when a photo or typed resource link is supplied; every external URL is accepted only when the configured external-domain policy allows it.
// @Tags xiangwan-admin
// @Accept json
// @Produce json
// @Param instance_id path string true "Instance ID"
// @Param Idempotency-Key header string true "Canonical lowercase UUIDv4 operation key"
// @Param request body CreateAdminReviewResourceRequest true "Review resource fields"
// @Success 201 {object} AdminReviewResourceResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 404 {object} response.Body
// @Failure 409 {object} response.Body
// @Router /xiangwan/admin/instances/{instance_id}/review-resources [post]
func (handler *AdminCatalogHandler) CreateReviewResource(c *gin.Context) {
	principal, ok := handler.principal(c)
	if !ok || !validAdminWriteRequest(c) {
		return
	}
	if handler.reviewWriter == nil {
		writeError(c, errx.NewInternal("review resource writer is unavailable"))
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
	var payload CreateAdminReviewResourceRequest
	if err := decodeAdminCatalogJSON(c, &payload); err != nil {
		writeError(c, err)
		return
	}
	var sessionID *uuid.UUID
	if strings.TrimSpace(payload.SessionID) != "" {
		parsed, parseErr := parseCanonicalUUID(payload.SessionID)
		if parseErr != nil {
			writeError(c, errx.NewBadRequest("invalid Session id"))
			return
		}
		sessionID = &parsed
	}
	var replacesRelationID *uuid.UUID
	if payload.ReplacesRelationID != "" {
		parsed, err := parseCanonicalUUID(payload.ReplacesRelationID)
		if err != nil {
			writeError(c, errx.NewBadRequest("invalid replaced relation ID"))
			return
		}
		replacesRelationID = &parsed
	}
	links := make([]resource.ReviewResourceLink, 0, 2)
	if payload.Recording != nil && (payload.Recording.Enabled || payload.Recording.URL != "") {
		links = append(links, resource.ReviewResourceLink{
			Kind:     resource.ReviewResourceLinkRecording,
			Hidden:   !payload.Recording.Enabled,
			Title:    payload.Recording.Title,
			Subtitle: payload.Recording.Subtitle,
			URL:      payload.Recording.URL,
		})
	}
	if payload.Materials != nil && (payload.Materials.Enabled || payload.Materials.URL != "") {
		links = append(links, resource.ReviewResourceLink{
			Kind:     resource.ReviewResourceLinkMaterials,
			Hidden:   !payload.Materials.Enabled,
			Title:    payload.Materials.Title,
			Subtitle: payload.Materials.Subtitle,
			URL:      payload.Materials.URL,
		})
	}
	files := make([]resource.ReviewResourceFile, 0, len(payload.Files))
	for _, selected := range payload.Files {
		fileID, parseErr := parseCanonicalUUID(selected.FileID)
		if parseErr != nil {
			writeError(c, errx.NewBadRequest("invalid review File id"))
			return
		}
		files = append(files, resource.ReviewResourceFile{
			FileID: fileID, Kind: selected.Kind, SHA256: selected.SHA256,
			Reviewed: selected.Reviewed,
		})
	}
	receipt, err := handler.reviewWriter.CreateReviewResource(
		c.Request.Context(),
		resource.CreateReviewResourceCommand{
			ActorID:                      principal.PrincipalID,
			IdentityLinkID:               principal.IdentityLinkID,
			InstanceID:                   instanceID,
			SessionID:                    sessionID,
			ExpectedTargetVersion:        payload.ExpectedTargetVersion,
			ReplacesRelationID:           replacesRelationID,
			ExpectedPhotoCurationVersion: payload.ExpectedPhotoCurationVersion,
			Title:                        payload.Title,
			Description:                  payload.Description,
			VideoURL:                     payload.VideoURL,
			VideoChannel:                 payload.VideoChannel,
			Photos:                       append([]string(nil), payload.Photos...),
			Files:                        files,
			Links:                        links,
			SortOrder:                    payload.SortOrder,
			OperationID:                  operationID,
			RequestID:                    adminRequestID(c),
		},
	)
	if err != nil {
		writeReviewResourceError(c, err)
		return
	}
	response.Created(c, projectAdminReviewResource(receipt))
}

// GetInstanceQuestionnaire godoc
// @Summary Read the current Xiangwan Instance registration questionnaire
// @Tags xiangwan-admin
// @Produce json
// @Param instance_id path string true "Instance ID"
// @Success 200 {object} AdminInstanceQuestionnaireResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 404 {object} response.Body
// @Router /xiangwan/admin/instances/{instance_id}/questionnaire [get]
func (handler *AdminCatalogHandler) GetInstanceQuestionnaire(c *gin.Context) {
	principal, ok := handler.principal(c)
	if !ok || !validAdminQuery(c) {
		return
	}
	instanceID, err := parseCanonicalUUID(c.Param("instance_id"))
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid Instance id"))
		return
	}
	value, err := handler.catalog.GetInstanceQuestionnaire(
		c.Request.Context(), principal, instanceID,
	)
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	response.OK(c, projectAdminQuestionnaire(value))
}

// PublishInstanceQuestionnaire godoc
// @Summary Publish a new immutable Xiangwan Instance registration questionnaire
// @Description ADMIN OP-KEY command. The new version is assigned to the Instance; existing registrations retain their snapshots.
// @Tags xiangwan-admin
// @Accept json
// @Produce json
// @Param instance_id path string true "Instance ID"
// @Param Idempotency-Key header string true "Canonical lowercase UUIDv4 operation key"
// @Param request body PublishAdminInstanceQuestionnaireRequest true "Questionnaire fields"
// @Success 201 {object} AdminInstanceQuestionnaireResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 404 {object} response.Body
// @Failure 409 {object} response.Body
// @Router /xiangwan/admin/instances/{instance_id}/questionnaire [post]
func (handler *AdminCatalogHandler) PublishInstanceQuestionnaire(c *gin.Context) {
	principal, ok := handler.principal(c)
	if !ok || !validAdminWriteRequest(c) {
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
	var payload PublishAdminInstanceQuestionnaireRequest
	if err := decodeAdminCatalogJSON(c, &payload); err != nil {
		writeError(c, err)
		return
	}
	fields := make([]activity.QuestionnaireField, 0, len(payload.Fields))
	for _, field := range payload.Fields {
		options := make([]activity.QuestionnaireOption, 0, len(field.Options))
		for _, option := range field.Options {
			options = append(options, activity.QuestionnaireOption{
				Code: option.Code, Label: option.Label,
			})
		}
		fields = append(fields, activity.QuestionnaireField{
			Code: field.Code, Type: field.Type, Label: field.Label,
			HelpText: field.HelpText, Required: field.Required,
			SortOrder: field.SortOrder, MinLength: field.MinLength,
			MaxLength: field.MaxLength, MaxSelections: field.MaxSelections,
			Options: options,
		})
	}
	value, err := handler.catalog.PublishInstanceQuestionnaire(
		c.Request.Context(), xiangwanadmin.PublishInstanceQuestionnaireCommand{
			ActorID: principal.PrincipalID, IdentityLinkID: principal.IdentityLinkID,
			OperationID: operationID, RequestID: adminRequestID(c),
			InstanceID: instanceID, PrivacyPurpose: payload.PrivacyPurpose,
			PrivacyPolicyVersion: payload.PrivacyPolicyVersion, Fields: fields,
		},
	)
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	response.Created(c, projectAdminQuestionnaire(&value))
}

// ListQuestionnaireTemplates godoc
// @Summary List reusable Xiangwan questionnaire templates
// @Tags xiangwan-admin
// @Produce json
// @Param page query int false "Page number" minimum(1)
// @Param page_size query int false "Page size" minimum(1) maximum(100)
// @Success 200 {object} AdminQuestionnaireTemplatePageResponse
// @Router /xiangwan/admin/questionnaire-templates [get]
func (handler *AdminCatalogHandler) ListQuestionnaireTemplates(c *gin.Context) {
	principal, ok := handler.principal(c)
	if !ok || !handler.requireQuestionnaireTemplates(c) || !validAdminQuery(c, "page", "page_size") {
		return
	}
	page, pageSize, ok := parseAdminPage(c)
	if !ok {
		return
	}
	result, err := handler.templates.ListQuestionnaireTemplates(c.Request.Context(), principal, page, pageSize)
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	items := make([]AdminQuestionnaireTemplateResponse, 0, len(result.Items))
	for _, value := range result.Items {
		items = append(items, projectAdminQuestionnaireTemplate(value))
	}
	response.OK(c, AdminQuestionnaireTemplatePageResponse{
		Items: items, Page: result.Page, PageSize: result.PageSize, Total: result.Total,
	})
}

// GetQuestionnaireTemplate godoc
// @Summary Read one reusable Xiangwan questionnaire template
// @Tags xiangwan-admin
// @Produce json
// @Param template_id path string true "Template ID"
// @Success 200 {object} AdminQuestionnaireTemplateResponse
// @Router /xiangwan/admin/questionnaire-templates/{template_id} [get]
func (handler *AdminCatalogHandler) GetQuestionnaireTemplate(c *gin.Context) {
	principal, ok := handler.principal(c)
	if !ok || !handler.requireQuestionnaireTemplates(c) || !validAdminQuery(c) {
		return
	}
	templateID, err := parseCanonicalUUID(c.Param("template_id"))
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid questionnaire template id"))
		return
	}
	value, err := handler.templates.GetQuestionnaireTemplate(c.Request.Context(), principal, templateID)
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	response.OK(c, projectAdminQuestionnaireTemplate(value))
}

// CreateQuestionnaireTemplate godoc
// @Summary Create a reusable Xiangwan questionnaire template
// @Tags xiangwan-admin
// @Accept json
// @Produce json
// @Param Idempotency-Key header string true "Canonical lowercase UUIDv4 operation key"
// @Param request body CreateAdminQuestionnaireTemplateRequest true "Questionnaire template"
// @Success 201 {object} AdminQuestionnaireTemplateResponse
// @Router /xiangwan/admin/questionnaire-templates [post]
func (handler *AdminCatalogHandler) CreateQuestionnaireTemplate(c *gin.Context) {
	principal, ok := handler.principal(c)
	if !ok || !handler.requireQuestionnaireTemplates(c) || !validAdminWriteRequest(c) {
		return
	}
	operationID, err := parseOperationKey(c)
	if err != nil {
		writeError(c, err)
		return
	}
	var payload CreateAdminQuestionnaireTemplateRequest
	if err := decodeAdminCatalogJSON(c, &payload); err != nil {
		writeError(c, err)
		return
	}
	value, err := handler.templates.CreateQuestionnaireTemplate(c.Request.Context(), xiangwanadmin.CreateQuestionnaireTemplateCommand{
		ActorID: principal.PrincipalID, IdentityLinkID: principal.IdentityLinkID,
		OperationID: operationID, RequestID: adminRequestID(c), Name: payload.Name,
		Description: payload.Description, PrivacyPurpose: payload.PrivacyPurpose,
		PrivacyPolicyVersion: payload.PrivacyPolicyVersion,
		Fields:               questionnaireFieldsFromRequests(payload.Fields),
	})
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	response.Created(c, projectAdminQuestionnaireTemplate(value))
}

// UpdateQuestionnaireTemplate godoc
// @Summary Create a new version of a reusable questionnaire template
// @Tags xiangwan-admin
// @Accept json
// @Produce json
// @Param template_id path string true "Template ID"
// @Param Idempotency-Key header string true "Canonical lowercase UUIDv4 operation key"
// @Param request body UpdateAdminQuestionnaireTemplateRequest true "Questionnaire template"
// @Success 200 {object} AdminQuestionnaireTemplateResponse
// @Router /xiangwan/admin/questionnaire-templates/{template_id} [patch]
func (handler *AdminCatalogHandler) UpdateQuestionnaireTemplate(c *gin.Context) {
	principal, ok := handler.principal(c)
	if !ok || !handler.requireQuestionnaireTemplates(c) || !validAdminWriteRequest(c) {
		return
	}
	operationID, err := parseOperationKey(c)
	if err != nil {
		writeError(c, err)
		return
	}
	templateID, err := parseCanonicalUUID(c.Param("template_id"))
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid questionnaire template id"))
		return
	}
	var payload UpdateAdminQuestionnaireTemplateRequest
	if err := decodeAdminCatalogJSON(c, &payload); err != nil {
		writeError(c, err)
		return
	}
	value, err := handler.templates.UpdateQuestionnaireTemplate(c.Request.Context(), xiangwanadmin.UpdateQuestionnaireTemplateCommand{
		ActorID: principal.PrincipalID, IdentityLinkID: principal.IdentityLinkID,
		OperationID: operationID, RequestID: adminRequestID(c), TemplateID: templateID,
		ExpectedVersion: payload.ExpectedVersion, Name: payload.Name, Description: payload.Description,
		PrivacyPurpose: payload.PrivacyPurpose, PrivacyPolicyVersion: payload.PrivacyPolicyVersion,
		Fields: questionnaireFieldsFromRequests(payload.Fields),
	})
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	response.OK(c, projectAdminQuestionnaireTemplate(value))
}

// ArchiveQuestionnaireTemplate godoc
// @Summary Archive a reusable Xiangwan questionnaire template
// @Tags xiangwan-admin
// @Accept json
// @Produce json
// @Param template_id path string true "Template ID"
// @Param Idempotency-Key header string true "Canonical lowercase UUIDv4 operation key"
// @Param request body ArchiveAdminQuestionnaireTemplateRequest true "Expected template version"
// @Success 200 {object} AdminQuestionnaireTemplateResponse
// @Router /xiangwan/admin/questionnaire-templates/{template_id} [delete]
func (handler *AdminCatalogHandler) ArchiveQuestionnaireTemplate(c *gin.Context) {
	principal, ok := handler.principal(c)
	if !ok || !handler.requireQuestionnaireTemplates(c) || !validAdminWriteRequest(c) {
		return
	}
	operationID, err := parseOperationKey(c)
	if err != nil {
		writeError(c, err)
		return
	}
	templateID, err := parseCanonicalUUID(c.Param("template_id"))
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid questionnaire template id"))
		return
	}
	var payload ArchiveAdminQuestionnaireTemplateRequest
	if err := decodeAdminCatalogJSON(c, &payload); err != nil {
		writeError(c, err)
		return
	}
	value, err := handler.templates.ArchiveQuestionnaireTemplate(c.Request.Context(), xiangwanadmin.ArchiveQuestionnaireTemplateCommand{
		ActorID: principal.PrincipalID, IdentityLinkID: principal.IdentityLinkID,
		OperationID: operationID, RequestID: adminRequestID(c), TemplateID: templateID,
		ExpectedVersion: payload.ExpectedVersion,
	})
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	response.OK(c, projectAdminQuestionnaireTemplate(value))
}

// AssignQuestionnaireTemplate godoc
// @Summary Copy the current template version into an Instance questionnaire
// @Tags xiangwan-admin
// @Accept json
// @Produce json
// @Param template_id path string true "Template ID"
// @Param Idempotency-Key header string true "Canonical lowercase UUIDv4 operation key"
// @Param request body AssignAdminQuestionnaireTemplateRequest true "Instance assignment"
// @Success 201 {object} AdminInstanceQuestionnaireResponse
// @Router /xiangwan/admin/questionnaire-templates/{template_id}/assignments [post]
func (handler *AdminCatalogHandler) AssignQuestionnaireTemplate(c *gin.Context) {
	principal, ok := handler.principal(c)
	if !ok || !handler.requireQuestionnaireTemplates(c) || !validAdminWriteRequest(c) {
		return
	}
	templateID, err := parseCanonicalUUID(c.Param("template_id"))
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid questionnaire template id"))
		return
	}
	handler.assignQuestionnaireTemplate(c, principal, templateID)
}

// AssignQuestionnaireTemplateToInstance is the Instance-oriented alias used
// by the admin engineering contract. It keeps the same idempotent command as
// the template-oriented route above.
func (handler *AdminCatalogHandler) AssignQuestionnaireTemplateToInstance(c *gin.Context) {
	principal, ok := handler.principal(c)
	if !ok || !handler.requireQuestionnaireTemplates(c) || !validAdminWriteRequest(c) {
		return
	}
	instanceID, err := parseCanonicalUUID(c.Param("instance_id"))
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid Instance id"))
		return
	}
	var payload AssignAdminQuestionnaireTemplateToInstanceRequest
	if err := decodeAdminCatalogJSON(c, &payload); err != nil {
		writeError(c, err)
		return
	}
	templateID, err := parseCanonicalUUID(payload.TemplateID)
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid questionnaire template id"))
		return
	}
	handler.assignQuestionnaireTemplateWithInstance(c, principal, templateID, instanceID, payload.ExpectedInstanceVersion)
}

func (handler *AdminCatalogHandler) assignQuestionnaireTemplate(c *gin.Context, principal xiangwanadmin.Principal, templateID uuid.UUID) {
	operationID, err := parseOperationKey(c)
	if err != nil {
		writeError(c, err)
		return
	}
	var payload AssignAdminQuestionnaireTemplateRequest
	if err := decodeAdminCatalogJSON(c, &payload); err != nil {
		writeError(c, err)
		return
	}
	instanceID, err := parseCanonicalUUID(payload.InstanceID)
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid Instance id"))
		return
	}
	handler.assignQuestionnaireTemplateWithOperation(c, principal, operationID, templateID, instanceID, payload.ExpectedInstanceVersion)
}

func (handler *AdminCatalogHandler) assignQuestionnaireTemplateWithInstance(c *gin.Context, principal xiangwanadmin.Principal, templateID, instanceID uuid.UUID, expectedVersion int64) {
	operationID, err := parseOperationKey(c)
	if err != nil {
		writeError(c, err)
		return
	}
	handler.assignQuestionnaireTemplateWithOperation(c, principal, operationID, templateID, instanceID, expectedVersion)
}

func (handler *AdminCatalogHandler) assignQuestionnaireTemplateWithOperation(c *gin.Context, principal xiangwanadmin.Principal, operationID, templateID, instanceID uuid.UUID, expectedVersion int64) {
	value, err := handler.templates.ApplyQuestionnaireTemplate(c.Request.Context(), xiangwanadmin.ApplyQuestionnaireTemplateCommand{
		ActorID: principal.PrincipalID, IdentityLinkID: principal.IdentityLinkID,
		OperationID: operationID, RequestID: adminRequestID(c), TemplateID: templateID,
		InstanceID: instanceID, ExpectedVersion: expectedVersion,
	})
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	response.Created(c, projectAdminQuestionnaire(&value))
}

// UpdateInstance godoc
// @Summary Patch a Xiangwan Instance's presentational fields
// @Description ADMIN OP-KEY command fenced by the Instance presentation_revision. Only cover_image_url and detail_blocks change; detail_blocks replaces the whole list. A byte-identical PATCH is a successful no-op and the operational version stays untouched, so published review resources pinned to it remain valid.
// @Tags xiangwan-admin
// @Accept json
// @Produce json
// @Param instance_id path string true "Instance ID"
// @Param Idempotency-Key header string true "Canonical lowercase UUIDv4 operation key"
// @Param request body UpdateAdminInstanceRequest true "Fields to patch"
// @Success 200 {object} AdminInstanceResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 404 {object} response.Body
// @Failure 409 {object} response.Body
// @Router /xiangwan/admin/instances/{instance_id} [patch]
func (handler *AdminCatalogHandler) UpdateInstance(c *gin.Context) {
	principal, ok := handler.principal(c)
	if !ok || !validAdminWriteRequest(c) {
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
	var payload UpdateAdminInstanceRequest
	if err := decodeAdminCatalogJSON(c, &payload); err != nil {
		writeError(c, err)
		return
	}
	if payload.CoverImageURL == nil && payload.DetailBlocks == nil {
		writeError(c, errx.NewBadRequest("no Instance field to update"))
		return
	}
	value, err := handler.catalog.UpdateInstance(c.Request.Context(),
		xiangwanadmin.UpdateInstanceCommand{
			ActorID: principal.PrincipalID, IdentityLinkID: principal.IdentityLinkID,
			OperationID: operationID,
			RequestID:   adminRequestID(c), InstanceID: instanceID,
			ExpectedPresentationRevision: payload.ExpectedPresentationRevision,
			CoverImageURL:                payload.CoverImageURL,
			DetailBlocks:                 payload.DetailBlocks,
		})
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	response.OK(c, projectAdminInstance(value))
}

// CreateSession godoc
// @Summary Create a complete Xiangwan Session
// @Description ADMIN OP-KEY command. Requires a complete Session so publication never fabricates operational facts.
// @Tags xiangwan-admin
// @Accept json
// @Produce json
// @Param instance_id path string true "Instance ID"
// @Param Idempotency-Key header string true "Canonical lowercase UUIDv4 operation key"
// @Param request body CreateAdminSessionRequest true "Complete Session fields"
// @Success 201 {object} AdminSessionResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 409 {object} response.Body
// @Router /xiangwan/admin/instances/{instance_id}/sessions [post]
func (handler *AdminCatalogHandler) CreateSession(c *gin.Context) {
	principal, ok := handler.principal(c)
	if !ok || !validAdminWriteRequest(c) {
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
	var payload CreateAdminSessionRequest
	if err := decodeAdminCatalogJSON(c, &payload); err != nil {
		writeError(c, err)
		return
	}
	registrationStart, registrationEnd, sessionStart, sessionEnd, ok :=
		parseAdminSessionTimes(c, payload)
	if !ok {
		return
	}
	value, err := handler.catalog.CreateSession(c.Request.Context(),
		xiangwanadmin.CreateSessionCommand{
			ActorID: principal.PrincipalID, IdentityLinkID: principal.IdentityLinkID,
			OperationID: operationID,
			RequestID:   adminRequestID(c), InstanceID: instanceID,
			ExpectedInstanceVersion: payload.ExpectedInstanceVersion,
			Title:                   payload.Title, RegistrationStartAt: registrationStart,
			RegistrationEndAt: registrationEnd, SessionStartAt: sessionStart,
			SessionEndAt: sessionEnd, Capacity: payload.Capacity,
			GroupMinimum:      payload.GroupMinimum,
			LowStockThreshold: payload.LowStockThreshold,
			PriceCents:        payload.PriceCents, DeliveryMode: payload.DeliveryMode,
			Area: payload.Area, VenueName: payload.VenueName,
			Address: payload.Address, Longitude: payload.Longitude,
			Latitude:                payload.Latitude,
			OnlineParticipationMode: payload.OnlineParticipationMode,
			OnlineCompliant:         payload.OnlineCompliant,
			SortOrder:               payload.SortOrder,
		})
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	response.Created(c, projectAdminSession(value))
}

// PublishInstance godoc
// @Summary Publish one Xiangwan Instance
// @Description ADMIN OP-KEY command. Validates the complete Session set and commits public facts, operation receipt, and audit event atomically.
// @Tags xiangwan-admin
// @Accept json
// @Produce json
// @Param instance_id path string true "Instance ID"
// @Param Idempotency-Key header string true "Canonical lowercase UUIDv4 operation key"
// @Param request body PublishAdminInstanceRequest true "Expected Instance version"
// @Success 200 {object} AdminPublicationResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 404 {object} response.Body
// @Failure 409 {object} response.Body
// @Router /xiangwan/admin/instances/{instance_id}/publications [post]
func (handler *AdminCatalogHandler) PublishInstance(c *gin.Context) {
	principal, ok := handler.principal(c)
	if !ok || !validAdminWriteRequest(c) {
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
	var payload PublishAdminInstanceRequest
	if err := decodeAdminCatalogJSON(c, &payload); err != nil {
		writeError(c, err)
		return
	}
	value, err := handler.catalog.PublishInstance(c.Request.Context(),
		xiangwanadmin.PublishInstanceCommand{
			ActorID: principal.PrincipalID, IdentityLinkID: principal.IdentityLinkID,
			OperationID: operationID,
			RequestID:   adminRequestID(c), InstanceID: instanceID,
			ExpectedInstanceVersion: payload.ExpectedInstanceVersion,
		})
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	response.OK(c, AdminPublicationResponse{
		ID: value.ID.String(), InstanceID: value.InstanceID.String(),
		PublicationVersion: value.PublicationVersion,
		SessionCount:       value.SessionCount,
		PublishedAt:        value.PublishedAt.UTC().Format(time.RFC3339Nano),
	})
}

// ScheduleInstancePublication records a future publication intent. The
// durable worker later calls the same publisher used by PublishInstance.
// @Summary Schedule one Xiangwan Instance for publication
// @Tags xiangwan-admin
// @Accept json
// @Produce json
// @Param instance_id path string true "Instance ID"
// @Param Idempotency-Key header string true "Canonical lowercase UUIDv4 operation key"
// @Param request body ScheduleAdminInstancePublicationRequest true "Expected version and future publish time"
// @Success 201 {object} AdminScheduledPublicationResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 404 {object} response.Body
// @Failure 409 {object} response.Body
// @Router /xiangwan/admin/instances/{instance_id}/publication-schedules [post]
func (handler *AdminCatalogHandler) ScheduleInstancePublication(c *gin.Context) {
	principal, ok := handler.principal(c)
	if !ok || !validAdminWriteRequest(c) || handler.scheduledPublications == nil {
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
	var payload ScheduleAdminInstancePublicationRequest
	if err := decodeAdminCatalogJSON(c, &payload); err != nil {
		writeError(c, err)
		return
	}
	scheduledAt, err := time.Parse(time.RFC3339Nano, payload.ScheduledAt)
	if err != nil {
		writeError(c, errx.NewBadRequest("scheduled_at must be RFC3339"))
		return
	}
	value, err := handler.scheduledPublications.ScheduleInstancePublication(
		c.Request.Context(), xiangwanadmin.SchedulePublicationCommand{
			ActorID: principal.PrincipalID, IdentityLinkID: principal.IdentityLinkID,
			OperationID: operationID, RequestID: adminRequestID(c), InstanceID: instanceID,
			ExpectedInstanceVersion: payload.ExpectedInstanceVersion, ScheduledAt: scheduledAt,
		},
	)
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	response.Created(c, projectAdminScheduledPublication(value))
}

// ListInstancePublicationSchedules returns the durable schedule history for an
// Instance, including terminal worker failures and operator cancellations.
// @Summary List Xiangwan Instance publication schedules
// @Tags xiangwan-admin
// @Produce json
// @Param instance_id path string true "Instance ID"
// @Success 200 {object} AdminScheduledPublicationPageResponse
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 404 {object} response.Body
// @Router /xiangwan/admin/instances/{instance_id}/publication-schedules [get]
func (handler *AdminCatalogHandler) ListInstancePublicationSchedules(c *gin.Context) {
	principal, ok := handler.principal(c)
	if !ok || handler.scheduledPublicationQueries == nil {
		return
	}
	instanceID, err := parseCanonicalUUID(c.Param("instance_id"))
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid Instance id"))
		return
	}
	values, err := handler.scheduledPublicationQueries.ListInstancePublicationSchedules(
		c.Request.Context(), principal, instanceID,
	)
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	items := make([]AdminScheduledPublicationResponse, 0, len(values))
	for _, value := range values {
		items = append(items, projectAdminScheduledPublication(value))
	}
	response.OK(c, AdminScheduledPublicationPageResponse{Items: items})
}

// CancelScheduledPublication stops a pending schedule and clears the
// instance's scheduled_at marker. The queue row remains as a cancelled fact.
// @Summary Cancel a pending Xiangwan publication schedule
// @Tags xiangwan-admin
// @Produce json
// @Param instance_id path string true "Instance ID"
// @Param schedule_id path string true "Schedule ID"
// @Param Idempotency-Key header string true "Canonical lowercase UUIDv4 operation key"
// @Success 200 {object} AdminScheduledPublicationResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 404 {object} response.Body
// @Failure 409 {object} response.Body
// @Router /xiangwan/admin/instances/{instance_id}/publication-schedules/{schedule_id}/cancellation [post]
func (handler *AdminCatalogHandler) CancelScheduledPublication(c *gin.Context) {
	principal, ok := handler.principal(c)
	if !ok || !validAdminWriteRequest(c) || handler.scheduledPublicationCancel == nil {
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
	scheduleID, err := parseCanonicalUUID(c.Param("schedule_id"))
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid schedule id"))
		return
	}
	value, err := handler.scheduledPublicationCancel.CancelScheduledPublication(
		c.Request.Context(), xiangwanadmin.CancelScheduledPublicationCommand{
			ActorID: principal.PrincipalID, IdentityLinkID: principal.IdentityLinkID,
			OperationID: operationID, RequestID: adminRequestID(c),
			InstanceID: instanceID, ScheduleID: scheduleID,
		},
	)
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	response.OK(c, projectAdminScheduledPublication(value))
}

// CompleteInstance godoc
// @Summary Move a published Xiangwan Instance into its terminal completed state
// @Description ADMIN OP-KEY command. Requires at least one Session to have ended and every other Session to have ended or been cancelled; the completed Instance becomes eligible for the public past-activities catalog.
// @Tags xiangwan-admin
// @Accept json
// @Produce json
// @Param instance_id path string true "Instance ID"
// @Param Idempotency-Key header string true "Canonical lowercase UUIDv4 operation key"
// @Param request body PublishAdminInstanceRequest true "Expected Instance version"
// @Success 200 {object} AdminInstanceResponse
// @Failure 400 {object} response.Body
// @Failure 409 {object} response.Body
// @Failure 500 {object} response.Body
// @Router /xiangwan/admin/instances/{instance_id}/completion [post]
func (handler *AdminCatalogHandler) CompleteInstance(c *gin.Context) {
	principal, ok := handler.principal(c)
	if !ok || !validAdminWriteRequest(c) {
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
	var payload PublishAdminInstanceRequest
	if err := decodeAdminCatalogJSON(c, &payload); err != nil {
		writeError(c, err)
		return
	}
	value, err := handler.catalog.CompleteInstance(c.Request.Context(),
		xiangwanadmin.CompleteInstanceCommand{
			ActorID: principal.PrincipalID, IdentityLinkID: principal.IdentityLinkID,
			OperationID: operationID,
			RequestID:   adminRequestID(c), InstanceID: instanceID,
			ExpectedInstanceVersion: payload.ExpectedInstanceVersion,
		})
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	response.OK(c, projectAdminInstance(value))
}

// PreviewInstanceCancellation godoc
// @Summary Preview taking a published Xiangwan Instance offline
// @Description ADMIN OP-KEY command. Creates a short-lived impact snapshot; no activity, registration, hold, order or refund fact changes until the follow-up cancellation command succeeds.
// @Tags xiangwan-admin
// @Accept json
// @Produce json
// @Param instance_id path string true "Instance ID"
// @Param Idempotency-Key header string true "Canonical lowercase UUIDv4 operation key"
// @Param request body AdminInstanceCancellationPreviewRequest true "Cancellation reason"
// @Success 200 {object} AdminInstanceCancellationPreviewResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 404 {object} response.Body
// @Failure 409 {object} response.Body
// @Router /xiangwan/admin/instances/{instance_id}/cancellation-previews [post]
func (handler *AdminCatalogHandler) PreviewInstanceCancellation(c *gin.Context) {
	principal, ok := handler.principal(c)
	if !ok || !validAdminWriteRequest(c) || handler.cancellation == nil {
		if ok && handler.cancellation == nil {
			writeError(c, errx.NewInternal("administrator cancellation is unavailable"))
		}
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
	var payload AdminInstanceCancellationPreviewRequest
	if err := decodeAdminCatalogJSON(c, &payload); err != nil {
		writeError(c, err)
		return
	}
	value, err := handler.cancellation.PreviewInstanceCancellation(c.Request.Context(), xiangwanadmin.PreviewInstanceCancellationCommand{
		ActorID: principal.PrincipalID, IdentityLinkID: principal.IdentityLinkID,
		OperationID: operationID, RequestID: adminRequestID(c), InstanceID: instanceID,
		Reason: strings.TrimSpace(payload.Reason),
	})
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	response.OK(c, projectAdminInstanceCancellationPreview(value))
}

// CancelInstance godoc
// @Summary Take a published Xiangwan Instance offline
// @Description ADMIN OP-KEY command. Consumes the exact preview and atomically closes sessions, releases holds, cancels registrations and records required refund/coupon follow-up facts.
// @Tags xiangwan-admin
// @Accept json
// @Produce json
// @Param instance_id path string true "Instance ID"
// @Param Idempotency-Key header string true "Canonical lowercase UUIDv4 operation key"
// @Param request body AdminInstanceCancellationRequest true "Cancellation confirmation"
// @Success 200 {object} AdminInstanceCancellationResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 404 {object} response.Body
// @Failure 409 {object} response.Body
// @Router /xiangwan/admin/instances/{instance_id}/cancellation [post]
func (handler *AdminCatalogHandler) CancelInstance(c *gin.Context) {
	principal, ok := handler.principal(c)
	if !ok || !validAdminWriteRequest(c) || handler.cancellation == nil {
		if ok && handler.cancellation == nil {
			writeError(c, errx.NewInternal("administrator cancellation is unavailable"))
		}
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
	var payload AdminInstanceCancellationRequest
	if err := decodeAdminCatalogJSON(c, &payload); err != nil {
		writeError(c, err)
		return
	}
	previewID, err := parseCanonicalUUID(payload.PreviewID)
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid cancellation preview id"))
		return
	}
	value, err := handler.cancellation.CancelInstance(c.Request.Context(), xiangwanadmin.CancelInstanceCommand{
		ActorID: principal.PrincipalID, IdentityLinkID: principal.IdentityLinkID,
		OperationID: operationID, RequestID: adminRequestID(c), InstanceID: instanceID,
		PreviewID: previewID, ExpectedInstanceVersion: payload.ExpectedInstanceVersion,
		Reason: strings.TrimSpace(payload.Reason),
	})
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	response.OK(c, AdminInstanceCancellationResponse{
		Instance:                 projectAdminInstance(value.Instance),
		ReceiptID:                value.Receipt.ID.String(),
		CancelledAt:              value.Receipt.CancelledAt.UTC().Format(time.RFC3339Nano),
		ResultingInstanceVersion: value.Receipt.ResultingInstanceVersion,
	})
}

// ListRegistrations godoc
// @Summary List masked Xiangwan Registrations
// @Description Reads only Registration rows covered by the live activity or onsite Grant and returns masked contact fields.
// @Tags xiangwan-admin
// @Produce json
// @Param series_id query string false "Exact Series ID"
// @Param instance_id query string false "Exact Instance ID"
// @Param session_id query string false "Exact Session ID"
// @Param participation_status query string false "Participation status" Enums(pending_payment, confirmed, cancelled)
// @Param page query int false "Page number" minimum(1)
// @Param page_size query int false "Page size" minimum(1) maximum(100)
// @Success 200 {object} AdminRegistrationPageResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Router /xiangwan/admin/registrations [get]
func (handler *AdminCatalogHandler) ListRegistrations(c *gin.Context) {
	principal, ok := handler.principal(c)
	if !ok || !validAdminQuery(
		c, "series_id", "instance_id", "session_id", "participation_status",
		"page", "page_size",
	) {
		return
	}
	page, pageSize, ok := parseAdminPage(c)
	if !ok {
		return
	}
	seriesID, ok := parseOptionalAdminUUID(c, "series_id")
	if !ok {
		return
	}
	instanceID, ok := parseOptionalAdminUUID(c, "instance_id")
	if !ok {
		return
	}
	sessionID, ok := parseOptionalAdminUUID(c, "session_id")
	if !ok {
		return
	}
	result, err := handler.catalog.ListRegistrations(
		c.Request.Context(), principal,
		xiangwanadmin.RegistrationFilter{
			SeriesID: seriesID, InstanceID: instanceID, SessionID: sessionID,
			ParticipationState: c.Query("participation_status"),
			Page:               page, PageSize: pageSize,
		},
	)
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	items := make([]AdminRegistrationResponse, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, projectAdminRegistration(item))
	}
	response.OK(c, AdminRegistrationPageResponse{
		Items: items, Page: result.Page, PageSize: result.PageSize, Total: result.Total,
	})
}

// GetRegistration godoc
// @Summary Read one Xiangwan Registration with audited operator-only contact details
// @Description Contacts are masked by default. A live tenant activity operator can explicitly request contact_purpose; both the purpose and authorization result are audited without contact values.
// @Tags xiangwan-admin
// @Produce json
// @Param registration_id path string true "Registration ID"
// @Param contact_purpose query string false "Purpose for temporary full contact access" Enums(activity_coordination, onsite_verification)
// @Success 200 {object} AdminRegistrationDetailResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 404 {object} response.Body
// @Router /xiangwan/admin/registrations/{registration_id} [get]
func (handler *AdminCatalogHandler) GetRegistration(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	principal, ok := handler.principal(c)
	if !ok || !validAdminQuery(c, "contact_purpose") {
		return
	}
	registrationID, err := parseCanonicalUUID(c.Param("registration_id"))
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid Registration id"))
		return
	}
	value, err := handler.catalog.GetRegistration(
		c.Request.Context(), principal, registrationID, c.Query("contact_purpose"),
	)
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	response.OK(c, projectAdminRegistrationDetail(value))
}

// ListCheckinTargets godoc
// @Summary List authorized Xiangwan Checkin targets
// @Description Returns non-sensitive Session labels covered by a live activity or onsite Grant and exposes no participant identity.
// @Tags xiangwan-admin
// @Produce json
// @Param page query int false "Page number" minimum(1)
// @Param page_size query int false "Page size" minimum(1) maximum(100)
// @Success 200 {object} AdminCheckinTargetPageResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Router /xiangwan/admin/checkin-targets [get]
func (handler *AdminCatalogHandler) ListCheckinTargets(c *gin.Context) {
	principal, ok := handler.principal(c)
	if !ok || !validAdminQuery(c, "page", "page_size") {
		return
	}
	page, pageSize, ok := parseAdminPage(c)
	if !ok {
		return
	}
	result, err := handler.catalog.ListCheckinTargets(
		c.Request.Context(), principal, page, pageSize,
	)
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	items := make([]AdminCheckinTargetResponse, 0, len(result.Items))
	for _, value := range result.Items {
		items = append(items, AdminCheckinTargetResponse{
			SeriesID: value.SeriesID.String(), InstanceID: value.InstanceID.String(),
			SessionID: value.SessionID.String(), SeriesTitle: value.SeriesTitle,
			InstanceTitle: value.InstanceTitle, SessionTitle: value.SessionTitle,
			SessionStartAt:             value.SessionStartAt.UTC().Format(time.RFC3339Nano),
			VenueName:                  value.VenueName,
			ConfirmedRegistrationCount: value.ConfirmedRegistrationCount,
			CheckedInRegistrationCount: value.CheckedInRegistrationCount,
			Capacity:                   value.Capacity,
		})
	}
	response.OK(c, AdminCheckinTargetPageResponse{
		Items: items, Page: result.Page, PageSize: result.PageSize, Total: result.Total,
	})
}

func (handler *AdminCatalogHandler) principal(c *gin.Context) (
	xiangwanadmin.Principal,
	bool,
) {
	if handler == nil || (handler.catalog == nil && handler.hostApplications == nil) {
		writeError(c, errx.NewInternal("administrator catalog is unavailable"))
		return xiangwanadmin.Principal{}, false
	}
	principal, err := AdminPrincipal(c)
	if err != nil {
		writeAdminError(c, err)
		return xiangwanadmin.Principal{}, false
	}
	return principal, true
}

func (handler *AdminCatalogHandler) requireQuestionnaireTemplates(c *gin.Context) bool {
	if handler != nil && handler.templates != nil {
		return true
	}
	writeError(c, errx.NewInternal("questionnaire template catalog is unavailable"))
	return false
}

func questionnaireFieldsFromRequests(values []AdminQuestionnaireFieldRequest) []activity.QuestionnaireField {
	fields := make([]activity.QuestionnaireField, 0, len(values))
	for _, field := range values {
		options := make([]activity.QuestionnaireOption, 0, len(field.Options))
		for _, option := range field.Options {
			options = append(options, activity.QuestionnaireOption{Code: option.Code, Label: option.Label})
		}
		fields = append(fields, activity.QuestionnaireField{
			Code: field.Code, Type: field.Type, Label: field.Label, HelpText: field.HelpText,
			Required: field.Required, SortOrder: field.SortOrder, MinLength: field.MinLength,
			MaxLength: field.MaxLength, MaxSelections: field.MaxSelections, Options: options,
		})
	}
	return fields
}

type CreateAdminSeriesRequest struct {
	Title       string `json:"title"`
	HomeVisible *bool  `json:"home_visible"`
}

type AdminBrandQuickTagRequest struct {
	Code  string `json:"code"`
	Label string `json:"label"`
}

type PublishAdminBrandProfileRequest struct {
	ExpectedVersion int64                       `json:"expected_version"`
	CommunityName   string                      `json:"community_name"`
	BrandIntro      string                      `json:"brand_intro"`
	HeroMode        activity.HomeHeroMode       `json:"hero_mode"`
	HeroEyebrow     string                      `json:"hero_eyebrow"`
	HeroSubtitle    string                      `json:"hero_subtitle"`
	HeroImageURL    string                      `json:"hero_image_url"`
	HeroImageAlt    string                      `json:"hero_image_alt"`
	QuickTags       []AdminBrandQuickTagRequest `json:"quick_tags"`
}

type AdminBrandProfileResponse struct {
	Configured         bool                          `json:"configured"`
	LifecycleStatus    activity.BrandLifecycleStatus `json:"lifecycle_status,omitempty"`
	PublicationVersion int64                         `json:"publication_version"`
	Version            int64                         `json:"version"`
	CommunityName      string                        `json:"community_name"`
	BrandIntro         string                        `json:"brand_intro"`
	HeroMode           activity.HomeHeroMode         `json:"hero_mode"`
	HeroEyebrow        string                        `json:"hero_eyebrow"`
	HeroSubtitle       string                        `json:"hero_subtitle"`
	HeroImageURL       string                        `json:"hero_image_url"`
	HeroImageAlt       string                        `json:"hero_image_alt"`
	QuickTags          []PublicHomeQuickTagResponse  `json:"quick_tags"`
	PublishedAt        *string                       `json:"published_at,omitempty"`
	UpdatedAt          *string                       `json:"updated_at,omitempty"`
}

type CreateAdminInstanceRequest struct {
	SeriesID              string                 `json:"series_id"`
	ExpectedSeriesVersion int64                  `json:"expected_series_version"`
	IssueNo               *int                   `json:"issue_no,omitempty"`
	Title                 string                 `json:"title"`
	ActivityType          activity.ActivityType  `json:"activity_type"`
	QuickTagCodes         []string               `json:"quick_tag_codes"`
	CoverImageURL         string                 `json:"cover_image_url"`
	DetailBlocks          []activity.DetailBlock `json:"detail_blocks"`
}

type CopyAdminInstanceRequest struct {
	CreateAdminInstanceRequest
	ExpectedSourceVersion                int64  `json:"expected_source_version"`
	ExpectedSourcePresentationRevision   int64  `json:"expected_source_presentation_revision"`
	ExpectedSourceQuestionnaireVersionID string `json:"expected_source_questionnaire_version_id,omitempty"`
}

type AdminQuestionnaireOptionRequest struct {
	Code  string `json:"code"`
	Label string `json:"label"`
}

type AdminQuestionnaireFieldRequest struct {
	Code          string                            `json:"code"`
	Type          activity.QuestionnaireFieldType   `json:"type"`
	Label         string                            `json:"label"`
	HelpText      string                            `json:"help_text"`
	Required      bool                              `json:"required"`
	SortOrder     int                               `json:"sort_order"`
	MinLength     *int                              `json:"min_length"`
	MaxLength     *int                              `json:"max_length"`
	MaxSelections *int                              `json:"max_selections"`
	Options       []AdminQuestionnaireOptionRequest `json:"options"`
}

type PublishAdminInstanceQuestionnaireRequest struct {
	PrivacyPurpose       string                           `json:"privacy_purpose"`
	PrivacyPolicyVersion string                           `json:"privacy_policy_version"`
	Fields               []AdminQuestionnaireFieldRequest `json:"fields"`
}

type CreateAdminQuestionnaireTemplateRequest struct {
	Name                 string                           `json:"name"`
	Description          string                           `json:"description"`
	PrivacyPurpose       string                           `json:"privacy_purpose"`
	PrivacyPolicyVersion string                           `json:"privacy_policy_version"`
	Fields               []AdminQuestionnaireFieldRequest `json:"fields"`
}

type UpdateAdminQuestionnaireTemplateRequest struct {
	ExpectedVersion      int64                            `json:"expected_version"`
	Name                 string                           `json:"name"`
	Description          string                           `json:"description"`
	PrivacyPurpose       string                           `json:"privacy_purpose"`
	PrivacyPolicyVersion string                           `json:"privacy_policy_version"`
	Fields               []AdminQuestionnaireFieldRequest `json:"fields"`
}

type ArchiveAdminQuestionnaireTemplateRequest struct {
	ExpectedVersion int64 `json:"expected_version"`
}

type AssignAdminQuestionnaireTemplateRequest struct {
	InstanceID              string `json:"instance_id"`
	ExpectedInstanceVersion int64  `json:"expected_instance_version"`
}

type AssignAdminQuestionnaireTemplateToInstanceRequest struct {
	TemplateID              string `json:"template_id"`
	ExpectedInstanceVersion int64  `json:"expected_instance_version"`
}

// UpdateAdminInstanceRequest patches presentational Instance fields. An absent
// or null field stays unchanged; detail_blocks, when present, replaces the
// whole list. The concurrency fence is the dedicated presentation_revision
// (returned as presentation_revision), not the operational version, so a
// cosmetic edit never invalidates published review resources.
type UpdateAdminInstanceRequest struct {
	ExpectedPresentationRevision int64                   `json:"expected_presentation_revision"`
	CoverImageURL                *string                 `json:"cover_image_url"`
	DetailBlocks                 *[]activity.DetailBlock `json:"detail_blocks"`
}

type CreateAdminSessionRequest struct {
	ExpectedInstanceVersion int64                 `json:"expected_instance_version"`
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

type PublishAdminInstanceRequest struct {
	ExpectedInstanceVersion int64 `json:"expected_instance_version"`
}

type ScheduleAdminInstancePublicationRequest struct {
	ExpectedInstanceVersion int64  `json:"expected_instance_version"`
	ScheduledAt             string `json:"scheduled_at"`
}

type AdminInstanceCancellationPreviewRequest struct {
	Reason string `json:"reason"`
}

type AdminInstanceCancellationRequest struct {
	PreviewID               string `json:"preview_id"`
	ExpectedInstanceVersion int64  `json:"expected_instance_version"`
	Reason                  string `json:"reason"`
}

type AdminSeriesResponse struct {
	ID                 string  `json:"id"`
	Title              string  `json:"title"`
	Status             string  `json:"status"`
	IsRecurring        bool    `json:"is_recurring"`
	HomeVisible        bool    `json:"home_visible"`
	PublishedInstances int     `json:"published_instances"`
	FavoriteCount      int64   `json:"favorite_count"`
	RegistrationCount  int64   `json:"registration_count"`
	CurrentInstanceID  *string `json:"current_instance_id,omitempty"`
	Version            int64   `json:"version"`
	UpdatedAt          string  `json:"updated_at"`
}

type AdminSeriesPageResponse struct {
	Items    []AdminSeriesResponse `json:"items"`
	Page     int                   `json:"page"`
	PageSize int                   `json:"page_size"`
	Total    int64                 `json:"total"`
}

type AdminInstanceResponse struct {
	ID                   string                 `json:"id"`
	SeriesID             string                 `json:"series_id"`
	IssueNo              int                    `json:"issue_no"`
	Title                string                 `json:"title"`
	Status               string                 `json:"status"`
	ActivityType         *string                `json:"activity_type,omitempty"`
	QuickTagCodes        []string               `json:"quick_tag_codes"`
	CoverImageURL        string                 `json:"cover_image_url"`
	DetailBlocks         []activity.DetailBlock `json:"detail_blocks"`
	PublicationVersion   int64                  `json:"publication_version"`
	PresentationRevision int64                  `json:"presentation_revision"`
	Version              int64                  `json:"version"`
	ScheduledAt          *string                `json:"scheduled_at,omitempty"`
	PublishedAt          *string                `json:"published_at,omitempty"`
	UpdatedAt            string                 `json:"updated_at"`
}

type AdminInstanceCopyResponse struct {
	AdminInstanceResponse
	SourceInstanceID             string `json:"source_instance_id"`
	SourceInstanceVersion        int64  `json:"source_instance_version"`
	SourcePresentationRevision   int64  `json:"source_presentation_revision"`
	SourceQuestionnaireVersionID string `json:"source_questionnaire_version_id,omitempty"`
}

type AdminInstanceCopyLineageResponse struct {
	SourceInstanceID             string `json:"source_instance_id"`
	SourceInstanceVersion        int64  `json:"source_instance_version"`
	SourcePresentationRevision   int64  `json:"source_presentation_revision"`
	SourceQuestionnaireVersionID string `json:"source_questionnaire_version_id,omitempty"`
}

type AdminInstanceListItemResponse struct {
	Instance     AdminInstanceResponse `json:"instance"`
	SeriesTitle  string                `json:"series_title"`
	SessionCount int                   `json:"session_count"`
}

type AdminInstancePageResponse struct {
	Items    []AdminInstanceListItemResponse `json:"items"`
	Page     int                             `json:"page"`
	PageSize int                             `json:"page_size"`
	Total    int64                           `json:"total"`
}

type AdminSessionResponse struct {
	ID                           string                 `json:"id"`
	InstanceID                   string                 `json:"instance_id"`
	Title                        string                 `json:"title"`
	Status                       string                 `json:"status"`
	RegistrationStartAt          *string                `json:"registration_start_at,omitempty"`
	RegistrationEndAt            *string                `json:"registration_end_at,omitempty"`
	SessionStartAt               *string                `json:"session_start_at,omitempty"`
	SessionEndAt                 *string                `json:"session_end_at,omitempty"`
	Capacity                     *int                   `json:"capacity,omitempty"`
	GroupMinimum                 *int                   `json:"group_minimum,omitempty"`
	LowStockThreshold            *int                   `json:"low_stock_threshold,omitempty"`
	PriceCents                   *int64                 `json:"price_cents,omitempty"`
	DeliveryMode                 *activity.DeliveryMode `json:"delivery_mode,omitempty"`
	Area                         *activity.AreaCode     `json:"area,omitempty"`
	VenueName                    *string                `json:"venue_name,omitempty"`
	Address                      *string                `json:"address,omitempty"`
	Longitude                    *float64               `json:"longitude,omitempty"`
	Latitude                     *float64               `json:"latitude,omitempty"`
	OnlineParticipationMode      *string                `json:"online_participation_mode,omitempty"`
	OnlineParticipationCompliant *bool                  `json:"online_participation_compliant,omitempty"`
	ConfirmedRegistrationCount   int                    `json:"confirmed_registration_count"`
	SortOrder                    int                    `json:"sort_order"`
	Version                      int64                  `json:"version"`
}

type AdminInstanceDetailResponse struct {
	Series        AdminSeriesResponse                `json:"series"`
	Instance      AdminInstanceResponse              `json:"instance"`
	Sessions      []AdminSessionResponse             `json:"sessions"`
	Questionnaire AdminInstanceQuestionnaireResponse `json:"questionnaire"`
	CopyLineage   *AdminInstanceCopyLineageResponse  `json:"copy_lineage,omitempty"`
}

type AdminInstanceReviewStatusResponse struct {
	InstanceID                  string                             `json:"instance_id"`
	InstanceStatus              activity.InstanceStatus            `json:"instance_status"`
	PublicReviewEligible        bool                               `json:"public_review_eligible"`
	PublicReviewAvailable       bool                               `json:"public_review_available"`
	InstanceReviewDocumentCount int                                `json:"instance_review_document_count"`
	PublicSessionResourceCount  int                                `json:"public_session_resource_count"`
	LatestPublishedAt           *string                            `json:"latest_published_at,omitempty"`
	Sessions                    []AdminSessionReviewStatusResponse `json:"sessions"`
}

type CreateAdminReviewResourceRequest struct {
	ReplacesRelationID           string                       `json:"replaces_relation_id,omitempty"`
	ExpectedPhotoCurationVersion *int64                       `json:"expected_photo_curation_version,omitempty"`
	ExpectedTargetVersion        int64                        `json:"expected_target_version"`
	SessionID                    string                       `json:"session_id"`
	Title                        string                       `json:"title"`
	Description                  string                       `json:"description"`
	VideoURL                     string                       `json:"video_url"`
	VideoChannel                 *resource.ReviewVideoChannel `json:"video_channel,omitempty"`
	Photos                       []string                     `json:"photos,omitempty"`
	Files                        []AdminReviewFileRequest     `json:"files,omitempty"`
	Recording                    *AdminReviewLinkRequest      `json:"recording,omitempty"`
	Materials                    *AdminReviewLinkRequest      `json:"materials,omitempty"`
	SortOrder                    int                          `json:"sort_order"`
}

type AdminReviewFileRequest struct {
	FileID   string `json:"file_id"`
	Kind     string `json:"kind"`
	SHA256   string `json:"sha256"`
	Reviewed bool   `json:"reviewed"`
}

// AdminReviewLinkRequest is intentionally limited to the two prototype
// resource rows. The resource writer applies the HTTPS domain allowlist and
// stores only the canonical URL in the immutable review snapshot.
type AdminReviewLinkRequest struct {
	Enabled  bool   `json:"enabled"`
	Title    string `json:"title"`
	Subtitle string `json:"subtitle"`
	URL      string `json:"url"`
}

type AdminReviewResourceResponse struct {
	RelationID    string  `json:"relation_id"`
	PublicationID string  `json:"publication_id"`
	ContentID     string  `json:"content_id"`
	TenantID      string  `json:"tenant_id"`
	InstanceID    string  `json:"instance_id"`
	SessionID     *string `json:"session_id,omitempty"`
	Title         string  `json:"title"`
	PublishedAt   string  `json:"published_at"`
}

type AdminSessionReviewStatusResponse struct {
	SessionID             string                 `json:"session_id"`
	Status                activity.SessionStatus `json:"status"`
	PublicResourceCount   int                    `json:"public_resource_count"`
	PublicReviewAvailable bool                   `json:"public_review_available"`
	LatestPublishedAt     *string                `json:"latest_published_at,omitempty"`
}

type AdminInstanceQuestionnaireResponse struct {
	Configured             bool                              `json:"configured"`
	QuestionnaireVersionID string                            `json:"questionnaire_version_id,omitempty"`
	InstanceID             string                            `json:"instance_id,omitempty"`
	Version                int64                             `json:"version,omitempty"`
	PrivacyPurpose         string                            `json:"privacy_purpose,omitempty"`
	PrivacyPolicyVersion   string                            `json:"privacy_policy_version,omitempty"`
	PublishedAt            string                            `json:"published_at,omitempty"`
	Fields                 []AdminQuestionnaireFieldResponse `json:"fields"`
}

type AdminQuestionnaireTemplateResponse struct {
	ID                   string                            `json:"id"`
	VersionID            string                            `json:"version_id"`
	Name                 string                            `json:"name"`
	Description          string                            `json:"description"`
	Version              int64                             `json:"version"`
	PrivacyPurpose       string                            `json:"privacy_purpose"`
	PrivacyPolicyVersion string                            `json:"privacy_policy_version"`
	Status               string                            `json:"status"`
	Fields               []AdminQuestionnaireFieldResponse `json:"fields"`
	CreatedAt            string                            `json:"created_at"`
	UpdatedAt            string                            `json:"updated_at"`
}

type AdminQuestionnaireTemplatePageResponse struct {
	Items    []AdminQuestionnaireTemplateResponse `json:"items"`
	Page     int                                  `json:"page"`
	PageSize int                                  `json:"page_size"`
	Total    int64                                `json:"total"`
}

type AdminQuestionnaireFieldResponse struct {
	FieldID       string                          `json:"field_id"`
	Code          string                          `json:"code"`
	Type          activity.QuestionnaireFieldType `json:"type"`
	Label         string                          `json:"label"`
	HelpText      string                          `json:"help_text"`
	Required      bool                            `json:"required"`
	SortOrder     int                             `json:"sort_order"`
	MinLength     *int                            `json:"min_length"`
	MaxLength     *int                            `json:"max_length"`
	MaxSelections *int                            `json:"max_selections"`
	Options       []QuestionnaireOptionResponse   `json:"options"`
}

type AdminPublicationResponse struct {
	ID                 string `json:"id"`
	InstanceID         string `json:"instance_id"`
	PublicationVersion int64  `json:"publication_version"`
	SessionCount       int    `json:"session_count"`
	PublishedAt        string `json:"published_at"`
}

type AdminScheduledPublicationResponse struct {
	ID                      string  `json:"id"`
	InstanceID              string  `json:"instance_id"`
	ExpectedInstanceVersion int64   `json:"expected_instance_version"`
	ScheduledAt             string  `json:"scheduled_at"`
	Status                  string  `json:"status"`
	AttemptCount            int     `json:"attempt_count"`
	LeaseExpiresAt          *string `json:"lease_expires_at,omitempty"`
	CompletedAt             *string `json:"completed_at,omitempty"`
	LastError               string  `json:"last_error,omitempty"`
	Version                 int64   `json:"version"`
}

type AdminScheduledPublicationPageResponse struct {
	Items []AdminScheduledPublicationResponse `json:"items"`
}

type AdminInstanceCancellationPreviewResponse struct {
	ID                           string                                    `json:"id"`
	InstanceID                   string                                    `json:"instance_id"`
	ExpectedInstanceVersion      int64                                     `json:"expected_instance_version"`
	SessionCount                 int                                       `json:"session_count"`
	TargetSessionCount           int                                       `json:"target_session_count"`
	AlreadyCancelledSessionCount int                                       `json:"already_cancelled_session_count"`
	CancelledRegistrationCount   int                                       `json:"cancelled_registration_count"`
	ConfirmedRegistrationCount   int                                       `json:"confirmed_registration_count"`
	ActiveHoldCount              int                                       `json:"active_hold_count"`
	FreeRegistrationCount        int                                       `json:"free_registration_count"`
	PaidRefundRegistrationCount  int                                       `json:"paid_refund_registration_count"`
	PendingOrderCount            int                                       `json:"pending_order_count"`
	UnknownPaymentCount          int                                       `json:"unknown_payment_count"`
	RefundCaseCount              int                                       `json:"refund_case_count"`
	RequestedRefundCents         int64                                     `json:"requested_refund_cents"`
	CouponAdjustmentCount        int                                       `json:"coupon_adjustment_count"`
	CancellationReason           string                                    `json:"cancellation_reason"`
	NotificationStrategy         activity.CancellationNotificationStrategy `json:"notification_strategy"`
	ExpiresAt                    string                                    `json:"expires_at"`
}

type AdminInstanceCancellationResponse struct {
	Instance                 AdminInstanceResponse `json:"instance"`
	ReceiptID                string                `json:"receipt_id"`
	CancelledAt              string                `json:"cancelled_at"`
	ResultingInstanceVersion int64                 `json:"resulting_instance_version"`
}

type AdminRegistrationResponse struct {
	ID                  string  `json:"id"`
	SeriesID            string  `json:"series_id"`
	InstanceID          string  `json:"instance_id"`
	SessionID           string  `json:"session_id"`
	InstanceTitle       string  `json:"instance_title"`
	SessionTitle        string  `json:"session_title"`
	ContactName         string  `json:"contact_name"`
	ContactPhone        string  `json:"contact_phone"`
	ParticipationStatus string  `json:"participation_status"`
	PaymentStatus       *string `json:"payment_status,omitempty"`
	RefundStatus        *string `json:"refund_status,omitempty"`
	CheckinStatus       string  `json:"checkin_status"`
	CreatedAt           string  `json:"created_at"`
	UpdatedAt           string  `json:"updated_at"`
}

type AdminRegistrationPageResponse struct {
	Items    []AdminRegistrationResponse `json:"items"`
	Page     int                         `json:"page"`
	PageSize int                         `json:"page_size"`
	Total    int64                       `json:"total"`
}

type AdminRegistrationDetailResponse struct {
	AdminRegistrationResponse
	Contact                    *AdminRegistrationContactResponse `json:"contact,omitempty"`
	PriceCents                 *int64                            `json:"price_cents,omitempty"`
	InstancePublicationVersion int64                             `json:"instance_publication_version"`
	SessionVersion             int64                             `json:"session_version"`
	ConfirmedAt                *string                           `json:"confirmed_at,omitempty"`
	CancelledAt                *string                           `json:"cancelled_at,omitempty"`
	CancellationReason         string                            `json:"cancellation_reason,omitempty"`
	CheckinID                  *string                           `json:"checkin_id,omitempty"`
	CheckedInAt                *string                           `json:"checked_in_at,omitempty"`
	SuccessfulRefundCents      *int64                            `json:"successful_refund_cents,omitempty"`
	PrivacyPolicyVersion       string                            `json:"privacy_policy_version"`
}

type AdminRegistrationContactResponse struct {
	Name  string `json:"name"`
	Phone string `json:"phone"`
}

type AdminCheckinTargetResponse struct {
	SeriesID                   string `json:"series_id"`
	InstanceID                 string `json:"instance_id"`
	SessionID                  string `json:"session_id"`
	SeriesTitle                string `json:"series_title"`
	InstanceTitle              string `json:"instance_title"`
	SessionTitle               string `json:"session_title"`
	SessionStartAt             string `json:"session_start_at"`
	VenueName                  string `json:"venue_name"`
	ConfirmedRegistrationCount int    `json:"confirmed_registration_count"`
	CheckedInRegistrationCount int    `json:"checked_in_registration_count"`
	Capacity                   int    `json:"capacity"`
}

type AdminCheckinTargetPageResponse struct {
	Items    []AdminCheckinTargetResponse `json:"items"`
	Page     int                          `json:"page"`
	PageSize int                          `json:"page_size"`
	Total    int64                        `json:"total"`
}

func projectAdminSeries(value activity.Series) AdminSeriesResponse {
	result := AdminSeriesResponse{
		ID: value.ID.String(), Title: value.Title, Status: string(value.Status),
		IsRecurring: value.IsRecurring, HomeVisible: value.HomeVisible,
		PublishedInstances: value.SuccessfulPublishedInstanceCount,
		FavoriteCount:      value.FavoriteCount,
		RegistrationCount:  value.HistoricalRegistrationCount,
		Version:            value.Version, UpdatedAt: value.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
	if value.CurrentPublicInstanceID != nil {
		id := value.CurrentPublicInstanceID.String()
		result.CurrentInstanceID = &id
	}
	return result
}

func projectAdminBrandProfile(value xiangwanadmin.BrandProfile) AdminBrandProfileResponse {
	quickTags := make([]PublicHomeQuickTagResponse, 0, len(value.QuickTags))
	for _, tag := range value.QuickTags {
		quickTags = append(quickTags, PublicHomeQuickTagResponse{Code: tag.Code, Label: tag.Label})
	}
	return AdminBrandProfileResponse{
		Configured: value.Configured, LifecycleStatus: value.LifecycleStatus,
		PublicationVersion: value.PublicationVersion, Version: value.Version,
		CommunityName: value.CommunityName, BrandIntro: value.BrandIntro,
		HeroMode: value.HeroMode, HeroEyebrow: value.HeroEyebrow,
		HeroSubtitle: value.HeroSubtitle, HeroImageURL: value.HeroImageURL,
		HeroImageAlt: value.HeroImageAlt, QuickTags: quickTags,
		PublishedAt: formatOptionalAdminTime(value.PublishedAt),
		UpdatedAt:   formatOptionalAdminTime(value.UpdatedAt),
	}
}

func projectAdminInstance(value activity.Instance) AdminInstanceResponse {
	result := AdminInstanceResponse{
		ID: value.ID.String(), SeriesID: value.SeriesID.String(), IssueNo: value.IssueNo, Title: value.Title,
		Status:               string(value.Status),
		QuickTagCodes:        append([]string(nil), value.QuickTagCodes...),
		CoverImageURL:        value.CoverImageURL,
		DetailBlocks:         append([]activity.DetailBlock(nil), value.DetailBlocks...),
		PublicationVersion:   value.PublicationVersion,
		PresentationRevision: value.PresentationRevision,
		Version:              value.Version,
		UpdatedAt:            value.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
	if result.QuickTagCodes == nil {
		result.QuickTagCodes = []string{}
	}
	if result.DetailBlocks == nil {
		result.DetailBlocks = []activity.DetailBlock{}
	}
	if value.ActivityType != nil {
		activityType := string(*value.ActivityType)
		result.ActivityType = &activityType
	}
	result.PublishedAt = formatOptionalAdminTime(value.PublishedAt)
	result.ScheduledAt = formatOptionalAdminTime(value.ScheduledAt)
	return result
}

func projectAdminScheduledPublication(value xiangwanadmin.ScheduledPublication) AdminScheduledPublicationResponse {
	return AdminScheduledPublicationResponse{
		ID: value.ID.String(), InstanceID: value.InstanceID.String(),
		ExpectedInstanceVersion: value.ExpectedInstanceVersion,
		ScheduledAt:             value.ScheduledAt.UTC().Format(time.RFC3339Nano),
		Status:                  value.Status, AttemptCount: value.AttemptCount,
		LeaseExpiresAt: formatOptionalAdminTime(value.LeaseExpiresAt),
		CompletedAt:    formatOptionalAdminTime(value.CompletedAt), LastError: value.LastError, Version: value.Version,
	}
}

func projectAdminInstanceReviewStatus(
	value xiangwanadmin.InstanceReviewStatus,
) AdminInstanceReviewStatusResponse {
	result := AdminInstanceReviewStatusResponse{
		InstanceID:                  value.InstanceID.String(),
		InstanceStatus:              value.InstanceStatus,
		PublicReviewEligible:        value.PublicReviewEligible,
		PublicReviewAvailable:       value.PublicReviewAvailable,
		InstanceReviewDocumentCount: value.InstanceReviewDocumentCount,
		PublicSessionResourceCount:  value.PublicSessionResourceCount,
		LatestPublishedAt:           formatOptionalAdminTime(value.LatestPublishedAt),
		Sessions:                    make([]AdminSessionReviewStatusResponse, 0, len(value.Sessions)),
	}
	for _, session := range value.Sessions {
		result.Sessions = append(result.Sessions, AdminSessionReviewStatusResponse{
			SessionID:             session.SessionID.String(),
			Status:                session.Status,
			PublicResourceCount:   session.PublicResourceCount,
			PublicReviewAvailable: session.PublicReviewAvailable,
			LatestPublishedAt:     formatOptionalAdminTime(session.LatestPublishedAt),
		})
	}
	return result
}

func projectAdminReviewResource(
	value resource.ReviewResourceReceipt,
) AdminReviewResourceResponse {
	result := AdminReviewResourceResponse{
		RelationID:    value.RelationID.String(),
		PublicationID: value.PublicationID.String(),
		ContentID:     value.ContentID.String(),
		TenantID:      value.TenantID.String(),
		InstanceID:    value.InstanceID.String(),
		Title:         value.Title,
		PublishedAt:   value.PublishedAt.UTC().Format(time.RFC3339Nano),
	}
	if value.SessionID != nil {
		sessionID := value.SessionID.String()
		result.SessionID = &sessionID
	}
	return result
}

func projectAdminInstanceCancellationPreview(
	value activity.InstanceCancellationPreview,
) AdminInstanceCancellationPreviewResponse {
	return AdminInstanceCancellationPreviewResponse{
		ID: value.ID.String(), InstanceID: value.InstanceID.String(),
		ExpectedInstanceVersion: value.ExpectedInstanceVersion,
		SessionCount:            value.SessionCount, TargetSessionCount: value.TargetSessionCount,
		AlreadyCancelledSessionCount: value.AlreadyCancelledSessionCount,
		CancelledRegistrationCount:   value.CancelledRegistrationCount,
		ConfirmedRegistrationCount:   value.ConfirmedRegistrationCount,
		ActiveHoldCount:              value.ActiveHoldCount, FreeRegistrationCount: value.FreeRegistrationCount,
		PaidRefundRegistrationCount: value.PaidRefundRegistrationCount,
		PendingOrderCount:           value.PendingOrderCount, UnknownPaymentCount: value.UnknownPaymentCount,
		RefundCaseCount: value.RefundCaseCount, RequestedRefundCents: value.RequestedRefundCents,
		CouponAdjustmentCount: value.CouponAdjustmentCount,
		CancellationReason:    value.CancellationReason,
		NotificationStrategy:  value.NotificationStrategy,
		ExpiresAt:             value.ExpiresAt.UTC().Format(time.RFC3339Nano),
	}
}

func projectAdminQuestionnaire(
	value *xiangwanadmin.InstanceQuestionnaire,
) AdminInstanceQuestionnaireResponse {
	result := AdminInstanceQuestionnaireResponse{
		Fields: make([]AdminQuestionnaireFieldResponse, 0),
	}
	if value == nil {
		return result
	}
	result.Configured = true
	result.QuestionnaireVersionID = value.QuestionnaireVersionID.String()
	result.InstanceID = value.InstanceID.String()
	result.Version = value.Version
	result.PrivacyPurpose = value.PrivacyPurpose
	result.PrivacyPolicyVersion = value.PrivacyPolicyVersion
	result.PublishedAt = value.PublishedAt.UTC().Format(time.RFC3339Nano)
	result.Fields = make([]AdminQuestionnaireFieldResponse, 0, len(value.Fields))
	for _, field := range value.Fields {
		options := make([]QuestionnaireOptionResponse, 0, len(field.Options))
		for _, option := range field.Options {
			options = append(options, QuestionnaireOptionResponse{
				Code: option.Code, Label: option.Label,
			})
		}
		result.Fields = append(result.Fields, AdminQuestionnaireFieldResponse{
			FieldID: field.FieldID.String(), Code: field.Code, Type: field.Type,
			Label: field.Label, HelpText: field.HelpText, Required: field.Required,
			SortOrder: field.SortOrder, MinLength: cloneQuestionnaireResponseInt(field.MinLength),
			MaxLength:     cloneQuestionnaireResponseInt(field.MaxLength),
			MaxSelections: cloneQuestionnaireResponseInt(field.MaxSelections), Options: options,
		})
	}
	return result
}

func projectAdminQuestionnaireTemplate(
	value xiangwanadmin.QuestionnaireTemplate,
) AdminQuestionnaireTemplateResponse {
	fields := make([]AdminQuestionnaireFieldResponse, 0, len(value.Fields))
	for _, field := range value.Fields {
		options := make([]QuestionnaireOptionResponse, 0, len(field.Options))
		for _, option := range field.Options {
			options = append(options, QuestionnaireOptionResponse{Code: option.Code, Label: option.Label})
		}
		fields = append(fields, AdminQuestionnaireFieldResponse{
			FieldID: field.FieldID.String(), Code: field.Code, Type: field.Type,
			Label: field.Label, HelpText: field.HelpText, Required: field.Required,
			SortOrder: field.SortOrder, MinLength: cloneQuestionnaireResponseInt(field.MinLength),
			MaxLength:     cloneQuestionnaireResponseInt(field.MaxLength),
			MaxSelections: cloneQuestionnaireResponseInt(field.MaxSelections), Options: options,
		})
	}
	return AdminQuestionnaireTemplateResponse{
		ID: value.ID.String(), VersionID: value.VersionID.String(), Name: value.Name,
		Description: value.Description, Version: value.Version,
		PrivacyPurpose: value.PrivacyPurpose, PrivacyPolicyVersion: value.PrivacyPolicyVersion,
		Status: value.Status, Fields: fields,
		CreatedAt: value.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt: value.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func projectAdminSession(value activity.Session) AdminSessionResponse {
	return AdminSessionResponse{
		ID: value.ID.String(), InstanceID: value.InstanceID.String(),
		Title: value.Title, Status: string(value.Status),
		RegistrationStartAt: formatOptionalAdminTime(value.RegistrationStartAt),
		RegistrationEndAt:   formatOptionalAdminTime(value.RegistrationEndAt),
		SessionStartAt:      formatOptionalAdminTime(value.SessionStartAt),
		SessionEndAt:        formatOptionalAdminTime(value.SessionEndAt),
		Capacity:            value.Capacity, GroupMinimum: value.GroupMinimum,
		LowStockThreshold: value.LowStockThreshold, PriceCents: value.PriceCents,
		DeliveryMode: value.DeliveryMode, Area: value.Area,
		VenueName: value.VenueName, Address: value.Address,
		Longitude: value.Longitude, Latitude: value.Latitude,
		OnlineParticipationMode:      value.OnlineParticipationMode,
		OnlineParticipationCompliant: value.OnlineParticipationCompliant,
		ConfirmedRegistrationCount:   value.ConfirmedRegistrationCount,
		SortOrder:                    value.SortOrder, Version: value.Version,
	}
}

func projectAdminRegistration(
	value xiangwanadmin.RegistrationItem,
) AdminRegistrationResponse {
	return AdminRegistrationResponse{
		ID: value.ID.String(), SeriesID: value.SeriesID.String(),
		InstanceID: value.InstanceID.String(), SessionID: value.SessionID.String(),
		InstanceTitle: value.InstanceTitle, SessionTitle: value.SessionTitle,
		ContactName: value.ContactNameMasked, ContactPhone: value.ContactPhoneMasked,
		ParticipationStatus: value.ParticipationStatus,
		PaymentStatus:       value.PaymentStatus, RefundStatus: value.RefundStatus,
		CheckinStatus: value.CheckinStatus,
		CreatedAt:     value.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:     value.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func projectAdminRegistrationDetail(
	value xiangwanadmin.RegistrationDetail,
) AdminRegistrationDetailResponse {
	result := AdminRegistrationDetailResponse{
		AdminRegistrationResponse:  projectAdminRegistration(value.RegistrationItem),
		PriceCents:                 value.PriceCents,
		InstancePublicationVersion: value.InstancePublicationVersion,
		SessionVersion:             value.SessionVersion,
		CancellationReason:         value.CancellationReason,
		SuccessfulRefundCents:      value.SuccessfulRefundCents,
		PrivacyPolicyVersion:       value.PrivacyPolicyVersion,
	}
	result.ConfirmedAt = formatOptionalAdminTime(value.ConfirmedAt)
	if value.Contact != nil {
		result.Contact = &AdminRegistrationContactResponse{
			Name: value.Contact.Name, Phone: value.Contact.Phone,
		}
	}
	result.CancelledAt = formatOptionalAdminTime(value.CancelledAt)
	result.CheckedInAt = formatOptionalAdminTime(value.CheckedInAt)
	if value.CheckinID != nil {
		id := value.CheckinID.String()
		result.CheckinID = &id
	}
	return result
}

func parseAdminSessionTimes(
	c *gin.Context,
	payload CreateAdminSessionRequest,
) (time.Time, time.Time, time.Time, time.Time, bool) {
	values := []string{
		payload.RegistrationStartAt, payload.RegistrationEndAt,
		payload.SessionStartAt, payload.SessionEndAt,
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

func parseAdminPage(c *gin.Context) (int, int, bool) {
	page, ok := parseOptionalAdminInt(c, "page")
	if !ok {
		return 0, 0, false
	}
	pageSize, ok := parseOptionalAdminInt(c, "page_size")
	if !ok {
		return 0, 0, false
	}
	return page, pageSize, true
}

func parseOptionalAdminInt(c *gin.Context, key string) (int, bool) {
	value := c.Query(key)
	if value == "" {
		return 0, true
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 {
		writeError(c, errx.NewBadRequest("invalid administrator pagination"))
		return 0, false
	}
	return parsed, true
}

func parseOptionalAdminUUID(c *gin.Context, key string) (*uuid.UUID, bool) {
	value := c.Query(key)
	if value == "" {
		return nil, true
	}
	parsed, err := parseCanonicalUUID(value)
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid administrator filter"))
		return nil, false
	}
	return &parsed, true
}

func validAdminQuery(c *gin.Context, allowed ...string) bool {
	want := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		want[key] = struct{}{}
	}
	for key, values := range c.Request.URL.Query() {
		if _, exists := want[key]; !exists || len(values) != 1 {
			writeError(c, errx.NewBadRequest("invalid administrator query"))
			return false
		}
	}
	return true
}

func validAdminWriteRequest(c *gin.Context) bool {
	if len(c.Request.URL.Query()) != 0 || c.ContentType() != "application/json" {
		writeError(c, errx.NewBadRequest("invalid administrator request"))
		return false
	}
	return true
}

func decodeAdminCatalogJSON(c *gin.Context, target any) error {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxAdminCatalogBodyBytes)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			return errx.New(errx.CodeFileTooLarge, "administrator request is too large")
		}
		return errx.NewBadRequest("invalid administrator request body")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errx.NewBadRequest("invalid administrator request body")
	}
	return nil
}

func writeAdminCatalogError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, xiangwanadmin.ErrInvalidCatalogRequest),
		errors.Is(err, activitypostgres.ErrInvalidPublicationCommand),
		errors.Is(err, activitypostgres.ErrInvalidInstanceCancellationPreviewCommand),
		errors.Is(err, activitypostgres.ErrInvalidInstanceCancellationCommand):
		writeError(c, errx.NewBadRequest("invalid administrator request"))
	case errors.Is(err, xiangwanadmin.ErrOperationConflict):
		writeError(c, errx.New(
			errx.CodeXiangwanAdminOperationConflict,
			"administrator operation conflicts with an earlier request",
		))
	case errors.Is(err, xiangwanadmin.ErrVersionConflict):
		writeError(c, errx.New(
			errx.CodeXiangwanAdminVersionConflict,
			"administrator data changed; refresh and retry",
		))
	case errors.Is(err, xiangwanadmin.ErrBrandQuickTagInUse),
		errors.Is(err, xiangwanadmin.ErrInstanceNotEnded),
		errors.Is(err, xiangwanadmin.ErrArchiveNotAllowed),
		errors.Is(err, xiangwanadmin.ErrPeopleProfileUnavailable):
		writeError(c, errx.NewBadRequest(err.Error()))
	case errors.Is(err, xiangwanadmin.ErrInstanceRoleConflict):
		writeError(c, errx.NewConflict("该人物已经绑定此活动角色"))
	case errors.Is(err, xiangwanadmin.ErrQuickTagNotPublished):
		writeError(c, errx.New(
			errx.CodeXiangwanAdminQuickTagUnavailable,
			"activity quick tag is not published in the current BrandProfile",
		))
	case errors.Is(err, activitypostgres.ErrInstanceCancellationPreviewConflict),
		errors.Is(err, activitypostgres.ErrInstanceCancellationConflict),
		errors.Is(err, activitypostgres.ErrSessionCancellationConflict),
		errors.Is(err, activitypostgres.ErrSessionCancellationPreviewConflict):
		writeError(c, errx.New(
			errx.CodeXiangwanAdminVersionConflict,
			"活动状态已变化，请刷新后重新确认",
		))
	case errors.Is(err, activitypostgres.ErrSessionCancellationCouponPolicy):
		// Coupon-settled registrations cannot be cancelled safely until the
		// runtime has a versioned restore/forfeit policy. Keep this a stable
		// conflict (the same client action as self-service cancellation), rather
		// than leaking the internal policy error as a generic 500.
		writeError(c, errx.NewConflict("活动取消优惠券策略暂不可用"))
	case errors.Is(err, activitypostgres.ErrInvalidSessionCancellationCommand),
		errors.Is(err, activitypostgres.ErrInvalidSessionCancellationPreviewCommand):
		writeError(c, errx.NewBadRequest("取消场次需要有效预览、版本和原因"))
	case errors.Is(err, activitypostgres.ErrInstanceCancellationTransaction),
		errors.Is(err, activitypostgres.ErrSessionCancellationTransaction):
		writeError(c, errx.NewInternal("activity cancellation could not be completed"))
	case errors.Is(err, xiangwanadmin.ErrPublicationInvalid):
		var invalid *activity.InvalidInstancePublicationError
		violations := []activity.FactViolation{}
		if errors.As(err, &invalid) {
			violations = append(violations, invalid.Violations...)
		}
		writeErrorWithData(c, errx.New(
			errx.CodeXiangwanAdminPublicationInvalid,
			"activity is not ready to publish",
		), gin.H{"violations": violations})
	case errors.Is(err, xiangwanadmin.ErrTargetNotFound),
		errors.Is(err, peoplepostgres.ErrRoleBindingNotFound),
		errors.Is(err, activitypostgres.ErrInstanceCancellationNotFound),
		errors.Is(err, activitypostgres.ErrSessionCancellationNotFound),
		errors.Is(err, activitypostgres.ErrSeriesNotFound),
		errors.Is(err, activitypostgres.ErrInstanceNotFound),
		errors.Is(err, activitypostgres.ErrSessionNotFound):
		writeError(c, errx.New(
			errx.CodeXiangwanAdminTargetNotFound,
			"administrator target was not found",
		))
	case errors.Is(err, xiangwanadmin.ErrSessionInvalid),
		errors.Is(err, xiangwanadmin.ErrScopeForbidden):
		writeAdminError(c, err)
	default:
		// Admin writes run in Serializable transactions: a concurrent writer
		// winning the same expected version surfaces as 40001/40P01, which
		// the caller experiences as the documented version conflict, not an
		// outage (codex review 2026-09-19).
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) {
			switch postgresError.Code {
			case "40001", "40P01":
				writeError(c, errx.New(
					errx.CodeXiangwanAdminVersionConflict,
					"administrator data changed; refresh and retry",
				))
				return
			case "23514":
				writeError(c, errx.NewBadRequest("invalid administrator request"))
				return
			}
		}
		writeError(c, err)
	}
}

func writeReviewResourceError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, resource.ErrInvalidReviewResourceCommand):
		writeError(c, errx.NewBadRequest("invalid review resource"))
	case errors.Is(err, resource.ErrReviewResourceExternalLink):
		writeError(c, errx.NewBadRequest("回顾资源外链未通过白名单校验"))
	case errors.Is(err, resource.ErrReviewResourceConflict):
		writeError(c, errx.NewConflict("活动回顾目标已变化，请刷新后重试"))
	case errors.Is(err, resource.ErrReviewResourceUnavailable):
		writeError(c, errx.NewInternal("review resource writer is unavailable"))
	case errors.Is(err, xiangwanadmin.ErrScopeForbidden):
		writeError(c, errx.New(
			errx.CodeXiangwanAdminScopeForbidden,
			"administrator scope is forbidden",
		))
	case errors.Is(err, xiangwanadmin.ErrVersionConflict):
		writeError(c, errx.New(
			errx.CodeXiangwanAdminVersionConflict,
			"administrator data changed; refresh and retry",
		))
	default:
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) {
			switch postgresError.Code {
			case "40001", "40P01":
				writeError(c, errx.NewConflict("活动回顾目标已变化，请刷新后重试"))
				return
			case "23514", "23503":
				writeError(c, errx.NewBadRequest("活动回顾内容或目标无效"))
				return
			}
		}
		writeError(c, err)
	}
}

func formatOptionalAdminTime(value *time.Time) *string {
	if value == nil {
		return nil
	}
	formatted := value.UTC().Format(time.RFC3339Nano)
	return &formatted
}
