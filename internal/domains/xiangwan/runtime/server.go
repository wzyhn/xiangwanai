package xiangwanruntime

import (
	"context"
	"crypto/ed25519"
	cryptorand "crypto/rand"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/wzyhn/xiangwanai/internal/capabilities/storage/publicobject"
	activitypostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity/postgres"
	adminoidc "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin/oidc"
	adminpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin/postgres"
	xiangwanapi "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/api"
	bookingpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/booking/postgres"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/checkin"
	checkinpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/checkin/postgres"
	consumerprofilepostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/consumerprofile/postgres"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon"
	couponpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon/postgres"
	datarightspostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/datarights/postgres"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/identity"
	identitypostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/identity/postgres"
	identitywechat "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/identity/wechat"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	paymentpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment/postgres"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment/wechatpay"
	peoplepostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/people/postgres"
	registrationpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration/postgres"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/resource"
	resourcepostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/resource/postgres"
	"github.com/wzyhn/xiangwanai/internal/pkg/contentsecurity"
	"github.com/wzyhn/xiangwanai/internal/pkg/response"
	wechatpkg "github.com/wzyhn/xiangwanai/internal/pkg/wechat"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	wechatpayutils "github.com/wechatpay-apiv3/wechatpay-go/utils"
)

const (
	httpReadHeaderTimeout = 5 * time.Second
	httpReadTimeout       = 15 * time.Second
	httpWriteTimeout      = 30 * time.Second
	httpIdleTimeout       = 60 * time.Second
	shutdownTimeout       = 10 * time.Second
)

var (
	ErrInvalidRuntimeServer  = errors.New("invalid xiangwan Runtime server")
	ErrDatabaseConfiguration = errors.New(
		"invalid xiangwan PostgreSQL configuration",
	)
)

type Server struct {
	database   *sql.DB
	httpServer *http.Server
}

