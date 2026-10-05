package payment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	PrepayOperationKind = "wechat_prepay"
	PrepayProvider      = "wechat"
	PrepayCurrency      = "CNY"
	PrepayLeaseDuration = 30 * time.Second
)

type PrepayAttemptStatus string

const (
	PrepayAttemptStatusInProgress PrepayAttemptStatus = "in_progress"
	PrepayAttemptStatusReady      PrepayAttemptStatus = "ready"
	PrepayAttemptStatusUnknown    PrepayAttemptStatus = "unknown"
)

const (
	ProviderFailureAmbiguous       = "provider_ambiguous"
	ProviderFailureRejected        = "provider_rejected"
	ProviderFailureTimeout         = "provider_timeout"
	ProviderFailureInvalidResponse = "invalid_provider_response"
)

type MiniProgramPaymentParameters struct {
	AppID     string
	TimeStamp string
	NonceStr  string
	Package   string
	SignType  string
	PaySign   string
}

type PaymentAttempt struct {
	ID                 uuid.UUID
	TenantID           uuid.UUID
	OrderID            uuid.UUID
	PrincipalID        uuid.UUID
	GenerationID       uuid.UUID
	OperationKind      string
	IdempotencyKey     uuid.UUID
	RequestFingerprint string
	Provider           string
	OutTradeNo         string
	PaymentAppID       string
	PaymentMerchantID  string
	Description        string
	NotifyURL          string
	OrderVersion       int64
	AmountCents        int64
	Currency           string
	AttemptStatus      PrepayAttemptStatus
	OwnerToken         *uuid.UUID
	LeaseExpiresAt     *time.Time
	PrepayID           *string
	ClientTimestamp    *string
	ClientNonce        *string
	ClientPackage      *string
	ClientSignType     *string
	ClientPaySign      *string
	ProviderRequestID  *string
	LastErrorClass     *string
	CompletedAt        *time.Time
	Version            int64
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type NewPaymentAttemptCommand struct {
	ID                 uuid.UUID
	TenantID           uuid.UUID
	OrderID            uuid.UUID
	PrincipalID        uuid.UUID
	GenerationID       uuid.UUID
	IdempotencyKey     uuid.UUID
	RequestFingerprint string
	OutTradeNo         string
	PaymentAppID       string
	PaymentMerchantID  string
	Description        string
	NotifyURL          string
	OrderVersion       int64
	AmountCents        int64
	OwnerToken         uuid.UUID
	Now                time.Time
}

type CreatePrepayAttemptCommand struct {
	TenantID             uuid.UUID
	GenerationID         uuid.UUID
	OrderID              uuid.UUID
	PrincipalID          uuid.UUID
	IdempotencyKey       uuid.UUID
	ExpectedOrderVersion int64
	ExpectedPayableCents int64
}

type ProviderPrepayRequest struct {
	AppID        string
	MerchantID   string
	Description  string
	OutTradeNo   string
	NotifyURL    string
	OpenID       string
	AmountCents  int64
	TimeExpireAt time.Time
}

type ProviderPrepayResult struct {
	PrepayID          string
	ProviderRequestID string
	Parameters        MiniProgramPaymentParameters
}

type PrepayProviderPort interface {
	CreateMiniProgramPrepay(
		context.Context,
		ProviderPrepayRequest,
	) (ProviderPrepayResult, error)
}

type PrepayAttemptResult struct {
	Attempt       PaymentAttempt
	HoldExpiresAt time.Time
	Parameters    *MiniProgramPaymentParameters
}

var (
	ErrInvalidPrepayAttempt     = errors.New("invalid xiangwan prepay attempt")
	ErrPrepayAttemptTerminal    = errors.New("xiangwan prepay attempt is terminal")
	ErrPrepayAttemptLeaseActive = errors.New("xiangwan prepay attempt lease is active")
	ErrPrepayAttemptLeaseLost   = errors.New("xiangwan prepay attempt lease was lost")
	ErrInvalidProviderPrepay    = errors.New("invalid xiangwan provider prepay request or response")
	prepayFingerprintPattern    = regexp.MustCompile(`^[0-9a-f]{64}$`)
	providerCodePattern         = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)
	providerFailureClassPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	outTradeNoPattern           = regexp.MustCompile(`^[0-9A-Za-z_|*\-]{6,32}$`)
	digitsPattern               = regexp.MustCompile(`^[0-9]{10,13}$`)
)

