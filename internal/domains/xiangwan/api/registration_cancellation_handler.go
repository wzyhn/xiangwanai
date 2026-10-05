package xiangwanapi

import (
	"context"
	"errors"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/refund"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
	registrationpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration/postgres"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/wzyhn/xiangwanai/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type registrationCancellationApplication interface {
	Cancel(
		context.Context,
		uuid.UUID,
		uuid.UUID,
	) (registrationpostgres.RegistrationCancellationResult, error)
}

type RegistrationCancellationHandler struct {
	service   registrationCancellationApplication
	principal PrincipalResolver
}

func NewRegistrationCancellationHandler(
	service registrationCancellationApplication,
	principal PrincipalResolver,
) *RegistrationCancellationHandler {
	return &RegistrationCancellationHandler{
		service:   service,
		principal: principal,
	}
}

func (handler *RegistrationCancellationHandler) RegisterRoutes(
	group *gin.RouterGroup,
) {
	group.POST(
		"/registrations/:registration_id/cancellations",
		handler.CancelRegistration,
	)
}

// CancelRegistration godoc
// @Summary Cancel one authenticated consumer Xiangwan Registration
// @Description BUSINESS-STATE command. The client supplies no cancellation, payment, refund, capacity, or idempotency fact. PostgreSQL rechecks the Session-specific cancellation cutoff under aggregate locks, atomically revokes participation and capacity, closes a known-unpaid Order, and creates the unique manual Refund case when a versioned full-refund policy permits paid self-cancellation. Exact retries return the stored cancellation summary.
// @Tags xiangwan
// @Produce json
// @Param registration_id path string true "Registration ID"
// @Security BearerAuth
// @Success 200 {object} RegistrationCancellationResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 404 {object} response.Body
// @Failure 409 {object} response.Body
// @Failure 500 {object} response.Body
// @Router /xiangwan/registrations/{registration_id}/cancellations [post]
func (handler *RegistrationCancellationHandler) CancelRegistration(
	c *gin.Context,
) {
	if handler == nil || handler.service == nil || handler.principal == nil {
		writeError(c, errx.NewInternal("xiangwan Registration cancellation is unavailable"))
		return
	}
	if len(c.Request.URL.Query()) != 0 || !emptyJSONCompatibleRequest(c) ||
		len(c.Request.Header.Values("Idempotency-Key")) != 0 {
		writeError(c, errx.NewBadRequest("invalid Registration cancellation request"))
		return
	}
	registrationID, err := parseCanonicalUUID(c.Param("registration_id"))
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid Registration id"))
		return
	}
	principalID, err := handler.principal(c)
	if err != nil {
		writeError(c, err)
		return
	}
	result, err := handler.service.Cancel(
		c.Request.Context(),
		principalID,
		registrationID,
	)
	if err != nil {
		writeRegistrationCancellationError(c, err)
		return
	}
	response.OK(c, projectRegistrationCancellationResponse(result))
}

func writeRegistrationCancellationError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidRegistrationCancellationRequest),
		errors.Is(err, registrationpostgres.ErrInvalidRegistrationCancellationCommand):
		writeError(c, errx.NewBadRequest("invalid Registration cancellation request"))
	case errors.Is(err, registrationpostgres.ErrRegistrationCancellationNotFound),
		errors.Is(err, registrationpostgres.ErrRegistrationCancellationForbidden):
		writeError(c, errx.NewNotFound("Registration not found"))
	case errors.Is(err, registrationpostgres.ErrRegistrationCancellationPolicyMissing),
		errors.Is(err, registrationpostgres.ErrRegistrationCouponPolicyMissing):
		writeError(c, errx.NewConflict("self-service cancellation policy is unavailable"))
	case errors.Is(err, registrationpostgres.ErrRegistrationCancellationNotAllowed):
		writeError(c, errx.NewConflict("Registration is not eligible for self-service cancellation"))
	case errors.Is(err, registrationpostgres.ErrRegistrationCancellationConflict),
		errors.Is(err, registrationpostgres.ErrRegistrationCancellationTransaction):
		writeError(c, errx.NewConflict("Registration cancellation facts changed; refresh and retry"))
	default:
		_ = c.Error(err)
		writeError(c, errx.NewInternal("xiangwan Registration cancellation failed"))
	}
}

