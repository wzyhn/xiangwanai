package xiangwanruntime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	xiangwanapi "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/api"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/booking"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/checkin"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/consumerprofile"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/contribution"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/datarights"
	datarightspostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/datarights/postgres"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/identity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/people"
	peoplepostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/people/postgres"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
	registrationpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration/postgres"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/resource"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func allowRuntimeWeChatLogin(*gin.Context) {}

func TestRuntimeRouterServesLiveReadyAndGenerationGatedPublicReads(t *testing.T) {
	t.Parallel()

	tenantID := runtimeUUID(1)
	generationID := runtimeUUID(2)
	seriesID := runtimeUUID(3)
	instanceID := runtimeUUID(4)
	application := &fakeRuntimeReviewApplication{page: xiangwanapi.PublicReviewPage{
		Review: resource.PublicReviewDetail{
			Target: resource.PastHighlightReviewTarget{
				SeriesID:   seriesID,
				InstanceID: instanceID,
			},
			Documents: []resource.PublicReviewDocument{},
		},
		NextRoute: activity.NextInstanceRouteResolution{
			SeriesID:         seriesID,
			SourceInstanceID: instanceID,
			SessionRoute: activity.SessionRouteResolution{
				Kind:                activity.SessionRouteUnavailable,
				CandidateSessionIDs: []uuid.UUID{},
			},
		},
	}}
	gate := &fakeGenerationGate{}
	homeApplication := &fakeRuntimeHomeApplication{}
	sessionsApplication := &fakeRuntimeSessionsApplication{}
	sessionApplication := &fakeRuntimeSessionApplication{}
	authentication := &fakeRuntimeAuthentication{principalID: runtimeUUID(6)}
	myRegistrationsApplication := &fakeRuntimeMyRegistrationsApplication{
		page: booking.MyRegistrationsPage{
			ActiveState: booking.MyRegistrationStateAll,
			Items:       []booking.MyRegistrationItem{},
			AsOf:        time.Now().UTC(),
		},
	}
	myRegistrationApplication := &fakeRuntimeMyRegistrationApplication{}
	myOrdersApplication := &fakeRuntimeMyOrdersApplication{
		page: booking.MyOrdersPage{
			ActiveState: booking.MyOrderStateAll,
			Items:       []booking.MyOrderItem{},
			AsOf:        time.Now().UTC(),
		},
	}
	myOrderApplication := &fakeRuntimeMyOrderApplication{}
	questionnaireApplication := &fakeRuntimeQuestionnaireApplication{}
	createRegistrationApplication := &fakeRuntimeCreateRegistrationApplication{
		created: xiangwanapi.CreateRegistrationResult{Registration: registration.Registration{
			ID:                  runtimeUUID(20),
			SeriesID:            seriesID,
			InstanceID:          instanceID,
			SessionID:           runtimeUUID(21),
			ParticipationStatus: registration.ParticipationStatusConfirmed,
			Version:             1,
		},
		},
	}
	prepayOrderID := runtimeUUID(23)
	prepayApplication := &fakeRuntimeWeChatPrepayApplication{
		result: payment.PrepayAttemptResult{
			Attempt: payment.PaymentAttempt{
				ID:            runtimeUUID(24),
				OrderID:       prepayOrderID,
				AttemptStatus: payment.PrepayAttemptStatusUnknown,
			},
			HoldExpiresAt: time.Now().UTC().Add(10 * time.Minute),
		},
	}
	paymentQueryApplication := &fakeRuntimeWeChatPaymentQueryApplication{}
	cancellationApplication := &fakeRuntimeRegistrationCancellationApplication{}
	myCouponsApplication := &fakeRuntimeMyCouponsApplication{page: coupon.MyCouponsPage{
		ActiveState: coupon.MyCouponStateAll,
		Items:       []coupon.MyCouponItem{},
		AsOf:        time.Date(2026, time.September, 20, 1, 0, 0, 0, time.UTC),
	}}
	myFavoritesApplication := &fakeRuntimeMyFavoritesApplication{page: activity.MyFavoritesPage{
		Items: []activity.MyFavoriteItem{},
		AsOf:  time.Date(2026, time.September, 20, 2, 0, 0, 0, time.UTC),
	}}
	dataRightsApplication := &fakeRuntimeDataRightsApplication{
		histories: []datarights.CaseHistory{},
	}
	consumerProfileApplication := &fakeRuntimeConsumerProfileApplication{}
	myBenefitsApplication := &fakeRuntimeMyBenefitsApplication{}
	hostApplicationSubmission := &fakeRuntimeHostApplicationSubmissionApplication{}
	seriesFavoriteApplication := &fakeRuntimeSeriesFavoriteApplication{}
	checkinCredentialApplication := &fakeRuntimeCheckinCredentialApplication{}
	peopleApplication := &fakeRuntimePeopleApplication{}
	publicPoliciesApplication := &fakeRuntimePublicPoliciesApplication{
		value: xiangwanapi.PublicPolicies{
			PublishedVersions: []xiangwanapi.PublicPolicyVersion{},
		},
	}
	weChatLoginApplication := &fakeRuntimeWeChatLoginApplication{result: identity.LoginResult{
		PrincipalID: authentication.principalID,
		Token: identity.ConsumerToken{
			Value:     "signed.consumer.token",
			ExpiresAt: time.Now().UTC().Add(time.Hour),
		},
	}}
	notificationDecoder := &fakeRuntimeWeChatPaymentNotificationDecoder{}
	notificationApplication := &fakeRuntimeWeChatPaymentNotificationApplication{}
	router, err := NewRouter(RouterDependencies{
		TenantID:       tenantID,
		GenerationID:   generationID,
		GenerationGate: gate,
		PublicHome:     xiangwanapi.NewPublicHomeHandler(homeApplication),
		PublicPastActivities: new(
			xiangwanapi.PublicPastActivitiesHandler,
		),
		PublicSessions: xiangwanapi.NewPublicSessionCollectionHandler(
			sessionsApplication,
		),
		PublicSession: xiangwanapi.NewPublicSessionDetailHandler(
			sessionApplication,
		),
		PublicReview: xiangwanapi.NewPublicReviewHandler(application),
		PublicMedia: xiangwanapi.NewPublicMediaHandler(
			&fakeRuntimeMediaApplication{},
		),
		PublicPeople:   xiangwanapi.NewPublicPeopleHandler(peopleApplication),
		PublicPolicies: xiangwanapi.NewPublicPoliciesHandler(publicPoliciesApplication),
		BrandHero:      new(xiangwanapi.BrandHeroHandler),
		CoverImages:    new(xiangwanapi.CoverImageHandler),
		WeChatLogin: xiangwanapi.NewWeChatLoginHandler(
			weChatLoginApplication,
			testRuntimeAppID,
			"privacy-v1",
			nil,
		),
		WeChatLoginThrottle: allowRuntimeWeChatLogin,
		RequirePrincipal:    authentication.Middleware,
		Questionnaire: xiangwanapi.NewSessionQuestionnaireHandler(
			questionnaireApplication,
			authentication.Resolve,
		),
		CreateRegistration: xiangwanapi.NewCreateRegistrationHandler(
			createRegistrationApplication,
			authentication.Resolve,
		),
		WeChatPrepay: xiangwanapi.NewWeChatPrepayHandler(
			prepayApplication,
			authentication.Resolve,
		),
		WeChatPaymentQuery: xiangwanapi.NewWeChatPaymentQueryHandler(
			paymentQueryApplication,
			authentication.Resolve,
		),
		WeChatPaymentNotification: xiangwanapi.NewWeChatPaymentNotificationHandler(
			notificationDecoder,
			notificationApplication,
		),
		MyRegistrations: xiangwanapi.NewMyRegistrationsHandler(
			myRegistrationsApplication,
			authentication.Resolve,
		),
		MyRegistration: xiangwanapi.NewMyRegistrationDetailHandler(
			myRegistrationApplication,
			authentication.Resolve,
		),
		RegistrationCancellation: xiangwanapi.NewRegistrationCancellationHandler(
			cancellationApplication,
			authentication.Resolve,
		),
		CheckinCredential: xiangwanapi.NewCheckinCredentialHandler(
			checkinCredentialApplication,
			authentication.Resolve,
		),
		MyOrders: xiangwanapi.NewMyOrdersHandler(
			myOrdersApplication,
			authentication.Resolve,
		),
		MyOrder: xiangwanapi.NewMyOrderDetailHandler(
			myOrderApplication,
			authentication.Resolve,
		),
		MyCoupons: xiangwanapi.NewMyCouponsHandler(
			myCouponsApplication,
			authentication.Resolve,
		),
		MyFavorites: xiangwanapi.NewMyFavoritesHandler(
			myFavoritesApplication,
			authentication.Resolve,
		),
		DataRights: xiangwanapi.NewDataRightsHandler(
			dataRightsApplication,
			authentication.Resolve,
		),
		ConsumerProfile: xiangwanapi.NewConsumerProfileHandler(
			consumerProfileApplication,
			authentication.Resolve,
		),
		ConsumerIdentity: new(xiangwanapi.ConsumerIdentityHandler),
		MyBenefits: xiangwanapi.NewMyBenefitsHandler(
			myBenefitsApplication,
			authentication.Resolve,
		),
		HostApplicationSubmission: xiangwanapi.NewHostApplicationSubmissionHandler(
			hostApplicationSubmission,
			authentication.Resolve,
		),
		SeriesFavorite: xiangwanapi.NewSeriesFavoriteHandler(
			seriesFavoriteApplication,
			authentication.Resolve,
		),
	})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}

	live := performRuntimeRequest(router, "/live")
	if live.Code != http.StatusOK || gate.calls != 0 {
		t.Fatalf("live status=%d gate calls=%d", live.Code, gate.calls)
	}
	ready := performRuntimeRequest(router, "/ready")
	if ready.Code != http.StatusOK || gate.calls != 1 {
		t.Fatalf("ready status=%d gate calls=%d", ready.Code, gate.calls)
	}
	notificationRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/xiangwan/integrations/wechat-pay/notifications",
		strings.NewReader(`{"signed":"encrypted"}`),
	)
	notificationRequest.Header.Set("Content-Type", "application/json")
	notificationRequest.Header.Set("Wechatpay-Serial", "PUB_KEY_ID_TEST")
	notificationRequest.Header.Set("Wechatpay-Signature", "synthetic-signature")
	notificationRequest.Header.Set("Wechatpay-Timestamp", "1789344000")
	notificationRequest.Header.Set("Wechatpay-Nonce", "synthetic-nonce")
	notificationResponse := httptest.NewRecorder()
	router.ServeHTTP(notificationResponse, notificationRequest)
	if notificationResponse.Code != http.StatusNoContent || gate.calls != 1 ||
		authentication.calls != 0 || notificationDecoder.calls != 1 ||
		notificationApplication.calls != 1 {
		t.Fatalf(
			"notification status=%d gate=%d auth=%d decoder=%d application=%d body=%s",
			notificationResponse.Code,
			gate.calls,
			authentication.calls,
			notificationDecoder.calls,
			notificationApplication.calls,
			notificationResponse.Body.String(),
		)
	}
	home := performRuntimeRequest(router, "/api/v1/xiangwan/home-sessions")
	if home.Code != http.StatusOK || gate.calls != 2 || homeApplication.calls != 1 {
		t.Fatalf(
			"home status=%d gate calls=%d application calls=%d body=%s",
			home.Code,
			gate.calls,
			homeApplication.calls,
			home.Body.String(),
		)
	}
	sessions := performRuntimeRequest(
		router,
		"/api/v1/xiangwan/instances/"+instanceID.String()+"/sessions",
	)
	if sessions.Code != http.StatusOK || gate.calls != 3 ||
		sessionsApplication.calls != 1 {
		t.Fatalf(
			"Sessions status=%d gate calls=%d application calls=%d body=%s",
			sessions.Code,
			gate.calls,
			sessionsApplication.calls,
			sessions.Body.String(),
		)
	}
	session := performRuntimeRequest(
		router,
		"/api/v1/xiangwan/sessions/"+runtimeUUID(5).String(),
	)
	if session.Code != http.StatusOK || gate.calls != 4 ||
		sessionApplication.calls != 1 {
		t.Fatalf(
			"Session status=%d gate calls=%d application calls=%d body=%s",
			session.Code,
			gate.calls,
			sessionApplication.calls,
			session.Body.String(),
		)
	}
	review := performRuntimeRequest(
		router,
		"/api/v1/xiangwan/instances/"+instanceID.String()+"/review",
	)
	if review.Code != http.StatusOK || gate.calls != 5 ||
		application.calls != 1 || gate.tenantID != tenantID ||
		gate.generationID != generationID {
		t.Fatalf(
			"review status=%d gate=%+v application=%+v body=%s",
			review.Code,
			gate,
			application,
			review.Body.String(),
		)
	}
	if review.Header().Get("Cache-Control") != "no-store" ||
		review.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("security headers = %#v", review.Header())
	}
	questionnaireSessionID := runtimeUUID(9)
	questionnaire := performRuntimeRequest(
		router,
		"/api/v1/xiangwan/sessions/"+
			questionnaireSessionID.String()+"/questionnaire",
	)
	if questionnaire.Code != http.StatusOK || gate.calls != 6 ||
		authentication.calls != 1 || questionnaireApplication.calls != 1 ||
		questionnaireApplication.principalID != authentication.principalID ||
		questionnaireApplication.sessionID != questionnaireSessionID {
		t.Fatalf(
			"questionnaire status=%d gate=%d auth=%d application=%+v body=%s",
			questionnaire.Code,
			gate.calls,
			authentication.calls,
			questionnaireApplication,
			questionnaire.Body.String(),
		)
	}
	registrationSessionID := createRegistrationApplication.created.Registration.SessionID
	idempotencyKey := runtimeUUID(22)
	registrationRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/xiangwan/sessions/"+registrationSessionID.String()+
			"/registrations",
		strings.NewReader(
			`{"instance_publication_version":1,"price_cents":0,"privacy_policy_version":"privacy-v1","contact":{"name":"Wang Wei","phone_e164":"+8613812345678","policy_version":"contact-v1"},"answers":[]}`,
		),
	)
	registrationRequest.Header.Set("Content-Type", "application/json")
	registrationRequest.Header.Set("Idempotency-Key", idempotencyKey.String())
	registrationResponse := httptest.NewRecorder()
	router.ServeHTTP(registrationResponse, registrationRequest)
	if registrationResponse.Code != http.StatusOK || gate.calls != 7 ||
		authentication.calls != 2 || createRegistrationApplication.calls != 1 ||
		createRegistrationApplication.principalID != authentication.principalID ||
		createRegistrationApplication.sessionID != registrationSessionID ||
		createRegistrationApplication.request.IdempotencyKey != idempotencyKey {
		t.Fatalf(
			"create Registration status=%d gate=%d auth=%d application=%+v body=%s",
			registrationResponse.Code,
			gate.calls,
			authentication.calls,
			createRegistrationApplication,
			registrationResponse.Body.String(),
		)
	}
	myRegistrations := performRuntimeRequest(
		router,
		"/api/v1/xiangwan/me/registrations",
	)
	if myRegistrations.Code != http.StatusOK || gate.calls != 8 ||
		authentication.calls != 3 || myRegistrationsApplication.calls != 1 ||
		myRegistrationsApplication.principalID != authentication.principalID {
		t.Fatalf(
			"My Registrations status=%d gate=%d auth=%d application=%+v body=%s",
			myRegistrations.Code,
			gate.calls,
			authentication.calls,
			myRegistrationsApplication,
			myRegistrations.Body.String(),
		)
	}
	registrationID := runtimeUUID(7)
	myRegistration := performRuntimeRequest(
		router,
		"/api/v1/xiangwan/me/registrations/"+registrationID.String(),
	)
	if myRegistration.Code != http.StatusOK || gate.calls != 9 ||
		authentication.calls != 4 || myRegistrationApplication.calls != 1 ||
		myRegistrationApplication.principalID != authentication.principalID ||
		myRegistrationApplication.registrationID != registrationID {
		t.Fatalf(
			"My Registration status=%d gate=%d auth=%d application=%+v body=%s",
			myRegistration.Code,
			gate.calls,
			authentication.calls,
			myRegistrationApplication,
			myRegistration.Body.String(),
		)
	}
	myOrders := performRuntimeRequest(router, "/api/v1/xiangwan/me/orders")
	if myOrders.Code != http.StatusOK || gate.calls != 10 ||
		authentication.calls != 5 || myOrdersApplication.calls != 1 ||
		myOrdersApplication.principalID != authentication.principalID {
		t.Fatalf(
			"My Orders status=%d gate=%d auth=%d application=%+v body=%s",
			myOrders.Code,
			gate.calls,
			authentication.calls,
			myOrdersApplication,
			myOrders.Body.String(),
		)
	}
	orderID := runtimeUUID(8)
	myOrder := performRuntimeRequest(
		router,
		"/api/v1/xiangwan/me/orders/"+orderID.String(),
	)
	if myOrder.Code != http.StatusOK || gate.calls != 11 ||
		authentication.calls != 6 || myOrderApplication.calls != 1 ||
		myOrderApplication.principalID != authentication.principalID ||
		myOrderApplication.orderID != orderID {
		t.Fatalf(
			"My Order status=%d gate=%d auth=%d application=%+v body=%s",
			myOrder.Code,
			gate.calls,
			authentication.calls,
			myOrderApplication,
			myOrder.Body.String(),
		)
	}
	prepayRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/xiangwan/orders/"+prepayOrderID.String()+
			"/wechat-prepay-attempts",
		strings.NewReader(`{"order_version":1,"payable_cents":9900}`),
	)
	prepayRequest.Header.Set("Content-Type", "application/json")
	prepayRequest.Header.Set("Idempotency-Key", runtimeUUID(25).String())
	prepayResponse := httptest.NewRecorder()
	router.ServeHTTP(prepayResponse, prepayRequest)
	if prepayResponse.Code != http.StatusOK || gate.calls != 12 ||
		authentication.calls != 7 || prepayApplication.calls != 1 ||
		prepayApplication.principalID != authentication.principalID ||
		prepayApplication.orderID != prepayOrderID ||
		prepayApplication.request.ExpectedOrderVersion != 1 ||
		prepayApplication.request.ExpectedPayableCents != 9900 {
		t.Fatalf(
			"WeChat prepay status=%d gate=%d auth=%d application=%+v body=%s",
			prepayResponse.Code,
			gate.calls,
			authentication.calls,
			prepayApplication,
			prepayResponse.Body.String(),
		)
	}
	paymentQueryRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/xiangwan/orders/"+prepayOrderID.String()+
			"/payment-queries",
		nil,
	)
	paymentQueryResponse := httptest.NewRecorder()
	router.ServeHTTP(paymentQueryResponse, paymentQueryRequest)
	if paymentQueryResponse.Code != http.StatusOK || gate.calls != 13 ||
		authentication.calls != 8 || paymentQueryApplication.calls != 1 ||
		paymentQueryApplication.principalID != authentication.principalID ||
		paymentQueryApplication.orderID != prepayOrderID {
		t.Fatalf(
			"WeChat payment query status=%d gate=%d auth=%d application=%+v body=%s",
			paymentQueryResponse.Code,
			gate.calls,
			authentication.calls,
			paymentQueryApplication,
			paymentQueryResponse.Body.String(),
		)
	}
	cancellationRegistrationID := runtimeUUID(26)
	cancellationRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/xiangwan/registrations/"+
			cancellationRegistrationID.String()+"/cancellations",
		nil,
	)
	cancellationResponse := httptest.NewRecorder()
	router.ServeHTTP(cancellationResponse, cancellationRequest)
	if cancellationResponse.Code != http.StatusOK || gate.calls != 14 ||
		authentication.calls != 9 || cancellationApplication.calls != 1 ||
		cancellationApplication.principalID != authentication.principalID ||
		cancellationApplication.registrationID != cancellationRegistrationID {
		t.Fatalf(
			"Registration cancellation status=%d gate=%d auth=%d application=%+v body=%s",
			cancellationResponse.Code,
			gate.calls,
			authentication.calls,
			cancellationApplication,
			cancellationResponse.Body.String(),
		)
	}
	myCoupons := performRuntimeRequest(router, "/api/v1/xiangwan/me/coupons")
	if myCoupons.Code != http.StatusOK || gate.calls != 15 ||
		authentication.calls != 10 || myCouponsApplication.calls != 1 ||
		myCouponsApplication.principalID != authentication.principalID {
		t.Fatalf(
			"My Coupons status=%d gate=%d auth=%d application=%+v body=%s",
			myCoupons.Code,
			gate.calls,
			authentication.calls,
			myCouponsApplication,
			myCoupons.Body.String(),
		)
	}
	peopleList := performRuntimeRequest(router, "/api/v1/xiangwan/people")
	if peopleList.Code != http.StatusOK || gate.calls != 16 ||
		authentication.calls != 10 || peopleApplication.listCalls != 1 {
		t.Fatalf(
			"People list status=%d gate=%d auth=%d application=%+v body=%s",
			peopleList.Code,
			gate.calls,
			authentication.calls,
			peopleApplication,
			peopleList.Body.String(),
		)
	}
	peopleID := runtimeUUID(28)
	person := performRuntimeRequest(
		router,
		"/api/v1/xiangwan/people/"+peopleID.String(),
	)
	if person.Code != http.StatusOK || gate.calls != 17 ||
		authentication.calls != 10 || peopleApplication.readCalls != 1 ||
		peopleApplication.peopleID != peopleID {
		t.Fatalf(
			"Person detail status=%d gate=%d auth=%d application=%+v body=%s",
			person.Code,
			gate.calls,
			authentication.calls,
			peopleApplication,
			person.Body.String(),
		)
	}
	myBenefits := performRuntimeRequest(router, "/api/v1/xiangwan/me/benefits")
	if myBenefits.Code != http.StatusOK || gate.calls != 18 ||
		authentication.calls != 11 || myBenefitsApplication.calls != 1 ||
		myBenefitsApplication.principalID != authentication.principalID {
		t.Fatalf(
			"My Benefits status=%d gate=%d auth=%d application=%+v body=%s",
			myBenefits.Code,
			gate.calls,
			authentication.calls,
			myBenefitsApplication,
			myBenefits.Body.String(),
		)
	}
	myHostApplications := performRuntimeRequest(
		router,
		"/api/v1/xiangwan/me/host-applications",
	)
	if myHostApplications.Code != http.StatusOK || gate.calls != 19 ||
		authentication.calls != 12 || myBenefitsApplication.calls != 2 ||
		myBenefitsApplication.principalID != authentication.principalID {
		t.Fatalf(
			"My Host Applications status=%d gate=%d auth=%d application=%+v body=%s",
			myHostApplications.Code,
			gate.calls,
			authentication.calls,
			myBenefitsApplication,
			myHostApplications.Body.String(),
		)
	}
	hostApplicationRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/xiangwan/me/host-applications",
		strings.NewReader(`{
			"personal_introduction":"intro",
			"relevant_experience":"experience",
			"availability":"evenings",
			"contact_method":"contact"
		}`),
	)
	hostApplicationRequest.Header.Set("Content-Type", "application/json")
	hostApplicationResponse := httptest.NewRecorder()
	router.ServeHTTP(hostApplicationResponse, hostApplicationRequest)
	if hostApplicationResponse.Code != http.StatusOK || gate.calls != 20 ||
		authentication.calls != 13 || hostApplicationSubmission.calls != 1 ||
		hostApplicationSubmission.principalID != authentication.principalID {
		t.Fatalf(
			"Host Application submit status=%d gate=%d auth=%d application=%+v body=%s",
			hostApplicationResponse.Code,
			gate.calls,
			authentication.calls,
			hostApplicationSubmission,
			hostApplicationResponse.Body.String(),
		)
	}
	credentialRegistrationID := runtimeUUID(29)
	credentialRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/xiangwan/registrations/"+credentialRegistrationID.String()+
			"/checkin-credentials",
		nil,
	)
	credentialResponse := httptest.NewRecorder()
	router.ServeHTTP(credentialResponse, credentialRequest)
	if credentialResponse.Code != http.StatusOK || gate.calls != 21 ||
		authentication.calls != 14 || checkinCredentialApplication.calls != 1 ||
		checkinCredentialApplication.principalID != authentication.principalID ||
		checkinCredentialApplication.registrationID != credentialRegistrationID {
		t.Fatalf(
			"Checkin credential status=%d gate=%d auth=%d application=%+v body=%s",
			credentialResponse.Code,
			gate.calls,
			authentication.calls,
			checkinCredentialApplication,
			credentialResponse.Body.String(),
		)
	}
	seriesRouteID := runtimeUUID(37)
	seriesSessions := performRuntimeRequest(
		router,
		"/api/v1/xiangwan/series/"+seriesRouteID.String()+"/sessions",
	)
	if seriesSessions.Code != http.StatusOK || gate.calls != 22 ||
		authentication.calls != 14 || sessionsApplication.calls != 2 ||
		sessionsApplication.seriesID != seriesRouteID {
		t.Fatalf(
			"Series Sessions status=%d gate=%d auth=%d application=%+v body=%s",
			seriesSessions.Code,
			gate.calls,
			authentication.calls,
			sessionsApplication,
			seriesSessions.Body.String(),
		)
	}
	identityHistory := performRuntimeRequest(
		router,
		"/api/v1/xiangwan/me/identity-history",
	)
	if identityHistory.Code != http.StatusOK || gate.calls != 23 ||
		authentication.calls != 15 || myBenefitsApplication.calls != 3 ||
		myBenefitsApplication.principalID != authentication.principalID {
		t.Fatalf(
			"Identity History status=%d gate=%d auth=%d application=%+v body=%s",
			identityHistory.Code,
			gate.calls,
			authentication.calls,
			myBenefitsApplication,
			identityHistory.Body.String(),
		)
	}
	favoriteSeriesID := runtimeUUID(38)
	favoriteRequest := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/xiangwan/series/"+favoriteSeriesID.String()+"/favorite",
		nil,
	)
	favoriteResponse := httptest.NewRecorder()
	router.ServeHTTP(favoriteResponse, favoriteRequest)
	if favoriteResponse.Code != http.StatusOK || gate.calls != 24 ||
		authentication.calls != 16 || seriesFavoriteApplication.calls != 1 ||
		seriesFavoriteApplication.principalID != authentication.principalID ||
		seriesFavoriteApplication.seriesID != favoriteSeriesID ||
		!seriesFavoriteApplication.favorited {
		t.Fatalf(
			"Series favorite status=%d gate=%d auth=%d application=%+v body=%s",
			favoriteResponse.Code,
			gate.calls,
			authentication.calls,
			seriesFavoriteApplication,
			favoriteResponse.Body.String(),
		)
	}
	unfavoriteRequest := httptest.NewRequest(
		http.MethodDelete,
		"/api/v1/xiangwan/series/"+favoriteSeriesID.String()+"/favorite",
		nil,
	)
	unfavoriteResponse := httptest.NewRecorder()
	router.ServeHTTP(unfavoriteResponse, unfavoriteRequest)
	if unfavoriteResponse.Code != http.StatusOK || gate.calls != 25 ||
		authentication.calls != 17 || seriesFavoriteApplication.calls != 2 ||
		seriesFavoriteApplication.favorited {
		t.Fatalf(
			"Series unfavorite status=%d gate=%d auth=%d application=%+v body=%s",
			unfavoriteResponse.Code,
			gate.calls,
			authentication.calls,
			seriesFavoriteApplication,
			unfavoriteResponse.Body.String(),
		)
	}
	myFavorites := performRuntimeRequest(router, "/api/v1/xiangwan/me/favorites")
	if myFavorites.Code != http.StatusOK || gate.calls != 26 ||
		authentication.calls != 18 || myFavoritesApplication.calls != 1 ||
		myFavoritesApplication.principalID != authentication.principalID {
		t.Fatalf(
			"My Favorites status=%d gate=%d auth=%d application=%+v body=%s",
			myFavorites.Code,
			gate.calls,
			authentication.calls,
			myFavoritesApplication,
			myFavorites.Body.String(),
		)
	}
	publicPolicies := performRuntimeRequest(
		router,
		"/api/v1/xiangwan/public-policies",
	)
	if publicPolicies.Code != http.StatusOK || gate.calls != 27 ||
		authentication.calls != 18 || publicPoliciesApplication.calls != 1 {
		t.Fatalf(
			"Public Policies status=%d gate=%d auth=%d application=%+v body=%s",
			publicPolicies.Code,
			gate.calls,
			authentication.calls,
			publicPoliciesApplication,
			publicPolicies.Body.String(),
		)
	}
	dataRights := performRuntimeRequest(
		router,
		"/api/v1/xiangwan/me/data-rights-requests",
	)
	if dataRights.Code != http.StatusOK || gate.calls != 28 ||
		authentication.calls != 19 || dataRightsApplication.listCalls != 1 ||
		dataRightsApplication.principalID != authentication.principalID {
		t.Fatalf(
			"Data Rights status=%d gate=%d auth=%d application=%+v body=%s",
			dataRights.Code,
			gate.calls,
			authentication.calls,
			dataRightsApplication,
			dataRights.Body.String(),
		)
	}
	consumerProfile := performRuntimeRequest(
		router,
		"/api/v1/xiangwan/me/profile",
	)
	if consumerProfile.Code != http.StatusOK || gate.calls != 29 ||
		authentication.calls != 20 || consumerProfileApplication.getCalls != 1 ||
		consumerProfileApplication.principalID != authentication.principalID {
		t.Fatalf(
			"ConsumerProfile status=%d gate=%d auth=%d application=%+v body=%s",
			consumerProfile.Code,
			gate.calls,
			authentication.calls,
			consumerProfileApplication,
			consumerProfile.Body.String(),
		)
	}
	loginRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/wechat/login",
		strings.NewReader(`{"app_id":"`+testRuntimeAppID+`","code":"single-use-code"}`),
	)
	loginRequest.Header.Set("Content-Type", "application/json")
	loginResponse := httptest.NewRecorder()
	router.ServeHTTP(loginResponse, loginRequest)
	if loginResponse.Code != http.StatusOK || gate.calls != 30 ||
		authentication.calls != 20 || weChatLoginApplication.calls != 1 ||
		weChatLoginApplication.code != "single-use-code" {
		t.Fatalf(
			"WeChat Login status=%d gate=%d auth=%d application=%+v body=%s",
			loginResponse.Code,
			gate.calls,
			authentication.calls,
			weChatLoginApplication,
			loginResponse.Body.String(),
		)
	}
}

