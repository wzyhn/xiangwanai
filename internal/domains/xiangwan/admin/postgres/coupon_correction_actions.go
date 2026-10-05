package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/google/uuid"
)

func (catalog *Catalog) TransitionCouponCorrection(
	ctx context.Context, command xiangwanadmin.CouponCorrectionActionCommand,
) (receipt xiangwanadmin.CouponCorrectionActionResult, resultErr error) {
	defer func() {
		resultErr = catalog.auditRejectedCommand(ctx, command.ActorID,
			command.IdentityLinkID, command.OperationID,
			"coupon_correction.transition", "coupon_correction",
			command.CorrectionEntryID, command.RequestID, resultErr)
	}()
	command.OperatorNote = strings.TrimSpace(command.OperatorNote)
	command.EvidenceReference = strings.TrimSpace(command.EvidenceReference)
	if !catalog.valid(ctx) || !validWriteIdentity(command.ActorID,
		command.IdentityLinkID, command.OperationID, command.RequestID) ||
		command.CorrectionEntryID == uuid.Nil ||
		command.OperationID.Version() != 4 ||
		command.OperationID.Variant() != uuid.RFC4122 ||
		len([]rune(command.OperatorNote)) < 1 ||
		len([]rune(command.OperatorNote)) > 500 ||
		strings.IndexFunc(command.OperatorNote, unicode.IsControl) >= 0 ||
		strings.IndexFunc(command.EvidenceReference, unicode.IsControl) >= 0 {
		return receipt, xiangwanadmin.ErrInvalidCatalogRequest
	}
	switch command.Action {
	case xiangwanadmin.CouponCorrectionStart:
		if command.ExpectedVersion != 0 || command.EvidenceKind != "" ||
			command.EvidenceReference != "" || command.AdjustmentCents != 0 {
			return receipt, xiangwanadmin.ErrInvalidCatalogRequest
		}
	case xiangwanadmin.CouponCorrectionResolve:
		if command.ExpectedVersion != 1 ||
			(command.EvidenceKind != "financial" && command.EvidenceKind != "entitlement") ||
			len([]rune(command.EvidenceReference)) < 1 || len([]rune(command.EvidenceReference)) > 128 {
			return receipt, xiangwanadmin.ErrInvalidCatalogRequest
		}
	default:
		return receipt, xiangwanadmin.ErrInvalidCatalogRequest
	}
	digest, err := commandDigest(struct {
		CorrectionEntryID uuid.UUID
		ExpectedVersion   int64
		Action            xiangwanadmin.CouponCorrectionAction
		EvidenceKind      string
		EvidenceReference string
		AdjustmentCents   int64
		OperatorNote      string
	}{command.CorrectionEntryID, command.ExpectedVersion, command.Action,
		command.EvidenceKind, command.EvidenceReference,
		command.AdjustmentCents, command.OperatorNote})
	if err != nil {
		return receipt, err
	}
	write, err := catalog.beginCouponCorrectionWrite(ctx, command)
	if err != nil {
		return receipt, err
	}
	defer func() { _ = write.Rollback() }()
	operationKind := "coupon_correction." + string(command.Action)
	if saved, replay, err := readOperation[operationResult[xiangwanadmin.CouponCorrectionActionResult]](
		ctx, write, catalog.tenantID, command.ActorID, command.OperationID,
		operationKind, digest); err != nil {
		return receipt, err
	} else if replay {
		if saved.Value.CorrectionEntryID != command.CorrectionEntryID ||
			saved.Value.CouponID == uuid.Nil || saved.Value.RecordedAt.IsZero() ||
			(saved.Value.Status != "processing" && saved.Value.Status != "resolved") ||
			saved.Value.Version != command.ExpectedVersion+1 {
			return receipt, xiangwanadmin.ErrOperationConflict
		}
		if err := write.Commit(); err != nil {
			return receipt, fmt.Errorf("commit Coupon correction replay: %w", err)
		}
		return saved.Value, nil
	}
	var couponID uuid.UUID
	var faceValue int64
	err = write.QueryRowContext(ctx, `
SELECT marker.coupon_id, instrument.face_value_cents
FROM xiangwan_coupon_entries AS marker
JOIN xiangwan_coupons AS instrument
  ON instrument.tenant_id = marker.tenant_id
 AND instrument.id = marker.coupon_id
WHERE marker.tenant_id = $1 AND marker.id = $2
  AND marker.entry_type = 'correction_required'
FOR UPDATE OF instrument
`, catalog.tenantID, command.CorrectionEntryID).Scan(&couponID, &faceValue)
	if errors.Is(err, sql.ErrNoRows) {
		return receipt, xiangwanadmin.ErrTargetNotFound
	}
	if err != nil {
		return receipt, fmt.Errorf("lock Coupon correction marker: %w", err)
	}
	var currentVersion int64
	var firstActor uuid.UUID
	err = write.QueryRowContext(ctx, `
SELECT event_sequence, actor_id
FROM xiangwan_coupon_correction_events
WHERE tenant_id = $1 AND correction_entry_id = $2
ORDER BY event_sequence DESC LIMIT 1
`, catalog.tenantID, command.CorrectionEntryID).Scan(&currentVersion, &firstActor)
	if errors.Is(err, sql.ErrNoRows) {
		currentVersion = 0
	} else if err != nil {
		return receipt, fmt.Errorf("read Coupon correction handling: %w", err)
	}
	if currentVersion != command.ExpectedVersion {
		return receipt, xiangwanadmin.ErrVersionConflict
	}
	if command.Action == xiangwanadmin.CouponCorrectionResolve {
		if firstActor == command.ActorID {
			return receipt, xiangwanadmin.ErrScopeForbidden
		}
		var latestState string
		var redeemed bool
		err = write.QueryRowContext(ctx, `
SELECT latest.entry_type,
       EXISTS (SELECT 1 FROM xiangwan_coupon_entries AS used
               WHERE used.tenant_id = $1 AND used.coupon_id = $2
                 AND used.entry_type = 'redeemed')
FROM xiangwan_coupon_entries AS latest
WHERE latest.tenant_id = $1 AND latest.coupon_id = $2
  AND latest.entry_type <> 'correction_required'
ORDER BY latest.entry_sequence DESC LIMIT 1
`, catalog.tenantID, couponID).Scan(&latestState, &redeemed)
		if err != nil {
			return receipt, fmt.Errorf("read Coupon financial history: %w", err)
		}
		if latestState == "held" || (command.EvidenceKind == "financial" &&
			(!redeemed || command.AdjustmentCents != faceValue)) ||
			(command.EvidenceKind == "entitlement" &&
				(redeemed || latestState != "invalidated" || command.AdjustmentCents != 0)) {
			return receipt, xiangwanadmin.ErrVersionConflict
		}
	}
	now := catalog.now().UTC().Truncate(time.Microsecond)
	status := "processing"
	sequence := int64(1)
	if command.Action == xiangwanadmin.CouponCorrectionResolve {
		status = "resolved"
		sequence = 2
	}
	var evidenceKind, evidenceReference any
	if command.EvidenceKind != "" {
		evidenceKind = command.EvidenceKind
		evidenceReference = command.EvidenceReference
	}
	_, err = write.ExecContext(ctx, `
INSERT INTO xiangwan_coupon_correction_events (
    id, tenant_id, correction_entry_id, coupon_id, actor_id, operation_id,
    event_sequence, event_type, evidence_kind, evidence_reference,
    adjustment_cents, operator_note, recorded_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
`, uuid.New(), catalog.tenantID, command.CorrectionEntryID, couponID,
		command.ActorID, command.OperationID, sequence, statusToCorrectionEvent(status),
		evidenceKind, evidenceReference, command.AdjustmentCents,
		command.OperatorNote, now)
	if err != nil {
		return receipt, fmt.Errorf("append Coupon correction handling: %w", err)
	}
	receipt = xiangwanadmin.CouponCorrectionActionResult{
		CorrectionEntryID: command.CorrectionEntryID, CouponID: couponID,
		Status: status, Version: sequence, EvidenceKind: command.EvidenceKind,
		AdjustmentCents: command.AdjustmentCents, RecordedAt: now,
	}
	if err := writeOperationAndAudit(ctx, write, catalog.tenantID,
		command.ActorID, command.IdentityLinkID, command.OperationID,
		operationKind, digest, operationResult[xiangwanadmin.CouponCorrectionActionResult]{Value: receipt},
		command.CorrectionEntryID, sequence, command.RequestID, now); err != nil {
		return xiangwanadmin.CouponCorrectionActionResult{}, err
	}
	if err := write.Commit(); err != nil {
		return xiangwanadmin.CouponCorrectionActionResult{},
			fmt.Errorf("commit Coupon correction handling: %w", err)
	}
	return receipt, nil
}

