package paymentpostgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/refund"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
	"github.com/google/uuid"
)

var errPaymentRefundCaseNotFound = errors.New("xiangwan payment Refund case not found")

func ensurePaymentRefundCase(
	ctx context.Context,
	tx paidRegistrationTransaction,
	result PaymentConfirmationResult,
	reason refund.ReasonCode,
	processedAt time.Time,
) (refund.Case, error) {
	existing, err := getPaymentRefundCaseByOrder(
		ctx,
		tx,
		result.Order.TenantID,
		result.Order.ID,
	)
	if err == nil {
		if !paymentRefundCaseMatches(existing, result) {
			return refund.Case{}, fmt.Errorf(
				"%w: Refund identity or amount mismatch",
				ErrPaymentConfirmationTransaction,
			)
		}
		return existing, nil
	}
	if !errors.Is(err, errPaymentRefundCaseNotFound) {
		return refund.Case{}, err
	}
	if result.Order.ActualPaidCents == nil {
		return refund.Case{}, fmt.Errorf(
			"%w: paid Order has no actual amount",
			ErrPaymentConfirmationTransaction,
		)
	}

	created, err := refund.NewCase(refund.NewCaseCommand{
		TenantID:             result.Order.TenantID,
		OrderID:              result.Order.ID,
		RegistrationID:       result.Order.RegistrationID,
		SeriesID:             result.Order.SeriesID,
		InstanceID:           result.Order.InstanceID,
		SessionID:            result.Order.SessionID,
		PrincipalID:          result.Order.PrincipalID,
		ReasonCode:           reason,
		IdempotencyKey:       "refund:payment:" + result.Order.ID.String(),
		ActualPaidCents:      *result.Order.ActualPaidCents,
		RequestedRefundCents: *result.Order.ActualPaidCents,
		Now:                  processedAt,
	})
	if err != nil {
		return refund.Case{}, fmt.Errorf(
			"%w: construct Refund case: %v",
			ErrPaymentConfirmationTransaction,
			err,
		)
	}
	created, err = createPaymentRefundCase(ctx, tx, created)
	if err != nil {
		return refund.Case{}, err
	}
	return created, nil
}

func paymentRefundCaseMatches(value refund.Case, result PaymentConfirmationResult) bool {
	return result.Order.ActualPaidCents != nil &&
		value.TenantID == result.Order.TenantID &&
		value.OrderID == result.Order.ID &&
		value.RegistrationID == result.Order.RegistrationID &&
		value.SeriesID == result.Order.SeriesID &&
		value.InstanceID == result.Order.InstanceID &&
		value.SessionID == result.Order.SessionID &&
		value.PrincipalID == result.Order.PrincipalID &&
		value.RequestedRefundCents == *result.Order.ActualPaidCents
}

func paymentConfirmationRefundReason(
	currentOrder payment.Order,
	currentHold payment.CapacityHold,
	currentRegistration registration.Registration,
	seriesStatus activity.SeriesStatus,
	instanceStatus activity.InstanceStatus,
	sessionStatus activity.SessionStatus,
	paidAt time.Time,
) refund.ReasonCode {
	if currentRegistration.CancellationReason != nil {
		reason := *currentRegistration.CancellationReason
		switch {
		case reason == "user_cancelled" || strings.HasPrefix(reason, "user_cancelled@"):
			return refund.ReasonUserCancelled
		case reason == "session_cancelled":
			return refund.ReasonSessionCancelled
		case reason == "instance_cancelled":
			return refund.ReasonInstanceCancelled
		case strings.HasPrefix(reason, "operator_cancelled:"):
			return refund.ReasonOperatorAdjustment
		}
	}
	if currentHold.HoldStatus == payment.CapacityHoldStatusExpired ||
		!paidAt.Before(currentHold.ExpiresAt) {
		return refund.ReasonHoldExpiredAfterPayment
	}
	if currentOrder.PaymentStatus == payment.OrderStatusClosedUnpaid {
		return refund.ReasonOrderClosedAfterPayment
	}
	if seriesStatus != activity.SeriesStatusActive ||
		instanceStatus != activity.InstanceStatusPublished ||
		sessionStatus != activity.SessionStatusPublished {
		return refund.ReasonSessionUnavailableAfterPayment
	}
	return refund.ReasonOrderClosedAfterPayment
}

