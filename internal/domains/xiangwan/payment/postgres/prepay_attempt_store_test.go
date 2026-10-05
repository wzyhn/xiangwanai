package paymentpostgres

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
	"github.com/google/uuid"
)

func TestPrepayAttemptStoreAcquiresSingleProviderLease(t *testing.T) {
	fixture := newPrepayStoreFixture(t)
	created := fixture.newAttempt(t, fixture.now, fixture.attemptID, fixture.ownerToken)
	capture := &prepayStoreCapture{}
	tx := fixture.transaction(t, capture, func(query string, _ ...any) rowScanner {
		switch {
		case strings.Contains(query, "FROM xiangwan_payment_attempts"):
			return &fakeRow{err: sql.ErrNoRows}
		case strings.Contains(query, "INSERT INTO xiangwan_payment_attempts"):
			capture.attemptInserts++
			return &fakeRow{values: prepayAttemptScanValues(created)}
		default:
			return fixture.factRow(t, query)
		}
	})
	store := fixture.store(t, &fakePrepayTransactionStarter{transactions: []prepayTransaction{tx}})

	acquisition, err := store.AcquirePrepayAttempt(context.Background(), fixture.command)
	if err != nil {
		t.Fatalf("acquire prepay: %v", err)
	}
	if !tx.committed || capture.attemptInserts != 1 ||
		acquisition.InvocationToken != fixture.ownerToken ||
		acquisition.ProviderRequest == nil ||
		acquisition.ProviderRequest.OpenID != fixture.openID ||
		acquisition.ProviderRequest.OutTradeNo != fixture.order.MerchantOrderNo ||
		acquisition.ProviderRequest.AmountCents != fixture.order.PayableCents ||
		!acquisition.ProviderRequest.TimeExpireAt.Equal(fixture.hold.ExpiresAt) {
		t.Fatalf("unexpected acquisition: %+v capture=%+v", acquisition, capture)
	}
}

func TestPrepayAttemptStoreReusesOneOrderAcrossOperationKeys(t *testing.T) {
	fixture := newPrepayStoreFixture(t)
	attempt := fixture.newAttempt(t, fixture.now.Add(-time.Second), fixture.attemptID, fixture.ownerToken)
	ready, err := payment.CompletePaymentAttemptReady(
		attempt,
		fixture.ownerToken,
		validStoreProviderResult(),
		fixture.now,
	)
	if err != nil {
		t.Fatalf("prepare ready attempt: %v", err)
	}
	capture := &prepayStoreCapture{}
	tx := fixture.transaction(t, capture, func(query string, _ ...any) rowScanner {
		switch {
		case strings.Contains(query, "FROM xiangwan_payment_attempts") &&
			strings.Contains(query, "AND idempotency_key = $3"):
			return &fakeRow{err: sql.ErrNoRows}
		case strings.Contains(query, "FROM xiangwan_payment_attempts"):
			return &fakeRow{values: prepayAttemptScanValues(ready)}
		case strings.Contains(query, "INSERT INTO xiangwan_payment_attempts"):
			capture.attemptInserts++
			return &fakeRow{err: errors.New("must not insert replay")}
		default:
			return fixture.factRow(t, query)
		}
	})
	store := fixture.store(t, &fakePrepayTransactionStarter{transactions: []prepayTransaction{tx}})
	command := fixture.command
	command.IdempotencyKey = uuid.New()

	acquisition, err := store.AcquirePrepayAttempt(context.Background(), command)
	if err != nil {
		t.Fatalf("replay prepay: %v", err)
	}
	parameters, available := acquisition.Attempt.PaymentParameters()
	if !tx.committed || capture.attemptInserts != 0 ||
		acquisition.ProviderRequest != nil || !available ||
		parameters.Package != validStoreProviderResult().Parameters.Package {
		t.Fatalf("unexpected replay: %+v", acquisition)
	}
}

