package coupon

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/google/uuid"
)

var ErrInvalidSignedGrantPolicy = errors.New("invalid signed xiangwan Coupon grant policy")

var policyEvidenceRefPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/#-]{0,511}$`)

var (
	ErrInvalidPolicyActivation     = errors.New("invalid xiangwan Coupon policy activation")
	ErrPolicyActivationConflict    = errors.New("xiangwan Coupon policy activation conflict")
	ErrPolicyActivationUnavailable = errors.New("xiangwan Coupon policy activation unavailable")
)

type PolicyActivationCommand struct {
	ActorID        uuid.UUID
	IdentityLinkID uuid.UUID
	Payload        []byte
	Signature      []byte
}

type PolicyActivationReceipt struct {
	PolicyVersion string
	Enabled       bool
	EffectiveAt   time.Time
	RecordedAt    time.Time
	Duplicate     bool
}

type PolicyActivationWriter interface {
	Activate(context.Context, PolicyActivationCommand) (PolicyActivationReceipt, error)
}

// SignedGrantPolicy is the exact customer-approved, canonical JSON document.
// A disabled version is an explicit future-effective stop, not an empty active
// policy. No runtime defaults can supply monetary values or scope.
type SignedGrantPolicy struct {
	TenantID          uuid.UUID              `json:"tenant_id"`
	PolicyVersion     string                 `json:"policy_version"`
	Enabled           bool                   `json:"enabled"`
	FaceValueCents    *int64                 `json:"face_value_cents"`
	ValiditySeconds   *int64                 `json:"validity_seconds"`
	ScopeType         *ScopeType             `json:"scope_type"`
	ScopeActivityType *activity.ActivityType `json:"scope_activity_type"`
	ScopeSeriesID     *uuid.UUID             `json:"scope_series_id"`
	MinimumOrderCents *int64                 `json:"minimum_order_cents"`
	EvidenceRef       string                 `json:"evidence_ref"`
	ApprovedAt        time.Time              `json:"approved_at"`
	EffectiveAt       time.Time              `json:"effective_at"`
}

// VerifySignedGrantPolicy accepts only one compact, ordered JSON encoding and
// verifies its Ed25519 signature before an administrator may persist it. This
// avoids duplicate-key and alternate-encoding ambiguity in approval evidence.
func VerifySignedGrantPolicy(
	payload, signature []byte, publicKey ed25519.PublicKey,
) (SignedGrantPolicy, error) {
	if len(payload) == 0 || len(payload) > 4096 ||
		len(signature) != ed25519.SignatureSize ||
		len(publicKey) != ed25519.PublicKeySize ||
		!ed25519.Verify(publicKey, payload, signature) {
		return SignedGrantPolicy{}, ErrInvalidSignedGrantPolicy
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var document SignedGrantPolicy
	if err := decoder.Decode(&document); err != nil {
		return SignedGrantPolicy{}, ErrInvalidSignedGrantPolicy
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return SignedGrantPolicy{}, ErrInvalidSignedGrantPolicy
	}
	canonical, err := json.Marshal(document)
	if err != nil || !bytes.Equal(canonical, payload) ||
		!validSignedGrantPolicy(document) {
		return SignedGrantPolicy{}, ErrInvalidSignedGrantPolicy
	}
	return document, nil
}

func validSignedGrantPolicy(value SignedGrantPolicy) bool {
	if value.TenantID == uuid.Nil || !validReference(value.PolicyVersion) ||
		!policyEvidenceRefPattern.MatchString(value.EvidenceRef) ||
		value.ApprovedAt.IsZero() || value.EffectiveAt.IsZero() ||
		value.ApprovedAt.Location() != time.UTC ||
		value.EffectiveAt.Location() != time.UTC ||
		value.EffectiveAt.Before(value.ApprovedAt) {
		return false
	}
	if !value.Enabled {
		return value.FaceValueCents == nil && value.ValiditySeconds == nil &&
			value.ScopeType == nil && value.ScopeActivityType == nil &&
			value.ScopeSeriesID == nil && value.MinimumOrderCents == nil
	}
	if value.FaceValueCents == nil || value.ValiditySeconds == nil ||
		value.ScopeType == nil || value.MinimumOrderCents == nil ||
		*value.ValiditySeconds < 1 ||
		*value.ValiditySeconds > int64(MaxPolicyValidity/time.Second) {
		return false
	}
	return ValidateGrantPolicy(GrantPolicy{
		Configured: true, PolicyVersion: value.PolicyVersion,
		FaceValueCents: *value.FaceValueCents,
		Validity:       time.Duration(*value.ValiditySeconds) * time.Second,
		ScopeType:      *value.ScopeType, ScopeActivityType: value.ScopeActivityType,
		ScopeSeriesID:     value.ScopeSeriesID,
		MinimumOrderCents: *value.MinimumOrderCents,
	}) == nil
}