func NewServer(config Config) (*Server, error) {
	if strings.TrimSpace(config.DatabaseDSN) == "" ||
		config.TenantID == uuid.Nil || config.GenerationID == uuid.Nil ||
		!validSigningKey(config.JWTSigningKey) ||
		!validWechatAppID(config.AppID) ||
		!validWechatAppSecret(config.AppSecret) ||
		!validListenAddress(config.ListenAddress) ||
		strings.TrimSpace(config.StorageLocalDir) == "" ||
		!validRuntimeAdminConfig(config.Admin) ||
		(len(config.CouponGrantPolicyPublicKey) != 0 &&
			(len(config.CouponGrantPolicyPublicKey) != ed25519.PublicKeySize || !config.Admin.Enabled)) ||
		!validRuntimePrepayConfig(config) ||
		!validRuntimeCancellationConfig(config) ||
		!validRuntimeCheckinCredentialConfig(config) {
		return nil, ErrInvalidRuntimeServer
	}
	database, err := sql.Open("pgx", config.DatabaseDSN)
	if err != nil {
		return nil, ErrDatabaseConfiguration
	}
	database.SetMaxOpenConns(20)
	database.SetMaxIdleConns(5)
	database.SetConnMaxIdleTime(5 * time.Minute)
	database.SetConnMaxLifetime(30 * time.Minute)

	resourceRepository := resourcepostgres.NewRepository(database)
	activityRepository := activitypostgres.NewRepository(database)
	bookingRepository := bookingpostgres.NewRepository(database)
	peopleRepository := peoplepostgres.NewRepository(database)
	authenticator, err := NewAuthenticator(
		config.JWTSigningKey,
		config.JWTPreviousSecrets,
		config.AppID,
		NewPostgresPrincipalGate(database),
	)
	if err != nil {
		_ = database.Close()
		return nil, ErrInvalidRuntimeServer
	}
	identityResolver, err := identitypostgres.NewResolver(
		database,
		config.TenantID,
		config.GenerationID,
		config.AppID,
		config.PrivacyPolicyVersion,
	)
	if err != nil {
		_ = database.Close()
		return nil, ErrInvalidRuntimeServer
	}
	wechatClient := wechatpkg.NewMiniApp()
	codeExchanger, err := identitywechat.NewExchangerWithMiniApp(
		wechatClient,
		config.AppID,
		config.AppSecret,
	)
	if err != nil {
		_ = database.Close()
		return nil, ErrInvalidRuntimeServer
	}
	loginService, err := identity.NewLoginService(
		codeExchanger,
		identityResolver,
		authenticator,
		time.Now,
	)
	if err != nil {
		_ = database.Close()
		return nil, ErrInvalidRuntimeServer
	}
	objectReader, err := publicobject.NewLocalReader(
		database,
		config.StorageLocalDir,
	)
	if err != nil {
		_ = database.Close()
		return nil, ErrInvalidRuntimeServer
	}
	publicReviewService, err := xiangwanapi.NewPublicReviewService(
		config.TenantID,
		resourceRepository,
		activityRepository,
		resourceRepository,
		activityRepository,
		config.ExternalDomains,
		time.Now,
	)
	if err != nil {
		_ = database.Close()
		return nil, ErrInvalidRuntimeServer
	}
	publicPastActivitiesService, err :=
		xiangwanapi.NewPublicPastActivitiesService(
			config.TenantID,
			activityRepository,
			time.Now,
		)
	if err != nil {
		_ = database.Close()
		return nil, ErrInvalidRuntimeServer
	}
	publicHomeService, err := xiangwanapi.NewPublicHomeService(
		config.TenantID,
		activityRepository,
		activityRepository,
		activityRepository,
		time.Now,
	)
	if err != nil {
		_ = database.Close()
		return nil, ErrInvalidRuntimeServer
	}
	publicSessionService, err := xiangwanapi.NewPublicSessionDetailServiceWithPastHighlights(
		config.TenantID,
		activityRepository,
		activityRepository,
		peopleRepository,
		resourceRepository,
		activityRepository,
		config.ExternalDomains,
		time.Now,
	)
	if err != nil {
		_ = database.Close()
		return nil, ErrInvalidRuntimeServer
	}
	publicSessionsService, err := xiangwanapi.NewPublicSessionCollectionService(
		config.TenantID,
		activityRepository,
		activityRepository,
	)
	if err != nil {
		_ = database.Close()
		return nil, ErrInvalidRuntimeServer
	}
	publicPeopleService, err := xiangwanapi.NewPublicPeopleService(
		config.TenantID,
		peopleRepository,
		time.Now,
	)
	if err != nil {
		_ = database.Close()
		return nil, ErrInvalidRuntimeServer
	}
	credentialProtector, err := checkin.NewCredentialProtector(
		[]byte(config.CheckinCredentialHMACKey),
		cryptorand.Reader,
	)
	if err != nil {
		_ = database.Close()
		return nil, ErrInvalidRuntimeServer
	}
	checkinCredentialService, err := xiangwanapi.NewCheckinCredentialService(
		config.TenantID,
		config.CheckinCredentialTTL,
		checkinpostgres.NewCredentialIssuer(
			database,
			credentialProtector,
			config.GenerationID,
		),
	)
	if err != nil {
		_ = database.Close()
		return nil, ErrInvalidRuntimeServer
	}
	seriesFavoriteService, err := xiangwanapi.NewSeriesFavoriteService(
		config.TenantID,
		activitypostgres.NewSeriesFavoriteWriter(database),
	)
	if err != nil {
		_ = database.Close()
		return nil, ErrInvalidRuntimeServer
	}
	myFavoritesService, err := xiangwanapi.NewMyFavoritesService(
		config.TenantID,
		activityRepository,
		time.Now,
	)
	if err != nil {
		_ = database.Close()
		return nil, ErrInvalidRuntimeServer
	}
	publicPoliciesService, err := xiangwanapi.NewPublicPoliciesService(
		xiangwanapi.PublicPoliciesConfig{
			PrivacyPolicyVersion:          config.PrivacyPolicyVersion,
			UserAgreementVersion:          config.UserAgreementVersion,
			CancellationPolicyVersion:     config.CancelPolicyVersion,
			CancellationPolicyCutoffHours: config.CancelPolicyCutoffHours,
			CustomerServicePolicyVersion:  config.CustomerServicePolicyVersion,
			ExternalLinksPolicyVersion:    config.ExternalDomainsPolicyVersion,
			ManualContactEnabled:          config.ManualContactEnabled,
			ManualContactPolicyVersion:    config.ManualContactPolicyVersion,
			ManualContactPolicyText:       config.ManualContactPolicyText,
			WeChatPaymentEnabled:          config.PaidRegistrationEnabled && config.WeChatPrepayEnabled,
		},
	)
	if err != nil {
		_ = database.Close()
		return nil, ErrInvalidRuntimeServer
	}
	dataRightsService, err := xiangwanapi.NewDataRightsService(
		config.TenantID,
		config.PrivacyPolicyVersion,
		datarightspostgres.NewWriter(database),
		datarightspostgres.NewReader(database),
	)
	if err != nil {
		_ = database.Close()
		return nil, ErrInvalidRuntimeServer
	}
	consumerProfileRepository := consumerprofilepostgres.NewRepository(
		database,
		config.GenerationID,
	)
	profileContentChecker := contentsecurity.NewWechatChecker(
		wechatClient,
		config.AppID,
		config.AppSecret,
	)
	consumerProfileService, err := xiangwanapi.NewConsumerProfileService(
		config.TenantID,
		config.AppID,
		config.PrivacyPolicyVersion,
		consumerProfileRepository,
		consumerProfileRepository,
		profileContentChecker,
	)
	if err != nil {
		_ = database.Close()
		return nil, ErrInvalidRuntimeServer
	}
	sessionQuestionnaireService, err :=
		xiangwanapi.NewSessionQuestionnaireService(
			config.TenantID,
			publicSessionService,
			activityRepository,
		)
	if err != nil {
		_ = database.Close()
		return nil, ErrInvalidRuntimeServer
	}
	paymentAppID := ""
	if config.PaidRegistrationEnabled {
		paymentAppID = config.AppID
	}
	createRegistrationService, err := xiangwanapi.NewCreateRegistrationService(
		config.TenantID,
		publicSessionService,
		registrationpostgres.NewFreeRegistrar(database, config.GenerationID),
		paymentpostgres.NewPaidRegistrar(database, config.GenerationID),
		xiangwanapi.CreateRegistrationConfig{
			PrivacyPolicyVersion:       config.PrivacyPolicyVersion,
			ManualContactEnabled:       config.ManualContactEnabled,
			ContactPolicyVersion:       config.ManualContactPolicyVersion,
			PaidRegistrationEnabled:    config.PaidRegistrationEnabled,
			PaymentAppID:               paymentAppID,
			PaymentMerchantID:          config.PaymentMerchantID,
			MerchantConfigGenerationID: config.PaymentMerchantConfigGenerationID,
		},
	)
	if err != nil {
		_ = database.Close()
		return nil, ErrInvalidRuntimeServer
	}
	weChatPaymentServices, err := buildWeChatPaymentServices(
		context.Background(),
		database,
		config,
	)
	if err != nil {
		_ = database.Close()
		return nil, ErrInvalidRuntimeServer
	}
	publicMediaService, err := xiangwanapi.NewPublicMediaService(
		config.TenantID,
		resourceRepository,
		objectReader,
	)
	if err != nil {
		_ = database.Close()
		return nil, ErrInvalidRuntimeServer
	}
	myRegistrationsService, err := xiangwanapi.NewMyRegistrationsService(
		config.TenantID,
		bookingRepository,
		time.Now,
	)
	if err != nil {
		_ = database.Close()
		return nil, ErrInvalidRuntimeServer
	}
	cancellationPolicy, err := newRuntimePaidCancellationPolicy(
		config.CancelPolicyVersion,
		config.CancelPolicyCutoffHours,
	)
	if err != nil {
		_ = database.Close()
		return nil, ErrInvalidRuntimeServer
	}
	myRegistrationService, err :=
		xiangwanapi.NewMyRegistrationDetailServiceWithCancellationPolicy(
			config.TenantID,
			bookingRepository,
			cancellationPolicy,
			time.Now,
		)
	if err != nil {
		_ = database.Close()
		return nil, ErrInvalidRuntimeServer
	}
	registrationCancellationService, err :=
		xiangwanapi.NewRegistrationCancellationService(
			config.TenantID,
			registrationpostgres.NewRegistrationCancellerWithCouponRefundPolicy(
				database,
				cancellationPolicy,
				newRuntimeCouponRefundPolicy(
					config.CouponRefundPolicyVersion,
					config.CouponRefundDisposition,
				),
			),
		)
	if err != nil {
		_ = database.Close()
		return nil, ErrInvalidRuntimeServer
	}
	myOrdersService, err := xiangwanapi.NewMyOrdersService(
		config.TenantID,
		bookingRepository,
		time.Now,
	)
	if err != nil {
		_ = database.Close()
		return nil, ErrInvalidRuntimeServer
	}
	myCouponsService, err := xiangwanapi.NewMyCouponsService(
		config.TenantID,
		couponpostgres.NewMyCouponsReader(database),
		time.Now,
	)
	if err != nil {
		_ = database.Close()
		return nil, ErrInvalidRuntimeServer
	}
	hostApplicationAuthorizer :=
		peoplepostgres.NewActivePrincipalApplicantAuthorizer()
	hostRulesProvider := &peoplepostgres.PostgreSQLHostRulesProvider{}
	myBenefitsService, err := xiangwanapi.NewMyBenefitsService(
		config.TenantID,
		peoplepostgres.NewBenefitsReader(
			database,
			hostApplicationAuthorizer,
			hostRulesProvider,
		),
	)
	if err != nil {
		_ = database.Close()
		return nil, ErrInvalidRuntimeServer
	}
	hostApplicationSubmissionService, err :=
		xiangwanapi.NewHostApplicationSubmissionService(
			config.TenantID,
			peoplepostgres.NewFencedHostApplicationWriter(database, hostApplicationAuthorizer, hostRulesProvider, config.GenerationID, config.PrivacyPolicyVersion),
		)
	if err != nil {
		_ = database.Close()
		return nil, ErrInvalidRuntimeServer
	}
	myOrderService, err := xiangwanapi.NewMyOrderDetailService(
		config.TenantID,
		bookingRepository,
		time.Now,
	)
	if err != nil {
		_ = database.Close()
		return nil, ErrInvalidRuntimeServer
	}
	adminAuth := xiangwanapi.NewDisabledAdminAuthHandler()
	var adminCatalog *xiangwanapi.AdminCatalogHandler
	var onsiteCheckin *xiangwanapi.OnsiteCheckinHandler
	adminEnabled := false
	// The brand-banner store serves its public read route regardless of the
	// administrator surface; uploads inside it are authorized per request.
	authorizer, authorizerErr := adminpostgres.NewGrantAuthorizer(config.TenantID)
	if authorizerErr != nil {
		_ = database.Close()
		return nil, ErrInvalidRuntimeServer
	}
	// Review resources are administrator writes too.  Recheck the exact
	// generation and identity/Grant inside the writer's transaction so a
	// revocation or generation cutover cannot race the Content/publication
	// commit after the HTTP middleware has already admitted the request.
	reviewResourceAuthorize := func(ctx context.Context, tx *sql.Tx, actorID, identityLinkID uuid.UUID) error {
		var active bool
		if err := tx.QueryRowContext(ctx, `
SELECT TRUE
FROM xiangwan_runtime_generations
WHERE singleton_id = 1
  AND scope_key = 'wq-xiangwan'
  AND tenant_id = $1
  AND active_generation_id = $2
  AND write_epoch > 0
  AND bootstrap_completed_at IS NOT NULL
FOR SHARE
`, config.TenantID, config.GenerationID).Scan(&active); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return resource.ErrReviewResourceConflict
			}
			return err
		}
		if !active {
			return resource.ErrReviewResourceConflict
		}
		return authorizer.RequireActivityOperatorIdentity(
			ctx, tx, actorID, identityLinkID,
		)
	}
	reviewResourceWriter, reviewWriterErr := resourcepostgres.NewReviewResourceWriter(
		database,
		config.TenantID,
		config.ExternalDomains,
		config.ExternalDomainsPolicyVersion,
		reviewResourceAuthorize,
	)
	if reviewWriterErr != nil {
		_ = database.Close()
		return nil, ErrInvalidRuntimeServer
	}
	photoCurationService, photoCurationErr := resourcepostgres.NewPhotoCurationService(
		database, config.TenantID, reviewResourceAuthorize,
	)
	if photoCurationErr != nil {
		_ = database.Close()
		return nil, ErrInvalidRuntimeServer
	}
	brandHeroStore, brandHeroStoreErr := xiangwanapi.NewLocalBrandHeroStore(
		filepath.Join(config.StorageLocalDir, "brand-hero"),
	)
	if brandHeroStoreErr != nil {
		_ = database.Close()
		return nil, ErrInvalidRuntimeServer
	}
	brandHero := xiangwanapi.NewBrandHeroHandler(brandHeroStore, authorizer, database)
	brandHeroRefs, brandHeroRefsErr := adminpostgres.NewBrandHeroReferenceQuerier(config.TenantID)
	if brandHeroRefsErr != nil {
		_ = database.Close()
		return nil, ErrInvalidRuntimeServer
	}
	brandHero.SetGCDependencies(
		xiangwanapi.NewBrandHeroReferenceAdapter(brandHeroRefs, database),
		xiangwanapi.NewBrandHeroGCMarkerAdapter(
			adminpostgres.NewBrandHeroGCMarkerStore(), database,
		),
	)
	// The cover store serves its public read route regardless of the
	// administrator surface; uploads inside it are authorized per request.
	coverImageStore, coverImageStoreErr := xiangwanapi.NewLocalCoverImageStore(
		filepath.Join(config.StorageLocalDir, "covers"),
	)
	if coverImageStoreErr != nil {
		_ = database.Close()
		return nil, ErrInvalidRuntimeServer
	}
	coverRefs, coverRefsErr := adminpostgres.NewCoverImageReferenceQuerier(config.TenantID)
	if coverRefsErr != nil {
		_ = database.Close()
		return nil, ErrInvalidRuntimeServer
	}
	coverImages := xiangwanapi.NewCoverImageHandler(
		coverImageStore,
		authorizer,
		database,
		xiangwanapi.NewCoverReferenceAdapter(coverRefs, database),
		// The marker store (migration 788) turns the cover orphan GC
		// two-phase: objects are deleted only after staying unreferenced
		// for the retention window, never on upload age alone.
		xiangwanapi.NewCoverGCMarkerAdapter(
			adminpostgres.NewCoverGCMarkerStore(),
			database,
		),
	)
	// The avatar store serves its public read route regardless of the
	// administrator surface; the write repoints the session Principal's
	// Auth-owned avatar_url in the same runtime database.
	consumerAvatarStore, consumerAvatarStoreErr :=
		xiangwanapi.NewLocalConsumerAvatarStore(
			filepath.Join(config.StorageLocalDir, "avatars"),
		)
	if consumerAvatarStoreErr != nil {
		_ = database.Close()
		return nil, ErrInvalidRuntimeServer
	}
	consumerIdentityStore, consumerIdentityStoreErr :=
		identitypostgres.NewProfileStore(database)
	if consumerIdentityStoreErr != nil {
		_ = database.Close()
		return nil, ErrInvalidRuntimeServer
	}
	// The avatar review defaults ON and fails closed: an unavailable
	// moderation service refuses uploads instead of letting unreviewed
	// images go public. XIANGWAN_AVATAR_MODERATION_DISABLED=true is the
	// explicit local-development kill switch.
	var avatarModerator xiangwanapi.AvatarModerator
	if !config.AvatarModerationDisabled {
		avatarModerator = xiangwanapi.NewWechatAvatarModerator(
			wechatClient,
			config.AppID,
			config.AppSecret,
		)
	}
	consumerIdentity := xiangwanapi.NewConsumerIdentityHandler(
		consumerIdentityStore,
		consumerAvatarStore,
		authenticator.PrincipalID,
		avatarModerator,
		xiangwanapi.NewWechatNicknameModerator(
			profileContentChecker,
			consumerProfileRepository.ProviderOpenID,
			config.AppID,
		),
	)
	consumerIdentity.SetLegacyAvatarReader(
		newLegacyConsumerAvatarReader(database, objectReader),
	)
	consumerIdentity.SetRegistrationContactStore(identitypostgres.NewRegistrationContactStore(
		database, config.TenantID, config.GenerationID, config.PrivacyPolicyVersion,
		config.ManualContactPolicyVersion, config.ManualContactEnabled,
	))
	var peopleBindingHandler *xiangwanapi.PeopleBindingHandler
	if config.Admin.Enabled {
		sessionStore, storeErr := adminpostgres.NewSessionStore(
			database, config.TenantID, config.Admin.SessionKey,
		)
		if storeErr != nil {
			_ = database.Close()
			return nil, ErrInvalidRuntimeServer
		}
		discoveryContext, cancelDiscovery := context.WithTimeout(
			context.Background(), 10*time.Second,
		)
		oidcClient, oidcErr := adminoidc.New(discoveryContext, adminoidc.Config{
			Issuer: config.Admin.OIDCIssuer, ClientID: config.Admin.OIDCClientID,
			ClientSecret: config.Admin.OIDCSecret,
			RedirectURL:  config.Admin.OIDCRedirect,
			RequiredACR:  config.Admin.RequiredACR,
			CAFile:       config.Admin.OIDCCAFile,
		})
		cancelDiscovery()
		if oidcErr != nil {
			// A complete administrator configuration must not degrade into a
			// process whose admin routes are permanently unmounted: a
			// transient discovery failure used to leave a healthy-looking API
			// that could never serve an administrator, even after the IdP
			// recovered, until someone restarted it. Fail startup instead so
			// the deployment restart policy retries discovery (codex review
			// 2026-09-19).
			_ = database.Close()
			return nil, ErrInvalidRuntimeServer
		} else {
			adminAuth, err = xiangwanapi.NewAdminAuthHandler(
				sessionStore, oidcClient, []string{config.Admin.Origin},
			)
			if err != nil {
				_ = database.Close()
				return nil, ErrInvalidRuntimeServer
			}
			catalog, catalogErr := adminpostgres.NewCatalogWithCouponRefundPolicyAndExternalDomains(
				database,
				config.TenantID,
				config.GenerationID,
				authorizer,
				newRuntimeCouponRefundPolicy(
					config.CouponRefundPolicyVersion,
					config.CouponRefundDisposition,
				),
				config.ExternalDomains,
			)
			if catalogErr != nil {
				_ = database.Close()
				return nil, ErrInvalidRuntimeServer
			}
			adminCatalog = xiangwanapi.NewAdminCatalogHandlerWithCancellation(catalog, catalog)
			adminCatalog.SetHostRulesModerator(func(ctx context.Context, text string) error {
				return contentsecurity.EnforceText(ctx, profileContentChecker, "xiangwan.host_rules", config.AppID, "", text, 1)
			})
			if config.PrivacyPolicyVersion != "" {
				if err := catalog.SetPeopleBindingInvitationKey(config.Admin.SessionKey); err != nil {
					_ = database.Close()
					return nil, ErrInvalidRuntimeServer
				}
				acceptor, err := peoplepostgres.NewBindingInvitationAcceptor(database, config.TenantID, config.GenerationID, config.PrivacyPolicyVersion, func(ctx context.Context, tx *sql.Tx, actorID, identityID uuid.UUID) error {
					return authorizer.RequireSuperAdminIdentity(ctx, tx, actorID, identityID)
				})
				if err != nil {
					_ = database.Close()
					return nil, ErrInvalidRuntimeServer
				}
				peopleBindingHandler = xiangwanapi.NewPeopleBindingHandler(acceptor, authenticator.PrincipalID, func(c *gin.Context) {
					owner, err := authenticator.PrincipalID(c)
					if err != nil {
						c.Abort()
						return
					}
					decision, err := newPostgresSecurityThrottle(config.TenantID, "people_binding_invitation", owner.String(), 10, sqlSecurityThrottleExecutor{database: database}).Consume(c.Request.Context())
					if err != nil {
						writeServiceUnavailable(c)
						c.Abort()
						return
					}
					if !decision.Allowed {
						c.Header("Retry-After", strconv.Itoa(decision.RetryAfterSeconds))
						c.AbortWithStatusJSON(http.StatusTooManyRequests, response.Body{Code: 42900, Message: "rate limit exceeded"})
						return
					}
					c.Next()
				})
			}
			adminCatalog.SetCouponCorrectionReader(catalog)
			adminCatalog.SetCouponCorrectionOperator(catalog)
			if len(config.CouponGrantPolicyPublicKey) == ed25519.PublicKeySize {
				policyAuthorize := func(ctx context.Context, tx *sql.Tx, actorID, identityLinkID uuid.UUID) error {
					var active bool
					if err := tx.QueryRowContext(ctx, `
SELECT TRUE FROM xiangwan_runtime_generations
WHERE singleton_id = 1 AND scope_key = 'wq-xiangwan'
  AND tenant_id = $1 AND active_generation_id = $2
  AND write_epoch > 0 AND bootstrap_completed_at IS NOT NULL
FOR SHARE
`, config.TenantID, config.GenerationID).Scan(&active); err != nil {
						return err
					}
					if !active {
						return coupon.ErrInvalidPolicyActivation
					}
					return authorizer.RequireSuperAdminIdentity(ctx, tx, actorID, identityLinkID)
				}
				policyWriter, policyErr := couponpostgres.NewPolicyActivationWriter(
					database, config.TenantID, config.CouponGrantPolicyPublicKey,
					policyAuthorize,
				)
				if policyErr != nil {
					_ = database.Close()
					return nil, ErrInvalidRuntimeServer
				}
				adminCatalog.SetCouponPolicyActivationWriter(policyWriter)
				adminCatalog.SetCouponReplenisher(config.TenantID,
					couponpostgres.NewGrantor(database,
						couponpostgres.GenerationBoundGrantPolicyProvider{
							TenantID: config.TenantID, GenerationID: config.GenerationID,
						},
						couponpostgres.GenerationBoundManualGrantAuthorizer{
							TenantID: config.TenantID, GenerationID: config.GenerationID,
							RequireSuperAdmin: func(ctx context.Context, query couponpostgres.CouponAuthorizationQuery, actorID, identityLinkID uuid.UUID) error {
								return authorizer.RequireSuperAdminIdentity(ctx, query, actorID, identityLinkID)
							},
						}))
			}
			// Review-media byte staging remains closed until safety review,
			// permanent File binding and timeout/cleanup contracts are complete.
			adminCatalog.SetReviewResourceWriter(reviewResourceWriter)
			adminCatalog.SetPhotoCurationService(photoCurationService)
			refundOperator, refundOperatorErr := adminpostgres.NewRefundOperator(
				database, config.TenantID, config.GenerationID, authorizer,
				newRuntimeCouponRefundPolicy(
					config.CouponRefundPolicyVersion, config.CouponRefundDisposition,
				),
			)
			if refundOperatorErr != nil {
				_ = database.Close()
				return nil, ErrInvalidRuntimeServer
			}
			adminCatalog.SetRefundOperator(refundOperator)
			onsiteService, onsiteErr := xiangwanapi.NewOnsiteCheckinService(
				config.TenantID,
				checkinpostgres.NewVerifier(
					database, credentialProtector, authorizer, config.GenerationID,
				),
				checkinpostgres.NewRecorder(database, authorizer, config.GenerationID),
			)
			if onsiteErr != nil {
				_ = database.Close()
				return nil, ErrInvalidRuntimeServer
			}
			onsiteCheckin = xiangwanapi.NewOnsiteCheckinHandler(onsiteService, adminAuth)
			adminEnabled = true
		}
	}
	router, err := NewRouter(RouterDependencies{
		TenantID:       config.TenantID,
		GenerationID:   config.GenerationID,
		TrustedProxies: config.TrustedProxies,
		GenerationGate: NewPostgresGenerationGate(database),
		PublicHome: xiangwanapi.NewPublicHomeHandler(
			publicHomeService,
		),
		PublicPastActivities: xiangwanapi.NewPublicPastActivitiesHandler(
			publicPastActivitiesService,
		),
		PublicSessions: xiangwanapi.NewPublicSessionCollectionHandler(
			publicSessionsService,
		),
		PublicSession: xiangwanapi.NewPublicSessionDetailHandler(
			publicSessionService,
		),
		PublicReview: xiangwanapi.NewPublicReviewHandler(
			publicReviewService,
		),
		PublicMedia:    xiangwanapi.NewPublicMediaHandler(publicMediaService),
		BrandHero:      brandHero,
		CoverImages:    coverImages,
		PublicPeople:   xiangwanapi.NewPublicPeopleHandler(publicPeopleService),
		PublicPolicies: xiangwanapi.NewPublicPoliciesHandler(publicPoliciesService),
		WeChatLogin: xiangwanapi.NewWeChatLoginHandler(
			loginService,
			config.AppID,
			config.PrivacyPolicyVersion,
			consumerIdentityStore,
		),
		WeChatLoginThrottle: requireSecurityThrottle(
			NewPostgresWeChatLoginThrottle(
				database,
				config.TenantID,
				config.AppID,
			),
		),
		RequirePrincipal: authenticator.RequirePrincipal(),
		Questionnaire: xiangwanapi.NewSessionQuestionnaireHandler(
			sessionQuestionnaireService,
			authenticator.PrincipalID,
		),
		CreateRegistration: xiangwanapi.NewCreateRegistrationHandler(
			createRegistrationService,
			authenticator.PrincipalID,
		),
		WeChatPrepay: xiangwanapi.NewWeChatPrepayHandler(
			weChatPaymentServices.prepay,
			authenticator.PrincipalID,
		),
		WeChatPaymentQuery: xiangwanapi.NewWeChatPaymentQueryHandler(
			weChatPaymentServices.query,
			authenticator.PrincipalID,
		),
		WeChatPaymentNotification: xiangwanapi.NewWeChatPaymentNotificationHandler(
			weChatPaymentServices.decoder,
			weChatPaymentServices.notification,
		),
		MyRegistrations: xiangwanapi.NewMyRegistrationsHandler(
			myRegistrationsService,
			authenticator.PrincipalID,
		),
		MyRegistration: xiangwanapi.NewMyRegistrationDetailHandler(
			myRegistrationService,
			authenticator.PrincipalID,
		),
		RegistrationCancellation: xiangwanapi.NewRegistrationCancellationHandler(
			registrationCancellationService,
			authenticator.PrincipalID,
		),
		CheckinCredential: xiangwanapi.NewCheckinCredentialHandler(
			checkinCredentialService,
			authenticator.PrincipalID,
		),
		MyOrders: xiangwanapi.NewMyOrdersHandler(
			myOrdersService,
			authenticator.PrincipalID,
		),
		MyOrder: xiangwanapi.NewMyOrderDetailHandler(
			myOrderService,
			authenticator.PrincipalID,
		),
		MyCoupons: xiangwanapi.NewMyCouponsHandler(
			myCouponsService,
			authenticator.PrincipalID,
		),
		MyFavorites: xiangwanapi.NewMyFavoritesHandler(
			myFavoritesService,
			authenticator.PrincipalID,
		),
		DataRights: xiangwanapi.NewDataRightsHandler(
			dataRightsService,
			authenticator.PrincipalID,
		),
		ConsumerProfile: xiangwanapi.NewConsumerProfileHandler(
			consumerProfileService,
			authenticator.PrincipalID,
		),
		ConsumerIdentity: consumerIdentity,
		MyBenefits: xiangwanapi.NewMyBenefitsHandler(
			myBenefitsService,
			authenticator.PrincipalID,
		),
		HostApplicationSubmission: xiangwanapi.NewHostApplicationSubmissionHandler(
			hostApplicationSubmissionService,
			authenticator.PrincipalID,
		),
		PeopleBinding: peopleBindingHandler,
		SeriesFavorite: xiangwanapi.NewSeriesFavoriteHandler(
			seriesFavoriteService,
			authenticator.PrincipalID,
		),
		AdminEnabled:  adminEnabled,
		AdminAuth:     adminAuth,
		AdminCatalog:  adminCatalog,
		OnsiteCheckin: onsiteCheckin,
	})
	if err != nil {
		_ = database.Close()
		return nil, ErrInvalidRuntimeServer
	}
	return &Server{
		database: database,
		httpServer: &http.Server{
			Addr:              config.ListenAddress,
			Handler:           withoutPublicMediaWriteDeadline(router),
			ReadHeaderTimeout: httpReadHeaderTimeout,
			ReadTimeout:       httpReadTimeout,
			WriteTimeout:      httpWriteTimeout,
			IdleTimeout:       httpIdleTimeout,
		},
	}, nil
}