func TestRuntimeRouterFailsClosedWhenGenerationIsUnavailable(t *testing.T) {
	t.Parallel()

	instanceID := runtimeUUID(10)
	application := &fakeRuntimeReviewApplication{}
	homeApplication := &fakeRuntimeHomeApplication{}
	sessionsApplication := &fakeRuntimeSessionsApplication{}
	sessionApplication := &fakeRuntimeSessionApplication{}
	mediaApplication := &fakeRuntimeMediaApplication{}
	peopleApplication := &fakeRuntimePeopleApplication{}
	publicPoliciesApplication := &fakeRuntimePublicPoliciesApplication{}
	weChatLoginApplication := &fakeRuntimeWeChatLoginApplication{}
	authentication := &fakeRuntimeAuthentication{principalID: runtimeUUID(17)}
	myRegistrationsApplication := &fakeRuntimeMyRegistrationsApplication{}
	myRegistrationApplication := &fakeRuntimeMyRegistrationApplication{}
	myOrdersApplication := &fakeRuntimeMyOrdersApplication{}
	myOrderApplication := &fakeRuntimeMyOrderApplication{}
	myCouponsApplication := &fakeRuntimeMyCouponsApplication{}
	myFavoritesApplication := &fakeRuntimeMyFavoritesApplication{}
	dataRightsApplication := &fakeRuntimeDataRightsApplication{}
	consumerProfileApplication := &fakeRuntimeConsumerProfileApplication{}
	myBenefitsApplication := &fakeRuntimeMyBenefitsApplication{}
	hostApplicationSubmission := &fakeRuntimeHostApplicationSubmissionApplication{}
	seriesFavoriteApplication := &fakeRuntimeSeriesFavoriteApplication{}
	checkinCredentialApplication := &fakeRuntimeCheckinCredentialApplication{}
	questionnaireApplication := &fakeRuntimeQuestionnaireApplication{}
	prepayApplication := &fakeRuntimeWeChatPrepayApplication{}
	notificationDecoder := &fakeRuntimeWeChatPaymentNotificationDecoder{}
	notificationApplication := &fakeRuntimeWeChatPaymentNotificationApplication{}
	gate := &fakeGenerationGate{err: errors.New("private database failure")}
	router, err := NewRouter(RouterDependencies{
		TenantID:       runtimeUUID(11),
		GenerationID:   runtimeUUID(12),
		GenerationGate: gate,
		PublicHome:     xiangwanapi.NewPublicHomeHandler(homeApplication),
		PublicPastActivities: new(
			xiangwanapi.PublicPastActivitiesHandler,
		),
		PublicSessions: xiangwanapi.NewPublicSessionCollectionHandler(
			sessionsApplication,
		),
		PublicSession: xiangwanapi.NewPublicSessionDetailHandler(
			sessionApplication,
		),
		PublicReview: xiangwanapi.NewPublicReviewHandler(application),
		PublicMedia: xiangwanapi.NewPublicMediaHandler(
			mediaApplication,
		),
		PublicPeople:   xiangwanapi.NewPublicPeopleHandler(peopleApplication),
		PublicPolicies: xiangwanapi.NewPublicPoliciesHandler(publicPoliciesApplication),
		BrandHero:      new(xiangwanapi.BrandHeroHandler),
		CoverImages:    new(xiangwanapi.CoverImageHandler),
		WeChatLogin: xiangwanapi.NewWeChatLoginHandler(
			weChatLoginApplication,
			testRuntimeAppID,
			"privacy-v1",
			nil,
		),
		WeChatLoginThrottle: allowRuntimeWeChatLogin,
		RequirePrincipal:    authentication.Middleware,
		Questionnaire: xiangwanapi.NewSessionQuestionnaireHandler(
			questionnaireApplication,
			authentication.Resolve,
		),
		CreateRegistration: xiangwanapi.NewCreateRegistrationHandler(
			&fakeRuntimeCreateRegistrationApplication{},
			authentication.Resolve,
		),
		WeChatPrepay: xiangwanapi.NewWeChatPrepayHandler(
			prepayApplication,
			authentication.Resolve,
		),
		WeChatPaymentQuery: xiangwanapi.NewWeChatPaymentQueryHandler(
			&fakeRuntimeWeChatPaymentQueryApplication{},
			authentication.Resolve,
		),
		WeChatPaymentNotification: xiangwanapi.NewWeChatPaymentNotificationHandler(
			notificationDecoder,
			notificationApplication,
		),
		MyRegistrations: xiangwanapi.NewMyRegistrationsHandler(
			myRegistrationsApplication,
			authentication.Resolve,
		),
		MyRegistration: xiangwanapi.NewMyRegistrationDetailHandler(
			myRegistrationApplication,
			authentication.Resolve,
		),
		RegistrationCancellation: xiangwanapi.NewRegistrationCancellationHandler(
			&fakeRuntimeRegistrationCancellationApplication{},
			authentication.Resolve,
		),
		CheckinCredential: xiangwanapi.NewCheckinCredentialHandler(
			checkinCredentialApplication,
			authentication.Resolve,
		),
		MyOrders: xiangwanapi.NewMyOrdersHandler(
			myOrdersApplication,
			authentication.Resolve,
		),
		MyOrder: xiangwanapi.NewMyOrderDetailHandler(
			myOrderApplication,
			authentication.Resolve,
		),
		MyCoupons: xiangwanapi.NewMyCouponsHandler(
			myCouponsApplication,
			authentication.Resolve,
		),
		MyFavorites: xiangwanapi.NewMyFavoritesHandler(
			myFavoritesApplication,
			authentication.Resolve,
		),
		DataRights: xiangwanapi.NewDataRightsHandler(
			dataRightsApplication,
			authentication.Resolve,
		),
		ConsumerProfile: xiangwanapi.NewConsumerProfileHandler(
			consumerProfileApplication,
			authentication.Resolve,
		),
		ConsumerIdentity: new(xiangwanapi.ConsumerIdentityHandler),
		MyBenefits: xiangwanapi.NewMyBenefitsHandler(
			myBenefitsApplication,
			authentication.Resolve,
		),
		HostApplicationSubmission: xiangwanapi.NewHostApplicationSubmissionHandler(
			hostApplicationSubmission,
			authentication.Resolve,
		),
		SeriesFavorite: xiangwanapi.NewSeriesFavoriteHandler(
			seriesFavoriteApplication,
			authentication.Resolve,
		),
	})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}
	for _, path := range []string{
		"/ready",
		"/api/v1/xiangwan/home-sessions",
		"/api/v1/xiangwan/series/" + runtimeUUID(30).String() + "/sessions",
		"/api/v1/xiangwan/instances/" + instanceID.String() + "/sessions",
		"/api/v1/xiangwan/sessions/" + runtimeUUID(16).String(),
		"/api/v1/xiangwan/instances/" + instanceID.String() + "/review",
		"/api/v1/xiangwan/media/" + runtimeUUID(13).String() + "/" +
			runtimeUUID(14).String() + "/" + runtimeUUID(15).String(),
		"/api/v1/xiangwan/people",
		"/api/v1/xiangwan/people/" + runtimeUUID(28).String(),
		"/api/v1/xiangwan/public-policies",
		"/api/v1/xiangwan/sessions/" + runtimeUUID(20).String() +
			"/questionnaire",
		"/api/v1/xiangwan/me/registrations",
		"/api/v1/xiangwan/me/registrations/" + runtimeUUID(18).String(),
		"/api/v1/xiangwan/me/orders",
		"/api/v1/xiangwan/me/orders/" + runtimeUUID(19).String(),
		"/api/v1/xiangwan/me/coupons",
		"/api/v1/xiangwan/me/favorites",
		"/api/v1/xiangwan/me/data-rights-requests",
		"/api/v1/xiangwan/me/profile",
		"/api/v1/xiangwan/me/benefits",
		"/api/v1/xiangwan/me/host-applications",
		"/api/v1/xiangwan/me/identity-history",
	} {
		responseRecorder := performRuntimeRequest(router, path)
		if responseRecorder.Code != http.StatusServiceUnavailable ||
			!strings.Contains(responseRecorder.Body.String(), `"code":10006`) ||
			strings.Contains(responseRecorder.Body.String(), "database") {
			t.Fatalf("GET %s status=%d body=%s", path, responseRecorder.Code, responseRecorder.Body.String())
		}
	}
	loginRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/auth/wechat/login",
		strings.NewReader(`{"app_id":"`+testRuntimeAppID+`","code":"single-use-code"}`),
	)
	loginRequest.Header.Set("Content-Type", "application/json")
	loginResponse := httptest.NewRecorder()
	router.ServeHTTP(loginResponse, loginRequest)
	if loginResponse.Code != http.StatusServiceUnavailable ||
		weChatLoginApplication.calls != 0 {
		t.Fatalf(
			"gated WeChat Login status=%d application=%+v body=%s",
			loginResponse.Code,
			weChatLoginApplication,
			loginResponse.Body.String(),
		)
	}
	gateCallsBeforeNotification := gate.calls
	notificationRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/xiangwan/integrations/wechat-pay/notifications",
		strings.NewReader(`{"signed":"encrypted"}`),
	)
	notificationRequest.Header.Set("Content-Type", "application/json")
	notificationRequest.Header.Set("Wechatpay-Serial", "PUB_KEY_ID_TEST")
	notificationRequest.Header.Set("Wechatpay-Signature", "synthetic-signature")
	notificationRequest.Header.Set("Wechatpay-Timestamp", "1789344000")
	notificationRequest.Header.Set("Wechatpay-Nonce", "synthetic-nonce")
	notificationResponse := httptest.NewRecorder()
	router.ServeHTTP(notificationResponse, notificationRequest)
	if notificationResponse.Code != http.StatusNoContent ||
		gate.calls != gateCallsBeforeNotification || authentication.calls != 0 ||
		notificationDecoder.calls != 1 || notificationApplication.calls != 1 {
		t.Fatalf(
			"ungated notification status=%d gate=%d/%d auth=%d decoder=%d application=%d body=%s",
			notificationResponse.Code,
			gate.calls,
			gateCallsBeforeNotification,
			authentication.calls,
			notificationDecoder.calls,
			notificationApplication.calls,
			notificationResponse.Body.String(),
		)
	}
	prepayRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/xiangwan/orders/"+runtimeUUID(26).String()+
			"/wechat-prepay-attempts",
		strings.NewReader(`{"order_version":1,"payable_cents":9900}`),
	)
	prepayRequest.Header.Set("Content-Type", "application/json")
	prepayRequest.Header.Set("Idempotency-Key", runtimeUUID(27).String())
	prepayResponse := httptest.NewRecorder()
	router.ServeHTTP(prepayResponse, prepayRequest)
	if prepayResponse.Code != http.StatusServiceUnavailable ||
		!strings.Contains(prepayResponse.Body.String(), `"code":10006`) {
		t.Fatalf(
			"gated WeChat prepay status=%d body=%s",
			prepayResponse.Code,
			prepayResponse.Body.String(),
		)
	}
	credentialRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/xiangwan/registrations/"+runtimeUUID(29).String()+
			"/checkin-credentials",
		nil,
	)
	credentialResponse := httptest.NewRecorder()
	router.ServeHTTP(credentialResponse, credentialRequest)
	if credentialResponse.Code != http.StatusServiceUnavailable ||
		!strings.Contains(credentialResponse.Body.String(), `"code":10006`) {
		t.Fatalf(
			"gated Checkin credential status=%d body=%s",
			credentialResponse.Code,
			credentialResponse.Body.String(),
		)
	}
	hostApplicationRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/xiangwan/me/host-applications",
		strings.NewReader(`{
			"personal_introduction":"intro",
			"relevant_experience":"experience",
			"availability":"evenings",
			"contact_method":"contact"
		}`),
	)
	hostApplicationRequest.Header.Set("Content-Type", "application/json")
	hostApplicationResponse := httptest.NewRecorder()
	router.ServeHTTP(hostApplicationResponse, hostApplicationRequest)
	if hostApplicationResponse.Code != http.StatusServiceUnavailable ||
		!strings.Contains(hostApplicationResponse.Body.String(), `"code":10006`) {
		t.Fatalf(
			"gated Host Application submit status=%d body=%s",
			hostApplicationResponse.Code,
			hostApplicationResponse.Body.String(),
		)
	}
	favoriteRequest := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/xiangwan/series/"+runtimeUUID(31).String()+"/favorite",
		nil,
	)
	favoriteResponse := httptest.NewRecorder()
	router.ServeHTTP(favoriteResponse, favoriteRequest)
	if favoriteResponse.Code != http.StatusServiceUnavailable ||
		!strings.Contains(favoriteResponse.Body.String(), `"code":10006`) {
		t.Fatalf(
			"gated Series favorite status=%d body=%s",
			favoriteResponse.Code,
			favoriteResponse.Body.String(),
		)
	}
	if homeApplication.calls != 0 || sessionsApplication.calls != 0 ||
		sessionApplication.calls != 0 ||
		application.calls != 0 ||
		mediaApplication.calls != 0 || peopleApplication.listCalls != 0 ||
		peopleApplication.readCalls != 0 || publicPoliciesApplication.calls != 0 ||
		authentication.calls != 0 ||
		questionnaireApplication.calls != 0 ||
		myRegistrationsApplication.calls != 0 ||
		myRegistrationApplication.calls != 0 || myOrdersApplication.calls != 0 ||
		myOrderApplication.calls != 0 || myCouponsApplication.calls != 0 ||
		myFavoritesApplication.calls != 0 || dataRightsApplication.listCalls != 0 ||
		consumerProfileApplication.getCalls != 0 ||
		myBenefitsApplication.calls != 0 || hostApplicationSubmission.calls != 0 ||
		seriesFavoriteApplication.calls != 0 ||
		checkinCredentialApplication.calls != 0 ||
		prepayApplication.calls != 0 {
		t.Fatalf(
			"generation gate allowed home=%d Sessions=%d Session=%d review=%d media=%d People=%d/%d Public Policies=%d auth=%d questionnaire=%d My Registrations=%d My Registration=%d My Orders=%d My Order=%d My Coupons=%d My Favorites=%d Data Rights=%d ConsumerProfile=%d My Benefits=%d Host Application=%d Series Favorite=%d Checkin credential=%d calls",
			homeApplication.calls,
			sessionsApplication.calls,
			sessionApplication.calls,
			application.calls,
			mediaApplication.calls,
			peopleApplication.listCalls,
			peopleApplication.readCalls,
			publicPoliciesApplication.calls,
			authentication.calls,
			questionnaireApplication.calls,
			myRegistrationsApplication.calls,
			myRegistrationApplication.calls,
			myOrdersApplication.calls,
			myOrderApplication.calls,
			myCouponsApplication.calls,
			myFavoritesApplication.calls,
			dataRightsApplication.listCalls,
			consumerProfileApplication.getCalls,
			myBenefitsApplication.calls,
			hostApplicationSubmission.calls,
			seriesFavoriteApplication.calls,
			checkinCredentialApplication.calls,
		)
	}
}

