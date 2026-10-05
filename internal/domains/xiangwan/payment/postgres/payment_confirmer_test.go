package paymentpostgres

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/refund"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestPaymentConfirmerMerchantGenerationLegacyDrainBoundary(t *testing.T) {
	t.Parallel()

	legacyOrder := payment.Order{}
	legacy := &PaymentConfirmer{}
	if err := legacy.requireMerchantConfigForOrder(
		context.Background(),
		nil,
		legacyOrder,
		true,
	); err != nil {
		t.Fatalf("legacy close confirmer rejected NULL generation: %v", err)
	}
	strict := &PaymentConfirmer{strictMerchantConfig: true}
	if err := strict.requireMerchantConfigForOrder(
		context.Background(),
		nil,
		legacyOrder,
		true,
	); !errors.Is(err, ErrPaymentMerchantConfigUnavailable) {
		t.Fatalf("strict API confirmer error = %v, want merchant config unavailable", err)
	}
}

func TestPaymentConfirmerConfirmsParticipationAndConvertsCapacity(t *testing.T) {
	t.Parallel()

	current, command, processedAt := pendingPaymentConfirmationFixture(t)
	confirmer, tx, capture := newPaymentConfirmerHarness(
		t,
		current,
		command,
		processedAt,
		paymentConfirmerScenario{},
	)

	result, err := confirmer.Confirm(context.Background(), command)
	if err != nil {
		t.Fatalf("Confirm() error = %v", err)
	}
	if !tx.committed || tx.rolledBack {
		t.Fatalf("transaction committed=%t rolledBack=%t", tx.committed, tx.rolledBack)
	}
	if capture.isolation != sql.LevelSerializable {
		t.Fatalf("isolation = %v, want serializable", capture.isolation)
	}
	if !reflect.DeepEqual(capture.lockOrder, []string{
		"series", "instance", "session", "registration", "order", "hold",
	}) {
		t.Fatalf("lock order = %v", capture.lockOrder)
	}
	if result.Disposition != PaymentDispositionParticipationConfirmed ||
		result.Refund != nil ||
		result.Order.PaymentStatus != payment.OrderStatusPaidConfirmed ||
		result.Registration.ParticipationStatus != registration.ParticipationStatusConfirmed ||
		result.Hold.HoldStatus != payment.CapacityHoldStatusConverted {
		t.Fatalf("Confirm() = %+v", result)
	}
	if capture.orderUpdates != 1 ||
		capture.holdUpdates != 1 ||
		capture.registrationUpdates != 1 ||
		capture.capacityConversions != 1 ||
		capture.capacityReleases != 0 ||
		capture.historyIncrements != 1 {
		t.Fatalf("write counts = %+v", capture)
	}
}

func TestPaymentConfirmerPersistsTrustedQueryObservationAtomically(t *testing.T) {
	t.Parallel()

	current, command, processedAt := pendingPaymentConfirmationFixture(t)
	observation := paymentQuerySuccessObservation(
		t,
		current.Payment.Order,
		command,
		processedAt,
	)
	command.Observation = &observation
	confirmer, tx, capture := newPaymentConfirmerHarness(
		t,
		current,
		command,
		processedAt,
		paymentConfirmerScenario{},
	)
	result, err := confirmer.ConfirmTrustedPayment(
		context.Background(),
		payment.TrustedPaymentConfirmation{
			TenantID:            command.TenantID,
			PaymentAppID:        command.PaymentAppID,
			PaymentMerchantID:   command.PaymentMerchantID,
			MerchantOrderNo:     command.MerchantOrderNo,
			WeChatTransactionID: command.WeChatTransactionID,
			ActualPaidCents:     command.ActualPaidCents,
			PaidAt:              command.PaidAt,
			Observation:         observation,
		},
	)
	if err != nil || !tx.committed || capture.observationInserts != 1 ||
		result.Disposition != payment.PaymentConfirmationDispositionParticipationConfirmed ||
		!reflect.DeepEqual(capture.lockOrder, []string{
			"series", "instance", "session", "registration", "order", "hold", "observation",
		}) {
		t.Fatalf(
			"ConfirmTrustedPayment() result=%+v err=%v tx=%+v capture=%+v",
			result,
			err,
			tx,
			capture,
		)
	}
}

func TestPaymentConfirmerClosesTrustedTerminalQueryAndReleasesCapacity(t *testing.T) {
	t.Parallel()

	current, command, processedAt := pendingPaymentConfirmationFixture(t)
	observation := paymentQueryTerminalObservation(
		t,
		current.Payment.Order,
		payment.ProviderTradeStatePayError,
		processedAt,
	)
	confirmer, tx, capture := newPaymentConfirmerHarness(
		t,
		current,
		command,
		processedAt,
		paymentConfirmerScenario{},
	)
	result, err := confirmer.CloseTrustedUnpaidPayment(
		context.Background(),
		payment.TrustedUnpaidPaymentClosure{Observation: observation},
	)
	if err != nil || !tx.committed || tx.rolledBack ||
		result.Order.PaymentStatus != payment.OrderStatusClosedUnpaid ||
		result.Disposition != payment.PaymentConfirmationDispositionNone ||
		capture.orderUpdates != 1 || capture.holdUpdates != 1 ||
		capture.registrationUpdates != 1 || capture.capacityReleases != 1 ||
		capture.capacityConversions != 0 || capture.observationInserts != 1 ||
		!reflect.DeepEqual(capture.lockOrder, []string{
			"series", "instance", "session", "registration", "order", "hold", "observation",
		}) {
		t.Fatalf(
			"CloseTrustedUnpaidPayment() result=%+v err=%v tx=%+v capture=%+v",
			result,
			err,
			tx,
			capture,
		)
	}
}