func validRuntimeAdminConfig(config AdminConfig) bool {
	if !config.Enabled {
		return config.Origin == "" && config.OIDCIssuer == "" &&
			config.OIDCClientID == "" && config.OIDCSecret == "" &&
			config.OIDCRedirect == "" && config.RequiredACR == "" &&
			config.OIDCCAFile == "" &&
			len(config.SessionKey) == 0
	}
	if config.Origin == "" || config.OIDCIssuer == "" ||
		config.OIDCClientID == "" || config.OIDCSecret == "" ||
		config.OIDCRedirect == "" || len(config.SessionKey) != 32 {
		return false
	}
	origin, err := url.Parse(config.Origin)
	if err != nil || origin.Scheme != "https" || origin.Host == "" ||
		origin.User != nil || origin.Path != "" || origin.RawQuery != "" ||
		origin.Fragment != "" {
		return false
	}
	issuer, err := url.Parse(config.OIDCIssuer)
	if err != nil || issuer.Scheme != "https" || issuer.Host == "" ||
		issuer.User != nil || issuer.RawQuery != "" || issuer.Fragment != "" {
		return false
	}
	redirect, err := url.Parse(config.OIDCRedirect)
	return err == nil && redirect.Scheme == "https" && redirect.Host != "" &&
		redirect.User == nil && redirect.RawQuery == "" && redirect.Fragment == "" &&
		sameCanonicalAdminOrigin(origin, redirect) &&
		redirect.Path == "/api/v1/xiangwan/admin/auth/callback" &&
		config.RequiredACR == strings.TrimSpace(config.RequiredACR) &&
		config.OIDCCAFile == strings.TrimSpace(config.OIDCCAFile) &&
		!strings.ContainsAny(config.OIDCCAFile, "\r\n\x00") &&
		!strings.ContainsAny(config.RequiredACR, "\r\n\x00")
}

