package xiangwanruntime

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	paymentpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment/postgres"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment/wechatpay"
	"github.com/google/uuid"
	wechatpayutils "github.com/wechatpay-apiv3/wechatpay-go/utils"
)

const PaymentCloseWorkerPollInterval = time.Second

var (
	ErrInvalidPaymentCloseWorker = errors.New(
		"invalid xiangwan payment close worker",
	)
	ErrPaymentCloseWorkerSchemaUnavailable = errors.New(
		"xiangwan payment close worker schema is unavailable",
	)
	ErrPaymentCloseWorkerIdentityMismatch = errors.New(
		"xiangwan payment close worker merchant identity does not match queued jobs",
	)
)

type paymentCloseProcessor interface {
	ProcessNext(
		context.Context,
		uuid.UUID,
		uuid.UUID,
	) (payment.PaymentCloseJob, bool, error)
}

type paymentCloseSchemaGate interface {
	CheckPaymentCloseSchema(context.Context) error
	CheckPaymentCloseIdentity(context.Context, uuid.UUID, string, string, uuid.UUID) error
}

type PaymentCloseWorker struct {
	database                   *sql.DB
	processor                  paymentCloseProcessor
	generationGate             GenerationGate
	schemaGate                 paymentCloseSchemaGate
	tenantID                   uuid.UUID
	generationID               uuid.UUID
	paymentAppID               string
	paymentMerchantID          string
	merchantConfigGenerationID uuid.UUID
	pollInterval               time.Duration
}

func NewPaymentCloseWorker(
	ctx context.Context,
	config PaymentCloseWorkerConfig,
) (*PaymentCloseWorker, error) {
	if ctx == nil || !validPaymentCloseWorkerConfig(config) {
		return nil, ErrInvalidPaymentCloseWorker
	}
	database, err := openPaymentCloseWorkerDatabase(config.DatabaseDSN)
	if err != nil {
		return nil, err
	}
	provider, err := newPaymentCloseProvider(ctx, config)
	if err != nil {
		_ = database.Close()
		return nil, err
	}
	store, err := paymentpostgres.NewPaymentCloseJobStore(
		database,
		paymentpostgres.PaymentCloseJobStoreConfig{},
	)
	if err != nil {
		_ = database.Close()
		return nil, ErrInvalidPaymentCloseWorker
	}
	processor, err := payment.NewPaymentCloseServiceForMerchantConfig(
		store,
		provider,
		paymentpostgres.NewPaymentConfirmerForMerchantConfigWithLegacy(
			database,
			config.MerchantConfigGenerationID,
		),
		nil,
		config.MerchantConfigGenerationID,
		config.PaymentAppID,
		config.PaymentMerchantID,
	)
	if err != nil {
		_ = database.Close()
		return nil, ErrInvalidPaymentCloseWorker
	}
	executor := sqlGenerationExecutor{db: database}
	return &PaymentCloseWorker{
		database:                   database,
		processor:                  processor,
		generationGate:             &PostgresGenerationGate{db: executor},
		schemaGate:                 &postgresPaymentCloseSchemaGate{db: executor},
		tenantID:                   config.TenantID,
		generationID:               config.GenerationID,
		paymentAppID:               config.PaymentAppID,
		paymentMerchantID:          config.PaymentMerchantID,
		merchantConfigGenerationID: config.MerchantConfigGenerationID,
		pollInterval:               PaymentCloseWorkerPollInterval,
	}, nil
}

func CheckPaymentCloseWorkerReady(
	ctx context.Context,
	config PaymentCloseWorkerConfig,
) error {
	if ctx == nil || !validPaymentCloseWorkerConfig(config) {
		return ErrInvalidPaymentCloseWorker
	}
	database, err := openPaymentCloseWorkerDatabase(config.DatabaseDSN)
	if err != nil {
		return err
	}
	defer func() { _ = database.Close() }()
	if _, err := newPaymentCloseProvider(ctx, config); err != nil {
		return err
	}
	executor := sqlGenerationExecutor{db: database}
	worker := &PaymentCloseWorker{
		generationGate:             &PostgresGenerationGate{db: executor},
		schemaGate:                 &postgresPaymentCloseSchemaGate{db: executor},
		tenantID:                   config.TenantID,
		generationID:               config.GenerationID,
		paymentAppID:               config.PaymentAppID,
		paymentMerchantID:          config.PaymentMerchantID,
		merchantConfigGenerationID: config.MerchantConfigGenerationID,
	}
	return worker.Ready(ctx)
}

// newPaymentCloseProvider is shared by worker startup and worker-health. The
// health command must reject missing or malformed PEM/provider credentials in
// the same way as the long-running worker, while still making no provider API
// request. NewProvider only builds the local SDK client and HTTP transport.
func newPaymentCloseProvider(
	ctx context.Context,
	config PaymentCloseWorkerConfig,
) (*wechatpay.Provider, error) {
	privateKey, err := wechatpayutils.LoadPrivateKeyWithPath(
		config.WeChatPayPrivateKeyFile,
	)
	if err != nil {
		return nil, ErrInvalidPaymentCloseWorker
	}
	publicKey, err := wechatpayutils.LoadPublicKeyWithPath(
		config.WeChatPayPublicKeyFile,
	)
	if err != nil {
		return nil, ErrInvalidPaymentCloseWorker
	}
	provider, err := wechatpay.NewProvider(ctx, wechatpay.Config{
		MerchantID:                config.PaymentMerchantID,
		MerchantCertificateSerial: config.WeChatPayCertSerial,
		MerchantPrivateKey:        privateKey,
		WeChatPayPublicKeyID:      config.WeChatPayPublicKeyID,
		WeChatPayPublicKey:        publicKey,
	})
	if err != nil {
		return nil, ErrInvalidPaymentCloseWorker
	}
	return provider, nil
}

