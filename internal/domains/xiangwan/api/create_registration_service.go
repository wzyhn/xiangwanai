package xiangwanapi

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	paymentpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment/postgres"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
	registrationpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration/postgres"
	"github.com/google/uuid"
)

var (
	ErrInvalidCreateRegistrationService = errors.New(
		"invalid xiangwan create Registration service",
	)
	ErrInvalidCreateRegistrationRequest = errors.New(
		"invalid xiangwan create Registration request",
	)
	ErrManualRegistrationContactUnavailable = errors.New(
		"xiangwan manual Registration contact is unavailable",
	)
	ErrRegistrationPrivacyPolicyUnavailable = errors.New(
		"xiangwan Registration privacy policy is unavailable",
	)
	ErrPaidRegistrationUnavailable = errors.New(
		"xiangwan paid Registration is unavailable",
	)
	ErrCreateRegistrationConflict = errors.New(
		"xiangwan created Registration identity conflict",
	)
)

type freeRegistrationConfirmer interface {
	Replay(
		context.Context,
		registrationpostgres.ReplayFreeRegistrationCommand,
	) (registration.Registration, bool, error)
	Confirm(
		context.Context,
		registrationpostgres.ConfirmFreeRegistrationCommand,
	) (registration.Registration, error)
}

type paidRegistrationStarter interface {
	Replay(
		context.Context,
		paymentpostgres.ReplayPaidRegistrationCommand,
	) (paymentpostgres.PaidRegistrationContext, bool, error)
	Start(
		context.Context,
		paymentpostgres.StartPaidRegistrationCommand,
	) (paymentpostgres.PaidRegistrationContext, error)
}

type CreateRegistrationRequest struct {
	IdempotencyKey             uuid.UUID
	InstancePublicationVersion int64
	PriceCents                 int64
	CouponID                   *uuid.UUID
	PrivacyPolicyVersion       string
	ContactName                string
	ContactPhoneE164           string
	ContactPolicyVersion       string
	QuestionnaireVersionID     *uuid.UUID
	Answers                    []activity.QuestionnaireAnswer
}

type CreateRegistrationConfig struct {
	PrivacyPolicyVersion       string
	ManualContactEnabled       bool
	ContactPolicyVersion       string
	PaidRegistrationEnabled    bool
	PaymentAppID               string
	PaymentMerchantID          string
	MerchantConfigGenerationID uuid.UUID
}

type CreateRegistrationResult struct {
	Registration registration.Registration
	Payment      *payment.PaymentContext
}

type CreateRegistrationService struct {
	tenantID                   uuid.UUID
	sessionReader              questionnaireSessionReader
	freeRegistrar              freeRegistrationConfirmer
	paidRegistrar              paidRegistrationStarter
	privacyPolicyVersion       string
	manualContactEnabled       bool
	contactPolicyVersion       string
	paidEnabled                bool
	paymentAppID               string
	paymentMerchantID          string
	merchantConfigGenerationID uuid.UUID
}

