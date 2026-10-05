package xiangwanruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"

	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	xiangwanapi "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/api"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon"
	couponpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type inventoryPolicyWriter struct{}

func (inventoryPolicyWriter) Activate(context.Context, coupon.PolicyActivationCommand) (coupon.PolicyActivationReceipt, error) {
	return coupon.PolicyActivationReceipt{}, nil
}

type inventoryCouponReplenisher struct{}

func (inventoryCouponReplenisher) Replenish(context.Context, couponpostgres.ManualReplenishmentCommand) (couponpostgres.GrantResult, error) {
	return couponpostgres.GrantResult{}, nil
}

type inventoryCouponCorrectionOperator struct{}

func (inventoryCouponCorrectionOperator) TransitionCouponCorrection(context.Context, xiangwanadmin.CouponCorrectionActionCommand) (xiangwanadmin.CouponCorrectionActionResult, error) {
	return xiangwanadmin.CouponCorrectionActionResult{}, nil
}

type xiangwanRouteInventoryRecord struct {
	Method        string   `json:"method"`
	Path          string   `json:"path"`
	PolicyMarkers []string `json:"policy_markers"`
}

func inventoryRequirePrincipal(*gin.Context) {}
func inventoryLoginThrottle(*gin.Context)    {}

func xiangwanPaymentIdempotencyMarker(method, path string) string {
	switch {
	case method == "POST" && path == "/api/v1/xiangwan/orders/:order_id/wechat-prepay-attempts":
		return "idempotency:xiangwan-wechat-prepay-operation"
	case method == "POST" && path == "/api/v1/xiangwan/orders/:order_id/payment-queries":
		return "idempotency:xiangwan-payment-query-lease"
	case method == "POST" && path == "/api/v1/xiangwan/integrations/wechat-pay/notifications":
		return "idempotency:wechat-callback-trace"
	default:
		return ""
	}
}

func xiangwanRouteInventory(t *testing.T) []xiangwanRouteInventoryRecord {
	t.Helper()
	// Only registration runs. Empty handler shells cannot reach PostgreSQL,
	// credentials, media, or providers, and no HTTP request is dispatched.
	dependencies := xiangwanInventoryDependencies()
	dependencies.AdminEnabled = true
	dependencies.AdminAuth = xiangwanapi.NewDisabledAdminAuthHandler()
	catalog := new(xiangwanapi.AdminCatalogHandler)
	catalog.SetCouponCorrectionOperator(inventoryCouponCorrectionOperator{})
	dependencies.AdminCatalog = catalog
	dependencies.OnsiteCheckin = new(xiangwanapi.OnsiteCheckinHandler)
	engine, err := NewRouter(dependencies)
	if err != nil {
		t.Fatal(err)
	}

	// Match the existing cmd/api emitter: inspect Gin's registered chains so
	// moving a route across an authentication boundary changes the evidence.
	chains := xiangwanRegisteredHandlerChains(t, engine)
	principal := reflect.ValueOf(inventoryRequirePrincipal).Pointer()
	throttle := reflect.ValueOf(inventoryLoginThrottle).Pointer()
	records := make([]xiangwanRouteInventoryRecord, 0, len(chains))
	for _, route := range engine.Routes() {
		handlers, found := chains[route.Method+" "+route.Path]
		if !found {
			t.Fatalf("registered handler chain missing for %s %s", route.Method, route.Path)
		}
		markers := make([]string, 0, 2)
		for _, handler := range handlers {
			function := runtime.FuncForPC(handler)
			if route.Method == "PATCH" && route.Path == "/api/v1/xiangwan/me/registration-contact" && function != nil &&
				strings.Contains(function.Name(), "(*ConsumerIdentityHandler).UpdateRegistrationContact") {
				markers = append(markers, "idempotency:xiangwan-registration-contact-target-state", "ugc-input:text", "content-safety:text-enforce")
			}
			switch handler {
			case principal:
				markers = append(markers, "auth:required")
			case throttle:
				markers = append(markers, "rate-limit:wechat-login")
			}
			if isAdminSessionHandler(handler) {
				markers = append(markers, "auth:admin-session")
			}
			if route.Method == "POST" &&
				route.Path == "/api/v1/xiangwan/admin/instances/:instance_id/copies" &&
				isAdminCopyHandler(handler) {
				markers = append(markers, "idempotency:xiangwan-instance-copy-operation")
			}
			if route.Method == "POST" &&
				route.Path == "/api/v1/xiangwan/admin/refund-cases/:case_id/actions" &&
				isAdminRefundActionHandler(handler) {
				markers = append(markers, "idempotency:xiangwan-refund-event-operation")
			}
			if route.Method == "POST" &&
				route.Path == "/api/v1/xiangwan/admin/coupon-corrections/:entry_id/actions" &&
				isAdminCouponCorrectionActionHandler(handler) {
				markers = append(markers, "idempotency:xiangwan-coupon-correction-operation")
			}
			if route.Method == "POST" &&
				route.Path == "/api/v1/xiangwan/admin/review-resources/:relation_id/photo-curation" &&
				isAdminPhotoCurationHandler(handler) {
				markers = append(markers, "idempotency:xiangwan-photo-curation-operation")
			}
		}
		if marker := xiangwanPaymentIdempotencyMarker(route.Method, route.Path); marker != "" {
			markers = append(markers, marker)
		}
		sort.Strings(markers)
		records = append(records, xiangwanRouteInventoryRecord{route.Method, route.Path, markers})
	}
	sort.Slice(records, func(left, right int) bool {
		if records[left].Path != records[right].Path {
			return records[left].Path < records[right].Path
		}
		return records[left].Method < records[right].Method
	})
	return records
}