func validRuntimeCheckinCredentialConfig(config Config) bool {
	key := config.CheckinCredentialHMACKey
	return key == strings.TrimSpace(key) &&
		len([]byte(key)) >= minimumCheckinCredentialKeyLen &&
		len([]byte(key)) <= maximumCheckinCredentialKeyLen &&
		!strings.ContainsAny(key, "\r\n\x00") &&
		distinctCheckinCredentialKey(
			key,
			config.JWTSigningKey,
			config.JWTPreviousSecrets,
		) &&
		config.CheckinCredentialTTL > 0 &&
		config.CheckinCredentialTTL <= checkin.MaxCredentialTTL
}

func validRuntimeCancellationConfig(config Config) bool {
	if config.CancelPolicyVersion == "" {
		if config.CancelPolicyCutoffHours != 0 {
			return false
		}
	} else if !runtimePolicyVersionPattern.MatchString(config.CancelPolicyVersion) ||
		config.CancelPolicyCutoffHours < 0 ||
		config.CancelPolicyCutoffHours > maximumCancellationCutoffHours {
		return false
	}
	if config.CouponRefundPolicyVersion == "" {
		return config.CouponRefundDisposition == ""
	}
	return runtimePolicyVersionPattern.MatchString(
		config.CouponRefundPolicyVersion,
	) && (config.CouponRefundDisposition == "restore" ||
		config.CouponRefundDisposition == "forfeit")
}

