package xiangwanapi

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/booking"
	"github.com/google/uuid"
)

func TestMyRegistrationsServiceFixesTenantPrincipalAndClassificationTime(
	t *testing.T,
) {
	t.Parallel()

	tenantID := apiUUID(110)
	principalID := apiUUID(111)
	now := time.Date(2026, time.September, 15, 2, 30, 0, 0, time.UTC)
	wantPage := booking.MyRegistrationsPage{
		Items:       []booking.MyRegistrationItem{},
		ActiveState: booking.MyRegistrationStateRegistered,
		AsOf:        now,
	}
	reader := &fakeMyRegistrationsReader{page: wantPage}
	service, err := NewMyRegistrationsService(
		tenantID,
		reader,
		func() time.Time { return now },
	)
	if err != nil {
		t.Fatalf("NewMyRegistrationsService() error = %v", err)
	}
	request := MyRegistrationsRequest{
		State:  booking.MyRegistrationStateRegistered,
		Cursor: "opaque-owner-cursor",
		Limit:  25,
	}
	page, err := service.Read(context.Background(), principalID, request)
	if err != nil || !reflect.DeepEqual(page, wantPage) {
		t.Fatalf("Read() = %+v, %v", page, err)
	}
	wantFilter := booking.MyRegistrationFilter{
		TenantID:    tenantID,
		PrincipalID: principalID,
		State:       request.State,
		Limit:       request.Limit,
		Cursor:      request.Cursor,
		At:          now,
	}
	if reader.calls != 1 || !reflect.DeepEqual(reader.filter, wantFilter) {
		t.Fatalf("reader calls=%d filter=%+v", reader.calls, reader.filter)
	}
}

func TestMyRegistrationsServiceRejectsUnownedOrInvalidRequests(t *testing.T) {
	t.Parallel()

	reader := &fakeMyRegistrationsReader{}
	service, err := NewMyRegistrationsService(
		apiUUID(112),
		reader,
		time.Now,
	)
	if err != nil {
		t.Fatalf("NewMyRegistrationsService() error = %v", err)
	}
	tests := []struct {
		name        string
		ctx         context.Context
		principalID uuid.UUID
		request     MyRegistrationsRequest
	}{
		{name: "missing context", principalID: apiUUID(113)},
		{name: "missing principal", ctx: context.Background()},
		{name: "unknown state", ctx: context.Background(), principalID: apiUUID(114), request: MyRegistrationsRequest{State: "unknown"}},
		{name: "negative limit", ctx: context.Background(), principalID: apiUUID(115), request: MyRegistrationsRequest{Limit: -1}},
		{name: "oversized limit", ctx: context.Background(), principalID: apiUUID(116), request: MyRegistrationsRequest{Limit: booking.MaxMyRegistrationsLimit + 1}},
		{name: "oversized cursor", ctx: context.Background(), principalID: apiUUID(117), request: MyRegistrationsRequest{Cursor: string(make([]byte, 2049))}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, gotErr := service.Read(test.ctx, test.principalID, test.request)
			if !errors.Is(gotErr, ErrInvalidMyRegistrationsRequest) {
				t.Fatalf("Read(invalid) error = %v", gotErr)
			}
		})
	}
	if reader.calls != 0 {
		t.Fatalf("invalid requests reached reader %d times", reader.calls)
	}
}

func TestMyRegistrationsServicePreservesReaderErrors(t *testing.T) {
	t.Parallel()

	readErr := booking.ErrStaleMyRegistrationsCursor
	reader := &fakeMyRegistrationsReader{err: readErr}
	service, err := NewMyRegistrationsService(apiUUID(118), reader, time.Now)
	if err != nil {
		t.Fatalf("NewMyRegistrationsService() error = %v", err)
	}
	_, gotErr := service.Read(
		context.Background(),
		apiUUID(119),
		MyRegistrationsRequest{},
	)
	if !errors.Is(gotErr, readErr) {
		t.Fatalf("Read() error = %v", gotErr)
	}
}

type fakeMyRegistrationsReader struct {
	page booking.MyRegistrationsPage
	err  error

	calls  int
	filter booking.MyRegistrationFilter
}

func (reader *fakeMyRegistrationsReader) List(
	_ context.Context,
	filter booking.MyRegistrationFilter,
) (booking.MyRegistrationsPage, error) {
	reader.calls++
	reader.filter = filter
	return reader.page, reader.err
}
