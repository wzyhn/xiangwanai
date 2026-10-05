package activitypostgres

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon"
	couponpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon/postgres"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/refund"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
	"github.com/google/uuid"
)

type sessionCancellationPlan struct {
	snapshot activity.SessionCancellationSnapshot
	items    []sessionCancellationPlanItem
}

type sessionCancellationPlanItem struct {
	registration registration.Registration
	order        *payment.Order
	hold         *payment.CapacityHold
	refund       *refund.Case
	coupon       *couponpostgres.Ledger
}

type activityCancellationCause struct {
	registrationReason string
	refundReason       refund.ReasonCode
	refundKeyPrefix    string
	operatorNotePrefix string
}

var (
	sessionCancellationCause = activityCancellationCause{
		registrationReason: "session_cancelled",
		refundReason:       refund.ReasonSessionCancelled,
		refundKeyPrefix:    "refund:session-cancel:",
		operatorNotePrefix: "session_cancellation_key=",
	}
	instanceCancellationCause = activityCancellationCause{
		registrationReason: "instance_cancelled",
		refundReason:       refund.ReasonInstanceCancelled,
		refundKeyPrefix:    "refund:instance-cancel:",
		operatorNotePrefix: "instance_cancellation_key=",
	}
)

func buildSessionCancellationPlan(
	ctx context.Context,
	tx sessionCancellationTransaction,
	session activity.Session,
	seriesID uuid.UUID,
	registrations []registration.Registration,
	cause activityCancellationCause,
) (sessionCancellationPlan, error) {
	plan := sessionCancellationPlan{
		items: make([]sessionCancellationPlanItem, 0, len(registrations)),
	}
	for _, current := range registrations {
		if !sessionCancellationRegistrationMatches(session, current) ||
			current.SeriesID != seriesID {
			return sessionCancellationPlan{}, ErrSessionCancellationTransaction
		}
		item := sessionCancellationPlanItem{registration: current}
		order, hasOrder, err := tx.lockOrderByRegistration(
			ctx,
			current.TenantID,
			current.ID,
		)
		if err != nil {
			return sessionCancellationPlan{}, err
		}
		if !hasOrder {
			if current.ParticipationStatus != registration.ParticipationStatusConfirmed {
				return sessionCancellationPlan{}, ErrSessionCancellationTransaction
			}
			plan.items = append(plan.items, item)
			continue
		}

		hold, err := tx.lockHoldByOrder(ctx, order.TenantID, order.ID)
		if err != nil {
			return sessionCancellationPlan{}, err
		}
		if !sessionCancellationPaymentMatches(current, order, hold) {
			return sessionCancellationPlan{}, ErrSessionCancellationTransaction
		}
		item.order = &order
		item.hold = &hold

		if current.ParticipationStatus == registration.ParticipationStatusConfirmed {
			if hold.HoldStatus != payment.CapacityHoldStatusConverted {
				return sessionCancellationPlan{}, ErrSessionCancellationTransaction
			}
			switch order.PaymentStatus {
			case payment.OrderStatusPaidConfirmed:
				if order.ActualPaidCents == nil || *order.ActualPaidCents <= 0 {
					return sessionCancellationPlan{}, ErrSessionCancellationConflict
				}
				existing, refundErr := tx.lockRefundByOrder(
					ctx,
					order.TenantID,
					order.ID,
				)
				if refundErr == nil {
					if !sessionCancellationRefundMatches(
						existing,
						current,
						order,
						cause.refundReason,
					) {
						return sessionCancellationPlan{},
							ErrSessionCancellationConflict
					}
					item.refund = &existing
				} else if !errors.Is(
					refundErr,
					errSessionCancellationRefundNotFound,
				) {
					return sessionCancellationPlan{}, refundErr
				}
			case payment.OrderStatusSettledZero:
				if order.ActualPaidCents != nil || order.PayableCents != 0 ||
					order.DiscountCents <= 0 ||
					order.DiscountCents != order.OriginalPriceCents {
					return sessionCancellationPlan{}, ErrSessionCancellationTransaction
				}
				ledger, couponErr := tx.lockCouponByOrder(
					ctx,
					order.TenantID,
					order.ID,
				)
				if couponErr != nil {
					return sessionCancellationPlan{}, couponErr
				}
				state, couponErr := coupon.InspectRefundPolicyOrder(
					ledger.Instrument,
					ledger.Entries,
					order.ID,
					current.ID,
				)
				if couponErr != nil || state.Adjustment != nil ||
					ledger.Instrument.TenantID != order.TenantID ||
					ledger.Instrument.PrincipalID != order.PrincipalID {
					return sessionCancellationPlan{}, ErrSessionCancellationTransaction
				}
				item.coupon = &ledger
			default:
				return sessionCancellationPlan{}, ErrSessionCancellationTransaction
			}
		} else if current.ParticipationStatus !=
			registration.ParticipationStatusPendingPayment {
			return sessionCancellationPlan{}, ErrSessionCancellationTransaction
		}
		plan.items = append(plan.items, item)
	}

	plan.snapshot = sessionCancellationSnapshot(session, seriesID, plan.items, cause)
	if _, _, err := activity.AssessSessionCancellation(plan.snapshot); err != nil {
		return sessionCancellationPlan{}, fmt.Errorf(
			"%w: %v",
			ErrSessionCancellationTransaction,
			err,
		)
	}
	return plan, nil
}

