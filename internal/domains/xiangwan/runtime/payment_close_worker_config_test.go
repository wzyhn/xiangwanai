package xiangwanruntime

import (
	"errors"
	"strings"
	"testing"
)

func TestLoadPaymentCloseWorkerConfigUsesOnlyPostgresAndProviderIdentity(
	t *testing.T,
) {
	t.Parallel()

	values := validPaymentCloseWorkerEnvironment()
	config, err := LoadPaymentCloseWorkerConfig(mapEnvironment(values))
	if err != nil || config.DatabaseDSN != values[PaymentCloseDatabaseDSNEnv] ||
		config.TenantID.String() != values[TenantIDEnv] ||
		config.GenerationID.String() != values[GenerationIDEnv] ||
		config.PaymentAppID != values[PaymentCloseAppIDEnv] ||
		config.PaymentMerchantID != values[PaymentCloseMerchantIDEnv] ||
		config.MerchantConfigGenerationID.String() != values[PaymentCloseMerchantConfigGenerationIDEnv] ||
		config.WeChatPayCertSerial != values[PaymentCloseCertSerialEnv] ||
		config.WeChatPayPrivateKeyFile != values[PaymentClosePrivateKeyFileEnv] ||
		config.WeChatPayPublicKeyID != values[PaymentClosePublicKeyIDEnv] ||
		config.WeChatPayPublicKeyFile != values[PaymentClosePublicKeyFileEnv] {
		t.Fatalf("LoadPaymentCloseWorkerConfig() = %+v, %v", config, err)
	}
}

func TestLoadPaymentCloseWorkerConfigFailsClosedWithoutEchoingValues(
	t *testing.T,
) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(map[string]string)
	}{
		{name: "missing database", mutate: func(values map[string]string) {
			delete(values, PaymentCloseDatabaseDSNEnv)
		}},
		{name: "invalid tenant", mutate: func(values map[string]string) {
			values[TenantIDEnv] = "not-a-tenant"
		}},
		{name: "invalid generation", mutate: func(values map[string]string) {
			values[GenerationIDEnv] = "not-a-generation"
		}},
		{name: "invalid merchant config generation", mutate: func(values map[string]string) {
			values[PaymentCloseMerchantConfigGenerationIDEnv] = "not-a-generation"
		}},
		{name: "invalid merchant", mutate: func(values map[string]string) {
			values[PaymentCloseMerchantIDEnv] = "merchant-secret"
		}},
		{name: "missing app id", mutate: func(values map[string]string) {
			delete(values, PaymentCloseAppIDEnv)
		}},
		{name: "missing certificate", mutate: func(values map[string]string) {
			delete(values, PaymentCloseCertSerialEnv)
		}},
		{name: "padded private key path", mutate: func(values map[string]string) {
			values[PaymentClosePrivateKeyFileEnv] = " /private/do-not-echo.pem "
		}},
		{name: "invalid public key id", mutate: func(values map[string]string) {
			values[PaymentClosePublicKeyIDEnv] = "do-not-echo"
		}},
		{name: "missing public key path", mutate: func(values map[string]string) {
			delete(values, PaymentClosePublicKeyFileEnv)
		}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			values := validPaymentCloseWorkerEnvironment()
			values[PaymentCloseDatabaseDSNEnv] =
				"postgres://worker:do-not-echo@db/xiangwan"
			test.mutate(values)
			_, err := LoadPaymentCloseWorkerConfig(mapEnvironment(values))
			if !errors.Is(err, ErrInvalidRuntimeConfig) ||
				strings.Contains(err.Error(), "do-not-echo") {
				t.Fatalf("LoadPaymentCloseWorkerConfig(invalid) error = %v", err)
			}
		})
	}
}

func validPaymentCloseWorkerEnvironment() map[string]string {
	return map[string]string{
		PaymentCloseDatabaseDSNEnv: "postgres://worker:synthetic@db/xiangwan",
		TenantIDEnv:                "00000000-0000-4000-8000-000000000007",
		GenerationIDEnv:            "00000000-0000-4000-8000-000000000008",
		PaymentCloseMerchantConfigGenerationIDEnv: "00000000-0000-4000-8000-000000000009",
		PaymentCloseAppIDEnv:                      "wx1234567890abcdef",
		PaymentCloseMerchantIDEnv:                 "1900000109",
		PaymentCloseCertSerialEnv:                 "0123456789ABCDEF",
		PaymentClosePrivateKeyFileEnv:             "/run/secrets/xiangwan-close-private.pem",
		PaymentClosePublicKeyIDEnv:                "PUB_KEY_ID_0123456789ABCDEF",
		PaymentClosePublicKeyFileEnv:              "/run/secrets/xiangwan-close-public.pem",
	}
}
