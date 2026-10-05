package xiangwanruntime

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCouponGrantPolicyKeyRequiresCompleteAdminAndCanonicalKey(t *testing.T) {
	values := map[string]string{
		APIDatabaseDSNEnv:             "postgres://runtime:secret@db/xiangwan",
		TenantIDEnv:                   "00000000-0000-4000-8000-000000000001",
		GenerationIDEnv:               "00000000-0000-4000-8000-000000000002",
		JWTSigningKeyEnv:              testRuntimeSigningKey,
		AppIDEnv:                      testRuntimeAppID,
		AppSecretEnv:                  testRuntimeAppSecret,
		StorageLocalDirEnv:            "C:\\data\\xiangwan",
		CheckinCredentialHMACKeyEnv:   testRuntimeCheckinKey,
		CouponGrantPolicyPublicKeyEnv: base64.StdEncoding.EncodeToString(ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize)).Public().(ed25519.PublicKey)),
	}
	if _, err := LoadConfig(mapEnvironment(values)); !errors.Is(err, ErrInvalidRuntimeConfig) {
		t.Fatalf("policy key without administrator boundary accepted: %v", err)
	}
	values[AdminOriginEnv] = "https://admin.example.com"
	values[AdminOIDCIssuerEnv] = "https://id.example.com"
	values[AdminOIDCClientIDEnv] = "xiangwan-admin"
	values[AdminOIDCClientSecretEnv] = "test-client-secret"
	values[AdminOIDCRedirectURLEnv] = "https://admin.example.com/api/v1/xiangwan/admin/auth/callback"
	values[AdminSessionKeyEnv] = base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	config, err := LoadConfig(mapEnvironment(values))
	if err != nil || len(config.CouponGrantPolicyPublicKey) != ed25519.PublicKeySize {
		t.Fatalf("complete signed-policy boundary = %v, %v", config.CouponGrantPolicyPublicKey, err)
	}
	values[CouponGrantPolicyPublicKeyEnv] = "not-base64"
	if _, err := LoadConfig(mapEnvironment(values)); !errors.Is(err, ErrInvalidRuntimeConfig) {
		t.Fatalf("invalid policy key accepted: %v", err)
	}
}

const (
	testRuntimeSigningKey  = "synthetic-xiangwan-active-signing-key-for-tests"
	testRuntimePreviousKey = "synthetic-xiangwan-previous-signing-key-for-tests"
	testRuntimeAppID       = "wx1234567890abcdef"
	testRuntimeAppSecret   = "test" + "-xiangwan-app-secret"
	testRuntimeAPIV3Key    = "synthetic-api-v3-key-for-tests!!"
	testRuntimeCheckinKey  = "synthetic-xiangwan-checkin-credential-key-for-tests"
)

