package paymentpostgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrInvalidPaymentQueryStore        = errors.New("invalid xiangwan payment query store")
	ErrPaymentQueryOrderNotFound       = errors.New("xiangwan payment query Order not found")
	ErrPaymentQueryUnavailable         = errors.New("xiangwan payment query is unavailable")
	ErrPaymentQueryGenerationInactive  = errors.New("xiangwan payment query generation is inactive")
	ErrPaymentQueryTransactionConflict = errors.New(
		"xiangwan payment query transaction conflict",
	)
)

type PaymentQueryStoreConfig struct {
	PaymentAppID               string
	PaymentMerchantID          string
	MerchantConfigGenerationID uuid.UUID
	Now                        func() time.Time
	NewUUID                    func() uuid.UUID
}

type PaymentQueryStore struct {
	transactions               prepayTransactionStarter
	paymentAppID               string
	paymentMerchantID          string
	merchantConfigGenerationID uuid.UUID
	now                        func() time.Time
	newUUID                    func() uuid.UUID
}

func NewPaymentQueryStore(
	database *sql.DB,
	config PaymentQueryStoreConfig,
) (*PaymentQueryStore, error) {
	if database == nil {
		return nil, ErrInvalidPaymentQueryStore
	}
	return newPaymentQueryStore(sqlPrepayTransactionStarter{database: database}, config)
}

