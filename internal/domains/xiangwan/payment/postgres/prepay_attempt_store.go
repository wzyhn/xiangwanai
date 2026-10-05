package paymentpostgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrInvalidPrepayAttemptStore  = errors.New("invalid xiangwan prepay attempt store")
	ErrPrepayOrderNotFound        = errors.New("xiangwan prepay Order not found")
	ErrPrepayUnavailable          = errors.New("xiangwan prepay is unavailable for this Order")
	ErrPrepayOpenIDUnavailable    = errors.New("xiangwan WeChat payment identity is unavailable")
	ErrPrepayConfirmationConflict = errors.New("xiangwan prepay confirmation facts changed")
	ErrPrepayIdempotencyConflict  = errors.New("xiangwan prepay idempotency conflict")
	ErrPrepayGenerationInactive   = errors.New("xiangwan prepay generation is inactive")
	ErrPrepayTransactionConflict  = errors.New("xiangwan prepay transaction conflict")
	prepayMerchantIDPattern       = regexp.MustCompile(`^[0-9]{6,32}$`)
)

type PrepayAttemptStoreConfig struct {
	PaymentAppID               string
	PaymentMerchantID          string
	MerchantConfigGenerationID uuid.UUID
	Description                string
	NotifyURL                  string
	Now                        func() time.Time
	NewUUID                    func() uuid.UUID
}

type PrepayAttemptStore struct {
	transactions               prepayTransactionStarter
	paymentAppID               string
	paymentMerchantID          string
	merchantConfigGenerationID uuid.UUID
	description                string
	notifyURL                  string
	now                        func() time.Time
	newUUID                    func() uuid.UUID
}

func NewPrepayAttemptStore(
	database *sql.DB,
	config PrepayAttemptStoreConfig,
) (*PrepayAttemptStore, error) {
	if database == nil {
		return nil, ErrInvalidPrepayAttemptStore
	}
	return newPrepayAttemptStore(
		sqlPrepayTransactionStarter{database: database},
		config,
	)
}