func isAdminSessionHandler(handler uintptr) bool {
	function := runtime.FuncForPC(handler)
	return function != nil && strings.Contains(
		function.Name(), "(*AdminAuthHandler).RequireSessionAndCSRF",
	)
}

func isAdminCopyHandler(handler uintptr) bool {
	function := runtime.FuncForPC(handler)
	return function != nil && strings.Contains(
		function.Name(), "(*AdminCatalogHandler).CopyInstance",
	)
}

func isAdminRefundActionHandler(handler uintptr) bool {
	function := runtime.FuncForPC(handler)
	return function != nil && strings.Contains(
		function.Name(), "(*AdminCatalogHandler).TransitionRefund",
	)
}

func isAdminCouponCorrectionActionHandler(handler uintptr) bool {
	function := runtime.FuncForPC(handler)
	return function != nil && strings.Contains(
		function.Name(), "(*AdminCatalogHandler).TransitionCouponCorrection",
	)
}

func isAdminPhotoCurationHandler(handler uintptr) bool {
	function := runtime.FuncForPC(handler)
	return function != nil && strings.Contains(
		function.Name(), "(*AdminCatalogHandler).WriteReviewPhotoCuration",
	)
}

func xiangwanInventoryDependencies() RouterDependencies {
	return RouterDependencies{
		TenantID:                  runtimeUUID(1),
		GenerationID:              runtimeUUID(2),
		GenerationGate:            &fakeGenerationGate{},
		PublicHome:                new(xiangwanapi.PublicHomeHandler),
		PublicPastActivities:      new(xiangwanapi.PublicPastActivitiesHandler),
		PublicSessions:            new(xiangwanapi.PublicSessionCollectionHandler),
		PublicSession:             new(xiangwanapi.PublicSessionDetailHandler),
		PublicReview:              new(xiangwanapi.PublicReviewHandler),
		PublicMedia:               new(xiangwanapi.PublicMediaHandler),
		PublicPeople:              new(xiangwanapi.PublicPeopleHandler),
		PublicPolicies:            new(xiangwanapi.PublicPoliciesHandler),
		BrandHero:                 new(xiangwanapi.BrandHeroHandler),
		CoverImages:               new(xiangwanapi.CoverImageHandler),
		WeChatLogin:               new(xiangwanapi.WeChatLoginHandler),
		WeChatLoginThrottle:       inventoryLoginThrottle,
		RequirePrincipal:          inventoryRequirePrincipal,
		Questionnaire:             new(xiangwanapi.SessionQuestionnaireHandler),
		CreateRegistration:        new(xiangwanapi.CreateRegistrationHandler),
		WeChatPrepay:              new(xiangwanapi.WeChatPrepayHandler),
		WeChatPaymentQuery:        new(xiangwanapi.WeChatPaymentQueryHandler),
		WeChatPaymentNotification: new(xiangwanapi.WeChatPaymentNotificationHandler),
		MyRegistrations:           new(xiangwanapi.MyRegistrationsHandler),
		MyRegistration:            new(xiangwanapi.MyRegistrationDetailHandler),
		RegistrationCancellation:  new(xiangwanapi.RegistrationCancellationHandler),
		CheckinCredential:         new(xiangwanapi.CheckinCredentialHandler),
		MyOrders:                  new(xiangwanapi.MyOrdersHandler),
		MyOrder:                   new(xiangwanapi.MyOrderDetailHandler),
		MyCoupons:                 new(xiangwanapi.MyCouponsHandler),
		MyFavorites:               new(xiangwanapi.MyFavoritesHandler),
		DataRights:                new(xiangwanapi.DataRightsHandler),
		ConsumerProfile:           new(xiangwanapi.ConsumerProfileHandler),
		ConsumerIdentity:          new(xiangwanapi.ConsumerIdentityHandler),
		MyBenefits:                new(xiangwanapi.MyBenefitsHandler),
		HostApplicationSubmission: new(xiangwanapi.HostApplicationSubmissionHandler),
		SeriesFavorite:            new(xiangwanapi.SeriesFavoriteHandler),
	}
}