func TestPrepayAttemptStoreTakesOverExpiredLease(t *testing.T) {
	fixture := newPrepayStoreFixture(t)
	oldOwner := uuid.New()
	existing := fixture.newAttempt(
		t,
		fixture.now.Add(-payment.PrepayLeaseDuration),
		fixture.attemptID,
		oldOwner,
	)
	takenOver, err := payment.TakeOverPaymentAttempt(
		existing,
		fixture.command.GenerationID,
		fixture.ownerToken,
		fixture.now,
	)
	if err != nil {
		t.Fatalf("prepare takeover: %v", err)
	}
	capture := &prepayStoreCapture{}
	tx := fixture.transaction(t, capture, func(query string, _ ...any) rowScanner {
		switch {
		case strings.Contains(query, "FROM xiangwan_payment_attempts"):
			return &fakeRow{values: prepayAttemptScanValues(existing)}
		case strings.Contains(query, "UPDATE xiangwan_payment_attempts"):
			capture.attemptUpdates++
			return &fakeRow{values: prepayAttemptScanValues(takenOver)}
		default:
			return fixture.factRow(t, query)
		}
	})
	store := fixture.store(t, &fakePrepayTransactionStarter{transactions: []prepayTransaction{tx}})

	acquisition, err := store.AcquirePrepayAttempt(context.Background(), fixture.command)
	if err != nil {
		t.Fatalf("take over prepay: %v", err)
	}
	if !tx.committed || capture.attemptUpdates != 1 ||
		acquisition.ProviderRequest == nil ||
		acquisition.Attempt.OwnerToken == nil ||
		*acquisition.Attempt.OwnerToken != fixture.ownerToken {
		t.Fatalf("unexpected takeover: %+v", acquisition)
	}
}

func TestPrepayAttemptStoreCompletesReadyWithImmutableObservation(t *testing.T) {
	fixture := newPrepayStoreFixture(t)
	attempt := fixture.newAttempt(t, fixture.now.Add(-time.Second), fixture.attemptID, fixture.ownerToken)
	providerResult := validStoreProviderResult()
	ready, err := payment.CompletePaymentAttemptReady(
		attempt,
		fixture.ownerToken,
		providerResult,
		fixture.now,
	)
	if err != nil {
		t.Fatalf("prepare completion: %v", err)
	}
	capture := &prepayStoreCapture{}
	tx := fixture.transaction(t, capture, func(query string, _ ...any) rowScanner {
		switch {
		case strings.Contains(query, "FROM xiangwan_payment_attempts"):
			return &fakeRow{values: prepayAttemptScanValues(attempt)}
		case strings.Contains(query, "UPDATE xiangwan_payment_attempts"):
			capture.attemptUpdates++
			return &fakeRow{values: prepayAttemptScanValues(ready)}
		case strings.Contains(query, "INSERT INTO xiangwan_payment_observations"):
			capture.observationInserts++
			return &fakeRow{values: []any{fixture.observationID}}
		default:
			return fixture.factRow(t, query)
		}
	})
	store := fixture.store(t, &fakePrepayTransactionStarter{transactions: []prepayTransaction{tx}})
	acquisition := fixture.acquisition(attempt)

	result, err := store.CompletePrepayAttempt(
		context.Background(),
		acquisition,
		providerResult,
		nil,
	)
	if err != nil {
		t.Fatalf("complete prepay: %v", err)
	}
	if !tx.committed || capture.attemptUpdates != 1 ||
		capture.observationInserts != 1 || result.Parameters == nil ||
		result.Attempt.AttemptStatus != payment.PrepayAttemptStatusReady {
		t.Fatalf("unexpected completion: %+v capture=%+v", result, capture)
	}
}

