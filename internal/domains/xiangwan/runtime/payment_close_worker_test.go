package xiangwanruntime

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/google/uuid"
)

func TestPaymentCloseWorkerDrainsJobsThenStopsWithContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	processor := &fakePaymentCloseProcessor{}
	processor.process = func() (payment.PaymentCloseJob, bool, error) {
		processor.calls++
		if processor.calls < 3 {
			return payment.PaymentCloseJob{}, true, nil
		}
		cancel()
		return payment.PaymentCloseJob{}, false, nil
	}
	generationGate := &fakeWorkerGenerationGate{}
	schemaGate := &fakePaymentCloseSchemaGate{}
	worker := &PaymentCloseWorker{
		processor:                  processor,
		generationGate:             generationGate,
		schemaGate:                 schemaGate,
		tenantID:                   uuid.New(),
		generationID:               uuid.New(),
		paymentAppID:               "wx1234567890abcdef",
		paymentMerchantID:          "1900000109",
		merchantConfigGenerationID: uuid.New(),
		pollInterval:               time.Hour,
	}
	if err := worker.Run(ctx); err != nil || processor.calls != 3 ||
		generationGate.calls != 1 || schemaGate.calls != 2 {
		t.Fatalf(
			"Run() error=%v processor=%+v generation=%+v schema=%+v",
			err,
			processor,
			generationGate,
			schemaGate,
		)
	}
}

func TestPaymentCloseWorkerReturnsProcessingFailure(t *testing.T) {
	t.Parallel()

	processingError := payment.ErrPaymentCloseMerchantIdentityMismatch
	worker := &PaymentCloseWorker{
		processor: &fakePaymentCloseProcessor{
			process: func() (payment.PaymentCloseJob, bool, error) {
				return payment.PaymentCloseJob{}, false, processingError
			},
		},
		generationGate:             &fakeWorkerGenerationGate{},
		schemaGate:                 &fakePaymentCloseSchemaGate{},
		tenantID:                   uuid.New(),
		generationID:               uuid.New(),
		paymentAppID:               "wx1234567890abcdef",
		paymentMerchantID:          "1900000109",
		merchantConfigGenerationID: uuid.New(),
		pollInterval:               time.Second,
	}
	err := worker.Run(context.Background())
	if !errors.Is(err, processingError) ||
		!strings.Contains(err.Error(), "process xiangwan payment close job") {
		t.Fatalf("Run(processing failure) error = %v", err)
	}
}

func TestPaymentCloseWorkerRetriesLeaseLoss(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	processingError := payment.ErrPaymentCloseJobLeaseLost
	processor := &fakePaymentCloseProcessor{}
	processor.process = func() (payment.PaymentCloseJob, bool, error) {
		processor.calls++
		if processor.calls == 1 {
			return payment.PaymentCloseJob{}, false, processingError
		}
		cancel()
		return payment.PaymentCloseJob{}, false, nil
	}
	worker := &PaymentCloseWorker{
		processor:                  processor,
		generationGate:             &fakeWorkerGenerationGate{},
		schemaGate:                 &fakePaymentCloseSchemaGate{},
		tenantID:                   uuid.New(),
		generationID:               uuid.New(),
		paymentAppID:               "wx1234567890abcdef",
		paymentMerchantID:          "1900000109",
		merchantConfigGenerationID: uuid.New(),
		pollInterval:               time.Millisecond,
	}
	if err := worker.Run(ctx); err != nil || processor.calls != 2 {
		t.Fatalf("Run(transient processing failure) error=%v calls=%d", err, processor.calls)
	}
}

func TestPaymentCloseWorkerReadinessFailsBeforeProcessing(t *testing.T) {
	t.Parallel()

	inactive := errors.New("inactive generation")
	processor := &fakePaymentCloseProcessor{}
	worker := &PaymentCloseWorker{
		processor:                  processor,
		generationGate:             &fakeWorkerGenerationGate{err: inactive},
		schemaGate:                 &fakePaymentCloseSchemaGate{},
		tenantID:                   uuid.New(),
		generationID:               uuid.New(),
		paymentAppID:               "wx1234567890abcdef",
		paymentMerchantID:          "1900000109",
		merchantConfigGenerationID: uuid.New(),
		pollInterval:               time.Second,
	}
	if err := worker.Run(context.Background()); !errors.Is(err, inactive) ||
		processor.calls != 0 {
		t.Fatalf("Run(inactive) error=%v processor=%+v", err, processor)
	}
}

func TestNewPaymentCloseProviderLoadsLocalCredentialsWithoutNetwork(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey() error = %v", err)
	}
	publicKey := &privateKey.PublicKey
	privatePath := filepath.Join(directory, "merchant-private.pem")
	publicPath := filepath.Join(directory, "wechat-public.pem")
	privateDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatalf("x509.MarshalPKCS8PrivateKey() error = %v", err)
	}
	if err := os.WriteFile(privatePath, pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: privateDER,
	}), 0o600); err != nil {
		t.Fatalf("write private key: %v", err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		t.Fatalf("x509.MarshalPKIXPublicKey() error = %v", err)
	}
	if err := os.WriteFile(publicPath, pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: publicDER,
	}), 0o600); err != nil {
		t.Fatalf("write public key: %v", err)
	}
	provider, err := newPaymentCloseProvider(context.Background(), PaymentCloseWorkerConfig{
		PaymentMerchantID:       "1900000109",
		WeChatPayCertSerial:     "0123456789ABCDEF",
		WeChatPayPrivateKeyFile: privatePath,
		WeChatPayPublicKeyID:    "PUB_KEY_ID_0123456789ABCDEF",
		WeChatPayPublicKeyFile:  publicPath,
	})
	if err != nil || provider == nil {
		t.Fatalf("newPaymentCloseProvider() = %v, %v", provider, err)
	}
}

