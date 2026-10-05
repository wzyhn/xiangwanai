package xiangwanruntime

import (
	"context"
	"net/http"
	"time"

	xiangwanapi "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/api"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/wzyhn/xiangwanai/internal/pkg/logx"
	"github.com/wzyhn/xiangwanai/internal/pkg/requestctx"
	"github.com/wzyhn/xiangwanai/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

const readinessTimeout = 2 * time.Second

type RouterDependencies struct {
	TenantID                  uuid.UUID
	GenerationID              uuid.UUID
	TrustedProxies            []string
	GenerationGate            GenerationGate
	PublicHome                *xiangwanapi.PublicHomeHandler
	PublicPastActivities      *xiangwanapi.PublicPastActivitiesHandler
	PublicSessions            *xiangwanapi.PublicSessionCollectionHandler
	PublicSession             *xiangwanapi.PublicSessionDetailHandler
	PublicReview              *xiangwanapi.PublicReviewHandler
	PublicMedia               *xiangwanapi.PublicMediaHandler
	PublicPeople              *xiangwanapi.PublicPeopleHandler
	PublicPolicies            *xiangwanapi.PublicPoliciesHandler
	BrandHero                 *xiangwanapi.BrandHeroHandler
	CoverImages               *xiangwanapi.CoverImageHandler
	WeChatLogin               *xiangwanapi.WeChatLoginHandler
	WeChatLoginThrottle       gin.HandlerFunc
	RequirePrincipal          gin.HandlerFunc
	Questionnaire             *xiangwanapi.SessionQuestionnaireHandler
	CreateRegistration        *xiangwanapi.CreateRegistrationHandler
	WeChatPrepay              *xiangwanapi.WeChatPrepayHandler
	WeChatPaymentQuery        *xiangwanapi.WeChatPaymentQueryHandler
	WeChatPaymentNotification *xiangwanapi.WeChatPaymentNotificationHandler
	MyRegistrations           *xiangwanapi.MyRegistrationsHandler
	MyRegistration            *xiangwanapi.MyRegistrationDetailHandler
	RegistrationCancellation  *xiangwanapi.RegistrationCancellationHandler
	CheckinCredential         *xiangwanapi.CheckinCredentialHandler
	MyOrders                  *xiangwanapi.MyOrdersHandler
	MyOrder                   *xiangwanapi.MyOrderDetailHandler
	MyCoupons                 *xiangwanapi.MyCouponsHandler
	MyFavorites               *xiangwanapi.MyFavoritesHandler
	DataRights                *xiangwanapi.DataRightsHandler
	ConsumerProfile           *xiangwanapi.ConsumerProfileHandler
	ConsumerIdentity          *xiangwanapi.ConsumerIdentityHandler
	MyBenefits                *xiangwanapi.MyBenefitsHandler
	HostApplicationSubmission *xiangwanapi.HostApplicationSubmissionHandler
	PeopleBinding             *xiangwanapi.PeopleBindingHandler
	SeriesFavorite            *xiangwanapi.SeriesFavoriteHandler
	AdminEnabled              bool
	AdminAuth                 *xiangwanapi.AdminAuthHandler
	AdminCatalog              *xiangwanapi.AdminCatalogHandler
	OnsiteCheckin             *xiangwanapi.OnsiteCheckinHandler
}

func NewRouter(dependencies RouterDependencies) (*gin.Engine, error) {
	if dependencies.TenantID == uuid.Nil ||
		dependencies.GenerationID == uuid.Nil ||
		dependencies.GenerationGate == nil ||
		dependencies.PublicHome == nil ||
		dependencies.PublicPastActivities == nil ||
		dependencies.PublicSessions == nil ||
		dependencies.PublicSession == nil || dependencies.PublicReview == nil ||
		dependencies.PublicMedia == nil || dependencies.PublicPeople == nil ||
		dependencies.PublicPolicies == nil ||
		dependencies.BrandHero == nil ||
		dependencies.CoverImages == nil ||
		dependencies.WeChatLogin == nil ||
		dependencies.WeChatLoginThrottle == nil ||
		dependencies.RequirePrincipal == nil ||
		dependencies.Questionnaire == nil ||
		dependencies.CreateRegistration == nil ||
		dependencies.WeChatPrepay == nil ||
		dependencies.WeChatPaymentQuery == nil ||
		dependencies.WeChatPaymentNotification == nil ||
		dependencies.MyRegistrations == nil || dependencies.MyRegistration == nil ||
		dependencies.RegistrationCancellation == nil ||
		dependencies.CheckinCredential == nil ||
		dependencies.MyOrders == nil || dependencies.MyOrder == nil ||
		dependencies.MyCoupons == nil || dependencies.MyFavorites == nil ||
		dependencies.DataRights == nil ||
		dependencies.ConsumerProfile == nil ||
		dependencies.ConsumerIdentity == nil ||
		dependencies.MyBenefits == nil ||
		dependencies.HostApplicationSubmission == nil ||
		dependencies.SeriesFavorite == nil ||
		(dependencies.AdminEnabled &&
			(dependencies.AdminAuth == nil || dependencies.AdminCatalog == nil ||
				dependencies.OnsiteCheckin == nil)) {
		return nil, ErrInvalidRuntimeConfig
	}
	engine := gin.New()
	if err := engine.SetTrustedProxies(dependencies.TrustedProxies); err != nil {
		return nil, ErrInvalidRuntimeConfig
	}
	engine.HandleMethodNotAllowed = true
	engine.Use(
		requestIDBoundary(),
		logx.GinAccessLogger(zap.L()),
		securityHeaders(),
		recoveryBoundary(),
	)
	engine.GET("/live", func(c *gin.Context) {
		response.OK(c, gin.H{"status": "live"})
	})
	engine.GET("/ready", readinessHandler(dependencies))
	engine.NoRoute(func(c *gin.Context) {
		response.Err(c, errx.NewNotFound("route not found"))
	})
	engine.NoMethod(func(c *gin.Context) {
		c.PureJSON(http.StatusMethodNotAllowed, response.Body{
			Code:    int(errx.CodeBadRequest),
			Message: "method not allowed",
		})
	})
	// Provider callbacks are authenticated cryptographically and intentionally
	// remain available during generation transitions so already-created
	// payments can converge to durable local facts.
	providerRoutes := engine.Group("/api/v1/xiangwan")
	dependencies.WeChatPaymentNotification.RegisterRoutes(providerRoutes)
	loginRoutes := engine.Group("/api/v1")
	loginRoutes.Use(
		requireActiveGeneration(dependencies),
		dependencies.WeChatLoginThrottle,
	)
	dependencies.WeChatLogin.RegisterRoutes(loginRoutes)
	publicRoutes := engine.Group("/api/v1/xiangwan")
	publicRoutes.Use(requireActiveGeneration(dependencies))
	dependencies.PublicHome.RegisterRoutes(publicRoutes)
	dependencies.PublicPastActivities.RegisterRoutes(publicRoutes)
	dependencies.PublicSessions.RegisterRoutes(publicRoutes)
	dependencies.PublicSession.RegisterRoutes(publicRoutes)
	dependencies.PublicReview.RegisterRoutes(publicRoutes)
	dependencies.PublicMedia.RegisterRoutes(publicRoutes)
	dependencies.PublicPeople.RegisterRoutes(publicRoutes)
	dependencies.PublicPolicies.RegisterRoutes(publicRoutes)
	dependencies.BrandHero.RegisterPublicRoutes(publicRoutes)
	dependencies.CoverImages.RegisterPublicRoutes(publicRoutes)
	dependencies.ConsumerIdentity.RegisterPublicRoutes(publicRoutes)
	adminAuth := dependencies.AdminAuth
	if adminAuth == nil {
		adminAuth = xiangwanapi.NewDisabledAdminAuthHandler()
	}
	adminRoutes := publicRoutes.Group("/admin")
	adminAuth.RegisterPublicRoutes(adminRoutes)
	if dependencies.AdminEnabled {
		protectedAdminRoutes := publicRoutes.Group("/admin")
		protectedAdminRoutes.Use(adminAuth.RequireSessionAndCSRF())
		adminAuth.RegisterProtectedRoutes(protectedAdminRoutes)
		dependencies.AdminCatalog.RegisterRoutes(protectedAdminRoutes)
		dependencies.BrandHero.RegisterAdminRoutes(protectedAdminRoutes)
		dependencies.CoverImages.RegisterAdminRoutes(protectedAdminRoutes)
		dependencies.OnsiteCheckin.RegisterRoutes(protectedAdminRoutes)
	}
	authenticatedRoutes := publicRoutes.Group("")
	authenticatedRoutes.Use(dependencies.RequirePrincipal)
	dependencies.Questionnaire.RegisterRoutes(authenticatedRoutes)
	dependencies.CreateRegistration.RegisterRoutes(authenticatedRoutes)
	dependencies.WeChatPrepay.RegisterRoutes(authenticatedRoutes)
	dependencies.WeChatPaymentQuery.RegisterRoutes(authenticatedRoutes)
	dependencies.MyRegistrations.RegisterRoutes(authenticatedRoutes)
	dependencies.MyRegistration.RegisterRoutes(authenticatedRoutes)
	dependencies.RegistrationCancellation.RegisterRoutes(authenticatedRoutes)
	dependencies.CheckinCredential.RegisterRoutes(authenticatedRoutes)
	dependencies.MyOrders.RegisterRoutes(authenticatedRoutes)
	dependencies.MyOrder.RegisterRoutes(authenticatedRoutes)
	dependencies.MyCoupons.RegisterRoutes(authenticatedRoutes)
	dependencies.MyFavorites.RegisterRoutes(authenticatedRoutes)
	dependencies.DataRights.RegisterRoutes(authenticatedRoutes)
	dependencies.ConsumerProfile.RegisterRoutes(authenticatedRoutes)
	dependencies.ConsumerIdentity.RegisterAuthenticatedRoutes(authenticatedRoutes)
	dependencies.MyBenefits.RegisterRoutes(authenticatedRoutes)
	dependencies.HostApplicationSubmission.RegisterRoutes(authenticatedRoutes)
	if dependencies.PeopleBinding != nil {
		dependencies.PeopleBinding.RegisterRoutes(authenticatedRoutes)
	}
	dependencies.SeriesFavorite.RegisterRoutes(authenticatedRoutes)
	return engine, nil
}

func readinessHandler(dependencies RouterDependencies) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), readinessTimeout)
		defer cancel()
		if err := dependencies.GenerationGate.CheckActiveGeneration(
			ctx,
			dependencies.TenantID,
			dependencies.GenerationID,
		); err != nil {
			writeServiceUnavailable(c)
			return
		}
		response.OK(c, gin.H{"status": "ready"})
	}
}

