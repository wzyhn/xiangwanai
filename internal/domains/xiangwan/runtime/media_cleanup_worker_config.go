package xiangwanruntime

import (
	"strings"

	"github.com/google/uuid"
)

const MediaCleanupDatabaseDSNEnv = "XIANGWAN_MEDIA_CLEANUP_DATABASE_DSN"

// MediaCleanupWorkerConfig deliberately needs neither HTTP/admin credentials
// nor WeChat provider secrets. The PostgreSQL role needs only generation
// inspection and the exact review-File cleanup tables.
type MediaCleanupWorkerConfig struct {
	DatabaseDSN  string
	TenantID     uuid.UUID
	GenerationID uuid.UUID
	StorageDir   string
}

func LoadMediaCleanupWorkerConfig(
	lookup EnvironmentLookup,
) (MediaCleanupWorkerConfig, error) {
	if lookup == nil {
		return MediaCleanupWorkerConfig{}, ErrInvalidRuntimeConfig
	}
	databaseDSN, err := loadMediaCleanupValue(lookup, MediaCleanupDatabaseDSNEnv)
	if err != nil {
		return MediaCleanupWorkerConfig{}, err
	}
	tenantID, err := loadCanonicalUUID(lookup, TenantIDEnv)
	if err != nil {
		return MediaCleanupWorkerConfig{}, err
	}
	generationID, err := loadCanonicalUUID(lookup, GenerationIDEnv)
	if err != nil {
		return MediaCleanupWorkerConfig{}, err
	}
	storageDir, err := loadMediaCleanupValue(lookup, StorageLocalDirEnv)
	if err != nil {
		return MediaCleanupWorkerConfig{}, err
	}
	return MediaCleanupWorkerConfig{
		DatabaseDSN: databaseDSN, TenantID: tenantID,
		GenerationID: generationID, StorageDir: storageDir,
	}, nil
}

func loadMediaCleanupValue(lookup EnvironmentLookup, name string) (string, error) {
	raw, exists := lookup(name)
	value := strings.TrimSpace(raw)
	if !exists || value == "" || raw != value ||
		strings.ContainsAny(value, "\r\n\x00") {
		return "", configError(name)
	}
	return value, nil
}

func validMediaCleanupWorkerConfig(config MediaCleanupWorkerConfig) bool {
	return config.DatabaseDSN != "" &&
		config.DatabaseDSN == strings.TrimSpace(config.DatabaseDSN) &&
		!strings.ContainsAny(config.DatabaseDSN, "\r\n\x00") &&
		config.TenantID != uuid.Nil && config.GenerationID != uuid.Nil &&
		config.StorageDir != "" &&
		config.StorageDir == strings.TrimSpace(config.StorageDir) &&
		!strings.ContainsAny(config.StorageDir, "\r\n\x00")
}