func NewCreateRegistrationService(
	tenantID uuid.UUID,
	sessionReader questionnaireSessionReader,
	freeRegistrar freeRegistrationConfirmer,
	paidRegistrar paidRegistrationStarter,
	config CreateRegistrationConfig,
) (*CreateRegistrationService, error) {
	config.PrivacyPolicyVersion = strings.TrimSpace(
		config.PrivacyPolicyVersion,
	)
	config.ContactPolicyVersion = strings.TrimSpace(
		config.ContactPolicyVersion,
	)
	config.PaymentAppID = strings.TrimSpace(config.PaymentAppID)
	config.PaymentMerchantID = strings.TrimSpace(config.PaymentMerchantID)
	if tenantID == uuid.Nil || sessionReader == nil || freeRegistrar == nil ||
		(config.PrivacyPolicyVersion != "" &&
			!validPublicPolicyVersion(config.PrivacyPolicyVersion)) ||
		(config.ManualContactEnabled &&
			invalidRegistrationConfigText(config.ContactPolicyVersion, 100)) ||
		(!config.ManualContactEnabled && config.ContactPolicyVersion != "") ||
		(config.PaidRegistrationEnabled &&
			(paidRegistrar == nil ||
				invalidRegistrationConfigText(config.PaymentAppID, 64) ||
				invalidRegistrationConfigText(config.PaymentMerchantID, 64) ||
				config.MerchantConfigGenerationID == uuid.Nil)) ||
		(!config.PaidRegistrationEnabled &&
			(config.PaymentAppID != "" || config.PaymentMerchantID != "" ||
				config.MerchantConfigGenerationID != uuid.Nil)) {
		return nil, ErrInvalidCreateRegistrationService
	}
	return &CreateRegistrationService{
		tenantID:                   tenantID,
		sessionReader:              sessionReader,
		freeRegistrar:              freeRegistrar,
		paidRegistrar:              paidRegistrar,
		privacyPolicyVersion:       config.PrivacyPolicyVersion,
		manualContactEnabled:       config.ManualContactEnabled,
		contactPolicyVersion:       config.ContactPolicyVersion,
		paidEnabled:                config.PaidRegistrationEnabled,
		paymentAppID:               config.PaymentAppID,
		paymentMerchantID:          config.PaymentMerchantID,
		merchantConfigGenerationID: config.MerchantConfigGenerationID,
	}, nil
}

func (service *CreateRegistrationService) Create(
	ctx context.Context,
	principalID uuid.UUID,
	sessionID uuid.UUID,
	request CreateRegistrationRequest,
) (CreateRegistrationResult, error) {
	if service == nil || service.tenantID == uuid.Nil ||
		service.sessionReader == nil || service.freeRegistrar == nil {
		return CreateRegistrationResult{}, ErrInvalidCreateRegistrationService
	}
	if ctx == nil || principalID == uuid.Nil || sessionID == uuid.Nil ||
		request.IdempotencyKey == uuid.Nil ||
		request.IdempotencyKey.Version() != 4 ||
		request.IdempotencyKey.Variant() != uuid.RFC4122 ||
		request.InstancePublicationVersion < 1 || request.PriceCents < 0 {
		return CreateRegistrationResult{}, ErrInvalidCreateRegistrationRequest
	}
	if request.PriceCents == 0 && request.CouponID != nil {
		return CreateRegistrationResult{}, ErrInvalidCreateRegistrationRequest
	}
	if request.ContactPolicyVersion != strings.TrimSpace(request.ContactPolicyVersion) ||
		invalidRegistrationConfigText(request.ContactPolicyVersion, 100) {
		return CreateRegistrationResult{}, ErrManualRegistrationContactUnavailable
	}
	if request.PrivacyPolicyVersion != "" &&
		!validPublicPolicyVersion(request.PrivacyPolicyVersion) {
		return CreateRegistrationResult{}, ErrRegistrationPrivacyPolicyUnavailable
	}
	submission := registration.RegistrationSubmission{
		InstancePublicationVersion: request.InstancePublicationVersion,
		PriceCents:                 request.PriceCents,
		PrivacyPolicyVersion:       request.PrivacyPolicyVersion,
		Contact: registration.ContactSnapshot{
			Source:        registration.ContactSourceManual,
			Name:          request.ContactName,
			PhoneE164:     request.ContactPhoneE164,
			PolicyVersion: request.ContactPolicyVersion,
		},
		QuestionnaireVersionID: cloneCreateRegistrationUUID(
			request.QuestionnaireVersionID,
		),
		Answers: cloneCreateRegistrationAnswers(request.Answers),
	}
	if replayed, found, err := service.replayCommittedRegistration(
		ctx,
		principalID,
		sessionID,
		request.IdempotencyKey,
		submission,
		request.CouponID,
	); err != nil || found {
		return replayed, err
	}
	if !validPublicPolicyVersion(service.privacyPolicyVersion) {
		return CreateRegistrationResult{}, ErrRegistrationPrivacyPolicyUnavailable
	}
	page, err := service.sessionReader.ReadSessionDetail(ctx, sessionID)
	if err != nil {
		return service.replayOrCurrentFailure(
			ctx,
			principalID,
			sessionID,
			request.IdempotencyKey,
			submission,
			request.CouponID,
			fmt.Errorf(
				"read Registration Session identity: %w",
				err,
			),
		)
	}
	if page.BrandStatus != activity.BrandLifecycleActive ||
		!page.Detail.CTA.Enabled ||
		page.Detail.CTA.Action !=
			activity.SessionDetailCTAActionStartRegistration ||
		page.Detail.SeriesID == uuid.Nil || page.Detail.InstanceID == uuid.Nil ||
		page.Detail.SessionID != sessionID {
		return service.replayOrCurrentFailure(
			ctx,
			principalID,
			sessionID,
			request.IdempotencyKey,
			submission,
			request.CouponID,
			registrationpostgres.ErrRegistrationUnavailable,
		)
	}
	if page.Detail.PublicationVersion !=
		request.InstancePublicationVersion ||
		page.Detail.PriceCents != request.PriceCents {
		return service.replayOrCurrentFailure(
			ctx,
			principalID,
			sessionID,
			request.IdempotencyKey,
			submission,
			request.CouponID,
			registrationpostgres.ErrRegistrationTransactionConflict,
		)
	}
	if request.PriceCents == 0 {
		return service.confirmFreeRegistration(
			ctx,
			principalID,
			page.Detail,
			request.IdempotencyKey,
			submission,
		)
	}
	return service.startPaidRegistration(
		ctx,
		principalID,
		page.Detail,
		request.IdempotencyKey,
		submission,
		request.CouponID,
	)
}

