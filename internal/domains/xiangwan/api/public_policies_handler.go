package xiangwanapi

import (
	"context"

	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/wzyhn/xiangwanai/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

type publicPoliciesApplication interface {
	Read(context.Context) (PublicPolicies, error)
}

type PublicPoliciesHandler struct {
	service publicPoliciesApplication
}

func NewPublicPoliciesHandler(
	service publicPoliciesApplication,
) *PublicPoliciesHandler {
	return &PublicPoliciesHandler{service: service}
}

func (handler *PublicPoliciesHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.GET("/public-policies", handler.GetPublicPolicies)
}

// GetPublicPolicies godoc
// @Summary List published Xiangwan policy versions and public capability switches
// @Description Returns configured publication identifiers, fail-closed capability switches, published manual-registration contact text, and cancellation/refund rules matching the active cancellation policy. Privacy text is opened through the native WeChat privacy contract. It never returns contact values, customer-service details, external-domain allowlists, or private runtime configuration.
// @Tags xiangwan
// @Produce json
// @Success 200 {object} PublicPoliciesResponse
// @Failure 400 {object} response.Body
// @Failure 500 {object} response.Body
// @Router /xiangwan/public-policies [get]
func (handler *PublicPoliciesHandler) GetPublicPolicies(c *gin.Context) {
	if handler == nil || handler.service == nil {
		writeError(c, errx.NewInternal("xiangwan Public Policies is unavailable"))
		return
	}
	if len(c.Request.URL.Query()) != 0 {
		writeError(c, errx.NewBadRequest("invalid Public Policies query"))
		return
	}
	value, err := handler.service.Read(c.Request.Context())
	if err != nil {
		_ = c.Error(err)
		writeError(c, errx.NewInternal("xiangwan Public Policies failed"))
		return
	}
	response.OK(c, projectPublicPoliciesResponse(value))
}

type PublicPoliciesResponse struct {
	PublishedVersions               []PublicPolicyVersionResponse    `json:"published_versions"`
	ManualRegistrationContactPolicy *PublicPolicyTextResponse        `json:"manual_registration_contact_policy,omitempty"`
	CancellationPolicy              *PublicPolicyTextResponse        `json:"cancellation_policy,omitempty"`
	Capabilities                    PublicPolicyCapabilitiesResponse `json:"capabilities"`
}

type PublicPolicyVersionResponse struct {
	Kind    PublicPolicyKind `json:"kind"`
	Version string           `json:"version"`
}

type PublicPolicyTextResponse struct {
	Version string `json:"version"`
	Content string `json:"content"`
}

type PublicPolicyCapabilitiesResponse struct {
	PrivacyNoticeAvailable               bool `json:"privacy_notice_available"`
	UserAgreementAvailable               bool `json:"user_agreement_available"`
	PaidSelfServiceCancellationAvailable bool `json:"paid_self_service_cancellation_available"`
	CustomerServiceAvailable             bool `json:"customer_service_available"`
	ExternalLinksAvailable               bool `json:"external_links_available"`
	ManualRegistrationContactAvailable   bool `json:"manual_registration_contact_available"`
	WeChatPaymentAvailable               bool `json:"wechat_payment_available"`
}

func projectPublicPoliciesResponse(value PublicPolicies) PublicPoliciesResponse {
	result := PublicPoliciesResponse{
		PublishedVersions: make(
			[]PublicPolicyVersionResponse,
			0,
			len(value.PublishedVersions),
		),
		Capabilities: PublicPolicyCapabilitiesResponse{
			PrivacyNoticeAvailable: value.Capabilities.PrivacyNoticeAvailable,
			UserAgreementAvailable: value.Capabilities.UserAgreementAvailable,
			PaidSelfServiceCancellationAvailable: value.Capabilities.
				PaidSelfServiceCancellationAvailable,
			CustomerServiceAvailable: value.Capabilities.CustomerServiceAvailable,
			ExternalLinksAvailable:   value.Capabilities.ExternalLinksAvailable,
			ManualRegistrationContactAvailable: value.Capabilities.
				ManualRegistrationContactAvailable,
			WeChatPaymentAvailable: value.Capabilities.WeChatPaymentAvailable,
		},
	}
	for _, policy := range value.PublishedVersions {
		result.PublishedVersions = append(
			result.PublishedVersions,
			PublicPolicyVersionResponse{Kind: policy.Kind, Version: policy.Version},
		)
	}
	if value.ManualRegistrationContactPolicy != nil {
		result.ManualRegistrationContactPolicy = &PublicPolicyTextResponse{
			Version: value.ManualRegistrationContactPolicy.Version,
			Content: value.ManualRegistrationContactPolicy.Content,
		}
	}
	if value.CancellationPolicy != nil {
		result.CancellationPolicy = &PublicPolicyTextResponse{
			Version: value.CancellationPolicy.Version,
			Content: value.CancellationPolicy.Content,
		}
	}
	return result
}
