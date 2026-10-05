// Package consumerprofilepostgres persists Xiangwan consumer profile
// candidates, moderation outcomes, and publications in customer PostgreSQL.
package consumerprofilepostgres

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/consumerprofile"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

const maxProfileWriteAttempts = 3

var (
	ErrInvalidRepository           = errors.New("invalid xiangwan ConsumerProfile repository")
	ErrPrincipalUnavailable        = errors.New("xiangwan ConsumerProfile Principal unavailable")
	ErrProviderIdentityUnavailable = errors.New("xiangwan ConsumerProfile provider identity unavailable")
	ErrOperationNotFound           = errors.New("xiangwan ConsumerProfile operation not found")
	ErrOperationConflict           = errors.New("xiangwan ConsumerProfile operation conflict")
	ErrVersionConflict             = errors.New("xiangwan ConsumerProfile version conflict")
	ErrTransactionConflict         = errors.New("xiangwan ConsumerProfile transaction conflict")
	ErrGenerationInactive          = errors.New("xiangwan ConsumerProfile generation is inactive")
	ErrModerationLeaseLost         = errors.New("xiangwan ConsumerProfile moderation lease is lost")
)

type Repository struct {
	database     *sql.DB
	generationID uuid.UUID
	now          func() time.Time
}

func NewRepository(database *sql.DB, generationID uuid.UUID) *Repository {
	return &Repository{
		database:     database,
		generationID: generationID,
		now:          time.Now,
	}
}