func newPrepayAttemptStore(
	transactions prepayTransactionStarter,
	config PrepayAttemptStoreConfig,
) (*PrepayAttemptStore, error) {
	if transactions == nil || invalidPrepayStoreConfig(config) {
		return nil, ErrInvalidPrepayAttemptStore
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	newUUID := config.NewUUID
	if newUUID == nil {
		newUUID = uuid.New
	}
	return &PrepayAttemptStore{
		transactions:               transactions,
		paymentAppID:               config.PaymentAppID,
		paymentMerchantID:          config.PaymentMerchantID,
		merchantConfigGenerationID: config.MerchantConfigGenerationID,
		description:                config.Description,
		notifyURL:                  config.NotifyURL,
		now:                        now,
		newUUID:                    newUUID,
	}, nil
}

func (store *PrepayAttemptStore) AcquirePrepayAttempt(
	ctx context.Context,
	command payment.CreatePrepayAttemptCommand,
) (payment.PrepayAcquisition, error) {
	if store == nil || store.transactions == nil || store.now == nil ||
		store.newUUID == nil || ctx == nil {
		return payment.PrepayAcquisition{}, ErrInvalidPrepayAttemptStore
	}
	baseFingerprint, err := payment.PrepayRequestFingerprint(command)
	if err != nil {
		return payment.PrepayAcquisition{}, err
	}
	fingerprint := store.requestFingerprint(baseFingerprint)
	now := store.now().UTC()
	attemptID := store.newUUID()
	ownerToken := store.newUUID()
	if now.IsZero() || attemptID == uuid.Nil || ownerToken == uuid.Nil {
		return payment.PrepayAcquisition{}, ErrInvalidPrepayAttemptStore
	}

	tx, err := store.transactions.beginPrepayTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelSerializable,
	})
	if err != nil {
		return payment.PrepayAcquisition{}, fmt.Errorf("begin xiangwan prepay acquisition: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	facts, err := store.lockPrepayFacts(ctx, tx, command.TenantID, command.OrderID)
	if err != nil {
		return payment.PrepayAcquisition{}, err
	}
	if facts.order.PrincipalID != command.PrincipalID ||
		facts.registrationPrincipalID != command.PrincipalID {
		return payment.PrepayAcquisition{}, ErrPrepayOrderNotFound
	}
	if err := requireMerchantConfigGeneration(
		ctx,
		tx,
		command.TenantID,
		store.merchantConfigGenerationID,
		store.paymentAppID,
		store.paymentMerchantID,
		false,
	); err != nil {
		return payment.PrepayAcquisition{}, err
	}
	if err := store.validatePrepayFacts(facts, command, now); err != nil {
		return payment.PrepayAcquisition{}, err
	}

	existing, found, err := findPrepayAttempt(
		ctx,
		tx,
		command.TenantID,
		command.PrincipalID,
		command.IdempotencyKey,
		command.OrderID,
	)
	if err != nil {
		return payment.PrepayAcquisition{}, err
	}
	if found {
		return store.acquireExisting(
			ctx,
			tx,
			existing,
			facts,
			command,
			fingerprint,
			ownerToken,
			now,
		)
	}

	attempt, err := payment.NewPaymentAttempt(payment.NewPaymentAttemptCommand{
		ID:                 attemptID,
		TenantID:           command.TenantID,
		OrderID:            command.OrderID,
		PrincipalID:        command.PrincipalID,
		GenerationID:       command.GenerationID,
		IdempotencyKey:     command.IdempotencyKey,
		RequestFingerprint: fingerprint,
		OutTradeNo:         facts.order.MerchantOrderNo,
		PaymentAppID:       store.paymentAppID,
		PaymentMerchantID:  store.paymentMerchantID,
		Description:        store.description,
		NotifyURL:          store.notifyURL,
		OrderVersion:       command.ExpectedOrderVersion,
		AmountCents:        command.ExpectedPayableCents,
		OwnerToken:         ownerToken,
		Now:                now,
	})
	if err != nil {
		return payment.PrepayAcquisition{}, err
	}
	inserted, err := insertPrepayAttempt(ctx, tx, attempt)
	if errors.Is(err, sql.ErrNoRows) {
		existing, found, err = findPrepayAttempt(
			ctx,
			tx,
			command.TenantID,
			command.PrincipalID,
			command.IdempotencyKey,
			command.OrderID,
		)
		if err != nil {
			return payment.PrepayAcquisition{}, err
		}
		if !found {
			return payment.PrepayAcquisition{}, ErrPrepayTransactionConflict
		}
		return store.acquireExisting(
			ctx,
			tx,
			existing,
			facts,
			command,
			fingerprint,
			ownerToken,
			now,
		)
	}
	if err != nil {
		return payment.PrepayAcquisition{}, classifyPrepayWriteError(err)
	}
	if err := commitPrepayTransaction(tx); err != nil {
		return payment.PrepayAcquisition{}, err
	}
	return store.providerAcquisition(inserted, facts), nil
}

func (store *PrepayAttemptStore) CompletePrepayAttempt(
	ctx context.Context,
	acquisition payment.PrepayAcquisition,
	providerResult payment.ProviderPrepayResult,
	providerErr error,
) (payment.PrepayAttemptResult, error) {
	if store == nil || store.transactions == nil || store.now == nil ||
		store.newUUID == nil || ctx == nil ||
		acquisition.Attempt.ID == uuid.Nil ||
		acquisition.InvocationToken == uuid.Nil ||
		acquisition.ProviderRequest == nil {
		return payment.PrepayAttemptResult{}, ErrInvalidPrepayAttemptStore
	}
	if providerErr == nil {
		if err := payment.ValidateProviderPrepayResult(
			providerResult,
			acquisition.Attempt.PaymentAppID,
		); err != nil {
			providerErr = payment.NewProviderFailure(
				payment.ProviderFailureInvalidResponse,
				"",
				err,
			)
		}
	}
	now := store.now().UTC()
	if now.IsZero() {
		return payment.PrepayAttemptResult{}, ErrInvalidPrepayAttemptStore
	}
	tx, err := store.transactions.beginPrepayTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelSerializable,
	})
	if err != nil {
		return payment.PrepayAttemptResult{}, fmt.Errorf("begin xiangwan prepay completion: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	facts, err := store.lockPrepayFacts(
		ctx,
		tx,
		acquisition.Attempt.TenantID,
		acquisition.Attempt.OrderID,
	)
	if err != nil {
		return payment.PrepayAttemptResult{}, err
	}
	current, err := getPrepayAttemptForUpdate(
		ctx,
		tx,
		acquisition.Attempt.TenantID,
		acquisition.Attempt.ID,
	)
	if err != nil {
		return payment.PrepayAttemptResult{}, err
	}
	if !sameAttemptIdentity(current, acquisition.Attempt) {
		return payment.PrepayAttemptResult{}, ErrPrepayTransactionConflict
	}

	observation, err := store.newProviderObservation(
		current,
		acquisition.InvocationToken,
		providerResult,
		providerErr,
		now,
	)
	if err != nil {
		return payment.PrepayAttemptResult{}, err
	}
	if current.AttemptStatus != payment.PrepayAttemptStatusInProgress ||
		current.OwnerToken == nil ||
		*current.OwnerToken != acquisition.InvocationToken {
		if err := insertPrepayObservation(ctx, tx, observation); err != nil {
			return payment.PrepayAttemptResult{}, err
		}
		if err := commitPrepayTransaction(tx); err != nil {
			return payment.PrepayAttemptResult{}, err
		}
		return prepayResult(current, facts.hold.ExpiresAt), nil
	}

	revalidationCommand := payment.CreatePrepayAttemptCommand{
		TenantID:             current.TenantID,
		GenerationID:         current.GenerationID,
		OrderID:              current.OrderID,
		PrincipalID:          current.PrincipalID,
		IdempotencyKey:       current.IdempotencyKey,
		ExpectedOrderVersion: current.OrderVersion,
		ExpectedPayableCents: current.AmountCents,
	}
	localStateErr := store.validatePrepayFacts(facts, revalidationCommand, now)
	var updated payment.PaymentAttempt
	if providerErr == nil && localStateErr == nil {
		updated, err = payment.CompletePaymentAttemptReady(
			current,
			acquisition.InvocationToken,
			providerResult,
			now,
		)
	} else {
		errorClass := "local_state_changed"
		providerRequestID := providerResult.ProviderRequestID
		if providerErr != nil {
			errorClass, _ = payment.ProviderFailureMetadata(providerErr)
			providerRequestID = ""
		}
		updated, err = payment.CompletePaymentAttemptUnknown(
			current,
			acquisition.InvocationToken,
			errorClass,
			providerRequestID,
			now,
		)
	}
	if err != nil {
		return payment.PrepayAttemptResult{}, err
	}
	updated, err = updatePrepayAttempt(ctx, tx, updated, current)
	if err != nil {
		return payment.PrepayAttemptResult{}, classifyPrepayWriteError(err)
	}
	if updated.AttemptStatus == payment.PrepayAttemptStatusUnknown &&
		facts.order.PaymentStatus == payment.OrderStatusPending {
		unknownOrder, changed, transitionErr := payment.MarkOrderUnknown(
			facts.order,
			now,
		)
		if transitionErr != nil {
			return payment.PrepayAttemptResult{}, transitionErr
		}
		if changed {
			repository := &Repository{db: tx}
			if _, updateErr := repository.UpdateOrderPayment(
				ctx,
				unknownOrder,
				facts.order.Version,
			); updateErr != nil {
				return payment.PrepayAttemptResult{}, classifyPrepayWriteError(updateErr)
			}
		}
	}
	if err := insertPrepayObservation(ctx, tx, observation); err != nil {
		return payment.PrepayAttemptResult{}, err
	}
	if err := commitPrepayTransaction(tx); err != nil {
		return payment.PrepayAttemptResult{}, err
	}
	return prepayResult(updated, facts.hold.ExpiresAt), nil
}

