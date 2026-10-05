package payment

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

const (
	PaymentCloseJobLeaseDuration = 30 * time.Second
	PaymentCloseJobRetryDelay    = 5 * time.Second
)

type PaymentCloseJobStatus string

const (
	PaymentCloseJobStatusPending    PaymentCloseJobStatus = "pending"
	PaymentCloseJobStatusInProgress PaymentCloseJobStatus = "in_progress"
	PaymentCloseJobStatusRetry      PaymentCloseJobStatus = "retry"
	PaymentCloseJobStatusCompleted  PaymentCloseJobStatus = "completed"
)

type PaymentCloseResolution string

const (
	PaymentCloseResolutionNone             PaymentCloseResolution = ""
	PaymentCloseResolutionProviderClosed   PaymentCloseResolution = "provider_closed"
	PaymentCloseResolutionProviderTerminal PaymentCloseResolution = "provider_terminal"
	PaymentCloseResolutionProviderAbsent   PaymentCloseResolution = "provider_absent"
	PaymentCloseResolutionPaymentConverged PaymentCloseResolution = "payment_converged"
	PaymentCloseResolutionManualReview     PaymentCloseResolution = "manual_review"
)

type PaymentCloseJob struct {
	ID                uuid.UUID
	TenantID          uuid.UUID
	OrderID           uuid.UUID
	PrincipalID       uuid.UUID
	PaymentAppID      string
	PaymentMerchantID string
	// MerchantConfigGenerationID is copied from the immutable Order snapshot.
	// A zero value is retained for legacy jobs created before the fence.
	MerchantConfigGenerationID uuid.UUID
	OutTradeNo                 string
	AmountCents                int64
	JobStatus                  PaymentCloseJobStatus
	GenerationID               *uuid.UUID
	OwnerToken                 *uuid.UUID
	LeaseExpiresAt             *time.Time
	NextAttemptAt              *time.Time
	AttemptCount               int
	LastTradeState             *ProviderTradeState
	LastErrorClass             *string
	LastErrorCode              *string
	LastProviderRequestID      *string
	Resolution                 PaymentCloseResolution
	CompletedAt                *time.Time
	Version                    int64
	CreatedAt                  time.Time
	UpdatedAt                  time.Time
}

type ProviderPaymentCloseRequest struct {
	AppID      string
	MerchantID string
	OutTradeNo string
}

type ProviderPaymentCloseResult struct {
	ProviderRequestID string
}

type PaymentCloseJobAcquisition struct {
	Found           bool
	Job             PaymentCloseJob
	Order           Order
	InvocationToken uuid.UUID
	QueryRequest    *ProviderPaymentQueryRequest
	CloseRequest    *ProviderPaymentCloseRequest
}

type PaymentCloseJobCompletion struct {
	Resolution        PaymentCloseResolution
	TradeState        *ProviderTradeState
	Failure           error
	ProviderRequestID string
}

type PaymentCloseJobStore interface {
	AcquirePaymentCloseJob(
		context.Context,
		uuid.UUID,
		uuid.UUID,
		uuid.UUID,
		string,
		string,
	) (PaymentCloseJobAcquisition, error)
	CompletePaymentCloseJob(
		context.Context,
		PaymentCloseJobAcquisition,
		*TransactionObservation,
		PaymentCloseJobCompletion,
	) (PaymentCloseJob, error)
}

type PaymentCloseProviderPort interface {
	PaymentQueryProviderPort
	ClosePaymentByOutTradeNo(
		context.Context,
		ProviderPaymentCloseRequest,
	) (ProviderPaymentCloseResult, error)
}

var (
	ErrInvalidPaymentCloseJob   = errors.New("invalid xiangwan payment close job")
	ErrPaymentCloseJobNotDue    = errors.New("xiangwan payment close job is not due")
	ErrPaymentCloseJobLeaseLost = errors.New("xiangwan payment close job lease was lost")
	ErrPaymentCloseJobTerminal  = errors.New("xiangwan payment close job is terminal")
)

