package xiangwanapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon"
	couponpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon/postgres"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestMyCouponsHandlerReturnsOnlyConsumerSafeOwnedFacts(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 21, 4, 5, 6, 0, time.UTC)
	principalID := apiUUID(220)
	seriesID := apiUUID(221)
	orderID := apiUUID(222)
	registrationID := apiUUID(223)
	policyVersion := "coupon-refund-v2"
	service := &fakeMyCouponsApplication{page: coupon.MyCouponsPage{
		ActiveState: coupon.MyCouponStateAvailable,
		AsOf:        now,
		NextCursor:  "next-owner-cursor",
		Items: []coupon.MyCouponItem{{
			CouponID:           apiUUID(224),
			FaceValueCents:     5000,
			Currency:           coupon.MyCouponsCurrency,
			MinimumOrderCents:  9900,
			ValidFrom:          now.Add(-24 * time.Hour),
			ExpiresAt:          now.Add(30 * 24 * time.Hour),
			GrantKind:          coupon.GrantKindInitialGuest,
			GrantPolicyVersion: "private-grant-policy-version",
			ApplicabilityTarget: coupon.MyCouponApplicabilityTarget{
				ScopeType: coupon.ScopeTypeSeries,
				SeriesID:  &seriesID,
			},
			State:                coupon.MyCouponStateAvailable,
			LedgerStatus:         coupon.StatusAvailable,
			Usable:               true,
			ActiveOrderID:        &orderID,
			ActiveRegistrationID: &registrationID,
			LatestAdjustment: &coupon.MyCouponAdjustment{
				EntryID:       apiUUID(225),
				EntryType:     coupon.EntryTypeRestored,
				PolicyVersion: policyVersion,
				OccurredAt:    now.Add(-time.Hour),
			},
			LastEntryID:       apiUUID(226),
			LastEntrySequence: 4,
			LastEntryType:     coupon.EntryTypeRestored,
			LastBusinessAt:    now.Add(-time.Hour),
			SortRank:          1,
		}},
	}}
	engine := gin.New()
	NewMyCouponsHandler(
		service,
		func(*gin.Context) (uuid.UUID, error) { return principalID, nil },
	).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(
		recorder,
		httptest.NewRequest(
			http.MethodGet,
			"/api/v1/xiangwan/me/coupons?state=available&limit=10",
			nil,
		),
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET My Coupons status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Code int               `json:"code"`
		Data MyCouponsResponse `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode My Coupons response: %v", err)
	}
	if envelope.Code != 0 ||
		envelope.Data.ActiveState != coupon.MyCouponStateAvailable ||
		len(envelope.Data.Items) != 1 ||
		envelope.Data.Items[0].CouponID != apiUUID(224).String() ||
		envelope.Data.Items[0].Applicability.SeriesID != seriesID.String() ||
		envelope.Data.Items[0].ActiveOrderID != orderID.String() ||
		envelope.Data.Items[0].ActiveRegistrationID != registrationID.String() ||
		envelope.Data.Items[0].LatestAdjustment == nil ||
		envelope.Data.Items[0].LatestAdjustment.Disposition !=
			coupon.EntryTypeRestored ||
		envelope.Data.NextCursor != "next-owner-cursor" {
		t.Fatalf("GET My Coupons response = %+v", envelope.Data)
	}
	if service.calls != 1 || service.principalID != principalID ||
		service.request.State != coupon.MyCouponStateAvailable ||
		service.request.Limit != 10 {
		t.Fatalf("service = %+v", service)
	}
	for _, forbidden := range []string{
		"principal_id",
		"tenant_id",
		"private-grant-policy-version",
		policyVersion,
		"entry_id",
		"last_entry",
		"ledger_status",
		"sort_rank",
		"actor_id",
		"reason",
	} {
		if strings.Contains(recorder.Body.String(), forbidden) {
			t.Fatalf("private/internal field %q crossed HTTP boundary: %s", forbidden, recorder.Body.String())
		}
	}
}

func TestMyCouponsHandlerRejectsAmbiguousQuery(t *testing.T) {
	t.Parallel()

	for _, suffix := range []string{
		"?unknown=value",
		"?state=all&state=available",
		"?state=unknown",
		"?state=",
		"?cursor=%20opaque",
		"?limit=0",
		"?limit=01",
		"?limit=101",
	} {
		suffix := suffix
		t.Run(suffix, func(t *testing.T) {
			t.Parallel()
			service := &fakeMyCouponsApplication{}
			engine := gin.New()
			NewMyCouponsHandler(
				service,
				func(*gin.Context) (uuid.UUID, error) { return apiUUID(227), nil },
			).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(
				recorder,
				httptest.NewRequest(
					http.MethodGet,
					"/api/v1/xiangwan/me/coupons"+suffix,
					nil,
				),
			)
			if recorder.Code != http.StatusBadRequest || service.calls != 0 {
				t.Fatalf("status=%d calls=%d body=%s", recorder.Code, service.calls, recorder.Body.String())
			}
		})
	}
}

func TestMyCouponsHandlerMapsStableErrorsAndEmptyState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		serviceErr   error
		principalErr error
		wantStatus   int
	}{
		{name: "invalid cursor", serviceErr: couponpostgres.ErrInvalidMyCouponsCursor, wantStatus: http.StatusBadRequest},
		{name: "stale cursor", serviceErr: couponpostgres.ErrStaleMyCouponsCursor, wantStatus: http.StatusConflict},
		{name: "reader failure", serviceErr: errors.New("private database address"), wantStatus: http.StatusInternalServerError},
		{name: "missing principal", principalErr: errx.NewUnauthorized("missing principal"), wantStatus: http.StatusUnauthorized},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service := &fakeMyCouponsApplication{err: test.serviceErr}
			engine := gin.New()
			NewMyCouponsHandler(
				service,
				func(*gin.Context) (uuid.UUID, error) {
					return apiUUID(228), test.principalErr
				},
			).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(
				recorder,
				httptest.NewRequest(http.MethodGet, "/api/v1/xiangwan/me/coupons", nil),
			)
			if recorder.Code != test.wantStatus ||
				strings.Contains(recorder.Body.String(), "database address") {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}

	service := &fakeMyCouponsApplication{page: coupon.MyCouponsPage{
		ActiveState: coupon.MyCouponStateAll,
		Items:       []coupon.MyCouponItem{},
		AsOf:        time.Now().UTC(),
	}}
	engine := gin.New()
	NewMyCouponsHandler(
		service,
		func(*gin.Context) (uuid.UUID, error) { return apiUUID(229), nil },
	).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/api/v1/xiangwan/me/coupons", nil),
	)
	if recorder.Code != http.StatusOK ||
		!strings.Contains(recorder.Body.String(), `"items":[]`) ||
		!strings.Contains(recorder.Body.String(), `"empty_state":"no_coupons"`) {
		t.Fatalf("empty status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

type fakeMyCouponsApplication struct {
	page coupon.MyCouponsPage
	err  error

	calls       int
	principalID uuid.UUID
	request     MyCouponsRequest
}

func (service *fakeMyCouponsApplication) Read(
	_ context.Context,
	principalID uuid.UUID,
	request MyCouponsRequest,
) (coupon.MyCouponsPage, error) {
	service.calls++
	service.principalID = principalID
	service.request = request
	return service.page, service.err
}