func newPaymentQueryStore(
	transactions prepayTransactionStarter,
	config PaymentQueryStoreConfig,
) (*PaymentQueryStore, error) {
	if transactions == nil {
		return nil, ErrInvalidPaymentQueryStore
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	newUUID := config.NewUUID
	if newUUID == nil {
		newUUID = uuid.New
	}
	return &PaymentQueryStore{
		transactions:               transactions,
		paymentAppID:               config.PaymentAppID,
		paymentMerchantID:          config.PaymentMerchantID,
		merchantConfigGenerationID: config.MerchantConfigGenerationID,
		now:                        now,
		newUUID:                    newUUID,
	}, nil
}

func (store *PaymentQueryStore) AcquirePaymentQuery(
	ctx context.Context,
	command payment.PaymentQueryCommand,
) (payment.PaymentQueryAcquisition, error) {
	if store == nil || store.transactions == nil || store.now == nil ||
		store.newUUID == nil || ctx == nil ||
		payment.ValidatePaymentQueryCommand(command) != nil {
		return payment.PaymentQueryAcquisition{}, ErrInvalidPaymentQueryStore
	}
	now := store.now().UTC()
	leaseID := store.newUUID()
	ownerToken := store.newUUID()
	if now.IsZero() || leaseID == uuid.Nil || ownerToken == uuid.Nil {
		return payment.PaymentQueryAcquisition{}, ErrInvalidPaymentQueryStore
	}
	tx, err := store.transactions.beginPrepayTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelSerializable,
	})
	if err != nil {
		return payment.PaymentQueryAcquisition{}, fmt.Errorf(
			"begin xiangwan payment query acquisition: %w",
			err,
		)
	}
	defer func() { _ = tx.Rollback() }()
	activeGenerationID, err := lockPaymentQueryGeneration(ctx, tx, command.TenantID)
	if err != nil {
		return payment.PaymentQueryAcquisition{}, err
	}
	if activeGenerationID != command.GenerationID {
		return payment.PaymentQueryAcquisition{}, ErrPaymentQueryGenerationInactive
	}
	repository := &Repository{db: tx}
	order, err := repository.GetOrderForUpdate(ctx, command.TenantID, command.OrderID)
	if errors.Is(err, ErrOrderNotFound) {
		return payment.PaymentQueryAcquisition{}, ErrPaymentQueryOrderNotFound
	}
	if err != nil {
		return payment.PaymentQueryAcquisition{}, err
	}
	if order.PrincipalID != command.PrincipalID {
		return payment.PaymentQueryAcquisition{}, ErrPaymentQueryOrderNotFound
	}
	if err := requireOrderPaymentIdentity(
		order,
		store.paymentAppID,
		store.paymentMerchantID,
	); err != nil {
		return payment.PaymentQueryAcquisition{}, err
	}
	if err := requireOrderMerchantConfigGeneration(
		order,
		store.merchantConfigGenerationID,
	); err != nil {
		return payment.PaymentQueryAcquisition{}, err
	}
	if err := requireMerchantConfigGeneration(
		ctx,
		tx,
		command.TenantID,
		store.merchantConfigGenerationID,
		store.paymentAppID,
		store.paymentMerchantID,
		true,
	); err != nil {
		return payment.PaymentQueryAcquisition{}, err
	}
	if order.PaymentStatus == payment.OrderStatusPaidConfirmed ||
		order.PaymentStatus == payment.OrderStatusClosedUnpaid {
		if err := commitPaymentQueryTransaction(tx); err != nil {
			return payment.PaymentQueryAcquisition{}, err
		}
		return payment.PaymentQueryAcquisition{Order: order}, nil
	}
	if order.PaymentStatus != payment.OrderStatusPending &&
		order.PaymentStatus != payment.OrderStatusUnknown {
		return payment.PaymentQueryAcquisition{}, ErrPaymentQueryUnavailable
	}
	eligible, err := hasQueryablePrepayAttempt(ctx, tx, order)
	if err != nil {
		return payment.PaymentQueryAcquisition{}, err
	}
	if !eligible {
		return payment.PaymentQueryAcquisition{}, ErrPaymentQueryUnavailable
	}

	current, found, err := findPaymentQueryLeaseForUpdate(
		ctx,
		tx,
		command.TenantID,
		command.OrderID,
	)
	if err != nil {
		return payment.PaymentQueryAcquisition{}, err
	}
	var acquired payment.PaymentQueryLease
	if !found {
		acquired, err = payment.NewPaymentQueryLease(
			payment.CreatePaymentQueryLeaseCommand{
				ID:                leaseID,
				TenantID:          command.TenantID,
				OrderID:           command.OrderID,
				PrincipalID:       command.PrincipalID,
				GenerationID:      command.GenerationID,
				PaymentAppID:      order.PaymentAppID,
				PaymentMerchantID: order.PaymentMerchantID,
				OutTradeNo:        order.MerchantOrderNo,
				AmountCents:       order.PayableCents,
				OwnerToken:        ownerToken,
				Now:               now,
			},
		)
		if err != nil {
			return payment.PaymentQueryAcquisition{}, err
		}
		acquired, err = insertPaymentQueryLease(ctx, tx, acquired)
	} else {
		if !paymentQueryLeaseMatchesOrder(current, order, command.PrincipalID) {
			return payment.PaymentQueryAcquisition{}, ErrPaymentQueryTransactionConflict
		}
		acquired, err = payment.AcquirePaymentQueryLease(
			current,
			command.GenerationID,
			ownerToken,
			now,
		)
		if errors.Is(err, payment.ErrPaymentQueryLeaseActive) ||
			errors.Is(err, payment.ErrPaymentQueryNotDue) {
			if err := commitPaymentQueryTransaction(tx); err != nil {
				return payment.PaymentQueryAcquisition{}, err
			}
			return payment.PaymentQueryAcquisition{
				Lease: current,
				Order: order,
			}, nil
		}
		if err != nil {
			return payment.PaymentQueryAcquisition{}, err
		}
		acquired, err = updatePaymentQueryLease(ctx, tx, acquired, current)
	}
	if err != nil {
		return payment.PaymentQueryAcquisition{}, classifyPaymentQueryWriteError(err)
	}
	if err := commitPaymentQueryTransaction(tx); err != nil {
		return payment.PaymentQueryAcquisition{}, err
	}
	providerRequest := providerPaymentQueryRequest(acquired)
	return payment.PaymentQueryAcquisition{
		Lease:           acquired,
		Order:           order,
		InvocationToken: ownerToken,
		ProviderRequest: &providerRequest,
	}, nil
}

func (store *PaymentQueryStore) CompleteObservedPaymentQuery(
	ctx context.Context,
	acquisition payment.PaymentQueryAcquisition,
	observation payment.TransactionObservation,
) (payment.PaymentQueryResult, error) {
	return store.completePaymentQuery(
		ctx,
		acquisition,
		func(current payment.PaymentQueryLease, now time.Time) (payment.PaymentQueryLease, error) {
			return payment.CompletePaymentQueryObserved(
				current,
				acquisition.InvocationToken,
				observation,
				now,
			)
		},
		&observation,
		payment.PaymentConvergence{},
		false,
	)
}