func (store *PrepayAttemptStore) acquireExisting(
	ctx context.Context,
	tx prepayTransaction,
	existing payment.PaymentAttempt,
	facts prepayLockedFacts,
	command payment.CreatePrepayAttemptCommand,
	fingerprint string,
	ownerToken uuid.UUID,
	now time.Time,
) (payment.PrepayAcquisition, error) {
	if !store.attemptMatches(existing, facts, command, fingerprint) {
		return payment.PrepayAcquisition{}, ErrPrepayIdempotencyConflict
	}
	switch existing.AttemptStatus {
	case payment.PrepayAttemptStatusReady, payment.PrepayAttemptStatusUnknown:
		if err := commitPrepayTransaction(tx); err != nil {
			return payment.PrepayAcquisition{}, err
		}
		return payment.PrepayAcquisition{
			Attempt:       existing,
			HoldExpiresAt: facts.hold.ExpiresAt,
		}, nil
	case payment.PrepayAttemptStatusInProgress:
		if existing.LeaseExpiresAt == nil {
			return payment.PrepayAcquisition{}, ErrPrepayTransactionConflict
		}
		if now.Before(*existing.LeaseExpiresAt) {
			if err := commitPrepayTransaction(tx); err != nil {
				return payment.PrepayAcquisition{}, err
			}
			return payment.PrepayAcquisition{
				Attempt:       existing,
				HoldExpiresAt: facts.hold.ExpiresAt,
			}, nil
		}
	default:
		return payment.PrepayAcquisition{}, ErrPrepayTransactionConflict
	}
	takenOver, err := payment.TakeOverPaymentAttempt(
		existing,
		command.GenerationID,
		ownerToken,
		now,
	)
	if err != nil {
		return payment.PrepayAcquisition{}, err
	}
	takenOver, err = updatePrepayAttempt(ctx, tx, takenOver, existing)
	if err != nil {
		return payment.PrepayAcquisition{}, classifyPrepayWriteError(err)
	}
	if err := commitPrepayTransaction(tx); err != nil {
		return payment.PrepayAcquisition{}, err
	}
	return store.providerAcquisition(takenOver, facts), nil
}

func (store *PrepayAttemptStore) providerAcquisition(
	attempt payment.PaymentAttempt,
	facts prepayLockedFacts,
) payment.PrepayAcquisition {
	return payment.PrepayAcquisition{
		Attempt:         attempt,
		HoldExpiresAt:   facts.hold.ExpiresAt,
		InvocationToken: *attempt.OwnerToken,
		ProviderRequest: &payment.ProviderPrepayRequest{
			AppID:        attempt.PaymentAppID,
			MerchantID:   attempt.PaymentMerchantID,
			Description:  attempt.Description,
			OutTradeNo:   attempt.OutTradeNo,
			NotifyURL:    attempt.NotifyURL,
			OpenID:       facts.openID,
			AmountCents:  attempt.AmountCents,
			TimeExpireAt: facts.hold.ExpiresAt,
		},
	}
}

type prepayLockedFacts struct {
	generationID            uuid.UUID
	brandStatus             activity.BrandLifecycleStatus
	seriesStatus            activity.SeriesStatus
	instanceStatus          activity.InstanceStatus
	sessionStatus           activity.SessionStatus
	registrationEndAt       time.Time
	registrationStatus      registration.ParticipationStatus
	registrationPrincipalID uuid.UUID
	hold                    payment.CapacityHold
	order                   payment.Order
	openID                  string
	openIDCount             int64
}