func (repository *Repository) ReadMine(
	ctx context.Context,
	tenantID uuid.UUID,
	principalID uuid.UUID,
) (consumerprofile.Snapshot, error) {
	if repository == nil || repository.database == nil ||
		repository.generationID == uuid.Nil || ctx == nil ||
		tenantID == uuid.Nil || principalID == uuid.Nil {
		return consumerprofile.Snapshot{}, ErrInvalidRepository
	}
	tx, err := repository.database.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelRepeatableRead,
		ReadOnly:  true,
	})
	if err != nil {
		return consumerprofile.Snapshot{}, fmt.Errorf("begin ConsumerProfile read: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	snapshot, err := readPrincipalProfile(ctx, tx, principalID)
	if err != nil {
		return consumerprofile.Snapshot{}, err
	}
	published, err := readPublishedProfile(ctx, tx, tenantID, principalID)
	if err != nil {
		return consumerprofile.Snapshot{}, err
	}
	snapshot.Published = published
	pending, err := readLatestPending(ctx, tx, tenantID, principalID)
	if err != nil {
		return consumerprofile.Snapshot{}, err
	}
	snapshot.Pending = pending
	if err := consumerprofile.ValidateSnapshot(snapshot); err != nil {
		return consumerprofile.Snapshot{}, ErrTransactionConflict
	}
	if err := tx.Commit(); err != nil {
		return consumerprofile.Snapshot{}, classifyWriteError(err)
	}
	return snapshot, nil
}

func (repository *Repository) ProviderOpenID(
	ctx context.Context,
	principalID uuid.UUID,
	appID string,
) (string, error) {
	if repository == nil || repository.database == nil ||
		repository.generationID == uuid.Nil || ctx == nil ||
		principalID == uuid.Nil || appID == "" {
		return "", ErrInvalidRepository
	}
	rows, err := repository.database.QueryContext(ctx, `
SELECT identity_link.provider_id
FROM identity_links AS identity_link
JOIN principals AS principal ON principal.id = identity_link.principal_id
WHERE identity_link.principal_id = $1
  AND identity_link.provider = 'wechat'
  AND identity_link.app_id = $2
  AND principal.status = 'active'
  AND principal.deleted_at IS NULL
ORDER BY identity_link.id
LIMIT 2
`, principalID, appID)
	if err != nil {
		return "", fmt.Errorf("read ConsumerProfile provider identity: %w", err)
	}
	defer func() { _ = rows.Close() }()
	openIDs := make([]string, 0, 2)
	for rows.Next() {
		var openID string
		if err := rows.Scan(&openID); err != nil {
			return "", fmt.Errorf("scan ConsumerProfile provider identity: %w", err)
		}
		openIDs = append(openIDs, openID)
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("iterate ConsumerProfile provider identity: %w", err)
	}
	if len(openIDs) != 1 || openIDs[0] == "" {
		return "", ErrProviderIdentityUnavailable
	}
	return openIDs[0], nil
}

func (repository *Repository) Replay(
	ctx context.Context,
	patch consumerprofile.Patch,
) (consumerprofile.MutationReceipt, error) {
	if repository == nil || repository.database == nil ||
		repository.generationID == uuid.Nil || ctx == nil {
		return consumerprofile.MutationReceipt{}, ErrInvalidRepository
	}
	fingerprint, err := consumerprofile.PatchFingerprint(patch)
	if err != nil {
		return consumerprofile.MutationReceipt{}, ErrInvalidRepository
	}
	receipt, storedFingerprint, err := readReceipt(
		repository.database.QueryRowContext(ctx, receiptQuery,
			patch.TenantID,
			patch.PrincipalID,
			patch.OperationKey,
		),
	)
	if errors.Is(err, sql.ErrNoRows) {
		return consumerprofile.MutationReceipt{}, ErrOperationNotFound
	}
	if err != nil {
		return consumerprofile.MutationReceipt{}, fmt.Errorf("replay ConsumerProfile operation: %w", err)
	}
	if subtle.ConstantTimeCompare(storedFingerprint, fingerprint[:]) != 1 {
		return consumerprofile.MutationReceipt{}, ErrOperationConflict
	}
	receipt.Replayed = true
	if err := consumerprofile.ValidateMutationReceipt(receipt); err != nil {
		return consumerprofile.MutationReceipt{}, ErrTransactionConflict
	}
	return receipt, nil
}

func (repository *Repository) Apply(
	ctx context.Context,
	patch consumerprofile.Patch,
	outcome consumerprofile.ModerationOutcome,
) (consumerprofile.MutationReceipt, error) {
	if repository == nil || repository.database == nil || repository.now == nil ||
		repository.generationID == uuid.Nil || ctx == nil ||
		consumerprofile.ValidateModerationOutcome(outcome) != nil {
		return consumerprofile.MutationReceipt{}, ErrInvalidRepository
	}
	if err := consumerprofile.ValidatePatch(patch); err != nil {
		return consumerprofile.MutationReceipt{}, ErrInvalidRepository
	}
	if (consumerprofile.ModerationText(patch.Fields) == "") !=
		(outcome.Source == consumerprofile.ModerationSourceEmptyContent) {
		return consumerprofile.MutationReceipt{}, ErrInvalidRepository
	}
	for attempt := 0; attempt < maxProfileWriteAttempts; attempt++ {
		receipt, err := repository.applyOnce(ctx, patch, outcome)
		if !errors.Is(err, ErrTransactionConflict) {
			return receipt, err
		}
	}
	return consumerprofile.MutationReceipt{}, ErrTransactionConflict
}

func (repository *Repository) applyOnce(
	ctx context.Context,
	patch consumerprofile.Patch,
	outcome consumerprofile.ModerationOutcome,
) (consumerprofile.MutationReceipt, error) {
	fingerprint, err := consumerprofile.PatchFingerprint(patch)
	if err != nil {
		return consumerprofile.MutationReceipt{}, ErrInvalidRepository
	}
	tx, err := repository.database.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelSerializable,
	})
	if err != nil {
		return consumerprofile.MutationReceipt{}, classifyWriteError(err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := lockActiveGeneration(
		ctx,
		tx,
		patch.TenantID,
		repository.generationID,
	); err != nil {
		return consumerprofile.MutationReceipt{}, err
	}
	if err := lockActivePrincipal(ctx, tx, patch.PrincipalID); err != nil {
		return consumerprofile.MutationReceipt{}, err
	}
	existing, storedFingerprint, err := readReceipt(
		tx.QueryRowContext(ctx, receiptQuery+"\nFOR UPDATE OF candidate",
			patch.TenantID,
			patch.PrincipalID,
			patch.OperationKey,
		),
	)
	switch {
	case err == nil:
		if subtle.ConstantTimeCompare(storedFingerprint, fingerprint[:]) != 1 {
			return consumerprofile.MutationReceipt{}, ErrOperationConflict
		}
		if err := tx.Commit(); err != nil {
			return consumerprofile.MutationReceipt{}, classifyWriteError(err)
		}
		committed = true
		existing.Replayed = true
		return existing, nil
	case !errors.Is(err, sql.ErrNoRows):
		return consumerprofile.MutationReceipt{}, fmt.Errorf("read ConsumerProfile operation: %w", err)
	}
	currentVersion, exists, err := lockPublishedVersion(
		ctx,
		tx,
		patch.TenantID,
		patch.PrincipalID,
	)
	if err != nil {
		return consumerprofile.MutationReceipt{}, err
	}
	if currentVersion != patch.ExpectedVersion {
		return consumerprofile.MutationReceipt{}, ErrVersionConflict
	}

	var submittedAt time.Time
	if err := tx.QueryRowContext(ctx, "SELECT clock_timestamp()").Scan(&submittedAt); err != nil {
		return consumerprofile.MutationReceipt{}, classifyWriteError(err)
	}
	submittedAt = submittedAt.UTC().Truncate(time.Microsecond)
	if _, err := tx.ExecContext(ctx, `
UPDATE xiangwan_consumer_profile_moderation_jobs
SET job_status = 'superseded',
    lease_token = NULL,
    lease_expires_at = NULL,
    last_result = 'superseded',
    updated_at = clock_timestamp()
WHERE tenant_id = $1
  AND principal_id = $2
  AND job_status = 'pending'
`, patch.TenantID, patch.PrincipalID); err != nil {
		return consumerprofile.MutationReceipt{}, classifyWriteError(err)
	}
	candidateID := uuid.New()
	tags, err := json.Marshal(patch.Fields.Tags)
	if err != nil {
		return consumerprofile.MutationReceipt{}, ErrInvalidRepository
	}
	result, err := tx.ExecContext(ctx, `
INSERT INTO xiangwan_consumer_profile_candidates (
    id, tenant_id, principal_id, operation_key, request_fingerprint,
    base_profile_version, occupation, introduction, tags,
    occupation_public, introduction_public, tags_public,
    privacy_policy_version, submitted_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9,
    $10, $11, $12, $13, $14
)
`, candidateID, patch.TenantID, patch.PrincipalID, patch.OperationKey,
		fingerprint[:], patch.ExpectedVersion, patch.Fields.Occupation,
		patch.Fields.Introduction, tags, patch.Fields.Visibility.Occupation,
		patch.Fields.Visibility.Introduction, patch.Fields.Visibility.Tags,
		patch.PrivacyPolicyVersion, submittedAt)
	if err != nil {
		return consumerprofile.MutationReceipt{}, classifyWriteError(err)
	}
	if err := requireOneRow(result); err != nil {
		return consumerprofile.MutationReceipt{}, err
	}
	if err := insertModerationDecision(
		ctx,
		tx,
		patch.TenantID,
		patch.PrincipalID,
		candidateID,
		1,
		outcome,
	); err != nil {
		return consumerprofile.MutationReceipt{}, err
	}
	if outcome.Status == consumerprofile.ModerationStatusPendingReview {
		result, err = tx.ExecContext(ctx, `
INSERT INTO xiangwan_consumer_profile_moderation_jobs (
    tenant_id, principal_id, candidate_id, job_status, attempt_count,
    available_at, created_at, updated_at
) VALUES ($1, $2, $3, 'pending', 0, $4, $4, $4)
`, patch.TenantID, patch.PrincipalID, candidateID, submittedAt)
		if err != nil {
			return consumerprofile.MutationReceipt{}, classifyWriteError(err)
		}
		if err := requireOneRow(result); err != nil {
			return consumerprofile.MutationReceipt{}, err
		}
	}

	publishedVersion := int64(0)
	if outcome.Status == consumerprofile.ModerationStatusApproved {
		publishedVersion = patch.ExpectedVersion + 1
		result, err = tx.ExecContext(ctx, `
INSERT INTO xiangwan_consumer_profile_publications (
    id, tenant_id, principal_id, candidate_id, profile_version, published_at
) VALUES ($1, $2, $3, $4, $5, $6)
`, uuid.New(), patch.TenantID, patch.PrincipalID, candidateID,
			publishedVersion, submittedAt)
		if err != nil {
			return consumerprofile.MutationReceipt{}, classifyWriteError(err)
		}
		if err := requireOneRow(result); err != nil {
			return consumerprofile.MutationReceipt{}, err
		}
		if err := publishProfile(
			ctx,
			tx,
			patch,
			candidateID,
			tags,
			submittedAt,
			exists,
		); err != nil {
			return consumerprofile.MutationReceipt{}, err
		}
	}

	receipt := consumerprofile.MutationReceipt{
		CandidateID:      candidateID,
		Fields:           patch.Fields,
		BaseVersion:      patch.ExpectedVersion,
		CandidateVersion: patch.ExpectedVersion + 1,
		ModerationStatus: outcome.Status,
		PublishedVersion: publishedVersion,
		PrivacyVersion:   patch.PrivacyPolicyVersion,
		SubmittedAt:      submittedAt,
	}
	if err := consumerprofile.ValidateMutationReceipt(receipt); err != nil {
		return consumerprofile.MutationReceipt{}, ErrTransactionConflict
	}
	if err := tx.Commit(); err != nil {
		return consumerprofile.MutationReceipt{}, classifyWriteError(err)
	}
	committed = true
	return receipt, nil
}

type rowScanner interface {
	Scan(...any) error
}

func readPrincipalProfile(
	ctx context.Context,
	tx *sql.Tx,
	principalID uuid.UUID,
) (consumerprofile.Snapshot, error) {
	var nickname, avatarURL string
	var avatarFileID uuid.NullUUID
	err := tx.QueryRowContext(ctx, `
SELECT nickname, avatar_url, avatar_file_id
FROM principals
WHERE id = $1 AND status = 'active' AND deleted_at IS NULL
`, principalID).Scan(&nickname, &avatarURL, &avatarFileID)
	if errors.Is(err, sql.ErrNoRows) {
		return consumerprofile.Snapshot{}, ErrPrincipalUnavailable
	}
	if err != nil {
		return consumerprofile.Snapshot{}, fmt.Errorf("read ConsumerProfile Principal: %w", err)
	}
	var fileID *uuid.UUID
	if avatarFileID.Valid {
		value := avatarFileID.UUID
		fileID = &value
	}
	etag, err := consumerprofile.PrincipalProfileETag(
		principalID,
		nickname,
		avatarURL,
		fileID,
	)
	if err != nil {
		return consumerprofile.Snapshot{}, ErrTransactionConflict
	}
	return consumerprofile.Snapshot{Principal: consumerprofile.PrincipalProfile{
		Nickname:     nickname,
		AvatarURL:    avatarURL,
		AvatarFileID: fileID,
		ETag:         etag,
	}}, nil
}

func readPublishedProfile(
	ctx context.Context,
	tx *sql.Tx,
	tenantID uuid.UUID,
	principalID uuid.UUID,
) (consumerprofile.PublishedProfile, error) {
	var value consumerprofile.PublishedProfile
	var tags []byte
	err := tx.QueryRowContext(ctx, `
SELECT occupation, introduction, tags,
       occupation_public, introduction_public, tags_public,
       privacy_policy_version, version, updated_at
FROM xiangwan_consumer_profiles
WHERE tenant_id = $1 AND principal_id = $2
`, tenantID, principalID).Scan(
		&value.Fields.Occupation,
		&value.Fields.Introduction,
		&tags,
		&value.Fields.Visibility.Occupation,
		&value.Fields.Visibility.Introduction,
		&value.Fields.Visibility.Tags,
		&value.PrivacyPolicyVersion,
		&value.Version,
		&value.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		value.Fields.Tags = []string{}
		return value, nil
	}
	if err != nil {
		return consumerprofile.PublishedProfile{}, fmt.Errorf("read published ConsumerProfile: %w", err)
	}
	if err := json.Unmarshal(tags, &value.Fields.Tags); err != nil {
		return consumerprofile.PublishedProfile{}, ErrTransactionConflict
	}
	return value, nil
}

func readLatestPending(
	ctx context.Context,
	tx *sql.Tx,
	tenantID uuid.UUID,
	principalID uuid.UUID,
) (*consumerprofile.PendingUpdate, error) {
	var value consumerprofile.PendingUpdate
	var tags []byte
	err := tx.QueryRowContext(ctx, `
SELECT candidate.occupation, candidate.introduction, candidate.tags,
       candidate.occupation_public, candidate.introduction_public,
       candidate.tags_public, candidate.base_profile_version + 1,
       candidate.submitted_at
FROM xiangwan_consumer_profile_candidates AS candidate
JOIN LATERAL (
    SELECT decision.verdict
    FROM xiangwan_consumer_profile_moderation_decisions AS decision
    WHERE decision.tenant_id = candidate.tenant_id
      AND decision.candidate_id = candidate.id
    ORDER BY decision.decision_version DESC
    LIMIT 1
) AS latest_decision ON TRUE
LEFT JOIN xiangwan_consumer_profile_publications AS publication
  ON publication.tenant_id = candidate.tenant_id
 AND publication.candidate_id = candidate.id
LEFT JOIN xiangwan_consumer_profiles AS current_profile
  ON current_profile.tenant_id = candidate.tenant_id
 AND current_profile.principal_id = candidate.principal_id
WHERE candidate.tenant_id = $1
  AND candidate.principal_id = $2
  AND latest_decision.verdict = 'pending_review'
  AND publication.id IS NULL
  AND candidate.base_profile_version = COALESCE(current_profile.version, 0)
ORDER BY candidate.submitted_at DESC, candidate.id DESC
LIMIT 1
`, tenantID, principalID).Scan(
		&value.Fields.Occupation,
		&value.Fields.Introduction,
		&tags,
		&value.Fields.Visibility.Occupation,
		&value.Fields.Visibility.Introduction,
		&value.Fields.Visibility.Tags,
		&value.CandidateVersion,
		&value.SubmittedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read pending ConsumerProfile: %w", err)
	}
	if err := json.Unmarshal(tags, &value.Fields.Tags); err != nil {
		return nil, ErrTransactionConflict
	}
	return &value, nil
}

const receiptQuery = `
SELECT candidate.id, candidate.request_fingerprint,
       candidate.base_profile_version,
       candidate.base_profile_version + 1,
       candidate.occupation, candidate.introduction, candidate.tags,
       candidate.occupation_public, candidate.introduction_public,
       candidate.tags_public, candidate.privacy_policy_version,
       candidate.submitted_at, latest_decision.verdict,
       COALESCE(publication.profile_version, 0)
FROM xiangwan_consumer_profile_candidates AS candidate
JOIN principals AS principal ON principal.id = candidate.principal_id
JOIN LATERAL (
    SELECT decision.verdict
    FROM xiangwan_consumer_profile_moderation_decisions AS decision
    WHERE decision.tenant_id = candidate.tenant_id
      AND decision.candidate_id = candidate.id
    ORDER BY decision.decision_version DESC
    LIMIT 1
) AS latest_decision ON TRUE
LEFT JOIN xiangwan_consumer_profile_publications AS publication
  ON publication.tenant_id = candidate.tenant_id
 AND publication.candidate_id = candidate.id
WHERE candidate.tenant_id = $1
  AND candidate.principal_id = $2
  AND candidate.operation_key = $3
  AND principal.status = 'active'
  AND principal.deleted_at IS NULL`

func readReceipt(
	row rowScanner,
) (consumerprofile.MutationReceipt, []byte, error) {
	var value consumerprofile.MutationReceipt
	var fingerprint, tags []byte
	err := row.Scan(
		&value.CandidateID,
		&fingerprint,
		&value.BaseVersion,
		&value.CandidateVersion,
		&value.Fields.Occupation,
		&value.Fields.Introduction,
		&tags,
		&value.Fields.Visibility.Occupation,
		&value.Fields.Visibility.Introduction,
		&value.Fields.Visibility.Tags,
		&value.PrivacyVersion,
		&value.SubmittedAt,
		&value.ModerationStatus,
		&value.PublishedVersion,
	)
	if err != nil {
		return consumerprofile.MutationReceipt{}, nil, err
	}
	if len(fingerprint) != 32 || json.Unmarshal(tags, &value.Fields.Tags) != nil {
		return consumerprofile.MutationReceipt{}, nil, ErrTransactionConflict
	}
	return value, fingerprint, nil
}

func lockActivePrincipal(
	ctx context.Context,
	tx *sql.Tx,
	principalID uuid.UUID,
) error {
	var lockedID uuid.UUID
	err := tx.QueryRowContext(ctx, `
SELECT id
FROM principals
WHERE id = $1 AND status = 'active' AND deleted_at IS NULL
FOR UPDATE
`, principalID).Scan(&lockedID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrPrincipalUnavailable
	}
	if err != nil {
		return fmt.Errorf("lock ConsumerProfile Principal: %w", err)
	}
	return nil
}

func lockPublishedVersion(
	ctx context.Context,
	tx *sql.Tx,
	tenantID uuid.UUID,
	principalID uuid.UUID,
) (int64, bool, error) {
	var version int64
	err := tx.QueryRowContext(ctx, `
SELECT version
FROM xiangwan_consumer_profiles
WHERE tenant_id = $1 AND principal_id = $2
FOR UPDATE
`, tenantID, principalID).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("lock ConsumerProfile version: %w", err)
	}
	return version, true, nil
}

