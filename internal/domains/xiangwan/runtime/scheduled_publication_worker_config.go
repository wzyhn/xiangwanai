package xiangwanruntime

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

const ScheduledPublicationDatabaseDSNEnv = "XIANGWAN_SCHEDULED_PUBLICATION_DATABASE_DSN"

type ScheduledPublicationWorkerConfig struct {
	DatabaseDSN  string
	TenantID     uuid.UUID
	GenerationID uuid.UUID
	PollInterval time.Duration
}

func LoadScheduledPublicationWorkerConfig(lookup EnvironmentLookup) (ScheduledPublicationWorkerConfig, error) {
	if lookup == nil {
		return ScheduledPublicationWorkerConfig{}, ErrInvalidRuntimeConfig
	}
	dsn, ok := lookup(ScheduledPublicationDatabaseDSNEnv)
	dsn = strings.TrimSpace(dsn)
	if !ok || dsn == "" || strings.ContainsAny(dsn, "\r\n\x00") {
		return ScheduledPublicationWorkerConfig{}, configError(ScheduledPublicationDatabaseDSNEnv)
	}
	tenantID, err := loadCanonicalUUID(lookup, TenantIDEnv)
	if err != nil {
		return ScheduledPublicationWorkerConfig{}, err
	}
	generationID, err := loadCanonicalUUID(lookup, GenerationIDEnv)
	if err != nil {
		return ScheduledPublicationWorkerConfig{}, err
	}
	return ScheduledPublicationWorkerConfig{
		DatabaseDSN: dsn, TenantID: tenantID, GenerationID: generationID,
		PollInterval: time.Second,
	}, nil
}
