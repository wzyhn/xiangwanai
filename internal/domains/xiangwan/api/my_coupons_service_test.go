package xiangwanapi

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon"
	couponpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon/postgres"
	"github.com/google/uuid"
)

func TestMyCouponsServiceBindsOwnerAndClassificationTime(t *testing.T) {
	t.Parallel()

	tenantID := apiUUID(210)
	principalID := apiUUID(211)
	now := time.Date(2026, time.September, 21, 2, 3, 4, 0, time.UTC)
	wantPage := coupon.MyCouponsPage{
		Items:       []coupon.MyCouponItem{},
		ActiveState: coupon.MyCouponStateAvailable,
		AsOf:        now,
	}
	reader := &fakeMyCouponsReader{page: wantPage}
	service, err := NewMyCouponsService(
		tenantID,
		reader,
		func() time.Time { return now },
	)
	if err != nil {
		t.Fatalf("NewMyCouponsService() error = %v", err)
	}
	request := MyCouponsRequest{
		State:  coupon.MyCouponStateAvailable,
		Cursor: "opaque-owner-cursor",
		Limit:  25,
	}
	page, err := service.Read(context.Background(), principalID, request)
	if err != nil || !reflect.DeepEqual(page, wantPage) {
		t.Fatalf("Read() = %+v, %v", page, err)
	}
	wantFilter := coupon.MyCouponFilter{
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

func TestMyCouponsServiceValidatesRequestAndPreservesReaderCause(t *testing.T) {
	t.Parallel()

	if _, err := NewMyCouponsService(uuid.Nil, nil, nil); !errors.Is(
		err,
		ErrInvalidMyCouponsService,
	) {
		t.Fatalf("NewMyCouponsService(invalid) error = %v", err)
	}
	reader := &fakeMyCouponsReader{}
	service, err := NewMyCouponsService(apiUUID(212), reader, time.Now)
	if err != nil {
		t.Fatalf("NewMyCouponsService() error = %v", err)
	}
	tests := []struct {
		name        string
		principalID uuid.UUID
		request     MyCouponsRequest
	}{
		{name: "missing principal"},
		{name: "unknown state", principalID: apiUUID(213), request: MyCouponsRequest{State: "unknown"}},
		{name: "negative limit", principalID: apiUUID(214), request: MyCouponsRequest{Limit: -1}},
		{name: "excessive limit", principalID: apiUUID(215), request: MyCouponsRequest{Limit: coupon.MaxMyCouponsLimit + 1}},
		{name: "long cursor", principalID: apiUUID(216), request: MyCouponsRequest{Cursor: string(make([]byte, 2049))}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, gotErr := service.Read(
				context.Background(),
				test.principalID,
				test.request,
			); !errors.Is(gotErr, ErrInvalidMyCouponsRequest) {
				t.Fatalf("Read(invalid) error = %v", gotErr)
			}
		})
	}
	if reader.calls != 0 {
		t.Fatalf("invalid requests reached reader %d times", reader.calls)
	}

	wantErr := couponpostgres.ErrStaleMyCouponsCursor
	failingReader := &fakeMyCouponsReader{err: wantErr}
	failingService, err := NewMyCouponsService(
		apiUUID(216),
		failingReader,
		time.Now,
	)
	if err != nil {
		t.Fatalf("NewMyCouponsService(failing) error = %v", err)
	}
	if _, gotErr := failingService.Read(
		context.Background(),
		apiUUID(217),
		MyCouponsRequest{},
	); !errors.Is(gotErr, wantErr) {
		t.Fatalf("Read(failure) error = %v", gotErr)
	}
}

type fakeMyCouponsReader struct {
	page coupon.MyCouponsPage
	err  error

	calls  int
	filter coupon.MyCouponFilter
}

func (reader *fakeMyCouponsReader) List(
	_ context.Context,
	filter coupon.MyCouponFilter,
) (coupon.MyCouponsPage, error) {
	reader.calls++
	reader.filter = filter
	return reader.page, reader.err
}