func getPaymentRefundCaseByOrder(
	ctx context.Context,
	tx paidRegistrationTransaction,
	tenantID uuid.UUID,
	orderID uuid.UUID,
) (refund.Case, error) {
	value, err := scanPaymentRefundCase(tx.queryRowContext(ctx, `
SELECT
    id, tenant_id, order_id, registration_id, series_id, instance_id,
    session_id, principal_id, refund_status, reason_code, idempotency_key,
    requested_refund_cents, successful_refund_cents,
    processing_started_at, resolved_at, handled_by,
    external_refund_id, evidence_reference, operator_note, failure_reason,
    version, created_at, updated_at
FROM xiangwan_refund_cases
WHERE tenant_id = $1 AND order_id = $2
`, tenantID, orderID))
	if errors.Is(err, sql.ErrNoRows) {
		return refund.Case{}, errPaymentRefundCaseNotFound
	}
	if err != nil {
		return refund.Case{}, fmt.Errorf("get payment Refund case: %w", err)
	}
	return value, nil
}

func createPaymentRefundCase(
	ctx context.Context,
	tx paidRegistrationTransaction,
	value refund.Case,
) (refund.Case, error) {
	created, err := scanPaymentRefundCase(tx.queryRowContext(ctx, `
INSERT INTO xiangwan_refund_cases (
    id, tenant_id, order_id, registration_id, series_id, instance_id,
    session_id, principal_id, refund_status, reason_code, idempotency_key,
    requested_refund_cents, successful_refund_cents,
    processing_started_at, resolved_at, handled_by,
    external_refund_id, evidence_reference, operator_note, failure_reason,
    version, created_at, updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9, $10, $11,
    $12, $13,
    $14, $15, $16,
    $17, $18, $19, $20,
    $21, $22, $23
)
RETURNING
    id, tenant_id, order_id, registration_id, series_id, instance_id,
    session_id, principal_id, refund_status, reason_code, idempotency_key,
    requested_refund_cents, successful_refund_cents,
    processing_started_at, resolved_at, handled_by,
    external_refund_id, evidence_reference, operator_note, failure_reason,
    version, created_at, updated_at
`,
		value.ID,
		value.TenantID,
		value.OrderID,
		value.RegistrationID,
		value.SeriesID,
		value.InstanceID,
		value.SessionID,
		value.PrincipalID,
		value.RefundStatus,
		value.ReasonCode,
		value.IdempotencyKey,
		value.RequestedRefundCents,
		value.SuccessfulRefundCents,
		value.ProcessingStartedAt,
		value.ResolvedAt,
		value.HandledBy,
		value.ExternalRefundID,
		value.EvidenceReference,
		value.OperatorNote,
		value.FailureReason,
		value.Version,
		value.CreatedAt,
		value.UpdatedAt,
	))
	if err != nil {
		return refund.Case{}, fmt.Errorf("create payment Refund case: %w", err)
	}
	return created, nil
}

func scanPaymentRefundCase(row rowScanner) (refund.Case, error) {
	var value refund.Case
	var processingStartedAt sql.NullTime
	var resolvedAt sql.NullTime
	var handledBy uuid.NullUUID
	var externalRefundID sql.NullString
	var evidenceReference sql.NullString
	var operatorNote sql.NullString
	var failureReason sql.NullString
	err := row.Scan(
		&value.ID,
		&value.TenantID,
		&value.OrderID,
		&value.RegistrationID,
		&value.SeriesID,
		&value.InstanceID,
		&value.SessionID,
		&value.PrincipalID,
		&value.RefundStatus,
		&value.ReasonCode,
		&value.IdempotencyKey,
		&value.RequestedRefundCents,
		&value.SuccessfulRefundCents,
		&processingStartedAt,
		&resolvedAt,
		&handledBy,
		&externalRefundID,
		&evidenceReference,
		&operatorNote,
		&failureReason,
		&value.Version,
		&value.CreatedAt,
		&value.UpdatedAt,
	)
	if err != nil {
		return refund.Case{}, err
	}
	value.ProcessingStartedAt = nullTimePointer(processingStartedAt)
	value.ResolvedAt = nullTimePointer(resolvedAt)
	value.HandledBy = paymentNullUUIDPointer(handledBy)
	value.ExternalRefundID = nullStringPointer(externalRefundID)
	value.EvidenceReference = nullStringPointer(evidenceReference)
	value.OperatorNote = nullStringPointer(operatorNote)
	value.FailureReason = nullStringPointer(failureReason)
	return value, nil
}

func paymentNullUUIDPointer(value uuid.NullUUID) *uuid.UUID {
	if !value.Valid {
		return nil
	}
	return &value.UUID
}
