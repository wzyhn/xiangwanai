package xiangwanapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/consumerprofile"
	consumerprofilepostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/consumerprofile/postgres"
	"github.com/wzyhn/xiangwanai/internal/pkg/contentsecurity"
	"github.com/google/uuid"
)

var (
	ErrInvalidConsumerProfileService = errors.New(
		"invalid xiangwan ConsumerProfile service",
	)
	ErrInvalidConsumerProfileRequest = errors.New(
		"invalid xiangwan ConsumerProfile request",
	)
	ErrConsumerProfilePolicyUnavailable = errors.New(
		"xiangwan ConsumerProfile privacy policy unavailable",
	)
	ErrConsumerProfileModerationUnavailable = errors.New(
		"xiangwan ConsumerProfile moderation unavailable",
	)
	ErrConsumerProfileContentRejected = errors.New(
		"xiangwan ConsumerProfile content rejected",
	)
	ErrInvalidConsumerProfileProjection = errors.New(
		"invalid xiangwan ConsumerProfile projection",
	)
)

type consumerProfileReader interface {
	ReadMine(
		context.Context,
		uuid.UUID,
		uuid.UUID,
	) (consumerprofile.Snapshot, error)
	ProviderOpenID(context.Context, uuid.UUID, string) (string, error)
}

type consumerProfileWriter interface {
	Replay(
		context.Context,
		consumerprofile.Patch,
	) (consumerprofile.MutationReceipt, error)
	Apply(
		context.Context,
		consumerprofile.Patch,
		consumerprofile.ModerationOutcome,
	) (consumerprofile.MutationReceipt, error)
}

type ConsumerProfileUpdateRequest struct {
	OperationKey    uuid.UUID
	ExpectedVersion int64
	Fields          consumerprofile.Fields
}

type ConsumerProfileService struct {
	tenantID             uuid.UUID
	appID                string
	privacyPolicyVersion string
	reader               consumerProfileReader
	writer               consumerProfileWriter
	checker              contentsecurity.Checker
	now                  func() time.Time
}

func NewConsumerProfileService(
	tenantID uuid.UUID,
	appID string,
	privacyPolicyVersion string,
	reader consumerProfileReader,
	writer consumerProfileWriter,
	checker contentsecurity.Checker,
) (*ConsumerProfileService, error) {
	if tenantID == uuid.Nil || !validConsumerProfileAppID(appID) ||
		reader == nil || writer == nil || checker == nil ||
		(privacyPolicyVersion != "" &&
			!consumerprofile.ValidPolicyVersion(privacyPolicyVersion)) {
		return nil, ErrInvalidConsumerProfileService
	}
	return &ConsumerProfileService{
		tenantID:             tenantID,
		appID:                appID,
		privacyPolicyVersion: privacyPolicyVersion,
		reader:               reader,
		writer:               writer,
		checker:              checker,
		now:                  time.Now,
	}, nil
}

func (service *ConsumerProfileService) GetMine(
	ctx context.Context,
	principalID uuid.UUID,
) (consumerprofile.Snapshot, error) {
	if !service.valid() {
		return consumerprofile.Snapshot{}, ErrInvalidConsumerProfileService
	}
	if ctx == nil || principalID == uuid.Nil {
		return consumerprofile.Snapshot{}, ErrInvalidConsumerProfileRequest
	}
	snapshot, err := service.reader.ReadMine(
		ctx,
		service.tenantID,
		principalID,
	)
	if err != nil {
		return consumerprofile.Snapshot{}, fmt.Errorf("read my ConsumerProfile: %w", err)
	}
	if err := consumerprofile.ValidateSnapshot(snapshot); err != nil {
		return consumerprofile.Snapshot{}, ErrInvalidConsumerProfileProjection
	}
	return snapshot, nil
}