func AcquirePaymentCloseJob(
	current PaymentCloseJob,
	generationID uuid.UUID,
	ownerToken uuid.UUID,
	at time.Time,
) (PaymentCloseJob, error) {
	if validatePaymentCloseJob(current) != nil || generationID == uuid.Nil ||
		ownerToken == uuid.Nil || at.IsZero() {
		return PaymentCloseJob{}, ErrInvalidPaymentCloseJob
	}
	now := at.UTC()
	switch current.JobStatus {
	case PaymentCloseJobStatusPending, PaymentCloseJobStatusRetry:
		if current.NextAttemptAt == nil || now.Before(*current.NextAttemptAt) {
			return PaymentCloseJob{}, ErrPaymentCloseJobNotDue
		}
	case PaymentCloseJobStatusInProgress:
		if current.LeaseExpiresAt == nil || now.Before(*current.LeaseExpiresAt) {
			return PaymentCloseJob{}, ErrPaymentCloseJobNotDue
		}
	case PaymentCloseJobStatusCompleted:
		return PaymentCloseJob{}, ErrPaymentCloseJobTerminal
	default:
		return PaymentCloseJob{}, ErrInvalidPaymentCloseJob
	}
	updated := clonePaymentCloseJob(current)
	leaseExpiresAt := now.Add(PaymentCloseJobLeaseDuration)
	updated.JobStatus = PaymentCloseJobStatusInProgress
	updated.GenerationID = cloneUUID(&generationID)
	updated.OwnerToken = cloneUUID(&ownerToken)
	updated.LeaseExpiresAt = &leaseExpiresAt
	updated.NextAttemptAt = nil
	updated.AttemptCount++
	updated.LastTradeState = nil
	updated.LastErrorClass = nil
	updated.LastErrorCode = nil
	updated.LastProviderRequestID = nil
	updated.Resolution = PaymentCloseResolutionNone
	updated.CompletedAt = nil
	updated.Version++
	updated.UpdatedAt = now
	return updated, nil
}

func RetryPaymentCloseJob(
	current PaymentCloseJob,
	ownerToken uuid.UUID,
	completion PaymentCloseJobCompletion,
	at time.Time,
) (PaymentCloseJob, error) {
	if err := validateOwnedPaymentCloseJob(current, ownerToken, at); err != nil {
		return PaymentCloseJob{}, err
	}
	tradeState, errorClass, errorCode, err := paymentCloseCompletionFacts(
		completion,
		false,
	)
	if err != nil || (tradeState == nil && errorClass == nil) {
		return PaymentCloseJob{}, ErrInvalidPaymentCloseJob
	}
	now := at.UTC()
	nextAttemptAt := now.Add(PaymentCloseJobRetryDelay)
	updated := clonePaymentCloseJob(current)
	updated.JobStatus = PaymentCloseJobStatusRetry
	updated.OwnerToken = nil
	updated.LeaseExpiresAt = nil
	updated.NextAttemptAt = &nextAttemptAt
	updated.LastTradeState = tradeState
	updated.LastErrorClass = errorClass
	updated.LastErrorCode = errorCode
	updated.LastProviderRequestID = optionalBoundedText(
		completion.ProviderRequestID,
		128,
	)
	updated.Resolution = PaymentCloseResolutionNone
	updated.CompletedAt = nil
	updated.Version++
	updated.UpdatedAt = now
	return updated, nil
}

func CompletePaymentCloseJob(
	current PaymentCloseJob,
	ownerToken uuid.UUID,
	completion PaymentCloseJobCompletion,
	at time.Time,
) (PaymentCloseJob, error) {
	if err := validateOwnedPaymentCloseJob(current, ownerToken, at); err != nil {
		return PaymentCloseJob{}, err
	}
	tradeState, errorClass, errorCode, err := paymentCloseCompletionFacts(
		completion,
		true,
	)
	if err != nil {
		return PaymentCloseJob{}, err
	}
	now := at.UTC()
	updated := clonePaymentCloseJob(current)
	updated.JobStatus = PaymentCloseJobStatusCompleted
	updated.OwnerToken = nil
	updated.LeaseExpiresAt = nil
	updated.NextAttemptAt = nil
	updated.LastTradeState = tradeState
	updated.LastErrorClass = errorClass
	updated.LastErrorCode = errorCode
	updated.LastProviderRequestID = optionalBoundedText(
		completion.ProviderRequestID,
		128,
	)
	updated.Resolution = completion.Resolution
	updated.CompletedAt = &now
	updated.Version++
	updated.UpdatedAt = now
	return updated, nil
}

func ValidateProviderPaymentCloseRequest(request ProviderPaymentCloseRequest) error {
	if invalidBoundedText(request.AppID, 64) ||
		invalidBoundedText(request.MerchantID, 64) ||
		!outTradeNoPattern.MatchString(request.OutTradeNo) {
		return ErrInvalidPaymentCloseJob
	}
	return nil
}

func ValidateProviderPaymentCloseResult(result ProviderPaymentCloseResult) error {
	if result.ProviderRequestID != "" &&
		invalidBoundedText(result.ProviderRequestID, 128) {
		return ErrInvalidPaymentCloseJob
	}
	return nil
}