func TestRuntimeRouterUsesStableUnknownRouteEnvelope(t *testing.T) {
	t.Parallel()

	authentication := &fakeRuntimeAuthentication{principalID: runtimeUUID(22)}
	router, err := NewRouter(RouterDependencies{
		TenantID:       runtimeUUID(20),
		GenerationID:   runtimeUUID(21),
		GenerationGate: &fakeGenerationGate{},
		PublicHome: xiangwanapi.NewPublicHomeHandler(
			&fakeRuntimeHomeApplication{},
		),
		PublicPastActivities: new(
			xiangwanapi.PublicPastActivitiesHandler,
		),
		PublicSessions: xiangwanapi.NewPublicSessionCollectionHandler(
			&fakeRuntimeSessionsApplication{},
		),
		PublicSession: xiangwanapi.NewPublicSessionDetailHandler(
			&fakeRuntimeSessionApplication{},
		),
		PublicReview: xiangwanapi.NewPublicReviewHandler(
			&fakeRuntimeReviewApplication{},
		),
		PublicMedia: xiangwanapi.NewPublicMediaHandler(
			&fakeRuntimeMediaApplication{},
		),
		PublicPeople: xiangwanapi.NewPublicPeopleHandler(
			&fakeRuntimePeopleApplication{},
		),
		PublicPolicies: xiangwanapi.NewPublicPoliciesHandler(
			&fakeRuntimePublicPoliciesApplication{},
		),
		BrandHero:   new(xiangwanapi.BrandHeroHandler),
		CoverImages: new(xiangwanapi.CoverImageHandler),
		WeChatLogin: xiangwanapi.NewWeChatLoginHandler(
			&fakeRuntimeWeChatLoginApplication{},
			testRuntimeAppID,
			"privacy-v1",
			nil,
		),
		WeChatLoginThrottle: allowRuntimeWeChatLogin,
		RequirePrincipal:    authentication.Middleware,
		Questionnaire: xiangwanapi.NewSessionQuestionnaireHandler(
			&fakeRuntimeQuestionnaireApplication{},
			authentication.Resolve,
		),
		CreateRegistration: xiangwanapi.NewCreateRegistrationHandler(
			&fakeRuntimeCreateRegistrationApplication{},
			authentication.Resolve,
		),
		WeChatPrepay: xiangwanapi.NewWeChatPrepayHandler(
			&fakeRuntimeWeChatPrepayApplication{},
			authentication.Resolve,
		),
		WeChatPaymentQuery: xiangwanapi.NewWeChatPaymentQueryHandler(
			&fakeRuntimeWeChatPaymentQueryApplication{},
			authentication.Resolve,
		),
		WeChatPaymentNotification: xiangwanapi.NewWeChatPaymentNotificationHandler(
			nil,
			nil,
		),
		MyRegistrations: xiangwanapi.NewMyRegistrationsHandler(
			&fakeRuntimeMyRegistrationsApplication{},
			authentication.Resolve,
		),
		MyRegistration: xiangwanapi.NewMyRegistrationDetailHandler(
			&fakeRuntimeMyRegistrationApplication{},
			authentication.Resolve,
		),
		RegistrationCancellation: xiangwanapi.NewRegistrationCancellationHandler(
			&fakeRuntimeRegistrationCancellationApplication{},
			authentication.Resolve,
		),
		CheckinCredential: xiangwanapi.NewCheckinCredentialHandler(
			&fakeRuntimeCheckinCredentialApplication{},
			authentication.Resolve,
		),
		MyOrders: xiangwanapi.NewMyOrdersHandler(
			&fakeRuntimeMyOrdersApplication{},
			authentication.Resolve,
		),
		MyOrder: xiangwanapi.NewMyOrderDetailHandler(
			&fakeRuntimeMyOrderApplication{},
			authentication.Resolve,
		),
		MyCoupons: xiangwanapi.NewMyCouponsHandler(
			&fakeRuntimeMyCouponsApplication{},
			authentication.Resolve,
		),
		MyFavorites: xiangwanapi.NewMyFavoritesHandler(
			&fakeRuntimeMyFavoritesApplication{},
			authentication.Resolve,
		),
		DataRights: xiangwanapi.NewDataRightsHandler(
			&fakeRuntimeDataRightsApplication{},
			authentication.Resolve,
		),
		ConsumerProfile: xiangwanapi.NewConsumerProfileHandler(
			&fakeRuntimeConsumerProfileApplication{},
			authentication.Resolve,
		),
		ConsumerIdentity: new(xiangwanapi.ConsumerIdentityHandler),
		MyBenefits: xiangwanapi.NewMyBenefitsHandler(
			&fakeRuntimeMyBenefitsApplication{},
			authentication.Resolve,
		),
		HostApplicationSubmission: xiangwanapi.NewHostApplicationSubmissionHandler(
			&fakeRuntimeHostApplicationSubmissionApplication{},
			authentication.Resolve,
		),
		SeriesFavorite: xiangwanapi.NewSeriesFavoriteHandler(
			&fakeRuntimeSeriesFavoriteApplication{},
			authentication.Resolve,
		),
	})
	if err != nil {
		t.Fatalf("NewRouter() error = %v", err)
	}
	responseRecorder := performRuntimeRequest(router, "/api/v1/xiangwan/unknown")
	var body struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(responseRecorder.Body.Bytes(), &body); err != nil ||
		responseRecorder.Code != http.StatusNotFound || body.Code != 10004 {
		t.Fatalf("unknown route status=%d body=%s error=%v", responseRecorder.Code, responseRecorder.Body.String(), err)
	}
}