func TestLoadConfigBuildsPGOnlyRuntimeConfiguration(t *testing.T) {
	t.Parallel()

	values := map[string]string{
		APIDatabaseDSNEnv:               " postgres://runtime:secret@db/xiangwan ",
		TenantIDEnv:                     "00000000-0000-4000-8000-000000000001",
		GenerationIDEnv:                 "00000000-0000-4000-8000-000000000002",
		JWTSigningKeyEnv:                testRuntimeSigningKey,
		JWTPreviousSecretsEnv:           " " + testRuntimePreviousKey + " ",
		AppIDEnv:                        testRuntimeAppID,
		AppSecretEnv:                    testRuntimeAppSecret,
		ListenAddressEnv:                "127.0.0.1:8088",
		TrustedProxiesEnv:               "127.0.0.1,10.0.0.0/8",
		ExternalDomainsEnv:              "docs.example.com, *.media.example.com",
		ExternalDomainsPolicyVersionEnv: "external-links-v2",
		PrivacyPolicyVersionEnv:         "privacy-v3",
		UserAgreementVersionEnv:         "terms-v2",
		CustomerServicePolicyVersionEnv: "customer-service-v1",
		StorageLocalDirEnv:              " C:\\data\\xiangwan ",
		ManualContactEnabledEnv:         "true",
		ManualContactPolicyVersionEnv:   "contact-v1",
		ManualContactPolicyTextEnv:      "联系人信息仅用于本次活动联络。",
		PaidRegistrationEnabledEnv:      "false",
		CancelPolicyVersionEnv:          "cancel-v3",
		CancelPolicyCutoffHoursEnv:      "24",
		CouponRefundPolicyVersionEnv:    "coupon-refund-v2",
		CouponRefundDispositionEnv:      "restore",
		CheckinCredentialHMACKeyEnv:     testRuntimeCheckinKey,
		CheckinCredentialTTLSecondsEnv:  "300",
	}
	config, err := LoadConfig(mapEnvironment(values))
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if config.DatabaseDSN != "postgres://runtime:secret@db/xiangwan" ||
		config.TenantID.String() != values[TenantIDEnv] ||
		config.GenerationID.String() != values[GenerationIDEnv] ||
		config.JWTSigningKey != testRuntimeSigningKey ||
		len(config.JWTPreviousSecrets) != 1 ||
		config.JWTPreviousSecrets[0] != testRuntimePreviousKey ||
		config.AppID != testRuntimeAppID ||
		config.AppSecret != testRuntimeAppSecret ||
		config.ListenAddress != values[ListenAddressEnv] ||
		len(config.TrustedProxies) != 2 ||
		config.TrustedProxies[0] != "127.0.0.1" ||
		config.TrustedProxies[1] != "10.0.0.0/8" ||
		config.StorageLocalDir != "C:\\data\\xiangwan" ||
		config.ExternalDomainsPolicyVersion != "external-links-v2" ||
		config.PrivacyPolicyVersion != "privacy-v3" ||
		config.UserAgreementVersion != "terms-v2" ||
		config.CustomerServicePolicyVersion != "customer-service-v1" {
		t.Fatalf("LoadConfig() = %+v", config)
	}
	if !config.ManualContactEnabled ||
		config.ManualContactPolicyVersion != "contact-v1" ||
		config.ManualContactPolicyText != "联系人信息仅用于本次活动联络。" {
		t.Fatalf("manual contact config = %+v", config)
	}
	if config.PaidRegistrationEnabled || config.PaymentMerchantID != "" {
		t.Fatalf("paid Registration config = %+v", config)
	}
	if config.PaymentMerchantConfigGenerationID != uuid.Nil {
		t.Fatalf("disabled merchant config generation = %+v", config.PaymentMerchantConfigGenerationID)
	}
	if config.CancelPolicyVersion != "cancel-v3" ||
		config.CancelPolicyCutoffHours != 24 ||
		config.CouponRefundPolicyVersion != "coupon-refund-v2" ||
		config.CouponRefundDisposition != "restore" {
		t.Fatalf("cancellation config = %+v", config)
	}
	if config.CheckinCredentialHMACKey != testRuntimeCheckinKey ||
		config.CheckinCredentialTTL != 5*time.Minute {
		t.Fatalf("Checkin credential config is invalid")
	}
	for _, rawURL := range []string{
		"https://docs.example.com/review",
		"https://a.media.example.com/photo.webp",
	} {
		if _, allowed := config.ExternalDomains.AllowURL(rawURL); !allowed {
			t.Fatalf("configured domain rejected: %s", rawURL)
		}
	}
	if _, allowed := config.ExternalDomains.AllowURL(
		"https://media.example.com/root",
	); allowed {
		t.Fatal("wildcard policy unexpectedly allowed its root")
	}
}