func (service *CreateRegistrationService) replayCommittedRegistration(
	ctx context.Context,
	principalID uuid.UUID,
	sessionID uuid.UUID,
	idempotencyKey uuid.UUID,
	submission registration.RegistrationSubmission,
	couponID *uuid.UUID,
) (CreateRegistrationResult, bool, error) {
	if submission.PriceCents == 0 {
		replayed, found, err := service.freeRegistrar.Replay(
			ctx,
			registrationpostgres.ReplayFreeRegistrationCommand{
				TenantID:       service.tenantID,
				SessionID:      sessionID,
				PrincipalID:    principalID,
				IdempotencyKey: idempotencyKey.String(),
				Submission:     submission,
			},
		)
		if err != nil {
			return CreateRegistrationResult{}, false, fmt.Errorf(
				"replay xiangwan free Registration: %w",
				err,
			)
		}
		if !found {
			return CreateRegistrationResult{}, false, nil
		}
		detail := activity.SessionDetail{
			SeriesID:   replayed.SeriesID,
			InstanceID: replayed.InstanceID,
			SessionID:  sessionID,
		}
		if !registrationResultIdentityMatches(
			replayed,
			service.tenantID,
			principalID,
			detail,
			idempotencyKey,
		) || (replayed.ParticipationStatus !=
			registration.ParticipationStatusConfirmed &&
			replayed.ParticipationStatus !=
				registration.ParticipationStatusCancelled) {
			return CreateRegistrationResult{}, false,
				ErrCreateRegistrationConflict
		}
		return CreateRegistrationResult{Registration: replayed}, true, nil
	}
	if service.paidRegistrar == nil {
		return CreateRegistrationResult{}, false, nil
	}
	replayed, found, err := service.paidRegistrar.Replay(
		ctx,
		paymentpostgres.ReplayPaidRegistrationCommand{
			TenantID:       service.tenantID,
			SessionID:      sessionID,
			PrincipalID:    principalID,
			IdempotencyKey: idempotencyKey.String(),
			Submission:     submission,
			CouponID:       cloneCreateRegistrationUUID(couponID),
		},
	)
	if err != nil {
		return CreateRegistrationResult{}, false, fmt.Errorf(
			"replay xiangwan paid Registration: %w",
			err,
		)
	}
	if !found {
		return CreateRegistrationResult{}, false, nil
	}
	detail := activity.SessionDetail{
		SeriesID:   replayed.Registration.SeriesID,
		InstanceID: replayed.Registration.InstanceID,
		SessionID:  sessionID,
		PriceCents: submission.PriceCents,
	}
	merchantOrderNo := strings.ReplaceAll(idempotencyKey.String(), "-", "")
	if !registrationResultIdentityMatches(
		replayed.Registration,
		service.tenantID,
		principalID,
		detail,
		idempotencyKey,
	) || !paidRegistrationResultMatches(
		replayed,
		idempotencyKey,
		merchantOrderNo,
		replayed.Payment.Order.PaymentAppID,
		replayed.Payment.Order.PaymentMerchantID,
		replayed.Payment.Order.MerchantConfigGenerationID,
		couponID,
		submission.PriceCents,
	) {
		return CreateRegistrationResult{}, false,
			ErrCreateRegistrationConflict
	}
	paymentContext := replayed.Payment
	return CreateRegistrationResult{
		Registration: replayed.Registration,
		Payment:      &paymentContext,
	}, true, nil
}

