package xiangwanruntime

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/checkin"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/resource"
	"github.com/google/uuid"
)

const (
	DatabaseDSNEnv                       = "DATABASE_DSN"
	APIDatabaseDSNEnv                    = "XIANGWAN_API_DATABASE_DSN"
	PaymentCloseDatabaseDSNEnv           = "XIANGWAN_PAYMENT_CLOSE_DATABASE_DSN"
	ProfileModerationDatabaseDSNEnv      = "XIANGWAN_PROFILE_MODERATION_DATABASE_DSN"
	TenantIDEnv                          = "XIANGWAN_TENANT_ID"
	GenerationIDEnv                      = "XIANGWAN_GENERATION_ID"
	JWTSigningKeyEnv                     = "JWT_SIGNING_KEY"
	JWTPreviousSecretsEnv                = "JWT_PREVIOUS_SECRETS"
	AppIDEnv                             = "XIANGWAN_APP_ID"
	AppSecretEnv                         = "XIANGWAN_APP_SECRET"
	ListenAddressEnv                     = "XIANGWAN_LISTEN_ADDRESS"
	TrustedProxiesEnv                    = "SERVER_TRUSTED_PROXIES"
	ExternalDomainsEnv                   = "XIANGWAN_EXTERNAL_DOMAINS"
	ExternalDomainsPolicyVersionEnv      = "XIANGWAN_EXTERNAL_DOMAINS_POLICY_VERSION"
	PrivacyPolicyVersionEnv              = "XIANGWAN_PRIVACY_POLICY_VERSION"
	UserAgreementVersionEnv              = "XIANGWAN_USER_AGREEMENT_VERSION"
	CustomerServicePolicyVersionEnv      = "XIANGWAN_CUSTOMER_SERVICE_POLICY_VERSION"
	StorageLocalDirEnv                   = "STORAGE_LOCAL_DIR"
	ManualContactEnabledEnv              = "XIANGWAN_ALLOW_MANUAL_CONTACT"
	ManualContactPolicyVersionEnv        = "XIANGWAN_MANUAL_CONTACT_POLICY_VERSION"
	ManualContactPolicyTextEnv           = "XIANGWAN_MANUAL_CONTACT_POLICY_TEXT"
	PaidRegistrationEnabledEnv           = "XIANGWAN_PAID_REGISTRATION_ENABLED"
	PaymentMerchantIDEnv                 = "XIANGWAN_PAYMENT_MERCHANT_ID"
	PaymentMerchantConfigGenerationIDEnv = "XIANGWAN_PAYMENT_MERCHANT_CONFIG_GENERATION_ID"
	CancelPolicyVersionEnv               = "XIANGWAN_CANCEL_POLICY_VERSION"
	CancelPolicyCutoffHoursEnv           = "XIANGWAN_CANCEL_FULL_REFUND_CUTOFF_HOURS"
	CouponRefundPolicyVersionEnv         = "XIANGWAN_COUPON_REFUND_POLICY_VERSION"
	CouponRefundDispositionEnv           = "XIANGWAN_COUPON_REFUND_DISPOSITION"
	CheckinCredentialHMACKeyEnv          = "XIANGWAN_CHECKIN_CREDENTIAL_HMAC_KEY"
	CheckinCredentialTTLSecondsEnv       = "XIANGWAN_CHECKIN_CREDENTIAL_TTL_SECONDS"
	AvatarModerationDisabledEnv          = "XIANGWAN_AVATAR_MODERATION_DISABLED"
	WeChatPrepayEnabledEnv               = "XIANGWAN_WECHAT_PREPAY_ENABLED"
	WeChatPayCertSerialEnv               = "XIANGWAN_WECHAT_PAY_CERT_SERIAL"
	WeChatPayPrivateKeyFileEnv           = "XIANGWAN_WECHAT_PAY_PRIVATE_KEY_FILE"
	WeChatPayPublicKeyIDEnv              = "XIANGWAN_WECHAT_PAY_PUBLIC_KEY_ID"
	WeChatPayPublicKeyFileEnv            = "XIANGWAN_WECHAT_PAY_PUBLIC_KEY_FILE"
	WeChatPayAPIV3KeyEnv                 = "XIANGWAN_WECHAT_PAY_API_V3_KEY"
	WeChatPayNotifyURLEnv                = "XIANGWAN_WECHAT_PAY_NOTIFY_URL"
	WeChatPayDescriptionEnv              = "XIANGWAN_WECHAT_PAY_DESCRIPTION"
	AdminOriginEnv                       = "XIANGWAN_ADMIN_ORIGIN"
	AdminOIDCIssuerEnv                   = "XIANGWAN_ADMIN_OIDC_ISSUER"
	AdminOIDCClientIDEnv                 = "XIANGWAN_ADMIN_OIDC_CLIENT_ID"
	AdminOIDCClientSecretEnv             = "XIANGWAN_ADMIN_OIDC_CLIENT_SECRET"
	AdminOIDCRedirectURLEnv              = "XIANGWAN_ADMIN_OIDC_REDIRECT_URL"
	AdminOIDCRequiredACREnv              = "XIANGWAN_ADMIN_OIDC_REQUIRED_ACR"
	AdminOIDCCAFileEnv                   = "XIANGWAN_ADMIN_OIDC_CA_FILE"
	AdminSessionKeyEnv                   = "XIANGWAN_ADMIN_SESSION_KEY"
	defaultListenAddress                 = ":8080"
	minimumSigningKeyLen                 = 32
	maximumPreviousKeys                  = 2
	maximumPaymentPathLen                = 1024
	maximumCancellationCutoffHours       = 24 * 366
	minimumCheckinCredentialKeyLen       = 32
	maximumCheckinCredentialKeyLen       = 1024
	maximumTrustedProxies                = 32
	defaultCheckinCredentialTTL          = 10 * time.Minute
	CouponGrantPolicyPublicKeyEnv        = "XIANGWAN_COUPON_GRANT_POLICY_PUBLIC_KEY"
)