func TestLoadConfigDefaultsListenAndFailsClosedWithoutExternalDomains(
	t *testing.T,
) {
	t.Parallel()

	config, err := LoadConfig(mapEnvironment(map[string]string{
		APIDatabaseDSNEnv:           "postgres://db/xiangwan",
		TenantIDEnv:                 "00000000-0000-4000-8000-000000000003",
		GenerationIDEnv:             "00000000-0000-4000-8000-000000000004",
		JWTSigningKeyEnv:            testRuntimeSigningKey,
		AppIDEnv:                    testRuntimeAppID,
		AppSecretEnv:                testRuntimeAppSecret,
		StorageLocalDirEnv:          "C:\\data\\xiangwan",
		CheckinCredentialHMACKeyEnv: testRuntimeCheckinKey,
	}))
	if err != nil || config.ListenAddress != defaultListenAddress {
		t.Fatalf("LoadConfig(defaults) = %+v, %v", config, err)
	}
	if len(config.TrustedProxies) != 0 {
		t.Fatalf("trusted proxy defaults = %+v", config.TrustedProxies)
	}
	if _, allowed := config.ExternalDomains.AllowURL(
		"https://docs.example.com/review",
	); allowed {
		t.Fatal("missing external-domain config did not fail closed")
	}
	if config.ManualContactEnabled || config.ManualContactPolicyVersion != "" ||
		config.ManualContactPolicyText != "" {
		t.Fatalf("manual contact default = %+v", config)
	}
	if config.ExternalDomainsPolicyVersion != "" ||
		config.PrivacyPolicyVersion != "" || config.UserAgreementVersion != "" ||
		config.CustomerServicePolicyVersion != "" {
		t.Fatalf("public policy defaults = %+v", config)
	}
	if config.PaidRegistrationEnabled || config.PaymentMerchantID != "" {
		t.Fatalf("paid Registration default = %+v", config)
	}
	if config.CancelPolicyVersion != "" || config.CancelPolicyCutoffHours != 0 ||
		config.CouponRefundPolicyVersion != "" ||
		config.CouponRefundDisposition != "" {
		t.Fatalf("cancellation policy default = %+v", config)
	}
	if config.WeChatPrepayEnabled || config.WeChatPayCertSerial != "" ||
		config.WeChatPayPrivateKeyFile != "" ||
		config.WeChatPayPublicKeyID != "" ||
		config.WeChatPayPublicKeyFile != "" ||
		config.WeChatPayAPIV3Key != "" ||
		config.WeChatPayNotifyURL != "" || config.WeChatPayDescription != "" {
		t.Fatalf("WeChat prepay default = %+v", config)
	}
	if config.CheckinCredentialHMACKey != testRuntimeCheckinKey ||
		config.CheckinCredentialTTL != defaultCheckinCredentialTTL {
		t.Fatal("Checkin credential defaults are invalid")
	}
}

func TestLoadConfigTreatsEmptyOptionalCancellationPairsAsDisabled(t *testing.T) {
	t.Parallel()

	values := map[string]string{
		APIDatabaseDSNEnv:            "postgres://db/xiangwan",
		TenantIDEnv:                  "00000000-0000-4000-8000-000000000003",
		GenerationIDEnv:              "00000000-0000-4000-8000-000000000004",
		JWTSigningKeyEnv:             testRuntimeSigningKey,
		AppIDEnv:                     testRuntimeAppID,
		AppSecretEnv:                 testRuntimeAppSecret,
		StorageLocalDirEnv:           "C:\\data\\xiangwan",
		CancelPolicyVersionEnv:       "",
		CancelPolicyCutoffHoursEnv:   "",
		CouponRefundPolicyVersionEnv: "",
		CouponRefundDispositionEnv:   "",
		CheckinCredentialHMACKeyEnv:  testRuntimeCheckinKey,
	}
	config, err := LoadConfig(mapEnvironment(values))
	if err != nil || config.CancelPolicyVersion != "" ||
		config.CouponRefundPolicyVersion != "" {
		t.Fatalf("LoadConfig(empty cancellation policy) = %+v, %v", config, err)
	}
}

func TestLoadConfigBuildsEnabledWeChatPrepayConfiguration(t *testing.T) {
	t.Parallel()

	values := validWeChatPrepayEnvironment()
	config, err := LoadConfig(mapEnvironment(values))
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if !config.PaidRegistrationEnabled || !config.WeChatPrepayEnabled ||
		config.PaymentMerchantID != values[PaymentMerchantIDEnv] ||
		config.PaymentMerchantConfigGenerationID.String() != values[PaymentMerchantConfigGenerationIDEnv] ||
		config.WeChatPayCertSerial != values[WeChatPayCertSerialEnv] ||
		config.WeChatPayPrivateKeyFile != values[WeChatPayPrivateKeyFileEnv] ||
		config.WeChatPayPublicKeyID != values[WeChatPayPublicKeyIDEnv] ||
		config.WeChatPayPublicKeyFile != values[WeChatPayPublicKeyFileEnv] ||
		config.WeChatPayAPIV3Key != values[WeChatPayAPIV3KeyEnv] ||
		config.WeChatPayNotifyURL != values[WeChatPayNotifyURLEnv] ||
		config.WeChatPayDescription != values[WeChatPayDescriptionEnv] {
		t.Fatalf("enabled WeChat prepay config = %+v", config)
	}
}