func (service *CreateRegistrationService) replayOrCurrentFailure(
	ctx context.Context,
	principalID uuid.UUID,
	sessionID uuid.UUID,
	idempotencyKey uuid.UUID,
	submission registration.RegistrationSubmission,
	couponID *uuid.UUID,
	currentFailure error,
) (CreateRegistrationResult, error) {
	replayed, found, err := service.replayCommittedRegistration(
		ctx,
		principalID,
		sessionID,
		idempotencyKey,
		submission,
		couponID,
	)
	if err != nil || found {
		return replayed, err
	}
	return CreateRegistrationResult{}, currentFailure
}

func (service *CreateRegistrationService) confirmFreeRegistration(
	ctx context.Context,
	principalID uuid.UUID,
	detail activity.SessionDetail,
	idempotencyKey uuid.UUID,
	submission registration.RegistrationSubmission,
) (CreateRegistrationResult, error) {
	created, err := service.freeRegistrar.Confirm(
		ctx,
		registrationpostgres.ConfirmFreeRegistrationCommand{
			TenantID:             service.tenantID,
			SeriesID:             detail.SeriesID,
			InstanceID:           detail.InstanceID,
			SessionID:            detail.SessionID,
			PrincipalID:          principalID,
			IdempotencyKey:       idempotencyKey.String(),
			Submission:           &submission,
			PrivacyPolicyVersion: service.privacyPolicyVersion,
			ManualContactEnabled: service.manualContactEnabled,
			ContactPolicyVersion: service.contactPolicyVersion,
		},
	)
	if err != nil {
		if errors.Is(err, registrationpostgres.ErrRegistrationPrivacyPolicyConflict) {
			return CreateRegistrationResult{}, ErrRegistrationPrivacyPolicyUnavailable
		}
		if errors.Is(err, registrationpostgres.ErrRegistrationContactPolicyConflict) {
			return CreateRegistrationResult{}, ErrManualRegistrationContactUnavailable
		}
		return CreateRegistrationResult{}, fmt.Errorf(
			"confirm xiangwan free Registration: %w",
			err,
		)
	}
	if !registrationResultIdentityMatches(
		created,
		service.tenantID,
		principalID,
		detail,
		idempotencyKey,
	) || (created.ParticipationStatus !=
		registration.ParticipationStatusConfirmed &&
		created.ParticipationStatus !=
			registration.ParticipationStatusCancelled) {
		return CreateRegistrationResult{}, ErrCreateRegistrationConflict
	}
	return CreateRegistrationResult{Registration: created}, nil
}