func applySessionCancellationPlan(
	ctx context.Context,
	tx sessionCancellationTransaction,
	command SessionCancellationCommand,
	plan sessionCancellationPlan,
	processedAt time.Time,
	cause activityCancellationCause,
	couponPolicy coupon.RefundPolicyEvaluator,
) (activity.SessionCancellationImpact, error) {
	impact := activity.SessionCancellationImpact{
		CancelledRegistrationCount: len(plan.items),
	}
	for _, item := range plan.items {
		current := item.registration
		cancelled, changed, err := registration.CancelRegistration(
			current,
			cause.registrationReason,
			processedAt,
		)
		if err != nil || !changed {
			return activity.SessionCancellationImpact{}, fmt.Errorf(
				"%w: cancel Registration: %v",
				ErrSessionCancellationTransaction,
				err,
			)
		}

		applyCouponPolicy := false
		if item.order == nil {
			impact.ReleasedConfirmedCount++
		} else {
			order := *item.order
			hold := *item.hold
			switch current.ParticipationStatus {
			case registration.ParticipationStatusConfirmed:
				impact.ReleasedConfirmedCount++
				switch order.PaymentStatus {
				case payment.OrderStatusPaidConfirmed:
					refundCase, refundErr := ensureSessionCancellationPlanRefund(
						ctx,
						tx,
						command,
						current,
						order,
						item.refund,
						processedAt,
						cause,
					)
					if refundErr != nil {
						return activity.SessionCancellationImpact{}, refundErr
					}
					impact.RefundCaseCount++
					if refundCase.RequestedRefundCents >
						math.MaxInt64-impact.RequestedRefundCents {
						return activity.SessionCancellationImpact{},
							ErrSessionCancellationTransaction
					}
					impact.RequestedRefundCents += refundCase.RequestedRefundCents
				case payment.OrderStatusSettledZero:
					applyCouponPolicy = true
				default:
					return activity.SessionCancellationImpact{},
						ErrSessionCancellationTransaction
				}
			case registration.ParticipationStatusPendingPayment:
				finishedHold, _, finishErr := finishSessionCancellationHold(
					hold,
					processedAt,
				)
				if finishErr != nil {
					return activity.SessionCancellationImpact{}, fmt.Errorf(
						"%w: finish capacity hold: %v",
						ErrSessionCancellationTransaction,
						finishErr,
					)
				}
				if _, err := tx.updateHold(
					ctx,
					finishedHold,
					hold.Version,
				); err != nil {
					return activity.SessionCancellationImpact{},
						classifySessionCancellationWriteError(err)
				}
				impact.ReleasedHoldCount++
				if order.PaymentStatus == payment.OrderStatusPending {
					closedOrder, orderChanged, closeErr := payment.CloseOrderUnpaid(
						order,
						processedAt,
					)
					if closeErr != nil || !orderChanged {
						return activity.SessionCancellationImpact{}, fmt.Errorf(
							"%w: close pending Order: %v",
							ErrSessionCancellationTransaction,
							closeErr,
						)
					}
					if _, err := tx.updateOrder(
						ctx,
						closedOrder,
						order.Version,
					); err != nil {
						return activity.SessionCancellationImpact{},
							classifySessionCancellationWriteError(err)
					}
					impact.ClosedPendingOrderCount++
				}
				if order.DiscountCents > 0 {
					if err := tx.releaseCouponForOrder(
						ctx,
						order.TenantID,
						order.ID,
						cause.registrationReason,
						processedAt,
					); err != nil {
						return activity.SessionCancellationImpact{}, err
					}
				}
			default:
				return activity.SessionCancellationImpact{},
					ErrSessionCancellationTransaction
			}
		}
		if _, err := tx.updateRegistration(
			ctx,
			cancelled,
			current.Version,
		); err != nil {
			return activity.SessionCancellationImpact{},
				classifySessionCancellationWriteError(err)
		}
		if applyCouponPolicy {
			if item.order == nil || item.coupon == nil {
				return activity.SessionCancellationImpact{},
					ErrSessionCancellationTransaction
			}
			if err := appendSessionCancellationCouponAdjustment(
				ctx,
				tx,
				command.ActorID,
				*item.order,
				*item.coupon,
				processedAt,
				cause,
				couponPolicy,
			); err != nil {
				return activity.SessionCancellationImpact{}, err
			}
			impact.CouponAdjustmentCount++
		}
	}
	return impact, nil
}