func performRuntimeRequest(router http.Handler, path string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	responseRecorder := httptest.NewRecorder()
	router.ServeHTTP(responseRecorder, request)
	return responseRecorder
}

type fakeGenerationGate struct {
	err error

	calls        int
	tenantID     uuid.UUID
	generationID uuid.UUID
}

func (fake *fakeGenerationGate) CheckActiveGeneration(
	_ context.Context,
	tenantID uuid.UUID,
	generationID uuid.UUID,
) error {
	fake.calls++
	fake.tenantID = tenantID
	fake.generationID = generationID
	return fake.err
}

type fakeRuntimeReviewApplication struct {
	page xiangwanapi.PublicReviewPage
	err  error

	calls int
}

type fakeRuntimeHomeApplication struct {
	calls int
}

type fakeRuntimeSessionApplication struct {
	calls int
}

type fakeRuntimeSessionsApplication struct {
	calls    int
	seriesID uuid.UUID
}

func (fake *fakeRuntimeSessionsApplication) ReadSeriesSessions(
	_ context.Context,
	seriesID uuid.UUID,
) (xiangwanapi.PublicSessionCollectionPage, error) {
	fake.calls++
	fake.seriesID = seriesID
	return xiangwanapi.PublicSessionCollectionPage{
		BrandStatus: activity.BrandLifecycleActive,
		SeriesID:    &seriesID,
		Route: activity.SessionRouteResolution{
			Kind:                activity.SessionRouteUnavailable,
			CandidateSessionIDs: []uuid.UUID{},
		},
		Sessions: []xiangwanapi.PublicInstanceSession{},
	}, nil
}

