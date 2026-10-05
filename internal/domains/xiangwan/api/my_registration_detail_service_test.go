package xiangwanapi

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/booking"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
	"github.com/google/uuid"
)

func TestMyRegistrationDetailServiceFixesOwnerAndRegistration(t *testing.T) {
	t.Parallel()

	tenantID := apiUUID(130)
	principalID := apiUUID(131)
	registrationID := apiUUID(132)
	asOf := time.Date(2026, time.September, 15, 6, 0, 0, 0, time.UTC)
	want := booking.MyRegistrationDetail{AsOf: asOf}
	reader := &fakeMyRegistrationDetailReader{detail: want}
	service, err := NewMyRegistrationDetailService(
		tenantID,
		reader,
		func() time.Time { return asOf },
	)
	if err != nil {
		t.Fatalf("NewMyRegistrationDetailService() error = %v", err)
	}
	detail, err := service.Read(
		context.Background(),
		principalID,
		registrationID,
	)
	if err != nil || !reflect.DeepEqual(detail, want) {
		t.Fatalf("Read() = %+v, %v", detail, err)
	}
	if reader.calls != 1 || reader.tenantID != tenantID ||
		reader.principalID != principalID ||
		reader.registrationID != registrationID || !reader.asOf.Equal(asOf) {
		t.Fatalf("reader = %+v", reader)
	}
}

func TestMyRegistrationDetailServiceRejectsIncompleteIdentity(t *testing.T) {
	t.Parallel()

	reader := &fakeMyRegistrationDetailReader{}
	service, err := NewMyRegistrationDetailService(
		apiUUID(133),
		reader,
		time.Now,
	)
	if err != nil {
		t.Fatalf("NewMyRegistrationDetailService() error = %v", err)
	}
	for _, test := range []struct {
		ctx            context.Context
		principalID    uuid.UUID
		registrationID uuid.UUID
	}{
		{principalID: apiUUID(134), registrationID: apiUUID(135)},
		{ctx: context.Background(), registrationID: apiUUID(136)},
		{ctx: context.Background(), principalID: apiUUID(137)},
	} {
		_, gotErr := service.Read(test.ctx, test.principalID, test.registrationID)
		if !errors.Is(gotErr, ErrInvalidMyRegistrationDetailRequest) {
			t.Fatalf("Read(invalid) error = %v", gotErr)
		}
	}
	if reader.calls != 0 {
		t.Fatalf("invalid identity reached reader %d times", reader.calls)
	}
}

func TestMyRegistrationDetailServicePreservesNotFound(t *testing.T) {
	t.Parallel()

	reader := &fakeMyRegistrationDetailReader{
		err: booking.ErrMyRegistrationNotFound,
	}
	service, err := NewMyRegistrationDetailService(
		apiUUID(138),
		reader,
		time.Now,
	)
	if err != nil {
		t.Fatalf("NewMyRegistrationDetailService() error = %v", err)
	}
	_, gotErr := service.Read(
		context.Background(),
		apiUUID(139),
		apiUUID(140),
	)
	if !errors.Is(gotErr, booking.ErrMyRegistrationNotFound) {
		t.Fatalf("Read(not found) error = %v", gotErr)
	}
}

func TestMyRegistrationDetailServiceAppliesConfiguredFreeCancellationCutoff(
	t *testing.T,
) {
	t.Parallel()

	tenantID := apiUUID(151)
	principalID := apiUUID(152)
	registrationID := apiUUID(153)
	sessionID := apiUUID(154)
	asOf := time.Date(2026, time.September, 15, 6, 0, 0, 0, time.UTC)
	policy, err := registration.NewSelfCancellationPolicy(
		"cancel-v3",
		24*time.Hour,
	)
	if err != nil {
		t.Fatalf("NewSelfCancellationPolicy() error = %v", err)
	}
	reader := &fakeMyRegistrationDetailReader{
		detail: booking.MyRegistrationDetail{
			Item: booking.MyRegistrationItem{
				RegistrationID: registrationID,
				SessionID:      sessionID,
				SessionStartAt: asOf.Add(12 * time.Hour),
			},
			CancellationAction: booking.MyRegistrationCancellationActionAvailable,
			AsOf:               asOf,
		},
	}
	service, err := NewMyRegistrationDetailServiceWithCancellationPolicy(
		tenantID,
		reader,
		policy,
		func() time.Time { return asOf },
	)
	if err != nil {
		t.Fatalf("NewMyRegistrationDetailServiceWithCancellationPolicy() error = %v", err)
	}
	detail, err := service.Read(
		context.Background(),
		principalID,
		registrationID,
	)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if detail.CancellationAction != booking.MyRegistrationCancellationActionUnavailable {
		t.Fatalf("CancellationAction = %q", detail.CancellationAction)
	}
}

type fakeMyRegistrationDetailReader struct {
	detail booking.MyRegistrationDetail
	err    error

	calls          int
	tenantID       uuid.UUID
	principalID    uuid.UUID
	registrationID uuid.UUID
	asOf           time.Time
}

func (reader *fakeMyRegistrationDetailReader) GetRegistrationDetail(
	_ context.Context,
	tenantID uuid.UUID,
	principalID uuid.UUID,
	registrationID uuid.UUID,
	asOf time.Time,
) (booking.MyRegistrationDetail, error) {
	reader.calls++
	reader.tenantID = tenantID
	reader.principalID = principalID
	reader.registrationID = registrationID
	reader.asOf = asOf
	return reader.detail, reader.err
}