var ErrInvalidRuntimeConfig = errors.New("invalid xiangwan Runtime config")

var (
	runtimeMerchantIDPattern    = regexp.MustCompile(`^[0-9]{6,32}$`)
	runtimeCertSerialPattern    = regexp.MustCompile(`^[0-9A-F]{16,64}$`)
	runtimePublicKeyIDPattern   = regexp.MustCompile(`^PUB_KEY_ID_[0-9A-Za-z_]{1,64}$`)
	runtimePolicyVersionPattern = regexp.MustCompile(
		`^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$`,
	)
)

type EnvironmentLookup func(string) (string, bool)

type Config struct {
	DatabaseDSN                       string
	TenantID                          uuid.UUID
	GenerationID                      uuid.UUID
	JWTSigningKey                     string
	JWTPreviousSecrets                []string
	AppID                             string
	AppSecret                         string
	ListenAddress                     string
	TrustedProxies                    []string
	StorageLocalDir                   string
	ExternalDomains                   resource.ExternalDomainPolicy
	ExternalDomainsPolicyVersion      string
	PrivacyPolicyVersion              string
	UserAgreementVersion              string
	CustomerServicePolicyVersion      string
	ManualContactEnabled              bool
	ManualContactPolicyVersion        string
	ManualContactPolicyText           string
	PaidRegistrationEnabled           bool
	PaymentMerchantID                 string
	PaymentMerchantConfigGenerationID uuid.UUID
	CancelPolicyVersion               string
	CancelPolicyCutoffHours           int
	CouponRefundPolicyVersion         string
	CouponRefundDisposition           coupon.RefundDisposition
	CheckinCredentialHMACKey          string
	CheckinCredentialTTL              time.Duration
	AvatarModerationDisabled          bool
	WeChatPrepayEnabled               bool
	WeChatPayCertSerial               string
	WeChatPayPrivateKeyFile           string
	WeChatPayPublicKeyID              string
	WeChatPayPublicKeyFile            string
	WeChatPayAPIV3Key                 string
	WeChatPayNotifyURL                string
	WeChatPayDescription              string
	Admin                             AdminConfig
	CouponGrantPolicyPublicKey        ed25519.PublicKey
}

type AdminConfig struct {
	Enabled      bool
	Origin       string
	OIDCIssuer   string
	OIDCClientID string
	OIDCSecret   string
	OIDCRedirect string
	RequiredACR  string
	OIDCCAFile   string
	SessionKey   []byte
}

