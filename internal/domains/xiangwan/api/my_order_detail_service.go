package xiangwanapi

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/booking"
	"github.com/google/uuid"
)

var (
	ErrInvalidMyOrderDetailService = errors.New(
		"invalid xiangwan My Order detail service",
	)
	ErrInvalidMyOrderDetailRequest = errors.New(
		"invalid xiangwan My Order detail request",
	)
)

type myOrderDetailReader interface {
	GetOrderDetail(
		context.Context,
		uuid.UUID,
		uuid.UUID,
		uuid.UUID,
		time.Time,
	) (booking.MyOrderDetail, error)
}

type MyOrderDetailService struct {
	tenantID uuid.UUID
	reader   myOrderDetailReader
	clock    func() time.Time
}

func NewMyOrderDetailService(
	tenantID uuid.UUID,
	reader myOrderDetailReader,
	clock func() time.Time,
) (*MyOrderDetailService, error) {
	if tenantID == uuid.Nil || reader == nil || clock == nil {
		return nil, ErrInvalidMyOrderDetailService
	}
	return &MyOrderDetailService{
		tenantID: tenantID,
		reader:   reader,
		clock:    clock,
	}, nil
}

func (service *MyOrderDetailService) Read(
	ctx context.Context,
	principalID uuid.UUID,
	orderID uuid.UUID,
) (booking.MyOrderDetail, error) {
	if service == nil || service.tenantID == uuid.Nil || service.reader == nil ||
		service.clock == nil {
		return booking.MyOrderDetail{}, ErrInvalidMyOrderDetailService
	}
	if ctx == nil || principalID == uuid.Nil || orderID == uuid.Nil {
		return booking.MyOrderDetail{}, ErrInvalidMyOrderDetailRequest
	}
	asOf := service.clock().UTC()
	if asOf.IsZero() {
		return booking.MyOrderDetail{}, ErrInvalidMyOrderDetailService
	}
	detail, err := service.reader.GetOrderDetail(
		ctx,
		service.tenantID,
		principalID,
		orderID,
		asOf,
	)
	if err != nil {
		return booking.MyOrderDetail{}, fmt.Errorf(
			"read xiangwan My Order detail: %w",
			err,
		)
	}
	return detail, nil
}