func TestPrepayAttemptStoreMakesProviderFailureUnknown(t *testing.T) {
	fixture := newPrepayStoreFixture(t)
	attempt := fixture.newAttempt(t, fixture.now.Add(-time.Second), fixture.attemptID, fixture.ownerToken)
	unknown, err := payment.CompletePaymentAttemptUnknown(
		attempt,
		fixture.ownerToken,
		payment.ProviderFailureTimeout,
		"",
		fixture.now,
	)
	if err != nil {
		t.Fatalf("prepare unknown: %v", err)
	}
	capture := &prepayStoreCapture{}
	tx := fixture.transaction(t, capture, func(query string, _ ...any) rowScanner {
		switch {
		case strings.Contains(query, "FROM xiangwan_payment_attempts"):
			return &fakeRow{values: prepayAttemptScanValues(attempt)}
		case strings.Contains(query, "UPDATE xiangwan_payment_attempts"):
			capture.attemptUpdates++
			return &fakeRow{values: prepayAttemptScanValues(unknown)}
		case strings.Contains(query, "UPDATE xiangwan_orders"):
			capture.orderUpdates++
			unknownOrder, _, _ := payment.MarkOrderUnknown(fixture.order, fixture.now)
			return &fakeRow{values: orderScanValues(unknownOrder)}
		case strings.Contains(query, "INSERT INTO xiangwan_payment_observations"):
			capture.observationInserts++
			return &fakeRow{values: []any{fixture.observationID}}
		default:
			return fixture.factRow(t, query)
		}
	})
	store := fixture.store(t, &fakePrepayTransactionStarter{transactions: []prepayTransaction{tx}})

	result, err := store.CompletePrepayAttempt(
		context.Background(),
		fixture.acquisition(attempt),
		payment.ProviderPrepayResult{},
		payment.NewProviderFailure(
			payment.ProviderFailureTimeout,
			"",
			context.DeadlineExceeded,
		),
	)
	if err != nil {
		t.Fatalf("complete unknown prepay: %v", err)
	}
	if result.Attempt.AttemptStatus != payment.PrepayAttemptStatusUnknown ||
		result.Parameters != nil || capture.observationInserts != 1 ||
		capture.orderUpdates != 1 {
		t.Fatalf("unexpected unknown result: %+v", result)
	}
}

func TestPrepayAttemptStoreRecordsReadyObservationAcrossGenerationChange(
	t *testing.T,
) {
	fixture := newPrepayStoreFixture(t)
	attempt := fixture.newAttempt(
		t,
		fixture.now.Add(-time.Second),
		fixture.attemptID,
		fixture.ownerToken,
	)
	unknown, err := payment.CompletePaymentAttemptUnknown(
		attempt,
		fixture.ownerToken,
		"local_state_changed",
		validStoreProviderResult().ProviderRequestID,
		fixture.now,
	)
	if err != nil {
		t.Fatalf("prepare generation-change completion: %v", err)
	}
	capture := &prepayStoreCapture{}
	tx := fixture.transaction(t, capture, func(query string, _ ...any) rowScanner {
		switch {
		case strings.Contains(query, "FROM xiangwan_runtime_generations"):
			return &fakeRow{values: []any{uuid.New(), int64(2)}}
		case strings.Contains(query, "FROM xiangwan_payment_attempts"):
			return &fakeRow{values: prepayAttemptScanValues(attempt)}
		case strings.Contains(query, "UPDATE xiangwan_payment_attempts"):
			capture.attemptUpdates++
			return &fakeRow{values: prepayAttemptScanValues(unknown)}
		case strings.Contains(query, "UPDATE xiangwan_orders"):
			capture.orderUpdates++
			unknownOrder, _, _ := payment.MarkOrderUnknown(fixture.order, fixture.now)
			return &fakeRow{values: orderScanValues(unknownOrder)}
		case strings.Contains(query, "INSERT INTO xiangwan_payment_observations"):
			capture.observationInserts++
			return &fakeRow{values: []any{fixture.observationID}}
		default:
			return fixture.factRow(t, query)
		}
	})
	store := fixture.store(
		t,
		&fakePrepayTransactionStarter{transactions: []prepayTransaction{tx}},
	)

	result, err := store.CompletePrepayAttempt(
		context.Background(),
		fixture.acquisition(attempt),
		validStoreProviderResult(),
		nil,
	)
	if err != nil {
		t.Fatalf("complete prepay across generation change: %v", err)
	}
	if !tx.committed || capture.attemptUpdates != 1 || capture.orderUpdates != 1 ||
		capture.observationInserts != 1 ||
		result.Attempt.AttemptStatus != payment.PrepayAttemptStatusUnknown ||
		result.Parameters != nil {
		t.Fatalf("generation-change result=%+v capture=%+v", result, capture)
	}
}