func LoadConfig(lookup EnvironmentLookup) (Config, error) {
	if lookup == nil {
		return Config{}, ErrInvalidRuntimeConfig
	}
	databaseDSN, exists := lookup(APIDatabaseDSNEnv)
	if !exists || strings.TrimSpace(databaseDSN) == "" {
		return Config{}, configError(APIDatabaseDSNEnv)
	}
	tenantID, err := loadCanonicalUUID(lookup, TenantIDEnv)
	if err != nil {
		return Config{}, err
	}
	generationID, err := loadCanonicalUUID(lookup, GenerationIDEnv)
	if err != nil {
		return Config{}, err
	}
	signingKey, previousSecrets, appID, appSecret, err := loadConsumerAuth(lookup)
	if err != nil {
		return Config{}, err
	}
	listenAddress := defaultListenAddress
	if raw, configured := lookup(ListenAddressEnv); configured {
		listenAddress = strings.TrimSpace(raw)
	}
	if !validListenAddress(listenAddress) {
		return Config{}, configError(ListenAddressEnv)
	}
	trustedProxies, err := loadTrustedProxies(lookup)
	if err != nil {
		return Config{}, err
	}
	storageLocalDir, exists := lookup(StorageLocalDirEnv)
	storageLocalDir = strings.TrimSpace(storageLocalDir)
	if !exists || storageLocalDir == "" ||
		strings.ContainsAny(storageLocalDir, "\r\n\x00") {
		return Config{}, configError(StorageLocalDirEnv)
	}
	externalDomains, err := loadExternalDomains(lookup)
	if err != nil {
		return Config{}, err
	}
	externalDomainsPolicyVersion, err := loadExternalDomainsPolicyVersion(
		lookup,
		externalDomains.Configured(),
	)
	if err != nil {
		return Config{}, err
	}
	privacyPolicyVersion, err := loadOptionalPublicPolicyVersion(
		lookup,
		PrivacyPolicyVersionEnv,
	)
	if err != nil {
		return Config{}, err
	}
	userAgreementVersion, err := loadOptionalPublicPolicyVersion(
		lookup,
		UserAgreementVersionEnv,
	)
	if err != nil {
		return Config{}, err
	}
	customerServicePolicyVersion, err := loadOptionalPublicPolicyVersion(
		lookup,
		CustomerServicePolicyVersionEnv,
	)
	if err != nil {
		return Config{}, err
	}
	manualContactEnabled, manualContactPolicyVersion, manualContactPolicyText, err :=
		loadManualContactPolicy(lookup)
	if err != nil {
		return Config{}, err
	}
	paidRegistrationEnabled, paymentMerchantID, err :=
		loadPaidRegistrationPolicy(lookup)
	if err != nil {
		return Config{}, err
	}
	merchantConfigGenerationID, err := loadPaymentMerchantConfigGenerationID(
		lookup,
		paidRegistrationEnabled,
	)
	if err != nil {
		return Config{}, err
	}
	cancelPolicyVersion, cancelPolicyCutoffHours, err :=
		loadCancellationPolicy(lookup)
	if err != nil {
		return Config{}, err
	}
	couponRefundPolicyVersion, couponRefundDisposition, err :=
		loadCouponRefundPolicy(lookup)
	if err != nil {
		return Config{}, err
	}
	checkinCredentialHMACKey, checkinCredentialTTL, err :=
		loadCheckinCredentialConfig(lookup)
	if err != nil {
		return Config{}, err
	}
	if !distinctCheckinCredentialKey(
		checkinCredentialHMACKey,
		signingKey,
		previousSecrets,
	) {
		return Config{}, configError(CheckinCredentialHMACKeyEnv)
	}
	prepayConfig, err := loadWeChatPrepayPolicy(
		lookup,
		paidRegistrationEnabled,
		paymentMerchantID,
	)
	if err != nil {
		return Config{}, err
	}
	if paidRegistrationEnabled && !prepayConfig.enabled {
		// A positive-price registration creates a durable Order and capacity
		// hold. Do not allow that write path while the provider/query/notify
		// convergence path is disabled; operators must stop new paid intake
		// before draining existing orders.
		return Config{}, configError(WeChatPrepayEnabledEnv)
	}
	avatarModerationDisabled, err := loadAvatarModerationPolicy(lookup)
	if err != nil {
		return Config{}, err
	}
	adminConfig, err := loadAdminConfig(lookup)
	if err != nil {
		return Config{}, err
	}
	var couponGrantPolicyPublicKey ed25519.PublicKey
	if raw, configured := lookup(CouponGrantPolicyPublicKeyEnv); configured && strings.TrimSpace(raw) != "" {
		decoded, decodeErr := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(raw))
		if decodeErr != nil || len(decoded) != ed25519.PublicKeySize || !adminConfig.Enabled {
			return Config{}, configError(CouponGrantPolicyPublicKeyEnv)
		}
		couponGrantPolicyPublicKey = ed25519.PublicKey(decoded)
	}
	return Config{
		DatabaseDSN:                       strings.TrimSpace(databaseDSN),
		TenantID:                          tenantID,
		GenerationID:                      generationID,
		JWTSigningKey:                     signingKey,
		JWTPreviousSecrets:                previousSecrets,
		AppID:                             appID,
		AppSecret:                         appSecret,
		ListenAddress:                     listenAddress,
		TrustedProxies:                    trustedProxies,
		StorageLocalDir:                   storageLocalDir,
		ExternalDomains:                   externalDomains,
		ExternalDomainsPolicyVersion:      externalDomainsPolicyVersion,
		PrivacyPolicyVersion:              privacyPolicyVersion,
		UserAgreementVersion:              userAgreementVersion,
		CustomerServicePolicyVersion:      customerServicePolicyVersion,
		ManualContactEnabled:              manualContactEnabled,
		ManualContactPolicyVersion:        manualContactPolicyVersion,
		ManualContactPolicyText:           manualContactPolicyText,
		PaidRegistrationEnabled:           paidRegistrationEnabled,
		PaymentMerchantID:                 paymentMerchantID,
		PaymentMerchantConfigGenerationID: merchantConfigGenerationID,
		CancelPolicyVersion:               cancelPolicyVersion,
		CancelPolicyCutoffHours:           cancelPolicyCutoffHours,
		CouponRefundPolicyVersion:         couponRefundPolicyVersion,
		CouponRefundDisposition:           couponRefundDisposition,
		CheckinCredentialHMACKey:          checkinCredentialHMACKey,
		CheckinCredentialTTL:              checkinCredentialTTL,
		AvatarModerationDisabled:          avatarModerationDisabled,
		WeChatPrepayEnabled:               prepayConfig.enabled,
		WeChatPayCertSerial:               prepayConfig.certificateSerial,
		WeChatPayPrivateKeyFile:           prepayConfig.privateKeyFile,
		WeChatPayPublicKeyID:              prepayConfig.publicKeyID,
		WeChatPayPublicKeyFile:            prepayConfig.publicKeyFile,
		WeChatPayAPIV3Key:                 prepayConfig.apiV3Key,
		WeChatPayNotifyURL:                prepayConfig.notifyURL,
		WeChatPayDescription:              prepayConfig.description,
		Admin:                             adminConfig,
		CouponGrantPolicyPublicKey:        couponGrantPolicyPublicKey,
	}, nil
}

