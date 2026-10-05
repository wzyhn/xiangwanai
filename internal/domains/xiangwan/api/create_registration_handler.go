package xiangwanapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	paymentpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment/postgres"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
	registrationpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration/postgres"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/wzyhn/xiangwanai/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	maxCreateRegistrationBodyBytes         = 64 * 1024
	registrationPrivacyPolicyVersionHeader = "X-Xiangwan-Privacy-Policy-Version"
)

type createRegistrationApplication interface {
	Create(
		context.Context,
		uuid.UUID,
		uuid.UUID,
		CreateRegistrationRequest,
	) (CreateRegistrationResult, error)
}

type CreateRegistrationHandler struct {
	service   createRegistrationApplication
	principal PrincipalResolver
}

func NewCreateRegistrationHandler(
	service createRegistrationApplication,
	principal PrincipalResolver,
) *CreateRegistrationHandler {
	return &CreateRegistrationHandler{
		service:   service,
		principal: principal,
	}
}

func (handler *CreateRegistrationHandler) RegisterRoutes(
	group *gin.RouterGroup,
) {
	group.POST(
		"/sessions/:session_id/registrations",
		handler.CreateRegistration,
	)
}

// CreateRegistration godoc
// @Summary Start one Xiangwan Session registration
// @Description OP-KEY command. Atomically rechecks the exact Session publication, displayed price, optional owner Coupon selection, acknowledged public privacy-policy version when supplied, current questionnaire, answers, capacity, contact-policy version, and duplicate participation. During the rolling compatibility window the privacy version may arrive in the request header, the legacy JSON field, or be omitted by an N-1 client. A free Session returns a confirmed Registration. An enabled paid Session returns a pending Registration, immutable Order price snapshot (including any validated Coupon discount), and ten-minute PostgreSQL capacity Hold; provider payment starts through a separate recoverable command.
// @Tags xiangwan
// @Accept json
// @Produce json
// @Param session_id path string true "Session ID"
// @Param Idempotency-Key header string true "Canonical lowercase UUIDv4 operation key"
// @Param X-Xiangwan-Privacy-Policy-Version header string false "Acknowledged public privacy-policy version"
// @Param request body CreateRegistrationHTTPRequest true "Registration submission"
// @Security BearerAuth
// @Success 200 {object} CreateRegistrationResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 404 {object} response.Body
// @Failure 409 {object} response.Body
// @Failure 413 {object} response.Body
// @Failure 500 {object} response.Body
// @Router /xiangwan/sessions/{session_id}/registrations [post]
func (handler *CreateRegistrationHandler) CreateRegistration(c *gin.Context) {
	if handler == nil || handler.service == nil || handler.principal == nil {
		writeError(c, errx.NewInternal("xiangwan Registration is unavailable"))
		return
	}
	if len(c.Request.URL.Query()) != 0 || c.ContentType() != "application/json" {
		writeError(c, errx.NewBadRequest("invalid Registration request"))
		return
	}
	sessionID, err := parseCanonicalUUID(c.Param("session_id"))
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid Session id"))
		return
	}
	idempotencyKey, err := parseOperationKey(c)
	if err != nil {
		writeError(c, err)
		return
	}
	request, err := decodeCreateRegistrationRequest(c)
	if err != nil {
		writeError(c, err)
		return
	}
	request.IdempotencyKey = idempotencyKey
	principalID, err := handler.principal(c)
	if err != nil {
		writeError(c, err)
		return
	}
	created, err := handler.service.Create(
		c.Request.Context(),
		principalID,
		sessionID,
		request,
	)
	if err != nil {
		writeCreateRegistrationError(c, err)
		return
	}
	response.OK(c, projectCreateRegistrationResponse(created))
}

type CreateRegistrationHTTPRequest struct {
	InstancePublicationVersion *int64                                `json:"instance_publication_version" binding:"required"`
	PriceCents                 *int64                                `json:"price_cents" binding:"required"`
	CouponID                   *string                               `json:"coupon_id,omitempty"`
	PrivacyPolicyVersion       *string                               `json:"privacy_policy_version"`
	Contact                    CreateRegistrationContactHTTPRequest  `json:"contact"`
	QuestionnaireVersionID     *string                               `json:"questionnaire_version_id"`
	Answers                    []CreateRegistrationAnswerHTTPRequest `json:"answers"`
}

type CreateRegistrationContactHTTPRequest struct {
	Name          string `json:"name"`
	PhoneE164     string `json:"phone_e164"`
	PolicyVersion string `json:"policy_version"`
}

type CreateRegistrationAnswerHTTPRequest struct {
	FieldID string    `json:"field_id"`
	Values  *[]string `json:"values"`
}