type RegistrationCancellationResponse struct {
	RegistrationID      string                           `json:"registration_id"`
	SessionID           string                           `json:"session_id"`
	ParticipationStatus registration.ParticipationStatus `json:"participation_status"`
	RegistrationVersion int64                            `json:"registration_version"`
	CancelledAt         string                           `json:"cancelled_at"`
	PolicyVersion       string                           `json:"policy_version,omitempty"`
	Order               *RegistrationCancellationOrder   `json:"order,omitempty"`
	Refund              *RegistrationCancellationRefund  `json:"refund,omitempty"`
	CouponAdjustment    *CancellationCouponAdjustment    `json:"coupon_adjustment,omitempty"`
	NextAction          string                           `json:"next_action"`
}

type RegistrationCancellationOrder struct {
	OrderID       string              `json:"order_id"`
	PaymentStatus payment.OrderStatus `json:"payment_status"`
	Version       int64               `json:"version"`
}

type RegistrationCancellationRefund struct {
	Status                refund.Status `json:"status"`
	RequestedRefundCents  int64         `json:"requested_refund_cents"`
	SuccessfulRefundCents int64         `json:"successful_refund_cents"`
	UpdatedAt             string        `json:"updated_at"`
}

type CancellationCouponAdjustment struct {
	Disposition   coupon.RefundDisposition `json:"disposition"`
	PolicyVersion string                   `json:"policy_version"`
	OccurredAt    string                   `json:"occurred_at"`
}

func projectRegistrationCancellationResponse(
	result registrationpostgres.RegistrationCancellationResult,
) RegistrationCancellationResponse {
	value := result.Registration
	projected := RegistrationCancellationResponse{
		RegistrationID:      value.ID.String(),
		SessionID:           value.SessionID.String(),
		ParticipationStatus: value.ParticipationStatus,
		RegistrationVersion: value.Version,
		PolicyVersion:       result.PolicyVersion,
		NextAction:          "cancellation_completed",
	}
	if value.CancelledAt != nil {
		projected.CancelledAt = value.CancelledAt.UTC().Format(time.RFC3339Nano)
	}
	if result.Order != nil {
		projected.Order = &RegistrationCancellationOrder{
			OrderID:       result.Order.ID.String(),
			PaymentStatus: result.Order.PaymentStatus,
			Version:       result.Order.Version,
		}
		if result.Order.PaymentStatus == payment.OrderStatusUnknown {
			projected.NextAction = "payment_confirmation_pending"
		}
	}
	if result.Refund != nil {
		projected.Refund = &RegistrationCancellationRefund{
			Status:                result.Refund.RefundStatus,
			RequestedRefundCents:  result.Refund.RequestedRefundCents,
			SuccessfulRefundCents: result.Refund.SuccessfulRefundCents,
			UpdatedAt:             result.Refund.UpdatedAt.UTC().Format(time.RFC3339Nano),
		}
		switch result.Refund.RefundStatus {
		case refund.StatusRefunded:
			projected.NextAction = "refund_completed"
		case refund.StatusFailed, refund.StatusRejected:
			projected.NextAction = "contact_support"
		default:
			projected.NextAction = "refund_processing"
		}
	}
	if result.CouponAdjustment != nil {
		disposition := coupon.RefundDispositionForfeit
		if result.CouponAdjustment.EntryType == coupon.EntryTypeRestored {
			disposition = coupon.RefundDispositionRestore
		}
		policyVersion := ""
		if result.CouponAdjustment.RefundPolicyVersion != nil {
			policyVersion = *result.CouponAdjustment.RefundPolicyVersion
		}
		projected.CouponAdjustment = &CancellationCouponAdjustment{
			Disposition:   disposition,
			PolicyVersion: policyVersion,
			OccurredAt: result.CouponAdjustment.OccurredAt.UTC().Format(
				time.RFC3339Nano,
			),
		}
	}
	return projected
}