func (service *CreateRegistrationService) startPaidRegistration(
	ctx context.Context,
	principalID uuid.UUID,
	detail activity.SessionDetail,
	idempotencyKey uuid.UUID,
	submission registration.RegistrationSubmission,
	couponID *uuid.UUID,
) (CreateRegistrationResult, error) {
	if !service.paidEnabled || service.paidRegistrar == nil {
		return CreateRegistrationResult{}, ErrPaidRegistrationUnavailable
	}
	merchantOrderNo := strings.ReplaceAll(idempotencyKey.String(), "-", "")
	started, err := service.paidRegistrar.Start(
		ctx,
		paymentpostgres.StartPaidRegistrationCommand{
			TenantID:                   service.tenantID,
			SeriesID:                   detail.SeriesID,
			InstanceID:                 detail.InstanceID,
			SessionID:                  detail.SessionID,
			PrincipalID:                principalID,
			IdempotencyKey:             idempotencyKey.String(),
			MerchantOrderNo:            merchantOrderNo,
			PaymentAppID:               service.paymentAppID,
			PaymentMerchantID:          service.paymentMerchantID,
			MerchantConfigGenerationID: service.merchantConfigGenerationID,
			CouponID:                   cloneCreateRegistrationUUID(couponID),
			Submission:                 &submission,
			PrivacyPolicyVersion:       service.privacyPolicyVersion,
			ManualContactEnabled:       service.manualContactEnabled,
			ContactPolicyVersion:       service.contactPolicyVersion,
		},
	)
	if err != nil {
		if errors.Is(err, paymentpostgres.ErrPaidRegistrationPrivacyPolicyConflict) {
			return CreateRegistrationResult{}, ErrRegistrationPrivacyPolicyUnavailable
		}
		if errors.Is(err, paymentpostgres.ErrPaidRegistrationContactPolicyConflict) {
			return CreateRegistrationResult{}, ErrManualRegistrationContactUnavailable
		}
		return CreateRegistrationResult{}, fmt.Errorf(
			"start xiangwan paid Registration: %w",
			err,
		)
	}
	if !registrationResultIdentityMatches(
		started.Registration,
		service.tenantID,
		principalID,
		detail,
		idempotencyKey,
	) || !paidRegistrationResultMatches(
		started,
		idempotencyKey,
		merchantOrderNo,
		service.paymentAppID,
		service.paymentMerchantID,
		service.merchantConfigGenerationID,
		couponID,
		detail.PriceCents,
	) {
		return CreateRegistrationResult{}, ErrCreateRegistrationConflict
	}
	paymentContext := started.Payment
	return CreateRegistrationResult{
		Registration: started.Registration,
		Payment:      &paymentContext,
	}, nil
}

func registrationResultIdentityMatches(
	created registration.Registration,
	tenantID uuid.UUID,
	principalID uuid.UUID,
	detail activity.SessionDetail,
	idempotencyKey uuid.UUID,
) bool {
	return created.ID != uuid.Nil && created.Version >= 1 &&
		created.TenantID == tenantID && created.SeriesID == detail.SeriesID &&
		created.InstanceID == detail.InstanceID &&
		created.SessionID == detail.SessionID &&
		created.PrincipalID == principalID &&
		created.IdempotencyKey == idempotencyKey.String()
}