type prepayStoreFixture struct {
	now            time.Time
	tenantID       uuid.UUID
	generationID   uuid.UUID
	principalID    uuid.UUID
	seriesID       uuid.UUID
	instanceID     uuid.UUID
	sessionID      uuid.UUID
	registrationID uuid.UUID
	attemptID      uuid.UUID
	ownerToken     uuid.UUID
	observationID  uuid.UUID
	openID         string
	order          payment.Order
	hold           payment.CapacityHold
	command        payment.CreatePrepayAttemptCommand
}

func newPrepayStoreFixture(t *testing.T) prepayStoreFixture {
	t.Helper()
	now := time.Date(2026, 9, 13, 9, 0, 0, 0, time.UTC)
	fixture := prepayStoreFixture{
		now:            now,
		tenantID:       uuid.New(),
		generationID:   uuid.New(),
		principalID:    uuid.New(),
		seriesID:       uuid.New(),
		instanceID:     uuid.New(),
		sessionID:      uuid.New(),
		registrationID: uuid.New(),
		attemptID:      uuid.New(),
		ownerToken:     uuid.New(),
		observationID:  uuid.New(),
		openID:         "openid-xiangwan-1",
	}
	fixture.order = payment.Order{
		ID:                 uuid.New(),
		TenantID:           fixture.tenantID,
		RegistrationID:     fixture.registrationID,
		SeriesID:           fixture.seriesID,
		InstanceID:         fixture.instanceID,
		SessionID:          fixture.sessionID,
		PrincipalID:        fixture.principalID,
		PaymentStatus:      payment.OrderStatusPending,
		IdempotencyKey:     "registration:payment:1",
		MerchantOrderNo:    "XW-PREPAY-ORDER-00000001",
		PaymentAppID:       "wxXiangwan123",
		PaymentMerchantID:  "1900000109",
		OriginalPriceCents: 19900,
		PayableCents:       19900,
		Version:            1,
		CreatedAt:          now.Add(-time.Minute),
		UpdatedAt:          now.Add(-time.Minute),
	}
	fixture.hold = payment.CapacityHold{
		ID:             uuid.New(),
		TenantID:       fixture.tenantID,
		OrderID:        fixture.order.ID,
		RegistrationID: fixture.registrationID,
		SessionID:      fixture.sessionID,
		HoldStatus:     payment.CapacityHoldStatusActive,
		ExpiresAt:      now.Add(9 * time.Minute),
		Version:        1,
		CreatedAt:      now.Add(-time.Minute),
		UpdatedAt:      now.Add(-time.Minute),
	}
	fixture.command = payment.CreatePrepayAttemptCommand{
		TenantID:             fixture.tenantID,
		GenerationID:         fixture.generationID,
		OrderID:              fixture.order.ID,
		PrincipalID:          fixture.principalID,
		IdempotencyKey:       uuid.New(),
		ExpectedOrderVersion: fixture.order.Version,
		ExpectedPayableCents: fixture.order.PayableCents,
	}
	return fixture
}

func (fixture prepayStoreFixture) store(
	t *testing.T,
	starter prepayTransactionStarter,
) *PrepayAttemptStore {
	t.Helper()
	ids := []uuid.UUID{fixture.attemptID, fixture.ownerToken, fixture.observationID}
	index := 0
	store, err := newPrepayAttemptStore(starter, PrepayAttemptStoreConfig{
		PaymentAppID:      fixture.order.PaymentAppID,
		PaymentMerchantID: fixture.order.PaymentMerchantID,
		Description:       "享玩活动报名",
		NotifyURL:         "https://xiangwan.example.com/api/v1/xiangwan/wechat-pay/notifications",
		Now:               func() time.Time { return fixture.now },
		NewUUID: func() uuid.UUID {
			if index >= len(ids) {
				return uuid.New()
			}
			value := ids[index]
			index++
			return value
		},
	})
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	return store
}