func publishProfile(
	ctx context.Context,
	tx *sql.Tx,
	patch consumerprofile.Patch,
	candidateID uuid.UUID,
	tags []byte,
	publishedAt time.Time,
	exists bool,
) error {
	var result sql.Result
	var err error
	if !exists {
		result, err = tx.ExecContext(ctx, `
INSERT INTO xiangwan_consumer_profiles (
    tenant_id, principal_id, occupation, introduction, tags,
    occupation_public, introduction_public, tags_public,
    privacy_policy_version, version, published_candidate_id,
    created_at, updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, 1, $10, $11, $11
)
`, patch.TenantID, patch.PrincipalID, patch.Fields.Occupation,
			patch.Fields.Introduction, tags, patch.Fields.Visibility.Occupation,
			patch.Fields.Visibility.Introduction, patch.Fields.Visibility.Tags,
			patch.PrivacyPolicyVersion, candidateID, publishedAt)
	} else {
		result, err = tx.ExecContext(ctx, `
UPDATE xiangwan_consumer_profiles
SET occupation = $4,
    introduction = $5,
    tags = $6,
    occupation_public = $7,
    introduction_public = $8,
    tags_public = $9,
    privacy_policy_version = $10,
    version = version + 1,
    published_candidate_id = $11,
    updated_at = $12
WHERE tenant_id = $1 AND principal_id = $2 AND version = $3
`, patch.TenantID, patch.PrincipalID, patch.ExpectedVersion,
			patch.Fields.Occupation, patch.Fields.Introduction, tags,
			patch.Fields.Visibility.Occupation,
			patch.Fields.Visibility.Introduction,
			patch.Fields.Visibility.Tags,
			patch.PrivacyPolicyVersion, candidateID, publishedAt)
	}
	if err != nil {
		return classifyWriteError(err)
	}
	if err := requireOneRow(result); err != nil {
		return ErrVersionConflict
	}
	return nil
}