func TestPaymentConfirmerRecordsTerminalQueryAfterConcurrentUnpaidClose(t *testing.T) {
	t.Parallel()

	current, command, processedAt := pendingPaymentConfirmationFixture(t)
	closedAt := processedAt.Add(-30 * time.Second)
	closedOrder, _, err := payment.CloseOrderUnpaid(
		current.Payment.Order,
		closedAt,
	)
	if err != nil {
		t.Fatalf("CloseOrderUnpaid() error = %v", err)
	}
	releasedHold, _, err := payment.ReleaseCapacityHold(
		current.Payment.Hold,
		"payment_hold_expired",
		closedAt,
	)
	if err != nil {
		t.Fatalf("ReleaseCapacityHold() error = %v", err)
	}
	cancelledRegistration, _, err := registration.CancelRegistration(
		current.Registration,
		"payment_hold_expired",
		closedAt,
	)
	if err != nil {
		t.Fatalf("CancelRegistration() error = %v", err)
	}
	current.Payment.Order = closedOrder
	current.Payment.Hold = releasedHold
	current.Registration = cancelledRegistration
	observation := paymentQueryTerminalObservation(
		t,
		closedOrder,
		payment.ProviderTradeStateRevoked,
		processedAt,
	)
	confirmer, tx, capture := newPaymentConfirmerHarness(
		t,
		current,
		command,
		processedAt,
		paymentConfirmerScenario{},
	)
	result, err := confirmer.CloseTrustedUnpaidPayment(
		context.Background(),
		payment.TrustedUnpaidPaymentClosure{Observation: observation},
	)
	if err != nil || !tx.committed ||
		result.Order.PaymentStatus != payment.OrderStatusClosedUnpaid ||
		capture.observationInserts != 1 || capture.orderUpdates != 0 ||
		capture.holdUpdates != 0 || capture.registrationUpdates != 0 ||
		capture.capacityReleases != 0 {
		t.Fatalf(
			"CloseTrustedUnpaidPayment(reconciled) result=%+v err=%v tx=%+v capture=%+v",
			result,
			err,
			tx,
			capture,
		)
	}
}

func TestPaymentConfirmerBindsTrustedNotificationToResolvedOrderAtomically(t *testing.T) {
	t.Parallel()

	current, command, processedAt := pendingPaymentConfirmationFixture(t)
	confirmer, tx, capture := newPaymentConfirmerHarness(
		t,
		current,
		command,
		processedAt,
		paymentConfirmerScenario{},
	)
	notification := trustedPaymentNotification(command, processedAt)
	result, err := confirmer.ConfirmTrustedPaymentNotification(
		context.Background(),
		notification,
	)
	if err != nil || !tx.committed || capture.observationInserts != 1 ||
		capture.observationSource != payment.TransactionObservationSourcePaymentNotification ||
		capture.observationSourceKey != notification.Notification.NotificationID ||
		capture.observationOrderID != current.Payment.Order.ID ||
		capture.observationPrincipalID != current.Payment.Order.PrincipalID ||
		result.Disposition != payment.PaymentConfirmationDispositionParticipationConfirmed ||
		!reflect.DeepEqual(capture.lockOrder, []string{
			"series", "instance", "session", "registration", "order", "hold", "observation",
		}) {
		t.Fatalf(
			"ConfirmTrustedPaymentNotification() result=%+v err=%v tx=%+v capture=%+v",
			result,
			err,
			tx,
			capture,
		)
	}
}

func TestPaymentConfirmerRollsBackTrustedObservationFailure(t *testing.T) {
	t.Parallel()

	current, command, processedAt := pendingPaymentConfirmationFixture(t)
	observation := paymentQuerySuccessObservation(
		t,
		current.Payment.Order,
		command,
		processedAt,
	)
	command.Observation = &observation
	writeErr := errors.New("observation write failed")
	confirmer, tx, capture := newPaymentConfirmerHarness(
		t,
		current,
		command,
		processedAt,
		paymentConfirmerScenario{observationWriteErr: writeErr},
	)
	_, err := confirmer.Confirm(context.Background(), command)
	if !errors.Is(err, writeErr) || tx.committed || !tx.rolledBack ||
		capture.observationInserts != 1 || capture.orderUpdates != 0 {
		t.Fatalf("Confirm(observation failure) err=%v tx=%+v capture=%+v", err, tx, capture)
	}
}

func TestPaymentConfirmerRedeemsSelectedCouponInSameTransaction(t *testing.T) {
	t.Parallel()

	current, command, processedAt := pendingPaymentConfirmationFixture(t)
	startCommand := validPaidRegistrationCommand()
	startCommand.TenantID = current.Payment.Order.TenantID
	startCommand.PrincipalID = current.Payment.Order.PrincipalID
	ledger := paidRegistrarCouponLedger(
		t,
		startCommand,
		current.Payment.Order.CreatedAt,
		1_000,
	)
	current.Payment.Order.DiscountCents = ledger.Instrument.FaceValueCents
	current.Payment.Order.PayableCents =
		current.Payment.Order.OriginalPriceCents - ledger.Instrument.FaceValueCents
	command.ActualPaidCents = current.Payment.Order.PayableCents
	held, err := coupon.Hold(coupon.HoldCommand{
		Instrument:         ledger.Instrument,
		History:            ledger.Entries,
		OrderID:            current.Payment.Order.ID,
		RegistrationID:     current.Registration.ID,
		SeriesID:           current.Registration.SeriesID,
		ActivityType:       activity.ActivityTypeAIRoundtable,
		OriginalPriceCents: current.Payment.Order.OriginalPriceCents,
		HoldExpiresAt:      current.Payment.Hold.ExpiresAt,
		At:                 current.Payment.Order.CreatedAt,
		RecordedAt:         current.Payment.Order.CreatedAt,
	})
	if err != nil {
		t.Fatalf("coupon.Hold() error = %v", err)
	}
	ledger.Entries = append(ledger.Entries, held)
	couponRepository := &fakeCouponOrderRepository{ledger: ledger}
	confirmer, tx, _ := newPaymentConfirmerHarness(
		t,
		current,
		command,
		processedAt,
		paymentConfirmerScenario{},
	)
	tx.coupons = couponRepository

	result, err := confirmer.Confirm(context.Background(), command)
	if err != nil {
		t.Fatalf("Confirm(Coupon) error = %v", err)
	}
	if !tx.committed || result.Coupon == nil ||
		result.Coupon.Entry.EntryType != coupon.EntryTypeRedeemed ||
		len(couponRepository.appends) != 1 ||
		couponRepository.appends[0].EntryType != coupon.EntryTypeRedeemed {
		t.Fatalf("Confirm(Coupon) = %+v appends=%+v", result, couponRepository.appends)
	}
}