func (fixture prepayStoreFixture) newAttempt(
	t *testing.T,
	now time.Time,
	attemptID uuid.UUID,
	ownerToken uuid.UUID,
) payment.PaymentAttempt {
	t.Helper()
	baseFingerprint, err := payment.PrepayRequestFingerprint(fixture.command)
	if err != nil {
		t.Fatalf("fingerprint: %v", err)
	}
	store, err := newPrepayAttemptStore(&fakePrepayTransactionStarter{}, PrepayAttemptStoreConfig{
		PaymentAppID:      fixture.order.PaymentAppID,
		PaymentMerchantID: fixture.order.PaymentMerchantID,
		Description:       "享玩活动报名",
		NotifyURL:         "https://xiangwan.example.com/api/v1/xiangwan/wechat-pay/notifications",
	})
	if err != nil {
		t.Fatalf("new fingerprint store: %v", err)
	}
	attempt, err := payment.NewPaymentAttempt(payment.NewPaymentAttemptCommand{
		ID:                 attemptID,
		TenantID:           fixture.tenantID,
		OrderID:            fixture.order.ID,
		PrincipalID:        fixture.principalID,
		GenerationID:       fixture.generationID,
		IdempotencyKey:     fixture.command.IdempotencyKey,
		RequestFingerprint: store.requestFingerprint(baseFingerprint),
		OutTradeNo:         fixture.order.MerchantOrderNo,
		PaymentAppID:       fixture.order.PaymentAppID,
		PaymentMerchantID:  fixture.order.PaymentMerchantID,
		Description:        store.description,
		NotifyURL:          store.notifyURL,
		OrderVersion:       fixture.order.Version,
		AmountCents:        fixture.order.PayableCents,
		OwnerToken:         ownerToken,
		Now:                now,
	})
	if err != nil {
		t.Fatalf("new attempt: %v", err)
	}
	return attempt
}

func (fixture prepayStoreFixture) acquisition(
	attempt payment.PaymentAttempt,
) payment.PrepayAcquisition {
	return payment.PrepayAcquisition{
		Attempt:         attempt,
		HoldExpiresAt:   fixture.hold.ExpiresAt,
		InvocationToken: fixture.ownerToken,
		ProviderRequest: &payment.ProviderPrepayRequest{
			AppID:        attempt.PaymentAppID,
			MerchantID:   attempt.PaymentMerchantID,
			Description:  attempt.Description,
			OutTradeNo:   attempt.OutTradeNo,
			NotifyURL:    attempt.NotifyURL,
			OpenID:       fixture.openID,
			AmountCents:  attempt.AmountCents,
			TimeExpireAt: fixture.hold.ExpiresAt,
		},
	}
}

func (fixture prepayStoreFixture) factRow(t *testing.T, query string) rowScanner {
	t.Helper()
	switch {
	case strings.Contains(query, "SELECT series_id, instance_id"):
		return &fakeRow{values: []any{
			fixture.seriesID,
			fixture.instanceID,
			fixture.sessionID,
			fixture.registrationID,
		}}
	case strings.Contains(query, "FROM xiangwan_runtime_generations"):
		return &fakeRow{values: []any{fixture.generationID, int64(1)}}
	case strings.Contains(query, "FROM xiangwan_brand_profiles"):
		return &fakeRow{values: []any{activity.BrandLifecycleActive}}
	case strings.Contains(query, "FROM xiangwan_activity_series"):
		return &fakeRow{values: []any{activity.SeriesStatusActive}}
	case strings.Contains(query, "FROM xiangwan_activity_instances"):
		return &fakeRow{values: []any{activity.InstanceStatusPublished}}
	case strings.Contains(query, "FROM xiangwan_activity_sessions"):
		return &fakeRow{values: []any{
			activity.SessionStatusPublished,
			sql.NullTime{Time: fixture.now.Add(time.Hour), Valid: true},
		}}
	case strings.Contains(query, "SELECT participation_status"):
		return &fakeRow{values: []any{
			registration.ParticipationStatusPendingPayment,
			fixture.principalID,
		}}
	case strings.Contains(query, "FROM xiangwan_capacity_holds"):
		return &fakeRow{values: holdScanValues(fixture.hold)}
	case strings.Contains(query, "FROM xiangwan_orders"):
		return &fakeRow{values: orderScanValues(fixture.order)}
	case strings.Contains(query, "FROM identity_links"):
		return &fakeRow{values: []any{
			int64(1),
			sql.NullString{String: fixture.openID, Valid: true},
		}}
	default:
		t.Fatalf("unexpected prepay query: %s", query)
		return &fakeRow{}
	}
}