func ensureSessionCancellationPlanRefund(
	ctx context.Context,
	tx sessionCancellationTransaction,
	command SessionCancellationCommand,
	current registration.Registration,
	order payment.Order,
	existing *refund.Case,
	processedAt time.Time,
	cause activityCancellationCause,
) (refund.Case, error) {
	if existing != nil {
		return *existing, nil
	}
	if order.ActualPaidCents == nil {
		return refund.Case{}, ErrSessionCancellationTransaction
	}
	created, err := refund.NewCase(refund.NewCaseCommand{
		TenantID:             order.TenantID,
		OrderID:              order.ID,
		RegistrationID:       current.ID,
		SeriesID:             current.SeriesID,
		InstanceID:           current.InstanceID,
		SessionID:            current.SessionID,
		PrincipalID:          current.PrincipalID,
		ReasonCode:           cause.refundReason,
		IdempotencyKey:       cause.refundKeyPrefix + order.ID.String(),
		ActualPaidCents:      *order.ActualPaidCents,
		RequestedRefundCents: *order.ActualPaidCents,
		OperatorNote: cause.operatorNotePrefix +
			command.IdempotencyKey,
		Now: processedAt,
	})
	if err != nil {
		return refund.Case{}, fmt.Errorf(
			"%w: construct Refund case: %v",
			ErrSessionCancellationTransaction,
			err,
		)
	}
	created, err = tx.createRefund(ctx, created)
	if err != nil {
		return refund.Case{}, classifySessionCancellationWriteError(err)
	}
	return created, nil
}

func appendSessionCancellationCouponAdjustment(
	ctx context.Context,
	tx sessionCancellationTransaction,
	actorID uuid.UUID,
	order payment.Order,
	ledger couponpostgres.Ledger,
	processedAt time.Time,
	cause activityCancellationCause,
	policy coupon.RefundPolicyEvaluator,
) error {
	if policy == nil {
		return ErrSessionCancellationCouponPolicy
	}
	state, err := coupon.InspectRefundPolicyOrder(
		ledger.Instrument,
		ledger.Entries,
		order.ID,
		order.RegistrationID,
	)
	if err != nil || state.Adjustment != nil {
		return ErrSessionCancellationTransaction
	}
	input := coupon.RefundPolicyInput{
		TenantID:       order.TenantID,
		CouponID:       ledger.Instrument.ID,
		OrderID:        order.ID,
		RegistrationID: order.RegistrationID,
		Trigger:        coupon.RefundTriggerSettledZeroCancellation,
		Reason:         cause.registrationReason + " zero-settled Coupon",
		EvaluatedAt:    processedAt,
	}
	decision, err := policy.EvaluateCouponRefund(ctx, input)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrSessionCancellationCouponPolicy, err)
	}
	if err := coupon.ValidateRefundPolicyDecision(decision); err != nil {
		return fmt.Errorf("%w: %v", ErrSessionCancellationCouponPolicy, err)
	}
	adjustment, err := coupon.ApplyRefundPolicy(coupon.ApplyRefundPolicyCommand{
		Instrument: ledger.Instrument,
		History:    ledger.Entries,
		Input:      input,
		Decision:   decision,
		ActorID:    actorID,
		RecordedAt: processedAt,
	})
	if err != nil {
		return fmt.Errorf(
			"%w: construct Coupon adjustment: %v",
			ErrSessionCancellationTransaction,
			err,
		)
	}
	if _, err := tx.appendCouponEntry(ctx, adjustment); err != nil {
		return classifySessionCancellationWriteError(err)
	}
	return nil
}

