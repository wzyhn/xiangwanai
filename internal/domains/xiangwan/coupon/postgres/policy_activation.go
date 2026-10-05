package couponpostgres

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon"
	"github.com/wzyhn/xiangwanai/internal/pkg/requestctx"
	"github.com/google/uuid"
)

var (
	ErrInvalidPolicyActivation     = coupon.ErrInvalidPolicyActivation
	ErrPolicyActivationConflict    = coupon.ErrPolicyActivationConflict
	ErrPolicyActivationUnavailable = coupon.ErrPolicyActivationUnavailable
)

type PolicyActivationAuthorizer func(context.Context, *sql.Tx, uuid.UUID, uuid.UUID) error

type PolicyActivationCommand = coupon.PolicyActivationCommand
type PolicyActivationReceipt = coupon.PolicyActivationReceipt

// PolicyActivationWriter persists only customer-signed, future-effective
// policy versions. The caller's super-admin identity and active deployment
// generation are rechecked in the same transaction as the immutable row and
// audit. This writer never invents monetary or eligibility values.
type PolicyActivationWriter struct {
	db        *sql.DB
	tenantID  uuid.UUID
	publicKey ed25519.PublicKey
	authorize PolicyActivationAuthorizer
	now       func() time.Time
}

func NewPolicyActivationWriter(
	db *sql.DB, tenantID uuid.UUID, publicKey ed25519.PublicKey,
	authorize PolicyActivationAuthorizer,
) (*PolicyActivationWriter, error) {
	if db == nil || tenantID == uuid.Nil ||
		len(publicKey) != ed25519.PublicKeySize || authorize == nil {
		return nil, ErrPolicyActivationUnavailable
	}
	return &PolicyActivationWriter{
		db: db, tenantID: tenantID,
		publicKey: append(ed25519.PublicKey(nil), publicKey...),
		authorize: authorize, now: time.Now,
	}, nil
}

