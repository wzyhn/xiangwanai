package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon"
	refundpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/refund/postgres"
	"github.com/google/uuid"
)

type RefundOperator struct {
	tenantID  uuid.UUID
	processor *refundpostgres.Processor
	now       func() time.Time
}

func NewRefundOperator(
	db *sql.DB,
	tenantID, generationID uuid.UUID,
	authorizer *GrantAuthorizer,
	policy coupon.RefundPolicyEvaluator,
) (*RefundOperator, error) {
	if db == nil || tenantID == uuid.Nil || generationID == uuid.Nil || authorizer == nil {
		return nil, xiangwanadmin.ErrInvalidAdminConfiguration
	}
	authorize := func(
		ctx context.Context, tx *sql.Tx, actorID, identityLinkID uuid.UUID, action string,
	) error {
		if actorID == uuid.Nil || identityLinkID == uuid.Nil {
			return xiangwanadmin.ErrScopeForbidden
		}
		var writeEpoch int64
		err := tx.QueryRowContext(ctx, `
SELECT write_epoch
FROM xiangwan_runtime_generations
WHERE singleton_id = 1
  AND scope_key = 'wq-xiangwan'
  AND tenant_id = $1
  AND active_generation_id = $2
  AND write_epoch > 0
  AND bootstrap_completed_at IS NOT NULL
FOR SHARE
`, tenantID, generationID).Scan(&writeEpoch)
		if errors.Is(err, sql.ErrNoRows) {
			return xiangwanadmin.ErrVersionConflict
		}
		if err != nil {
			return fmt.Errorf("lock administrator refund generation: %w", err)
		}
		capability := xiangwanadmin.CapabilityFinance
		if action == string(xiangwanadmin.RefundActionComplete) ||
			action == string(xiangwanadmin.RefundActionReject) {
			capability = xiangwanadmin.CapabilitySuperAdmin
		}
		return authorizer.require(ctx, tx, actorID, &identityLinkID, string(capability), nil)
	}
	return &RefundOperator{
		tenantID:  tenantID,
		processor: refundpostgres.NewAuthorizedProcessorWithCouponRefundPolicy(db, policy, authorize),
		now:       time.Now,
	}, nil
}

func (operator *RefundOperator) TransitionRefund(
	ctx context.Context, command xiangwanadmin.RefundActionCommand,
) (xiangwanadmin.RefundActionResult, error) {
	if operator == nil || operator.processor == nil || ctx == nil ||
		command.ActorID == uuid.Nil || command.IdentityLinkID == uuid.Nil ||
		command.OperationID == uuid.Nil || command.CaseID == uuid.Nil ||
		command.ExpectedVersion < 1 ||
		len([]rune(command.OperatorNote)) > 1000 {
		return xiangwanadmin.RefundActionResult{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	command.ExternalRefundID = strings.TrimSpace(command.ExternalRefundID)
	command.EvidenceReference = strings.TrimSpace(command.EvidenceReference)
	command.FailureReason = strings.TrimSpace(command.FailureReason)
	key := command.OperationID.String()
	now := operator.now().UTC().Truncate(time.Microsecond)
	var result refundpostgres.RefundOperationResult
	var err error
	switch command.Action {
	case xiangwanadmin.RefundActionStart:
		if command.SuccessfulRefundCents != 0 || command.ExternalRefundID != "" ||
			command.EvidenceReference != "" || command.FailureReason != "" {
			return xiangwanadmin.RefundActionResult{}, xiangwanadmin.ErrInvalidCatalogRequest
		}
		result, err = operator.processor.StartProcessing(ctx, refundpostgres.StartProcessingCommand{
			TenantID: operator.tenantID, RefundCaseID: command.CaseID,
			ActorID: command.ActorID, IdentityLinkID: command.IdentityLinkID,
			ExpectedVersion: command.ExpectedVersion, ReplayIgnoresOccurredAt: true,
			IdempotencyKey: key, OperatorNote: command.OperatorNote, OccurredAt: now,
		})
	case xiangwanadmin.RefundActionComplete:
		if command.SuccessfulRefundCents <= 0 || command.ExternalRefundID == "" ||
			command.EvidenceReference == "" || command.FailureReason != "" {
			return xiangwanadmin.RefundActionResult{}, xiangwanadmin.ErrInvalidCatalogRequest
		}
		result, err = operator.processor.Complete(ctx, refundpostgres.CompleteRefundCommand{
			TenantID: operator.tenantID, RefundCaseID: command.CaseID,
			ActorID: command.ActorID, IdentityLinkID: command.IdentityLinkID,
			ExpectedVersion: command.ExpectedVersion, RequireDistinctPriorHandler: true,
			ReplayIgnoresOccurredAt: true, IdempotencyKey: key,
			SuccessfulRefundCents: command.SuccessfulRefundCents,
			ExternalRefundID:      command.ExternalRefundID, EvidenceReference: command.EvidenceReference,
			OperatorNote: command.OperatorNote, OccurredAt: now,
		})
	case xiangwanadmin.RefundActionFail, xiangwanadmin.RefundActionReject:
		if command.SuccessfulRefundCents != 0 || command.ExternalRefundID != "" ||
			command.EvidenceReference != "" || command.FailureReason == "" {
			return xiangwanadmin.RefundActionResult{}, xiangwanadmin.ErrInvalidCatalogRequest
		}
		input := refundpostgres.ResolveRefundCommand{
			TenantID: operator.tenantID, RefundCaseID: command.CaseID,
			ActorID: command.ActorID, IdentityLinkID: command.IdentityLinkID,
			ExpectedVersion: command.ExpectedVersion, ReplayIgnoresOccurredAt: true,
			RequireProcessing: command.Action == xiangwanadmin.RefundActionFail,
			IdempotencyKey:    key, FailureReason: command.FailureReason,
			OperatorNote: command.OperatorNote, OccurredAt: now,
		}
		if command.Action == xiangwanadmin.RefundActionFail {
			result, err = operator.processor.Fail(ctx, input)
		} else {
			result, err = operator.processor.Reject(ctx, input)
		}
	default:
		return xiangwanadmin.RefundActionResult{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	if err != nil {
		switch {
		case errors.Is(err, refundpostgres.ErrInvalidRefundOperationCommand):
			return xiangwanadmin.RefundActionResult{}, xiangwanadmin.ErrInvalidCatalogRequest
		case errors.Is(err, refundpostgres.ErrRefundOperationNotFound):
			return xiangwanadmin.RefundActionResult{}, xiangwanadmin.ErrTargetNotFound
		case errors.Is(err, refundpostgres.ErrRefundOperationConflict),
			errors.Is(err, refundpostgres.ErrRefundOperationTransaction),
			errors.Is(err, refundpostgres.ErrCouponRefundPolicyUnavailable):
			return xiangwanadmin.RefundActionResult{}, xiangwanadmin.ErrVersionConflict
		default:
			return xiangwanadmin.RefundActionResult{}, err
		}
	}
	return xiangwanadmin.RefundActionResult{
		CaseID: result.Case.ID, Status: result.Case.RefundStatus,
		Version: result.Case.Version, SuccessfulRefundCents: result.Case.SuccessfulRefundCents,
		EventSequence: result.Event.EventSequence,
	}, nil
}