type prepayOrderHint struct {
	seriesID       uuid.UUID
	instanceID     uuid.UUID
	sessionID      uuid.UUID
	registrationID uuid.UUID
}

func (store *PrepayAttemptStore) lockPrepayFacts(
	ctx context.Context,
	tx prepayTransaction,
	tenantID uuid.UUID,
	orderID uuid.UUID,
) (prepayLockedFacts, error) {
	var hint prepayOrderHint
	err := tx.queryRowContext(ctx, `
SELECT series_id, instance_id, session_id, registration_id
FROM xiangwan_orders
WHERE tenant_id = $1 AND id = $2
`, tenantID, orderID).Scan(
		&hint.seriesID,
		&hint.instanceID,
		&hint.sessionID,
		&hint.registrationID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return prepayLockedFacts{}, ErrPrepayOrderNotFound
	}
	if err != nil {
		return prepayLockedFacts{}, fmt.Errorf("read xiangwan prepay Order identity: %w", err)
	}

	var facts prepayLockedFacts
	var writeEpoch int64
	err = tx.queryRowContext(ctx, `
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
`, tenantID).Scan(&facts.generationID, &writeEpoch)
	if errors.Is(err, sql.ErrNoRows) {
		return prepayLockedFacts{}, ErrPrepayGenerationInactive
	}
	if err != nil {
		return prepayLockedFacts{}, fmt.Errorf("lock xiangwan prepay generation: %w", err)
	}

	err = tx.queryRowContext(ctx, `
SELECT lifecycle_status
FROM xiangwan_brand_profiles
WHERE tenant_id = $1
FOR SHARE
`, tenantID).Scan(&facts.brandStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return prepayLockedFacts{}, ErrPrepayUnavailable
	}
	if err != nil {
		return prepayLockedFacts{}, fmt.Errorf("lock xiangwan prepay Brand: %w", err)
	}
	err = tx.queryRowContext(ctx, `
SELECT status
FROM xiangwan_activity_series
WHERE tenant_id = $1 AND id = $2
FOR SHARE
`, tenantID, hint.seriesID).Scan(&facts.seriesStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return prepayLockedFacts{}, ErrPrepayUnavailable
	}
	if err != nil {
		return prepayLockedFacts{}, fmt.Errorf("lock xiangwan prepay Series: %w", err)
	}
	err = tx.queryRowContext(ctx, `
SELECT status
FROM xiangwan_activity_instances
WHERE tenant_id = $1 AND series_id = $2 AND id = $3
FOR SHARE
`, tenantID, hint.seriesID, hint.instanceID).Scan(&facts.instanceStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return prepayLockedFacts{}, ErrPrepayUnavailable
	}
	if err != nil {
		return prepayLockedFacts{}, fmt.Errorf("lock xiangwan prepay Instance: %w", err)
	}
	var registrationEndAt sql.NullTime
	err = tx.queryRowContext(ctx, `
SELECT status, registration_end_at
FROM xiangwan_activity_sessions
WHERE tenant_id = $1 AND instance_id = $2 AND id = $3
FOR SHARE
`, tenantID, hint.instanceID, hint.sessionID).Scan(
		&facts.sessionStatus,
		&registrationEndAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return prepayLockedFacts{}, ErrPrepayUnavailable
	}
	if err != nil {
		return prepayLockedFacts{}, fmt.Errorf("lock xiangwan prepay Session: %w", err)
	}
	if registrationEndAt.Valid {
		facts.registrationEndAt = registrationEndAt.Time
	}
	err = tx.queryRowContext(ctx, `
SELECT participation_status, principal_id
FROM xiangwan_registrations
WHERE tenant_id = $1 AND id = $2
FOR SHARE
`, tenantID, hint.registrationID).Scan(
		&facts.registrationStatus,
		&facts.registrationPrincipalID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return prepayLockedFacts{}, ErrPrepayUnavailable
	}
	if err != nil {
		return prepayLockedFacts{}, fmt.Errorf("lock xiangwan prepay Registration: %w", err)
	}
	facts.hold, err = scanCapacityHold(tx.queryRowContext(ctx, `
SELECT
    id, tenant_id, order_id, registration_id, session_id,
    hold_status, expires_at, converted_at, released_at, release_reason,
    version, created_at, updated_at
FROM xiangwan_capacity_holds
WHERE tenant_id = $1 AND order_id = $2
FOR SHARE
`, tenantID, orderID))
	if errors.Is(err, sql.ErrNoRows) {
		return prepayLockedFacts{}, ErrPrepayUnavailable
	}
	if err != nil {
		return prepayLockedFacts{}, fmt.Errorf("lock xiangwan prepay Hold: %w", err)
	}
	facts.order, err = scanOrder(tx.queryRowContext(ctx, `
SELECT
    id, tenant_id, registration_id, series_id, instance_id, session_id, principal_id,
    payment_status, idempotency_key, merchant_order_no, payment_app_id,
    payment_merchant_id, merchant_config_generation_id,
    original_price_cents, discount_cents, payable_cents,
    actual_paid_cents, wechat_transaction_id, paid_at, closed_at,
    version, created_at, updated_at
FROM xiangwan_orders
WHERE tenant_id = $1 AND id = $2
FOR UPDATE
`, tenantID, orderID))
	if errors.Is(err, sql.ErrNoRows) {
		return prepayLockedFacts{}, ErrPrepayOrderNotFound
	}
	if err != nil {
		return prepayLockedFacts{}, fmt.Errorf("lock xiangwan prepay Order: %w", err)
	}
	var openID sql.NullString
	err = tx.queryRowContext(ctx, `
SELECT COUNT(*), MIN(provider_id)
FROM identity_links
WHERE principal_id = $1
  AND provider = 'wechat'
  AND app_id = $2
`, facts.order.PrincipalID, store.paymentAppID).Scan(&facts.openIDCount, &openID)
	if err != nil {
		return prepayLockedFacts{}, fmt.Errorf("read xiangwan payment OpenID: %w", err)
	}
	if openID.Valid {
		facts.openID = openID.String
	}
	return facts, nil
}

