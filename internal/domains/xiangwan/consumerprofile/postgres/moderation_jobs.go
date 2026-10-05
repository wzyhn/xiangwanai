package consumerprofilepostgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/consumerprofile"
	"github.com/google/uuid"
)

const (
	maximumModerationBackoff  = time.Hour
	maximumModerationAttempts = 8
)

func (repository *Repository) ClaimModerationTask(
	ctx context.Context,
	tenantID uuid.UUID,
) (consumerprofile.ModerationTask, bool, error) {
	if repository == nil || repository.database == nil ||
		repository.generationID == uuid.Nil || ctx == nil || tenantID == uuid.Nil {
		return consumerprofile.ModerationTask{}, false, ErrInvalidRepository
	}
	tx, err := repository.database.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return consumerprofile.ModerationTask{}, false, classifyWriteError(err)
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
		tenantID,
		repository.generationID,
	); err != nil {
		return consumerprofile.ModerationTask{}, false, err
	}
	leaseToken := uuid.New()
	var task consumerprofile.ModerationTask
	var tags []byte
	err = tx.QueryRowContext(ctx, `
WITH exhausted AS (
    UPDATE xiangwan_consumer_profile_moderation_jobs AS job
    SET job_status = 'manual_required',
        lease_token = NULL,
        lease_expires_at = NULL,
        last_result = 'manual_required',
        updated_at = clock_timestamp()
    WHERE job.tenant_id = $1
      AND job.job_status = 'pending'
      AND job.attempt_count >= $3
      AND (
          job.lease_expires_at IS NULL
          OR job.lease_expires_at <= clock_timestamp()
      )
    RETURNING job.candidate_id
), selected AS (
    SELECT job.tenant_id, job.candidate_id
    FROM xiangwan_consumer_profile_moderation_jobs AS job
    WHERE job.tenant_id = $1
      AND job.job_status = 'pending'
      AND job.attempt_count < $3
      AND job.available_at <= clock_timestamp()
      AND (
          job.lease_expires_at IS NULL
          OR job.lease_expires_at <= clock_timestamp()
      )
    ORDER BY job.available_at, job.candidate_id
    LIMIT 1
    FOR UPDATE OF job SKIP LOCKED
), claimed AS (
    UPDATE xiangwan_consumer_profile_moderation_jobs AS job
    SET lease_token = $2,
        lease_expires_at = clock_timestamp() + INTERVAL '30 seconds',
        attempt_count = job.attempt_count + 1,
        updated_at = clock_timestamp()
    FROM selected
    WHERE job.tenant_id = selected.tenant_id
      AND job.candidate_id = selected.candidate_id
    RETURNING job.candidate_id, job.principal_id, job.attempt_count
)
SELECT claimed.candidate_id, claimed.principal_id, claimed.attempt_count,
       candidate.occupation, candidate.introduction, candidate.tags,
       candidate.occupation_public, candidate.introduction_public,
       candidate.tags_public
FROM claimed
JOIN xiangwan_consumer_profile_candidates AS candidate
  ON candidate.tenant_id = $1
 AND candidate.id = claimed.candidate_id
CROSS JOIN (SELECT COUNT(*) FROM exhausted) AS exhausted_summary
`, tenantID, leaseToken, maximumModerationAttempts).Scan(
		&task.CandidateID,
		&task.PrincipalID,
		&task.Attempt,
		&task.Fields.Occupation,
		&task.Fields.Introduction,
		&tags,
		&task.Fields.Visibility.Occupation,
		&task.Fields.Visibility.Introduction,
		&task.Fields.Visibility.Tags,
	)
	if errors.Is(err, sql.ErrNoRows) {
		if err := tx.Commit(); err != nil {
			return consumerprofile.ModerationTask{}, false, classifyWriteError(err)
		}
		committed = true
		return consumerprofile.ModerationTask{}, false, nil
	}
	if err != nil {
		return consumerprofile.ModerationTask{}, false,
			fmt.Errorf("claim ConsumerProfile moderation task: %w", err)
	}
	task.LeaseToken = leaseToken
	if json.Unmarshal(tags, &task.Fields.Tags) != nil ||
		consumerprofile.ValidateModerationTask(task) != nil {
		return consumerprofile.ModerationTask{}, false, ErrTransactionConflict
	}
	if err := tx.Commit(); err != nil {
		return consumerprofile.ModerationTask{}, false, classifyWriteError(err)
	}
	committed = true
	return task, true, nil
}