type ProviderFailure struct {
	class string
	code  string
	cause error
}

func NewProviderFailure(class string, code string, cause error) error {
	if !providerFailureClassPattern.MatchString(class) {
		class = ProviderFailureAmbiguous
	}
	if code != "" && !providerCodePattern.MatchString(code) {
		code = ""
	}
	return &ProviderFailure{class: class, code: code, cause: cause}
}

func (failure *ProviderFailure) Error() string {
	if failure == nil {
		return "xiangwan payment provider failure"
	}
	return "xiangwan payment provider failure: " + failure.class
}

func (failure *ProviderFailure) Unwrap() error {
	if failure == nil {
		return nil
	}
	return failure.cause
}

func ProviderFailureMetadata(err error) (string, string) {
	var failure *ProviderFailure
	if !errors.As(err, &failure) || failure == nil {
		return ProviderFailureAmbiguous, ""
	}
	return failure.class, failure.code
}

func PrepayRequestFingerprint(command CreatePrepayAttemptCommand) (string, error) {
	if err := ValidateCreatePrepayAttemptCommand(command); err != nil {
		return "", err
	}
	canonical := strings.Join([]string{
		"xiangwan-wechat-prepay-v1",
		command.TenantID.String(),
		command.OrderID.String(),
		command.PrincipalID.String(),
		strconv.FormatInt(command.ExpectedOrderVersion, 10),
		strconv.FormatInt(command.ExpectedPayableCents, 10),
	}, "\n")
	digest := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(digest[:]), nil
}

func ValidateCreatePrepayAttemptCommand(command CreatePrepayAttemptCommand) error {
	if command.TenantID == uuid.Nil || command.GenerationID == uuid.Nil ||
		command.OrderID == uuid.Nil || command.PrincipalID == uuid.Nil ||
		command.IdempotencyKey == uuid.Nil || command.IdempotencyKey.Version() != 4 ||
		command.IdempotencyKey.Variant() != uuid.RFC4122 ||
		command.ExpectedOrderVersion < 1 || command.ExpectedPayableCents <= 0 {
		return ErrInvalidPrepayAttempt
	}
	return nil
}

func NewPaymentAttempt(command NewPaymentAttemptCommand) (PaymentAttempt, error) {
	if command.ID == uuid.Nil || command.TenantID == uuid.Nil ||
		command.OrderID == uuid.Nil || command.PrincipalID == uuid.Nil ||
		command.GenerationID == uuid.Nil || command.IdempotencyKey == uuid.Nil ||
		command.IdempotencyKey.Version() != 4 ||
		command.IdempotencyKey.Variant() != uuid.RFC4122 ||
		!prepayFingerprintPattern.MatchString(command.RequestFingerprint) ||
		!outTradeNoPattern.MatchString(command.OutTradeNo) ||
		invalidBoundedText(command.PaymentAppID, 64) ||
		invalidBoundedText(command.PaymentMerchantID, 64) ||
		invalidBoundedText(command.Description, 127) ||
		invalidBoundedText(command.NotifyURL, 2048) ||
		command.OrderVersion < 1 || command.AmountCents <= 0 ||
		command.OwnerToken == uuid.Nil ||
		command.Now.IsZero() {
		return PaymentAttempt{}, ErrInvalidPrepayAttempt
	}
	now := command.Now.UTC()
	leaseExpiresAt := now.Add(PrepayLeaseDuration)
	ownerToken := command.OwnerToken
	return PaymentAttempt{
		ID:                 command.ID,
		TenantID:           command.TenantID,
		OrderID:            command.OrderID,
		PrincipalID:        command.PrincipalID,
		GenerationID:       command.GenerationID,
		OperationKind:      PrepayOperationKind,
		IdempotencyKey:     command.IdempotencyKey,
		RequestFingerprint: command.RequestFingerprint,
		Provider:           PrepayProvider,
		OutTradeNo:         command.OutTradeNo,
		PaymentAppID:       command.PaymentAppID,
		PaymentMerchantID:  command.PaymentMerchantID,
		Description:        command.Description,
		NotifyURL:          command.NotifyURL,
		OrderVersion:       command.OrderVersion,
		AmountCents:        command.AmountCents,
		Currency:           PrepayCurrency,
		AttemptStatus:      PrepayAttemptStatusInProgress,
		OwnerToken:         &ownerToken,
		LeaseExpiresAt:     &leaseExpiresAt,
		Version:            1,
		CreatedAt:          now,
		UpdatedAt:          now,
	}, nil
}