// sameCanonicalAdminOrigin compares two configured https origins the way the
// API's normalizeAdminOrigin does: browsers serialize an origin without its
// scheme default port, so an explicit ":443" on either side must compare
// equal to the bare host or a valid configuration is rejected at startup
// (codex review 2026-09-19).
func sameCanonicalAdminOrigin(left *url.URL, right *url.URL) bool {
	return canonicalAdminOrigin(left) == canonicalAdminOrigin(right)
}

func canonicalAdminOrigin(origin *url.URL) string {
	host := strings.ToLower(origin.Host)
	if origin.Scheme == "https" {
		host = strings.TrimSuffix(host, ":443")
	}
	return origin.Scheme + "://" + host
}

func loadAdminConfig(lookup EnvironmentLookup) (AdminConfig, error) {
	names := []string{
		AdminOriginEnv,
		AdminOIDCIssuerEnv,
		AdminOIDCClientIDEnv,
		AdminOIDCClientSecretEnv,
		AdminOIDCRedirectURLEnv,
		AdminOIDCRequiredACREnv,
		AdminOIDCCAFileEnv,
		AdminSessionKeyEnv,
	}
	configured := false
	values := make(map[string]string, len(names))
	for _, name := range names {
		value, exists := lookup(name)
		if exists {
			configured = true
		}
		values[name] = value
	}
	if !configured {
		return AdminConfig{}, nil
	}
	for _, name := range []string{
		AdminOriginEnv,
		AdminOIDCIssuerEnv,
		AdminOIDCClientIDEnv,
		AdminOIDCClientSecretEnv,
		AdminOIDCRedirectURLEnv,
		AdminSessionKeyEnv,
	} {
		if values[name] == "" || values[name] != strings.TrimSpace(values[name]) ||
			strings.ContainsAny(values[name], "\r\n\x00") {
			return AdminConfig{}, configError(name)
		}
	}
	origin, err := url.Parse(values[AdminOriginEnv])
	if err != nil || origin.Scheme != "https" || origin.Host == "" ||
		origin.User != nil || origin.Path != "" || origin.RawQuery != "" ||
		origin.Fragment != "" {
		return AdminConfig{}, configError(AdminOriginEnv)
	}
	issuer, err := url.Parse(values[AdminOIDCIssuerEnv])
	if err != nil || issuer.Scheme != "https" || issuer.Host == "" ||
		issuer.User != nil || issuer.RawQuery != "" || issuer.Fragment != "" {
		return AdminConfig{}, configError(AdminOIDCIssuerEnv)
	}
	redirect, err := url.Parse(values[AdminOIDCRedirectURLEnv])
	if err != nil || redirect.Scheme != "https" || redirect.Host == "" ||
		redirect.User != nil || redirect.RawQuery != "" || redirect.Fragment != "" ||
		!sameCanonicalAdminOrigin(origin, redirect) ||
		redirect.Path != "/api/v1/xiangwan/admin/auth/callback" {
		return AdminConfig{}, configError(AdminOIDCRedirectURLEnv)
	}
	if len(values[AdminOIDCClientIDEnv]) > 255 ||
		len(values[AdminOIDCClientSecretEnv]) > 4096 {
		return AdminConfig{}, configError(AdminOIDCClientIDEnv)
	}
	requiredACR := values[AdminOIDCRequiredACREnv]
	if requiredACR != strings.TrimSpace(requiredACR) ||
		strings.ContainsAny(requiredACR, "\r\n\x00") || len(requiredACR) > 255 {
		return AdminConfig{}, configError(AdminOIDCRequiredACREnv)
	}
	oidcCAFile := values[AdminOIDCCAFileEnv]
	if oidcCAFile != "" && (oidcCAFile != strings.TrimSpace(oidcCAFile) ||
		strings.ContainsAny(oidcCAFile, "\r\n\x00")) {
		return AdminConfig{}, configError(AdminOIDCCAFileEnv)
	}
	sessionKey, err := base64.RawURLEncoding.DecodeString(values[AdminSessionKeyEnv])
	if err != nil || len(sessionKey) != 32 {
		return AdminConfig{}, configError(AdminSessionKeyEnv)
	}
	return AdminConfig{
		Enabled:      true,
		Origin:       origin.Scheme + "://" + strings.ToLower(origin.Host),
		OIDCIssuer:   values[AdminOIDCIssuerEnv],
		OIDCClientID: values[AdminOIDCClientIDEnv],
		OIDCSecret:   values[AdminOIDCClientSecretEnv],
		OIDCRedirect: values[AdminOIDCRedirectURLEnv],
		RequiredACR:  requiredACR,
		OIDCCAFile:   oidcCAFile,
		SessionKey:   sessionKey,
	}, nil
}