func (store *PaymentQueryStore) CompleteFailedPaymentQuery(
	ctx context.Context,
	acquisition payment.PaymentQueryAcquisition,
	failure error,
) (payment.PaymentQueryResult, error) {
	return store.completePaymentQuery(
		ctx,
		acquisition,
		func(current payment.PaymentQueryLease, now time.Time) (payment.PaymentQueryLease, error) {
			return payment.CompletePaymentQueryFailed(
				current,
				acquisition.InvocationToken,
				failure,
				now,
			)
		},
		nil,
		payment.PaymentConvergence{},
		true,
	)
}

func (store *PaymentQueryStore) CompleteFailedPaymentQueryWithObservation(
	ctx context.Context,
	acquisition payment.PaymentQueryAcquisition,
	observation payment.TransactionObservation,
	failure error,
) (payment.PaymentQueryResult, error) {
	if payment.ValidateTransactionObservation(observation) != nil {
		return payment.PaymentQueryResult{}, ErrInvalidPaymentQueryStore
	}
	return store.completePaymentQuery(
		ctx,
		acquisition,
		func(current payment.PaymentQueryLease, now time.Time) (payment.PaymentQueryLease, error) {
			return payment.CompletePaymentQueryFailed(
				current,
				acquisition.InvocationToken,
				failure,
				now,
			)
		},
		&observation,
		payment.PaymentConvergence{},
		true,
	)
}

func (store *PaymentQueryStore) CompleteConvergedPaymentQuery(
	ctx context.Context,
	acquisition payment.PaymentQueryAcquisition,
	convergence payment.PaymentConvergence,
) (payment.PaymentQueryResult, error) {
	if convergence.Order.PaymentStatus != payment.OrderStatusPaidConfirmed ||
		convergence.Order.ID != acquisition.Order.ID ||
		convergence.Order.TenantID != acquisition.Order.TenantID ||
		(convergence.Disposition != payment.PaymentConfirmationDispositionParticipationConfirmed &&
			convergence.Disposition != payment.PaymentConfirmationDispositionRefundRequired) {
		return payment.PaymentQueryResult{}, ErrInvalidPaymentQueryStore
	}
	return store.completePaymentQuery(
		ctx,
		acquisition,
		func(current payment.PaymentQueryLease, now time.Time) (payment.PaymentQueryLease, error) {
			return payment.CompletePaymentQueryConverged(
				current,
				acquisition.InvocationToken,
				convergence.ProviderRequestID,
				now,
			)
		},
		nil,
		convergence,
		false,
	)
}

func (store *PaymentQueryStore) CompleteClosedPaymentQuery(
	ctx context.Context,
	acquisition payment.PaymentQueryAcquisition,
	observation payment.TransactionObservation,
	convergence payment.PaymentConvergence,
) (payment.PaymentQueryResult, error) {
	if convergence.Order.PaymentStatus != payment.OrderStatusClosedUnpaid ||
		convergence.Order.ID != acquisition.Order.ID ||
		convergence.Order.TenantID != acquisition.Order.TenantID ||
		convergence.Disposition != payment.PaymentConfirmationDispositionNone ||
		payment.ValidateTransactionObservation(observation) != nil ||
		!payment.IsProviderTerminalUnpaidState(observation.TradeState) {
		return payment.PaymentQueryResult{}, ErrInvalidPaymentQueryStore
	}
	return store.completePaymentQuery(
		ctx,
		acquisition,
		func(current payment.PaymentQueryLease, now time.Time) (payment.PaymentQueryLease, error) {
			return payment.CompletePaymentQueryClosed(
				current,
				acquisition.InvocationToken,
				observation,
				now,
			)
		},
		nil,
		convergence,
		false,
	)
}

type paymentQueryTransition func(
	payment.PaymentQueryLease,
	time.Time,
) (payment.PaymentQueryLease, error)