func TakeOverPaymentAttempt(
	current PaymentAttempt,
	generationID uuid.UUID,
	ownerToken uuid.UUID,
	at time.Time,
) (PaymentAttempt, error) {
	if err := validateMutableAttempt(current); err != nil {
		return PaymentAttempt{}, err
	}
	if generationID == uuid.Nil || ownerToken == uuid.Nil || at.IsZero() {
		return PaymentAttempt{}, ErrInvalidPrepayAttempt
	}
	now := at.UTC()
	if now.Before(*current.LeaseExpiresAt) {
		return PaymentAttempt{}, ErrPrepayAttemptLeaseActive
	}
	updated := clonePaymentAttempt(current)
	leaseExpiresAt := now.Add(PrepayLeaseDuration)
	updated.GenerationID = generationID
	updated.OwnerToken = &ownerToken
	updated.LeaseExpiresAt = &leaseExpiresAt
	updated.Version++
	updated.UpdatedAt = now
	return updated, nil
}

func CompletePaymentAttemptReady(
	current PaymentAttempt,
	ownerToken uuid.UUID,
	providerResult ProviderPrepayResult,
	at time.Time,
) (PaymentAttempt, error) {
	if err := validateOwnedAttempt(current, ownerToken, at); err != nil {
		return PaymentAttempt{}, err
	}
	if err := ValidateProviderPrepayResult(providerResult, current.PaymentAppID); err != nil {
		return PaymentAttempt{}, err
	}
	updated := clonePaymentAttempt(current)
	completedAt := at.UTC()
	prepayID := providerResult.PrepayID
	clientTimestamp := providerResult.Parameters.TimeStamp
	clientNonce := providerResult.Parameters.NonceStr
	clientPackage := providerResult.Parameters.Package
	clientSignType := providerResult.Parameters.SignType
	clientPaySign := providerResult.Parameters.PaySign
	updated.AttemptStatus = PrepayAttemptStatusReady
	updated.OwnerToken = nil
	updated.LeaseExpiresAt = nil
	updated.PrepayID = &prepayID
	updated.ClientTimestamp = &clientTimestamp
	updated.ClientNonce = &clientNonce
	updated.ClientPackage = &clientPackage
	updated.ClientSignType = &clientSignType
	updated.ClientPaySign = &clientPaySign
	updated.ProviderRequestID = optionalBoundedText(providerResult.ProviderRequestID, 128)
	updated.CompletedAt = &completedAt
	updated.Version++
	updated.UpdatedAt = completedAt
	return updated, nil
}

func CompletePaymentAttemptUnknown(
	current PaymentAttempt,
	ownerToken uuid.UUID,
	errorClass string,
	providerRequestID string,
	at time.Time,
) (PaymentAttempt, error) {
	if err := validateOwnedAttempt(current, ownerToken, at); err != nil {
		return PaymentAttempt{}, err
	}
	if !providerFailureClassPattern.MatchString(errorClass) {
		return PaymentAttempt{}, ErrInvalidPrepayAttempt
	}
	updated := clonePaymentAttempt(current)
	completedAt := at.UTC()
	updated.AttemptStatus = PrepayAttemptStatusUnknown
	updated.OwnerToken = nil
	updated.LeaseExpiresAt = nil
	updated.ProviderRequestID = optionalBoundedText(providerRequestID, 128)
	updated.LastErrorClass = &errorClass
	updated.CompletedAt = &completedAt
	updated.Version++
	updated.UpdatedAt = completedAt
	return updated, nil
}

