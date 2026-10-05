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

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/booking"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestMyRegistrationDetailHandlerReturnsExactDecision(t *testing.T) {
	t.Parallel()

	principalID := apiUUID(141)
	registrationID := apiUUID(142)
	receiptID := apiUUID(143)
	asOf := time.Date(2026, time.September, 15, 7, 0, 0, 0, time.UTC)
	service := &fakeMyRegistrationDetailApplication{
		detail: booking.MyRegistrationDetail{
			Item: booking.MyRegistrationItem{
				RegistrationID:      registrationID,
				RegistrationVersion: 3,
				SeriesID:            apiUUID(144),
				SeriesTitle:         "AI Roundtable",
				InstanceID:          apiUUID(145),
				InstanceTitle:       "AI Roundtable 04",
				InstanceStatus:      activity.InstanceStatusCancelled,
				SessionID:           apiUUID(146),
				SessionTitle:        "Sunday Session",
				SessionStatus:       activity.SessionStatusCancelled,
				SessionStartAt:      asOf.Add(24 * time.Hour),
				SessionEndAt:        asOf.Add(26 * time.Hour),
				DeliveryMode:        activity.DeliveryModeOnline,
				ParticipationStatus: registration.ParticipationStatusCancelled,
				Checkin: booking.CheckinSummary{
					Status: booking.CheckinStatusNotRecorded,
				},
				State:          booking.MyRegistrationStateCancelled,
				LastBusinessAt: asOf.Add(-time.Hour),
			},
			Contact: booking.MyRegistrationContact{
				Name:        "王薇",
				PhoneMasked: "+861****5678",
			},
			Cancellation: &booking.MyRegistrationCancellationSummary{
				ReceiptID: &receiptID,
				Scope:     booking.MyRegistrationCancellationScopeInstance,
				Reason:    "Instance cancelled",
				At:        asOf.Add(-time.Hour),
			},
			CouponAdjustment: &booking.MyRegistrationCouponAdjustment{
				Disposition:   coupon.RefundDispositionForfeit,
				PolicyVersion: "coupon-refund-v2",
				OccurredAt:    asOf.Add(-30 * time.Minute),
			},
			AccessDenial:              booking.MyRegistrationAccessInstanceCancelled,
			CheckinCredentialEligible: false,
			PrivateAccessEligible:     false,
			CancellationAction:        booking.MyRegistrationCancellationActionUnavailable,
			AsOf:                      asOf,
		},
	}
	engine := gin.New()
	NewMyRegistrationDetailHandler(
		service,
		func(*gin.Context) (uuid.UUID, error) { return principalID, nil },
	).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(
		recorder,
		httptest.NewRequest(
			http.MethodGet,
			"/api/v1/xiangwan/me/registrations/"+registrationID.String(),
			nil,
		),
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET My Registration status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Code int                          `json:"code"`
		Data MyRegistrationDetailResponse `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode My Registration response: %v", err)
	}
	if envelope.Code != 0 ||
		envelope.Data.Registration.RegistrationID != registrationID.String() ||
		envelope.Data.Registration.SessionID != apiUUID(146).String() ||
		envelope.Data.Contact.Name != "王薇" ||
		envelope.Data.Contact.PhoneMasked != "+861****5678" ||
		envelope.Data.Cancellation == nil ||
		envelope.Data.Cancellation.Scope != booking.MyRegistrationCancellationScopeInstance ||
		envelope.Data.CouponAdjustment == nil ||
		envelope.Data.CouponAdjustment.Disposition != coupon.RefundDispositionForfeit ||
		envelope.Data.CouponAdjustment.PolicyVersion != "coupon-refund-v2" ||
		envelope.Data.AccessDenial != booking.MyRegistrationAccessInstanceCancelled ||
		envelope.Data.CancellationAction != booking.MyRegistrationCancellationActionUnavailable ||
		envelope.Data.CheckinCredentialEligible || envelope.Data.PrivateAccessEligible {
		t.Fatalf("GET My Registration response = %+v", envelope.Data)
	}
	if service.calls != 1 || service.principalID != principalID ||
		service.registrationID != registrationID {
		t.Fatalf("service = %+v", service)
	}
	for _, forbidden := range []string{
		"principal_id",
		"tenant_id",
		"receipt_id",
		"phone_e164",
		"+8613812345678",
		"sort_rank",
		"sort_at",
	} {
		if strings.Contains(recorder.Body.String(), forbidden) {
			t.Fatalf("private/internal field %q crossed HTTP boundary: %s", forbidden, recorder.Body.String())
		}
	}
	if !strings.Contains(
		recorder.Body.String(),
		`"cancellation_policy_required":false`,
	) {
		t.Fatalf("final policy decision missing from response: %s", recorder.Body.String())
	}
}

func TestMyRegistrationDetailHandlerRejectsInvalidIdentityBeforeRead(t *testing.T) {
	t.Parallel()

	for _, suffix := range []string{
		"not-a-uuid",
		strings.ToUpper("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"),
		apiUUID(147).String() + "?unexpected=true",
	} {
		suffix := suffix
		t.Run(suffix, func(t *testing.T) {
			t.Parallel()
			service := &fakeMyRegistrationDetailApplication{}
			engine := gin.New()
			NewMyRegistrationDetailHandler(
				service,
				func(*gin.Context) (uuid.UUID, error) { return apiUUID(148), nil },
			).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(
				recorder,
				httptest.NewRequest(
					http.MethodGet,
					"/api/v1/xiangwan/me/registrations/"+suffix,
					nil,
				),
			)
			if recorder.Code != http.StatusBadRequest || service.calls != 0 {
				t.Fatalf("invalid identity status=%d calls=%d body=%s", recorder.Code, service.calls, recorder.Body.String())
			}
		})
	}
}

func TestMyRegistrationDetailHandlerUsesOpaqueOwnedNotFound(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "missing or other owner", err: booking.ErrMyRegistrationNotFound, wantStatus: http.StatusNotFound, wantCode: `"code":10004`},
		{name: "backend", err: errors.New("private database address"), wantStatus: http.StatusInternalServerError, wantCode: `"code":10006`},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service := &fakeMyRegistrationDetailApplication{err: test.err}
			engine := gin.New()
			NewMyRegistrationDetailHandler(
				service,
				func(*gin.Context) (uuid.UUID, error) { return apiUUID(149), nil },
			).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(
				recorder,
				httptest.NewRequest(
					http.MethodGet,
					"/api/v1/xiangwan/me/registrations/"+apiUUID(150).String(),
					nil,
				),
			)
			body := recorder.Body.String()
			if recorder.Code != test.wantStatus || !strings.Contains(body, test.wantCode) ||
				strings.Contains(body, "database address") {
				t.Fatalf("error status=%d body=%s", recorder.Code, body)
			}
		})
	}
}

type fakeMyRegistrationDetailApplication struct {
	detail booking.MyRegistrationDetail
	err    error

	calls          int
	principalID    uuid.UUID
	registrationID uuid.UUID
}

func (service *fakeMyRegistrationDetailApplication) Read(
	_ context.Context,
	principalID uuid.UUID,
	registrationID uuid.UUID,
) (booking.MyRegistrationDetail, error) {
	service.calls++
	service.principalID = principalID
	service.registrationID = registrationID
	return service.detail, service.err
}