func loadTrustedProxies(lookup EnvironmentLookup) ([]string, error) {
	raw, configured := lookup(TrustedProxiesEnv)
	if !configured || raw == "" {
		return nil, nil
	}
	if raw != strings.TrimSpace(raw) || strings.ContainsAny(raw, "\r\n\x00") {
		return nil, configError(TrustedProxiesEnv)
	}
	parts := strings.Split(raw, ",")
	if len(parts) > maximumTrustedProxies {
		return nil, configError(TrustedProxiesEnv)
	}
	proxies := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		if part == "" || part != strings.TrimSpace(part) {
			return nil, configError(TrustedProxiesEnv)
		}
		if net.ParseIP(part) == nil {
			_, network, err := net.ParseCIDR(part)
			if err != nil {
				return nil, configError(TrustedProxiesEnv)
			}
			ones, _ := network.Mask.Size()
			if ones == 0 {
				return nil, configError(TrustedProxiesEnv)
			}
		}
		if _, duplicate := seen[part]; duplicate {
			return nil, configError(TrustedProxiesEnv)
		}
		seen[part] = struct{}{}
		proxies = append(proxies, part)
	}
	return proxies, nil
}

func loadExternalDomainsPolicyVersion(
	lookup EnvironmentLookup,
	domainsConfigured bool,
) (string, error) {
	version, err := loadOptionalPublicPolicyVersion(
		lookup,
		ExternalDomainsPolicyVersionEnv,
	)
	if err != nil {
		return "", err
	}
	if domainsConfigured != (version != "") {
		return "", configError(ExternalDomainsPolicyVersionEnv)
	}
	return version, nil
}

func loadOptionalPublicPolicyVersion(
	lookup EnvironmentLookup,
	name string,
) (string, error) {
	raw, configured := lookup(name)
	if !configured || raw == "" {
		return "", nil
	}
	version := strings.TrimSpace(raw)
	if raw != version || !runtimePolicyVersionPattern.MatchString(version) {
		return "", configError(name)
	}
	return version, nil
}

func loadCheckinCredentialConfig(
	lookup EnvironmentLookup,
) (string, time.Duration, error) {
	rawKey, configured := lookup(CheckinCredentialHMACKeyEnv)
	key := strings.TrimSpace(rawKey)
	if !configured || rawKey != key ||
		len([]byte(key)) < minimumCheckinCredentialKeyLen ||
		len([]byte(key)) > maximumCheckinCredentialKeyLen ||
		strings.ContainsAny(key, "\r\n\x00") {
		return "", 0, configError(CheckinCredentialHMACKeyEnv)
	}
	ttl := defaultCheckinCredentialTTL
	if rawTTL, exists := lookup(CheckinCredentialTTLSecondsEnv); exists {
		if rawTTL == "" || rawTTL != strings.TrimSpace(rawTTL) {
			return "", 0, configError(CheckinCredentialTTLSecondsEnv)
		}
		seconds, err := strconv.ParseInt(rawTTL, 10, 64)
		maximumSeconds := int64(checkin.MaxCredentialTTL / time.Second)
		if err != nil || seconds < 1 || seconds > maximumSeconds ||
			strconv.FormatInt(seconds, 10) != rawTTL {
			return "", 0, configError(CheckinCredentialTTLSecondsEnv)
		}
		ttl = time.Duration(seconds) * time.Second
	}
	return key, ttl, nil
}

func distinctCheckinCredentialKey(
	checkinKey string,
	activeJWTKey string,
	previousJWTKeys []string,
) bool {
	if checkinKey == activeJWTKey {
		return false
	}
	for _, previousKey := range previousJWTKeys {
		if checkinKey == previousKey {
			return false
		}
	}
	return true
}

