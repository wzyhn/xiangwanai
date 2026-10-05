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

func TestMyOrderDetailServiceFixesOwnerAndOrder(t *testing.T) {
	t.Parallel()

	tenantID := apiUUID(170)
	principalID := apiUUID(171)
	orderID := apiUUID(172)
	asOf := time.Date(2026, time.September, 15, 10, 0, 0, 0, time.UTC)
	want := booking.MyOrderDetail{AsOf: asOf}
	reader := &fakeMyOrderDetailReader{detail: want}
	service, err := NewMyOrderDetailService(
		tenantID,
		reader,
		func() time.Time { return asOf },
	)
	if err != nil {
		t.Fatalf("NewMyOrderDetailService() error = %v", err)
	}
	detail, err := service.Read(context.Background(), principalID, orderID)
	if err != nil || !reflect.DeepEqual(detail, want) {
		t.Fatalf("Read() = %+v, %v", detail, err)
	}
	if reader.calls != 1 || reader.tenantID != tenantID ||
		reader.principalID != principalID || reader.orderID != orderID ||
		!reader.asOf.Equal(asOf) {
		t.Fatalf("reader = %+v", reader)
	}
}

func TestMyOrderDetailServiceRejectsIncompleteIdentity(t *testing.T) {
	t.Parallel()

	reader := &fakeMyOrderDetailReader{}
	service, err := NewMyOrderDetailService(apiUUID(173), reader, time.Now)
	if err != nil {
		t.Fatalf("NewMyOrderDetailService() error = %v", err)
	}
	for _, test := range []struct {
		ctx         context.Context
		principalID uuid.UUID
		orderID     uuid.UUID
	}{
		{principalID: apiUUID(174), orderID: apiUUID(175)},
		{ctx: context.Background(), orderID: apiUUID(176)},
		{ctx: context.Background(), principalID: apiUUID(177)},
	} {
		_, gotErr := service.Read(test.ctx, test.principalID, test.orderID)
		if !errors.Is(gotErr, ErrInvalidMyOrderDetailRequest) {
			t.Fatalf("Read(invalid) error = %v", gotErr)
		}
	}
	if reader.calls != 0 {
		t.Fatalf("invalid identity reached reader %d times", reader.calls)
	}
}

func TestMyOrderDetailServicePreservesNotFound(t *testing.T) {
	t.Parallel()

	reader := &fakeMyOrderDetailReader{err: booking.ErrMyOrderNotFound}
	service, err := NewMyOrderDetailService(apiUUID(178), reader, time.Now)
	if err != nil {
		t.Fatalf("NewMyOrderDetailService() error = %v", err)
	}
	_, gotErr := service.Read(
		context.Background(),
		apiUUID(179),
		apiUUID(180),
	)
	if !errors.Is(gotErr, booking.ErrMyOrderNotFound) {
		t.Fatalf("Read(not found) error = %v", gotErr)
	}
}

type fakeMyOrderDetailReader struct {
	detail booking.MyOrderDetail
	err    error

	calls       int
	tenantID    uuid.UUID
	principalID uuid.UUID
	orderID     uuid.UUID
	asOf        time.Time
}

func (reader *fakeMyOrderDetailReader) GetOrderDetail(
	_ context.Context,
	tenantID uuid.UUID,
	principalID uuid.UUID,
	orderID uuid.UUID,
	asOf time.Time,
) (booking.MyOrderDetail, error) {
	reader.calls++
	reader.tenantID = tenantID
	reader.principalID = principalID
	reader.orderID = orderID
	reader.asOf = asOf
	return reader.detail, reader.err
}