func sessionCancellationSnapshot(
	session activity.Session,
	seriesID uuid.UUID,
	items []sessionCancellationPlanItem,
	cause activityCancellationCause,
) activity.SessionCancellationSnapshot {
	values := make(
		[]activity.SessionCancellationRegistrationSnapshot,
		0,
		len(items),
	)
	for _, item := range items {
		current := item.registration
		value := activity.SessionCancellationRegistrationSnapshot{
			RegistrationID:      current.ID,
			ParticipationStatus: string(current.ParticipationStatus),
			Version:             current.Version,
			UpdatedAt:           current.UpdatedAt,
		}
		if item.order != nil && item.hold != nil {
			order := item.order
			hold := item.hold
			orderValue := activity.SessionCancellationOrderSnapshot{
				OrderID:            order.ID,
				PaymentStatus:      string(order.PaymentStatus),
				OriginalPriceCents: order.OriginalPriceCents,
				DiscountCents:      order.DiscountCents,
				PayableCents:       order.PayableCents,
				ActualPaidCents:    cloneInt64Pointer(order.ActualPaidCents),
				Version:            order.Version,
				UpdatedAt:          order.UpdatedAt,
				Hold: activity.SessionCancellationHoldSnapshot{
					HoldID:     hold.ID,
					HoldStatus: string(hold.HoldStatus),
					ExpiresAt:  hold.ExpiresAt,
					Version:    hold.Version,
					UpdatedAt:  hold.UpdatedAt,
				},
			}
			if item.refund != nil {
				refundCase := item.refund
				orderValue.Refund = &activity.SessionCancellationRefundSnapshot{
					RefundCaseID:          refundCase.ID,
					RefundStatus:          string(refundCase.RefundStatus),
					ReasonCode:            string(refundCase.ReasonCode),
					RequestedRefundCents:  refundCase.RequestedRefundCents,
					SuccessfulRefundCents: refundCase.SuccessfulRefundCents,
					Version:               refundCase.Version,
					UpdatedAt:             refundCase.UpdatedAt,
				}
			}
			if item.coupon != nil {
				state, err := coupon.InspectRefundPolicyOrder(
					item.coupon.Instrument,
					item.coupon.Entries,
					order.ID,
					current.ID,
				)
				if err == nil && state.Adjustment == nil &&
					len(item.coupon.Entries) > 0 {
					latest := item.coupon.Entries[len(item.coupon.Entries)-1]
					orderValue.Coupon =
						&activity.SessionCancellationCouponSnapshot{
							CouponID:            item.coupon.Instrument.ID,
							RedemptionEntryID:   state.Redemption.ID,
							LedgerEntrySequence: latest.EntrySequence,
						}
				}
			}
			value.Order = &orderValue
		}
		values = append(values, value)
	}
	return activity.SessionCancellationSnapshot{
		TenantID:                   session.TenantID,
		SeriesID:                   seriesID,
		InstanceID:                 session.InstanceID,
		SessionID:                  session.ID,
		SessionStatus:              session.Status,
		SessionVersion:             session.Version,
		SessionUpdatedAt:           session.UpdatedAt,
		RefundReasonCode:           string(cause.refundReason),
		ConfirmedRegistrationCount: session.ConfirmedRegistrationCount,
		ActiveHoldCount:            session.ActiveHoldCount,
		Registrations:              values,
	}
}

func cloneInt64Pointer(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