func statusToCorrectionEvent(status string) string {
	if status == "processing" {
		return "started"
	}
	return "resolved"
}

func (catalog *Catalog) beginCouponCorrectionWrite(
	ctx context.Context, command xiangwanadmin.CouponCorrectionActionCommand,
) (*activityWriteTransaction, error) {
	conn, release, err := catalog.lockAdminOperation(ctx,
		command.ActorID, command.OperationID)
	if err != nil {
		return nil, err
	}
	tx, err := conn.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, errors.Join(err, release())
	}
	write := &activityWriteTransaction{Tx: tx, release: release}
	var writeEpoch int64
	err = tx.QueryRowContext(ctx, `
SELECT write_epoch FROM xiangwan_runtime_generations
WHERE singleton_id = 1 AND scope_key = 'wq-xiangwan'
  AND tenant_id = $1 AND active_generation_id = $2
  AND write_epoch > 0 AND bootstrap_completed_at IS NOT NULL
FOR SHARE
`, catalog.tenantID, catalog.generationID).Scan(&writeEpoch)
	if errors.Is(err, sql.ErrNoRows) {
		_ = write.Rollback()
		return nil, xiangwanadmin.ErrVersionConflict
	}
	if err != nil {
		_ = write.Rollback()
		return nil, fmt.Errorf("lock Coupon correction generation: %w", err)
	}
	if err := catalog.authorizer.require(ctx, tx, command.ActorID,
		&command.IdentityLinkID, "super_admin", nil); err != nil {
		_ = write.Rollback()
		return nil, err
	}
	return write, nil
}