func loadCancellationPolicy(
	lookup EnvironmentLookup,
) (string, int, error) {
	rawVersion, versionConfigured := lookup(CancelPolicyVersionEnv)
	rawCutoff, cutoffConfigured := lookup(CancelPolicyCutoffHoursEnv)
	if !versionConfigured && !cutoffConfigured {
		return "", 0, nil
	}
	if versionConfigured && cutoffConfigured &&
		rawVersion == "" && rawCutoff == "" {
		return "", 0, nil
	}
	version := strings.TrimSpace(rawVersion)
	if !versionConfigured || !cutoffConfigured || rawVersion != version ||
		!runtimePolicyVersionPattern.MatchString(version) ||
		rawCutoff == "" || rawCutoff != strings.TrimSpace(rawCutoff) {
		return "", 0, configError(CancelPolicyVersionEnv)
	}
	cutoffHours, err := strconv.Atoi(rawCutoff)
	if err != nil || cutoffHours < 0 ||
		cutoffHours > maximumCancellationCutoffHours ||
		strconv.Itoa(cutoffHours) != rawCutoff {
		return "", 0, configError(CancelPolicyCutoffHoursEnv)
	}
	return version, cutoffHours, nil
}

func loadCouponRefundPolicy(
	lookup EnvironmentLookup,
) (string, coupon.RefundDisposition, error) {
	rawVersion, versionConfigured := lookup(CouponRefundPolicyVersionEnv)
	rawDisposition, dispositionConfigured := lookup(CouponRefundDispositionEnv)
	if !versionConfigured && !dispositionConfigured {
		return "", "", nil
	}
	if versionConfigured && dispositionConfigured &&
		rawVersion == "" && rawDisposition == "" {
		return "", "", nil
	}
	version := strings.TrimSpace(rawVersion)
	disposition := coupon.RefundDisposition(strings.TrimSpace(rawDisposition))
	if !versionConfigured || !dispositionConfigured || rawVersion != version ||
		rawDisposition != string(disposition) ||
		!runtimePolicyVersionPattern.MatchString(version) {
		return "", "", configError(CouponRefundPolicyVersionEnv)
	}
	if disposition != coupon.RefundDispositionRestore &&
		disposition != coupon.RefundDispositionForfeit {
		return "", "", configError(CouponRefundDispositionEnv)
	}
	return version, disposition, nil
}

type weChatPrepayConfig struct {
	enabled           bool
	certificateSerial string
	privateKeyFile    string
	publicKeyID       string
	publicKeyFile     string
	apiV3Key          string
	notifyURL         string
	description       string
}

// loadAvatarModerationPolicy reads the explicit kill switch for the
// server-side WeChat avatar image review. The default (absent) is audit ON:
// an avatar upload is refused when the moderation service cannot produce a
// verdict (fail closed). Setting XIANGWAN_AVATAR_MODERATION_DISABLED=true is
// reserved for local development without WeChat credentials.
func loadAvatarModerationPolicy(lookup EnvironmentLookup) (bool, error) {
	raw, configured := lookup(AvatarModerationDisabledEnv)
	if !configured {
		return false, nil
	}
	if raw != "true" && raw != "false" {
		return false, configError(AvatarModerationDisabledEnv)
	}
	return raw == "true", nil
}

func loadWeChatPrepayPolicy(
	lookup EnvironmentLookup,
	paidRegistrationEnabled bool,
	merchantID string,
) (weChatPrepayConfig, error) {
	rawEnabled, configured := lookup(WeChatPrepayEnabledEnv)
	if !configured {
		rawEnabled = "false"
	}
	if rawEnabled != "true" && rawEnabled != "false" {
		return weChatPrepayConfig{}, configError(WeChatPrepayEnabledEnv)
	}
	names := []string{
		WeChatPayCertSerialEnv,
		WeChatPayPrivateKeyFileEnv,
		WeChatPayPublicKeyIDEnv,
		WeChatPayPublicKeyFileEnv,
		WeChatPayAPIV3KeyEnv,
		WeChatPayNotifyURLEnv,
		WeChatPayDescriptionEnv,
	}
	values := make(map[string]string, len(names))
	for _, name := range names {
		raw, exists := lookup(name)
		value := strings.TrimSpace(raw)
		if rawEnabled == "false" {
			if exists && raw != "" {
				return weChatPrepayConfig{}, configError(name)
			}
			continue
		}
		if !exists || raw != value || value == "" ||
			strings.ContainsAny(value, "\r\n\x00") {
			return weChatPrepayConfig{}, configError(name)
		}
		values[name] = value
	}
	if rawEnabled == "false" {
		return weChatPrepayConfig{}, nil
	}
	if !paidRegistrationEnabled {
		return weChatPrepayConfig{}, configError(WeChatPrepayEnabledEnv)
	}
	if !runtimeMerchantIDPattern.MatchString(merchantID) {
		return weChatPrepayConfig{}, configError(PaymentMerchantIDEnv)
	}
	if !runtimeCertSerialPattern.MatchString(values[WeChatPayCertSerialEnv]) {
		return weChatPrepayConfig{}, configError(WeChatPayCertSerialEnv)
	}
	if !validPaymentKeyFile(values[WeChatPayPrivateKeyFileEnv]) {
		return weChatPrepayConfig{}, configError(WeChatPayPrivateKeyFileEnv)
	}
	if !runtimePublicKeyIDPattern.MatchString(values[WeChatPayPublicKeyIDEnv]) {
		return weChatPrepayConfig{}, configError(WeChatPayPublicKeyIDEnv)
	}
	if !validPaymentKeyFile(values[WeChatPayPublicKeyFileEnv]) {
		return weChatPrepayConfig{}, configError(WeChatPayPublicKeyFileEnv)
	}
	if !validPaymentAPIV3Key(values[WeChatPayAPIV3KeyEnv]) {
		return weChatPrepayConfig{}, configError(WeChatPayAPIV3KeyEnv)
	}
	if !validPaymentNotifyURL(values[WeChatPayNotifyURLEnv]) {
		return weChatPrepayConfig{}, configError(WeChatPayNotifyURLEnv)
	}
	if len([]rune(values[WeChatPayDescriptionEnv])) > 127 {
		return weChatPrepayConfig{}, configError(WeChatPayDescriptionEnv)
	}
	return weChatPrepayConfig{
		enabled:           true,
		certificateSerial: values[WeChatPayCertSerialEnv],
		privateKeyFile:    values[WeChatPayPrivateKeyFileEnv],
		publicKeyID:       values[WeChatPayPublicKeyIDEnv],
		publicKeyFile:     values[WeChatPayPublicKeyFileEnv],
		apiV3Key:          values[WeChatPayAPIV3KeyEnv],
		notifyURL:         values[WeChatPayNotifyURLEnv],
		description:       values[WeChatPayDescriptionEnv],
	}, nil
}