func paidRegistrationResultMatches(
	started paymentpostgres.PaidRegistrationContext,
	idempotencyKey uuid.UUID,
	merchantOrderNo string,
	paymentAppID string,
	paymentMerchantID string,
	merchantConfigGenerationID uuid.UUID,
	couponID *uuid.UUID,
	priceCents int64,
) bool {
	if invalidRegistrationConfigText(paymentAppID, 64) ||
		invalidRegistrationConfigText(paymentMerchantID, 64) {
		return false
	}
	registrationValue := started.Registration
	order := started.Payment.Order
	hold := started.Payment.Hold
	stateCoherent := registrationValue.ParticipationStatus ==
		registration.ParticipationStatusCancelled ||
		(registrationValue.ParticipationStatus ==
			registration.ParticipationStatusPendingPayment &&
			(order.PaymentStatus == payment.OrderStatusPending ||
				order.PaymentStatus == payment.OrderStatusUnknown) &&
			hold.HoldStatus == payment.CapacityHoldStatusActive) ||
		(registrationValue.ParticipationStatus ==
			registration.ParticipationStatusConfirmed &&
			order.PaymentStatus == payment.OrderStatusPaidConfirmed &&
			hold.HoldStatus == payment.CapacityHoldStatusConverted)
	if (couponID == nil && started.Coupon != nil) ||
		(couponID != nil && (started.Coupon == nil || started.Coupon.Instrument.ID != *couponID)) ||
		order.ID == uuid.Nil || hold.ID == uuid.Nil ||
		order.Version < 1 || hold.Version < 1 || order.CreatedAt.IsZero() ||
		hold.CreatedAt.IsZero() ||
		hold.ExpiresAt.Sub(hold.CreatedAt) != payment.CapacityHoldDuration ||
		!stateCoherent ||
		(order.PaymentStatus != payment.OrderStatusPending &&
			order.PaymentStatus != payment.OrderStatusUnknown &&
			order.PaymentStatus != payment.OrderStatusPaidConfirmed &&
			order.PaymentStatus != payment.OrderStatusClosedUnpaid) ||
		(hold.HoldStatus != payment.CapacityHoldStatusActive &&
			hold.HoldStatus != payment.CapacityHoldStatusConverted &&
			hold.HoldStatus != payment.CapacityHoldStatusReleased &&
			hold.HoldStatus != payment.CapacityHoldStatusExpired) ||
		(registrationValue.ParticipationStatus !=
			registration.ParticipationStatusPendingPayment &&
			registrationValue.ParticipationStatus !=
				registration.ParticipationStatusConfirmed &&
			registrationValue.ParticipationStatus !=
				registration.ParticipationStatusCancelled) {
		return false
	}
	return order.TenantID == registrationValue.TenantID &&
		order.RegistrationID == registrationValue.ID &&
		order.SeriesID == registrationValue.SeriesID &&
		order.InstanceID == registrationValue.InstanceID &&
		order.SessionID == registrationValue.SessionID &&
		order.PrincipalID == registrationValue.PrincipalID &&
		order.IdempotencyKey == idempotencyKey.String() &&
		order.MerchantOrderNo == merchantOrderNo &&
		order.PaymentAppID == paymentAppID &&
		order.PaymentMerchantID == paymentMerchantID &&
		order.MerchantConfigGenerationID == merchantConfigGenerationID &&
		order.OriginalPriceCents == priceCents &&
		order.DiscountCents == expectedDiscountCents(started.Coupon) &&
		order.PayableCents == priceCents-expectedDiscountCents(started.Coupon) && hold.TenantID == order.TenantID &&
		hold.OrderID == order.ID && hold.RegistrationID == registrationValue.ID &&
		hold.SessionID == registrationValue.SessionID
}

func expectedDiscountCents(applied *paymentpostgres.AppliedCoupon) int64 {
	if applied == nil {
		return 0
	}
	return applied.Instrument.FaceValueCents
}

func invalidRegistrationConfigText(value string, maximum int) bool {
	return value == "" || len([]rune(value)) > maximum ||
		strings.ContainsAny(value, "\r\n\x00")
}

func cloneCreateRegistrationUUID(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneCreateRegistrationAnswers(
	answers []activity.QuestionnaireAnswer,
) []activity.QuestionnaireAnswer {
	cloned := make([]activity.QuestionnaireAnswer, len(answers))
	for index, answer := range answers {
		cloned[index] = activity.QuestionnaireAnswer{
			FieldID: answer.FieldID,
			Values:  append([]string(nil), answer.Values...),
		}
	}
	return cloned
}