func insertModerationDecision(
	ctx context.Context,
	tx *sql.Tx,
	tenantID uuid.UUID,
	principalID uuid.UUID,
	candidateID uuid.UUID,
	decisionVersion int64,
	outcome consumerprofile.ModerationOutcome,
) error {
	if consumerprofile.ValidateModerationOutcome(outcome) != nil ||
		decisionVersion < 1 {
		return ErrInvalidRepository
	}
	var providerTraceID any
	var providerObservedAt any
	var providerPolicyVersion any
	var providerLabel any
	var providerSuggest any
	if observation := outcome.Observation; observation != nil {
		providerTraceID = observation.TraceID
		providerObservedAt = observation.ObservedAt
		providerPolicyVersion = observation.PolicyVersion
		providerLabel = observation.Label
		providerSuggest = observation.Suggest
	}
	result, err := tx.ExecContext(ctx, `
INSERT INTO xiangwan_consumer_profile_moderation_decisions (
    id, tenant_id, principal_id, candidate_id, decision_version,
    verdict, decision_source, occurred_at, provider_trace_id,
    provider_observed_at, provider_policy_version, provider_label,
    provider_suggest
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13
)
`, uuid.New(), tenantID, principalID, candidateID, decisionVersion,
		outcome.Status, outcome.Source, outcome.OccurredAt, providerTraceID,
		providerObservedAt, providerPolicyVersion, providerLabel, providerSuggest)
	if err != nil {
		return classifyWriteError(err)
	}
	return requireOneRow(result)
}