func (repository *Repository) ResolveModerationTask(
	ctx context.Context,
	tenantID uuid.UUID,
	candidateID uuid.UUID,
	leaseToken uuid.UUID,
	outcome consumerprofile.ModerationOutcome,
) error {
	if repository == nil || repository.database == nil || repository.now == nil ||
		repository.generationID == uuid.Nil || ctx == nil || tenantID == uuid.Nil ||
		candidateID == uuid.Nil || leaseToken == uuid.Nil ||
		outcome.Source == consumerprofile.ModerationSourceEmptyContent ||
		consumerprofile.ValidateModerationOutcome(outcome) != nil {
		return ErrInvalidRepository
	}
	for attempt := 0; attempt < maxProfileWriteAttempts; attempt++ {
		err := repository.resolveModerationTaskOnce(
			ctx,
			tenantID,
			candidateID,
			leaseToken,
			outcome,
		)
		if !errors.Is(err, ErrTransactionConflict) {
			return err
		}
	}
	return ErrTransactionConflict
}

func (repository *Repository) resolveModerationTaskOnce(
	ctx context.Context,
	tenantID uuid.UUID,
	candidateID uuid.UUID,
	leaseToken uuid.UUID,
	outcome consumerprofile.ModerationOutcome,
) error {
	tx, err := repository.database.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelSerializable,
	})
	if err != nil {
		return classifyWriteError(err)
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
		tenantID,
		repository.generationID,
	); err != nil {
		return err
	}
	var patch consumerprofile.Patch
	var attemptCount int
	var tags []byte
	err = tx.QueryRowContext(ctx, `
SELECT candidate.principal_id, candidate.operation_key,
       candidate.base_profile_version, candidate.occupation,
       candidate.introduction, candidate.tags,
       candidate.occupation_public, candidate.introduction_public,
       candidate.tags_public, candidate.privacy_policy_version,
       job.attempt_count
FROM xiangwan_consumer_profile_moderation_jobs AS job
JOIN xiangwan_consumer_profile_candidates AS candidate
  ON candidate.tenant_id = job.tenant_id
 AND candidate.principal_id = job.principal_id
 AND candidate.id = job.candidate_id
WHERE job.tenant_id = $1
  AND job.candidate_id = $2
  AND job.job_status = 'pending'
  AND job.lease_token = $3
  AND job.lease_expires_at > clock_timestamp()
FOR UPDATE OF job
`, tenantID, candidateID, leaseToken).Scan(
		&patch.PrincipalID,
		&patch.OperationKey,
		&patch.ExpectedVersion,
		&patch.Fields.Occupation,
		&patch.Fields.Introduction,
		&tags,
		&patch.Fields.Visibility.Occupation,
		&patch.Fields.Visibility.Introduction,
		&patch.Fields.Visibility.Tags,
		&patch.PrivacyPolicyVersion,
		&attemptCount,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrModerationLeaseLost
	}
	if err != nil {
		return fmt.Errorf("lock ConsumerProfile moderation task: %w", err)
	}
	patch.TenantID = tenantID
	if json.Unmarshal(tags, &patch.Fields.Tags) != nil ||
		consumerprofile.ValidatePatch(patch) != nil {
		return ErrTransactionConflict
	}
	currentVersion, exists, err := lockPublishedVersion(
		ctx,
		tx,
		tenantID,
		patch.PrincipalID,
	)
	if err != nil {
		return err
	}
	if currentVersion != patch.ExpectedVersion {
		if err := finishModerationJob(
			ctx,
			tx,
			tenantID,
			candidateID,
			leaseToken,
			"superseded",
			"superseded",
		); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return classifyWriteError(err)
		}
		committed = true
		return nil
	}
	var decisionVersion int64
	if err := tx.QueryRowContext(ctx, `
SELECT COALESCE(MAX(decision_version), 0) + 1
FROM xiangwan_consumer_profile_moderation_decisions
WHERE tenant_id = $1 AND candidate_id = $2
`, tenantID, candidateID).Scan(&decisionVersion); err != nil {
		return fmt.Errorf("read ConsumerProfile moderation version: %w", err)
	}
	if err := insertModerationDecision(
		ctx,
		tx,
		tenantID,
		patch.PrincipalID,
		candidateID,
		decisionVersion,
		outcome,
	); err != nil {
		return err
	}
	if outcome.Status == consumerprofile.ModerationStatusApproved {
		var publishedAt time.Time
		if err := tx.QueryRowContext(ctx, "SELECT clock_timestamp()").Scan(&publishedAt); err != nil {
			return classifyWriteError(err)
		}
		publishedAt = publishedAt.UTC().Truncate(time.Microsecond)
		result, err := tx.ExecContext(ctx, `
INSERT INTO xiangwan_consumer_profile_publications (
    id, tenant_id, principal_id, candidate_id, profile_version, published_at
) VALUES ($1, $2, $3, $4, $5, $6)
`, uuid.New(), tenantID, patch.PrincipalID, candidateID,
			patch.ExpectedVersion+1, publishedAt)
		if err != nil {
			return classifyWriteError(err)
		}
		if err := requireOneRow(result); err != nil {
			return err
		}
		if err := publishProfile(
			ctx,
			tx,
			patch,
			candidateID,
			tags,
			publishedAt,
			exists,
		); err != nil {
			return err
		}
		if err := finishModerationJob(
			ctx,
			tx,
			tenantID,
			candidateID,
			leaseToken,
			"completed",
			string(outcome.Status),
		); err != nil {
			return err
		}
	} else if outcome.Status == consumerprofile.ModerationStatusRejected {
		if err := finishModerationJob(
			ctx,
			tx,
			tenantID,
			candidateID,
			leaseToken,
			"completed",
			string(outcome.Status),
		); err != nil {
			return err
		}
	} else if attemptCount >= maximumModerationAttempts {
		if err := finishModerationJob(
			ctx,
			tx,
			tenantID,
			candidateID,
			leaseToken,
			"manual_required",
			"manual_required",
		); err != nil {
			return err
		}
	} else {
		delaySeconds := int64(moderationBackoff(attemptCount) / time.Second)
		result, err := tx.ExecContext(ctx, `
UPDATE xiangwan_consumer_profile_moderation_jobs
SET available_at = clock_timestamp() + ($5 * INTERVAL '1 second'),
    lease_token = NULL,
    lease_expires_at = NULL,
    last_result = $4,
    updated_at = clock_timestamp()
WHERE tenant_id = $1
  AND candidate_id = $2
  AND lease_token = $3
  AND job_status = 'pending'
`, tenantID, candidateID, leaseToken, string(outcome.Status), delaySeconds)
		if err != nil {
			return classifyWriteError(err)
		}
		if err := requireOneRow(result); err != nil {
			return ErrModerationLeaseLost
		}
	}
	if err := tx.Commit(); err != nil {
		return classifyWriteError(err)
	}
	committed = true
	return nil
}

func finishModerationJob(
	ctx context.Context,
	tx *sql.Tx,
	tenantID uuid.UUID,
	candidateID uuid.UUID,
	leaseToken uuid.UUID,
	jobStatus string,
	lastResult string,
) error {
	result, err := tx.ExecContext(ctx, `
UPDATE xiangwan_consumer_profile_moderation_jobs
SET job_status = $4,
    lease_token = NULL,
    lease_expires_at = NULL,
    last_result = $5,
    updated_at = clock_timestamp()
WHERE tenant_id = $1
  AND candidate_id = $2
  AND lease_token = $3
  AND job_status = 'pending'
`, tenantID, candidateID, leaseToken, jobStatus, lastResult)
	if err != nil {
		return classifyWriteError(err)
	}
	if err := requireOneRow(result); err != nil {
		return ErrModerationLeaseLost
	}
	return nil
}

func moderationBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := 30 * time.Second
	for current := 1; current < attempt && delay < maximumModerationBackoff; current++ {
		delay *= 2
	}
	if delay > maximumModerationBackoff {
		return maximumModerationBackoff
	}
	return delay
}