func TestNewPaymentCloseProviderFailsClosedForMissingCredentials(t *testing.T) {
	t.Parallel()

	_, err := newPaymentCloseProvider(context.Background(), PaymentCloseWorkerConfig{
		PaymentMerchantID:       "1900000109",
		WeChatPayCertSerial:     "0123456789ABCDEF",
		WeChatPayPrivateKeyFile: filepath.Join(t.TempDir(), "missing-private.pem"),
		WeChatPayPublicKeyID:    "PUB_KEY_ID_0123456789ABCDEF",
		WeChatPayPublicKeyFile:  filepath.Join(t.TempDir(), "missing-public.pem"),
	})
	if !errors.Is(err, ErrInvalidPaymentCloseWorker) {
		t.Fatalf("newPaymentCloseProvider(missing) error = %v", err)
	}
}

func TestPostgresPaymentCloseSchemaGateRequiresMigration774(t *testing.T) {
	t.Parallel()

	var query string
	gate := &postgresPaymentCloseSchemaGate{db: &fakeGenerationExecutor{
		query: func(value string, _ ...any) generationRowScanner {
			query = value
			return fakeGenerationRow{active: true}
		},
	}}
	if err := gate.CheckPaymentCloseSchema(context.Background()); err != nil {
		t.Fatalf("CheckPaymentCloseSchema() error = %v", err)
	}
	if !strings.Contains(query, "public.xiangwan_payment_close_jobs") {
		t.Fatalf("schema query = %q", query)
	}

	gate.db = &fakeGenerationExecutor{
		query: func(string, ...any) generationRowScanner {
			return fakeGenerationRow{}
		},
	}
	if err := gate.CheckPaymentCloseSchema(context.Background()); !errors.Is(err, ErrPaymentCloseWorkerSchemaUnavailable) {
		t.Fatalf("CheckPaymentCloseSchema(missing) error = %v", err)
	}
}

func TestPostgresPaymentCloseSchemaGateRejectsQueuedIdentityMismatch(t *testing.T) {
	t.Parallel()

	var query string
	var args []any
	gate := &postgresPaymentCloseSchemaGate{db: &fakeGenerationExecutor{
		query: func(value string, captured ...any) generationRowScanner {
			query = value
			args = captured
			return fakeGenerationRow{active: true}
		},
	}}
	tenantID := uuid.New()
	if err := gate.CheckPaymentCloseIdentity(
		context.Background(),
		tenantID,
		"wx1234567890abcdef",
		"1900000109",
		uuid.MustParse("00000000-0000-4000-8000-000000000009"),
	); err != nil {
		t.Fatalf("CheckPaymentCloseIdentity() error = %v", err)
	}
	if !strings.Contains(query, "payment_app_id IS DISTINCT FROM $2") ||
		!strings.Contains(query, "payment_merchant_id IS DISTINCT FROM $3") ||
		!strings.Contains(query, "merchant_config_generation_id IS NULL OR merchant_config_generation_id = $4") ||
		!strings.Contains(query, "FROM xiangwan_payment_merchant_config_generations") ||
		!strings.Contains(query, "status IN ('active', 'draining')") {
		t.Fatalf("identity query = %q", query)
	}
	if len(args) != 4 || args[0] != tenantID || args[1] != "wx1234567890abcdef" || args[2] != "1900000109" {
		t.Fatalf("identity args = %#v", args)
	}

	gate.db = &fakeGenerationExecutor{
		query: func(string, ...any) generationRowScanner {
			return fakeGenerationRow{}
		},
	}
	if !errors.Is(gate.CheckPaymentCloseIdentity(
		context.Background(), tenantID, "wx1234567890abcdef", "1900000109",
		uuid.MustParse("00000000-0000-4000-8000-000000000009"),
	), ErrPaymentCloseWorkerIdentityMismatch) {
		t.Fatal("CheckPaymentCloseIdentity(mismatch) did not fail closed")
	}
}

type fakePaymentCloseProcessor struct {
	process func() (payment.PaymentCloseJob, bool, error)
	calls   int
}

func (processor *fakePaymentCloseProcessor) ProcessNext(
	_ context.Context,
	_ uuid.UUID,
	_ uuid.UUID,
) (payment.PaymentCloseJob, bool, error) {
	if processor.process == nil {
		processor.calls++
		return payment.PaymentCloseJob{}, false, nil
	}
	return processor.process()
}

type fakeWorkerGenerationGate struct {
	err   error
	calls int
}

func (gate *fakeWorkerGenerationGate) CheckActiveGeneration(
	_ context.Context,
	_ uuid.UUID,
	_ uuid.UUID,
) error {
	gate.calls++
	return gate.err
}

type fakePaymentCloseSchemaGate struct {
	err   error
	calls int
}

func (gate *fakePaymentCloseSchemaGate) CheckPaymentCloseSchema(
	_ context.Context,
) error {
	gate.calls++
	return gate.err
}

func (gate *fakePaymentCloseSchemaGate) CheckPaymentCloseIdentity(
	_ context.Context,
	_ uuid.UUID,
	_ string,
	_ string,
	_ uuid.UUID,
) error {
	gate.calls++
	return gate.err
}
