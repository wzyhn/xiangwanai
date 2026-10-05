package xiangwanapi

import (
	"context"
	"errors"
	"fmt"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/people"
	"github.com/google/uuid"
)

var (
	ErrInvalidMyBenefitsService = errors.New(
		"invalid xiangwan My Benefits service",
	)
	ErrInvalidMyBenefitsRequest = errors.New(
		"invalid xiangwan My Benefits request",
	)
)

type myBenefitsReader interface {
	Read(
		context.Context,
		uuid.UUID,
		uuid.UUID,
	) (people.MyBenefits, error)
}

type MyBenefitsService struct {
	tenantID uuid.UUID
	reader   myBenefitsReader
}

func NewMyBenefitsService(
	tenantID uuid.UUID,
	reader myBenefitsReader,
) (*MyBenefitsService, error) {
	if tenantID == uuid.Nil || reader == nil {
		return nil, ErrInvalidMyBenefitsService
	}
	return &MyBenefitsService{tenantID: tenantID, reader: reader}, nil
}

func (service *MyBenefitsService) Read(
	ctx context.Context,
	principalID uuid.UUID,
) (people.MyBenefits, error) {
	if service == nil || service.tenantID == uuid.Nil || service.reader == nil {
		return people.MyBenefits{}, ErrInvalidMyBenefitsService
	}
	if ctx == nil || principalID == uuid.Nil {
		return people.MyBenefits{}, ErrInvalidMyBenefitsRequest
	}
	result, err := service.reader.Read(ctx, service.tenantID, principalID)
	if err != nil {
		return people.MyBenefits{}, fmt.Errorf(
			"read xiangwan My Benefits: %w",
			err,
		)
	}
	return result, nil
}
