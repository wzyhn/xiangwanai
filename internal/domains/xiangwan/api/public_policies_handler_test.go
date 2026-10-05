package xiangwanapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestPublicPoliciesHandlerReturnsSafePublishedVersionsAndSwitches(t *testing.T) {
	t.Parallel()

	application := &fakePublicPoliciesApplication{value: PublicPolicies{
		PublishedVersions: []PublicPolicyVersion{
			{Kind: PublicPolicyPrivacy, Version: "privacy-v3"},
			{Kind: PublicPolicyManualContact, Version: "contact-v1"},
			{Kind: PublicPolicyCancellation, Version: "cancel-v2"},
		},
		Capabilities: PublicPolicyCapabilities{
			PrivacyNoticeAvailable:               true,
			PaidSelfServiceCancellationAvailable: true,
			ManualRegistrationContactAvailable:   true,
			WeChatPaymentAvailable:               true,
		},
		ManualRegistrationContactPolicy: &PublicPolicyText{
			Version: "contact-v1",
			Content: "联系人信息仅用于本次活动联络。",
		},
	}}
	engine := gin.New()
	NewPublicPoliciesHandler(application).RegisterRoutes(
		engine.Group("/api/v1/xiangwan"),
	)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(
		recorder,
		httptest.NewRequest(
			http.MethodGet,
			"/api/v1/xiangwan/public-policies",
			nil,
		),
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET Public Policies status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Code int                    `json:"code"`
		Data PublicPoliciesResponse `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode Public Policies response: %v", err)
	}
	if envelope.Code != 0 || application.calls != 1 ||
		len(envelope.Data.PublishedVersions) != 3 ||
		envelope.Data.PublishedVersions[0].Kind != PublicPolicyPrivacy ||
		envelope.Data.PublishedVersions[1].Kind != PublicPolicyManualContact ||
		envelope.Data.PublishedVersions[1].Version != "contact-v1" ||
		!envelope.Data.Capabilities.PrivacyNoticeAvailable ||
		!envelope.Data.Capabilities.PaidSelfServiceCancellationAvailable ||
		!envelope.Data.Capabilities.ManualRegistrationContactAvailable ||
		!envelope.Data.Capabilities.WeChatPaymentAvailable ||
		envelope.Data.ManualRegistrationContactPolicy == nil ||
		envelope.Data.ManualRegistrationContactPolicy.Version != "contact-v1" ||
		envelope.Data.ManualRegistrationContactPolicy.Content !=
			"联系人信息仅用于本次活动联络。" ||
		envelope.Data.Capabilities.CustomerServiceAvailable {
		t.Fatalf("GET Public Policies response = %+v", envelope.Data)
	}
	for _, forbidden := range []string{
		"allowlist",
		"domain",
		"contact_detail",
		"policy_text",
		"cutoff_hours",
		"tenant_id",
	} {
		if strings.Contains(recorder.Body.String(), forbidden) {
			t.Fatalf("private/config field %q crossed HTTP boundary: %s", forbidden, recorder.Body.String())
		}
	}
}

func TestPublicPoliciesHandlerRejectsQueryAndUsesOpaqueFailure(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name       string
		path       string
		appErr     error
		wantStatus int
	}{
		{
			name:       "query",
			path:       "/api/v1/xiangwan/public-policies?preview=true",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "service failure",
			path:       "/api/v1/xiangwan/public-policies",
			appErr:     errors.New("private configuration path"),
			wantStatus: http.StatusInternalServerError,
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			application := &fakePublicPoliciesApplication{err: test.appErr}
			engine := gin.New()
			NewPublicPoliciesHandler(application).RegisterRoutes(
				engine.Group("/api/v1/xiangwan"),
			)
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(
				recorder,
				httptest.NewRequest(http.MethodGet, test.path, nil),
			)
			if recorder.Code != test.wantStatus ||
				strings.Contains(recorder.Body.String(), "configuration path") {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if test.wantStatus == http.StatusBadRequest && application.calls != 0 {
				t.Fatalf("invalid query reached application %d times", application.calls)
			}
		})
	}
}

type fakePublicPoliciesApplication struct {
	value PublicPolicies
	err   error
	calls int
}

func (application *fakePublicPoliciesApplication) Read(
	_ context.Context,
) (PublicPolicies, error) {
	application.calls++
	return application.value, application.err
}