func TestPaymentConfirmerExactReplayDoesNotCountTwice(t *testing.T) {
	t.Parallel()

	current, command, processedAt := pendingPaymentConfirmationFixture(t)
	paidOrder, _, err := payment.ConfirmOrderPayment(current.Payment.Order, payment.PaymentConfirmation{
		ActualPaidCents:     command.ActualPaidCents,
		WeChatTransactionID: command.WeChatTransactionID,
		PaidAt:              command.PaidAt,
	})
	if err != nil {
		t.Fatalf("ConfirmOrderPayment() error = %v", err)
	}
	convertedHold, _, err := payment.ConvertCapacityHold(current.Payment.Hold, command.PaidAt)
	if err != nil {
		t.Fatalf("ConvertCapacityHold() error = %v", err)
	}
	confirmedRegistration, _, err := registration.ConfirmRegistration(current.Registration, processedAt)
	if err != nil {
		t.Fatalf("ConfirmRegistration() error = %v", err)
	}
	current.Payment.Order = paidOrder
	current.Payment.Hold = convertedHold
	current.Registration = confirmedRegistration

	confirmer, tx, capture := newPaymentConfirmerHarness(
		t,
		current,
		command,
		processedAt.Add(time.Minute),
		paymentConfirmerScenario{},
	)
	result, err := confirmer.Confirm(context.Background(), command)
	if err != nil {
		t.Fatalf("Confirm(replay) error = %v", err)
	}
	if result.Disposition != PaymentDispositionParticipationConfirmed ||
		result.Refund != nil ||
		!tx.committed ||
		tx.rolledBack ||
		capture.orderUpdates != 0 ||
		capture.holdUpdates != 0 ||
		capture.registrationUpdates != 0 ||
		capture.capacityConversions != 0 ||
		capture.historyIncrements != 0 {
		t.Fatalf("replay result/transaction/capture = %+v %+v %+v", result, tx, capture)
	}
}

func TestPaymentConfirmerExpiresLatePaymentAndRequiresRefund(t *testing.T) {
	t.Parallel()

	current, command, _ := pendingPaymentConfirmationFixture(t)
	command.PaidAt = current.Payment.Hold.ExpiresAt
	processedAt := current.Payment.Hold.ExpiresAt.Add(time.Minute)
	confirmer, tx, capture := newPaymentConfirmerHarness(
		t,
		current,
		command,
		processedAt,
		paymentConfirmerScenario{},
	)

	result, err := confirmer.Confirm(context.Background(), command)
	if err != nil {
		t.Fatalf("Confirm(late) error = %v", err)
	}
	if result.Disposition != PaymentDispositionRefundRequired ||
		result.Refund == nil ||
		result.Refund.RefundStatus != refund.StatusPendingManual ||
		result.Refund.ReasonCode != refund.ReasonHoldExpiredAfterPayment ||
		result.Order.PaymentStatus != payment.OrderStatusPaidConfirmed ||
		result.Hold.HoldStatus != payment.CapacityHoldStatusExpired ||
		result.Registration.ParticipationStatus != registration.ParticipationStatusCancelled {
		t.Fatalf("Confirm(late) = %+v", result)
	}
	if !tx.committed || tx.rolledBack ||
		capture.capacityReleases != 1 ||
		capture.capacityConversions != 0 ||
		capture.historyIncrements != 0 ||
		capture.refundLookups != 1 ||
		capture.refundCreates != 1 {
		t.Fatalf("late transaction/capture = %+v %+v", tx, capture)
	}
}

func TestPaymentConfirmerReleasesUnavailableParticipationAndRequiresRefund(t *testing.T) {
	t.Parallel()

	current, command, processedAt := pendingPaymentConfirmationFixture(t)
	confirmer, tx, capture := newPaymentConfirmerHarness(
		t,
		current,
		command,
		processedAt,
		paymentConfirmerScenario{seriesStatus: activity.SeriesStatusArchived},
	)

	result, err := confirmer.Confirm(context.Background(), command)
	if err != nil {
		t.Fatalf("Confirm(unavailable) error = %v", err)
	}
	if result.Disposition != PaymentDispositionRefundRequired ||
		result.Refund == nil ||
		result.Refund.ReasonCode != refund.ReasonSessionUnavailableAfterPayment ||
		result.Hold.HoldStatus != payment.CapacityHoldStatusReleased ||
		result.Registration.ParticipationStatus != registration.ParticipationStatusCancelled {
		t.Fatalf("Confirm(unavailable) = %+v", result)
	}
	if !tx.committed || tx.rolledBack ||
		capture.capacityReleases != 1 ||
		capture.historyIncrements != 0 ||
		capture.refundLookups != 1 ||
		capture.refundCreates != 1 {
		t.Fatalf("unavailable transaction/capture = %+v %+v", tx, capture)
	}
}