func validRuntimePrepayConfig(config Config) bool {
	if !config.WeChatPrepayEnabled {
		return !config.PaidRegistrationEnabled &&
			config.PaymentMerchantID == "" &&
			config.PaymentMerchantConfigGenerationID == uuid.Nil &&
			config.WeChatPayCertSerial == "" &&
			config.WeChatPayPrivateKeyFile == "" &&
			config.WeChatPayPublicKeyID == "" &&
			config.WeChatPayPublicKeyFile == "" &&
			config.WeChatPayAPIV3Key == "" &&
			config.WeChatPayNotifyURL == "" &&
			config.WeChatPayDescription == ""
	}
	return config.PaidRegistrationEnabled &&
		config.PaymentMerchantConfigGenerationID != uuid.Nil &&
		runtimeMerchantIDPattern.MatchString(config.PaymentMerchantID) &&
		runtimeCertSerialPattern.MatchString(config.WeChatPayCertSerial) &&
		validPaymentKeyFile(config.WeChatPayPrivateKeyFile) &&
		runtimePublicKeyIDPattern.MatchString(config.WeChatPayPublicKeyID) &&
		validPaymentKeyFile(config.WeChatPayPublicKeyFile) &&
		validPaymentAPIV3Key(config.WeChatPayAPIV3Key) &&
		validPaymentNotifyURL(config.WeChatPayNotifyURL) &&
		config.WeChatPayDescription == strings.TrimSpace(config.WeChatPayDescription) &&
		config.WeChatPayDescription != "" &&
		len([]rune(config.WeChatPayDescription)) <= 127 &&
		!strings.ContainsAny(config.WeChatPayDescription, "\r\n\x00")
}