func openPaymentCloseWorkerDatabase(databaseDSN string) (*sql.DB, error) {
	if databaseDSN == "" {
		return nil, ErrInvalidPaymentCloseWorker
	}
	database, err := sql.Open("pgx", databaseDSN)
	if err != nil {
		return nil, ErrDatabaseConfiguration
	}
	database.SetMaxOpenConns(5)
	database.SetMaxIdleConns(2)
	database.SetConnMaxIdleTime(2 * time.Minute)
	database.SetConnMaxLifetime(15 * time.Minute)
	return database, nil
}

func (worker *PaymentCloseWorker) Ready(ctx context.Context) error {
	if worker == nil || worker.generationGate == nil || worker.schemaGate == nil ||
		ctx == nil || worker.tenantID == uuid.Nil || worker.generationID == uuid.Nil ||
		worker.merchantConfigGenerationID == uuid.Nil ||
		!validWechatAppID(worker.paymentAppID) ||
		!runtimeMerchantIDPattern.MatchString(worker.paymentMerchantID) {
		return ErrInvalidPaymentCloseWorker
	}
	if err := worker.generationGate.CheckActiveGeneration(
		ctx,
		worker.tenantID,
		worker.generationID,
	); err != nil {
		return err
	}
	if err := worker.schemaGate.CheckPaymentCloseSchema(ctx); err != nil {
		return err
	}
	return worker.schemaGate.CheckPaymentCloseIdentity(
		ctx,
		worker.tenantID,
		worker.paymentAppID,
		worker.paymentMerchantID,
		worker.merchantConfigGenerationID,
	)
}

func (worker *PaymentCloseWorker) Run(ctx context.Context) error {
	if worker == nil || worker.processor == nil || worker.pollInterval <= 0 ||
		ctx == nil {
		return ErrInvalidPaymentCloseWorker
	}
	if ctx.Err() != nil {
		return nil
	}
	if err := worker.Ready(ctx); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	for {
		if ctx.Err() != nil {
			return nil
		}
		_, found, err := worker.processor.ProcessNext(
			ctx,
			worker.tenantID,
			worker.generationID,
		)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if !paymentCloseWorkerShouldRetry(err) {
				return fmt.Errorf("process xiangwan payment close job: %w", err)
			}
			if !worker.waitPoll(ctx) {
				return nil
			}
			continue
		}
		if found {
			continue
		}
		if !worker.waitPoll(ctx) {
			return nil
		}
	}
}

func (worker *PaymentCloseWorker) waitPoll(ctx context.Context) bool {
	timer := time.NewTimer(worker.pollInterval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func paymentCloseWorkerShouldRetry(err error) bool {
	return errors.Is(err, payment.ErrPaymentCloseJobLeaseLost)
}

func (worker *PaymentCloseWorker) Close() error {
	if worker == nil || worker.database == nil {
		return nil
	}
	return worker.database.Close()
}

type postgresPaymentCloseSchemaGate struct {
	db generationQueryExecutor
}

func (gate *postgresPaymentCloseSchemaGate) CheckPaymentCloseSchema(
	ctx context.Context,
) error {
	if gate == nil || gate.db == nil || ctx == nil {
		return ErrInvalidPaymentCloseWorker
	}
	var ready bool
	if err := gate.db.queryRowContext(ctx, `
SELECT to_regclass('public.xiangwan_payment_close_jobs') IS NOT NULL
`).Scan(&ready); err != nil {
		return fmt.Errorf("check xiangwan payment close schema: %w", err)
	}
	if !ready {
		return ErrPaymentCloseWorkerSchemaUnavailable
	}
	return nil
}

func (gate *postgresPaymentCloseSchemaGate) CheckPaymentCloseIdentity(
	ctx context.Context,
	tenantID uuid.UUID,
	appID string,
	merchantID string,
	merchantConfigGenerationID uuid.UUID,
) error {
	if gate == nil || gate.db == nil || ctx == nil || tenantID == uuid.Nil ||
		!validWechatAppID(appID) || !runtimeMerchantIDPattern.MatchString(merchantID) ||
		merchantConfigGenerationID == uuid.Nil {
		return ErrInvalidPaymentCloseWorker
	}
	var aligned bool
	if err := gate.db.queryRowContext(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM xiangwan_payment_merchant_config_generations
    WHERE tenant_id = $1
      AND id = $4
      AND provider = 'wechat'
      AND payment_app_id = $2
      AND payment_merchant_id = $3
      AND status IN ('active', 'draining')
)
AND NOT EXISTS (
    SELECT 1
    FROM xiangwan_payment_close_jobs
    WHERE tenant_id = $1
      AND job_status <> 'completed'
      AND (merchant_config_generation_id IS NULL OR merchant_config_generation_id = $4)
      AND (
          payment_app_id IS DISTINCT FROM $2
          OR payment_merchant_id IS DISTINCT FROM $3
      )
)
`, tenantID, appID, merchantID, merchantConfigGenerationID).Scan(&aligned); err != nil {
		return fmt.Errorf("check xiangwan payment close identity: %w", err)
	}
	if !aligned {
		return ErrPaymentCloseWorkerIdentityMismatch
	}
	return nil
}