func (store *PrepayAttemptStore) validatePrepayFacts(
	facts prepayLockedFacts,
	command payment.CreatePrepayAttemptCommand,
	now time.Time,
) error {
	order := facts.order
	hold := facts.hold
	if facts.generationID != command.GenerationID {
		return ErrPrepayGenerationInactive
	}
	if order.PrincipalID != command.PrincipalID ||
		facts.registrationPrincipalID != command.PrincipalID {
		return ErrPrepayOrderNotFound
	}
	if err := requireOrderPaymentIdentity(
		order,
		store.paymentAppID,
		store.paymentMerchantID,
	); err != nil {
		return err
	}
	if err := requireOrderMerchantConfigGeneration(
		order,
		store.merchantConfigGenerationID,
	); err != nil {
		return err
	}
	if order.Version != command.ExpectedOrderVersion ||
		order.PayableCents != command.ExpectedPayableCents {
		return ErrPrepayConfirmationConflict
	}
	if facts.brandStatus != activity.BrandLifecycleActive ||
		facts.seriesStatus != activity.SeriesStatusActive ||
		facts.instanceStatus != activity.InstanceStatusPublished ||
		facts.sessionStatus != activity.SessionStatusPublished ||
		facts.registrationEndAt.IsZero() || !now.Before(facts.registrationEndAt) ||
		facts.registrationStatus != registration.ParticipationStatusPendingPayment ||
		order.PaymentStatus != payment.OrderStatusPending ||
		order.ActualPaidCents != nil || order.ClosedAt != nil ||
		hold.OrderID != order.ID || hold.RegistrationID != order.RegistrationID ||
		hold.SessionID != order.SessionID ||
		hold.HoldStatus != payment.CapacityHoldStatusActive ||
		!now.Before(hold.ExpiresAt) {
		return ErrPrepayUnavailable
	}
	if facts.openIDCount != 1 || invalidPrepayOpenID(facts.openID) {
		return ErrPrepayOpenIDUnavailable
	}
	return nil
}

func (store *PrepayAttemptStore) attemptMatches(
	attempt payment.PaymentAttempt,
	facts prepayLockedFacts,
	command payment.CreatePrepayAttemptCommand,
	fingerprint string,
) bool {
	return attempt.TenantID == command.TenantID &&
		attempt.OrderID == command.OrderID &&
		attempt.PrincipalID == command.PrincipalID &&
		attempt.RequestFingerprint == fingerprint &&
		attempt.Provider == payment.PrepayProvider &&
		attempt.OperationKind == payment.PrepayOperationKind &&
		attempt.OutTradeNo == facts.order.MerchantOrderNo &&
		attempt.PaymentAppID == store.paymentAppID &&
		attempt.PaymentMerchantID == store.paymentMerchantID &&
		attempt.Description == store.description &&
		attempt.NotifyURL == store.notifyURL &&
		attempt.OrderVersion == command.ExpectedOrderVersion &&
		attempt.AmountCents == command.ExpectedPayableCents &&
		attempt.Currency == payment.PrepayCurrency
}

func (store *PrepayAttemptStore) requestFingerprint(base string) string {
	digest := sha256.Sum256([]byte(strings.Join([]string{
		base,
		store.paymentAppID,
		store.paymentMerchantID,
		store.description,
		store.notifyURL,
	}, "\n")))
	return hex.EncodeToString(digest[:])
}

type prepayProviderObservation struct {
	id                uuid.UUID
	tenantID          uuid.UUID
	attemptID         uuid.UUID
	orderID           uuid.UUID
	invocationToken   uuid.UUID
	kind              string
	providerRequestID *string
	providerCode      *string
	prepayIDDigest    *string
	errorClass        *string
	observedAt        time.Time
}