func (store *PaymentQueryStore) completePaymentQuery(
	ctx context.Context,
	acquisition payment.PaymentQueryAcquisition,
	transition paymentQueryTransition,
	observation *payment.TransactionObservation,
	convergence payment.PaymentConvergence,
	markOrderUnknown bool,
) (payment.PaymentQueryResult, error) {
	if store == nil || store.transactions == nil || store.now == nil ||
		ctx == nil || transition == nil || acquisition.InvocationToken == uuid.Nil ||
		acquisition.Lease.ID == uuid.Nil {
		return payment.PaymentQueryResult{}, ErrInvalidPaymentQueryStore
	}
	now := store.now().UTC()
	if now.IsZero() {
		return payment.PaymentQueryResult{}, ErrInvalidPaymentQueryStore
	}
	tx, err := store.transactions.beginPrepayTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelSerializable,
	})
	if err != nil {
		return payment.PaymentQueryResult{}, fmt.Errorf(
			"begin xiangwan payment query completion: %w",
			err,
		)
	}
	defer func() { _ = tx.Rollback() }()

	repository := &Repository{db: tx}
	currentOrder, err := repository.GetOrderForUpdate(
		ctx,
		acquisition.Lease.TenantID,
		acquisition.Lease.OrderID,
	)
	if err != nil {
		return payment.PaymentQueryResult{}, err
	}
	current, err := getPaymentQueryLeaseForUpdate(
		ctx,
		tx,
		acquisition.Lease.TenantID,
		acquisition.Lease.ID,
	)
	if err != nil {
		return payment.PaymentQueryResult{}, err
	}
	if !samePaymentQueryLeaseIdentity(current, acquisition.Lease) {
		return payment.PaymentQueryResult{}, ErrPaymentQueryTransactionConflict
	}
	updated, err := transition(current, now)
	if err != nil {
		return payment.PaymentQueryResult{}, err
	}
	if observation != nil {
		if err := insertPaymentTransactionObservation(ctx, tx, *observation); err != nil {
			return payment.PaymentQueryResult{}, err
		}
	}
	updated, err = updatePaymentQueryLease(ctx, tx, updated, current)
	if err != nil {
		return payment.PaymentQueryResult{}, classifyPaymentQueryWriteError(err)
	}
	if markOrderUnknown && currentOrder.PaymentStatus == payment.OrderStatusPending {
		unknownOrder, changed, transitionErr := payment.MarkOrderUnknown(
			currentOrder,
			now,
		)
		if transitionErr != nil {
			return payment.PaymentQueryResult{}, transitionErr
		}
		if changed {
			currentOrder, err = repository.UpdateOrderPayment(
				ctx,
				unknownOrder,
				currentOrder.Version,
			)
			if err != nil {
				return payment.PaymentQueryResult{}, classifyPaymentQueryWriteError(err)
			}
		}
	}
	if convergence.Order.ID != uuid.Nil {
		switch convergence.Order.PaymentStatus {
		case payment.OrderStatusPaidConfirmed:
			if currentOrder.PaymentStatus != payment.OrderStatusPaidConfirmed ||
				currentOrder.WeChatTransactionID == nil ||
				convergence.Order.WeChatTransactionID == nil ||
				*currentOrder.WeChatTransactionID !=
					*convergence.Order.WeChatTransactionID {
				return payment.PaymentQueryResult{}, ErrPaymentQueryTransactionConflict
			}
		case payment.OrderStatusClosedUnpaid:
			if currentOrder.PaymentStatus != payment.OrderStatusClosedUnpaid {
				return payment.PaymentQueryResult{}, ErrPaymentQueryTransactionConflict
			}
		default:
			return payment.PaymentQueryResult{}, ErrPaymentQueryTransactionConflict
		}
	}
	if err := commitPaymentQueryTransaction(tx); err != nil {
		return payment.PaymentQueryResult{}, err
	}
	result := payment.PaymentQueryResult{
		Order:               currentOrder,
		QueryStatus:         updated.QueryStatus,
		NextQueryAt:         clonePaymentQueryTime(updated.NextQueryAt),
		RetryPaymentAllowed: payment.PaymentQueryAllowsRetry(currentOrder, updated),
	}
	// The query failure remains truthful in its lease; a separately proven
	// prepay rejection may already have closed the local Order atomically.
	if currentOrder.PaymentStatus == payment.OrderStatusClosedUnpaid {
		result.QueryStatus = payment.PaymentQueryStatusClosed
		result.NextQueryAt = nil
	}
	if convergence.Order.ID != uuid.Nil {
		result.Disposition = convergence.Disposition
	}
	return result, nil
}

