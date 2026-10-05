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
	ErrInvalidMyRegistrationsService = errors.New(
		"invalid xiangwan My Registrations service",
	)
	ErrInvalidMyRegistrationsRequest = errors.New(
		"invalid xiangwan My Registrations request",
	)
)

type myRegistrationsReader interface {
	List(
		context.Context,
		booking.MyRegistrationFilter,
	) (booking.MyRegistrationsPage, error)
}

type MyRegistrationsRequest struct {
	State  booking.MyRegistrationState
	Cursor string
	Limit  int
}

type MyRegistrationsService struct {
	tenantID uuid.UUID
	reader   myRegistrationsReader
	clock    func() time.Time
}

func NewMyRegistrationsService(
	tenantID uuid.UUID,
	reader myRegistrationsReader,
	clock func() time.Time,
) (*MyRegistrationsService, error) {
	if tenantID == uuid.Nil || reader == nil || clock == nil {
		return nil, ErrInvalidMyRegistrationsService
	}
	return &MyRegistrationsService{
		tenantID: tenantID,
		reader:   reader,
		clock:    clock,
	}, nil
}

func (service *MyRegistrationsService) Read(
	ctx context.Context,
	principalID uuid.UUID,
	request MyRegistrationsRequest,
) (booking.MyRegistrationsPage, error) {
	if service == nil || service.tenantID == uuid.Nil || service.reader == nil ||
		service.clock == nil {
		return booking.MyRegistrationsPage{},
			ErrInvalidMyRegistrationsService
	}
	if ctx == nil || principalID == uuid.Nil ||
		(request.State != "" && !booking.ValidMyRegistrationState(request.State)) ||
		request.Limit < 0 || request.Limit > booking.MaxMyRegistrationsLimit ||
		len(request.Cursor) > 2048 {
		return booking.MyRegistrationsPage{},
			ErrInvalidMyRegistrationsRequest
	}
	at := service.clock().UTC()
	if at.IsZero() {
		return booking.MyRegistrationsPage{},
			ErrInvalidMyRegistrationsService
	}
	page, err := service.reader.List(ctx, booking.MyRegistrationFilter{
		TenantID:    service.tenantID,
		PrincipalID: principalID,
		State:       request.State,
		Limit:       request.Limit,
		Cursor:      request.Cursor,
		At:          at,
	})
	if err != nil {
		return booking.MyRegistrationsPage{}, fmt.Errorf(
			"read xiangwan My Registrations: %w",
			err,
		)
	}
	return page, nil
}
