package payment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	PaymentQueryLeaseDuration = 15 * time.Second
	PaymentQueryRetryDelay    = 2 * time.Second
	PaymentQueryCurrency      = "CNY"
	PaymentQueryTradeType     = "JSAPI"
)

type PaymentQueryStatus string

const (
	PaymentQueryStatusInProgress PaymentQueryStatus = "in_progress"
	PaymentQueryStatusPending    PaymentQueryStatus = "pending"
	PaymentQueryStatusUnknown    PaymentQueryStatus = "unknown"
	PaymentQueryStatusConverged  PaymentQueryStatus = "converged"
	PaymentQueryStatusClosed     PaymentQueryStatus = "closed"
)

type ProviderTradeState string

const (
	ProviderTradeStateSuccess    ProviderTradeState = "SUCCESS"
	ProviderTradeStateRefund     ProviderTradeState = "REFUND"
	ProviderTradeStateNotPay     ProviderTradeState = "NOTPAY"
	ProviderTradeStateClosed     ProviderTradeState = "CLOSED"
	ProviderTradeStateRevoked    ProviderTradeState = "REVOKED"
	ProviderTradeStateUserPaying ProviderTradeState = "USERPAYING"
	ProviderTradeStatePayError   ProviderTradeState = "PAYERROR"
)

type PaymentConfirmationDisposition string

const (
	PaymentConfirmationDispositionNone                   PaymentConfirmationDisposition = ""
	PaymentConfirmationDispositionParticipationConfirmed PaymentConfirmationDisposition = "participation_confirmed"
	PaymentConfirmationDispositionRefundRequired         PaymentConfirmationDisposition = "refund_required"
)

const (
	TransactionObservationSourceMerchantQuery       = "merchant_query"
	TransactionObservationSourcePaymentNotification = "payment_notification"
)