func lockPaymentQueryGeneration(
	ctx context.Context,
	tx prepayTransaction,
	tenantID uuid.UUID,
) (uuid.UUID, error) {
	var generationID uuid.UUID
	var writeEpoch int64
	err := tx.queryRowContext(ctx, `
SELECT runtime_generation.active_generation_id, runtime_generation.write_epoch
FROM xiangwan_runtime_generations AS runtime_generation
JOIN tenants AS tenant
  ON tenant.id = runtime_generation.tenant_id
WHERE runtime_generation.singleton_id = 1
  AND runtime_generation.scope_key = 'wq-xiangwan'
  AND runtime_generation.tenant_id = $1
  AND runtime_generation.active_generation_id IS NOT NULL
  AND runtime_generation.write_epoch > 0
  AND runtime_generation.bootstrap_completed_at IS NOT NULL
  AND tenant.type = 'business'
  AND tenant.metadata @> '{"product_code":"wq-xiangwan"}'::jsonb
FOR SHARE OF runtime_generation
`, tenantID).Scan(&generationID, &writeEpoch)
	if errors.Is(err, sql.ErrNoRows) {
		return uuid.Nil, ErrPaymentQueryGenerationInactive
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("lock xiangwan payment query generation: %w", err)
	}
	return generationID, nil
}

