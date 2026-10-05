package xiangwanruntime

import (
	"strings"

	"github.com/google/uuid"
)

type ProfileModerationWorkerConfig struct {
	DatabaseDSN  string
	TenantID     uuid.UUID
	GenerationID uuid.UUID
	AppID        string
	AppSecret    string
}

func LoadProfileModerationWorkerConfig(
	lookup EnvironmentLookup,
) (ProfileModerationWorkerConfig, error) {
	if lookup == nil {
		return ProfileModerationWorkerConfig{}, ErrInvalidRuntimeConfig
	}
	databaseDSN, exists := lookup(ProfileModerationDatabaseDSNEnv)
	databaseDSN = strings.TrimSpace(databaseDSN)
	if !exists || databaseDSN == "" ||
		strings.ContainsAny(databaseDSN, "\r\n\x00") {
		return ProfileModerationWorkerConfig{}, configError(ProfileModerationDatabaseDSNEnv)
	}
	tenantID, err := loadCanonicalUUID(lookup, TenantIDEnv)
	if err != nil {
		return ProfileModerationWorkerConfig{}, err
	}
	generationID, err := loadCanonicalUUID(lookup, GenerationIDEnv)
	if err != nil {
		return ProfileModerationWorkerConfig{}, err
	}
	rawAppID, exists := lookup(AppIDEnv)
	appID := strings.TrimSpace(rawAppID)
	if !exists || rawAppID != appID || !validWechatAppID(appID) {
		return ProfileModerationWorkerConfig{}, configError(AppIDEnv)
	}
	rawAppSecret, exists := lookup(AppSecretEnv)
	appSecret := strings.TrimSpace(rawAppSecret)
	if !exists || rawAppSecret != appSecret || !validWechatAppSecret(appSecret) {
		return ProfileModerationWorkerConfig{}, configError(AppSecretEnv)
	}
	return ProfileModerationWorkerConfig{
		DatabaseDSN:  databaseDSN,
		TenantID:     tenantID,
		GenerationID: generationID,
		AppID:        appID,
		AppSecret:    appSecret,
	}, nil
}

func validProfileModerationWorkerConfig(
	config ProfileModerationWorkerConfig,
) bool {
	return config.DatabaseDSN != "" &&
		config.DatabaseDSN == strings.TrimSpace(config.DatabaseDSN) &&
		!strings.ContainsAny(config.DatabaseDSN, "\r\n\x00") &&
		config.TenantID != uuid.Nil && config.GenerationID != uuid.Nil &&
		validWechatAppID(config.AppID) && validWechatAppSecret(config.AppSecret)
}
