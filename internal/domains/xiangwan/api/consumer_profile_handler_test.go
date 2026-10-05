package xiangwanapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/consumerprofile"
	consumerprofilepostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/consumerprofile/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestConsumerProfileHandlerReturnsSeparatedProfileAuthorities(t *testing.T) {
	t.Parallel()

	principalID := apiUUID(31)
	avatarFileID := apiUUID(32)
	application := &fakeConsumerProfileApplication{snapshot: consumerprofile.Snapshot{
		Principal: consumerprofile.PrincipalProfile{
			Nickname:     "已审核昵称",
			AvatarURL:    "/approved/avatar",
			AvatarFileID: &avatarFileID,
			ETag:         "pp_opaque-etag",
		},
		Published: consumerprofile.PublishedProfile{
			Fields: consumerprofile.Fields{
				Occupation:   "产品设计",
				Introduction: "喜欢线下活动",
				Tags:         []string{"徒步"},
				Visibility:   consumerprofile.Visibility{Occupation: true},
			},
			PrivacyPolicyVersion: "privacy-v1",
			Version:              2,
			UpdatedAt:            time.Date(2026, 9, 14, 5, 6, 7, 0, time.UTC),
		},
		Pending: &consumerprofile.PendingUpdate{
			Fields:           consumerprofile.Fields{Occupation: "新职业", Tags: []string{}},
			CandidateVersion: 3,
			SubmittedAt:      time.Date(2026, 9, 14, 6, 7, 8, 0, time.UTC),
		},
	}}
	engine := gin.New()
	NewConsumerProfileHandler(
		application,
		func(*gin.Context) (uuid.UUID, error) { return principalID, nil },
	).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/xiangwan/me/profile",
		nil,
	)
	engine.ServeHTTP(recorder, request)

	body := recorder.Body.String()
	if recorder.Code != http.StatusOK || application.getPrincipalID != principalID ||
		!strings.Contains(body, `"nickname":"已审核昵称"`) ||
		!strings.Contains(body, `"principal_profile_etag":"pp_opaque-etag"`) ||
		!strings.Contains(body, `"occupation":"产品设计"`) ||
		!strings.Contains(body, `"candidate_version":3`) {
		t.Fatalf("GET profile status=%d app=%+v body=%s", recorder.Code, application, body)
	}
	for _, forbidden := range []string{
		"openid", "unionid", "avatar_file_id", "operation_key", "fingerprint", "trace_id",
	} {
		if strings.Contains(strings.ToLower(body), forbidden) {
			t.Fatalf("private profile fact %q crossed response: %s", forbidden, body)
		}
	}
}

func TestProjectConsumerAvatarURLConvertsOnlyMatchingAuthPublication(t *testing.T) {
	t.Parallel()

	principalID := uuid.MustParse("00000000-0000-0000-0000-000000000041")
	fileID := uuid.MustParse("00000000-0000-0000-0000-000000000042")
	legacy := "/api/v1/auth/principals/" + principalID.String() +
		"/avatar/" + fileID.String()
	value := consumerprofile.PrincipalProfile{
		AvatarURL:    legacy,
		AvatarFileID: &fileID,
	}
	if got := projectConsumerAvatarURL(principalID, value); got != ConsumerLegacyAvatarPath(principalID, fileID) {
		t.Fatalf("projectConsumerAvatarURL() = %q", got)
	}
	value.AvatarURL = "/api/v1/files/" + fileID.String() + "/content?exp=1&sig=old"
	if got := projectConsumerAvatarURL(principalID, value); got != ConsumerLegacyAvatarPath(principalID, fileID) {
		t.Fatalf("legacy storage URL = %q", got)
	}
	value.AvatarURL = "/api/v1/auth/principals/00000000-0000-0000-0000-000000000099/avatar/" + fileID.String()
	if got := projectConsumerAvatarURL(principalID, value); got != value.AvatarURL {
		t.Fatalf("mismatched auth URL = %q, want unchanged", got)
	}
	value.AvatarURL = "https://cdn.example/avatar.png"
	if got := projectConsumerAvatarURL(principalID, value); got != value.AvatarURL {
		t.Fatalf("foreign URL = %q, want unchanged", got)
	}
}

