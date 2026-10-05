package xiangwanapi

import (
	"context"
	"errors"
	"fmt"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/datarights"
	datarightspostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/datarights/postgres"
	"github.com/google/uuid"
)

var (
	ErrInvalidDataRightsService = errors.New(
		"invalid xiangwan Data Rights service",
	)
	ErrInvalidDataRightsRequest = errors.New(
		"invalid xiangwan Data Rights request",
	)
	ErrDataRightsPolicyUnavailable = errors.New(
		"xiangwan Data Rights policy unavailable",
	)
	ErrInvalidDataRightsProjection = errors.New(
		"invalid xiangwan Data Rights projection",
	)
)

type dataRightsWriter interface {
	Submit(
		context.Context,
		datarights.Submission,
	) (datarightspostgres.SubmissionResult, error)
}

type dataRightsReader interface {
	ListMine(
		context.Context,
		uuid.UUID,
		uuid.UUID,
	) ([]datarights.CaseHistory, error)
}

type DataRightsSubmissionRequest struct {
	RequestType  datarights.RequestType
	RequestScope datarights.RequestScope
	OperationKey uuid.UUID
}

type DataRightsService struct {
	tenantID             uuid.UUID
	privacyPolicyVersion string
	writer               dataRightsWriter
	reader               dataRightsReader
}

func NewDataRightsService(
	tenantID uuid.UUID,
	privacyPolicyVersion string,
	writer dataRightsWriter,
	reader dataRightsReader,
) (*DataRightsService, error) {
	if tenantID == uuid.Nil || writer == nil || reader == nil ||
		(privacyPolicyVersion != "" &&
			!datarights.ValidPolicyVersion(privacyPolicyVersion)) {
		return nil, ErrInvalidDataRightsService
	}
	return &DataRightsService{
		tenantID:             tenantID,
		privacyPolicyVersion: privacyPolicyVersion,
		writer:               writer,
		reader:               reader,
	}, nil
}

func (service *DataRightsService) Submit(
	ctx context.Context,
	principalID uuid.UUID,
	request DataRightsSubmissionRequest,
) (datarightspostgres.SubmissionResult, error) {
	if service == nil || service.tenantID == uuid.Nil || service.writer == nil ||
		service.reader == nil {
		return datarightspostgres.SubmissionResult{},
			ErrInvalidDataRightsService
	}
	if ctx == nil || principalID == uuid.Nil || request.OperationKey == uuid.Nil ||
		request.OperationKey.Version() != 4 ||
		request.OperationKey.Variant() != uuid.RFC4122 ||
		!datarights.ValidRequestType(request.RequestType) ||
		!datarights.ValidRequestScope(request.RequestScope) {
		return datarightspostgres.SubmissionResult{},
			ErrInvalidDataRightsRequest
	}
	if service.privacyPolicyVersion == "" {
		return datarightspostgres.SubmissionResult{},
			ErrDataRightsPolicyUnavailable
	}
	result, err := service.writer.Submit(ctx, datarights.Submission{
		TenantID:             service.tenantID,
		PrincipalID:          principalID,
		OperationKey:         request.OperationKey,
		RequestType:          request.RequestType,
		RequestScope:         request.RequestScope,
		PrivacyPolicyVersion: service.privacyPolicyVersion,
	})
	if err != nil {
		return datarightspostgres.SubmissionResult{}, fmt.Errorf(
			"submit xiangwan Data Rights request: %w",
			err,
		)
	}
	if err := validateOwnedDataRightsCase(
		result.Case,
		service.tenantID,
		principalID,
	); err != nil || result.Case.OperationKey != request.OperationKey ||
		result.Case.RequestType != request.RequestType ||
		result.Case.RequestScope != request.RequestScope ||
		result.Case.PrivacyPolicyVersion != service.privacyPolicyVersion {
		return datarightspostgres.SubmissionResult{},
			ErrInvalidDataRightsProjection
	}
	return result, nil
}

func (service *DataRightsService) ListMine(
	ctx context.Context,
	principalID uuid.UUID,
) ([]datarights.CaseHistory, error) {
	if service == nil || service.tenantID == uuid.Nil || service.writer == nil ||
		service.reader == nil {
		return nil, ErrInvalidDataRightsService
	}
	if ctx == nil || principalID == uuid.Nil {
		return nil, ErrInvalidDataRightsRequest
	}
	histories, err := service.reader.ListMine(
		ctx,
		service.tenantID,
		principalID,
	)
	if err != nil {
		return nil, fmt.Errorf("read xiangwan Data Rights cases: %w", err)
	}
	if histories == nil || len(histories) > datarights.MaxMyCases {
		return nil, ErrInvalidDataRightsProjection
	}
	seen := make(map[uuid.UUID]struct{}, len(histories))
	for _, history := range histories {
		if err := validateOwnedDataRightsHistory(
			history,
			service.tenantID,
			principalID,
		); err != nil {
			return nil, ErrInvalidDataRightsProjection
		}
		if _, duplicate := seen[history.Case.ID]; duplicate {
			return nil, ErrInvalidDataRightsProjection
		}
		seen[history.Case.ID] = struct{}{}
	}
	return histories, nil
}

func validateOwnedDataRightsCase(
	value datarights.Case,
	tenantID uuid.UUID,
	principalID uuid.UUID,
) error {
	if err := datarights.ValidateCase(value); err != nil ||
		value.TenantID != tenantID || value.PrincipalID != principalID {
		return ErrInvalidDataRightsProjection
	}
	return nil
}

func validateOwnedDataRightsHistory(
	history datarights.CaseHistory,
	tenantID uuid.UUID,
	principalID uuid.UUID,
) error {
	if err := validateOwnedDataRightsCase(
		history.Case,
		tenantID,
		principalID,
	); err != nil || history.Events == nil || len(history.Events) == 0 ||
		len(history.Events) != int(history.Case.Version) {
		return ErrInvalidDataRightsProjection
	}
	for index, event := range history.Events {
		if err := datarights.ValidateCaseEvent(event); err != nil ||
			event.TenantID != history.Case.TenantID ||
			event.CaseID != history.Case.ID ||
			event.CaseVersion != int64(index+1) ||
			event.OccurredAt.Before(history.Case.SubmittedAt) ||
			(index > 0 && event.OccurredAt.Before(
				history.Events[index-1].OccurredAt,
			)) {
			return ErrInvalidDataRightsProjection
		}
	}
	first := history.Events[0]
	if first.EventType != datarights.EventTypeSubmitted ||
		first.ResultingStatus != datarights.CaseStatusSubmitted ||
		first.ActorPrincipalID == nil ||
		*first.ActorPrincipalID != history.Case.PrincipalID ||
		first.PolicyBasisVersion != history.Case.PrivacyPolicyVersion ||
		history.Events[len(history.Events)-1].ResultingStatus !=
			history.Case.Status {
		return ErrInvalidDataRightsProjection
	}
	return nil
}