func (service *ConsumerProfileService) Update(
	ctx context.Context,
	principalID uuid.UUID,
	request ConsumerProfileUpdateRequest,
) (consumerprofile.MutationReceipt, error) {
	if !service.valid() {
		return consumerprofile.MutationReceipt{},
			ErrInvalidConsumerProfileService
	}
	if ctx == nil || principalID == uuid.Nil || request.OperationKey == uuid.Nil ||
		request.OperationKey.Version() != 4 ||
		request.OperationKey.Variant() != uuid.RFC4122 ||
		request.ExpectedVersion < 0 {
		return consumerprofile.MutationReceipt{},
			ErrInvalidConsumerProfileRequest
	}
	fields, err := consumerprofile.NormalizeFields(request.Fields)
	if err != nil {
		return consumerprofile.MutationReceipt{},
			ErrInvalidConsumerProfileRequest
	}
	patch := consumerprofile.Patch{
		TenantID:             service.tenantID,
		PrincipalID:          principalID,
		OperationKey:         request.OperationKey,
		ExpectedVersion:      request.ExpectedVersion,
		Fields:               fields,
		PrivacyPolicyVersion: service.privacyPolicyVersion,
	}

	replayed, err := service.writer.Replay(ctx, patch)
	switch {
	case err == nil:
		if !receiptMatchesPatch(replayed, patch) || !replayed.Replayed {
			return consumerprofile.MutationReceipt{},
				ErrInvalidConsumerProfileProjection
		}
		if replayed.ModerationStatus == consumerprofile.ModerationStatusRejected {
			return consumerprofile.MutationReceipt{},
				ErrConsumerProfileContentRejected
		}
		return replayed, nil
	case !errors.Is(err, consumerprofilepostgres.ErrOperationNotFound):
		return consumerprofile.MutationReceipt{}, fmt.Errorf(
			"replay ConsumerProfile update: %w",
			err,
		)
	}
	if service.privacyPolicyVersion == "" {
		return consumerprofile.MutationReceipt{},
			ErrConsumerProfilePolicyUnavailable
	}

	outcome, err := service.initialModerationOutcome(fields)
	if err != nil {
		return consumerprofile.MutationReceipt{}, err
	}
	receipt, err := service.writer.Apply(ctx, patch, outcome)
	if err != nil {
		return consumerprofile.MutationReceipt{}, fmt.Errorf(
			"apply ConsumerProfile update: %w",
			err,
		)
	}
	if !receiptMatchesPatch(receipt, patch) {
		return consumerprofile.MutationReceipt{},
			ErrInvalidConsumerProfileProjection
	}
	if receipt.ModerationStatus == consumerprofile.ModerationStatusRejected {
		return consumerprofile.MutationReceipt{},
			ErrConsumerProfileContentRejected
	}
	return receipt, nil
}

func (service *ConsumerProfileService) initialModerationOutcome(
	fields consumerprofile.Fields,
) (consumerprofile.ModerationOutcome, error) {
	observedAt := service.now().UTC().Truncate(time.Microsecond)
	text := consumerprofile.ModerationText(fields)
	if text == "" {
		return consumerprofile.NewEmptyModerationOutcome(observedAt)
	}
	// Non-empty changes are reserved transactionally before any provider call.
	// The PostgreSQL worker owns the external moderation attempt and resolves
	// the durable candidate under a generation-fenced lease.
	return consumerprofile.NewUnavailableModerationOutcome(observedAt)
}

func (service *ConsumerProfileService) valid() bool {
	return service != nil && service.tenantID != uuid.Nil &&
		validConsumerProfileAppID(service.appID) && service.reader != nil &&
		service.writer != nil && service.checker != nil && service.now != nil
}

func receiptMatchesPatch(
	receipt consumerprofile.MutationReceipt,
	patch consumerprofile.Patch,
) bool {
	if consumerprofile.ValidateMutationReceipt(receipt) != nil ||
		receipt.BaseVersion != patch.ExpectedVersion {
		return false
	}
	left, leftErr := consumerprofile.NormalizeFields(receipt.Fields)
	right, rightErr := consumerprofile.NormalizeFields(patch.Fields)
	if leftErr != nil || rightErr != nil {
		return false
	}
	leftJSON, leftErr := jsonProfileFields(left)
	rightJSON, rightErr := jsonProfileFields(right)
	return leftErr == nil && rightErr == nil && leftJSON == rightJSON
}

func jsonProfileFields(value consumerprofile.Fields) (string, error) {
	payload, err := json.Marshal(value)
	return string(payload), err
}

func validConsumerProfileAppID(value string) bool {
	if len(value) < 3 || len(value) > 64 || !strings.HasPrefix(value, "wx") {
		return false
	}
	for _, character := range value[2:] {
		if (character < 'a' || character > 'z') &&
			(character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') {
			return false
		}
	}
	return true
}