func xiangwanRegisteredHandlerChains(t *testing.T, engine *gin.Engine) map[string][]uintptr {
	t.Helper()
	chains := make(map[string][]uintptr)
	trees := reflect.ValueOf(engine).Elem().FieldByName("trees")
	if !trees.IsValid() {
		t.Fatal("Gin route tree shape changed")
	}
	for index := 0; index < trees.Len(); index++ {
		tree := trees.Index(index)
		method := tree.FieldByName("method").String()
		var walk func(reflect.Value)
		walk = func(pointer reflect.Value) {
			if pointer.IsNil() {
				return
			}
			node := pointer.Elem()
			handlers := node.FieldByName("handlers")
			if handlers.Len() > 0 {
				pointers := make([]uintptr, 0, handlers.Len())
				for handler := 0; handler < handlers.Len(); handler++ {
					pointers = append(pointers, handlers.Index(handler).Pointer())
				}
				chains[method+" "+node.FieldByName("fullPath").String()] = pointers
			}
			children := node.FieldByName("children")
			for child := 0; child < children.Len(); child++ {
				walk(children.Index(child))
			}
		}
		walk(tree.FieldByName("root"))
	}
	return chains
}

func TestXiangwanAdminRoutesRequireSessionInEnabledRuntime(t *testing.T) {
	dependencies := xiangwanInventoryDependencies()
	dependencies.AdminEnabled = true
	dependencies.AdminAuth = xiangwanapi.NewDisabledAdminAuthHandler()
	catalog := new(xiangwanapi.AdminCatalogHandler)
	catalog.SetCouponCorrectionOperator(inventoryCouponCorrectionOperator{})
	dependencies.AdminCatalog = catalog
	dependencies.OnsiteCheckin = new(xiangwanapi.OnsiteCheckinHandler)
	engine, err := NewRouter(dependencies)
	if err != nil {
		t.Fatal(err)
	}
	chains := xiangwanRegisteredHandlerChains(t, engine)
	hasAdminSession := func(chain []uintptr) bool {
		return slices.ContainsFunc(chain, isAdminSessionHandler)
	}
	protectedCount := 0
	for _, route := range engine.Routes() {
		if !strings.HasPrefix(route.Path, "/api/v1/xiangwan/admin/") {
			continue
		}
		chain := chains[route.Method+" "+route.Path]
		switch route.Path {
		case "/api/v1/xiangwan/admin/auth/login",
			"/api/v1/xiangwan/admin/auth/callback",
			"/api/v1/xiangwan/admin/auth/status":
			if hasAdminSession(chain) {
				t.Errorf("%s %s unexpectedly requires an administrator session", route.Method, route.Path)
			}
			continue
		}
		protectedCount++
		if !hasAdminSession(chain) {
			t.Errorf("%s %s missing administrator session middleware", route.Method, route.Path)
		}
	}
	if protectedCount == 0 {
		t.Fatal("enabled administrator runtime registered no protected routes")
	}
	for _, route := range []string{
		"POST /api/v1/xiangwan/admin/coupon-grant-policies",
		"POST /api/v1/xiangwan/admin/coupon-grants",
		"POST /api/v1/xiangwan/admin/media-upload-intents",
		"PUT /api/v1/xiangwan/admin/media-upload-intents/:file_id/bytes",
		"GET /api/v1/xiangwan/admin/media-upload-intents/:file_id/preview",
		"POST /api/v1/xiangwan/admin/media-confirmations",
	} {
		if _, found := chains[route]; found {
			t.Errorf("conditional route %s is exposed without its enablement", route)
		}
	}
	for _, route := range []string{
		"GET /api/v1/xiangwan/admin/audit-events",
		"GET /api/v1/xiangwan/admin/coupon-corrections",
		"POST /api/v1/xiangwan/admin/coupon-corrections/:entry_id/actions",
		"GET /api/v1/xiangwan/admin/refund-cases",
		"GET /api/v1/xiangwan/admin/refund-cases/:case_id",
		"POST /api/v1/xiangwan/admin/refund-cases/:case_id/actions",
		"GET /api/v1/xiangwan/admin/review-resources/:relation_id/photo-curation",
		"POST /api/v1/xiangwan/admin/review-resources/:relation_id/photo-curation",
		"GET /api/v1/xiangwan/admin/instances/:instance_id/review-photo-curations",
		"GET /api/v1/xiangwan/admin/orders",
		"GET /api/v1/xiangwan/admin/orders/:order_id",
		"POST /api/v1/xiangwan/admin/instances/:instance_id/copies",
		"GET /api/v1/xiangwan/admin/registrations/:registration_id/answers",
	} {
		if !hasAdminSession(chains[route]) {
			t.Errorf("%s missing from the protected administrator route chain", route)
		}
	}
}

