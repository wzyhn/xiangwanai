package paymentpostgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrInvalidPaymentCloseJobStore = errors.New(
		"invalid xiangwan payment close job store",
	)
	ErrPaymentCloseJobTransaction = errors.New(
		"xiangwan payment close job transaction conflict",
	)
)

type PaymentCloseJobStoreConfig struct {
	Now     func() time.Time
	NewUUID func() uuid.UUID
}

type PaymentCloseJobStore struct {
	transactions prepayTransactionStarter
	now          func() time.Time
	newUUID      func() uuid.UUID
}

var _ payment.PaymentCloseJobStore = (*PaymentCloseJobStore)(nil)

func NewPaymentCloseJobStore(
	database *sql.DB,
	config PaymentCloseJobStoreConfig,
) (*PaymentCloseJobStore, error) {
	if database == nil {
		return nil, ErrInvalidPaymentCloseJobStore
	}
	return newPaymentCloseJobStore(
		sqlPrepayTransactionStarter{database: database},
		config,
	)
}

func newPaymentCloseJobStore(
	transactions prepayTransactionStarter,
	config PaymentCloseJobStoreConfig,
) (*PaymentCloseJobStore, error) {
	if transactions == nil {
		return nil, ErrInvalidPaymentCloseJobStore
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	newUUID := config.NewUUID
	if newUUID == nil {
		newUUID = uuid.New
	}
	return &PaymentCloseJobStore{
		transactions: transactions,
		now:          now,
		newUUID:      newUUID,
	}, nil
}

func (store *PaymentCloseJobStore) AcquirePaymentCloseJob(
	ctx context.Context,
	tenantID uuid.UUID,
	generationID uuid.UUID,
	merchantConfigGenerationID uuid.UUID,
	paymentAppID string,
	paymentMerchantID string,
) (payment.PaymentCloseJobAcquisition, error) {
	if store == nil || store.transactions == nil || store.now == nil ||
		store.newUUID == nil || ctx == nil || tenantID == uuid.Nil ||
		generationID == uuid.Nil || merchantConfigGenerationID == uuid.Nil ||
		paymentAppID == "" || paymentAppID != strings.TrimSpace(paymentAppID) ||
		paymentMerchantID == "" ||
		paymentMerchantID != strings.TrimSpace(paymentMerchantID) {
		return payment.PaymentCloseJobAcquisition{},
			ErrInvalidPaymentCloseJobStore
	}
	now := store.now().UTC()
	ownerToken := store.newUUID()
	if now.IsZero() || ownerToken == uuid.Nil {
		return payment.PaymentCloseJobAcquisition{},
			ErrInvalidPaymentCloseJobStore
	}
	tx, err := store.transactions.beginPrepayTx(
		ctx,
		&sql.TxOptions{Isolation: sql.LevelSerializable},
	)
	if err != nil {
		return payment.PaymentCloseJobAcquisition{}, fmt.Errorf(
			"begin xiangwan payment close acquisition: %w",
			err,
		)
	}
	defer func() { _ = tx.Rollback() }()

	activeGenerationID, err := lockPaymentQueryGeneration(ctx, tx, tenantID)
	if err != nil {
		return payment.PaymentCloseJobAcquisition{}, err
	}
	if activeGenerationID != generationID {
		return payment.PaymentCloseJobAcquisition{},
			ErrPaymentQueryGenerationInactive
	}
	current, found, err := findDuePaymentCloseJobForUpdate(
		ctx,
		tx,
		tenantID,
		now,
		merchantConfigGenerationID,
		paymentAppID,
		paymentMerchantID,
	)
	if err != nil {
		return payment.PaymentCloseJobAcquisition{}, err
	}
	if !found {
		if err := commitPaymentCloseJobTransaction(tx); err != nil {
			return payment.PaymentCloseJobAcquisition{}, err
		}
		return payment.PaymentCloseJobAcquisition{}, nil
	}
	acquired, err := payment.AcquirePaymentCloseJob(
		current,
		generationID,
		ownerToken,
		now,
	)
	if err != nil {
		return payment.PaymentCloseJobAcquisition{}, err
	}
	acquired, err = updatePaymentCloseJob(ctx, tx, acquired, current)
	if err != nil {
		return payment.PaymentCloseJobAcquisition{},
			classifyPaymentCloseJobWriteError(err)
	}

	repository := &Repository{db: tx}
	order, err := repository.GetOrderForUpdate(ctx, tenantID, acquired.OrderID)
	if err != nil {
		return payment.PaymentCloseJobAcquisition{}, err
	}
	if !paymentCloseJobMatchesOrder(acquired, order) {
		return payment.PaymentCloseJobAcquisition{},
			ErrPaymentCloseJobTransaction
	}
	if order.PaymentStatus == payment.OrderStatusPaidConfirmed {
		completed, transitionErr := payment.CompletePaymentCloseJob(
			acquired,
			ownerToken,
			payment.PaymentCloseJobCompletion{
				Resolution: payment.PaymentCloseResolutionPaymentConverged,
			},
			now,
		)
		if transitionErr != nil {
			return payment.PaymentCloseJobAcquisition{}, transitionErr
		}
		completed, err = updatePaymentCloseJob(ctx, tx, completed, acquired)
		if err != nil {
			return payment.PaymentCloseJobAcquisition{},
				classifyPaymentCloseJobWriteError(err)
		}
		if err := commitPaymentCloseJobTransaction(tx); err != nil {
			return payment.PaymentCloseJobAcquisition{}, err
		}
		return payment.PaymentCloseJobAcquisition{
			Found: true,
			Job:   completed,
			Order: order,
		}, nil
	}
	if order.PaymentStatus != payment.OrderStatusClosedUnpaid {
		return payment.PaymentCloseJobAcquisition{},
			ErrPaymentCloseJobTransaction
	}
	if err := commitPaymentCloseJobTransaction(tx); err != nil {
		return payment.PaymentCloseJobAcquisition{}, err
	}
	queryRequest := payment.ProviderPaymentQueryRequest{
		AppID:       acquired.PaymentAppID,
		MerchantID:  acquired.PaymentMerchantID,
		OutTradeNo:  acquired.OutTradeNo,
		AmountCents: acquired.AmountCents,
		Currency:    payment.PaymentQueryCurrency,
	}
	closeRequest := payment.ProviderPaymentCloseRequest{
		AppID:      acquired.PaymentAppID,
		MerchantID: acquired.PaymentMerchantID,
		OutTradeNo: acquired.OutTradeNo,
	}
	return payment.PaymentCloseJobAcquisition{
		Found:           true,
		Job:             acquired,
		Order:           order,
		InvocationToken: ownerToken,
		QueryRequest:    &queryRequest,
		CloseRequest:    &closeRequest,
	}, nil
}

func (store *PaymentCloseJobStore) CompletePaymentCloseJob(
	ctx context.Context,
	acquisition payment.PaymentCloseJobAcquisition,
	observation *payment.TransactionObservation,
	completion payment.PaymentCloseJobCompletion,
) (payment.PaymentCloseJob, error) {
	if store == nil || store.transactions == nil || store.now == nil ||
		ctx == nil || !acquisition.Found || acquisition.InvocationToken == uuid.Nil ||
		acquisition.Job.ID == uuid.Nil {
		return payment.PaymentCloseJob{}, ErrInvalidPaymentCloseJobStore
	}
	now := store.now().UTC()
	if now.IsZero() {
		return payment.PaymentCloseJob{}, ErrInvalidPaymentCloseJobStore
	}
	tx, err := store.transactions.beginPrepayTx(
		ctx,
		&sql.TxOptions{Isolation: sql.LevelSerializable},
	)
	if err != nil {
		return payment.PaymentCloseJob{}, fmt.Errorf(
			"begin xiangwan payment close completion: %w",
			err,
		)
	}
	defer func() { _ = tx.Rollback() }()

	current, err := getPaymentCloseJobForUpdate(
		ctx,
		tx,
		acquisition.Job.TenantID,
		acquisition.Job.ID,
	)
	if err != nil {
		return payment.PaymentCloseJob{}, err
	}
	if !samePaymentCloseJobIdentity(current, acquisition.Job) {
		return payment.PaymentCloseJob{}, ErrPaymentCloseJobTransaction
	}
	repository := &Repository{db: tx}
	order, err := repository.GetOrderForUpdate(
		ctx,
		current.TenantID,
		current.OrderID,
	)
	if err != nil {
		return payment.PaymentCloseJob{}, err
	}
	if !paymentCloseJobMatchesOrder(current, order) {
		return payment.PaymentCloseJob{}, ErrPaymentCloseJobTransaction
	}
	if observation != nil {
		if !paymentCloseObservationMatchesJob(*observation, current) {
			return payment.PaymentCloseJob{}, ErrPaymentCloseJobTransaction
		}
		if err := insertPaymentTransactionObservation(
			ctx,
			tx,
			*observation,
		); err != nil {
			return payment.PaymentCloseJob{}, err
		}
	}
	if order.PaymentStatus == payment.OrderStatusPaidConfirmed {
		completion = payment.PaymentCloseJobCompletion{
			Resolution: payment.PaymentCloseResolutionPaymentConverged,
		}
	}
	var updated payment.PaymentCloseJob
	if completion.Resolution == payment.PaymentCloseResolutionNone {
		if order.PaymentStatus != payment.OrderStatusClosedUnpaid {
			return payment.PaymentCloseJob{}, ErrPaymentCloseJobTransaction
		}
		updated, err = payment.RetryPaymentCloseJob(
			current,
			acquisition.InvocationToken,
			completion,
			now,
		)
	} else {
		if !paymentCloseResolutionMatchesOrder(completion.Resolution, order) {
			return payment.PaymentCloseJob{}, ErrPaymentCloseJobTransaction
		}
		updated, err = payment.CompletePaymentCloseJob(
			current,
			acquisition.InvocationToken,
			completion,
			now,
		)
	}
	if err != nil {
		return payment.PaymentCloseJob{}, err
	}
	updated, err = updatePaymentCloseJob(ctx, tx, updated, current)
	if err != nil {
		return payment.PaymentCloseJob{}, classifyPaymentCloseJobWriteError(err)
	}
	if err := commitPaymentCloseJobTransaction(tx); err != nil {
		return payment.PaymentCloseJob{}, err
	}
	return updated, nil
}

const paymentCloseJobColumns = `
    id, tenant_id, order_id, principal_id,
    payment_app_id, payment_merchant_id, merchant_config_generation_id,
    out_trade_no, amount_cents,
    job_status, generation_id, owner_token, lease_expires_at, next_attempt_at,
    attempt_count, last_trade_state, last_error_class, last_error_code,
    last_provider_request_id, resolution, completed_at,
    version, created_at, updated_at
`

func findDuePaymentCloseJobForUpdate(
	ctx context.Context,
	tx prepayTransaction,
	tenantID uuid.UUID,
	now time.Time,
	merchantConfigGenerationID uuid.UUID,
	paymentAppID string,
	paymentMerchantID string,
) (payment.PaymentCloseJob, bool, error) {
	value, err := scanPaymentCloseJob(tx.queryRowContext(ctx, `
SELECT `+paymentCloseJobColumns+`
FROM xiangwan_payment_close_jobs
WHERE tenant_id = $1
  AND (
      (job_status IN ('pending', 'retry') AND next_attempt_at <= $2)
      OR (job_status = 'in_progress' AND lease_expires_at <= $2)
  )
  AND (merchant_config_generation_id IS NULL OR merchant_config_generation_id = $3)
  AND payment_app_id = $4
  AND payment_merchant_id = $5
ORDER BY COALESCE(lease_expires_at, next_attempt_at), order_id
LIMIT 1
FOR UPDATE SKIP LOCKED
`, tenantID, now, merchantConfigGenerationID, paymentAppID, paymentMerchantID))
	if errors.Is(err, sql.ErrNoRows) {
		return payment.PaymentCloseJob{}, false, nil
	}
	if err != nil {
		return payment.PaymentCloseJob{}, false, fmt.Errorf(
			"find xiangwan payment close job: %w",
			err,
		)
	}
	return value, true, nil
}

func getPaymentCloseJobForUpdate(
	ctx context.Context,
	tx prepayTransaction,
	tenantID uuid.UUID,
	jobID uuid.UUID,
) (payment.PaymentCloseJob, error) {
	value, err := scanPaymentCloseJob(tx.queryRowContext(ctx, `
SELECT `+paymentCloseJobColumns+`
FROM xiangwan_payment_close_jobs
WHERE tenant_id = $1 AND id = $2
FOR UPDATE
`, tenantID, jobID))
	if errors.Is(err, sql.ErrNoRows) {
		return payment.PaymentCloseJob{}, ErrPaymentCloseJobTransaction
	}
	if err != nil {
		return payment.PaymentCloseJob{}, fmt.Errorf(
			"get xiangwan payment close job: %w",
			err,
		)
	}
	return value, nil
}

func updatePaymentCloseJob(
	ctx context.Context,
	tx prepayTransaction,
	updated payment.PaymentCloseJob,
	current payment.PaymentCloseJob,
) (payment.PaymentCloseJob, error) {
	value, err := scanPaymentCloseJob(tx.queryRowContext(ctx, `
UPDATE xiangwan_payment_close_jobs
SET job_status = $4,
    generation_id = $5,
    owner_token = $6,
    lease_expires_at = $7,
    next_attempt_at = $8,
    attempt_count = $9,
    last_trade_state = $10,
    last_error_class = $11,
    last_error_code = $12,
    last_provider_request_id = $13,
    resolution = NULLIF($14, ''),
    completed_at = $15,
    version = version + 1,
    updated_at = $16
WHERE tenant_id = $1
  AND id = $2
  AND version = $3
  AND job_status = $18
  AND owner_token IS NOT DISTINCT FROM $17
RETURNING `+paymentCloseJobColumns,
		updated.TenantID,
		updated.ID,
		current.Version,
		updated.JobStatus,
		updated.GenerationID,
		updated.OwnerToken,
		updated.LeaseExpiresAt,
		updated.NextAttemptAt,
		updated.AttemptCount,
		updated.LastTradeState,
		updated.LastErrorClass,
		updated.LastErrorCode,
		updated.LastProviderRequestID,
		updated.Resolution,
		updated.CompletedAt,
		updated.UpdatedAt,
		current.OwnerToken,
		current.JobStatus,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return payment.PaymentCloseJob{}, payment.ErrPaymentCloseJobLeaseLost
	}
	return value, err
}

func scanPaymentCloseJob(row rowScanner) (payment.PaymentCloseJob, error) {
	var value payment.PaymentCloseJob
	var merchantConfigGenerationID uuid.NullUUID
	var generationID uuid.NullUUID
	var ownerToken uuid.NullUUID
	var leaseExpiresAt sql.NullTime
	var nextAttemptAt sql.NullTime
	var lastTradeState sql.NullString
	var lastErrorClass sql.NullString
	var lastErrorCode sql.NullString
	var lastProviderRequestID sql.NullString
	var resolution sql.NullString
	var completedAt sql.NullTime
	err := row.Scan(
		&value.ID,
		&value.TenantID,
		&value.OrderID,
		&value.PrincipalID,
		&value.PaymentAppID,
		&value.PaymentMerchantID,
		&merchantConfigGenerationID,
		&value.OutTradeNo,
		&value.AmountCents,
		&value.JobStatus,
		&generationID,
		&ownerToken,
		&leaseExpiresAt,
		&nextAttemptAt,
		&value.AttemptCount,
		&lastTradeState,
		&lastErrorClass,
		&lastErrorCode,
		&lastProviderRequestID,
		&resolution,
		&completedAt,
		&value.Version,
		&value.CreatedAt,
		&value.UpdatedAt,
	)
	if err != nil {
		return payment.PaymentCloseJob{}, err
	}
	if generationID.Valid {
		value.GenerationID = &generationID.UUID
	}
	if merchantConfigGenerationID.Valid {
		value.MerchantConfigGenerationID = merchantConfigGenerationID.UUID
	}
	if ownerToken.Valid {
		value.OwnerToken = &ownerToken.UUID
	}
	value.LeaseExpiresAt = nullTimePointer(leaseExpiresAt)
	value.NextAttemptAt = nullTimePointer(nextAttemptAt)
	if lastTradeState.Valid {
		tradeState := payment.ProviderTradeState(lastTradeState.String)
		value.LastTradeState = &tradeState
	}
	value.LastErrorClass = nullStringPointer(lastErrorClass)
	value.LastErrorCode = nullStringPointer(lastErrorCode)
	value.LastProviderRequestID = nullStringPointer(lastProviderRequestID)
	if resolution.Valid {
		value.Resolution = payment.PaymentCloseResolution(resolution.String)
	}
	value.CompletedAt = nullTimePointer(completedAt)
	return value, nil
}

func paymentCloseJobMatchesOrder(
	job payment.PaymentCloseJob,
	order payment.Order,
) bool {
	return job.TenantID == order.TenantID && job.OrderID == order.ID &&
		job.PrincipalID == order.PrincipalID &&
		job.PaymentAppID == order.PaymentAppID &&
		job.PaymentMerchantID == order.PaymentMerchantID &&
		job.MerchantConfigGenerationID == order.MerchantConfigGenerationID &&
		job.OutTradeNo == order.MerchantOrderNo &&
		job.AmountCents == order.PayableCents
}

func paymentCloseObservationMatchesJob(
	observation payment.TransactionObservation,
	job payment.PaymentCloseJob,
) bool {
	return payment.ValidateTransactionObservation(observation) == nil &&
		observation.ObservationSource ==
			payment.TransactionObservationSourceMerchantQuery &&
		observation.TenantID == job.TenantID &&
		observation.OrderID == job.OrderID &&
		observation.PrincipalID == job.PrincipalID &&
		observation.PaymentAppID == job.PaymentAppID &&
		observation.PaymentMerchantID == job.PaymentMerchantID &&
		observation.OutTradeNo == job.OutTradeNo &&
		observation.AmountCents == job.AmountCents
}

func samePaymentCloseJobIdentity(
	left payment.PaymentCloseJob,
	right payment.PaymentCloseJob,
) bool {
	return left.ID == right.ID && left.TenantID == right.TenantID &&
		left.OrderID == right.OrderID && left.PrincipalID == right.PrincipalID &&
		left.PaymentAppID == right.PaymentAppID &&
		left.PaymentMerchantID == right.PaymentMerchantID &&
		left.MerchantConfigGenerationID == right.MerchantConfigGenerationID &&
		left.OutTradeNo == right.OutTradeNo &&
		left.AmountCents == right.AmountCents
}

func paymentCloseResolutionMatchesOrder(
	resolution payment.PaymentCloseResolution,
	order payment.Order,
) bool {
	if resolution == payment.PaymentCloseResolutionPaymentConverged {
		return order.PaymentStatus == payment.OrderStatusPaidConfirmed
	}
	return order.PaymentStatus == payment.OrderStatusClosedUnpaid
}

func commitPaymentCloseJobTransaction(tx prepayTransaction) error {
	if err := tx.Commit(); err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) && postgresError.Code == "40001" {
			return fmt.Errorf("%w: %v", ErrPaymentCloseJobTransaction, err)
		}
		return fmt.Errorf("commit xiangwan payment close job: %w", err)
	}
	return nil
}

func classifyPaymentCloseJobWriteError(err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) &&
		(postgresError.Code == "23505" || postgresError.Code == "23514") {
		return fmt.Errorf("%w: %v", ErrPaymentCloseJobTransaction, err)
	}
	return err
}