func (fixture prepayStoreFixture) transaction(
	_ *testing.T,
	capture *prepayStoreCapture,
	queryRow func(string, ...any) rowScanner,
) *fakePrepayTransaction {
	return &fakePrepayTransaction{
		fakeQueryExecutor: &fakeQueryExecutor{queryRow: func(query string, args ...any) rowScanner {
			capture.queries = append(capture.queries, query)
			return queryRow(query, args...)
		}},
	}
}

type prepayStoreCapture struct {
	queries            []string
	attemptInserts     int
	attemptUpdates     int
	orderUpdates       int
	observationInserts int
}

type fakePrepayTransactionStarter struct {
	transactions []prepayTransaction
	beginErr     error
	index        int
	isolation    sql.IsolationLevel
}

func (starter *fakePrepayTransactionStarter) beginPrepayTx(
	_ context.Context,
	options *sql.TxOptions,
) (prepayTransaction, error) {
	starter.isolation = options.Isolation
	if starter.beginErr != nil {
		return nil, starter.beginErr
	}
	if starter.index >= len(starter.transactions) {
		return nil, errors.New("unexpected prepay transaction")
	}
	tx := starter.transactions[starter.index]
	starter.index++
	return tx, nil
}

type fakePrepayTransaction struct {
	*fakeQueryExecutor
	commitErr  error
	committed  bool
	rolledBack bool
}

func (tx *fakePrepayTransaction) Commit() error {
	if tx.commitErr != nil {
		return tx.commitErr
	}
	tx.committed = true
	return nil
}

func (tx *fakePrepayTransaction) Rollback() error {
	tx.rolledBack = true
	return nil
}

func prepayAttemptScanValues(attempt payment.PaymentAttempt) []any {
	ownerToken := uuid.NullUUID{}
	if attempt.OwnerToken != nil {
		ownerToken = uuid.NullUUID{UUID: *attempt.OwnerToken, Valid: true}
	}
	return []any{
		attempt.ID,
		attempt.TenantID,
		attempt.OrderID,
		attempt.PrincipalID,
		attempt.GenerationID,
		attempt.OperationKind,
		attempt.IdempotencyKey,
		attempt.RequestFingerprint,
		attempt.Provider,
		attempt.OutTradeNo,
		attempt.PaymentAppID,
		attempt.PaymentMerchantID,
		attempt.Description,
		attempt.NotifyURL,
		attempt.OrderVersion,
		attempt.AmountCents,
		attempt.Currency,
		attempt.AttemptStatus,
		ownerToken,
		nullTime(attempt.LeaseExpiresAt),
		nullString(attempt.PrepayID),
		nullString(attempt.ClientTimestamp),
		nullString(attempt.ClientNonce),
		nullString(attempt.ClientPackage),
		nullString(attempt.ClientSignType),
		nullString(attempt.ClientPaySign),
		nullString(attempt.ProviderRequestID),
		nullString(attempt.LastErrorClass),
		nullTime(attempt.CompletedAt),
		attempt.Version,
		attempt.CreatedAt,
		attempt.UpdatedAt,
	}
}

func validStoreProviderResult() payment.ProviderPrepayResult {
	prepayID := "wx201410272009395522657a690389285100"
	return payment.ProviderPrepayResult{
		PrepayID:          prepayID,
		ProviderRequestID: "provider-request-1",
		Parameters: payment.MiniProgramPaymentParameters{
			AppID:     "wxXiangwan123",
			TimeStamp: "1789290000",
			NonceStr:  "nonce-1",
			Package:   "prepay_id=" + prepayID,
			SignType:  "RSA",
			PaySign:   "signed-payment-parameters",
		},
	}
}
