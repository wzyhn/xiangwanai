package xiangwanruntime

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	xiangwanapi "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/api"
	"github.com/google/uuid"
)

func TestNewServerRejectsIncompleteConfiguration(t *testing.T) {
	t.Parallel()

	if _, err := NewServer(Config{}); !errors.Is(err, ErrInvalidRuntimeServer) {
		t.Fatalf("NewServer(invalid) error = %v", err)
	}
}

func TestBuildWeChatPrepayServiceKeepsProviderDisabledWithoutKeyFiles(
	t *testing.T,
) {
	t.Parallel()

	service, err := buildWeChatPrepayService(
		context.Background(),
		nil,
		Config{TenantID: runtimeUUID(40), GenerationID: runtimeUUID(41)},
	)
	if err != nil || service == nil {
		t.Fatalf("buildWeChatPrepayService(disabled) = %v, %v", service, err)
	}
	_, err = service.Create(
		context.Background(),
		runtimeUUID(42),
		runtimeUUID(43),
		xiangwanapi.WeChatPrepayRequest{
			IdempotencyKey:       uuid.New(),
			ExpectedOrderVersion: 1,
			ExpectedPayableCents: 9_900,
		},
	)
	if !errors.Is(err, xiangwanapi.ErrWeChatPrepayDisabled) {
		t.Fatalf("disabled prepay Create() error = %v", err)
	}
}

func TestBuildWeChatPaymentServicesRejectsPaidRegistrationWithoutPrepay(
	t *testing.T,
) {
	t.Parallel()

	config := Config{
		TenantID:                runtimeUUID(46),
		GenerationID:            runtimeUUID(47),
		PaidRegistrationEnabled: true,
		PaymentMerchantID:       "1900000109",
	}
	services, err := buildWeChatPaymentServices(
		context.Background(),
		nil,
		config,
	)
	if services.prepay != nil || !errors.Is(err, ErrInvalidRuntimeServer) {
		t.Fatalf("buildWeChatPaymentServices(mismatch) = %+v, %v", services, err)
	}
}

func TestBuildWeChatPrepayServiceFailsClosedWhenKeyFileCannotBeLoaded(
	t *testing.T,
) {
	t.Parallel()

	config := Config{
		TenantID:                runtimeUUID(44),
		GenerationID:            runtimeUUID(45),
		AppID:                   testRuntimeAppID,
		PaidRegistrationEnabled: true,
		PaymentMerchantID:       "1900000109",
		WeChatPrepayEnabled:     true,
		WeChatPayCertSerial:     "0123456789ABCDEF",
		WeChatPayPrivateKeyFile: "synthetic-missing-merchant-key.pem",
		WeChatPayPublicKeyID:    "PUB_KEY_ID_0123456789ABCDEF",
		WeChatPayPublicKeyFile:  "synthetic-missing-wechat-key.pem",
		WeChatPayAPIV3Key:       testRuntimeAPIV3Key,
		WeChatPayNotifyURL:      "https://pay.example.com/api/v1/xiangwan/integrations/wechat-pay/notifications",
		WeChatPayDescription:    "Xiangwan activity registration",
	}
	service, err := buildWeChatPrepayService(
		context.Background(),
		&sql.DB{},
		config,
	)
	if service != nil || !errors.Is(err, ErrInvalidRuntimeServer) ||
		strings.Contains(err.Error(), config.WeChatPayPrivateKeyFile) {
		t.Fatalf("buildWeChatPrepayService(missing key) = %v, %v", service, err)
	}
}