func requireActiveGeneration(
	dependencies RouterDependencies,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), readinessTimeout)
		defer cancel()
		if err := dependencies.GenerationGate.CheckActiveGeneration(
			ctx,
			dependencies.TenantID,
			dependencies.GenerationID,
		); err != nil {
			writeServiceUnavailable(c)
			c.Abort()
			return
		}
		c.Next()
	}
}

func writeServiceUnavailable(c *gin.Context) {
	c.PureJSON(http.StatusServiceUnavailable, response.Body{
		Code:    int(errx.CodeInternal),
		Message: "service unavailable",
	})
}

func securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Header("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		c.Header("Referrer-Policy", "no-referrer")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Next()
	}
}

func requestIDBoundary() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(requestctx.RequestIDHeader)
		if id == "" {
			id = uuid.NewString()
		}
		c.Set(requestctx.RequestIDHeader, id)
		c.Header(requestctx.RequestIDHeader, id)
		c.Request = c.Request.WithContext(requestctx.WithRequestID(c.Request.Context(), id))
		c.Next()
	}
}

func recoveryBoundary() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if recover() != nil {
				// Panic values are attacker-influenced in the general case. Keep
				// the request correlation fields, but never persist the raw value.
				logx.FromContext(c.Request.Context()).Error("http_panic")
				c.AbortWithStatusJSON(
					http.StatusInternalServerError,
					response.Body{
						Code:    int(errx.CodeInternal),
						Message: "internal server error",
					},
				)
			}
		}()
		c.Next()
	}
}