func TestLoadConfigRejectsPaidRegistrationWithoutPrepayConvergence(t *testing.T) {
	t.Parallel()

	values := map[string]string{
		APIDatabaseDSNEnv:                    "postgres://runtime:secret@db/xiangwan",
		TenantIDEnv:                          "00000000-0000-4000-8000-000000000009",
		GenerationIDEnv:                      "00000000-0000-4000-8000-00000000000a",
		JWTSigningKeyEnv:                     testRuntimeSigningKey,
		AppIDEnv:                             testRuntimeAppID,
		AppSecretEnv:                         testRuntimeAppSecret,
		StorageLocalDirEnv:                   "C:\\data\\xiangwan",
		CheckinCredentialHMACKeyEnv:          testRuntimeCheckinKey,
		PaidRegistrationEnabledEnv:           "true",
		PaymentMerchantIDEnv:                 "1900000109",
		PaymentMerchantConfigGenerationIDEnv: "00000000-0000-4000-8000-00000000000b",
		WeChatPrepayEnabledEnv:               "false",
	}
	_, err := LoadConfig(mapEnvironment(values))
	if !errors.Is(err, ErrInvalidRuntimeConfig) ||
		!strings.Contains(err.Error(), WeChatPrepayEnabledEnv) {
		t.Fatalf("LoadConfig(paid without prepay) error = %v", err)
	}
}

