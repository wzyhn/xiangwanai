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

func TestMyOrdersServiceFixesOwnerAndClassificationTime(t *testing.T) {
	t.Parallel()

	tenantID := apiUUID(151)
	principalID := apiUUID(152)
	asOf := time.Date(2026, time.September, 15, 8, 0, 0, 0, time.UTC)
	want := booking.MyOrdersPage{
		ActiveState: booking.MyOrderStatePendingPayment,
		Items:       []booking.MyOrderItem{},
		AsOf:        asOf,
	}
	reader := &fakeMyOrdersReader{page: want}
	service, err := NewMyOrdersService(
		tenantID,
		reader,
		func() time.Time { return asOf },
	)
	if err != nil {
		t.Fatalf("NewMyOrdersService() error = %v", err)
	}
	request := MyOrdersRequest{
		State:  booking.MyOrderStatePendingPayment,
		Cursor: "opaque-owner-cursor",
		Limit:  25,
	}
	page, err := service.Read(context.Background(), principalID, request)
	if err != nil || !reflect.DeepEqual(page, want) {
		t.Fatalf("Read() = %+v, %v", page, err)
	}
	wantFilter := booking.MyOrderFilter{
		TenantID:    tenantID,
		PrincipalID: principalID,
		State:       request.State,
		Limit:       request.Limit,
		Cursor:      request.Cursor,
		At:          asOf,
	}
	if reader.calls != 1 || !reflect.DeepEqual(reader.filter, wantFilter) {
		t.Fatalf("reader calls=%d filter=%+v", reader.calls, reader.filter)
	}
}

func TestMyOrdersServiceRejectsInvalidRequests(t *testing.T) {
	t.Parallel()

	reader := &fakeMyOrdersReader{}
	service, err := NewMyOrdersService(apiUUID(153), reader, time.Now)
	if err != nil {
		t.Fatalf("NewMyOrdersService() error = %v", err)
	}
	for _, test := range []struct {
		ctx         context.Context
		principalID uuid.UUID
		request     MyOrdersRequest
	}{
		{principalID: apiUUID(154)},
		{ctx: context.Background()},
		{ctx: context.Background(), principalID: apiUUID(155), request: MyOrdersRequest{State: "unknown"}},
		{ctx: context.Background(), principalID: apiUUID(156), request: MyOrdersRequest{Limit: -1}},
		{ctx: context.Background(), principalID: apiUUID(157), request: MyOrdersRequest{Limit: booking.MaxMyOrdersLimit + 1}},
		{ctx: context.Background(), principalID: apiUUID(158), request: MyOrdersRequest{Cursor: string(make([]byte, 2049))}},
	} {
		_, gotErr := service.Read(test.ctx, test.principalID, test.request)
		if !errors.Is(gotErr, ErrInvalidMyOrdersRequest) {
			t.Fatalf("Read(invalid) error = %v", gotErr)
		}
	}
	if reader.calls != 0 {
		t.Fatalf("invalid request reached reader %d times", reader.calls)
	}
}

func TestMyOrdersServicePreservesCursorErrors(t *testing.T) {
	t.Parallel()

	reader := &fakeMyOrdersReader{err: booking.ErrStaleMyOrdersCursor}
	service, err := NewMyOrdersService(apiUUID(159), reader, time.Now)
	if err != nil {
		t.Fatalf("NewMyOrdersService() error = %v", err)
	}
	_, gotErr := service.Read(
		context.Background(),
		apiUUID(160),
		MyOrdersRequest{},
	)
	if !errors.Is(gotErr, booking.ErrStaleMyOrdersCursor) {
		t.Fatalf("Read(stale) error = %v", gotErr)
	}
}

type fakeMyOrdersReader struct {
	page booking.MyOrdersPage
	err  error

	calls  int
	filter booking.MyOrderFilter
}

func (reader *fakeMyOrdersReader) ListOrders(
	_ context.Context,
	filter booking.MyOrderFilter,
) (booking.MyOrdersPage, error) {
	reader.calls++
	reader.filter = filter
	return reader.page, reader.err
}