func hasQueryablePrepayAttempt(
	ctx context.Context,
	tx prepayTransaction,
	order payment.Order,
) (bool, error) {
	var eligible bool
	err := tx.queryRowContext(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM xiangwan_payment_attempts
    WHERE tenant_id = $1
      AND order_id = $2
      AND principal_id = $3
      AND payment_app_id = $4
      AND payment_merchant_id = $5
      AND out_trade_no = $6
      AND amount_cents = $7
      AND attempt_status IN ('ready', 'unknown')
)
`,
		order.TenantID,
		order.ID,
		order.PrincipalID,
		order.PaymentAppID,
		order.PaymentMerchantID,
		order.MerchantOrderNo,
		order.PayableCents,
	).Scan(&eligible)
	if err != nil {
		return false, fmt.Errorf("read xiangwan queryable prepay attempt: %w", err)
	}
	return eligible, nil
}

const paymentQueryLeaseColumns = `
    id, tenant_id, order_id, principal_id, generation_id,
    payment_app_id, payment_merchant_id, out_trade_no, amount_cents, currency,
    query_status, owner_token, lease_expires_at, next_query_at,
    last_trade_state, last_error_class, last_provider_request_id, completed_at,
    version, created_at, updated_at
`

func findPaymentQueryLeaseForUpdate(
	ctx context.Context,
	tx prepayTransaction,
	tenantID uuid.UUID,
	orderID uuid.UUID,
) (payment.PaymentQueryLease, bool, error) {
	value, err := scanPaymentQueryLease(tx.queryRowContext(ctx, `
SELECT `+paymentQueryLeaseColumns+`
FROM xiangwan_payment_query_leases
WHERE tenant_id = $1 AND order_id = $2
FOR UPDATE
`, tenantID, orderID))
	if errors.Is(err, sql.ErrNoRows) {
		return payment.PaymentQueryLease{}, false, nil
	}
	if err != nil {
		return payment.PaymentQueryLease{}, false, fmt.Errorf(
			"find xiangwan payment query lease: %w",
			err,
		)
	}
	return value, true, nil
}

func getPaymentQueryLeaseForUpdate(
	ctx context.Context,
	tx prepayTransaction,
	tenantID uuid.UUID,
	leaseID uuid.UUID,
) (payment.PaymentQueryLease, error) {
	value, err := scanPaymentQueryLease(tx.queryRowContext(ctx, `
SELECT `+paymentQueryLeaseColumns+`
FROM xiangwan_payment_query_leases
WHERE tenant_id = $1 AND id = $2
FOR UPDATE
`, tenantID, leaseID))
	if errors.Is(err, sql.ErrNoRows) {
		return payment.PaymentQueryLease{}, ErrPaymentQueryTransactionConflict
	}
	if err != nil {
		return payment.PaymentQueryLease{}, fmt.Errorf(
			"get xiangwan payment query lease: %w",
			err,
		)
	}
	return value, nil
}

func insertPaymentQueryLease(
	ctx context.Context,
	tx prepayTransaction,
	value payment.PaymentQueryLease,
) (payment.PaymentQueryLease, error) {
	return scanPaymentQueryLease(tx.queryRowContext(ctx, `
INSERT INTO xiangwan_payment_query_leases (
    id, tenant_id, order_id, principal_id, generation_id,
    payment_app_id, payment_merchant_id, out_trade_no, amount_cents, currency,
    query_status, owner_token, lease_expires_at, next_query_at,
    last_trade_state, last_error_class, last_provider_request_id, completed_at,
    version, created_at, updated_at
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9, $10,
    $11, $12, $13, $14,
    $15, $16, $17, $18,
    $19, $20, $21
)
RETURNING `+paymentQueryLeaseColumns,
		paymentQueryLeaseArguments(value)...,
	))
}

func updatePaymentQueryLease(
	ctx context.Context,
	tx prepayTransaction,
	updated payment.PaymentQueryLease,
	current payment.PaymentQueryLease,
) (payment.PaymentQueryLease, error) {
	value, err := scanPaymentQueryLease(tx.queryRowContext(ctx, `
UPDATE xiangwan_payment_query_leases
SET generation_id = $4,
    query_status = $5,
    owner_token = $6,
    lease_expires_at = $7,
    next_query_at = $8,
    last_trade_state = $9,
    last_error_class = $10,
    last_provider_request_id = $11,
    completed_at = $12,
    version = version + 1,
    updated_at = $13
WHERE tenant_id = $1
  AND id = $2
  AND version = $3
  AND query_status = $15
  AND owner_token IS NOT DISTINCT FROM $14
RETURNING `+paymentQueryLeaseColumns,
		updated.TenantID,
		updated.ID,
		current.Version,
		updated.GenerationID,
		updated.QueryStatus,
		updated.OwnerToken,
		updated.LeaseExpiresAt,
		updated.NextQueryAt,
		updated.LastTradeState,
		updated.LastErrorClass,
		updated.LastProviderRequestID,
		updated.CompletedAt,
		updated.UpdatedAt,
		current.OwnerToken,
		current.QueryStatus,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return payment.PaymentQueryLease{}, payment.ErrPaymentQueryLeaseLost
	}
	return value, err
}

func insertPaymentTransactionObservation(
	ctx context.Context,
	tx queryExecutor,
	observation payment.TransactionObservation,
) error {
	if payment.ValidateTransactionObservation(observation) != nil {
		return ErrInvalidPaymentQueryStore
	}
	var insertedID uuid.UUID
	err := tx.queryRowContext(ctx, `
INSERT INTO xiangwan_payment_transaction_observations (
    id, tenant_id, order_id, principal_id,
    observation_source, source_key, provider_request_id,
    payment_app_id, payment_merchant_id, out_trade_no,
    transaction_id, trade_type, trade_state, amount_cents, currency,
    success_at, payload_digest, observed_at
) VALUES (
    $1, $2, $3, $4,
    $5, $6, NULLIF($7, ''),
    $8, $9, $10,
    NULLIF($11, ''), $12, $13, $14, $15,
    $16, $17, $18
)
ON CONFLICT (observation_source, payment_merchant_id, source_key) DO NOTHING
RETURNING id
`,
		observation.ID,
		observation.TenantID,
		observation.OrderID,
		observation.PrincipalID,
		observation.ObservationSource,
		observation.SourceKey,
		observation.ProviderRequestID,
		observation.PaymentAppID,
		observation.PaymentMerchantID,
		observation.OutTradeNo,
		observation.TransactionID,
		observation.TradeType,
		observation.TradeState,
		observation.AmountCents,
		observation.Currency,
		observation.SuccessAt,
		observation.PayloadDigest,
		observation.ObservedAt,
	).Scan(&insertedID)
	if errors.Is(err, sql.ErrNoRows) {
		existing, readErr := getPaymentTransactionObservationBySource(
			ctx,
			tx,
			observation.ObservationSource,
			observation.PaymentMerchantID,
			observation.SourceKey,
		)
		if readErr != nil {
			return readErr
		}
		if samePaymentTransactionObservation(existing, observation) {
			return nil
		}
		return ErrPaymentConfirmationConflict
	}
	if err != nil {
		return fmt.Errorf("record xiangwan payment transaction observation: %w", err)
	}
	return nil
}

func getPaymentTransactionObservationBySource(
	ctx context.Context,
	tx queryExecutor,
	source string,
	merchantID string,
	sourceKey string,
) (payment.TransactionObservation, error) {
	var (
		value             payment.TransactionObservation
		providerRequestID sql.NullString
		transactionID     sql.NullString
		successAt         sql.NullTime
	)
	err := tx.queryRowContext(ctx, `
SELECT
    id, tenant_id, order_id, principal_id,
    observation_source, source_key, provider_request_id,
    payment_app_id, payment_merchant_id, out_trade_no,
    transaction_id, trade_type, trade_state, amount_cents, currency,
    success_at, payload_digest, observed_at
FROM xiangwan_payment_transaction_observations
WHERE observation_source = $1
  AND payment_merchant_id = $2
  AND source_key = $3
`, source, merchantID, sourceKey).Scan(
		&value.ID,
		&value.TenantID,
		&value.OrderID,
		&value.PrincipalID,
		&value.ObservationSource,
		&value.SourceKey,
		&providerRequestID,
		&value.PaymentAppID,
		&value.PaymentMerchantID,
		&value.OutTradeNo,
		&transactionID,
		&value.TradeType,
		&value.TradeState,
		&value.AmountCents,
		&value.Currency,
		&successAt,
		&value.PayloadDigest,
		&value.ObservedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return payment.TransactionObservation{}, ErrPaymentConfirmationConflict
	}
	if err != nil {
		return payment.TransactionObservation{}, fmt.Errorf(
			"read xiangwan payment transaction observation replay: %w",
			err,
		)
	}
	if providerRequestID.Valid {
		value.ProviderRequestID = providerRequestID.String
	}
	if transactionID.Valid {
		value.TransactionID = transactionID.String
	}
	value.SuccessAt = nullTimePointer(successAt)
	return value, nil
}

func samePaymentTransactionObservation(
	left payment.TransactionObservation,
	right payment.TransactionObservation,
) bool {
	return left.TenantID == right.TenantID &&
		left.OrderID == right.OrderID &&
		left.PrincipalID == right.PrincipalID &&
		left.ObservationSource == right.ObservationSource &&
		left.SourceKey == right.SourceKey &&
		left.ProviderRequestID == right.ProviderRequestID &&
		left.PaymentAppID == right.PaymentAppID &&
		left.PaymentMerchantID == right.PaymentMerchantID &&
		left.OutTradeNo == right.OutTradeNo &&
		left.TransactionID == right.TransactionID &&
		left.TradeType == right.TradeType &&
		left.TradeState == right.TradeState &&
		left.AmountCents == right.AmountCents &&
		left.Currency == right.Currency &&
		optionalPaymentTimeEqual(left.SuccessAt, right.SuccessAt) &&
		left.PayloadDigest == right.PayloadDigest
}

func optionalPaymentTimeEqual(left *time.Time, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func paymentQueryLeaseArguments(value payment.PaymentQueryLease) []any {
	return []any{
		value.ID,
		value.TenantID,
		value.OrderID,
		value.PrincipalID,
		value.GenerationID,
		value.PaymentAppID,
		value.PaymentMerchantID,
		value.OutTradeNo,
		value.AmountCents,
		value.Currency,
		value.QueryStatus,
		value.OwnerToken,
		value.LeaseExpiresAt,
		value.NextQueryAt,
		value.LastTradeState,
		value.LastErrorClass,
		value.LastProviderRequestID,
		value.CompletedAt,
		value.Version,
		value.CreatedAt,
		value.UpdatedAt,
	}
}

func scanPaymentQueryLease(row rowScanner) (payment.PaymentQueryLease, error) {
	var value payment.PaymentQueryLease
	var ownerToken uuid.NullUUID
	var leaseExpiresAt sql.NullTime
	var nextQueryAt sql.NullTime
	var lastTradeState sql.NullString
	var lastErrorClass sql.NullString
	var lastProviderRequestID sql.NullString
	var completedAt sql.NullTime
	err := row.Scan(
		&value.ID,
		&value.TenantID,
		&value.OrderID,
		&value.PrincipalID,
		&value.GenerationID,
		&value.PaymentAppID,
		&value.PaymentMerchantID,
		&value.OutTradeNo,
		&value.AmountCents,
		&value.Currency,
		&value.QueryStatus,
		&ownerToken,
		&leaseExpiresAt,
		&nextQueryAt,
		&lastTradeState,
		&lastErrorClass,
		&lastProviderRequestID,
		&completedAt,
		&value.Version,
		&value.CreatedAt,
		&value.UpdatedAt,
	)
	if err != nil {
		return payment.PaymentQueryLease{}, err
	}
	if ownerToken.Valid {
		value.OwnerToken = &ownerToken.UUID
	}
	value.LeaseExpiresAt = nullTimePointer(leaseExpiresAt)
	value.NextQueryAt = nullTimePointer(nextQueryAt)
	if lastTradeState.Valid {
		tradeState := payment.ProviderTradeState(lastTradeState.String)
		value.LastTradeState = &tradeState
	}
	value.LastErrorClass = nullStringPointer(lastErrorClass)
	value.LastProviderRequestID = nullStringPointer(lastProviderRequestID)
	value.CompletedAt = nullTimePointer(completedAt)
	return value, nil
}

func providerPaymentQueryRequest(
	lease payment.PaymentQueryLease,
) payment.ProviderPaymentQueryRequest {
	return payment.ProviderPaymentQueryRequest{
		AppID:       lease.PaymentAppID,
		MerchantID:  lease.PaymentMerchantID,
		OutTradeNo:  lease.OutTradeNo,
		AmountCents: lease.AmountCents,
		Currency:    lease.Currency,
	}
}

func paymentQueryLeaseMatchesOrder(
	lease payment.PaymentQueryLease,
	order payment.Order,
	principalID uuid.UUID,
) bool {
	return lease.TenantID == order.TenantID && lease.OrderID == order.ID &&
		lease.PrincipalID == principalID && order.PrincipalID == principalID &&
		lease.PaymentAppID == order.PaymentAppID &&
		lease.PaymentMerchantID == order.PaymentMerchantID &&
		lease.OutTradeNo == order.MerchantOrderNo &&
		lease.AmountCents == order.PayableCents &&
		lease.Currency == payment.PaymentQueryCurrency
}

func samePaymentQueryLeaseIdentity(
	left payment.PaymentQueryLease,
	right payment.PaymentQueryLease,
) bool {
	return left.ID == right.ID && left.TenantID == right.TenantID &&
		left.OrderID == right.OrderID && left.PrincipalID == right.PrincipalID &&
		left.PaymentAppID == right.PaymentAppID &&
		left.PaymentMerchantID == right.PaymentMerchantID &&
		left.OutTradeNo == right.OutTradeNo &&
		left.AmountCents == right.AmountCents && left.Currency == right.Currency
}

func clonePaymentQueryTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func commitPaymentQueryTransaction(tx prepayTransaction) error {
	if err := tx.Commit(); err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) && postgresError.Code == "40001" {
			return fmt.Errorf("%w: %v", ErrPaymentQueryTransactionConflict, err)
		}
		return fmt.Errorf("commit xiangwan payment query transaction: %w", err)
	}
	return nil
}

func classifyPaymentQueryWriteError(err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) &&
		(postgresError.Code == "23505" || postgresError.Code == "23514") {
		return fmt.Errorf("%w: %v", ErrPaymentQueryTransactionConflict, err)
	}
	return err
}