func TestPaymentConfirmerReplaysTerminalRefundDisposition(t *testing.T) {
	t.Parallel()

	current, command, _ := pendingPaymentConfirmationFixture(t)
	paidOrder, _, err := payment.ConfirmOrderPayment(current.Payment.Order, payment.PaymentConfirmation{
		ActualPaidCents:     command.ActualPaidCents,
		WeChatTransactionID: command.WeChatTransactionID,
		PaidAt:              command.PaidAt,
	})
	if err != nil {
		t.Fatalf("ConfirmOrderPayment() error = %v", err)
	}
	expiredAt := current.Payment.Hold.ExpiresAt
	expiredHold, _, err := payment.ExpireCapacityHold(
		current.Payment.Hold,
		"payment_refund_required",
		expiredAt,
	)
	if err != nil {
		t.Fatalf("ExpireCapacityHold() error = %v", err)
	}
	cancelledRegistration, _, err := registration.CancelRegistration(
		current.Registration,
		"payment_refund_required",
		expiredAt,
	)
	if err != nil {
		t.Fatalf("CancelRegistration() error = %v", err)
	}
	current.Payment.Order = paidOrder
	current.Payment.Hold = expiredHold
	current.Registration = cancelledRegistration
	existingRefund, err := refund.NewCase(refund.NewCaseCommand{
		TenantID:             paidOrder.TenantID,
		OrderID:              paidOrder.ID,
		RegistrationID:       paidOrder.RegistrationID,
		SeriesID:             paidOrder.SeriesID,
		InstanceID:           paidOrder.InstanceID,
		SessionID:            paidOrder.SessionID,
		PrincipalID:          paidOrder.PrincipalID,
		ReasonCode:           refund.ReasonHoldExpiredAfterPayment,
		IdempotencyKey:       "refund:payment:" + paidOrder.ID.String(),
		ActualPaidCents:      *paidOrder.ActualPaidCents,
		RequestedRefundCents: *paidOrder.ActualPaidCents,
		Now:                  expiredAt,
	})
	if err != nil {
		t.Fatalf("NewCase(existing Refund) error = %v", err)
	}

	confirmer, tx, capture := newPaymentConfirmerHarness(
		t,
		current,
		command,
		expiredAt.Add(time.Minute),
		paymentConfirmerScenario{existingRefund: &existingRefund},
	)
	result, err := confirmer.Confirm(context.Background(), command)
	if err != nil {
		t.Fatalf("Confirm(refund replay) error = %v", err)
	}
	if result.Disposition != PaymentDispositionRefundRequired ||
		result.Refund == nil ||
		result.Refund.ID != existingRefund.ID ||
		!tx.committed ||
		tx.rolledBack ||
		capture.orderUpdates != 0 ||
		capture.holdUpdates != 0 ||
		capture.registrationUpdates != 0 ||
		capture.capacityReleases != 0 ||
		capture.refundLookups != 1 ||
		capture.refundCreates != 0 {
		t.Fatalf("refund replay result/transaction/capture = %+v %+v %+v", result, tx, capture)
	}
}

func TestPaymentConfirmerRejectsConflictingProviderFact(t *testing.T) {
	t.Parallel()

	current, command, processedAt := pendingPaymentConfirmationFixture(t)
	command.ActualPaidCents--
	confirmer, tx, capture := newPaymentConfirmerHarness(
		t,
		current,
		command,
		processedAt,
		paymentConfirmerScenario{},
	)
	if _, err := confirmer.Confirm(context.Background(), command); !errors.Is(
		err,
		ErrPaymentConfirmationConflict,
	) {
		t.Fatalf("Confirm(amount mismatch) error = %v", err)
	}
	if tx.committed || !tx.rolledBack || capture.orderUpdates != 0 {
		t.Fatalf("transaction/capture = %+v %+v", tx, capture)
	}

	paidOrder, _, err := payment.ConfirmOrderPayment(current.Payment.Order, payment.PaymentConfirmation{
		ActualPaidCents:     current.Payment.Order.PayableCents,
		WeChatTransactionID: "wx-transaction-recorded",
		PaidAt:              command.PaidAt,
	})
	if err != nil {
		t.Fatalf("ConfirmOrderPayment() error = %v", err)
	}
	current.Payment.Order = paidOrder
	command.ActualPaidCents = paidOrder.PayableCents
	command.WeChatTransactionID = "wx-transaction-conflict"
	confirmer, tx, _ = newPaymentConfirmerHarness(
		t,
		current,
		command,
		processedAt,
		paymentConfirmerScenario{},
	)
	if _, err := confirmer.Confirm(context.Background(), command); !errors.Is(
		err,
		ErrPaymentConfirmationConflict,
	) {
		t.Fatalf("Confirm(transaction mismatch) error = %v", err)
	}
	if tx.committed || !tx.rolledBack {
		t.Fatalf("transaction committed=%t rolledBack=%t", tx.committed, tx.rolledBack)
	}
}

func TestPaymentConfirmerValidatesBeforeTransactionAndRejectsFutureFact(t *testing.T) {
	t.Parallel()

	current, command, processedAt := pendingPaymentConfirmationFixture(t)
	command.TenantID = uuid.Nil
	starter := &fakePaidRegistrationTransactionStarter{}
	confirmer := &PaymentConfirmer{transactions: starter, now: time.Now}
	if _, err := confirmer.Confirm(context.Background(), command); !errors.Is(
		err,
		ErrInvalidPaymentConfirmationCommand,
	) {
		t.Fatalf("Confirm(invalid) error = %v", err)
	}
	if starter.begins != 0 {
		t.Fatalf("invalid command began %d transaction(s)", starter.begins)
	}

	command.TenantID = current.Payment.Order.TenantID
	command.PaidAt = processedAt.Add(time.Second)
	confirmer, tx, _ := newPaymentConfirmerHarness(
		t,
		current,
		command,
		processedAt,
		paymentConfirmerScenario{},
	)
	if _, err := confirmer.Confirm(context.Background(), command); !errors.Is(
		err,
		ErrInvalidPaymentConfirmationCommand,
	) {
		t.Fatalf("Confirm(future) error = %v", err)
	}
	if tx.committed || !tx.rolledBack {
		t.Fatalf("transaction committed=%t rolledBack=%t", tx.committed, tx.rolledBack)
	}
}