type fakeRuntimePeopleApplication struct {
	listCalls int
	readCalls int
	peopleID  uuid.UUID
}

func (fake *fakeRuntimePeopleApplication) List(
	_ context.Context,
	_ xiangwanapi.PublicPeopleRequest,
) (people.PublicProfilesPage, error) {
	fake.listCalls++
	return people.PublicProfilesPage{
		Items: []people.Profile{},
		AsOf:  time.Now().UTC(),
	}, nil
}

func (fake *fakeRuntimePeopleApplication) Read(
	_ context.Context,
	peopleID uuid.UUID,
) (people.Profile, error) {
	fake.readCalls++
	fake.peopleID = peopleID
	publishedAt := time.Now().UTC()
	return people.Profile{
		ID:          peopleID,
		DisplayName: "Lin",
		ModeratedAt: &publishedAt,
		Version:     1,
	}, nil
}

type fakeRuntimePublicPoliciesApplication struct {
	value xiangwanapi.PublicPolicies
	err   error
	calls int
}

type fakeRuntimeWeChatLoginApplication struct {
	result identity.LoginResult
	err    error
	calls  int
	code   string
}

func (fake *fakeRuntimePublicPoliciesApplication) Read(
	_ context.Context,
) (xiangwanapi.PublicPolicies, error) {
	fake.calls++
	return fake.value, fake.err
}