type PaymentQueryLease struct {
	ID                    uuid.UUID
	TenantID              uuid.UUID
	OrderID               uuid.UUID
	PrincipalID           uuid.UUID
	GenerationID          uuid.UUID
	PaymentAppID          string
	PaymentMerchantID     string
	OutTradeNo            string
	AmountCents           int64
	Currency              string
	QueryStatus           PaymentQueryStatus
	OwnerToken            *uuid.UUID
	LeaseExpiresAt        *time.Time
	NextQueryAt           *time.Time
	LastTradeState        *ProviderTradeState
	LastErrorClass        *string
	LastProviderRequestID *string
	CompletedAt           *time.Time
	Version               int64
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

type CreatePaymentQueryLeaseCommand struct {
	ID                uuid.UUID
	TenantID          uuid.UUID
	OrderID           uuid.UUID
	PrincipalID       uuid.UUID
	GenerationID      uuid.UUID
	PaymentAppID      string
	PaymentMerchantID string
	OutTradeNo        string
	AmountCents       int64
	OwnerToken        uuid.UUID
	Now               time.Time
}

type PaymentQueryCommand struct {
	TenantID     uuid.UUID
	GenerationID uuid.UUID
	OrderID      uuid.UUID
	PrincipalID  uuid.UUID
}

type ProviderPaymentQueryRequest struct {
	AppID       string
	MerchantID  string
	OutTradeNo  string
	AmountCents int64
	Currency    string
}

type ProviderPaymentQueryResult struct {
	AppID             string
	MerchantID        string
	OutTradeNo        string
	TransactionID     string
	TradeType         string
	TradeState        ProviderTradeState
	AmountCents       int64
	Currency          string
	SuccessAt         *time.Time
	ProviderRequestID string
}

type TransactionObservation struct {
	ID                uuid.UUID
	TenantID          uuid.UUID
	OrderID           uuid.UUID
	PrincipalID       uuid.UUID
	ObservationSource string
	SourceKey         string
	ProviderRequestID string
	PaymentAppID      string
	PaymentMerchantID string
	OutTradeNo        string
	TransactionID     string
	TradeType         string
	TradeState        ProviderTradeState
	AmountCents       int64
	Currency          string
	SuccessAt         *time.Time
	PayloadDigest     string
	ObservedAt        time.Time
}

type MerchantQueryObservationContext struct {
	ObservationID uuid.UUID
	TenantID      uuid.UUID
	OrderID       uuid.UUID
	PrincipalID   uuid.UUID
	Request       ProviderPaymentQueryRequest
}

type PaymentQueryAcquisition struct {
	Lease           PaymentQueryLease
	Order           Order
	InvocationToken uuid.UUID
	ProviderRequest *ProviderPaymentQueryRequest
}

type PaymentQueryResult struct {
	Order               Order
	QueryStatus         PaymentQueryStatus
	Disposition         PaymentConfirmationDisposition
	NextQueryAt         *time.Time
	RetryPaymentAllowed bool
}

// A pending query may also represent USERPAYING or an unknown provider state.
// Call this only for a query completed against the provider in this request.
// A cached NOTPAY lease cannot authorize another payment-sheet invocation.
// The write endpoint still rechecks Order and hold facts.
func PaymentQueryAllowsRetry(order Order, lease PaymentQueryLease) bool {
	return order.PaymentStatus == OrderStatusPending &&
		lease.QueryStatus == PaymentQueryStatusPending &&
		lease.LastTradeState != nil &&
		*lease.LastTradeState == ProviderTradeStateNotPay
}

type TrustedPaymentConfirmation struct {
	TenantID            uuid.UUID
	PaymentAppID        string
	PaymentMerchantID   string
	MerchantOrderNo     string
	WeChatTransactionID string
	ActualPaidCents     int64
	PaidAt              time.Time
	Observation         TransactionObservation
}

type PaymentConvergence struct {
	Order             Order
	Disposition       PaymentConfirmationDisposition
	ProviderRequestID string
}

type TrustedUnpaidPaymentClosure struct {
	Observation TransactionObservation
}

type PaymentQueryProviderPort interface {
	QueryPaymentByOutTradeNo(
		context.Context,
		ProviderPaymentQueryRequest,
	) (ProviderPaymentQueryResult, error)
}

type PaymentQueryStore interface {
	AcquirePaymentQuery(
		context.Context,
		PaymentQueryCommand,
	) (PaymentQueryAcquisition, error)
	CompleteObservedPaymentQuery(
		context.Context,
		PaymentQueryAcquisition,
		TransactionObservation,
	) (PaymentQueryResult, error)
	CompleteFailedPaymentQuery(
		context.Context,
		PaymentQueryAcquisition,
		error,
	) (PaymentQueryResult, error)
	CompleteFailedPaymentQueryWithObservation(
		context.Context,
		PaymentQueryAcquisition,
		TransactionObservation,
		error,
	) (PaymentQueryResult, error)
	CompleteConvergedPaymentQuery(
		context.Context,
		PaymentQueryAcquisition,
		PaymentConvergence,
	) (PaymentQueryResult, error)
	CompleteClosedPaymentQuery(
		context.Context,
		PaymentQueryAcquisition,
		TransactionObservation,
		PaymentConvergence,
	) (PaymentQueryResult, error)
}

type TrustedPaymentConfirmer interface {
	RejectedPrepayCloser
	ConfirmTrustedPayment(
		context.Context,
		TrustedPaymentConfirmation,
	) (PaymentConvergence, error)
	CloseTrustedUnpaidPayment(
		context.Context,
		TrustedUnpaidPaymentClosure,
	) (PaymentConvergence, error)
}

var (
	ErrInvalidPaymentQuery     = errors.New("invalid xiangwan payment query")
	ErrPaymentQueryLeaseActive = errors.New("xiangwan payment query lease is active")
	ErrPaymentQueryNotDue      = errors.New("xiangwan payment query is not due")
	ErrPaymentQueryLeaseLost   = errors.New("xiangwan payment query lease was lost")
	ErrPaymentQueryTerminal    = errors.New("xiangwan payment query is terminal")
)

func ValidatePaymentQueryCommand(command PaymentQueryCommand) error {
	if command.TenantID == uuid.Nil || command.GenerationID == uuid.Nil ||
		command.OrderID == uuid.Nil || command.PrincipalID == uuid.Nil {
		return ErrInvalidPaymentQuery
	}
	return nil
}

func NewPaymentQueryLease(
	command CreatePaymentQueryLeaseCommand,
) (PaymentQueryLease, error) {
	request := ProviderPaymentQueryRequest{
		AppID:       command.PaymentAppID,
		MerchantID:  command.PaymentMerchantID,
		OutTradeNo:  command.OutTradeNo,
		AmountCents: command.AmountCents,
		Currency:    PaymentQueryCurrency,
	}
	if command.ID == uuid.Nil || command.TenantID == uuid.Nil ||
		command.OrderID == uuid.Nil || command.PrincipalID == uuid.Nil ||
		command.GenerationID == uuid.Nil || command.OwnerToken == uuid.Nil ||
		command.Now.IsZero() || ValidateProviderPaymentQueryRequest(request) != nil {
		return PaymentQueryLease{}, ErrInvalidPaymentQuery
	}
	now := command.Now.UTC()
	ownerToken := command.OwnerToken
	leaseExpiresAt := now.Add(PaymentQueryLeaseDuration)
	return PaymentQueryLease{
		ID:                command.ID,
		TenantID:          command.TenantID,
		OrderID:           command.OrderID,
		PrincipalID:       command.PrincipalID,
		GenerationID:      command.GenerationID,
		PaymentAppID:      command.PaymentAppID,
		PaymentMerchantID: command.PaymentMerchantID,
		OutTradeNo:        command.OutTradeNo,
		AmountCents:       command.AmountCents,
		Currency:          PaymentQueryCurrency,
		QueryStatus:       PaymentQueryStatusInProgress,
		OwnerToken:        &ownerToken,
		LeaseExpiresAt:    &leaseExpiresAt,
		Version:           1,
		CreatedAt:         now,
		UpdatedAt:         now,
	}, nil
}

func AcquirePaymentQueryLease(
	current PaymentQueryLease,
	generationID uuid.UUID,
	ownerToken uuid.UUID,
	at time.Time,
) (PaymentQueryLease, error) {
	if err := validatePaymentQueryLease(current); err != nil ||
		generationID == uuid.Nil || ownerToken == uuid.Nil || at.IsZero() {
		return PaymentQueryLease{}, ErrInvalidPaymentQuery
	}
	now := at.UTC()
	switch current.QueryStatus {
	case PaymentQueryStatusInProgress:
		if now.Before(*current.LeaseExpiresAt) {
			return PaymentQueryLease{}, ErrPaymentQueryLeaseActive
		}
	case PaymentQueryStatusPending, PaymentQueryStatusUnknown:
		if now.Before(*current.NextQueryAt) {
			return PaymentQueryLease{}, ErrPaymentQueryNotDue
		}
	case PaymentQueryStatusConverged, PaymentQueryStatusClosed:
		return PaymentQueryLease{}, ErrPaymentQueryTerminal
	default:
		return PaymentQueryLease{}, ErrInvalidPaymentQuery
	}
	updated := clonePaymentQueryLease(current)
	leaseExpiresAt := now.Add(PaymentQueryLeaseDuration)
	updated.GenerationID = generationID
	updated.QueryStatus = PaymentQueryStatusInProgress
	updated.OwnerToken = &ownerToken
	updated.LeaseExpiresAt = &leaseExpiresAt
	updated.NextQueryAt = nil
	updated.LastTradeState = nil
	updated.LastErrorClass = nil
	updated.LastProviderRequestID = nil
	updated.CompletedAt = nil
	updated.Version++
	updated.UpdatedAt = now
	return updated, nil
}

func CompletePaymentQueryObserved(
	current PaymentQueryLease,
	ownerToken uuid.UUID,
	observation TransactionObservation,
	at time.Time,
) (PaymentQueryLease, error) {
	if err := validateOwnedPaymentQuery(current, ownerToken, at); err != nil ||
		ValidateTransactionObservation(observation) != nil ||
		observation.TenantID != current.TenantID ||
		observation.OrderID != current.OrderID ||
		observation.PrincipalID != current.PrincipalID ||
		observation.PaymentAppID != current.PaymentAppID ||
		observation.PaymentMerchantID != current.PaymentMerchantID ||
		observation.OutTradeNo != current.OutTradeNo ||
		observation.AmountCents != current.AmountCents {
		return PaymentQueryLease{}, ErrInvalidPaymentQuery
	}
	if observation.TradeState == ProviderTradeStateSuccess ||
		IsProviderTerminalUnpaidState(observation.TradeState) {
		return PaymentQueryLease{}, ErrInvalidPaymentQuery
	}
	now := at.UTC()
	nextQueryAt := now.Add(PaymentQueryRetryDelay)
	tradeState := observation.TradeState
	updated := clonePaymentQueryLease(current)
	updated.QueryStatus = PaymentQueryStatusPending
	updated.OwnerToken = nil
	updated.LeaseExpiresAt = nil
	updated.NextQueryAt = &nextQueryAt
	updated.LastTradeState = &tradeState
	updated.LastErrorClass = nil
	updated.LastProviderRequestID = optionalBoundedText(
		observation.ProviderRequestID,
		128,
	)
	updated.Version++
	updated.UpdatedAt = now
	return updated, nil
}

func CompletePaymentQueryFailed(
	current PaymentQueryLease,
	ownerToken uuid.UUID,
	failure error,
	at time.Time,
) (PaymentQueryLease, error) {
	if err := validateOwnedPaymentQuery(current, ownerToken, at); err != nil {
		return PaymentQueryLease{}, err
	}
	errorClass, _ := ProviderFailureMetadata(failure)
	if !providerFailureClassPattern.MatchString(errorClass) {
		return PaymentQueryLease{}, ErrInvalidPaymentQuery
	}
	now := at.UTC()
	nextQueryAt := now.Add(PaymentQueryRetryDelay)
	updated := clonePaymentQueryLease(current)
	updated.QueryStatus = PaymentQueryStatusUnknown
	updated.OwnerToken = nil
	updated.LeaseExpiresAt = nil
	updated.NextQueryAt = &nextQueryAt
	updated.LastTradeState = nil
	updated.LastErrorClass = &errorClass
	updated.LastProviderRequestID = nil
	updated.Version++
	updated.UpdatedAt = now
	return updated, nil
}

func CompletePaymentQueryConverged(
	current PaymentQueryLease,
	ownerToken uuid.UUID,
	providerRequestID string,
	at time.Time,
) (PaymentQueryLease, error) {
	if err := validateOwnedPaymentQuery(current, ownerToken, at); err != nil {
		return PaymentQueryLease{}, err
	}
	now := at.UTC()
	tradeState := ProviderTradeStateSuccess
	updated := clonePaymentQueryLease(current)
	updated.QueryStatus = PaymentQueryStatusConverged
	updated.OwnerToken = nil
	updated.LeaseExpiresAt = nil
	updated.NextQueryAt = nil
	updated.LastTradeState = &tradeState
	updated.LastErrorClass = nil
	updated.LastProviderRequestID = optionalBoundedText(providerRequestID, 128)
	updated.CompletedAt = &now
	updated.Version++
	updated.UpdatedAt = now
	return updated, nil
}

func CompletePaymentQueryClosed(
	current PaymentQueryLease,
	ownerToken uuid.UUID,
	observation TransactionObservation,
	at time.Time,
) (PaymentQueryLease, error) {
	if err := validateOwnedPaymentQuery(current, ownerToken, at); err != nil ||
		ValidateTransactionObservation(observation) != nil ||
		!IsProviderTerminalUnpaidState(observation.TradeState) ||
		observation.TenantID != current.TenantID ||
		observation.OrderID != current.OrderID ||
		observation.PrincipalID != current.PrincipalID ||
		observation.PaymentAppID != current.PaymentAppID ||
		observation.PaymentMerchantID != current.PaymentMerchantID ||
		observation.OutTradeNo != current.OutTradeNo ||
		observation.AmountCents != current.AmountCents {
		return PaymentQueryLease{}, ErrInvalidPaymentQuery
	}
	now := at.UTC()
	tradeState := observation.TradeState
	updated := clonePaymentQueryLease(current)
	updated.QueryStatus = PaymentQueryStatusClosed
	updated.OwnerToken = nil
	updated.LeaseExpiresAt = nil
	updated.NextQueryAt = nil
	updated.LastTradeState = &tradeState
	updated.LastErrorClass = nil
	updated.LastProviderRequestID = optionalBoundedText(
		observation.ProviderRequestID,
		128,
	)
	updated.CompletedAt = &now
	updated.Version++
	updated.UpdatedAt = now
	return updated, nil
}

func ValidateProviderPaymentQueryRequest(
	request ProviderPaymentQueryRequest,
) error {
	if invalidBoundedText(request.AppID, 64) ||
		invalidBoundedText(request.MerchantID, 64) ||
		!outTradeNoPattern.MatchString(request.OutTradeNo) ||
		request.AmountCents <= 0 || request.Currency != PaymentQueryCurrency {
		return ErrInvalidPaymentQuery
	}
	return nil
}

func ValidateProviderPaymentQueryResult(
	result ProviderPaymentQueryResult,
	request ProviderPaymentQueryRequest,
) error {
	if ValidateProviderPaymentQueryRequest(request) != nil ||
		result.AppID != request.AppID || result.MerchantID != request.MerchantID ||
		result.OutTradeNo != request.OutTradeNo ||
		result.AmountCents != request.AmountCents || result.Currency != request.Currency ||
		result.TradeType != PaymentQueryTradeType ||
		!validProviderTradeState(result.TradeState) ||
		(result.ProviderRequestID != "" && invalidBoundedText(result.ProviderRequestID, 128)) ||
		(result.TransactionID != "" && invalidBoundedText(result.TransactionID, 128)) {
		return ErrInvalidPaymentQuery
	}
	if result.TradeState == ProviderTradeStateSuccess {
		if result.TransactionID == "" || result.SuccessAt == nil || result.SuccessAt.IsZero() {
			return ErrInvalidPaymentQuery
		}
	} else if IsProviderTerminalUnpaidState(result.TradeState) {
		if result.TransactionID != "" || result.SuccessAt != nil {
			return ErrInvalidPaymentQuery
		}
	} else if (result.TransactionID == "") != (result.SuccessAt == nil) {
		return ErrInvalidPaymentQuery
	}
	return nil
}

func NewTransactionObservation(
	acquisition PaymentQueryAcquisition,
	result ProviderPaymentQueryResult,
	observedAt time.Time,
) (TransactionObservation, error) {
	if acquisition.InvocationToken == uuid.Nil || acquisition.ProviderRequest == nil ||
		observedAt.IsZero() {
		return TransactionObservation{}, ErrInvalidPaymentQuery
	}
	return NewMerchantQueryObservation(
		MerchantQueryObservationContext{
			ObservationID: acquisition.InvocationToken,
			TenantID:      acquisition.Lease.TenantID,
			OrderID:       acquisition.Lease.OrderID,
			PrincipalID:   acquisition.Lease.PrincipalID,
			Request:       *acquisition.ProviderRequest,
		},
		result,
		observedAt,
	)
}

func NewMerchantQueryObservation(
	source MerchantQueryObservationContext,
	result ProviderPaymentQueryResult,
	observedAt time.Time,
) (TransactionObservation, error) {
	if source.ObservationID == uuid.Nil || source.TenantID == uuid.Nil ||
		source.OrderID == uuid.Nil || source.PrincipalID == uuid.Nil ||
		observedAt.IsZero() ||
		ValidateProviderPaymentQueryResult(result, source.Request) != nil {
		return TransactionObservation{}, ErrInvalidPaymentQuery
	}
	observation := TransactionObservation{
		ID:                source.ObservationID,
		TenantID:          source.TenantID,
		OrderID:           source.OrderID,
		PrincipalID:       source.PrincipalID,
		ObservationSource: TransactionObservationSourceMerchantQuery,
		SourceKey:         source.ObservationID.String(),
		ProviderRequestID: result.ProviderRequestID,
		PaymentAppID:      result.AppID,
		PaymentMerchantID: result.MerchantID,
		OutTradeNo:        result.OutTradeNo,
		TransactionID:     result.TransactionID,
		TradeType:         result.TradeType,
		TradeState:        result.TradeState,
		AmountCents:       result.AmountCents,
		Currency:          result.Currency,
		SuccessAt:         cloneTimePointer(result.SuccessAt),
		ObservedAt:        observedAt.UTC(),
	}
	observation.PayloadDigest = transactionObservationDigest(observation)
	if ValidateTransactionObservation(observation) != nil {
		return TransactionObservation{}, ErrInvalidPaymentQuery
	}
	return observation, nil
}

func ValidateTransactionObservation(observation TransactionObservation) error {
	if observation.ID == uuid.Nil || observation.TenantID == uuid.Nil ||
		observation.OrderID == uuid.Nil || observation.PrincipalID == uuid.Nil ||
		observation.ObservedAt.IsZero() ||
		!validTransactionObservationSource(observation) {
		return ErrInvalidPaymentQuery
	}
	request := ProviderPaymentQueryRequest{
		AppID:       observation.PaymentAppID,
		MerchantID:  observation.PaymentMerchantID,
		OutTradeNo:  observation.OutTradeNo,
		AmountCents: observation.AmountCents,
		Currency:    observation.Currency,
	}
	return ValidateProviderPaymentQueryResult(ProviderPaymentQueryResult{
		AppID:             observation.PaymentAppID,
		MerchantID:        observation.PaymentMerchantID,
		OutTradeNo:        observation.OutTradeNo,
		TransactionID:     observation.TransactionID,
		TradeType:         observation.TradeType,
		TradeState:        observation.TradeState,
		AmountCents:       observation.AmountCents,
		Currency:          observation.Currency,
		SuccessAt:         observation.SuccessAt,
		ProviderRequestID: observation.ProviderRequestID,
	}, request)
}

func validTransactionObservationSource(observation TransactionObservation) bool {
	switch observation.ObservationSource {
	case TransactionObservationSourceMerchantQuery:
		return observation.SourceKey == observation.ID.String() &&
			observation.PayloadDigest == transactionObservationDigest(observation)
	case TransactionObservationSourcePaymentNotification:
		return notificationIDPattern.MatchString(observation.SourceKey) &&
			payloadDigestPattern.MatchString(observation.PayloadDigest)
	default:
		return false
	}
}

func validatePaymentQueryLease(value PaymentQueryLease) error {
	request := ProviderPaymentQueryRequest{
		AppID:       value.PaymentAppID,
		MerchantID:  value.PaymentMerchantID,
		OutTradeNo:  value.OutTradeNo,
		AmountCents: value.AmountCents,
		Currency:    value.Currency,
	}
	if value.ID == uuid.Nil || value.TenantID == uuid.Nil ||
		value.OrderID == uuid.Nil || value.PrincipalID == uuid.Nil ||
		value.GenerationID == uuid.Nil || value.Version < 1 ||
		value.CreatedAt.IsZero() || value.UpdatedAt.Before(value.CreatedAt) ||
		ValidateProviderPaymentQueryRequest(request) != nil {
		return ErrInvalidPaymentQuery
	}
	switch value.QueryStatus {
	case PaymentQueryStatusInProgress:
		if value.OwnerToken == nil || *value.OwnerToken == uuid.Nil ||
			value.LeaseExpiresAt == nil || !value.LeaseExpiresAt.After(value.UpdatedAt) ||
			value.NextQueryAt != nil || value.LastTradeState != nil ||
			value.LastErrorClass != nil || value.LastProviderRequestID != nil ||
			value.CompletedAt != nil {
			return ErrInvalidPaymentQuery
		}
	case PaymentQueryStatusPending:
		if value.OwnerToken != nil || value.LeaseExpiresAt != nil ||
			value.NextQueryAt == nil || value.NextQueryAt.Before(value.UpdatedAt) ||
			value.LastTradeState == nil ||
			*value.LastTradeState == ProviderTradeStateSuccess ||
			!validProviderTradeState(*value.LastTradeState) ||
			value.LastErrorClass != nil || value.CompletedAt != nil {
			return ErrInvalidPaymentQuery
		}
	case PaymentQueryStatusUnknown:
		if value.OwnerToken != nil || value.LeaseExpiresAt != nil ||
			value.NextQueryAt == nil || value.NextQueryAt.Before(value.UpdatedAt) ||
			value.LastTradeState != nil || value.LastErrorClass == nil ||
			!providerFailureClassPattern.MatchString(*value.LastErrorClass) ||
			value.LastProviderRequestID != nil || value.CompletedAt != nil {
			return ErrInvalidPaymentQuery
		}
	case PaymentQueryStatusConverged:
		if value.OwnerToken != nil || value.LeaseExpiresAt != nil ||
			value.NextQueryAt != nil || value.LastTradeState == nil ||
			*value.LastTradeState != ProviderTradeStateSuccess ||
			value.LastErrorClass != nil || value.CompletedAt == nil ||
			value.CompletedAt.Before(value.CreatedAt) {
			return ErrInvalidPaymentQuery
		}
	case PaymentQueryStatusClosed:
		if value.OwnerToken != nil || value.LeaseExpiresAt != nil ||
			value.NextQueryAt != nil || value.LastTradeState == nil ||
			!IsProviderTerminalUnpaidState(*value.LastTradeState) ||
			value.LastErrorClass != nil || value.CompletedAt == nil ||
			value.CompletedAt.Before(value.CreatedAt) {
			return ErrInvalidPaymentQuery
		}
	default:
		return ErrInvalidPaymentQuery
	}
	if value.LastProviderRequestID != nil &&
		invalidBoundedText(*value.LastProviderRequestID, 128) {
		return ErrInvalidPaymentQuery
	}
	return nil
}

func validateOwnedPaymentQuery(
	current PaymentQueryLease,
	ownerToken uuid.UUID,
	at time.Time,
) error {
	if err := validatePaymentQueryLease(current); err != nil ||
		ownerToken == uuid.Nil || at.IsZero() {
		return ErrInvalidPaymentQuery
	}
	if current.QueryStatus != PaymentQueryStatusInProgress ||
		current.OwnerToken == nil || *current.OwnerToken != ownerToken {
		return ErrPaymentQueryLeaseLost
	}
	return nil
}

func validProviderTradeState(value ProviderTradeState) bool {
	switch value {
	case ProviderTradeStateSuccess, ProviderTradeStateRefund,
		ProviderTradeStateNotPay, ProviderTradeStateClosed,
		ProviderTradeStateRevoked, ProviderTradeStateUserPaying,
		ProviderTradeStatePayError:
		return true
	default:
		return false
	}
}

func IsProviderTerminalUnpaidState(value ProviderTradeState) bool {
	switch value {
	case ProviderTradeStateClosed, ProviderTradeStateRevoked,
		ProviderTradeStatePayError:
		return true
	default:
		return false
	}
}

func transactionObservationDigest(observation TransactionObservation) string {
	successAt := ""
	if observation.SuccessAt != nil {
		successAt = observation.SuccessAt.UTC().Format(time.RFC3339Nano)
	}
	canonical := strings.Join([]string{
		"xiangwan-wechat-transaction-observation-v1",
		observation.ObservationSource,
		observation.SourceKey,
		observation.PaymentAppID,
		observation.PaymentMerchantID,
		observation.OutTradeNo,
		observation.TransactionID,
		observation.TradeType,
		string(observation.TradeState),
		strconv.FormatInt(observation.AmountCents, 10),
		observation.Currency,
		successAt,
	}, "\n")
	digest := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(digest[:])
}

func clonePaymentQueryLease(value PaymentQueryLease) PaymentQueryLease {
	cloned := value
	if value.OwnerToken != nil {
		owner := *value.OwnerToken
		cloned.OwnerToken = &owner
	}
	cloned.LeaseExpiresAt = cloneTimePointer(value.LeaseExpiresAt)
	cloned.NextQueryAt = cloneTimePointer(value.NextQueryAt)
	if value.LastTradeState != nil {
		tradeState := *value.LastTradeState
		cloned.LastTradeState = &tradeState
	}
	if value.LastErrorClass != nil {
		errorClass := *value.LastErrorClass
		cloned.LastErrorClass = &errorClass
	}
	if value.LastProviderRequestID != nil {
		requestID := *value.LastProviderRequestID
		cloned.LastProviderRequestID = &requestID
	}
	cloned.CompletedAt = cloneTimePointer(value.CompletedAt)
	return cloned
}

func cloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