func TestPaymentConfirmerHandlesMissingOrInconsistentContext(t *testing.T) {
	t.Parallel()

	current, command, processedAt := pendingPaymentConfirmationFixture(t)
	confirmer, tx, _ := newPaymentConfirmerHarness(
		t,
		current,
		command,
		processedAt,
		paymentConfirmerScenario{orderMissing: true},
	)
	if _, err := confirmer.Confirm(context.Background(), command); !errors.Is(
		err,
		ErrPaymentConfirmationNotFound,
	) {
		t.Fatalf("Confirm(missing) error = %v", err)
	}
	if tx.committed || !tx.rolledBack {
		t.Fatalf("transaction committed=%t rolledBack=%t", tx.committed, tx.rolledBack)
	}

	current.Payment.Hold.RegistrationID = uuid.New()
	confirmer, tx, _ = newPaymentConfirmerHarness(
		t,
		current,
		command,
		processedAt,
		paymentConfirmerScenario{},
	)
	if _, err := confirmer.Confirm(context.Background(), command); !errors.Is(
		err,
		ErrPaymentConfirmationTransaction,
	) {
		t.Fatalf("Confirm(identity mismatch) error = %v", err)
	}
	if tx.committed || !tx.rolledBack {
		t.Fatalf("transaction committed=%t rolledBack=%t", tx.committed, tx.rolledBack)
	}
}

func TestPaymentConfirmerRollsBackWriteAndCommitFailures(t *testing.T) {
	t.Parallel()

	current, command, processedAt := pendingPaymentConfirmationFixture(t)
	writeFailure := errors.New("write failed")
	tests := []struct {
		name     string
		scenario paymentConfirmerScenario
		wantErr  error
	}{
		{
			name:     "Order update",
			scenario: paymentConfirmerScenario{orderUpdateErr: writeFailure},
			wantErr:  writeFailure,
		},
		{
			name:     "hold update",
			scenario: paymentConfirmerScenario{holdUpdateErr: writeFailure},
			wantErr:  writeFailure,
		},
		{
			name:     "Registration update",
			scenario: paymentConfirmerScenario{registrationUpdateErr: writeFailure},
			wantErr:  writeFailure,
		},
		{
			name:     "capacity conversion",
			scenario: paymentConfirmerScenario{capacityConversionErr: sql.ErrNoRows},
			wantErr:  ErrPaymentConfirmationTransaction,
		},
		{
			name:     "history increment",
			scenario: paymentConfirmerScenario{historyIncrementErr: sql.ErrNoRows},
			wantErr:  ErrPaymentConfirmationTransaction,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			confirmer, tx, _ := newPaymentConfirmerHarness(
				t,
				current,
				command,
				processedAt,
				test.scenario,
			)
			if _, err := confirmer.Confirm(context.Background(), command); !errors.Is(err, test.wantErr) {
				t.Fatalf("Confirm() error = %v, want %v", err, test.wantErr)
			}
			if tx.committed || !tx.rolledBack {
				t.Fatalf("transaction committed=%t rolledBack=%t", tx.committed, tx.rolledBack)
			}
		})
	}

	commitFailure := errors.New("commit failed")
	confirmer, tx, capture := newPaymentConfirmerHarness(
		t,
		current,
		command,
		processedAt,
		paymentConfirmerScenario{commitErr: commitFailure},
	)
	if _, err := confirmer.Confirm(context.Background(), command); !errors.Is(err, commitFailure) {
		t.Fatalf("Confirm(commit failure) error = %v", err)
	}
	if tx.committed || !tx.rolledBack || capture.historyIncrements != 1 {
		t.Fatalf("transaction/capture = %+v %+v", tx, capture)
	}
}

func TestPaymentConfirmationErrorsAreClassified(t *testing.T) {
	t.Parallel()

	uniqueTransaction := fmtPgError("23505", "uq_xiangwan_orders_wechat_transaction")
	if got := classifyPaymentConfirmationWriteError(uniqueTransaction); !errors.Is(
		got,
		ErrPaymentConfirmationConflict,
	) {
		t.Fatalf("classify unique transaction = %v", got)
	}
	if got := classifyPaymentConfirmationWriteError(ErrOrderVersionConflict); !errors.Is(
		got,
		ErrPaymentConfirmationTransaction,
	) {
		t.Fatalf("classify version conflict = %v", got)
	}
	if got := classifyPaymentConfirmationCommitError(&pgconn.PgError{Code: "40001"}); !errors.Is(
		got,
		ErrPaymentConfirmationTransaction,
	) {
		t.Fatalf("classify serialization = %v", got)
	}
}

func TestPaymentConfirmationRefundReasonPreservesCancellationOrigin(t *testing.T) {
	t.Parallel()

	current, command, _ := pendingPaymentConfirmationFixture(t)
	tests := []struct {
		name   string
		reason string
		want   refund.ReasonCode
	}{
		{
			name:   "versioned user policy",
			reason: "user_cancelled@cancel-v3",
			want:   refund.ReasonUserCancelled,
		},
		{
			name:   "operator cancellation",
			reason: "operator_cancelled:" + uuid.New().String() + ":approved exception",
			want:   refund.ReasonOperatorAdjustment,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			currentRegistration := current.Registration
			currentRegistration.CancellationReason = &test.reason
			got := paymentConfirmationRefundReason(
				current.Payment.Order,
				current.Payment.Hold,
				currentRegistration,
				activity.SeriesStatusActive,
				activity.InstanceStatusPublished,
				activity.SessionStatusPublished,
				command.PaidAt,
			)
			if got != test.want {
				t.Fatalf("paymentConfirmationRefundReason() = %q, want %q", got, test.want)
			}
		})
	}
}