func (attempt PaymentAttempt) PaymentParameters() (*MiniProgramPaymentParameters, bool) {
	if attempt.AttemptStatus != PrepayAttemptStatusReady ||
		attempt.ClientTimestamp == nil || attempt.ClientNonce == nil ||
		attempt.ClientPackage == nil || attempt.ClientSignType == nil ||
		attempt.ClientPaySign == nil {
		return nil, false
	}
	parameters := &MiniProgramPaymentParameters{
		AppID:     attempt.PaymentAppID,
		TimeStamp: *attempt.ClientTimestamp,
		NonceStr:  *attempt.ClientNonce,
		Package:   *attempt.ClientPackage,
		SignType:  *attempt.ClientSignType,
		PaySign:   *attempt.ClientPaySign,
	}
	return parameters, true
}

func ValidateProviderPrepayRequest(request ProviderPrepayRequest) error {
	if invalidBoundedText(request.AppID, 64) ||
		invalidBoundedText(request.MerchantID, 64) ||
		invalidBoundedText(request.Description, 127) ||
		!outTradeNoPattern.MatchString(request.OutTradeNo) ||
		invalidBoundedText(request.NotifyURL, 2048) ||
		invalidBoundedText(request.OpenID, 128) || request.AmountCents <= 0 ||
		request.TimeExpireAt.IsZero() {
		return ErrInvalidProviderPrepay
	}
	return nil
}

func ValidateProviderPrepayResult(
	result ProviderPrepayResult,
	expectedAppID string,
) error {
	parameters := result.Parameters
	if invalidBoundedText(result.PrepayID, 64) ||
		(result.ProviderRequestID != "" && invalidBoundedText(result.ProviderRequestID, 128)) ||
		parameters.AppID != expectedAppID ||
		!digitsPattern.MatchString(parameters.TimeStamp) ||
		invalidBoundedText(parameters.NonceStr, 64) ||
		parameters.Package != "prepay_id="+result.PrepayID ||
		parameters.SignType != "RSA" ||
		invalidBoundedText(parameters.PaySign, 1024) {
		return ErrInvalidProviderPrepay
	}
	return nil
}

func validateMutableAttempt(current PaymentAttempt) error {
	if current.AttemptStatus != PrepayAttemptStatusInProgress {
		return ErrPrepayAttemptTerminal
	}
	if current.OwnerToken == nil || *current.OwnerToken == uuid.Nil ||
		current.LeaseExpiresAt == nil || current.LeaseExpiresAt.IsZero() ||
		current.CreatedAt.IsZero() || current.UpdatedAt.IsZero() ||
		current.Version < 1 {
		return ErrInvalidPrepayAttempt
	}
	return nil
}

func validateOwnedAttempt(
	current PaymentAttempt,
	ownerToken uuid.UUID,
	at time.Time,
) error {
	if err := validateMutableAttempt(current); err != nil {
		return err
	}
	if ownerToken == uuid.Nil || *current.OwnerToken != ownerToken {
		return ErrPrepayAttemptLeaseLost
	}
	if at.IsZero() || at.Before(current.UpdatedAt) {
		return ErrInvalidPrepayAttempt
	}
	return nil
}

func optionalBoundedText(value string, maxRunes int) *string {
	if value == "" {
		return nil
	}
	if invalidBoundedText(value, maxRunes) {
		return nil
	}
	copy := value
	return &copy
}

func clonePaymentAttempt(value PaymentAttempt) PaymentAttempt {
	cloned := value
	cloned.OwnerToken = cloneUUID(value.OwnerToken)
	cloned.LeaseExpiresAt = cloneTime(value.LeaseExpiresAt)
	cloned.PrepayID = cloneString(value.PrepayID)
	cloned.ClientTimestamp = cloneString(value.ClientTimestamp)
	cloned.ClientNonce = cloneString(value.ClientNonce)
	cloned.ClientPackage = cloneString(value.ClientPackage)
	cloned.ClientSignType = cloneString(value.ClientSignType)
	cloned.ClientPaySign = cloneString(value.ClientPaySign)
	cloned.ProviderRequestID = cloneString(value.ProviderRequestID)
	cloned.LastErrorClass = cloneString(value.LastErrorClass)
	cloned.CompletedAt = cloneTime(value.CompletedAt)
	return cloned
}

func cloneUUID(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func PrepayIDDigest(prepayID string) (string, error) {
	if invalidBoundedText(prepayID, 64) {
		return "", fmt.Errorf("%w: invalid prepay id", ErrInvalidProviderPrepay)
	}
	digest := sha256.Sum256([]byte(prepayID))
	return hex.EncodeToString(digest[:]), nil
}
