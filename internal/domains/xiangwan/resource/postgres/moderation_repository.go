package resourcepostgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/resource"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrModerationNotFound = errors.New(
		"xiangwan moderation observation not found",
	)
	ErrModerationExists = errors.New(
		"xiangwan moderation observation already exists",
	)
	ErrModerationFactsConflict = errors.New(
		"xiangwan moderation observation facts conflict",
	)
)

type RecordModerationResult struct {
	Observation resource.ModerationObservation
	Duplicate   bool
}

func (repository *Repository) RecordModeration(
	ctx context.Context,
	value resource.ModerationObservation,
) (RecordModerationResult, error) {
	if repository == nil || repository.db == nil ||
		resource.ValidateModerationObservation(value) != nil {
		return RecordModerationResult{}, resource.ErrInvalidModerationObservation
	}
	existing, err := repository.GetModerationByProviderReference(
		ctx,
		value.TenantID,
		value.Provider,
		value.ProviderReference,
	)
	switch {
	case err == nil:
		return replayModerationResult(existing, value)
	case errors.Is(err, ErrModerationNotFound):
	case err != nil:
		return RecordModerationResult{}, err
	}
	result, err := repository.db.execContext(ctx, `
INSERT INTO xiangwan_resource_moderation_observations (
    id, tenant_id, relation_id, content_id, content_revision_at,
    provider, provider_reference, policy_version,
    observation_source, decision, subject_digest, payload_digest,
    actor_id, reason, observed_at, recorded_at
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8,
    $9, $10, $11, $12,
    $13, $14, $15, $16
)
ON CONFLICT (tenant_id, provider, provider_reference) DO NOTHING
`,
		value.ID, value.TenantID, value.RelationID, value.ContentID,
		value.ContentRevision, value.Provider, value.ProviderReference,
		value.PolicyVersion, value.Source, value.Decision,
		value.SubjectDigest[:], value.PayloadDigest[:], value.ActorID,
		value.Reason, value.ObservedAt, value.RecordedAt,
	)
	if err != nil {
		return RecordModerationResult{}, classifyModerationWriteError(err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return RecordModerationResult{}, fmt.Errorf(
			"read xiangwan moderation insert result: %w",
			err,
		)
	}
	if affected == 1 {
		return RecordModerationResult{Observation: value}, nil
	}
	if affected != 0 {
		return RecordModerationResult{}, ErrModerationFactsConflict
	}
	existing, err = repository.GetModerationByProviderReference(
		ctx,
		value.TenantID,
		value.Provider,
		value.ProviderReference,
	)
	if err != nil {
		return RecordModerationResult{}, err
	}
	return replayModerationResult(existing, value)
}

func replayModerationResult(
	existing resource.ModerationObservation,
	requested resource.ModerationObservation,
) (RecordModerationResult, error) {
	if !resource.SameModerationIntent(existing, requested) {
		return RecordModerationResult{}, resource.ErrModerationIntentConflict
	}
	return RecordModerationResult{
		Observation: existing,
		Duplicate:   true,
	}, nil
}

func (repository *Repository) GetModerationByID(
	ctx context.Context,
	tenantID uuid.UUID,
	observationID uuid.UUID,
) (resource.ModerationObservation, error) {
	if repository == nil || repository.db == nil ||
		tenantID == uuid.Nil || observationID == uuid.Nil {
		return resource.ModerationObservation{},
			resource.ErrInvalidModerationObservation
	}
	return scanModerationObservation(repository.db.queryRowContext(ctx, `
SELECT
    id, tenant_id, relation_id, content_id, content_revision_at,
    provider, provider_reference, policy_version,
    observation_source, decision, subject_digest, payload_digest,
    actor_id, reason, observed_at, recorded_at
FROM xiangwan_resource_moderation_observations
WHERE tenant_id = $1 AND id = $2
`, tenantID, observationID))
}

func (repository *Repository) GetModerationByProviderReference(
	ctx context.Context,
	tenantID uuid.UUID,
	provider string,
	providerReference string,
) (resource.ModerationObservation, error) {
	if repository == nil || repository.db == nil ||
		tenantID == uuid.Nil || provider == "" || providerReference == "" {
		return resource.ModerationObservation{},
			resource.ErrInvalidModerationObservation
	}
	return scanModerationObservation(repository.db.queryRowContext(ctx, `
SELECT
    id, tenant_id, relation_id, content_id, content_revision_at,
    provider, provider_reference, policy_version,
    observation_source, decision, subject_digest, payload_digest,
    actor_id, reason, observed_at, recorded_at
FROM xiangwan_resource_moderation_observations
WHERE tenant_id = $1
  AND provider = $2
  AND provider_reference = $3
`, tenantID, provider, providerReference))
}

func scanModerationObservation(
	row rowScanner,
) (resource.ModerationObservation, error) {
	var value resource.ModerationObservation
	var subjectDigest []byte
	var payloadDigest []byte
	var actorID uuid.NullUUID
	var reason sql.NullString
	if err := row.Scan(
		&value.ID, &value.TenantID, &value.RelationID, &value.ContentID,
		&value.ContentRevision, &value.Provider, &value.ProviderReference,
		&value.PolicyVersion, &value.Source, &value.Decision,
		&subjectDigest, &payloadDigest, &actorID, &reason,
		&value.ObservedAt, &value.RecordedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return resource.ModerationObservation{}, ErrModerationNotFound
		}
		return resource.ModerationObservation{}, fmt.Errorf(
			"scan xiangwan moderation observation: %w",
			err,
		)
	}
	if len(subjectDigest) != resource.DigestSize ||
		len(payloadDigest) != resource.DigestSize {
		return resource.ModerationObservation{}, ErrModerationFactsConflict
	}
	copy(value.SubjectDigest[:], subjectDigest)
	copy(value.PayloadDigest[:], payloadDigest)
	if actorID.Valid {
		value.ActorID = &actorID.UUID
	}
	if reason.Valid {
		value.Reason = &reason.String
	}
	value.ContentRevision = value.ContentRevision.UTC()
	value.ObservedAt = value.ObservedAt.UTC()
	value.RecordedAt = value.RecordedAt.UTC()
	if resource.ValidateModerationObservation(value) != nil {
		return resource.ModerationObservation{}, ErrModerationFactsConflict
	}
	return value, nil
}

func classifyModerationWriteError(err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		switch postgresError.Code {
		case "23505":
			return fmt.Errorf("%w: %v", ErrModerationExists, err)
		case "23503", "23514", "40001", "40P01":
			return fmt.Errorf("%w: %v", ErrModerationFactsConflict, err)
		}
	}
	return err
}