func paymentCloseCompletionFacts(
	completion PaymentCloseJobCompletion,
	terminal bool,
) (*ProviderTradeState, *string, *string, error) {
	tradeState := cloneProviderTradeState(completion.TradeState)
	var errorClass *string
	var errorCode *string
	if completion.Failure != nil {
		class, code := ProviderFailureMetadata(completion.Failure)
		if !providerFailureClassPattern.MatchString(class) {
			return nil, nil, nil, ErrInvalidPaymentCloseJob
		}
		errorClass = &class
		errorCode = optionalBoundedText(code, 64)
	}
	if completion.ProviderRequestID != "" &&
		invalidBoundedText(completion.ProviderRequestID, 128) {
		return nil, nil, nil, ErrInvalidPaymentCloseJob
	}
	if tradeState != nil && !validProviderTradeState(*tradeState) {
		return nil, nil, nil, ErrInvalidPaymentCloseJob
	}
	if !terminal {
		if completion.Resolution != PaymentCloseResolutionNone ||
			(tradeState != nil && *tradeState != ProviderTradeStateNotPay &&
				*tradeState != ProviderTradeStateUserPaying) {
			return nil, nil, nil, ErrInvalidPaymentCloseJob
		}
		return tradeState, errorClass, errorCode, nil
	}
	valid := false
	switch completion.Resolution {
	case PaymentCloseResolutionProviderClosed:
		valid = tradeState != nil && *tradeState == ProviderTradeStateNotPay &&
			errorClass == nil
	case PaymentCloseResolutionProviderTerminal:
		valid = tradeState != nil && IsProviderTerminalUnpaidState(*tradeState) &&
			errorClass == nil
	case PaymentCloseResolutionProviderAbsent:
		valid = tradeState == nil && errorClass != nil && errorCode != nil &&
			(*errorCode == "ORDER_NOT_EXIST" || *errorCode == "ORDERNOTEXIST")
	case PaymentCloseResolutionPaymentConverged:
		valid = (tradeState == nil || *tradeState == ProviderTradeStateSuccess) &&
			errorClass == nil
	case PaymentCloseResolutionManualReview:
		valid = tradeState != nil && *tradeState == ProviderTradeStateRefund &&
			errorClass == nil
	}
	if !valid {
		return nil, nil, nil, ErrInvalidPaymentCloseJob
	}
	return tradeState, errorClass, errorCode, nil
}

func validateOwnedPaymentCloseJob(
	current PaymentCloseJob,
	ownerToken uuid.UUID,
	at time.Time,
) error {
	if validatePaymentCloseJob(current) != nil || ownerToken == uuid.Nil ||
		at.IsZero() || at.Before(current.UpdatedAt) {
		return ErrInvalidPaymentCloseJob
	}
	if current.JobStatus != PaymentCloseJobStatusInProgress ||
		current.OwnerToken == nil || *current.OwnerToken != ownerToken {
		return ErrPaymentCloseJobLeaseLost
	}
	return nil
}

