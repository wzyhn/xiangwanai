package xiangwanapi

import (
	"context"
	"errors"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/booking"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/wzyhn/xiangwanai/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type myRegistrationDetailApplication interface {
	Read(
		context.Context,
		uuid.UUID,
		uuid.UUID,
	) (booking.MyRegistrationDetail, error)
}

type MyRegistrationDetailHandler struct {
	service   myRegistrationDetailApplication
	principal PrincipalResolver
}

func NewMyRegistrationDetailHandler(
	service myRegistrationDetailApplication,
	principal PrincipalResolver,
) *MyRegistrationDetailHandler {
	return &MyRegistrationDetailHandler{
		service:   service,
		principal: principal,
	}
}

func (handler *MyRegistrationDetailHandler) RegisterRoutes(
	group *gin.RouterGroup,
) {
	group.GET(
		"/me/registrations/:registration_id",
		handler.GetMyRegistrationDetail,
	)
}

// GetMyRegistrationDetail godoc
// @Summary Read one authenticated consumer Xiangwan registration
// @Description Returns one exact owner-fenced Registration and its Session, payment, Refund, check-in, cancellation, and access decisions.
// @Tags xiangwan
// @Produce json
// @Param registration_id path string true "Registration ID"
// @Security BearerAuth
// @Success 200 {object} MyRegistrationDetailResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 404 {object} response.Body
// @Failure 500 {object} response.Body
// @Router /xiangwan/me/registrations/{registration_id} [get]
func (handler *MyRegistrationDetailHandler) GetMyRegistrationDetail(
	c *gin.Context,
) {
	if handler == nil || handler.service == nil || handler.principal == nil {
		writeError(c, errx.NewInternal("xiangwan My Registration detail is unavailable"))
		return
	}
	if len(c.Request.URL.Query()) != 0 {
		writeError(c, errx.NewBadRequest("invalid My Registration detail query"))
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
	detail, err := handler.service.Read(
		c.Request.Context(),
		principalID,
		registrationID,
	)
	if err != nil {
		writeMyRegistrationDetailError(c, err)
		return
	}
	response.OK(c, projectMyRegistrationDetailResponse(detail))
}

func writeMyRegistrationDetailError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidMyRegistrationDetailRequest),
		errors.Is(err, booking.ErrInvalidMyRegistrationIdentity):
		writeError(c, errx.NewBadRequest("invalid My Registration detail request"))
	case errors.Is(err, booking.ErrMyRegistrationNotFound):
		writeError(c, errx.NewNotFound("Registration not found"))
	default:
		_ = c.Error(err)
		writeError(c, errx.NewInternal("xiangwan My Registration detail failed"))
	}
}

type MyRegistrationDetailResponse struct {
	Registration               MyRegistrationItemResponse               `json:"registration"`
	Contact                    MyRegistrationContactResponse            `json:"contact"`
	Cancellation               *MyRegistrationCancellationResponse      `json:"cancellation,omitempty"`
	CouponAdjustment           *CancellationCouponAdjustment            `json:"coupon_adjustment,omitempty"`
	AccessDenial               booking.MyRegistrationAccessDenial       `json:"access_denial,omitempty"`
	CheckinCredentialEligible  bool                                     `json:"checkin_credential_eligible"`
	PrivateAccessEligible      bool                                     `json:"private_access_eligible"`
	CancellationAction         booking.MyRegistrationCancellationAction `json:"cancellation_action"`
	CancellationPolicyRequired bool                                     `json:"cancellation_policy_required"`
	AsOf                       string                                   `json:"as_of"`
}

type MyRegistrationContactResponse struct {
	Name        string `json:"name"`
	PhoneMasked string `json:"phone_masked"`
}

type MyRegistrationCancellationResponse struct {
	Scope  booking.MyRegistrationCancellationScope `json:"scope"`
	Reason string                                  `json:"reason"`
	At     string                                  `json:"at"`
}

func projectMyRegistrationDetailResponse(
	detail booking.MyRegistrationDetail,
) MyRegistrationDetailResponse {
	result := MyRegistrationDetailResponse{
		Registration: projectMyRegistrationItem(detail.Item),
		Contact: MyRegistrationContactResponse{
			Name:        detail.Contact.Name,
			PhoneMasked: detail.Contact.PhoneMasked,
		},
		AccessDenial:               detail.AccessDenial,
		CheckinCredentialEligible:  detail.CheckinCredentialEligible,
		PrivateAccessEligible:      detail.PrivateAccessEligible,
		CancellationAction:         detail.CancellationAction,
		CancellationPolicyRequired: detail.CancellationPolicyRequired,
		AsOf:                       formatMyRegistrationTime(detail.AsOf),
	}
	if detail.Cancellation != nil {
		result.Cancellation = &MyRegistrationCancellationResponse{
			Scope:  detail.Cancellation.Scope,
			Reason: detail.Cancellation.Reason,
			At:     detail.Cancellation.At.UTC().Format(time.RFC3339Nano),
		}
	}
	if detail.CouponAdjustment != nil {
		result.CouponAdjustment = &CancellationCouponAdjustment{
			Disposition:   detail.CouponAdjustment.Disposition,
			PolicyVersion: detail.CouponAdjustment.PolicyVersion,
			OccurredAt: detail.CouponAdjustment.OccurredAt.UTC().Format(
				time.RFC3339Nano,
			),
		}
	}
	return result
}