func lockActiveGeneration(
	ctx context.Context,
	tx *sql.Tx,
	tenantID uuid.UUID,
	generationID uuid.UUID,
) error {
	var writeEpoch int64
	err := tx.QueryRowContext(ctx, `
SELECT runtime_generation.write_epoch
FROM xiangwan_runtime_generations AS runtime_generation
JOIN tenants AS tenant ON tenant.id = runtime_generation.tenant_id
WHERE runtime_generation.singleton_id = 1
  AND runtime_generation.scope_key = 'wq-xiangwan'
  AND runtime_generation.tenant_id = $1
  AND runtime_generation.active_generation_id = $2
  AND runtime_generation.write_epoch > 0
  AND runtime_generation.bootstrap_completed_at IS NOT NULL
  AND tenant.type = 'business'
  AND tenant.metadata @> '{"product_code":"wq-xiangwan"}'::jsonb
FOR SHARE OF runtime_generation
`, tenantID, generationID).Scan(&writeEpoch)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrGenerationInactive
	}
	if err != nil {
		return fmt.Errorf("lock ConsumerProfile generation: %w", err)
	}
	if writeEpoch < 1 {
		return ErrGenerationInactive
	}
	return nil
}

func requireOneRow(result sql.Result) error {
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return ErrTransactionConflict
	}
	return nil
}

func classifyWriteError(err error) error {
	if err == nil || errors.Is(err, ErrPrincipalUnavailable) ||
		errors.Is(err, ErrProviderIdentityUnavailable) ||
		errors.Is(err, ErrOperationConflict) ||
		errors.Is(err, ErrVersionConflict) ||
		errors.Is(err, ErrTransactionConflict) ||
		errors.Is(err, ErrGenerationInactive) ||
		errors.Is(err, ErrModerationLeaseLost) {
		return err
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		switch postgresError.Code {
		case "23505", "40001", "40P01":
			return fmt.Errorf("%w: %v", ErrTransactionConflict, err)
		}
	}
	return err
}