func (writer *PolicyActivationWriter) Activate(
	ctx context.Context, command PolicyActivationCommand,
) (PolicyActivationReceipt, error) {
	if writer == nil || writer.db == nil || writer.authorize == nil ||
		ctx == nil || command.ActorID == uuid.Nil || command.IdentityLinkID == uuid.Nil {
		return PolicyActivationReceipt{}, ErrInvalidPolicyActivation
	}
	document, err := coupon.VerifySignedGrantPolicy(
		command.Payload, command.Signature, writer.publicKey,
	)
	if err != nil || document.TenantID != writer.tenantID {
		return PolicyActivationReceipt{}, ErrInvalidPolicyActivation
	}
	now := writer.now().UTC().Truncate(time.Microsecond)
	if document.ApprovedAt.After(now) {
		return PolicyActivationReceipt{}, ErrInvalidPolicyActivation
	}
	// The unique policy version/effective instant enforce immutable identity.
	// Read committed lets an ON CONFLICT waiter see the winner's row when it
	// retries the exact signed fact in the same transaction.
	tx, err := writer.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return PolicyActivationReceipt{}, fmt.Errorf("begin Coupon policy activation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := writer.authorize(ctx, tx, command.ActorID, command.IdentityLinkID); err != nil {
		return PolicyActivationReceipt{}, err
	}
	if !document.EffectiveAt.After(now.Add(time.Minute)) {
		// Once a version takes effect, its exact signed receipt remains
		// replayable. A previously unseen version may never be backdated.
		receipt, readErr := readPolicyActivationReplay(ctx, tx, writer.tenantID,
			command.ActorID, document)
		if errors.Is(readErr, ErrPolicyActivationConflict) {
			return PolicyActivationReceipt{}, ErrInvalidPolicyActivation
		}
		if readErr != nil {
			return PolicyActivationReceipt{}, readErr
		}
		if err := tx.Commit(); err != nil {
			return PolicyActivationReceipt{}, fmt.Errorf("commit Coupon policy replay: %w", err)
		}
		return receipt, nil
	}
	var scopeType any
	if document.ScopeType != nil {
		scopeType = string(*document.ScopeType)
	}
	var recordedAt time.Time
	err = tx.QueryRowContext(ctx, `
INSERT INTO xiangwan_coupon_grant_policy_versions (
    tenant_id, policy_version, enabled, face_value_cents, validity_seconds,
    scope_type, scope_activity_type, scope_series_id, minimum_order_cents,
    evidence_ref, approved_at, effective_at, recorded_by, recorded_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
ON CONFLICT DO NOTHING
RETURNING recorded_at
`, writer.tenantID, document.PolicyVersion, document.Enabled,
		document.FaceValueCents, document.ValiditySeconds, scopeType,
		document.ScopeActivityType, document.ScopeSeriesID,
		document.MinimumOrderCents, document.EvidenceRef,
		document.ApprovedAt, document.EffectiveAt, command.ActorID,
		now).Scan(&recordedAt)
	if errors.Is(err, sql.ErrNoRows) {
		receipt, readErr := readPolicyActivationReplay(ctx, tx, writer.tenantID,
			command.ActorID, document)
		if readErr != nil {
			return PolicyActivationReceipt{}, readErr
		}
		if err := tx.Commit(); err != nil {
			return PolicyActivationReceipt{}, fmt.Errorf("commit Coupon policy replay: %w", err)
		}
		return receipt, nil
	}
	if err != nil {
		return PolicyActivationReceipt{}, fmt.Errorf("write Coupon grant policy: %w", err)
	}
	requestID := requestctx.RequestID(ctx)
	if requestID == "" {
		requestID = uuid.NewString()
	}
	signatureDigest := sha256.Sum256(command.Signature)
	_, err = tx.ExecContext(ctx, `
INSERT INTO xiangwan_admin_audit_events (
    id, tenant_id, actor_id, action_code, target_type, target_id,
    request_id, details, occurred_at, created_at
) VALUES ($1,$2,$3,'coupon.grant_policy_activated','tenant',$2,$4,
    jsonb_build_object('identity_link_id',$5::TEXT,'policy_version',$6::TEXT,
                       'enabled',$7::BOOLEAN,'signature_sha256',$8::TEXT),$9,$9)
`, uuid.New(), writer.tenantID, command.ActorID, requestID,
		command.IdentityLinkID.String(), document.PolicyVersion, document.Enabled,
		fmt.Sprintf("%x", signatureDigest[:]), now)
	if err != nil {
		return PolicyActivationReceipt{}, fmt.Errorf("audit Coupon policy activation: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return PolicyActivationReceipt{}, fmt.Errorf("commit Coupon policy activation: %w", err)
	}
	return PolicyActivationReceipt{
		PolicyVersion: document.PolicyVersion, Enabled: document.Enabled,
		EffectiveAt: document.EffectiveAt, RecordedAt: recordedAt.UTC(),
	}, nil
}

func readPolicyActivationReplay(
	ctx context.Context, tx *sql.Tx, tenantID, actorID uuid.UUID,
	document coupon.SignedGrantPolicy,
) (PolicyActivationReceipt, error) {
	var enabled bool
	var face, validity, minimum sql.NullInt64
	var scopeType, scopeActivity sql.NullString
	var scopeSeries uuid.NullUUID
	var evidence string
	var approvedAt, effectiveAt, recordedAt time.Time
	var recordedBy uuid.UUID
	err := tx.QueryRowContext(ctx, `
SELECT enabled, face_value_cents, validity_seconds, scope_type,
       scope_activity_type, scope_series_id, minimum_order_cents,
       evidence_ref, approved_at, effective_at, recorded_by, recorded_at
FROM xiangwan_coupon_grant_policy_versions
WHERE tenant_id = $1 AND policy_version = $2
FOR SHARE
`, tenantID, document.PolicyVersion).Scan(&enabled, &face, &validity,
		&scopeType, &scopeActivity, &scopeSeries, &minimum, &evidence,
		&approvedAt, &effectiveAt, &recordedBy, &recordedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return PolicyActivationReceipt{}, ErrPolicyActivationConflict
	}
	if err != nil {
		return PolicyActivationReceipt{}, fmt.Errorf("read Coupon policy replay: %w", err)
	}
	if recordedBy != actorID || enabled != document.Enabled ||
		evidence != document.EvidenceRef ||
		!approvedAt.Equal(document.ApprovedAt) ||
		!effectiveAt.Equal(document.EffectiveAt) ||
		!sameNullableInt(face, document.FaceValueCents) ||
		!sameNullableInt(validity, document.ValiditySeconds) ||
		!sameNullableInt(minimum, document.MinimumOrderCents) ||
		!sameNullableString(scopeType, optionalScopeType(document.ScopeType)) ||
		!sameNullableString(scopeActivity, optionalActivityType(document.ScopeActivityType)) ||
		!sameNullableUUID(scopeSeries, document.ScopeSeriesID) {
		return PolicyActivationReceipt{}, ErrPolicyActivationConflict
	}
	return PolicyActivationReceipt{
		PolicyVersion: document.PolicyVersion, Enabled: document.Enabled,
		EffectiveAt: effectiveAt.UTC(), RecordedAt: recordedAt.UTC(), Duplicate: true,
	}, nil
}

func sameNullableInt(stored sql.NullInt64, expected *int64) bool {
	return stored.Valid == (expected != nil) && (expected == nil || stored.Int64 == *expected)
}

func sameNullableString(stored sql.NullString, expected *string) bool {
	return stored.Valid == (expected != nil) && (expected == nil || stored.String == *expected)
}

func sameNullableUUID(stored uuid.NullUUID, expected *uuid.UUID) bool {
	return stored.Valid == (expected != nil) && (expected == nil || stored.UUID == *expected)
}

func optionalScopeType(value *coupon.ScopeType) *string {
	if value == nil {
		return nil
	}
	converted := string(*value)
	return &converted
}

func optionalActivityType(value *activity.ActivityType) *string {
	if value == nil {
		return nil
	}
	converted := string(*value)
	return &converted
}
