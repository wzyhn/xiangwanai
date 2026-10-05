package coupon

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/google/uuid"
)

func TestSignedGrantPolicyVerifiesExactCustomerValues(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	face, validity, minimum := int64(2000), int64(86400), int64(0)
	scope, activityType := ScopeTypeActivityType, activity.ActivityTypeAIRoundtable
	document := SignedGrantPolicy{
		TenantID: uuid.New(), PolicyVersion: "guest-v1", Enabled: true,
		FaceValueCents: &face, ValiditySeconds: &validity,
		ScopeType: &scope, ScopeActivityType: &activityType,
		MinimumOrderCents: &minimum, EvidenceRef: "customer-approval-001",
		ApprovedAt:  time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		EffectiveAt: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC),
	}
	payload, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	signature := ed25519.Sign(privateKey, payload)
	got, err := VerifySignedGrantPolicy(payload, signature, publicKey)
	if err != nil || got.PolicyVersion != document.PolicyVersion ||
		*got.FaceValueCents != face {
		t.Fatalf("verified policy = %+v, %v", got, err)
	}
	mutated := append([]byte(nil), payload...)
	mutated[len(mutated)-1] ^= 1
	if _, err := VerifySignedGrantPolicy(mutated, signature, publicKey); !errors.Is(err, ErrInvalidSignedGrantPolicy) {
		t.Fatalf("modified values accepted: %v", err)
	}
	// Even a signed alternate encoding is rejected: approvers and the server
	// must see the same unique ordered set of facts.
	nonCanonical := append([]byte(" "), payload...)
	if _, err := VerifySignedGrantPolicy(nonCanonical, ed25519.Sign(privateKey, nonCanonical), publicKey); !errors.Is(err, ErrInvalidSignedGrantPolicy) {
		t.Fatalf("noncanonical values accepted: %v", err)
	}
	document.Enabled = false
	disabled, _ := json.Marshal(document)
	if _, err := VerifySignedGrantPolicy(disabled, ed25519.Sign(privateKey, disabled), publicKey); !errors.Is(err, ErrInvalidSignedGrantPolicy) {
		t.Fatalf("disabled policy retaining monetary fields accepted: %v", err)
	}
	stop := SignedGrantPolicy{
		TenantID: document.TenantID, PolicyVersion: "guest-stop-v2",
		EvidenceRef: "customer-stop-002", ApprovedAt: document.ApprovedAt,
		EffectiveAt: document.EffectiveAt,
	}
	stopPayload, _ := json.Marshal(stop)
	if _, err := VerifySignedGrantPolicy(stopPayload, ed25519.Sign(privateKey, stopPayload), publicKey); err != nil {
		t.Fatalf("explicit disabled version rejected: %v", err)
	}
}
