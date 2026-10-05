package xiangwanapi

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
)

var ErrInvalidPublicPoliciesService = errors.New(
	"invalid xiangwan Public Policies service",
)

type PublicPolicyKind string

const (
	PublicPolicyPrivacy         PublicPolicyKind = "privacy"
	PublicPolicyManualContact   PublicPolicyKind = "manual_contact"
	PublicPolicyUserAgreement   PublicPolicyKind = "user_agreement"
	PublicPolicyCancellation    PublicPolicyKind = "cancellation"
	PublicPolicyCustomerService PublicPolicyKind = "customer_service"
	PublicPolicyExternalLinks   PublicPolicyKind = "external_links"
)

type PublicPolicyVersion struct {
	Kind    PublicPolicyKind
	Version string
}

type PublicPolicyText struct {
	Version string
	Content string
}

type PublicPolicyCapabilities struct {
	PrivacyNoticeAvailable               bool
	UserAgreementAvailable               bool
	PaidSelfServiceCancellationAvailable bool
	CustomerServiceAvailable             bool
	ExternalLinksAvailable               bool
	ManualRegistrationContactAvailable   bool
	WeChatPaymentAvailable               bool
}

type PublicPolicies struct {
	PublishedVersions               []PublicPolicyVersion
	ManualRegistrationContactPolicy *PublicPolicyText
	CancellationPolicy              *PublicPolicyText
	Capabilities                    PublicPolicyCapabilities
}

type PublicPoliciesConfig struct {
	PrivacyPolicyVersion          string
	UserAgreementVersion          string
	CancellationPolicyVersion     string
	CancellationPolicyCutoffHours int
	CustomerServicePolicyVersion  string
	ExternalLinksPolicyVersion    string
	ManualContactEnabled          bool
	ManualContactPolicyVersion    string
	ManualContactPolicyText       string
	WeChatPaymentEnabled          bool
}

type PublicPoliciesService struct {
	value PublicPolicies
}

func NewPublicPoliciesService(
	config PublicPoliciesConfig,
) (*PublicPoliciesService, error) {
	if config.CancellationPolicyCutoffHours < 0 || config.CancellationPolicyCutoffHours > 366*24 {
		return nil, ErrInvalidPublicPoliciesService
	}
	if _, err := registration.NewSelfCancellationPolicy(config.CancellationPolicyVersion,
		time.Duration(config.CancellationPolicyCutoffHours)*time.Hour); err != nil {
		return nil, ErrInvalidPublicPoliciesService
	}
	manualContactConfigured := config.ManualContactPolicyVersion != "" &&
		config.ManualContactPolicyText != ""
	if config.ManualContactEnabled != manualContactConfigured ||
		(config.ManualContactPolicyVersion != "") !=
			(config.ManualContactPolicyText != "") ||
		(config.ManualContactEnabled &&
			(!validManualContactPolicyVersion(config.ManualContactPolicyVersion) ||
				!validManualContactPolicyText(config.ManualContactPolicyText))) {
		return nil, ErrInvalidPublicPoliciesService
	}
	ordered := []PublicPolicyVersion{
		{Kind: PublicPolicyPrivacy, Version: config.PrivacyPolicyVersion},
		{Kind: PublicPolicyManualContact, Version: config.ManualContactPolicyVersion},
		{Kind: PublicPolicyUserAgreement, Version: config.UserAgreementVersion},
		{Kind: PublicPolicyCancellation, Version: config.CancellationPolicyVersion},
		{Kind: PublicPolicyCustomerService, Version: config.CustomerServicePolicyVersion},
		{Kind: PublicPolicyExternalLinks, Version: config.ExternalLinksPolicyVersion},
	}
	published := make([]PublicPolicyVersion, 0, len(ordered))
	for _, policy := range ordered {
		if policy.Version == "" {
			continue
		}
		if policy.Kind != PublicPolicyManualContact &&
			!validPublicPolicyVersion(policy.Version) {
			return nil, ErrInvalidPublicPoliciesService
		}
		published = append(published, policy)
	}
	value := PublicPolicies{
		PublishedVersions: published,
		Capabilities: PublicPolicyCapabilities{
			PrivacyNoticeAvailable:               config.PrivacyPolicyVersion != "",
			UserAgreementAvailable:               config.UserAgreementVersion != "",
			PaidSelfServiceCancellationAvailable: config.CancellationPolicyVersion != "",
			CustomerServiceAvailable:             config.CustomerServicePolicyVersion != "",
			ExternalLinksAvailable:               config.ExternalLinksPolicyVersion != "",
			ManualRegistrationContactAvailable:   manualContactConfigured,
			WeChatPaymentAvailable:               config.WeChatPaymentEnabled,
		},
	}
	if manualContactConfigured {
		value.ManualRegistrationContactPolicy = &PublicPolicyText{
			Version: config.ManualContactPolicyVersion,
			Content: config.ManualContactPolicyText,
		}
	}
	if config.CancellationPolicyVersion != "" {
		deadline := "取消截止时间为活动开始时间。"
		if config.CancellationPolicyCutoffHours > 0 {
			deadline = fmt.Sprintf("取消截止时间为活动开始前 %d 小时。", config.CancellationPolicyCutoffHours)
		}
		value.CancellationPolicy = &PublicPolicyText{
			Version: config.CancellationPolicyVersion,
			Content: deadline + "截止时间内可取消报名，取消后参与资格、签到凭证及私密访问失效，名额立即释放。未使用优惠券的已支付订单按实付金额全额退回，进入人工退款处理；提交取消不代表退款已到账。超过截止时间或涉及尚未发布处理规则的优惠券，请联系活动方。",
		}
	}
	return &PublicPoliciesService{value: value}, nil
}

func (service *PublicPoliciesService) Read(
	ctx context.Context,
) (PublicPolicies, error) {
	if service == nil || ctx == nil || service.value.PublishedVersions == nil {
		return PublicPolicies{}, ErrInvalidPublicPoliciesService
	}
	result := service.value
	result.PublishedVersions = make(
		[]PublicPolicyVersion,
		len(service.value.PublishedVersions),
	)
	copy(result.PublishedVersions, service.value.PublishedVersions)
	if service.value.ManualRegistrationContactPolicy != nil {
		copyValue := *service.value.ManualRegistrationContactPolicy
		result.ManualRegistrationContactPolicy = &copyValue
	}
	if service.value.CancellationPolicy != nil {
		copyValue := *service.value.CancellationPolicy
		result.CancellationPolicy = &copyValue
	}
	return result, nil
}

func validManualContactPolicyVersion(value string) bool {
	return value != "" && value == strings.TrimSpace(value) &&
		len([]rune(value)) <= 100 && !strings.ContainsAny(value, "\r\n\x00")
}

func validManualContactPolicyText(value string) bool {
	return value != "" && value == strings.TrimSpace(value) &&
		len([]rune(value)) <= 4000 && !strings.ContainsRune(value, '\x00')
}

func validPublicPolicyVersion(value string) bool {
	if value == "" || value != strings.TrimSpace(value) || len(value) > 64 {
		return false
	}
	for index, character := range value {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			(index > 0 && strings.ContainsRune("._:-", character)) {
			continue
		}
		return false
	}
	return true
}
