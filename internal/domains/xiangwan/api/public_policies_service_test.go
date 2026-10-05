package xiangwanapi

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestPublicPoliciesServiceReturnsOnlyConfiguredVersionsAndCapabilities(
	t *testing.T,
) {
	t.Parallel()

	service, err := NewPublicPoliciesService(PublicPoliciesConfig{
		PrivacyPolicyVersion:       "privacy-v3",
		CancellationPolicyVersion:  "cancel-v2",
		ExternalLinksPolicyVersion: "external-links-v1",
		ManualContactEnabled:       true,
		ManualContactPolicyVersion: "contact-v1",
		ManualContactPolicyText:    "联系人信息仅用于本次活动联络。",
		WeChatPaymentEnabled:       true,
	})
	if err != nil {
		t.Fatalf("NewPublicPoliciesService() error = %v", err)
	}
	value, err := service.Read(context.Background())
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	wantVersions := []PublicPolicyVersion{
		{Kind: PublicPolicyPrivacy, Version: "privacy-v3"},
		{Kind: PublicPolicyManualContact, Version: "contact-v1"},
		{Kind: PublicPolicyCancellation, Version: "cancel-v2"},
		{Kind: PublicPolicyExternalLinks, Version: "external-links-v1"},
	}
	if !reflect.DeepEqual(value.PublishedVersions, wantVersions) ||
		!value.Capabilities.PrivacyNoticeAvailable ||
		value.Capabilities.UserAgreementAvailable ||
		!value.Capabilities.PaidSelfServiceCancellationAvailable ||
		value.Capabilities.CustomerServiceAvailable ||
		!value.Capabilities.ExternalLinksAvailable ||
		!value.Capabilities.WeChatPaymentAvailable {
		t.Fatalf("Read() = %+v", value)
	}
	if !value.Capabilities.ManualRegistrationContactAvailable {
		t.Fatalf("Read() = %+v", value)
	}
	if value.ManualRegistrationContactPolicy == nil ||
		value.ManualRegistrationContactPolicy.Version != "contact-v1" ||
		value.ManualRegistrationContactPolicy.Content !=
			"联系人信息仅用于本次活动联络。" {
		t.Fatalf("Read() contact policy = %+v", value)
	}

	value.PublishedVersions[0].Version = "mutated"
	value.ManualRegistrationContactPolicy.Content = "mutated"
	again, err := service.Read(context.Background())
	if err != nil || again.PublishedVersions[0].Version != "privacy-v3" ||
		again.ManualRegistrationContactPolicy == nil ||
		again.ManualRegistrationContactPolicy.Content !=
			"联系人信息仅用于本次活动联络。" {
		t.Fatalf("Read() exposed mutable service state: %+v, %v", again, err)
	}
}

func TestPublicPoliciesServicePreservesManualContactVersionContract(
	t *testing.T,
) {
	t.Parallel()

	for _, version := range []string{
		"contact/2026-09",
		strings.Repeat("v", 100),
	} {
		service, err := NewPublicPoliciesService(PublicPoliciesConfig{
			ManualContactEnabled:       true,
			ManualContactPolicyVersion: version,
			ManualContactPolicyText:    "本政策用于报名联系人信息。",
		})
		if err != nil {
			t.Fatalf("NewPublicPoliciesService(%q) error = %v", version, err)
		}
		value, err := service.Read(context.Background())
		if err != nil || len(value.PublishedVersions) != 1 ||
			value.PublishedVersions[0] != (PublicPolicyVersion{
				Kind:    PublicPolicyManualContact,
				Version: version,
			}) {
			t.Fatalf("Read(%q) = %+v, %v", version, value, err)
		}
	}
}

func TestPublicPoliciesServiceFailsClosedForMissingAndInvalidConfiguration(
	t *testing.T,
) {
	t.Parallel()

	empty, err := NewPublicPoliciesService(PublicPoliciesConfig{})
	if err != nil {
		t.Fatalf("NewPublicPoliciesService(empty) error = %v", err)
	}
	value, err := empty.Read(context.Background())
	if err != nil || value.PublishedVersions == nil ||
		len(value.PublishedVersions) != 0 ||
		value.Capabilities != (PublicPolicyCapabilities{}) {
		t.Fatalf("Read(empty) = %+v, %v", value, err)
	}
	for _, version := range []string{
		" privacy-v1",
		"privacy/v1",
		"-privacy-v1",
		string(make([]byte, 65)),
	} {
		if _, gotErr := NewPublicPoliciesService(PublicPoliciesConfig{
			PrivacyPolicyVersion: version,
		}); !errors.Is(gotErr, ErrInvalidPublicPoliciesService) {
			t.Fatalf("NewPublicPoliciesService(%q) error = %v", version, gotErr)
		}
	}
	for _, config := range []PublicPoliciesConfig{
		{ManualContactEnabled: true},
		{ManualContactPolicyVersion: "contact-v1"},
		{ManualContactPolicyText: "公开说明"},
		{ManualContactEnabled: true, ManualContactPolicyVersion: "contact-v1"},
		{ManualContactEnabled: true, ManualContactPolicyText: "公开说明"},
		{ManualContactEnabled: true, ManualContactPolicyVersion: " contact-v1"},
		{ManualContactEnabled: true, ManualContactPolicyVersion: "contact\nv1"},
		{ManualContactEnabled: true, ManualContactPolicyVersion: strings.Repeat("v", 101)},
		{ManualContactEnabled: true, ManualContactPolicyVersion: "contact-v1", ManualContactPolicyText: " 公开说明"},
		{ManualContactEnabled: true, ManualContactPolicyVersion: "contact-v1", ManualContactPolicyText: strings.Repeat("文", 4001)},
	} {
		if _, gotErr := NewPublicPoliciesService(config); !errors.Is(
			gotErr,
			ErrInvalidPublicPoliciesService,
		) {
			t.Fatalf("NewPublicPoliciesService(incomplete contact config) error = %v", gotErr)
		}
	}
	if _, err := (*PublicPoliciesService)(nil).Read(context.Background()); !errors.Is(err, ErrInvalidPublicPoliciesService) {
		t.Fatalf("nil service Read() error = %v", err)
	}
}