func (fake *fakeRuntimeWeChatLoginApplication) Login(
	_ context.Context,
	code string,
) (identity.LoginResult, error) {
	fake.calls++
	fake.code = code
	return fake.result, fake.err
}

type fakeRuntimeQuestionnaireApplication struct {
	questionnaire activity.SessionQuestionnaire
	err           error

	calls       int
	principalID uuid.UUID
	sessionID   uuid.UUID
}

type fakeRuntimeCreateRegistrationApplication struct {
	created xiangwanapi.CreateRegistrationResult
	err     error

	calls       int
	principalID uuid.UUID
	sessionID   uuid.UUID
	request     xiangwanapi.CreateRegistrationRequest
}

type fakeRuntimeWeChatPrepayApplication struct {
	result payment.PrepayAttemptResult
	err    error

	calls       int
	principalID uuid.UUID
	orderID     uuid.UUID
	request     xiangwanapi.WeChatPrepayRequest
}

type fakeRuntimeWeChatPaymentQueryApplication struct {
	calls       int
	principalID uuid.UUID
	orderID     uuid.UUID
}

type fakeRuntimeWeChatPaymentNotificationDecoder struct {
	calls int
}

func (fake *fakeRuntimeWeChatPaymentNotificationDecoder) Decode(
	_ context.Context,
	_ *http.Request,
) (payment.VerifiedPaymentNotification, error) {
	fake.calls++
	return payment.VerifiedPaymentNotification{}, nil
}