type paymentConfirmerScenario struct {
	orderMissing          bool
	seriesStatus          activity.SeriesStatus
	instanceStatus        activity.InstanceStatus
	sessionStatus         activity.SessionStatus
	orderUpdateErr        error
	holdUpdateErr         error
	registrationUpdateErr error
	capacityConversionErr error
	capacityReleaseErr    error
	historyIncrementErr   error
	refundLookupErr       error
	refundCreateErr       error
	existingRefund        *refund.Case
	commitErr             error
	observationWriteErr   error
}

type paymentConfirmerCapture struct {
	isolation              sql.IsolationLevel
	lockOrder              []string
	orderUpdates           int
	holdUpdates            int
	registrationUpdates    int
	capacityConversions    int
	capacityReleases       int
	historyIncrements      int
	refundLookups          int
	refundCreates          int
	observationInserts     int
	observationSource      string
	observationSourceKey   string
	observationOrderID     uuid.UUID
	observationPrincipalID uuid.UUID
}

func newPaymentConfirmerHarness(
	t *testing.T,
	current PaidRegistrationContext,
	command ConfirmPaymentCommand,
	processedAt time.Time,
	scenario paymentConfirmerScenario,
) (*PaymentConfirmer, *fakePaidRegistrationTransaction, *paymentConfirmerCapture) {
	t.Helper()

	if scenario.seriesStatus == "" {
		scenario.seriesStatus = activity.SeriesStatusActive
	}
	if scenario.instanceStatus == "" {
		scenario.instanceStatus = activity.InstanceStatusPublished
	}
	if scenario.sessionStatus == "" {
		scenario.sessionStatus = activity.SessionStatusPublished
	}
	capture := &paymentConfirmerCapture{}
	tx := &fakePaidRegistrationTransaction{commitErr: scenario.commitErr}
	tx.fakeQueryExecutor = &fakeQueryExecutor{queryRow: func(query string, args ...any) rowScanner {
		switch {
		case strings.Contains(query, "FROM xiangwan_orders"):
			if scenario.orderMissing {
				return &fakeRow{err: sql.ErrNoRows}
			}
			if strings.Contains(query, "FOR UPDATE") {
				capture.lockOrder = append(capture.lockOrder, "order")
			}
			return &fakeRow{values: orderScanValues(current.Payment.Order)}
		case strings.Contains(query, "FROM xiangwan_activity_series"):
			capture.lockOrder = append(capture.lockOrder, "series")
			return &fakeRow{values: []any{scenario.seriesStatus, int64(7)}}
		case strings.Contains(query, "FROM xiangwan_activity_instances"):
			capture.lockOrder = append(capture.lockOrder, "instance")
			return &fakeRow{values: []any{scenario.instanceStatus}}
		case strings.Contains(query, "FROM xiangwan_activity_sessions"):
			capture.lockOrder = append(capture.lockOrder, "session")
			return &fakeRow{values: []any{scenario.sessionStatus, int64(9)}}
		case strings.Contains(query, "FROM xiangwan_registrations"):
			capture.lockOrder = append(capture.lockOrder, "registration")
			return &fakeRow{values: paidRegistrationScanValues(current.Registration)}
		case strings.Contains(query, "FROM xiangwan_capacity_holds"):
			capture.lockOrder = append(capture.lockOrder, "hold")
			return &fakeRow{values: holdScanValues(current.Payment.Hold)}
		case strings.Contains(query, "FROM xiangwan_refund_cases"):
			capture.refundLookups++
			if scenario.refundLookupErr != nil {
				return &fakeRow{err: scenario.refundLookupErr}
			}
			if scenario.existingRefund == nil {
				return &fakeRow{err: sql.ErrNoRows}
			}
			return &fakeRow{values: paymentRefundCaseScanValues(*scenario.existingRefund)}
		case strings.Contains(query, "INSERT INTO xiangwan_refund_cases"):
			capture.refundCreates++
			if scenario.refundCreateErr != nil {
				return &fakeRow{err: scenario.refundCreateErr}
			}
			created := refund.Case{
				ID:                    args[0].(uuid.UUID),
				TenantID:              args[1].(uuid.UUID),
				OrderID:               args[2].(uuid.UUID),
				RegistrationID:        args[3].(uuid.UUID),
				SeriesID:              args[4].(uuid.UUID),
				InstanceID:            args[5].(uuid.UUID),
				SessionID:             args[6].(uuid.UUID),
				PrincipalID:           args[7].(uuid.UUID),
				RefundStatus:          args[8].(refund.Status),
				ReasonCode:            args[9].(refund.ReasonCode),
				IdempotencyKey:        args[10].(string),
				RequestedRefundCents:  args[11].(int64),
				SuccessfulRefundCents: args[12].(int64),
				ProcessingStartedAt:   args[13].(*time.Time),
				ResolvedAt:            args[14].(*time.Time),
				HandledBy:             args[15].(*uuid.UUID),
				ExternalRefundID:      args[16].(*string),
				EvidenceReference:     args[17].(*string),
				OperatorNote:          args[18].(*string),
				FailureReason:         args[19].(*string),
				Version:               args[20].(int64),
				CreatedAt:             args[21].(time.Time),
				UpdatedAt:             args[22].(time.Time),
			}
			return &fakeRow{values: paymentRefundCaseScanValues(created)}
		case strings.Contains(query, "INSERT INTO xiangwan_payment_transaction_observations"):
			capture.observationInserts++
			capture.lockOrder = append(capture.lockOrder, "observation")
			capture.observationOrderID = args[2].(uuid.UUID)
			capture.observationPrincipalID = args[3].(uuid.UUID)
			capture.observationSource = args[4].(string)
			capture.observationSourceKey = args[5].(string)
			if scenario.observationWriteErr != nil {
				return &fakeRow{err: scenario.observationWriteErr}
			}
			return &fakeRow{values: []any{args[0].(uuid.UUID)}}
		case strings.Contains(query, "UPDATE xiangwan_orders"):
			capture.orderUpdates++
			if scenario.orderUpdateErr != nil {
				return &fakeRow{err: scenario.orderUpdateErr}
			}
			updated := current.Payment.Order
			updated.PaymentStatus = args[2].(payment.OrderStatus)
			updated.ActualPaidCents = args[3].(*int64)
			updated.WeChatTransactionID = args[4].(*string)
			updated.PaidAt = args[5].(*time.Time)
			updated.ClosedAt = args[6].(*time.Time)
			updated.Version++
			updated.UpdatedAt = args[7].(time.Time)
			return &fakeRow{values: orderScanValues(updated)}
		case strings.Contains(query, "UPDATE xiangwan_capacity_holds"):
			capture.holdUpdates++
			if scenario.holdUpdateErr != nil {
				return &fakeRow{err: scenario.holdUpdateErr}
			}
			updated := current.Payment.Hold
			updated.HoldStatus = args[2].(payment.CapacityHoldStatus)
			updated.ConvertedAt = args[3].(*time.Time)
			updated.ReleasedAt = args[4].(*time.Time)
			updated.ReleaseReason = args[5].(*string)
			updated.Version++
			updated.UpdatedAt = args[6].(time.Time)
			return &fakeRow{values: holdScanValues(updated)}
		case strings.Contains(query, "UPDATE xiangwan_registrations"):
			capture.registrationUpdates++
			if scenario.registrationUpdateErr != nil {
				return &fakeRow{err: scenario.registrationUpdateErr}
			}
			updated := current.Registration
			updated.ParticipationStatus = args[2].(registration.ParticipationStatus)
			updated.ConfirmedAt = args[3].(*time.Time)
			updated.CancelledAt = args[4].(*time.Time)
			updated.CancellationReason = args[5].(*string)
			updated.Version++
			updated.UpdatedAt = args[6].(time.Time)
			return &fakeRow{values: paidRegistrationScanValues(updated)}
		case strings.Contains(query, "UPDATE xiangwan_activity_sessions") &&
			strings.Contains(query, "confirmed_registration_count"):
			capture.capacityConversions++
			if scenario.capacityConversionErr != nil {
				return &fakeRow{err: scenario.capacityConversionErr}
			}
			return &fakeRow{values: []any{int64(10)}}
		case strings.Contains(query, "UPDATE xiangwan_activity_sessions"):
			capture.capacityReleases++
			if scenario.capacityReleaseErr != nil {
				return &fakeRow{err: scenario.capacityReleaseErr}
			}
			return &fakeRow{values: []any{int64(10)}}
		case strings.Contains(query, "UPDATE xiangwan_activity_series"):
			capture.historyIncrements++
			if scenario.historyIncrementErr != nil {
				return &fakeRow{err: scenario.historyIncrementErr}
			}
			return &fakeRow{values: []any{int64(8)}}
		default:
			t.Fatalf("unexpected query: %s", query)
			return &fakeRow{}
		}
	}}
	starter := &fakePaidRegistrationTransactionStarter{tx: tx}
	starter.capture = &paidRegistrarCapture{}
	return &PaymentConfirmer{
		transactions: &confirmationTransactionStarter{
			delegate: starter,
			capture:  capture,
		},
		now: func() time.Time { return processedAt },
	}, tx, capture
}