func buildWeChatPrepayService(
	ctx context.Context,
	database *sql.DB,
	config Config,
) (*xiangwanapi.WeChatPrepayService, error) {
	services, err := buildWeChatPaymentServices(ctx, database, config)
	if err != nil {
		return nil, err
	}
	return services.prepay, nil
}

type weChatPaymentRuntimeServices struct {
	prepay       *xiangwanapi.WeChatPrepayService
	query        *xiangwanapi.WeChatPaymentQueryService
	notification *xiangwanapi.WeChatPaymentNotificationService
	decoder      *wechatpay.NotificationDecoder
}

func buildWeChatPaymentServices(
	ctx context.Context,
	database *sql.DB,
	config Config,
) (weChatPaymentRuntimeServices, error) {
	if ctx == nil || !validRuntimePrepayConfig(config) ||
		config.TenantID == uuid.Nil || config.GenerationID == uuid.Nil {
		return weChatPaymentRuntimeServices{}, ErrInvalidRuntimeServer
	}
	if !config.WeChatPrepayEnabled {
		prepayService, err := xiangwanapi.NewWeChatPrepayService(
			config.TenantID,
			config.GenerationID,
			nil,
			false,
		)
		if err != nil {
			return weChatPaymentRuntimeServices{}, err
		}
		queryService, err := xiangwanapi.NewWeChatPaymentQueryService(
			config.TenantID,
			config.GenerationID,
			nil,
			false,
		)
		if err != nil {
			return weChatPaymentRuntimeServices{}, err
		}
		notificationService, err := xiangwanapi.NewWeChatPaymentNotificationService(
			config.TenantID,
			nil,
			false,
		)
		return weChatPaymentRuntimeServices{
			prepay:       prepayService,
			query:        queryService,
			notification: notificationService,
		}, err
	}
	if database == nil {
		return weChatPaymentRuntimeServices{}, ErrInvalidRuntimeServer
	}
	privateKey, err := wechatpayutils.LoadPrivateKeyWithPath(
		config.WeChatPayPrivateKeyFile,
	)
	if err != nil {
		return weChatPaymentRuntimeServices{}, ErrInvalidRuntimeServer
	}
	publicKey, err := wechatpayutils.LoadPublicKeyWithPath(
		config.WeChatPayPublicKeyFile,
	)
	if err != nil {
		return weChatPaymentRuntimeServices{}, ErrInvalidRuntimeServer
	}
	notificationDecoder, err := wechatpay.NewNotificationDecoder(
		wechatpay.NotificationConfig{
			AppID:                config.AppID,
			MerchantID:           config.PaymentMerchantID,
			APIv3Key:             config.WeChatPayAPIV3Key,
			WeChatPayPublicKeyID: config.WeChatPayPublicKeyID,
			WeChatPayPublicKey:   publicKey,
		},
	)
	if err != nil {
		return weChatPaymentRuntimeServices{}, ErrInvalidRuntimeServer
	}
	provider, err := wechatpay.NewProvider(ctx, wechatpay.Config{
		MerchantID:                config.PaymentMerchantID,
		MerchantCertificateSerial: config.WeChatPayCertSerial,
		MerchantPrivateKey:        privateKey,
		WeChatPayPublicKeyID:      config.WeChatPayPublicKeyID,
		WeChatPayPublicKey:        publicKey,
	})
	if err != nil {
		return weChatPaymentRuntimeServices{}, ErrInvalidRuntimeServer
	}
	store, err := paymentpostgres.NewPrepayAttemptStore(
		database,
		paymentpostgres.PrepayAttemptStoreConfig{
			PaymentAppID:               config.AppID,
			PaymentMerchantID:          config.PaymentMerchantID,
			MerchantConfigGenerationID: config.PaymentMerchantConfigGenerationID,
			Description:                config.WeChatPayDescription,
			NotifyURL:                  config.WeChatPayNotifyURL,
		},
	)
	if err != nil {
		return weChatPaymentRuntimeServices{}, ErrInvalidRuntimeServer
	}
	confirmer := paymentpostgres.NewPaymentConfirmerForMerchantConfig(
		database,
		config.PaymentMerchantConfigGenerationID,
	)
	creator, err := payment.NewPrepayService(store, provider, confirmer)
	if err != nil {
		return weChatPaymentRuntimeServices{}, ErrInvalidRuntimeServer
	}
	prepayService, err := xiangwanapi.NewWeChatPrepayService(
		config.TenantID,
		config.GenerationID,
		creator,
		true,
	)
	if err != nil {
		return weChatPaymentRuntimeServices{}, ErrInvalidRuntimeServer
	}
	queryStore, err := paymentpostgres.NewPaymentQueryStore(
		database,
		paymentpostgres.PaymentQueryStoreConfig{
			PaymentAppID:               config.AppID,
			PaymentMerchantID:          config.PaymentMerchantID,
			MerchantConfigGenerationID: config.PaymentMerchantConfigGenerationID,
		},
	)
	if err != nil {
		return weChatPaymentRuntimeServices{}, ErrInvalidRuntimeServer
	}

	querier, err := payment.NewPaymentQueryService(
		queryStore,
		provider,
		confirmer,
		nil,
	)
	if err != nil {
		return weChatPaymentRuntimeServices{}, ErrInvalidRuntimeServer
	}
	queryService, err := xiangwanapi.NewWeChatPaymentQueryService(
		config.TenantID,
		config.GenerationID,
		querier,
		true,
	)
	if err != nil {
		return weChatPaymentRuntimeServices{}, ErrInvalidRuntimeServer
	}
	notificationService, err := xiangwanapi.NewWeChatPaymentNotificationService(
		config.TenantID,
		confirmer,
		true,
	)
	if err != nil {
		return weChatPaymentRuntimeServices{}, ErrInvalidRuntimeServer
	}
	return weChatPaymentRuntimeServices{
		prepay:       prepayService,
		query:        queryService,
		notification: notificationService,
		decoder:      notificationDecoder,
	}, nil
}

func (server *Server) Run(ctx context.Context) error {
	if server == nil || server.database == nil || server.httpServer == nil ||
		ctx == nil {
		return ErrInvalidRuntimeServer
	}
	result := make(chan error, 1)
	go func() {
		result <- server.httpServer.ListenAndServe()
	}()
	select {
	case err := <-result:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(
			context.WithoutCancel(ctx),
			shutdownTimeout,
		)
		defer cancel()
		if err := server.httpServer.Shutdown(shutdownContext); err != nil {
			return err
		}
		err := <-result
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

func (server *Server) Close() error {
	if server == nil || server.database == nil {
		return nil
	}
	return server.database.Close()
}