func validatePaymentCloseJob(value PaymentCloseJob) error {
	request := ProviderPaymentCloseRequest{
		AppID:      value.PaymentAppID,
		MerchantID: value.PaymentMerchantID,
		OutTradeNo: value.OutTradeNo,
	}
	if value.ID == uuid.Nil || value.TenantID == uuid.Nil ||
		value.OrderID == uuid.Nil || value.PrincipalID == uuid.Nil ||
		value.AmountCents <= 0 || value.Version < 1 || value.AttemptCount < 0 ||
		value.CreatedAt.IsZero() || value.UpdatedAt.Before(value.CreatedAt) ||
		ValidateProviderPaymentCloseRequest(request) != nil {
		return ErrInvalidPaymentCloseJob
	}
	switch value.JobStatus {
	case PaymentCloseJobStatusPending:
		if value.GenerationID != nil || value.OwnerToken != nil ||
			value.LeaseExpiresAt != nil || value.NextAttemptAt == nil ||
			value.NextAttemptAt.Before(value.UpdatedAt) || value.AttemptCount != 0 ||
			paymentCloseJobHasLastFacts(value) ||
			value.Resolution != PaymentCloseResolutionNone || value.CompletedAt != nil {
			return ErrInvalidPaymentCloseJob
		}
	case PaymentCloseJobStatusInProgress:
		if value.GenerationID == nil || *value.GenerationID == uuid.Nil ||
			value.OwnerToken == nil || *value.OwnerToken == uuid.Nil ||
			value.LeaseExpiresAt == nil ||
			!value.LeaseExpiresAt.After(value.UpdatedAt) ||
			value.NextAttemptAt != nil || value.AttemptCount < 1 ||
			paymentCloseJobHasLastFacts(value) ||
			value.Resolution != PaymentCloseResolutionNone || value.CompletedAt != nil {
			return ErrInvalidPaymentCloseJob
		}
	case PaymentCloseJobStatusRetry:
		if value.GenerationID == nil || *value.GenerationID == uuid.Nil ||
			value.OwnerToken != nil || value.LeaseExpiresAt != nil ||
			value.NextAttemptAt == nil ||
			value.NextAttemptAt.Before(value.UpdatedAt) || value.AttemptCount < 1 ||
			(value.LastTradeState == nil && value.LastErrorClass == nil) ||
			value.Resolution != PaymentCloseResolutionNone || value.CompletedAt != nil {
			return ErrInvalidPaymentCloseJob
		}
	case PaymentCloseJobStatusCompleted:
		if value.GenerationID == nil || *value.GenerationID == uuid.Nil ||
			value.OwnerToken != nil || value.LeaseExpiresAt != nil ||
			value.NextAttemptAt != nil || value.AttemptCount < 1 ||
			value.Resolution == PaymentCloseResolutionNone || value.CompletedAt == nil ||
			value.CompletedAt.Before(value.CreatedAt) ||
			!validPaymentCloseResolutionFacts(value) {
			return ErrInvalidPaymentCloseJob
		}
	default:
		return ErrInvalidPaymentCloseJob
	}
	if value.LastTradeState != nil && !validProviderTradeState(*value.LastTradeState) {
		return ErrInvalidPaymentCloseJob
	}
	if value.LastErrorClass != nil &&
		!providerFailureClassPattern.MatchString(*value.LastErrorClass) {
		return ErrInvalidPaymentCloseJob
	}
	if value.LastErrorCode != nil && value.LastErrorClass == nil {
		return ErrInvalidPaymentCloseJob
	}
	if value.LastErrorCode != nil && !providerCodePattern.MatchString(*value.LastErrorCode) {
		return ErrInvalidPaymentCloseJob
	}
	if value.LastProviderRequestID != nil &&
		invalidBoundedText(*value.LastProviderRequestID, 128) {
		return ErrInvalidPaymentCloseJob
	}
	return nil
}

func validPaymentCloseResolutionFacts(value PaymentCloseJob) bool {
	switch value.Resolution {
	case PaymentCloseResolutionProviderClosed:
		return value.LastTradeState != nil &&
			*value.LastTradeState == ProviderTradeStateNotPay &&
			value.LastErrorClass == nil && value.LastErrorCode == nil
	case PaymentCloseResolutionProviderTerminal:
		return value.LastTradeState != nil &&
			IsProviderTerminalUnpaidState(*value.LastTradeState) &&
			value.LastErrorClass == nil && value.LastErrorCode == nil
	case PaymentCloseResolutionProviderAbsent:
		return value.LastTradeState == nil && value.LastErrorClass != nil &&
			value.LastErrorCode != nil &&
			(*value.LastErrorCode == "ORDER_NOT_EXIST" ||
				*value.LastErrorCode == "ORDERNOTEXIST")
	case PaymentCloseResolutionPaymentConverged:
		return (value.LastTradeState == nil ||
			*value.LastTradeState == ProviderTradeStateSuccess) &&
			value.LastErrorClass == nil && value.LastErrorCode == nil
	case PaymentCloseResolutionManualReview:
		return value.LastTradeState != nil &&
			*value.LastTradeState == ProviderTradeStateRefund &&
			value.LastErrorClass == nil && value.LastErrorCode == nil
	default:
		return false
	}
}

func paymentCloseJobHasLastFacts(value PaymentCloseJob) bool {
	return value.LastTradeState != nil || value.LastErrorClass != nil ||
		value.LastErrorCode != nil || value.LastProviderRequestID != nil
}

func cloneProviderTradeState(value *ProviderTradeState) *ProviderTradeState {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func clonePaymentCloseJob(value PaymentCloseJob) PaymentCloseJob {
	cloned := value
	cloned.GenerationID = cloneUUID(value.GenerationID)
	cloned.OwnerToken = cloneUUID(value.OwnerToken)
	cloned.LeaseExpiresAt = cloneTime(value.LeaseExpiresAt)
	cloned.NextAttemptAt = cloneTime(value.NextAttemptAt)
	cloned.LastTradeState = cloneProviderTradeState(value.LastTradeState)
	cloned.LastErrorClass = cloneString(value.LastErrorClass)
	cloned.LastErrorCode = cloneString(value.LastErrorCode)
	cloned.LastProviderRequestID = cloneString(value.LastProviderRequestID)
	cloned.CompletedAt = cloneTime(value.CompletedAt)
	return cloned
}