func (store *PrepayAttemptStore) newProviderObservation(
	attempt payment.PaymentAttempt,
	invocationToken uuid.UUID,
	providerResult payment.ProviderPrepayResult,
	providerErr error,
	now time.Time,
) (prepayProviderObservation, error) {
	observation := prepayProviderObservation{
		id:              store.newUUID(),
		tenantID:        attempt.TenantID,
		attemptID:       attempt.ID,
		orderID:         attempt.OrderID,
		invocationToken: invocationToken,
		observedAt:      now,
	}
	if observation.id == uuid.Nil {
		return prepayProviderObservation{}, ErrInvalidPrepayAttemptStore
	}
	if providerErr == nil {
		digest, err := payment.PrepayIDDigest(providerResult.PrepayID)
		if err != nil {
			return prepayProviderObservation{}, err
		}
		observation.kind = "prepay_ready"
		observation.prepayIDDigest = &digest
		observation.providerRequestID = prepayOptionalText(
			providerResult.ProviderRequestID,
			128,
		)
		return observation, nil
	}
	class, code := payment.ProviderFailureMetadata(providerErr)
	observation.kind = "prepay_ambiguous"
	observation.errorClass = &class
	observation.providerCode = prepayOptionalText(code, 64)
	return observation, nil
}

func findPrepayAttempt(
	ctx context.Context,
	tx prepayTransaction,
	tenantID uuid.UUID,
	principalID uuid.UUID,
	idempotencyKey uuid.UUID,
	orderID uuid.UUID,
) (payment.PaymentAttempt, bool, error) {
	attempt, err := scanPrepayAttempt(tx.queryRowContext(ctx, prepayAttemptSelect+`
WHERE tenant_id = $1
  AND principal_id = $2
  AND operation_kind = 'wechat_prepay'
  AND idempotency_key = $3
FOR UPDATE
`, tenantID, principalID, idempotencyKey))
	if err == nil {
		return attempt, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return payment.PaymentAttempt{}, false, fmt.Errorf("find xiangwan prepay operation: %w", err)
	}
	attempt, err = scanPrepayAttempt(tx.queryRowContext(ctx, prepayAttemptSelect+`
WHERE tenant_id = $1 AND order_id = $2
FOR UPDATE
`, tenantID, orderID))
	if errors.Is(err, sql.ErrNoRows) {
		return payment.PaymentAttempt{}, false, nil
	}
	if err != nil {
		return payment.PaymentAttempt{}, false, fmt.Errorf("find xiangwan Order prepay attempt: %w", err)
	}
	return attempt, true, nil
}

func getPrepayAttemptForUpdate(
	ctx context.Context,
	tx prepayTransaction,
	tenantID uuid.UUID,
	attemptID uuid.UUID,
) (payment.PaymentAttempt, error) {
	attempt, err := scanPrepayAttempt(tx.queryRowContext(ctx, prepayAttemptSelect+`
WHERE tenant_id = $1 AND id = $2
FOR UPDATE
`, tenantID, attemptID))
	if errors.Is(err, sql.ErrNoRows) {
		return payment.PaymentAttempt{}, ErrPrepayTransactionConflict
	}
	if err != nil {
		return payment.PaymentAttempt{}, fmt.Errorf("lock xiangwan prepay attempt: %w", err)
	}
	return attempt, nil
}

const prepayAttemptSelect = `
SELECT
    id, tenant_id, order_id, principal_id, generation_id,
    operation_kind, idempotency_key, request_fingerprint, provider,
    out_trade_no, payment_app_id, payment_merchant_id,
    description, notify_url, order_version, amount_cents, currency,
    attempt_status, owner_token, lease_expires_at,
    prepay_id, client_timestamp, client_nonce, client_package,
    client_sign_type, client_pay_sign, provider_request_id,
    last_error_class, completed_at, version, created_at, updated_at
FROM xiangwan_payment_attempts
`

func insertPrepayAttempt(
	ctx context.Context,
	tx prepayTransaction,
	attempt payment.PaymentAttempt,
) (payment.PaymentAttempt, error) {
	return scanPrepayAttempt(tx.queryRowContext(ctx, `
INSERT INTO xiangwan_payment_attempts (
    id, tenant_id, order_id, principal_id, generation_id,
    operation_kind, idempotency_key, request_fingerprint, provider,
    out_trade_no, payment_app_id, payment_merchant_id,
    description, notify_url, order_version, amount_cents, currency,
    attempt_status, owner_token, lease_expires_at,
    prepay_id, client_timestamp, client_nonce, client_package,
    client_sign_type, client_pay_sign, provider_request_id,
    last_error_class, completed_at, version, created_at, updated_at
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9,
    $10, $11, $12,
    $13, $14, $15, $16, $17,
    $18, $19, $20,
    $21, $22, $23, $24,
    $25, $26, $27,
    $28, $29, $30, $31, $32
)
ON CONFLICT DO NOTHING
RETURNING
    id, tenant_id, order_id, principal_id, generation_id,
    operation_kind, idempotency_key, request_fingerprint, provider,
    out_trade_no, payment_app_id, payment_merchant_id,
    description, notify_url, order_version, amount_cents, currency,
    attempt_status, owner_token, lease_expires_at,
    prepay_id, client_timestamp, client_nonce, client_package,
    client_sign_type, client_pay_sign, provider_request_id,
    last_error_class, completed_at, version, created_at, updated_at
`, prepayAttemptArguments(attempt)...))
}