type confirmationTransactionStarter struct {
	delegate *fakePaidRegistrationTransactionStarter
	capture  *paymentConfirmerCapture
}

func (starter *confirmationTransactionStarter) beginTx(
	ctx context.Context,
	options *sql.TxOptions,
) (paidRegistrationTransaction, error) {
	starter.capture.isolation = options.Isolation
	return starter.delegate.beginTx(ctx, options)
}

func pendingPaymentConfirmationFixture(
	t *testing.T,
) (PaidRegistrationContext, ConfirmPaymentCommand, time.Time) {
	t.Helper()

	startCommand := validPaidRegistrationCommand()
	createdAt := time.Date(2026, time.September, 12, 6, 0, 0, 0, time.UTC)
	current := existingPaidRegistrationContext(t, startCommand, createdAt)
	command := ConfirmPaymentCommand{
		TenantID:            startCommand.TenantID,
		PaymentAppID:        startCommand.PaymentAppID,
		PaymentMerchantID:   startCommand.PaymentMerchantID,
		MerchantOrderNo:     startCommand.MerchantOrderNo,
		WeChatTransactionID: "wx-transaction-confirm-1",
		ActualPaidCents:     current.Payment.Order.PayableCents,
		PaidAt:              createdAt.Add(time.Minute),
	}
	return current, command, createdAt.Add(2 * time.Minute)
}