func validPaymentAPIV3Key(value string) bool {
	return len([]byte(value)) == 32 && value == strings.TrimSpace(value) &&
		!strings.ContainsAny(value, "\r\n\x00")
}

func validPaymentKeyFile(value string) bool {
	return value != "" && value == strings.TrimSpace(value) &&
		len(value) <= maximumPaymentPathLen &&
		!strings.ContainsAny(value, "\r\n\x00")
}

func validPaymentNotifyURL(value string) bool {
	if len(value) > 2048 {
		return false
	}
	parsed, err := url.ParseRequestURI(value)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" &&
		parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == ""
}

func loadPaidRegistrationPolicy(
	lookup EnvironmentLookup,
) (bool, string, error) {
	rawEnabled, configured := lookup(PaidRegistrationEnabledEnv)
	if !configured {
		rawEnabled = "false"
	}
	if rawEnabled != "true" && rawEnabled != "false" {
		return false, "", configError(PaidRegistrationEnabledEnv)
	}
	rawMerchantID, merchantConfigured := lookup(PaymentMerchantIDEnv)
	merchantID := strings.TrimSpace(rawMerchantID)
	if rawEnabled == "false" {
		if merchantConfigured && merchantID != "" {
			return false, "", configError(PaymentMerchantIDEnv)
		}
		return false, "", nil
	}
	if !merchantConfigured || merchantID == "" || rawMerchantID != merchantID ||
		len([]rune(merchantID)) > 64 ||
		strings.ContainsAny(merchantID, "\r\n\x00") {
		return false, "", configError(PaymentMerchantIDEnv)
	}
	return true, merchantID, nil
}

func loadPaymentMerchantConfigGenerationID(
	lookup EnvironmentLookup,
	paidRegistrationEnabled bool,
) (uuid.UUID, error) {
	raw, configured := lookup(PaymentMerchantConfigGenerationIDEnv)
	value := strings.TrimSpace(raw)
	if !paidRegistrationEnabled {
		if configured && value != "" {
			return uuid.Nil, configError(PaymentMerchantConfigGenerationIDEnv)
		}
		return uuid.Nil, nil
	}
	if !configured || raw != value || value == "" {
		return uuid.Nil, configError(PaymentMerchantConfigGenerationIDEnv)
	}
	parsed, err := uuid.Parse(value)
	if err != nil || parsed == uuid.Nil || parsed.String() != value {
		return uuid.Nil, configError(PaymentMerchantConfigGenerationIDEnv)
	}
	return parsed, nil
}

func loadManualContactPolicy(
	lookup EnvironmentLookup,
) (bool, string, string, error) {
	rawEnabled, configured := lookup(ManualContactEnabledEnv)
	if !configured {
		rawEnabled = "false"
	}
	if rawEnabled != "true" && rawEnabled != "false" {
		return false, "", "", configError(ManualContactEnabledEnv)
	}
	rawPolicy, policyConfigured := lookup(ManualContactPolicyVersionEnv)
	policy := strings.TrimSpace(rawPolicy)
	rawText, textConfigured := lookup(ManualContactPolicyTextEnv)
	policyText := strings.TrimSpace(rawText)
	if rawEnabled == "false" {
		if policyConfigured && policy != "" {
			return false, "", "", configError(ManualContactPolicyVersionEnv)
		}
		if textConfigured && policyText != "" {
			return false, "", "", configError(ManualContactPolicyTextEnv)
		}
		return false, "", "", nil
	}
	if !policyConfigured || policy == "" || rawPolicy != policy ||
		len([]rune(policy)) > 100 || strings.ContainsAny(policy, "\r\n\x00") {
		return false, "", "", configError(ManualContactPolicyVersionEnv)
	}
	if !textConfigured || policyText == "" || rawText != policyText ||
		len([]rune(policyText)) > 4000 || strings.ContainsRune(policyText, '\x00') {
		return false, "", "", configError(ManualContactPolicyTextEnv)
	}
	return true, policy, policyText, nil
}