func updatePrepayAttempt(
	ctx context.Context,
	tx prepayTransaction,
	updated payment.PaymentAttempt,
	current payment.PaymentAttempt,
) (payment.PaymentAttempt, error) {
	return scanPrepayAttempt(tx.queryRowContext(ctx, `
UPDATE xiangwan_payment_attempts
SET generation_id = $3,
    attempt_status = $4,
    owner_token = $5,
    lease_expires_at = $6,
    prepay_id = $7,
    client_timestamp = $8,
    client_nonce = $9,
    client_package = $10,
    client_sign_type = $11,
    client_pay_sign = $12,
    provider_request_id = $13,
    last_error_class = $14,
    completed_at = $15,
    version = version + 1,
    updated_at = $16
WHERE tenant_id = $1
  AND id = $2
  AND version = $17
  AND attempt_status = $18
  AND owner_token IS NOT DISTINCT FROM $19
RETURNING
    id, tenant_id, order_id, principal_id, generation_id,
    operation_kind, idempotency_key, request_fingerprint, provider,
    out_trade_no, payment_app_id, payment_merchant_id,
    description, notify_url, order_version, amount_cents, currency,
    attempt_status, owner_token, lease_expires_at,
    prepay_id, client_timestamp, client_nonce, client_package,
    client_sign_type, client_pay_sign, provider_request_id,
    last_error_class, completed_at, version, created_at, updated_at
`,
		updated.TenantID,
		updated.ID,
		updated.GenerationID,
		updated.AttemptStatus,
		updated.OwnerToken,
		updated.LeaseExpiresAt,
		updated.PrepayID,
		updated.ClientTimestamp,
		updated.ClientNonce,
		updated.ClientPackage,
		updated.ClientSignType,
		updated.ClientPaySign,
		updated.ProviderRequestID,
		updated.LastErrorClass,
		updated.CompletedAt,
		updated.UpdatedAt,
		current.Version,
		current.AttemptStatus,
		current.OwnerToken,
	))
}

func insertPrepayObservation(
	ctx context.Context,
	tx prepayTransaction,
	observation prepayProviderObservation,
) error {
	var id uuid.UUID
	err := tx.queryRowContext(ctx, `
INSERT INTO xiangwan_payment_observations (
    id, tenant_id, attempt_id, order_id, invocation_token,
    observation_kind, provider_request_id, provider_code,
    prepay_id_digest, error_class, observed_at
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8,
    $9, $10, $11
)
ON CONFLICT (tenant_id, attempt_id, invocation_token) DO NOTHING
RETURNING id
`,
		observation.id,
		observation.tenantID,
		observation.attemptID,
		observation.orderID,
		observation.invocationToken,
		observation.kind,
		observation.providerRequestID,
		observation.providerCode,
		observation.prepayIDDigest,
		observation.errorClass,
		observation.observedAt,
	).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("record xiangwan prepay provider observation: %w", err)
	}
	return nil
}

func prepayAttemptArguments(attempt payment.PaymentAttempt) []any {
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
		attempt.OwnerToken,
		attempt.LeaseExpiresAt,
		attempt.PrepayID,
		attempt.ClientTimestamp,
		attempt.ClientNonce,
		attempt.ClientPackage,
		attempt.ClientSignType,
		attempt.ClientPaySign,
		attempt.ProviderRequestID,
		attempt.LastErrorClass,
		attempt.CompletedAt,
		attempt.Version,
		attempt.CreatedAt,
		attempt.UpdatedAt,
	}
}

func scanPrepayAttempt(row rowScanner) (payment.PaymentAttempt, error) {
	var attempt payment.PaymentAttempt
	var ownerToken uuid.NullUUID
	var leaseExpiresAt sql.NullTime
	var prepayID sql.NullString
	var clientTimestamp sql.NullString
	var clientNonce sql.NullString
	var clientPackage sql.NullString
	var clientSignType sql.NullString
	var clientPaySign sql.NullString
	var providerRequestID sql.NullString
	var lastErrorClass sql.NullString
	var completedAt sql.NullTime
	err := row.Scan(
		&attempt.ID,
		&attempt.TenantID,
		&attempt.OrderID,
		&attempt.PrincipalID,
		&attempt.GenerationID,
		&attempt.OperationKind,
		&attempt.IdempotencyKey,
		&attempt.RequestFingerprint,
		&attempt.Provider,
		&attempt.OutTradeNo,
		&attempt.PaymentAppID,
		&attempt.PaymentMerchantID,
		&attempt.Description,
		&attempt.NotifyURL,
		&attempt.OrderVersion,
		&attempt.AmountCents,
		&attempt.Currency,
		&attempt.AttemptStatus,
		&ownerToken,
		&leaseExpiresAt,
		&prepayID,
		&clientTimestamp,
		&clientNonce,
		&clientPackage,
		&clientSignType,
		&clientPaySign,
		&providerRequestID,
		&lastErrorClass,
		&completedAt,
		&attempt.Version,
		&attempt.CreatedAt,
		&attempt.UpdatedAt,
	)
	if err != nil {
		return payment.PaymentAttempt{}, err
	}
	if ownerToken.Valid {
		attempt.OwnerToken = &ownerToken.UUID
	}
	attempt.LeaseExpiresAt = nullTimePointer(leaseExpiresAt)
	attempt.PrepayID = nullStringPointer(prepayID)
	attempt.ClientTimestamp = nullStringPointer(clientTimestamp)
	attempt.ClientNonce = nullStringPointer(clientNonce)
	attempt.ClientPackage = nullStringPointer(clientPackage)
	attempt.ClientSignType = nullStringPointer(clientSignType)
	attempt.ClientPaySign = nullStringPointer(clientPaySign)
	attempt.ProviderRequestID = nullStringPointer(providerRequestID)
	attempt.LastErrorClass = nullStringPointer(lastErrorClass)
	attempt.CompletedAt = nullTimePointer(completedAt)
	return attempt, nil
}

