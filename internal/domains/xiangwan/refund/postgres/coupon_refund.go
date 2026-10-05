package refundpostgres

import (
	"fmt"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon"
	couponpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon/postgres"
	"github.com/google/uuid"
)

func couponRefundAdjustmentState(
	ledger couponpostgres.Ledger,
	orderID uuid.UUID,
	fullCashRefund bool,
) (bool, *coupon.Entry, error) {
	var redeemed *coupon.Entry
	released := false
	for index := range ledger.Entries {
		entry := ledger.Entries[index]
		if entry.OrderID == nil || *entry.OrderID != orderID {
			continue
		}
		switch entry.EntryType {
		case coupon.EntryTypeReleased:
			released = true
		case coupon.EntryTypeRedeemed:
			cloned := entry
			redeemed = &cloned
		case coupon.EntryTypeRestored, coupon.EntryTypeForfeited:
			if redeemed == nil || entry.RelatedEntryID == nil ||
				*entry.RelatedEntryID != redeemed.ID {
				return false, nil, ErrRefundOperationTransaction
			}
			cloned := entry
			return fullCashRefund, &cloned, nil
		}
	}
	if redeemed != nil && released {
		return false, nil, ErrRefundOperationTransaction
	}
	if redeemed == nil {
		if released {
			return false, nil, nil
		}
		return false, nil, fmt.Errorf(
			"%w: discounted Order lacks Coupon close fact",
			ErrRefundOperationTransaction,
		)
	}
	return fullCashRefund, nil, nil
}

func cloneCouponEntry(value *coupon.Entry) *coupon.Entry {
	if value == nil {
		return nil
	}
	cloned := *value
	cloned.OrderID = cloneCouponUUID(value.OrderID)
	cloned.RegistrationID = cloneCouponUUID(value.RegistrationID)
	cloned.RelatedEntryID = cloneCouponUUID(value.RelatedEntryID)
	cloned.RefundCaseID = cloneCouponUUID(value.RefundCaseID)
	cloned.SourceCheckinEventID = cloneCouponUUID(value.SourceCheckinEventID)
	cloned.ActorID = cloneCouponUUID(value.ActorID)
	cloned.Reason = cloneCouponString(value.Reason)
	cloned.RefundPolicyVersion = cloneCouponString(value.RefundPolicyVersion)
	return &cloned
}

func cloneCouponUUID(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneCouponString(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
