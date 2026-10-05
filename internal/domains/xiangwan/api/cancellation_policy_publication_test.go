package xiangwanapi

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestCancellationPolicyPublicationMatchesCutoffAndProtectsServiceState(t *testing.T) {
	t.Parallel()
	for _, hours := range []int{0, 24} {
		service, err := NewPublicPoliciesService(PublicPoliciesConfig{
			CancellationPolicyVersion: "cancel-v1", CancellationPolicyCutoffHours: hours,
		})
		if err != nil {
			t.Fatal(err)
		}
		value, err := service.Read(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if value.CancellationPolicy == nil || value.CancellationPolicy.Version != "cancel-v1" ||
			!strings.Contains(value.CancellationPolicy.Content, "按实付金额全额退回") ||
			!strings.Contains(value.CancellationPolicy.Content, "人工退款") ||
			!strings.Contains(value.CancellationPolicy.Content, "提交取消不代表退款已到账") {
			t.Fatalf("policy = %+v", value.CancellationPolicy)
		}
		if hours == 0 && !strings.Contains(value.CancellationPolicy.Content, "取消截止时间为活动开始时间。") {
			t.Fatal("wrong start cutoff")
		}
		if hours == 24 && !strings.Contains(value.CancellationPolicy.Content, "开始前 24 小时") {
			t.Fatal("wrong configured cutoff")
		}
		projected := projectPublicPoliciesResponse(value)
		if projected.CancellationPolicy == nil || projected.CancellationPolicy.Content != value.CancellationPolicy.Content {
			t.Fatal("published rules lost at HTTP boundary")
		}
		value.CancellationPolicy.Content = "mutated"
		again, err := service.Read(context.Background())
		if err != nil || again.CancellationPolicy.Content == "mutated" {
			t.Fatal("policy exposed mutable state")
		}
	}
}

func TestCancellationPolicyPublicationFailsClosed(t *testing.T) {
	t.Parallel()
	for _, config := range []PublicPoliciesConfig{
		{CancellationPolicyCutoffHours: 1},
		{CancellationPolicyVersion: "cancel-v1", CancellationPolicyCutoffHours: -1},
		{CancellationPolicyVersion: "cancel-v1", CancellationPolicyCutoffHours: 366*24 + 1},
	} {
		if _, err := NewPublicPoliciesService(config); !errors.Is(err, ErrInvalidPublicPoliciesService) {
			t.Fatalf("invalid publication accepted: %v", err)
		}
	}
	service, err := NewPublicPoliciesService(PublicPoliciesConfig{})
	if err != nil {
		t.Fatal(err)
	}
	value, err := service.Read(context.Background())
	if err != nil || value.CancellationPolicy != nil || value.Capabilities.PaidSelfServiceCancellationAvailable {
		t.Fatal("unconfigured policy advertised")
	}
}
