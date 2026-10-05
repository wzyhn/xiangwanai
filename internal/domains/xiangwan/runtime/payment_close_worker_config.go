package xiangwanruntime

import (
	"strings"

	"github.com/google/uuid"
)

const (
	PaymentCloseMerchantIDEnv                 = "XIANGWAN_PAYMENT_CLOSE_MERCHANT_ID"
	PaymentCloseMerchantConfigGenerationIDEnv = "XIANGWAN_PAYMENT_CLOSE_MERCHANT_CONFIG_GENERATION_ID"
	PaymentCloseAppIDEnv                      = "XIANGWAN_PAYMENT_CLOSE_APP_ID"
	PaymentCloseCertSerialEnv                 = "XIANGWAN_PAYMENT_CLOSE_WECHAT_PAY_CERT_SERIAL"
	PaymentClosePrivateKeyFileEnv             = "XIANGWAN_PAYMENT_CLOSE_WECHAT_PAY_PRIVATE_KEY_FILE"
	PaymentClosePublicKeyIDEnv                = "XIANGWAN_PAYMENT_CLOSE_WECHAT_PAY_PUBLIC_KEY_ID"
	PaymentClosePublicKeyFileEnv              = "XIANGWAN_PAYMENT_CLOSE_WECHAT_PAY_PUBLIC_KEY_FILE"
)

type PaymentCloseWorkerConfig struct {
	DatabaseDSN                string
	TenantID                   uuid.UUID
	GenerationID               uuid.UUID
	PaymentAppID               string
	PaymentMerchantID          string
	MerchantConfigGenerationID uuid.UUID
	WeChatPayCertSerial        string
	WeChatPayPrivateKeyFile    string
	WeChatPayPublicKeyID       string
	WeChatPayPublicKeyFile     string
}

func LoadPaymentCloseWorkerConfig(
	lookup EnvironmentLookup,
) (PaymentCloseWorkerConfig, error) {
	if lookup == nil {
		return PaymentCloseWorkerConfig{}, ErrInvalidRuntimeConfig
	}
	databaseDSN, exists := lookup(PaymentCloseDatabaseDSNEnv)
	trimmedDatabaseDSN := strings.TrimSpace(databaseDSN)
	if !exists || trimmedDatabaseDSN == "" ||
		strings.ContainsAny(trimmedDatabaseDSN, "\r\n\x00") {
		return PaymentCloseWorkerConfig{}, configError(PaymentCloseDatabaseDSNEnv)
	}
	tenantID, err := loadCanonicalUUID(lookup, TenantIDEnv)
	if err != nil {
		return PaymentCloseWorkerConfig{}, err
	}
	generationID, err := loadCanonicalUUID(lookup, GenerationIDEnv)
	if err != nil {
		return PaymentCloseWorkerConfig{}, err
	}
	appID, err := loadPaymentCloseWorkerValue(lookup, PaymentCloseAppIDEnv)
	if err != nil || !validWechatAppID(appID) {
		return PaymentCloseWorkerConfig{}, configError(PaymentCloseAppIDEnv)
	}
	merchantID, err := loadPaymentCloseWorkerValue(lookup, PaymentCloseMerchantIDEnv)
	if err != nil || !runtimeMerchantIDPattern.MatchString(merchantID) {
		return PaymentCloseWorkerConfig{}, configError(PaymentCloseMerchantIDEnv)
	}
	merchantConfigGenerationID, err := loadCanonicalUUID(
		lookup,
		PaymentCloseMerchantConfigGenerationIDEnv,
	)
	if err != nil {
		return PaymentCloseWorkerConfig{}, err
	}
	certificateSerial, err := loadPaymentCloseWorkerValue(
		lookup,
		PaymentCloseCertSerialEnv,
	)
	if err != nil || !runtimeCertSerialPattern.MatchString(certificateSerial) {
		return PaymentCloseWorkerConfig{}, configError(PaymentCloseCertSerialEnv)
	}
	privateKeyFile, err := loadPaymentCloseWorkerValue(
		lookup,
		PaymentClosePrivateKeyFileEnv,
	)
	if err != nil || !validPaymentKeyFile(privateKeyFile) {
		return PaymentCloseWorkerConfig{}, configError(PaymentClosePrivateKeyFileEnv)
	}
	publicKeyID, err := loadPaymentCloseWorkerValue(
		lookup,
		PaymentClosePublicKeyIDEnv,
	)
	if err != nil || !runtimePublicKeyIDPattern.MatchString(publicKeyID) {
		return PaymentCloseWorkerConfig{}, configError(PaymentClosePublicKeyIDEnv)
	}
	publicKeyFile, err := loadPaymentCloseWorkerValue(
		lookup,
		PaymentClosePublicKeyFileEnv,
	)
	if err != nil || !validPaymentKeyFile(publicKeyFile) {
		return PaymentCloseWorkerConfig{}, configError(PaymentClosePublicKeyFileEnv)
	}
	return PaymentCloseWorkerConfig{
		DatabaseDSN:                trimmedDatabaseDSN,
		TenantID:                   tenantID,
		GenerationID:               generationID,
		PaymentAppID:               appID,
		PaymentMerchantID:          merchantID,
		MerchantConfigGenerationID: merchantConfigGenerationID,
		WeChatPayCertSerial:        certificateSerial,
		WeChatPayPrivateKeyFile:    privateKeyFile,
		WeChatPayPublicKeyID:       publicKeyID,
		WeChatPayPublicKeyFile:     publicKeyFile,
	}, nil
}

func loadPaymentCloseWorkerValue(
	lookup EnvironmentLookup,
	name string,
) (string, error) {
	raw, exists := lookup(name)
	value := strings.TrimSpace(raw)
	if !exists || raw != value || value == "" ||
		strings.ContainsAny(value, "\r\n\x00") {
		return "", configError(name)
	}
	return value, nil
}

func validPaymentCloseWorkerConfig(config PaymentCloseWorkerConfig) bool {
	return strings.TrimSpace(config.DatabaseDSN) != "" &&
		config.DatabaseDSN == strings.TrimSpace(config.DatabaseDSN) &&
		!strings.ContainsAny(config.DatabaseDSN, "\r\n\x00") &&
		config.TenantID != uuid.Nil && config.GenerationID != uuid.Nil &&
		config.MerchantConfigGenerationID != uuid.Nil &&
		validWechatAppID(config.PaymentAppID) &&
		runtimeMerchantIDPattern.MatchString(config.PaymentMerchantID) &&
		runtimeCertSerialPattern.MatchString(config.WeChatPayCertSerial) &&
		validPaymentKeyFile(config.WeChatPayPrivateKeyFile) &&
		runtimePublicKeyIDPattern.MatchString(config.WeChatPayPublicKeyID) &&
		validPaymentKeyFile(config.WeChatPayPublicKeyFile)
}