func TestLoadConfigRejectsInvalidValuesWithoutEchoingSecrets(t *testing.T) {
	t.Parallel()

	valid := map[string]string{
		APIDatabaseDSNEnv:           "postgres://runtime:do-not-echo@db/xiangwan",
		TenantIDEnv:                 "00000000-0000-4000-8000-000000000005",
		GenerationIDEnv:             "00000000-0000-4000-8000-000000000006",
		JWTSigningKeyEnv:            testRuntimeSigningKey,
		AppIDEnv:                    testRuntimeAppID,
		AppSecretEnv:                testRuntimeAppSecret,
		StorageLocalDirEnv:          "C:\\data\\xiangwan",
		CheckinCredentialHMACKeyEnv: testRuntimeCheckinKey,
	}
	tests := []struct {
		name   string
		mutate func(map[string]string)
	}{
		{name: "missing DSN", mutate: func(values map[string]string) { delete(values, APIDatabaseDSNEnv) }},
		{name: "noncanonical tenant", mutate: func(values map[string]string) {
			values[TenantIDEnv] = strings.ToUpper("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
		}},
		{name: "missing generation", mutate: func(values map[string]string) { delete(values, GenerationIDEnv) }},
		{name: "missing signing key", mutate: func(values map[string]string) { delete(values, JWTSigningKeyEnv) }},
		{name: "short signing key", mutate: func(values map[string]string) { values[JWTSigningKeyEnv] = "short" }},
		{name: "active key repeated as previous", mutate: func(values map[string]string) {
			values[JWTPreviousSecretsEnv] = testRuntimeSigningKey
		}},
		{name: "duplicate previous key", mutate: func(values map[string]string) {
			values[JWTPreviousSecretsEnv] = testRuntimePreviousKey + "," + testRuntimePreviousKey
		}},
		{name: "too many previous keys", mutate: func(values map[string]string) {
			values[JWTPreviousSecretsEnv] = testRuntimePreviousKey + "," +
				"synthetic-xiangwan-previous-signing-key-two-tests," +
				"synthetic-xiangwan-previous-signing-key-three-tests"
		}},
		{name: "invalid AppID", mutate: func(values map[string]string) { values[AppIDEnv] = "other-app" }},
		{name: "missing AppSecret", mutate: func(values map[string]string) { delete(values, AppSecretEnv) }},
		{name: "short AppSecret", mutate: func(values map[string]string) { values[AppSecretEnv] = "too-short" }},
		{name: "padded AppSecret", mutate: func(values map[string]string) { values[AppSecretEnv] = " " + testRuntimeAppSecret }},
		{name: "missing storage", mutate: func(values map[string]string) { delete(values, StorageLocalDirEnv) }},
		{name: "missing Checkin credential key", mutate: func(values map[string]string) { delete(values, CheckinCredentialHMACKeyEnv) }},
		{name: "short Checkin credential key", mutate: func(values map[string]string) { values[CheckinCredentialHMACKeyEnv] = "too-short" }},
		{name: "padded Checkin credential key", mutate: func(values map[string]string) { values[CheckinCredentialHMACKeyEnv] = " " + testRuntimeCheckinKey }},
		{name: "Checkin key reuses active JWT key", mutate: func(values map[string]string) {
			values[CheckinCredentialHMACKeyEnv] = testRuntimeSigningKey
		}},
		{name: "Checkin key reuses previous JWT key", mutate: func(values map[string]string) {
			values[JWTPreviousSecretsEnv] = testRuntimePreviousKey
			values[CheckinCredentialHMACKeyEnv] = testRuntimePreviousKey
		}},
		{name: "noncanonical Checkin credential TTL", mutate: func(values map[string]string) { values[CheckinCredentialTTLSecondsEnv] = "0600" }},
		{name: "overflowing Checkin credential TTL", mutate: func(values map[string]string) {
			values[CheckinCredentialTTLSecondsEnv] = "9223372036854775807"
		}},
		{name: "excessive Checkin credential TTL", mutate: func(values map[string]string) { values[CheckinCredentialTTLSecondsEnv] = "901" }},
		{name: "wildcard listen", mutate: func(values map[string]string) { values[ListenAddressEnv] = ":0" }},
		{name: "invalid trusted proxy", mutate: func(values map[string]string) { values[TrustedProxiesEnv] = "proxy.internal" }},
		{name: "padded trusted proxy", mutate: func(values map[string]string) { values[TrustedProxiesEnv] = "127.0.0.1, 10.0.0.0/8" }},
		{name: "trust all proxy range", mutate: func(values map[string]string) { values[TrustedProxiesEnv] = "0.0.0.0/0" }},
		{name: "unsafe domain", mutate: func(values map[string]string) { values[ExternalDomainsEnv] = "localhost" }},
		{name: "empty domain member", mutate: func(values map[string]string) { values[ExternalDomainsEnv] = "docs.example.com," }},
		{name: "external domains without policy version", mutate: func(values map[string]string) {
			values[ExternalDomainsEnv] = "docs.example.com"
		}},
		{name: "external policy version without domains", mutate: func(values map[string]string) {
			values[ExternalDomainsPolicyVersionEnv] = "external-links-v2"
		}},
		{name: "padded privacy policy version", mutate: func(values map[string]string) {
			values[PrivacyPolicyVersionEnv] = " privacy-v3"
		}},
		{name: "invalid user agreement version", mutate: func(values map[string]string) {
			values[UserAgreementVersionEnv] = "terms/v2"
		}},
		{name: "multiline customer service policy version", mutate: func(values map[string]string) {
			values[CustomerServicePolicyVersionEnv] = "customer-service-v1\nnext"
		}},
		{name: "invalid manual contact flag", mutate: func(values map[string]string) {
			values[ManualContactEnabledEnv] = "yes"
		}},
		{name: "enabled manual contact missing policy", mutate: func(values map[string]string) {
			values[ManualContactEnabledEnv] = "true"
		}},
		{name: "enabled manual contact missing text", mutate: func(values map[string]string) {
			values[ManualContactEnabledEnv] = "true"
			values[ManualContactPolicyVersionEnv] = "contact-v1"
		}},
		{name: "disabled manual contact with policy", mutate: func(values map[string]string) {
			values[ManualContactEnabledEnv] = "false"
			values[ManualContactPolicyVersionEnv] = "contact-v1"
		}},
		{name: "disabled manual contact with text", mutate: func(values map[string]string) {
			values[ManualContactEnabledEnv] = "false"
			values[ManualContactPolicyTextEnv] = "公开说明"
		}},
		{name: "padded manual contact text", mutate: func(values map[string]string) {
			values[ManualContactEnabledEnv] = "true"
			values[ManualContactPolicyVersionEnv] = "contact-v1"
			values[ManualContactPolicyTextEnv] = " 公开说明"
		}},
		{name: "invalid paid Registration flag", mutate: func(values map[string]string) {
			values[PaidRegistrationEnabledEnv] = "yes"
		}},
		{name: "enabled paid Registration missing merchant", mutate: func(values map[string]string) {
			values[PaidRegistrationEnabledEnv] = "true"
		}},
		{name: "disabled paid Registration with merchant", mutate: func(values map[string]string) {
			values[PaidRegistrationEnabledEnv] = "false"
			values[PaymentMerchantIDEnv] = "merchant-1"
		}},
		{name: "paid Registration missing merchant config generation", mutate: func(values map[string]string) {
			values[PaidRegistrationEnabledEnv] = "true"
			values[PaymentMerchantIDEnv] = "1900000109"
			values[WeChatPrepayEnabledEnv] = "true"
			delete(values, PaymentMerchantConfigGenerationIDEnv)
		}},
		{name: "disabled paid Registration with merchant config generation", mutate: func(values map[string]string) {
			values[PaymentMerchantConfigGenerationIDEnv] = "00000000-0000-4000-8000-00000000000b"
		}},
		{name: "cancellation version without cutoff", mutate: func(values map[string]string) {
			values[CancelPolicyVersionEnv] = "cancel-v3"
		}},
		{name: "cancellation cutoff without version", mutate: func(values map[string]string) {
			values[CancelPolicyCutoffHoursEnv] = "24"
		}},
		{name: "noncanonical cancellation cutoff", mutate: func(values map[string]string) {
			values[CancelPolicyVersionEnv] = "cancel-v3"
			values[CancelPolicyCutoffHoursEnv] = "024"
		}},
		{name: "excessive cancellation cutoff", mutate: func(values map[string]string) {
			values[CancelPolicyVersionEnv] = "cancel-v3"
			values[CancelPolicyCutoffHoursEnv] = "8785"
		}},
		{name: "Coupon policy version without disposition", mutate: func(values map[string]string) {
			values[CouponRefundPolicyVersionEnv] = "coupon-refund-v2"
		}},
		{name: "Coupon disposition without policy version", mutate: func(values map[string]string) {
			values[CouponRefundDispositionEnv] = "restore"
		}},
		{name: "invalid Coupon disposition", mutate: func(values map[string]string) {
			values[CouponRefundPolicyVersionEnv] = "coupon-refund-v2"
			values[CouponRefundDispositionEnv] = "automatic"
		}},
		{name: "invalid WeChat prepay flag", mutate: func(values map[string]string) {
			values[WeChatPrepayEnabledEnv] = "yes"
		}},
		{name: "disabled WeChat prepay with provider config", mutate: func(values map[string]string) {
			values[WeChatPrepayEnabledEnv] = "false"
			values[WeChatPayNotifyURLEnv] = "https://pay.example.com/notify"
		}},
		{name: "disabled WeChat prepay with padded provider config", mutate: func(values map[string]string) {
			values[WeChatPrepayEnabledEnv] = "false"
			values[WeChatPayNotifyURLEnv] = " "
		}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			values := make(map[string]string, len(valid))
			for key, value := range valid {
				values[key] = value
			}
			test.mutate(values)
			_, err := LoadConfig(mapEnvironment(values))
			if !errors.Is(err, ErrInvalidRuntimeConfig) ||
				strings.Contains(err.Error(), "do-not-echo") {
				t.Fatalf("LoadConfig(invalid) error = %v", err)
			}
		})
	}
}