func decodeCreateRegistrationRequest(
	c *gin.Context,
) (CreateRegistrationRequest, error) {
	c.Request.Body = http.MaxBytesReader(
		c.Writer,
		c.Request.Body,
		maxCreateRegistrationBodyBytes,
	)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	var payload CreateRegistrationHTTPRequest
	if err := decoder.Decode(&payload); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			return CreateRegistrationRequest{}, errx.New(
				errx.CodeFileTooLarge,
				"Registration request is too large",
			)
		}
		return CreateRegistrationRequest{},
			errx.NewBadRequest("invalid Registration request body")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return CreateRegistrationRequest{},
			errx.NewBadRequest("invalid Registration request body")
	}
	if len(payload.Answers) > activity.MaxQuestionnaireFields {
		return CreateRegistrationRequest{},
			errx.NewBadRequest("invalid Registration request body")
	}
	if payload.InstancePublicationVersion == nil ||
		*payload.InstancePublicationVersion < 1 || payload.PriceCents == nil ||
		*payload.PriceCents < 0 {
		return CreateRegistrationRequest{},
			errx.NewBadRequest("invalid Registration confirmation facts")
	}
	var couponID *uuid.UUID
	if payload.CouponID != nil {
		parsed, err := parseCanonicalUUID(*payload.CouponID)
		if err != nil {
			return CreateRegistrationRequest{}, errx.NewBadRequest("invalid coupon id")
		}
		couponID = &parsed
	}
	privacyPolicyVersion, err := parseRegistrationPrivacyPolicyVersion(
		c,
		payload.PrivacyPolicyVersion,
	)
	if err != nil {
		return CreateRegistrationRequest{}, err
	}
	request := CreateRegistrationRequest{
		InstancePublicationVersion: *payload.InstancePublicationVersion,
		PriceCents:                 *payload.PriceCents,
		CouponID:                   couponID,
		PrivacyPolicyVersion:       privacyPolicyVersion,
		ContactName:                payload.Contact.Name,
		ContactPhoneE164:           payload.Contact.PhoneE164,
		ContactPolicyVersion:       payload.Contact.PolicyVersion,
		Answers: make(
			[]activity.QuestionnaireAnswer,
			0,
			len(payload.Answers),
		),
	}
	if payload.QuestionnaireVersionID != nil {
		parsed, err := parseCanonicalUUID(*payload.QuestionnaireVersionID)
		if err != nil {
			return CreateRegistrationRequest{},
				errx.NewBadRequest("invalid questionnaire version id")
		}
		request.QuestionnaireVersionID = &parsed
	}
	for _, answer := range payload.Answers {
		fieldID, err := parseCanonicalUUID(answer.FieldID)
		if err != nil || answer.Values == nil {
			return CreateRegistrationRequest{},
				errx.NewBadRequest("invalid questionnaire answer")
		}
		request.Answers = append(request.Answers, activity.QuestionnaireAnswer{
			FieldID: fieldID,
			Values:  append([]string(nil), (*answer.Values)...),
		})
	}
	return request, nil
}

func parseRegistrationPrivacyPolicyVersion(
	c *gin.Context,
	bodyVersion *string,
) (string, error) {
	values := c.Request.Header.Values(registrationPrivacyPolicyVersionHeader)
	if len(values) > 1 {
		return "", errx.NewBadRequest(
			"invalid Registration privacy policy version",
		)
	}
	headerVersion := ""
	if len(values) == 1 {
		headerVersion = values[0]
		if !validPublicPolicyVersion(headerVersion) {
			return "", errx.NewBadRequest(
				"invalid Registration privacy policy version",
			)
		}
	}
	if bodyVersion == nil {
		return headerVersion, nil
	}
	if !validPublicPolicyVersion(*bodyVersion) ||
		(headerVersion != "" && headerVersion != *bodyVersion) {
		return "", errx.NewBadRequest(
			"invalid Registration privacy policy version",
		)
	}
	return *bodyVersion, nil
}

func parseOperationKey(c *gin.Context) (uuid.UUID, error) {
	values := c.Request.Header.Values("Idempotency-Key")
	if len(values) != 1 {
		return uuid.Nil, errx.NewBadRequest(
			"Idempotency-Key header is required exactly once",
		)
	}
	raw := strings.TrimSpace(values[0])
	parsed, err := uuid.Parse(raw)
	if err != nil || raw != values[0] || parsed == uuid.Nil ||
		parsed.String() != raw ||
		parsed.Version() != 4 || parsed.Variant() != uuid.RFC4122 {
		return uuid.Nil, errx.NewBadRequest(
			"Idempotency-Key must be a canonical lowercase UUIDv4",
		)
	}
	return parsed, nil
}