func prepayResult(
	attempt payment.PaymentAttempt,
	holdExpiresAt time.Time,
) payment.PrepayAttemptResult {
	result := payment.PrepayAttemptResult{
		Attempt:       attempt,
		HoldExpiresAt: holdExpiresAt,
	}
	if parameters, available := attempt.PaymentParameters(); available {
		result.Parameters = parameters
	}
	return result
}

func sameAttemptIdentity(left payment.PaymentAttempt, right payment.PaymentAttempt) bool {
	return left.ID == right.ID && left.TenantID == right.TenantID &&
		left.OrderID == right.OrderID && left.PrincipalID == right.PrincipalID &&
		left.IdempotencyKey == right.IdempotencyKey &&
		left.RequestFingerprint == right.RequestFingerprint &&
		left.OutTradeNo == right.OutTradeNo &&
		left.PaymentAppID == right.PaymentAppID &&
		left.PaymentMerchantID == right.PaymentMerchantID &&
		left.Description == right.Description && left.NotifyURL == right.NotifyURL &&
		left.OrderVersion == right.OrderVersion &&
		left.AmountCents == right.AmountCents
}

func invalidPrepayStoreConfig(config PrepayAttemptStoreConfig) bool {
	if config.PaymentAppID == "" || config.PaymentAppID != strings.TrimSpace(config.PaymentAppID) ||
		len([]rune(config.PaymentAppID)) > 64 ||
		!prepayMerchantIDPattern.MatchString(config.PaymentMerchantID) ||
		config.Description == "" || config.Description != strings.TrimSpace(config.Description) ||
		len([]rune(config.Description)) > 127 {
		return true
	}
	parsed, err := url.ParseRequestURI(config.NotifyURL)
	return err != nil || parsed.Scheme != "https" || parsed.Host == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		config.NotifyURL != strings.TrimSpace(config.NotifyURL) ||
		len([]rune(config.NotifyURL)) > 2048
}

func invalidPrepayOpenID(value string) bool {
	return value == "" || value != strings.TrimSpace(value) ||
		len([]rune(value)) > 128 || strings.ContainsAny(value, "\r\n\x00")
}

func prepayOptionalText(value string, maxRunes int) *string {
	if value == "" || value != strings.TrimSpace(value) ||
		len([]rune(value)) > maxRunes || strings.ContainsAny(value, "\r\n\x00") {
		return nil
	}
	copy := value
	return &copy
}

func classifyPrepayWriteError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrPrepayTransactionConflict
	}
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) {
		return err
	}
	if postgresError.Code == "40001" {
		return fmt.Errorf("%w: %v", ErrPrepayTransactionConflict, err)
	}
	if postgresError.Code == "23505" || postgresError.Code == "23514" {
		return fmt.Errorf("%w: %v", ErrPrepayTransactionConflict, err)
	}
	return err
}

func commitPrepayTransaction(tx prepayTransaction) error {
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit xiangwan prepay transaction: %w", classifyPrepayWriteError(err))
	}
	return nil
}

type prepayTransaction interface {
	queryExecutor
	Commit() error
	Rollback() error
}

type prepayTransactionStarter interface {
	beginPrepayTx(context.Context, *sql.TxOptions) (prepayTransaction, error)
}

type sqlPrepayTransactionStarter struct {
	database *sql.DB
}

func (starter sqlPrepayTransactionStarter) beginPrepayTx(
	ctx context.Context,
	options *sql.TxOptions,
) (prepayTransaction, error) {
	tx, err := starter.database.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return &sqlPrepayTransaction{transaction: tx}, nil
}

type sqlPrepayTransaction struct {
	transaction *sql.Tx
}

func (tx *sqlPrepayTransaction) queryRowContext(
	ctx context.Context,
	query string,
	args ...any,
) rowScanner {
	return tx.transaction.QueryRowContext(ctx, query, args...)
}

func (tx *sqlPrepayTransaction) Commit() error {
	return tx.transaction.Commit()
}

func (tx *sqlPrepayTransaction) Rollback() error {
	return tx.transaction.Rollback()
}