func TestLoadConfigRejectsIncompleteWeChatPrepayConfiguration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(map[string]string)
	}{
		{name: "paid Registration disabled", mutate: func(values map[string]string) {
			values[PaidRegistrationEnabledEnv] = "false"
			values[PaymentMerchantIDEnv] = ""
		}},
		{name: "nonnumeric merchant", mutate: func(values map[string]string) {
			values[PaymentMerchantIDEnv] = "merchant-1"
		}},
		{name: "missing certificate serial", mutate: func(values map[string]string) {
			delete(values, WeChatPayCertSerialEnv)
		}},
		{name: "lowercase certificate serial", mutate: func(values map[string]string) {
			values[WeChatPayCertSerialEnv] = "0123456789abcdef"
		}},
		{name: "missing private key path", mutate: func(values map[string]string) {
			delete(values, WeChatPayPrivateKeyFileEnv)
		}},
		{name: "padded private key path", mutate: func(values map[string]string) {
			values[WeChatPayPrivateKeyFileEnv] = " /run/secrets/merchant.pem "
		}},
		{name: "invalid public key id", mutate: func(values map[string]string) {
			values[WeChatPayPublicKeyIDEnv] = "CERTIFICATE_ID_1"
		}},
		{name: "missing public key path", mutate: func(values map[string]string) {
			delete(values, WeChatPayPublicKeyFileEnv)
		}},
		{name: "missing API v3 key", mutate: func(values map[string]string) {
			delete(values, WeChatPayAPIV3KeyEnv)
		}},
		{name: "short API v3 key", mutate: func(values map[string]string) {
			values[WeChatPayAPIV3KeyEnv] = "too-short"
		}},
		{name: "padded API v3 key", mutate: func(values map[string]string) {
			values[WeChatPayAPIV3KeyEnv] = " " + testRuntimeAPIV3Key
		}},
		{name: "HTTP notify URL", mutate: func(values map[string]string) {
			values[WeChatPayNotifyURLEnv] = "http://pay.example.com/notify"
		}},
		{name: "notify URL query", mutate: func(values map[string]string) {
			values[WeChatPayNotifyURLEnv] = "https://pay.example.com/notify?tenant=private"
		}},
		{name: "empty description", mutate: func(values map[string]string) {
			values[WeChatPayDescriptionEnv] = ""
		}},
		{name: "long description", mutate: func(values map[string]string) {
			values[WeChatPayDescriptionEnv] = strings.Repeat("活", 128)
		}},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			values := validWeChatPrepayEnvironment()
			test.mutate(values)
			_, err := LoadConfig(mapEnvironment(values))
			if !errors.Is(err, ErrInvalidRuntimeConfig) {
				t.Fatalf("LoadConfig(invalid prepay) error = %v", err)
			}
		})
	}
}

