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
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/refund"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestMyRegistrationsHandlerReturnsExactOwnedFacts(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 15, 4, 0, 0, 0, time.UTC)
	principalID := apiUUID(120)
	paid := int64(8800)
	checkedInAt := now.Add(-30 * time.Minute)
	resolvedAt := now.Add(-time.Hour)
	area := activity.AreaCodeHeping
	venue := "Xiangwan Lab"
	address := "Heping Road 1"
	confirmedAt := now.Add(-24 * time.Hour)
	service := &fakeMyRegistrationsApplication{page: booking.MyRegistrationsPage{
		ActiveState: booking.MyRegistrationStateRefunded,
		AsOf:        now,
		NextCursor:  "next-owner-cursor",
		Items: []booking.MyRegistrationItem{{
			RegistrationID:      apiUUID(121),
			RegistrationVersion: 4,
			SeriesID:            apiUUID(122),
			SeriesTitle:         "AI Meetup",
			InstanceID:          apiUUID(123),
			InstanceTitle:       "AI Meetup 03",
			InstanceStatus:      activity.InstanceStatusCompleted,
			SessionID:           apiUUID(124),
			SessionTitle:        "Sunday Session",
			SessionStatus:       activity.SessionStatusEnded,
			SessionStartAt:      now.Add(-3 * time.Hour),
			SessionEndAt:        now.Add(-time.Hour),
			DeliveryMode:        activity.DeliveryModeOffline,
			Area:                &area,
			VenueName:           &venue,
			Address:             &address,
			ParticipationStatus: registration.ParticipationStatusCancelled,
			ConfirmedAt:         &confirmedAt,
			Order: &booking.MyRegistrationOrderSummary{
				OrderID:            apiUUID(125),
				PaymentStatus:      payment.OrderStatusPaidConfirmed,
				OriginalPriceCents: 9900,
				DiscountCents:      1100,
				PayableCents:       8800,
				ActualPaidCents:    &paid,
				HoldStatus:         payment.CapacityHoldStatusConverted,
				HoldExpiresAt:      now.Add(-23 * time.Hour),
				HoldVersion:        2,
				HoldUpdatedAt:      now.Add(-24 * time.Hour),
				Version:            3,
				UpdatedAt:          now.Add(-24 * time.Hour),
			},
			Refund: &booking.MyRegistrationRefundSummary{
				RefundCaseID:          apiUUID(126),
				RefundStatus:          refund.StatusRefunded,
				ReasonCode:            refund.ReasonUserCancelled,
				RequestedRefundCents:  8800,
				SuccessfulRefundCents: 8800,
				ResolvedAt:            &resolvedAt,
				Version:               3,
				UpdatedAt:             resolvedAt,
			},
			Checkin: booking.CheckinSummary{
				Status:      booking.CheckinStatusCheckedIn,
				CheckedInAt: &checkedInAt,
			},
			State:          booking.MyRegistrationStateRefunded,
			LastBusinessAt: resolvedAt,
		}},
	}}
	handler := NewMyRegistrationsHandler(
		service,
		func(*gin.Context) (uuid.UUID, error) { return principalID, nil },
	)
	engine := gin.New()
	handler.RegisterRoutes(engine.Group("/api/v1/xiangwan"))
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(
		recorder,
		httptest.NewRequest(
			http.MethodGet,
			"/api/v1/xiangwan/me/registrations?state=refunded&limit=10",
			nil,
		),
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET My Registrations status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Code int                     `json:"code"`
		Data MyRegistrationsResponse `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode My Registrations response: %v", err)
	}
	if envelope.Code != 0 || envelope.Data.ActiveState != booking.MyRegistrationStateRefunded ||
		len(envelope.Data.Items) != 1 ||
		envelope.Data.Items[0].RegistrationID != apiUUID(121).String() ||
		envelope.Data.Items[0].SessionID != apiUUID(124).String() ||
		envelope.Data.Items[0].Order == nil ||
		envelope.Data.Items[0].Order.ActualPaidCents == nil ||
		*envelope.Data.Items[0].Order.ActualPaidCents != paid ||
		envelope.Data.Items[0].Refund == nil ||
		envelope.Data.Items[0].Refund.RefundStatus != refund.StatusRefunded ||
		envelope.Data.Items[0].Checkin.Status != booking.CheckinStatusCheckedIn ||
		envelope.Data.NextCursor != "next-owner-cursor" {
		t.Fatalf("GET My Registrations response = %+v", envelope.Data)
	}
	if service.calls != 1 || service.principalID != principalID ||
		service.request.State != booking.MyRegistrationStateRefunded ||
		service.request.Limit != 10 {
		t.Fatalf("service = %+v", service)
	}
	for _, forbidden := range []string{
		"principal_id",
		"tenant_id",
		"sort_rank",
		"sort_at",
		"payment_app_id",
		"external_refund_id",
	} {
		if strings.Contains(recorder.Body.String(), forbidden) {
			t.Fatalf("private/internal field %q crossed HTTP boundary: %s", forbidden, recorder.Body.String())
		}
	}
}

func TestMyRegistrationsHandlerRejectsAmbiguousQuery(t *testing.T) {
	t.Parallel()

	tests := []string{
		"?unknown=value",
		"?state=all&state=registered",
		"?state=unknown",
		"?state=",
		"?cursor=%20opaque",
		"?limit=0",
		"?limit=01",
		"?limit=101",
	}
	for _, suffix := range tests {
		suffix := suffix
		t.Run(suffix, func(t *testing.T) {
			t.Parallel()
			service := &fakeMyRegistrationsApplication{}
			engine := gin.New()
			NewMyRegistrationsHandler(
				service,
				func(*gin.Context) (uuid.UUID, error) { return apiUUID(127), nil },
			).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(
				recorder,
				httptest.NewRequest(
					http.MethodGet,
					"/api/v1/xiangwan/me/registrations"+suffix,
					nil,
				),
			)
			if recorder.Code != http.StatusBadRequest || service.calls != 0 {
				t.Fatalf("invalid query status=%d calls=%d body=%s", recorder.Code, service.calls, recorder.Body.String())
			}
		})
	}
}

func TestMyRegistrationsHandlerMapsStableErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		serviceErr   error
		principalErr error
		wantStatus   int
		wantCode     string
	}{
		{name: "invalid cursor", serviceErr: booking.ErrInvalidMyRegistrationsCursor, wantStatus: http.StatusBadRequest, wantCode: `"code":10001`},
		{name: "stale cursor", serviceErr: booking.ErrStaleMyRegistrationsCursor, wantStatus: http.StatusConflict, wantCode: `"code":10005`},
		{name: "reader failure", serviceErr: errors.New("private database address"), wantStatus: http.StatusInternalServerError, wantCode: `"code":10006`},
		{name: "missing principal", principalErr: errx.NewUnauthorized("missing authenticated principal"), wantStatus: http.StatusUnauthorized, wantCode: `"code":10002`},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service := &fakeMyRegistrationsApplication{err: test.serviceErr}
			engine := gin.New()
			NewMyRegistrationsHandler(
				service,
				func(*gin.Context) (uuid.UUID, error) {
					return apiUUID(128), test.principalErr
				},
			).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(
				recorder,
				httptest.NewRequest(http.MethodGet, "/api/v1/xiangwan/me/registrations", nil),
			)
			body := recorder.Body.String()
			if recorder.Code != test.wantStatus || !strings.Contains(body, test.wantCode) ||
				strings.Contains(body, "private database") {
				t.Fatalf("error status=%d body=%s", recorder.Code, body)
			}
		})
	}
}

func TestMyRegistrationsHandlerReturnsExplicitEmptyState(t *testing.T) {
	t.Parallel()

	service := &fakeMyRegistrationsApplication{page: booking.MyRegistrationsPage{
		ActiveState: booking.MyRegistrationStateAll,
		AsOf:        time.Now().UTC(),
		Items:       []booking.MyRegistrationItem{},
	}}
	engine := gin.New()
	NewMyRegistrationsHandler(
		service,
		func(*gin.Context) (uuid.UUID, error) { return apiUUID(129), nil },
	).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/api/v1/xiangwan/me/registrations", nil),
	)
	if recorder.Code != http.StatusOK ||
		!strings.Contains(recorder.Body.String(), `"items":[]`) ||
		!strings.Contains(recorder.Body.String(), `"empty_state":"no_registrations"`) {
		t.Fatalf("empty response status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

type fakeMyRegistrationsApplication struct {
	page booking.MyRegistrationsPage
	err  error

	calls       int
	principalID uuid.UUID
	request     MyRegistrationsRequest
}

func (service *fakeMyRegistrationsApplication) Read(
	_ context.Context,
	principalID uuid.UUID,
	request MyRegistrationsRequest,
) (booking.MyRegistrationsPage, error) {
	service.calls++
	service.principalID = principalID
	service.request = request
	return service.page, service.err
}