func TestConsumerProfileHandlerAllowsEmptyJSONCompatibleRead(t *testing.T) {
	t.Parallel()

	principalID := apiUUID(37)
	application := &fakeConsumerProfileApplication{snapshot: consumerprofile.Snapshot{}}
	engine := gin.New()
	NewConsumerProfileHandler(
		application,
		func(*gin.Context) (uuid.UUID, error) { return principalID, nil },
	).RegisterRoutes(engine.Group("/api/v1/xiangwan"))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/xiangwan/me/profile", nil)
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK || application.getPrincipalID != principalID {
		t.Fatalf("GET profile with empty JSON-compatible request status=%d app=%+v body=%s", recorder.Code, application, recorder.Body.String())
	}
}

func TestConsumerProfileHandlerSubmitsCompleteExtensionCandidate(t *testing.T) {
	t.Parallel()

	principalID := apiUUID(33)
	operationKey := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	application := &fakeConsumerProfileApplication{receipt: consumerprofile.MutationReceipt{
		CandidateID: apiUUID(34),
		Fields: consumerprofile.Fields{
			Occupation:   "产品设计",
			Introduction: "喜欢线下活动",
			Tags:         []string{"徒步", "咖啡"},
			Visibility:   consumerprofile.Visibility{Occupation: true, Tags: true},
		},
		BaseVersion:      1,
		CandidateVersion: 2,
		ModerationStatus: consumerprofile.ModerationStatusApproved,
		PublishedVersion: 2,
		PrivacyVersion:   "privacy-v1",
		SubmittedAt:      time.Date(2026, 9, 14, 7, 8, 9, 0, time.UTC),
	}}
	engine := gin.New()
	NewConsumerProfileHandler(
		application,
		func(*gin.Context) (uuid.UUID, error) { return principalID, nil },
	).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPatch,
		"/api/v1/xiangwan/me/profile",
		strings.NewReader(`{
            "expected_version":1,
            "occupation":"产品设计",
            "introduction":"喜欢线下活动",
            "tags":["徒步","咖啡"],
            "visibility":{"occupation":true,"introduction":false,"tags":true}
        }`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", operationKey)
	engine.ServeHTTP(recorder, request)

	body := recorder.Body.String()
	if recorder.Code != http.StatusOK || application.updatePrincipalID != principalID ||
		application.updateRequest.OperationKey.String() != operationKey ||
		application.updateRequest.ExpectedVersion != 1 ||
		application.updateRequest.Fields.Occupation != "产品设计" ||
		!application.updateRequest.Fields.Visibility.Tags ||
		!strings.Contains(body, `"moderation_status":"approved"`) ||
		!strings.Contains(body, `"published_version":2`) ||
		strings.Contains(body, application.receipt.CandidateID.String()) {
		t.Fatalf("PATCH profile status=%d app=%+v body=%s", recorder.Code, application, body)
	}
}

func TestConsumerProfileHandlerRejectsAmbiguousProfileWrites(t *testing.T) {
	t.Parallel()

	validBody := `{"expected_version":0,"occupation":"","introduction":"","tags":[],"visibility":{"occupation":false,"introduction":false,"tags":false}}`
	tests := []struct {
		name        string
		path        string
		body        string
		contentType string
		operation   string
	}{
		{name: "query", path: "/api/v1/xiangwan/me/profile?x=1", body: validBody, contentType: "application/json", operation: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"},
		{name: "content type", path: "/api/v1/xiangwan/me/profile", body: validBody, contentType: "text/plain", operation: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"},
		{name: "missing operation", path: "/api/v1/xiangwan/me/profile", body: validBody, contentType: "application/json"},
		{name: "missing field", path: "/api/v1/xiangwan/me/profile", body: `{"expected_version":0}`, contentType: "application/json", operation: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"},
		{name: "missing visibility choice", path: "/api/v1/xiangwan/me/profile", body: `{"expected_version":0,"occupation":"","introduction":"","tags":[],"visibility":{"occupation":false,"tags":false}}`, contentType: "application/json", operation: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"},
		{name: "nickname injection", path: "/api/v1/xiangwan/me/profile", body: strings.TrimSuffix(validBody, "}") + `,"nickname":"client-owned"}`, contentType: "application/json", operation: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"},
		{name: "trailing json", path: "/api/v1/xiangwan/me/profile", body: validBody + `{}`, contentType: "application/json", operation: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"},
		{name: "oversize", path: "/api/v1/xiangwan/me/profile", body: `{"expected_version":0,"occupation":"` + strings.Repeat("x", maxConsumerProfileBodyBytes) + `","introduction":"","tags":[],"visibility":{"occupation":false,"introduction":false,"tags":false}}`, contentType: "application/json", operation: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			application := &fakeConsumerProfileApplication{}
			engine := gin.New()
			NewConsumerProfileHandler(
				application,
				func(*gin.Context) (uuid.UUID, error) { return apiUUID(35), nil },
			).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPatch, test.path, strings.NewReader(test.body))
			request.Header.Set("Content-Type", test.contentType)
			if test.operation != "" {
				request.Header.Set("Idempotency-Key", test.operation)
			}
			engine.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusBadRequest && recorder.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if application.updateCalls != 0 {
				t.Fatalf("application called %d time(s)", application.updateCalls)
			}
		})
	}
}

func TestConsumerProfileHandlerMapsSafeFailureClasses(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "moderation unavailable", err: ErrConsumerProfileModerationUnavailable, wantStatus: http.StatusServiceUnavailable},
		{name: "content rejected", err: ErrConsumerProfileContentRejected, wantStatus: http.StatusBadRequest},
		{name: "version conflict", err: consumerprofilepostgres.ErrVersionConflict, wantStatus: http.StatusConflict},
		{name: "transaction retry", err: consumerprofilepostgres.ErrTransactionConflict, wantStatus: http.StatusServiceUnavailable},
		{name: "generation cutover", err: consumerprofilepostgres.ErrGenerationInactive, wantStatus: http.StatusServiceUnavailable},
		{name: "identity unavailable", err: consumerprofilepostgres.ErrProviderIdentityUnavailable, wantStatus: http.StatusForbidden},
		{name: "database", err: errors.New("private database address"), wantStatus: http.StatusInternalServerError},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			application := &fakeConsumerProfileApplication{err: test.err}
			engine := gin.New()
			NewConsumerProfileHandler(
				application,
				func(*gin.Context) (uuid.UUID, error) { return apiUUID(36), nil },
			).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodPatch,
				"/api/v1/xiangwan/me/profile",
				strings.NewReader(`{"expected_version":0,"occupation":"","introduction":"","tags":[],"visibility":{"occupation":false,"introduction":false,"tags":false}}`),
			)
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Idempotency-Key", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
			engine.ServeHTTP(recorder, request)
			if recorder.Code != test.wantStatus ||
				strings.Contains(recorder.Body.String(), "database address") {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

type fakeConsumerProfileApplication struct {
	snapshot          consumerprofile.Snapshot
	receipt           consumerprofile.MutationReceipt
	err               error
	getPrincipalID    uuid.UUID
	updatePrincipalID uuid.UUID
	updateRequest     ConsumerProfileUpdateRequest
	updateCalls       int
}

func (application *fakeConsumerProfileApplication) GetMine(
	_ context.Context,
	principalID uuid.UUID,
) (consumerprofile.Snapshot, error) {
	application.getPrincipalID = principalID
	return application.snapshot, application.err
}

func (application *fakeConsumerProfileApplication) Update(
	_ context.Context,
	principalID uuid.UUID,
	request ConsumerProfileUpdateRequest,
) (consumerprofile.MutationReceipt, error) {
	application.updateCalls++
	application.updatePrincipalID = principalID
	application.updateRequest = request
	return application.receipt, application.err
}