type fakeRuntimeWeChatPaymentNotificationApplication struct {
	calls int
}

func (fake *fakeRuntimeWeChatPaymentNotificationApplication) Process(
	_ context.Context,
	_ payment.VerifiedPaymentNotification,
) (payment.PaymentConvergence, error) {
	fake.calls++
	return payment.PaymentConvergence{}, nil
}

func (fake *fakeRuntimeWeChatPaymentQueryApplication) Query(
	_ context.Context,
	principalID uuid.UUID,
	orderID uuid.UUID,
) (payment.PaymentQueryResult, error) {
	fake.calls++
	fake.principalID = principalID
	fake.orderID = orderID
	return payment.PaymentQueryResult{
		Order: payment.Order{
			ID:            orderID,
			PaymentStatus: payment.OrderStatusUnknown,
		},
		QueryStatus: payment.PaymentQueryStatusUnknown,
	}, nil
}

func (fake *fakeRuntimeWeChatPrepayApplication) Create(
	_ context.Context,
	principalID uuid.UUID,
	orderID uuid.UUID,
	request xiangwanapi.WeChatPrepayRequest,
) (payment.PrepayAttemptResult, error) {
	fake.calls++
	fake.principalID = principalID
	fake.orderID = orderID
	fake.request = request
	return fake.result, fake.err
}

func (fake *fakeRuntimeCreateRegistrationApplication) Create(
	_ context.Context,
	principalID uuid.UUID,
	sessionID uuid.UUID,
	request xiangwanapi.CreateRegistrationRequest,
) (xiangwanapi.CreateRegistrationResult, error) {
	fake.calls++
	fake.principalID = principalID
	fake.sessionID = sessionID
	fake.request = request
	return fake.created, fake.err
}

func (fake *fakeRuntimeQuestionnaireApplication) Read(
	_ context.Context,
	principalID uuid.UUID,
	sessionID uuid.UUID,
) (activity.SessionQuestionnaire, error) {
	fake.calls++
	fake.principalID = principalID
	fake.sessionID = sessionID
	return fake.questionnaire, fake.err
}

type fakeRuntimeMyRegistrationsApplication struct {
	page booking.MyRegistrationsPage
	err  error

	calls       int
	principalID uuid.UUID
}

type fakeRuntimeMyRegistrationApplication struct {
	detail booking.MyRegistrationDetail
	err    error

	calls          int
	principalID    uuid.UUID
	registrationID uuid.UUID
}

type fakeRuntimeMyOrdersApplication struct {
	page booking.MyOrdersPage
	err  error

	calls       int
	principalID uuid.UUID
}

type fakeRuntimeRegistrationCancellationApplication struct {
	result registrationpostgres.RegistrationCancellationResult
	err    error

	calls          int
	principalID    uuid.UUID
	registrationID uuid.UUID
}

type fakeRuntimeMyCouponsApplication struct {
	page coupon.MyCouponsPage
	err  error

	calls       int
	principalID uuid.UUID
	request     xiangwanapi.MyCouponsRequest
}

type fakeRuntimeMyFavoritesApplication struct {
	page activity.MyFavoritesPage
	err  error

	calls       int
	principalID uuid.UUID
	request     xiangwanapi.MyFavoritesRequest
}

type fakeRuntimeDataRightsApplication struct {
	submitResult datarightspostgres.SubmissionResult
	histories    []datarights.CaseHistory
	submitErr    error
	listErr      error

	submitCalls int
	listCalls   int
	principalID uuid.UUID
	request     xiangwanapi.DataRightsSubmissionRequest
}

type fakeRuntimeConsumerProfileApplication struct {
	snapshot consumerprofile.Snapshot
	receipt  consumerprofile.MutationReceipt
	err      error

	getCalls    int
	updateCalls int
	principalID uuid.UUID
	request     xiangwanapi.ConsumerProfileUpdateRequest
}

type fakeRuntimeMyBenefitsApplication struct {
	calls       int
	principalID uuid.UUID
}

type fakeRuntimeHostApplicationSubmissionApplication struct {
	calls       int
	principalID uuid.UUID
	request     xiangwanapi.HostApplicationSubmissionRequest
}

type fakeRuntimeSeriesFavoriteApplication struct {
	calls       int
	principalID uuid.UUID
	seriesID    uuid.UUID
	favorited   bool
}

func (fake *fakeRuntimeSeriesFavoriteApplication) Set(
	_ context.Context,
	principalID uuid.UUID,
	seriesID uuid.UUID,
	favorited bool,
) (activity.SeriesFavoriteState, error) {
	fake.calls++
	fake.principalID = principalID
	fake.seriesID = seriesID
	fake.favorited = favorited
	return activity.SeriesFavoriteState{
		TenantID:      runtimeUUID(39),
		PrincipalID:   principalID,
		SeriesID:      seriesID,
		Favorited:     favorited,
		Changed:       true,
		FavoriteCount: 1,
		SeriesVersion: 2,
		OccurredAt:    time.Now().UTC(),
	}, nil
}

func (fake *fakeRuntimeHostApplicationSubmissionApplication) Apply(
	_ context.Context,
	principalID uuid.UUID,
	request xiangwanapi.HostApplicationSubmissionRequest,
) (peoplepostgres.HostApplicationResult, error) {
	fake.calls++
	fake.principalID = principalID
	fake.request = request
	return peoplepostgres.HostApplicationResult{
		Application: people.HostApplication{
			ID:                runtimeUUID(36),
			ApplicationCycle:  "2026-q4",
			PolicyVersion:     "host-rules-v3",
			ApplicationStatus: people.HostApplicationStatusPending,
			Version:           1,
			SubmittedAt:       time.Now().UTC(),
			UpdatedAt:         time.Now().UTC(),
		},
	}, nil
}

type fakeRuntimeCheckinCredentialApplication struct {
	calls          int
	principalID    uuid.UUID
	registrationID uuid.UUID
}

func (fake *fakeRuntimeCheckinCredentialApplication) Issue(
	_ context.Context,
	principalID uuid.UUID,
	registrationID uuid.UUID,
) (checkin.IssuedCredential, error) {
	fake.calls++
	fake.principalID = principalID
	fake.registrationID = registrationID
	now := time.Now().UTC()
	return checkin.IssuedCredential{
		Credential: checkin.Credential{
			RegistrationID:  registrationID,
			SeriesID:        runtimeUUID(32),
			InstanceID:      runtimeUUID(33),
			SessionID:       runtimeUUID(34),
			CredentialJTI:   runtimeUUID(35),
			CredentialEpoch: 1,
			IssuedAt:        now,
			ExpiresAt:       now.Add(10 * time.Minute),
		},
		QRToken:    "one-time-qr-token",
		BackupCode: "ABCD-EFGH-JKMP",
	}, nil
}

func (fake *fakeRuntimeMyBenefitsApplication) Read(
	_ context.Context,
	principalID uuid.UUID,
) (people.MyBenefits, error) {
	fake.calls++
	fake.principalID = principalID
	return people.MyBenefits{
		CurrentRoles:            []people.IdentityRoleSummary{},
		RoleHistory:             []people.IdentityRoleSummary{},
		HostRulesState:          people.HostRulesStatePending,
		HostApplicationHistory:  []people.HostApplicationSummary{},
		HostContributionHistory: []contribution.HistoryItem{},
	}, nil
}

