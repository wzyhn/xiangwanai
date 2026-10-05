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
	ErrInvalidMyOrdersService = errors.New(
		"invalid xiangwan My Orders service",
	)
	ErrInvalidMyOrdersRequest = errors.New(
		"invalid xiangwan My Orders request",
	)
)

type myOrdersReader interface {
	ListOrders(
		context.Context,
		booking.MyOrderFilter,
	) (booking.MyOrdersPage, error)
}

type MyOrdersRequest struct {
	State  booking.MyOrderState
	Cursor string
	Limit  int
}

type MyOrdersService struct {
	tenantID uuid.UUID
	reader   myOrdersReader
	clock    func() time.Time
}

func NewMyOrdersService(
	tenantID uuid.UUID,
	reader myOrdersReader,
	clock func() time.Time,
) (*MyOrdersService, error) {
	if tenantID == uuid.Nil || reader == nil || clock == nil {
		return nil, ErrInvalidMyOrdersService
	}
	return &MyOrdersService{
		tenantID: tenantID,
		reader:   reader,
		clock:    clock,
	}, nil
}

func (service *MyOrdersService) Read(
	ctx context.Context,
	principalID uuid.UUID,
	request MyOrdersRequest,
) (booking.MyOrdersPage, error) {
	if service == nil || service.tenantID == uuid.Nil || service.reader == nil ||
		service.clock == nil {
		return booking.MyOrdersPage{}, ErrInvalidMyOrdersService
	}
	if ctx == nil || principalID == uuid.Nil ||
		(request.State != "" && !booking.ValidMyOrderState(request.State)) ||
		request.Limit < 0 || request.Limit > booking.MaxMyOrdersLimit ||
		len(request.Cursor) > 2048 {
		return booking.MyOrdersPage{}, ErrInvalidMyOrdersRequest
	}
	asOf := service.clock().UTC()
	if asOf.IsZero() {
		return booking.MyOrdersPage{}, ErrInvalidMyOrdersService
	}
	page, err := service.reader.ListOrders(ctx, booking.MyOrderFilter{
		TenantID:    service.tenantID,
		PrincipalID: principalID,
		State:       request.State,
		Limit:       request.Limit,
		Cursor:      request.Cursor,
		At:          asOf,
	})
	if err != nil {
		return booking.MyOrdersPage{}, fmt.Errorf(
			"read xiangwan My Orders: %w",
			err,
		)
	}
	return page, nil
}