func validWeChatPrepayEnvironment() map[string]string {
	return map[string]string{
		APIDatabaseDSNEnv:                    "postgres://runtime:secret@db/xiangwan",
		TenantIDEnv:                          "00000000-0000-4000-8000-000000000007",
		GenerationIDEnv:                      "00000000-0000-4000-8000-000000000008",
		JWTSigningKeyEnv:                     testRuntimeSigningKey,
		AppIDEnv:                             testRuntimeAppID,
		AppSecretEnv:                         testRuntimeAppSecret,
		StorageLocalDirEnv:                   "C:\\data\\xiangwan",
		CheckinCredentialHMACKeyEnv:          testRuntimeCheckinKey,
		PaidRegistrationEnabledEnv:           "true",
		PaymentMerchantIDEnv:                 "1900000109",
		PaymentMerchantConfigGenerationIDEnv: "00000000-0000-4000-8000-00000000000b",
		WeChatPrepayEnabledEnv:               "true",
		WeChatPayCertSerialEnv:               "0123456789ABCDEF",
		WeChatPayPrivateKeyFileEnv:           "/run/secrets/xiangwan-merchant-private-key.pem",
		WeChatPayPublicKeyIDEnv:              "PUB_KEY_ID_0123456789ABCDEF",
		WeChatPayPublicKeyFileEnv:            "/run/secrets/wechat-pay-public-key.pem",
		WeChatPayAPIV3KeyEnv:                 testRuntimeAPIV3Key,
		WeChatPayNotifyURLEnv:                "https://pay.example.com/api/v1/xiangwan/integrations/wechat-pay/notifications",
		WeChatPayDescriptionEnv:              "Xiangwan activity registration",
	}
}

func mapEnvironment(values map[string]string) EnvironmentLookup {
	return func(name string) (string, bool) {
		value, exists := values[name]
		return value, exists
	}
}