func paymentQuerySuccessObservation(
	t *testing.T,
	order payment.Order,
	command ConfirmPaymentCommand,
	observedAt time.Time,
) payment.TransactionObservation {
	t.Helper()
	lease, err := payment.NewPaymentQueryLease(
		payment.CreatePaymentQueryLeaseCommand{
			ID:                uuid.New(),
			TenantID:          order.TenantID,
			OrderID:           order.ID,
			PrincipalID:       order.PrincipalID,
			GenerationID:      uuid.New(),
			PaymentAppID:      order.PaymentAppID,
			PaymentMerchantID: order.PaymentMerchantID,
			OutTradeNo:        order.MerchantOrderNo,
			AmountCents:       order.PayableCents,
			OwnerToken:        uuid.New(),
			Now:               order.CreatedAt,
		},
	)
	if err != nil {
		t.Fatalf("NewPaymentQueryLease() error = %v", err)
	}
	request := payment.ProviderPaymentQueryRequest{
		AppID:       order.PaymentAppID,
		MerchantID:  order.PaymentMerchantID,
		OutTradeNo:  order.MerchantOrderNo,
		AmountCents: order.PayableCents,
		Currency:    payment.PaymentQueryCurrency,
	}
	paidAt := command.PaidAt
	observation, err := payment.NewTransactionObservation(
		payment.PaymentQueryAcquisition{
			Lease:           lease,
			Order:           order,
			InvocationToken: *lease.OwnerToken,
			ProviderRequest: &request,
		},
		payment.ProviderPaymentQueryResult{
			AppID:             order.PaymentAppID,
			MerchantID:        order.PaymentMerchantID,
			OutTradeNo:        order.MerchantOrderNo,
			TransactionID:     command.WeChatTransactionID,
			TradeType:         payment.PaymentQueryTradeType,
			TradeState:        payment.ProviderTradeStateSuccess,
			AmountCents:       command.ActualPaidCents,
			Currency:          payment.PaymentQueryCurrency,
			SuccessAt:         &paidAt,
			ProviderRequestID: "provider-request-1",
		},
		observedAt,
	)
	if err != nil {
		t.Fatalf("NewTransactionObservation() error = %v", err)
	}
	return observation
}

func paymentQueryTerminalObservation(
	t *testing.T,
	order payment.Order,
	state payment.ProviderTradeState,
	observedAt time.Time,
) payment.TransactionObservation {
	t.Helper()
	lease, err := payment.NewPaymentQueryLease(
		payment.CreatePaymentQueryLeaseCommand{
			ID:                uuid.New(),
			TenantID:          order.TenantID,
			OrderID:           order.ID,
			PrincipalID:       order.PrincipalID,
			GenerationID:      uuid.New(),
			PaymentAppID:      order.PaymentAppID,
			PaymentMerchantID: order.PaymentMerchantID,
			OutTradeNo:        order.MerchantOrderNo,
			AmountCents:       order.PayableCents,
			OwnerToken:        uuid.New(),
			Now:               order.CreatedAt,
		},
	)
	if err != nil {
		t.Fatalf("NewPaymentQueryLease() error = %v", err)
	}
	request := payment.ProviderPaymentQueryRequest{
		AppID:       order.PaymentAppID,
		MerchantID:  order.PaymentMerchantID,
		OutTradeNo:  order.MerchantOrderNo,
		AmountCents: order.PayableCents,
		Currency:    payment.PaymentQueryCurrency,
	}
	observation, err := payment.NewTransactionObservation(
		payment.PaymentQueryAcquisition{
			Lease:           lease,
			Order:           order,
			InvocationToken: *lease.OwnerToken,
			ProviderRequest: &request,
		},
		payment.ProviderPaymentQueryResult{
			AppID:             order.PaymentAppID,
			MerchantID:        order.PaymentMerchantID,
			OutTradeNo:        order.MerchantOrderNo,
			TradeType:         payment.PaymentQueryTradeType,
			TradeState:        state,
			AmountCents:       order.PayableCents,
			Currency:          payment.PaymentQueryCurrency,
			ProviderRequestID: "provider-terminal-request-1",
		},
		observedAt,
	)
	if err != nil {
		t.Fatalf("NewTransactionObservation() error = %v", err)
	}
	return observation
}

func trustedPaymentNotification(
	command ConfirmPaymentCommand,
	observedAt time.Time,
) payment.TrustedPaymentNotification {
	paidAt := command.PaidAt
	return payment.TrustedPaymentNotification{
		TenantID:      command.TenantID,
		ObservationID: uuid.New(),
		ObservedAt:    observedAt,
		Notification: payment.VerifiedPaymentNotification{
			NotificationID: "notify-payment-confirmer-01",
			CreatedAt:      observedAt.Add(-time.Minute),
			EventType:      payment.PaymentNotificationEventTransactionSuccess,
			ResourceType:   payment.PaymentNotificationResourceTransaction,
			OriginalType:   payment.PaymentNotificationOriginalTypeTransaction,
			PayloadDigest:  strings.Repeat("e", 64),
			Transaction: payment.ProviderPaymentQueryResult{
				AppID:         command.PaymentAppID,
				MerchantID:    command.PaymentMerchantID,
				OutTradeNo:    command.MerchantOrderNo,
				TransactionID: command.WeChatTransactionID,
				TradeType:     payment.PaymentQueryTradeType,
				TradeState:    payment.ProviderTradeStateSuccess,
				AmountCents:   command.ActualPaidCents,
				Currency:      payment.PaymentQueryCurrency,
				SuccessAt:     &paidAt,
			},
		},
	}
}

func fmtPgError(code string, constraint string) error {
	return &pgconn.PgError{Code: code, ConstraintName: constraint}
}

func paymentRefundCaseScanValues(value refund.Case) []any {
	return []any{
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
		nullTime(value.ProcessingStartedAt),
		nullTime(value.ResolvedAt),
		paymentNullUUID(value.HandledBy),
		nullString(value.ExternalRefundID),
		nullString(value.EvidenceReference),
		nullString(value.OperatorNote),
		nullString(value.FailureReason),
		value.Version,
		value.CreatedAt,
		value.UpdatedAt,
	}
}

func paymentNullUUID(value *uuid.UUID) uuid.NullUUID {
	if value == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *value, Valid: true}
}