func (fake *fakeRuntimeMyCouponsApplication) Read(
	_ context.Context,
	principalID uuid.UUID,
	request xiangwanapi.MyCouponsRequest,
) (coupon.MyCouponsPage, error) {
	fake.calls++
	fake.principalID = principalID
	fake.request = request
	return fake.page, fake.err
}

func (fake *fakeRuntimeMyFavoritesApplication) Read(
	_ context.Context,
	principalID uuid.UUID,
	request xiangwanapi.MyFavoritesRequest,
) (activity.MyFavoritesPage, error) {
	fake.calls++
	fake.principalID = principalID
	fake.request = request
	return fake.page, fake.err
}

func (fake *fakeRuntimeDataRightsApplication) Submit(
	_ context.Context,
	principalID uuid.UUID,
	request xiangwanapi.DataRightsSubmissionRequest,
) (datarightspostgres.SubmissionResult, error) {
	fake.submitCalls++
	fake.principalID = principalID
	fake.request = request
	return fake.submitResult, fake.submitErr
}

func (fake *fakeRuntimeDataRightsApplication) ListMine(
	_ context.Context,
	principalID uuid.UUID,
) ([]datarights.CaseHistory, error) {
	fake.listCalls++
	fake.principalID = principalID
	return fake.histories, fake.listErr
}

func (fake *fakeRuntimeConsumerProfileApplication) GetMine(
	_ context.Context,
	principalID uuid.UUID,
) (consumerprofile.Snapshot, error) {
	fake.getCalls++
	fake.principalID = principalID
	if fake.snapshot.Principal.ETag == "" {
		fake.snapshot.Principal.ETag = "pp_test"
		fake.snapshot.Published.Fields.Tags = []string{}
	}
	return fake.snapshot, fake.err
}

func (fake *fakeRuntimeConsumerProfileApplication) Update(
	_ context.Context,
	principalID uuid.UUID,
	request xiangwanapi.ConsumerProfileUpdateRequest,
) (consumerprofile.MutationReceipt, error) {
	fake.updateCalls++
	fake.principalID = principalID
	fake.request = request
	return fake.receipt, fake.err
}

func (fake *fakeRuntimeRegistrationCancellationApplication) Cancel(
	_ context.Context,
	principalID uuid.UUID,
	registrationID uuid.UUID,
) (registrationpostgres.RegistrationCancellationResult, error) {
	fake.calls++
	fake.principalID = principalID
	fake.registrationID = registrationID
	if fake.result.Registration.ID == uuid.Nil {
		cancelledAt := time.Date(2026, time.September, 20, 1, 0, 0, 0, time.UTC)
		fake.result.Registration = registration.Registration{
			ID:                  registrationID,
			SessionID:           runtimeUUID(27),
			ParticipationStatus: registration.ParticipationStatusCancelled,
			CancelledAt:         &cancelledAt,
			Version:             2,
		}
	}
	return fake.result, fake.err
}

type fakeRuntimeMyOrderApplication struct {
	detail booking.MyOrderDetail
	err    error

	calls       int
	principalID uuid.UUID
	orderID     uuid.UUID
}

func (fake *fakeRuntimeMyOrderApplication) Read(
	_ context.Context,
	principalID uuid.UUID,
	orderID uuid.UUID,
) (booking.MyOrderDetail, error) {
	fake.calls++
	fake.principalID = principalID
	fake.orderID = orderID
	return fake.detail, fake.err
}

func (fake *fakeRuntimeMyOrdersApplication) Read(
	_ context.Context,
	principalID uuid.UUID,
	_ xiangwanapi.MyOrdersRequest,
) (booking.MyOrdersPage, error) {
	fake.calls++
	fake.principalID = principalID
	return fake.page, fake.err
}

func (fake *fakeRuntimeMyRegistrationApplication) Read(
	_ context.Context,
	principalID uuid.UUID,
	registrationID uuid.UUID,
) (booking.MyRegistrationDetail, error) {
	fake.calls++
	fake.principalID = principalID
	fake.registrationID = registrationID
	return fake.detail, fake.err
}

func (fake *fakeRuntimeMyRegistrationsApplication) Read(
	_ context.Context,
	principalID uuid.UUID,
	_ xiangwanapi.MyRegistrationsRequest,
) (booking.MyRegistrationsPage, error) {
	fake.calls++
	fake.principalID = principalID
	return fake.page, fake.err
}

type fakeRuntimeAuthentication struct {
	principalID uuid.UUID
	err         error
	calls       int
}

func (fake *fakeRuntimeAuthentication) Middleware(c *gin.Context) {
	fake.calls++
	if fake.err != nil {
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}
	c.Next()
}

func (fake *fakeRuntimeAuthentication) Resolve(
	*gin.Context,
) (uuid.UUID, error) {
	return fake.principalID, fake.err
}

func (fake *fakeRuntimeSessionsApplication) ReadInstanceSessions(
	_ context.Context,
	instanceID uuid.UUID,
) (xiangwanapi.PublicSessionCollectionPage, error) {
	fake.calls++
	return xiangwanapi.PublicSessionCollectionPage{
		BrandStatus: activity.BrandLifecycleActive,
		InstanceID:  instanceID,
		Route: activity.SessionRouteResolution{
			Kind:                activity.SessionRouteUnavailable,
			CandidateSessionIDs: []uuid.UUID{},
		},
		Sessions: []xiangwanapi.PublicInstanceSession{},
	}, nil
}

func (fake *fakeRuntimeSessionApplication) ReadSessionDetail(
	_ context.Context,
	sessionID uuid.UUID,
) (xiangwanapi.PublicSessionDetailPage, error) {
	fake.calls++
	now := time.Now().UTC()
	return xiangwanapi.PublicSessionDetailPage{
		BrandStatus: activity.BrandLifecycleActive,
		Detail: activity.SessionDetail{
			SeriesID:                runtimeUUID(30),
			InstanceID:              runtimeUUID(31),
			SessionID:               sessionID,
			PublicationVersion:      1,
			InstanceTitle:           "AI Roundtable",
			SessionTitle:            "Sunday Session",
			ActivityType:            activity.ActivityTypeAIRoundtable,
			QuickTagCodes:           []string{},
			RegistrationStartAt:     now.Add(-time.Hour),
			RegistrationEndAt:       now.Add(time.Hour),
			SessionStartAt:          now.Add(2 * time.Hour),
			SessionEndAt:            now.Add(3 * time.Hour),
			DeliveryMode:            activity.DeliveryModeOnline,
			Area:                    activity.AreaCodeOnline,
			OnlineParticipationMode: "wechat_group_after_registration",
			Display: activity.SessionDisplayDecision{
				State:    activity.DisplayStateOpen,
				Capacity: 20,
			},
			CTA: activity.SessionDetailCTA{
				Action:  activity.SessionDetailCTAActionStartRegistration,
				Label:   activity.SessionDetailCTALabelRegisterNow,
				Enabled: true,
			},
		},
	}, nil
}

func (fake *fakeRuntimeHomeApplication) ReadHome(
	_ context.Context,
	_ activity.HomeFilter,
) (xiangwanapi.PublicHomePage, error) {
	fake.calls++
	return xiangwanapi.PublicHomePage{
		Profile: activity.PublicHomeProfile{
			LifecycleStatus:    activity.BrandLifecycleActive,
			PublicationVersion: 1,
			CommunityName:      "Xiangwan",
			BrandIntro:         "Meet through thoughtful activities.",
			HeroMode:           activity.HomeHeroModeText,
			HeroEyebrow:        "TIANJIN AI COMMUNITY",
			AvailableQuickTags: []activity.HomeQuickTag{},
			PublishedAt:        time.Now().UTC(),
		},
		Catalog: activity.HomeCatalog{
			Cards:              []activity.HomeCard{},
			AvailableQuickTags: []activity.HomeQuickTag{},
			ActiveFilter: activity.HomeFilter{
				ActivityType: activity.ActivityTypeAll,
				Area:         activity.AreaCodeAll,
				TimeWindow:   activity.HomeTimeWindowAll,
				QuickTags:    []string{},
				Limit:        activity.DefaultHomeLimit,
			},
			BusinessTimezone: activity.BusinessTimezone,
			AsOf:             time.Now().UTC(),
		},
	}, nil
}

type fakeRuntimeMediaApplication struct {
	calls int
}

func (fake *fakeRuntimeMediaApplication) Open(
	context.Context,
	uuid.UUID,
	uuid.UUID,
	uuid.UUID,
) (*xiangwanapi.PublicMedia, error) {
	fake.calls++
	return nil, errors.New("media fixture is not configured")
}

func (fake *fakeRuntimeReviewApplication) ReadInstanceReview(
	_ context.Context,
	_ uuid.UUID,
	_ *uuid.UUID,
) (xiangwanapi.PublicReviewPage, error) {
	fake.calls++
	return fake.page, fake.err
}

func runtimeUUID(lastByte byte) uuid.UUID {
	var value uuid.UUID
	value[6] = 0x40
	value[8] = 0x80
	value[len(value)-1] = lastByte
	return value
}