func TestConditionalCouponRoutesKeepAdministratorSessionAndCSRFChain(t *testing.T) {
	dependencies := xiangwanInventoryDependencies()
	dependencies.AdminEnabled = true
	dependencies.AdminAuth = xiangwanapi.NewDisabledAdminAuthHandler()
	catalog := new(xiangwanapi.AdminCatalogHandler)
	catalog.SetCouponPolicyActivationWriter(inventoryPolicyWriter{})
	catalog.SetCouponReplenisher(uuid.New(), inventoryCouponReplenisher{})
	dependencies.AdminCatalog = catalog
	dependencies.OnsiteCheckin = new(xiangwanapi.OnsiteCheckinHandler)
	engine, err := NewRouter(dependencies)
	if err != nil {
		t.Fatal(err)
	}
	chains := xiangwanRegisteredHandlerChains(t, engine)
	for _, route := range []string{
		"POST /api/v1/xiangwan/admin/coupon-grant-policies",
		"POST /api/v1/xiangwan/admin/coupon-grants",
	} {
		if !slices.ContainsFunc(chains[route], isAdminSessionHandler) {
			t.Errorf("%s lacks administrator session and CSRF middleware", route)
		}
	}
}

func TestXiangwanRouteInventoryKeepsIndependentAuthBoundaries(t *testing.T) {
	routes := make(map[string][]string)
	for _, route := range xiangwanRouteInventory(t) {
		routes[route.Method+" "+route.Path] = route.PolicyMarkers
	}
	for route, want := range map[string][]string{
		"GET /live":                       {},
		"GET /ready":                      {},
		"GET /api/v1/xiangwan/me/profile": {"auth:required"},
		"GET /api/v1/xiangwan/me/questionnaire-prefill/:session_id":                {"auth:required"},
		"GET /api/v1/xiangwan/me/registration-contact":                             {"auth:required"},
		"PATCH /api/v1/xiangwan/me/registration-contact":                           {"auth:required", "content-safety:text-enforce", "idempotency:xiangwan-registration-contact-target-state", "ugc-input:text"},
		"POST /api/v1/auth/wechat/login":                                           {"rate-limit:wechat-login"},
		"POST /api/v1/xiangwan/integrations/wechat-pay/notifications":              {"idempotency:wechat-callback-trace"},
		"POST /api/v1/xiangwan/orders/:order_id/payment-queries":                   {"auth:required", "idempotency:xiangwan-payment-query-lease"},
		"POST /api/v1/xiangwan/orders/:order_id/wechat-prepay-attempts":            {"auth:required", "idempotency:xiangwan-wechat-prepay-operation"},
		"GET /api/v1/xiangwan/admin/auth/status":                                   {},
		"GET /api/v1/xiangwan/admin/orders":                                        {"auth:admin-session"},
		"GET /api/v1/xiangwan/admin/coupon-corrections":                            {"auth:admin-session"},
		"POST /api/v1/xiangwan/admin/coupon-corrections/:entry_id/actions":         {"auth:admin-session", "idempotency:xiangwan-coupon-correction-operation"},
		"GET /api/v1/xiangwan/admin/refund-cases/:case_id":                         {"auth:admin-session"},
		"POST /api/v1/xiangwan/admin/instances/:instance_id/copies":                {"auth:admin-session", "idempotency:xiangwan-instance-copy-operation"},
		"POST /api/v1/xiangwan/admin/refund-cases/:case_id/actions":                {"auth:admin-session", "idempotency:xiangwan-refund-event-operation"},
		"GET /api/v1/xiangwan/admin/review-resources/:relation_id/photo-curation":  {"auth:admin-session"},
		"GET /api/v1/xiangwan/admin/instances/:instance_id/review-photo-curations": {"auth:admin-session"},
		"POST /api/v1/xiangwan/admin/review-resources/:relation_id/photo-curation": {"auth:admin-session", "idempotency:xiangwan-photo-curation-operation"},
	} {
		got, found := routes[route]
		if !found || !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %v (found=%t), want %v", route, got, found, want)
		}
	}
}

func TestEmitXiangwanRouteInventory(t *testing.T) {
	if os.Getenv("WECONQ_EMIT_ROUTE_INVENTORY") != "1" {
		t.Skip("machine inventory emitter")
	}
	body, err := json.Marshal(xiangwanRouteInventory(t))
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("WECONQ_XIANGWAN_ROUTE_INVENTORY_BEGIN\n%s\nWECONQ_XIANGWAN_ROUTE_INVENTORY_END\n", body)
}