func writeCreateRegistrationError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidCreateRegistrationRequest),
		errors.Is(err, registration.ErrInvalidRegistrationSubmission),
		errors.Is(err, registrationpostgres.ErrInvalidFreeRegistrationCommand),
		errors.Is(err, registrationpostgres.ErrRegistrationAnswersInvalid),
		errors.Is(err, paymentpostgres.ErrInvalidPaidRegistrationCommand),
		errors.Is(err, paymentpostgres.ErrPaidRegistrationAnswersInvalid):
		writeError(c, errx.NewBadRequest("invalid Registration submission"))
	case errors.Is(err, activity.ErrSessionDetailUnavailable):
		writeError(c, errx.NewNotFound("Session not found"))
	case errors.Is(err, ErrRegistrationPrivacyPolicyUnavailable),
		errors.Is(err, registrationpostgres.ErrRegistrationPrivacyPolicyConflict),
		errors.Is(err, paymentpostgres.ErrPaidRegistrationPrivacyPolicyConflict):
		writeCreateRegistrationConflict(
			c,
			"Registration privacy policy changed",
			CreateRegistrationConflictFactsChanged,
		)
	case errors.Is(err, ErrManualRegistrationContactUnavailable):
		writeCreateRegistrationConflict(
			c,
			"Registration contact policy changed",
			CreateRegistrationConflictFactsChanged,
		)
	case errors.Is(err, registrationpostgres.ErrRegistrationContactPolicyConflict),
		errors.Is(err, paymentpostgres.ErrPaidRegistrationContactPolicyConflict):
		writeCreateRegistrationConflict(
			c,
			"Registration contact policy changed",
			CreateRegistrationConflictFactsChanged,
		)
	case errors.Is(err, ErrPaidRegistrationUnavailable):
		writeCreateRegistrationConflict(
			c,
			"paid Registration is unavailable",
			CreateRegistrationConflictFactsChanged,
		)
	case errors.Is(err, registrationpostgres.ErrRegistrationUnavailable):
		writeCreateRegistrationConflict(
			c,
			"Session is not open for registration",
			CreateRegistrationConflictFactsChanged,
		)
	case errors.Is(err, registrationpostgres.ErrRegistrationPaymentRequired):
		writeCreateRegistrationConflict(
			c,
			"Session requires the paid registration flow",
			CreateRegistrationConflictFactsChanged,
		)
	case errors.Is(err, registrationpostgres.ErrRegistrationQuestionnaireConflict):
		writeCreateRegistrationConflict(
			c,
			"questionnaire changed; review and resubmit",
			CreateRegistrationConflictFactsChanged,
		)
	case errors.Is(err, paymentpostgres.ErrPaidRegistrationQuestionnaireConflict):
		writeCreateRegistrationConflict(
			c,
			"questionnaire changed; review and resubmit",
			CreateRegistrationConflictFactsChanged,
		)
	case errors.Is(err, registrationpostgres.ErrRegistrationAlreadyOpen):
		writeCreateRegistrationConflict(
			c,
			"an active Registration already exists",
			CreateRegistrationConflictAlreadyOpen,
		)
	case errors.Is(err, registrationpostgres.ErrRegistrationIdempotencyConflict):
		writeCreateRegistrationConflict(
			c,
			"Idempotency-Key conflicts with another submission",
			CreateRegistrationConflictIdempotencyKey,
		)
	case errors.Is(err, paymentpostgres.ErrPaidRegistrationIdempotencyConflict):
		writeCreateRegistrationConflict(
			c,
			"Idempotency-Key conflicts with another submission",
			CreateRegistrationConflictIdempotencyKey,
		)
	case errors.Is(err, paymentpostgres.ErrPaidRegistrationAlreadyOpen):
		writeCreateRegistrationConflict(
			c,
			"an active Registration already exists",
			CreateRegistrationConflictAlreadyOpen,
		)
	case errors.Is(err, paymentpostgres.ErrPaidRegistrationUnavailable):
		writeCreateRegistrationConflict(
			c,
			"Session is not open for registration",
			CreateRegistrationConflictFactsChanged,
		)
	case errors.Is(err, paymentpostgres.ErrPaidRegistrationFreeSession):
		writeCreateRegistrationConflict(
			c,
			"Registration price changed; review and retry",
			CreateRegistrationConflictFactsChanged,
		)
	case errors.Is(err, registrationpostgres.ErrRegistrationCapacityConflict),
		errors.Is(err, registrationpostgres.ErrRegistrationTransactionConflict),
		errors.Is(err, paymentpostgres.ErrPaidRegistrationCapacityConflict),
		errors.Is(err, paymentpostgres.ErrPaidRegistrationTransactionConflict):
		writeCreateRegistrationConflict(
			c,
			"Registration facts changed; review and retry",
			CreateRegistrationConflictFactsChanged,
		)
	case errors.Is(err, registrationpostgres.ErrRegistrationGenerationInactive),
		errors.Is(err, paymentpostgres.ErrPaidRegistrationGenerationInactive):
		c.PureJSON(http.StatusServiceUnavailable, response.Body{
			Code:    int(errx.CodeInternal),
			Message: "service unavailable",
		})
	case errors.Is(err, paymentpostgres.ErrPaymentMerchantConfigUnavailable):
		c.PureJSON(http.StatusServiceUnavailable, response.Body{
			Code:    int(errx.CodeInternal),
			Message: "service unavailable",
		})
	default:
		_ = c.Error(err)
		writeError(c, errx.NewInternal("xiangwan Registration failed"))
	}
}

