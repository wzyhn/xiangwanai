package xiangwanapi

import (
	identitypostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/identity/postgres"
	"context"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeRegistrationContactStore struct {
	principalID uuid.UUID
	writes      int
}

func (store *fakeRegistrationContactStore) Read(_ context.Context, id uuid.UUID) (identitypostgres.RegistrationContact, error) {
	store.principalID = id
	return identitypostgres.RegistrationContact{Configured: true, Version: 1, PhoneE164: "+8613800000000", Nickname: "测试用户", PrincipalProfileETag: "etag"}, nil
}
func (store *fakeRegistrationContactStore) Update(ctx context.Context, id uuid.UUID, _ identitypostgres.RegistrationContactUpdate) (identitypostgres.RegistrationContact, error) {
	store.writes++
	return store.Read(ctx, id)
}

func TestRegistrationContactIsPrivateNoStoreAndRejectsClientOwnerSelectors(t *testing.T) {
	handler, _ := newConsumerIdentityTestHandler(t, &fakeConsumerIdentityStore{})
	store := &fakeRegistrationContactStore{}
	handler.SetRegistrationContactStore(store)
	router := gin.New()
	handler.RegisterAuthenticatedRoutes(router.Group(""))
	read := httptest.NewRecorder()
	router.ServeHTTP(read, httptest.NewRequest(http.MethodGet, "/me/registration-contact", nil))
	if read.Code != http.StatusOK || read.Header().Get("Cache-Control") != "no-store" || store.principalID != apiUUID(42) {
		t.Fatal("owner read was not private/session bound")
	}
	payload := map[string]any{"nickname": "测试用户", "phone_e164": "+8613800000000", "principal_profile_etag": "etag", "expected_version": 1, "privacy_policy_version": "privacy-v1", "contact_policy_version": "contact-v1"}
	for _, extra := range []string{"principal_id", "tenant_id", "role", "verified", "phone_bound"} {
		payload[extra] = "injected"
		body, _ := json.Marshal(payload)
		request := httptest.NewRequest(http.MethodPatch, "/me/registration-contact", strings.NewReader(string(body)))
		request.Header.Set("Content-Type", "application/json")
		output := httptest.NewRecorder()
		router.ServeHTTP(output, request)
		if output.Code != http.StatusBadRequest || store.writes != 0 {
			t.Fatalf("client field %s reached private writer", extra)
		}
		delete(payload, extra)
	}
	body, _ := json.Marshal(payload)
	request := httptest.NewRequest(http.MethodPatch, "/me/registration-contact", strings.NewReader(string(body)))
	request.Header.Set("Content-Type", "application/json")
	output := httptest.NewRecorder()
	router.ServeHTTP(output, request)
	if output.Code != http.StatusOK || store.writes != 1 || store.principalID != apiUUID(42) {
		t.Fatal("valid command did not use session principal")
	}
	handler.nicknameModerator = &fakeNicknameModerator{err: ErrNicknameRejected}
	output = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPatch, "/me/registration-contact", strings.NewReader(string(body)))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(output, request)
	if output.Code != http.StatusBadRequest || store.writes != 1 {
		t.Fatal("rejected nickname reached contact writer")
	}
}
