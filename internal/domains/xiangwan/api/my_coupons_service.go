package xiangwanapi

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon"
	"github.com/google/uuid"
)

var (
	ErrInvalidMyCouponsService = errors.New(
		"invalid xiangwan My Coupons service",
	)
	ErrInvalidMyCouponsRequest = errors.New(
		"invalid xiangwan My Coupons request",
	)
)

type myCouponsReader interface {
	List(
		context.Context,
		coupon.MyCouponFilter,
	) (coupon.MyCouponsPage, error)
}

type MyCouponsRequest struct {
	State  coupon.MyCouponState
	Cursor string
	Limit  int
}

type MyCouponsService struct {
	tenantID uuid.UUID
	reader   myCouponsReader
	clock    func() time.Time
}

func NewMyCouponsService(
	tenantID uuid.UUID,
	reader myCouponsReader,
	clock func() time.Time,
) (*MyCouponsService, error) {
	if tenantID == uuid.Nil || reader == nil || clock == nil {
		return nil, ErrInvalidMyCouponsService
	}
	return &MyCouponsService{
		tenantID: tenantID,
		reader:   reader,
		clock:    clock,
	}, nil
}

func (service *MyCouponsService) Read(
	ctx context.Context,
	principalID uuid.UUID,
	request MyCouponsRequest,
) (coupon.MyCouponsPage, error) {
	if service == nil || service.tenantID == uuid.Nil || service.reader == nil ||
		service.clock == nil {
		return coupon.MyCouponsPage{}, ErrInvalidMyCouponsService
	}
	if ctx == nil || principalID == uuid.Nil ||
		(request.State != "" && !coupon.ValidMyCouponState(request.State)) ||
		request.Limit < 0 || request.Limit > coupon.MaxMyCouponsLimit ||
		len(request.Cursor) > 2048 {
		return coupon.MyCouponsPage{}, ErrInvalidMyCouponsRequest
	}
	at := service.clock().UTC()
	if at.IsZero() {
		return coupon.MyCouponsPage{}, ErrInvalidMyCouponsService
	}
	page, err := service.reader.List(ctx, coupon.MyCouponFilter{
		TenantID:    service.tenantID,
		PrincipalID: principalID,
		State:       request.State,
		Limit:       request.Limit,
		Cursor:      request.Cursor,
		At:          at,
	})
	if err != nil {
		return coupon.MyCouponsPage{}, fmt.Errorf(
			"read xiangwan My Coupons: %w",
			err,
		)
	}
	return page, nil
}