type CreateRegistrationConflictReason string

const (
	CreateRegistrationConflictFactsChanged   CreateRegistrationConflictReason = "registration_facts_changed"
	CreateRegistrationConflictAlreadyOpen    CreateRegistrationConflictReason = "registration_already_open"
	CreateRegistrationConflictIdempotencyKey CreateRegistrationConflictReason = "idempotency_key_conflict"
)

type CreateRegistrationConflictResponse struct {
	Reason CreateRegistrationConflictReason `json:"reason"`
}

func writeCreateRegistrationConflict(
	c *gin.Context,
	message string,
	reason CreateRegistrationConflictReason,
) {
	writeErrorWithData(
		c,
		errx.NewConflict(message),
		CreateRegistrationConflictResponse{Reason: reason},
	)
}

type CreateRegistrationResponse struct {
	RegistrationID      string                           `json:"registration_id"`
	SeriesID            string                           `json:"series_id"`
	InstanceID          string                           `json:"instance_id"`
	SessionID           string                           `json:"session_id"`
	ParticipationStatus registration.ParticipationStatus `json:"participation_status"`
	Version             int64                            `json:"version"`
	ConfirmedAt         *string                          `json:"confirmed_at,omitempty"`
	CancelledAt         *string                          `json:"cancelled_at,omitempty"`
	NextAction          string                           `json:"next_action"`
	Order               *CreateRegistrationOrderResponse `json:"order,omitempty"`
}

type CreateRegistrationOrderResponse struct {
	OrderID            string                     `json:"order_id"`
	PaymentStatus      payment.OrderStatus        `json:"payment_status"`
	OriginalPriceCents int64                      `json:"original_price_cents"`
	DiscountCents      int64                      `json:"discount_cents"`
	PayableCents       int64                      `json:"payable_cents"`
	Currency           string                     `json:"currency"`
	HoldStatus         payment.CapacityHoldStatus `json:"hold_status"`
	HoldExpiresAt      string                     `json:"hold_expires_at"`
}

func projectCreateRegistrationResponse(
	created CreateRegistrationResult,
) CreateRegistrationResponse {
	registrationValue := created.Registration
	result := CreateRegistrationResponse{
		RegistrationID:      registrationValue.ID.String(),
		SeriesID:            registrationValue.SeriesID.String(),
		InstanceID:          registrationValue.InstanceID.String(),
		SessionID:           registrationValue.SessionID.String(),
		ParticipationStatus: registrationValue.ParticipationStatus,
		Version:             registrationValue.Version,
		NextAction:          "registration_confirmed",
	}
	if registrationValue.ConfirmedAt != nil {
		value := registrationValue.ConfirmedAt.UTC().Format(time.RFC3339Nano)
		result.ConfirmedAt = &value
	}
	if registrationValue.CancelledAt != nil {
		value := registrationValue.CancelledAt.UTC().Format(time.RFC3339Nano)
		result.CancelledAt = &value
		result.NextAction = "registration_cancelled"
	}
	if created.Payment != nil {
		order := created.Payment.Order
		hold := created.Payment.Hold
		result.Order = &CreateRegistrationOrderResponse{
			OrderID:            order.ID.String(),
			PaymentStatus:      order.PaymentStatus,
			OriginalPriceCents: order.OriginalPriceCents,
			DiscountCents:      order.DiscountCents,
			PayableCents:       order.PayableCents,
			Currency:           "CNY",
			HoldStatus:         hold.HoldStatus,
			HoldExpiresAt:      hold.ExpiresAt.UTC().Format(time.RFC3339Nano),
		}
		switch {
		case registrationValue.ParticipationStatus ==
			registration.ParticipationStatusCancelled ||
			order.PaymentStatus == payment.OrderStatusClosedUnpaid:
			result.NextAction = "registration_cancelled"
		case order.PaymentStatus == payment.OrderStatusUnknown:
			result.NextAction = "payment_confirmation_pending"
		case registrationValue.ParticipationStatus ==
			registration.ParticipationStatusPendingPayment:
			result.NextAction = "wechat_payment_required"
		default:
			result.NextAction = "registration_confirmed"
		}
	}
	return result
}