func loadConsumerAuth(
	lookup EnvironmentLookup,
) (string, []string, string, string, error) {
	rawSigningKey, exists := lookup(JWTSigningKeyEnv)
	signingKey := strings.TrimSpace(rawSigningKey)
	if !exists || !validSigningKey(signingKey) {
		return "", nil, "", "", configError(JWTSigningKeyEnv)
	}

	previousSecrets := []string{}
	if rawPrevious, configured := lookup(JWTPreviousSecretsEnv); configured &&
		strings.TrimSpace(rawPrevious) != "" {
		parts := strings.Split(rawPrevious, ",")
		if len(parts) > maximumPreviousKeys {
			return "", nil, "", "", configError(JWTPreviousSecretsEnv)
		}
		seen := map[string]struct{}{signingKey: {}}
		previousSecrets = make([]string, 0, len(parts))
		for _, part := range parts {
			candidate := strings.TrimSpace(part)
			if !validSigningKey(candidate) {
				return "", nil, "", "", configError(JWTPreviousSecretsEnv)
			}
			if _, duplicate := seen[candidate]; duplicate {
				return "", nil, "", "", configError(JWTPreviousSecretsEnv)
			}
			seen[candidate] = struct{}{}
			previousSecrets = append(previousSecrets, candidate)
		}
	}

	rawAppID, exists := lookup(AppIDEnv)
	appID := strings.TrimSpace(rawAppID)
	if !exists || !validWechatAppID(appID) {
		return "", nil, "", "", configError(AppIDEnv)
	}
	rawAppSecret, exists := lookup(AppSecretEnv)
	appSecret := strings.TrimSpace(rawAppSecret)
	if !exists || !validWechatAppSecret(appSecret) || rawAppSecret != appSecret {
		return "", nil, "", "", configError(AppSecretEnv)
	}
	return signingKey, previousSecrets, appID, appSecret, nil
}

func validSigningKey(value string) bool {
	return len(value) >= minimumSigningKeyLen &&
		value == strings.TrimSpace(value) &&
		!strings.ContainsAny(value, "\r\n\x00")
}

func validWechatAppID(value string) bool {
	if len(value) < 3 || len(value) > 64 || !strings.HasPrefix(value, "wx") {
		return false
	}
	for _, character := range value[2:] {
		if (character < 'a' || character > 'z') &&
			(character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') {
			return false
		}
	}
	return true
}

func validWechatAppSecret(value string) bool {
	return len(value) >= 16 && len(value) <= 128 &&
		value == strings.TrimSpace(value) &&
		!strings.ContainsAny(value, " \t\r\n\x00")
}

func loadCanonicalUUID(
	lookup EnvironmentLookup,
	name string,
) (uuid.UUID, error) {
	raw, exists := lookup(name)
	value := strings.TrimSpace(raw)
	parsed, err := uuid.Parse(value)
	if !exists || err != nil || parsed == uuid.Nil || parsed.String() != value {
		return uuid.Nil, configError(name)
	}
	return parsed, nil
}

func loadExternalDomains(
	lookup EnvironmentLookup,
) (resource.ExternalDomainPolicy, error) {
	raw, configured := lookup(ExternalDomainsEnv)
	if !configured || strings.TrimSpace(raw) == "" {
		policy, _ := resource.NewExternalDomainPolicy(nil)
		return policy, nil
	}
	parts := strings.Split(raw, ",")
	domains := make([]string, 0, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value == "" {
			return resource.ExternalDomainPolicy{},
				configError(ExternalDomainsEnv)
		}
		domains = append(domains, value)
	}
	policy, err := resource.NewExternalDomainPolicy(domains)
	if err != nil {
		return resource.ExternalDomainPolicy{}, configError(ExternalDomainsEnv)
	}
	return policy, nil
}

func validListenAddress(value string) bool {
	if value == "" || strings.ContainsAny(value, "\r\n\t ") {
		return false
	}
	_, port, err := net.SplitHostPort(value)
	if err != nil {
		return false
	}
	number, err := strconv.Atoi(port)
	return err == nil && number >= 1 && number <= 65535
}

func configError(name string) error {
	return errors.Join(
		ErrInvalidRuntimeConfig,
		errors.New(name+" is missing or invalid"),
	)
}
