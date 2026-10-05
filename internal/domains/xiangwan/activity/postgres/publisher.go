package activitypostgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/google/uuid"
)

const adminPublicationOperationLockReleaseTimeout = 5 * time.Second

var (
	ErrInvalidPublicationCommand          = errors.New("invalid xiangwan publication command")
	ErrPublicationConflict                = errors.New("xiangwan publication conflict")
	ErrPublicationState                   = errors.New("xiangwan activity cannot be published from its current state")
	ErrPublicationSessionSet              = errors.New("xiangwan publication Session set changed")
	ErrAdminPublicationForbidden          = errors.New("xiangwan administrator publication forbidden")
	ErrAdminPublicationGenerationInactive = errors.New(
		"xiangwan administrator publication generation inactive",
	)
	ErrAdminPublicationOperationConflict = errors.New(
		"xiangwan administrator publication operation conflict",
	)
	// ErrPublicationQuickTagUnavailable is returned from the authoritative
	// publication transaction when an Instance references a code that is not
	// present in the tenant's current published BrandProfile vocabulary.
	ErrPublicationQuickTagUnavailable = errors.New(
		"xiangwan publication quick tag is not in the current BrandProfile vocabulary",
	)
)

type PublishInstanceCommand struct {
	TenantID                uuid.UUID
	PublishedBy             uuid.UUID
	AdminIdentityLinkID     uuid.UUID
	ExpectedInstanceVersion int64
	Candidate               activity.InstancePublicationCandidate
	IdempotencyKey          uuid.UUID
	RequestID               string
}

// Publisher commits the complete public Instance projection and its immutable
// receipt in one PostgreSQL transaction.
type Publisher struct {
	transactions        publicationTransactionStarter
	adminOperationLocks adminPublicationOperationLocker
	adminGenerationID   uuid.UUID
	now                 func() time.Time
}

// NewAdminPublisher creates the operator-only publication adapter. Unlike the
// domain publisher, it rechecks the active runtime generation and PostgreSQL
// administrator grant in the same transaction as the publication.
func NewAdminPublisher(db *sql.DB, generationID uuid.UUID) *Publisher {
	return &Publisher{
		transactions:        sqlPublicationTransactionStarter{db: db},
		adminOperationLocks: sqlAdminPublicationOperationLocker{db: db},
		adminGenerationID:   generationID,
		now:                 time.Now,
	}
}

func NewPublisher(db *sql.DB) *Publisher {
	return &Publisher{
		transactions: sqlPublicationTransactionStarter{db: db},
		now:          time.Now,
	}
}

func (publisher *Publisher) Publish(
	ctx context.Context,
	command PublishInstanceCommand,
) (result activity.PublicationEvent, resultErr error) {
	if err := validatePublicationCommand(command); err != nil {
		return activity.PublicationEvent{}, err
	}
	if publisher != nil && publisher.adminGenerationID != uuid.Nil &&
		(command.AdminIdentityLinkID == uuid.Nil || command.IdempotencyKey == uuid.Nil ||
			len(command.RequestID) > 128 ||
			strings.TrimSpace(command.RequestID) != command.RequestID ||
			strings.ContainsAny(command.RequestID, "\r\n\x00")) {
		return activity.PublicationEvent{}, fmt.Errorf(
			"%w: administrator operation identity is invalid",
			ErrInvalidPublicationCommand,
		)
	}
	if err := activity.CheckInstancePublication(command.Candidate); err != nil {
		return activity.PublicationEvent{}, err
	}
	var operationConn *sql.Conn
	var release func() error
	var err error
	if publisher.adminGenerationID != uuid.Nil {
		if publisher.adminOperationLocks == nil {
			return activity.PublicationEvent{}, errors.New(
				"xiangwan administrator publication operation lock is unavailable",
			)
		}
		operationConn, release, err = publisher.adminOperationLocks.lock(
			ctx,
			command.TenantID,
			command.PublishedBy,
			command.IdempotencyKey,
		)
		if err != nil {
			return activity.PublicationEvent{}, err
		}
		defer func() {
			resultErr = errors.Join(resultErr, release())
		}()
	}
	tx, err := publisher.transactions.beginTx(
		ctx, operationConn, &sql.TxOptions{Isolation: sql.LevelSerializable},
	)
	if err != nil {
		return activity.PublicationEvent{}, fmt.Errorf("begin xiangwan publication transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	adminDigest := ""
	if publisher.adminGenerationID != uuid.Nil {
		if err := lockActiveAdminGeneration(
			ctx, tx, command.TenantID, publisher.adminGenerationID,
		); err != nil {
			return activity.PublicationEvent{}, err
		}
		if err := authorizeAdminPublication(
			ctx, tx, command.TenantID, command.PublishedBy,
			command.AdminIdentityLinkID,
		); err != nil {
			return activity.PublicationEvent{}, err
		}
		adminDigest = digestAdminPublicationCommand(command)
		replayResult, replay, replayErr := readAdminPublicationOperation(
			ctx, tx, command, adminDigest,
		)
		if replayErr != nil {
			return activity.PublicationEvent{}, replayErr
		}
		if replay {
			event, getErr := (&Repository{db: tx}).GetPublicationEvent(
				ctx,
				command.TenantID,
				command.Candidate.InstanceID,
				replayResult.PublicationVersion,
			)
			if getErr != nil {
				return activity.PublicationEvent{}, getErr
			}
			if event.ID != replayResult.ID {
				return activity.PublicationEvent{}, ErrAdminPublicationOperationConflict
			}
			if err := tx.Commit(); err != nil {
				return activity.PublicationEvent{}, fmt.Errorf(
					"commit xiangwan administrator publication replay: %w", err,
				)
			}
			committed = true
			return event, nil
		}
		if err := lockPublicationReferenceAggregate(
			ctx, tx, command.TenantID,
		); err != nil {
			return activity.PublicationEvent{}, err
		}
	}

	series, err := lockPublicationSeries(ctx, tx, command.TenantID, command.Candidate.SeriesID)
	if err != nil {
		return activity.PublicationEvent{}, err
	}
	if series.status == activity.SeriesStatusArchived {
		return activity.PublicationEvent{}, fmt.Errorf(
			"%w: Series status %q",
			ErrPublicationState,
			series.status,
		)
	}

	instance, err := lockPublicationInstance(
		ctx,
		tx,
		command.TenantID,
		command.Candidate.SeriesID,
		command.Candidate.InstanceID,
	)
	if err != nil {
		return activity.PublicationEvent{}, err
	}
	if instance.version != command.ExpectedInstanceVersion {
		return activity.PublicationEvent{}, fmt.Errorf(
			"%w: Instance version is %d, expected %d",
			ErrPublicationConflict,
			instance.version,
			command.ExpectedInstanceVersion,
		)
	}
	switch instance.status {
	case activity.InstanceStatusDraft,
		activity.InstanceStatusPendingPublish,
		activity.InstanceStatusPublished:
	default:
		return activity.PublicationEvent{}, fmt.Errorf(
			"%w: Instance status %q",
			ErrPublicationState,
			instance.status,
		)
	}

	storedSessions, err := lockPublicationSessions(
		ctx,
		tx,
		command.TenantID,
		command.Candidate.InstanceID,
	)
	if err != nil {
		return activity.PublicationEvent{}, err
	}
	candidate := command.Candidate
	candidate.QuickTagCodes = append([]string{}, command.Candidate.QuickTagCodes...)
	candidate.Sessions = append(
		[]activity.SessionPublicationCandidate(nil),
		command.Candidate.Sessions...,
	)
	if publisher.adminGenerationID != uuid.Nil {
		readiness, readinessErr := readAdminPublicationReferenceReadiness(
			ctx,
			tx,
			command.TenantID,
			command.Candidate.InstanceID,
			candidate.QuickTagCodes,
		)
		if readinessErr != nil {
			return activity.PublicationEvent{}, readinessErr
		}
		for index := range candidate.Sessions {
			candidate.Sessions[index].References = readiness
		}
	}
	if err := activity.CheckInstancePublication(candidate); err != nil {
		return activity.PublicationEvent{}, err
	}
	digest, err := activity.DigestInstancePublication(candidate)
	if err != nil {
		return activity.PublicationEvent{}, err
	}
	candidates, err := matchPublicationSessions(candidate.Sessions, storedSessions)
	if err != nil {
		return activity.PublicationEvent{}, err
	}

	publishedAt := publisher.now().UTC()
	for _, candidate := range candidates {
		stored := storedSessions[candidate.SessionID]
		if err := updatePublishedSession(
			ctx,
			tx,
			command.TenantID,
			command.Candidate.InstanceID,
			candidate,
			stored.version,
			publishedAt,
		); err != nil {
			return activity.PublicationEvent{}, err
		}
	}

	publicationVersion := instance.publicationVersion + 1
	if err := updatePublishedInstance(
		ctx,
		tx,
		command.TenantID,
		candidate.SeriesID,
		candidate.InstanceID,
		candidate.ActivityType,
		candidate.QuickTagCodes,
		publicationVersion,
		instance.version,
		publishedAt,
	); err != nil {
		return activity.PublicationEvent{}, err
	}

	if instance.publicationVersion == 0 {
		publishedInstanceCount := series.successfulPublishedInstanceCount + 1
		recurring, recurringErr := activity.DecideSeriesRecurring(
			series.isRecurring,
			publishedInstanceCount,
		)
		if recurringErr != nil {
			return activity.PublicationEvent{}, fmt.Errorf(
				"decide xiangwan Series recurrence: %w",
				recurringErr,
			)
		}
		if err := updateSeriesFirstPublication(
			ctx,
			tx,
			command.TenantID,
			candidate.SeriesID,
			candidate.InstanceID,
			publishedInstanceCount,
			recurring,
			series.version,
			publishedAt,
		); err != nil {
			return activity.PublicationEvent{}, err
		}
	}

	event, err := (&Repository{db: tx}).CreatePublicationEvent(ctx, activity.PublicationEvent{
		TenantID:           command.TenantID,
		SeriesID:           candidate.SeriesID,
		InstanceID:         candidate.InstanceID,
		PublicationVersion: publicationVersion,
		SessionCount:       len(candidates),
		CandidateDigest:    digest,
		PublishedBy:        command.PublishedBy,
		PublishedAt:        publishedAt,
	})
	if err != nil {
		return activity.PublicationEvent{}, err
	}
	if publisher.adminGenerationID != uuid.Nil {
		if err := writeAdminPublicationOperation(
			ctx, tx, command, adminDigest, event, publishedAt,
		); err != nil {
			return activity.PublicationEvent{}, err
		}
	}

	if err := tx.Commit(); err != nil {
		return activity.PublicationEvent{}, fmt.Errorf("commit xiangwan publication transaction: %w", err)
	}
	committed = true
	return event, nil
}

func validatePublicationCommand(command PublishInstanceCommand) error {
	switch {
	case command.TenantID == uuid.Nil:
		return fmt.Errorf("%w: tenant_id is required", ErrInvalidPublicationCommand)
	case command.PublishedBy == uuid.Nil:
		return fmt.Errorf("%w: published_by is required", ErrInvalidPublicationCommand)
	case command.ExpectedInstanceVersion < 1:
		return fmt.Errorf("%w: expected_instance_version must be positive", ErrInvalidPublicationCommand)
	case command.IdempotencyKey != uuid.Nil &&
		(command.RequestID == "" || len(command.RequestID) > 128 ||
			strings.TrimSpace(command.RequestID) != command.RequestID ||
			strings.ContainsAny(command.RequestID, "\r\n\x00")):
		return fmt.Errorf("%w: request_id is invalid", ErrInvalidPublicationCommand)
	default:
		return nil
	}
}

func lockActiveAdminGeneration(
	ctx context.Context,
	tx publicationTransaction,
	tenantID uuid.UUID,
	generationID uuid.UUID,
) error {
	var writeEpoch int64
	err := tx.queryRowContext(ctx, `
SELECT write_epoch
FROM xiangwan_runtime_generations
WHERE singleton_id = 1
  AND scope_key = 'wq-xiangwan'
  AND tenant_id = $1
  AND active_generation_id = $2
  AND write_epoch > 0
  AND bootstrap_completed_at IS NOT NULL
FOR SHARE
`, tenantID, generationID).Scan(&writeEpoch)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrAdminPublicationGenerationInactive
	}
	if err != nil {
		return fmt.Errorf("lock xiangwan administrator publication generation: %w", err)
	}
	return nil
}

func authorizeAdminPublication(
	ctx context.Context,
	tx publicationTransaction,
	tenantID uuid.UUID,
	actorID uuid.UUID,
	identityLinkID uuid.UUID,
) error {
	var selectedPrincipalID uuid.UUID
	var selectedIdentityLinkID uuid.UUID
	var selectedGrantID uuid.UUID
	err := tx.queryRowContext(ctx, `
SELECT principal.id, identity_link.id, admin_grant.id
FROM principals AS principal
JOIN xiangwan_admin_identity_links AS identity_link
  ON identity_link.tenant_id = $1
 AND identity_link.principal_id = principal.id
 AND identity_link.link_status = 'active'
 AND identity_link.id = $3
JOIN xiangwan_admin_grants AS admin_grant
  ON admin_grant.tenant_id = $1
 AND admin_grant.principal_id = principal.id
 AND admin_grant.domain_code = 'xiangwan'
 AND admin_grant.grant_status = 'active'
WHERE principal.id = $2
  AND principal.status = 'active'
  AND principal.deleted_at IS NULL
  AND principal.primary_tenant_id = $1
  AND admin_grant.scope_type = 'tenant'
  AND admin_grant.scope_id IS NULL
  AND admin_grant.capability IN ('super_admin', 'activity_operator')
ORDER BY admin_grant.id
LIMIT 1
FOR SHARE OF principal, identity_link, admin_grant
`, tenantID, actorID, identityLinkID).Scan(
		&selectedPrincipalID,
		&selectedIdentityLinkID,
		&selectedGrantID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrAdminPublicationForbidden
	}
	if err != nil {
		return fmt.Errorf("authorize xiangwan administrator publication: %w", err)
	}
	return nil
}

// lockPublicationReferenceAggregate shares a tenant transaction lock with
// every reference-table mutator installed by migration 781. The readiness
// snapshot therefore has a definite order relative to Brand, questionnaire,
// People, role, and resource changes and stays stable through commit.
func lockPublicationReferenceAggregate(
	ctx context.Context,
	tx publicationTransaction,
	tenantID uuid.UUID,
) error {
	var locked bool
	err := tx.queryRowContext(ctx, `
SELECT TRUE
FROM (
    SELECT pg_advisory_xact_lock(
        hashtextextended('wq-xiangwan:publication-references:' || $1::TEXT, 0)
    )
) AS reference_lock
`, tenantID).Scan(&locked)
	if err != nil || !locked {
		if err == nil {
			err = ErrPublicationConflict
		}
		return fmt.Errorf("lock xiangwan publication references: %w", err)
	}
	return nil
}

func readAdminPublicationReferenceReadiness(
	ctx context.Context,
	tx publicationTransaction,
	tenantID uuid.UUID,
	instanceID uuid.UUID,
	quickTagCodes []string,
) (activity.PublicationReferenceReadiness, error) {
	var readiness activity.PublicationReferenceReadiness
	err := tx.queryRowContext(ctx, `
SELECT
    EXISTS (
        SELECT 1
        FROM xiangwan_brand_profiles AS profile
        JOIN xiangwan_brand_profile_publications AS publication
          ON publication.tenant_id = profile.tenant_id
         AND publication.publication_version = profile.current_publication_version
        WHERE profile.tenant_id = $1
          AND profile.lifecycle_status = 'active'
    ),
    NOT EXISTS (
        SELECT 1
        FROM UNNEST($3::TEXT[]) AS selected(code)
        WHERE NOT EXISTS (
            SELECT 1
            FROM xiangwan_brand_profiles AS profile
            JOIN xiangwan_brand_profile_publications AS publication
              ON publication.tenant_id = profile.tenant_id
             AND publication.publication_version = profile.current_publication_version
            CROSS JOIN LATERAL JSONB_ARRAY_ELEMENTS(publication.quick_tags) AS tag(value)
            WHERE profile.tenant_id = $1
              AND profile.lifecycle_status = 'active'
              AND tag.value ->> 'code' = selected.code
        )
    ),
    NOT EXISTS (
        SELECT 1
        FROM xiangwan_instance_questionnaires
        WHERE tenant_id = $1 AND instance_id = $2
    ) OR EXISTS (
        SELECT 1
        FROM xiangwan_instance_questionnaires AS assignment
        JOIN xiangwan_questionnaire_versions AS version
          ON version.tenant_id = assignment.tenant_id
         AND version.instance_id = assignment.instance_id
         AND version.questionnaire_version_id = assignment.questionnaire_version_id
        WHERE assignment.tenant_id = $1
          AND assignment.instance_id = $2
          AND version.status = 'published'
          AND EXISTS (
              SELECT 1 FROM xiangwan_questionnaire_fields AS field
              WHERE field.questionnaire_version_id = version.questionnaire_version_id
          )
        ORDER BY assignment.assignment_version DESC
        LIMIT 1
    ),
    NOT EXISTS (
        SELECT 1
        FROM xiangwan_instance_role_bindings AS role_binding
        WHERE role_binding.tenant_id = $1
          AND role_binding.instance_id = $2
          AND role_binding.role_status = 'active'
          AND NOT EXISTS (
              SELECT 1
              FROM xiangwan_people_bindings AS people_binding
              JOIN xiangwan_people_profiles AS people_profile
                ON people_profile.tenant_id = people_binding.tenant_id
               AND people_profile.id = people_binding.people_profile_id
              WHERE people_binding.tenant_id = role_binding.tenant_id
                AND people_binding.principal_id = role_binding.principal_id
                AND people_binding.binding_status = 'active'
                AND people_profile.profile_status = 'published'
                AND people_profile.moderation_status = 'approved'
          )
    ),
    NOT EXISTS (
        SELECT 1
        FROM xiangwan_resource_relations AS relation
        WHERE relation.tenant_id = $1
          AND relation.instance_id = $2
          AND NOT EXISTS (
              SELECT 1 FROM xiangwan_resource_publications AS publication
              WHERE publication.tenant_id = relation.tenant_id
                AND publication.relation_id = relation.id
          )
    )
`, tenantID, instanceID, quickTagCodes).Scan(
		&readiness.ContentReady,
		&readiness.QuickTagsReady,
		&readiness.QuestionnaireReady,
		&readiness.PeopleReady,
		&readiness.ResourcesReady,
	)
	if err != nil {
		return activity.PublicationReferenceReadiness{}, fmt.Errorf(
			"read administrator publication references: %w",
			err,
		)
	}
	if !readiness.QuickTagsReady {
		return readiness, ErrPublicationQuickTagUnavailable
	}
	return readiness, nil
}

// ValidatePublishedQuickTagCodes checks an administrator draft against the
// tenant's current published BrandProfile vocabulary. Empty activity tags are
// intentionally valid even before a BrandProfile exists; a non-empty list is
// accepted only when every code is present in the active publication. Callers
// use this inside their write/preflight transaction, while the publisher
// repeats the same fact check inside its serializable commit transaction.
func ValidatePublishedQuickTagCodes(
	ctx context.Context,
	db DBTX,
	tenantID uuid.UUID,
	quickTagCodes []string,
) error {
	if tenantID == uuid.Nil {
		return fmt.Errorf("validate published quick tags: tenant_id is required")
	}
	if len(quickTagCodes) == 0 {
		return nil
	}
	var allPublished bool
	err := db.QueryRowContext(ctx, `
SELECT NOT EXISTS (
    SELECT 1
    FROM UNNEST($2::TEXT[]) AS selected(code)
    WHERE NOT EXISTS (
        SELECT 1
        FROM xiangwan_brand_profiles AS profile
        JOIN xiangwan_brand_profile_publications AS publication
          ON publication.tenant_id = profile.tenant_id
         AND publication.publication_version = profile.current_publication_version
        CROSS JOIN LATERAL JSONB_ARRAY_ELEMENTS(publication.quick_tags) AS tag(value)
        WHERE profile.tenant_id = $1
          AND profile.lifecycle_status = 'active'
          AND tag.value ->> 'code' = selected.code
    )
)`, tenantID, quickTagCodes).Scan(&allPublished)
	if err != nil {
		return fmt.Errorf("validate current BrandProfile quick tags: %w", err)
	}
	if !allPublished {
		return ErrPublicationQuickTagUnavailable
	}
	return nil
}

type adminPublicationOperationResult struct {
	ID                 uuid.UUID `json:"id"`
	PublicationVersion int64     `json:"publication_version"`
}

func digestAdminPublicationCommand(command PublishInstanceCommand) string {
	payload := command.Candidate.InstanceID.String() + ":" +
		fmt.Sprintf("%d", command.ExpectedInstanceVersion)
	digest := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(digest[:])
}

func readAdminPublicationOperation(
	ctx context.Context,
	tx publicationTransaction,
	command PublishInstanceCommand,
	digest string,
) (adminPublicationOperationResult, bool, error) {
	var kind string
	var storedDigest string
	var raw []byte
	err := tx.queryRowContext(ctx, `
SELECT operation_kind, request_digest, result
FROM xiangwan_admin_operations
WHERE tenant_id = $1 AND actor_id = $2 AND operation_id = $3
FOR UPDATE
`, command.TenantID, command.PublishedBy, command.IdempotencyKey).Scan(
		&kind, &storedDigest, &raw,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return adminPublicationOperationResult{}, false, nil
	}
	if err != nil {
		return adminPublicationOperationResult{}, false, fmt.Errorf("read administrator publication operation: %w", err)
	}
	if kind != "instance.publish" || storedDigest != digest {
		return adminPublicationOperationResult{}, false, ErrAdminPublicationOperationConflict
	}
	var result adminPublicationOperationResult
	if err := json.Unmarshal(raw, &result); err != nil || result.ID == uuid.Nil ||
		result.PublicationVersion < 1 {
		return adminPublicationOperationResult{}, false, ErrAdminPublicationOperationConflict
	}
	return result, true, nil
}

func writeAdminPublicationOperation(
	ctx context.Context,
	tx publicationTransaction,
	command PublishInstanceCommand,
	digest string,
	event activity.PublicationEvent,
	now time.Time,
) error {
	result, err := json.Marshal(adminPublicationOperationResult{
		ID: event.ID, PublicationVersion: event.PublicationVersion,
	})
	if err != nil {
		return fmt.Errorf("encode administrator publication result: %w", err)
	}
	var operationID uuid.UUID
	err = tx.queryRowContext(ctx, `
INSERT INTO xiangwan_admin_operations (
    tenant_id, actor_id, operation_id, operation_kind,
    request_digest, result, created_at
) VALUES ($1, $2, $3, 'instance.publish', $4, $5::JSONB, $6)
RETURNING operation_id
`, command.TenantID, command.PublishedBy, command.IdempotencyKey,
		digest, string(result), now).Scan(&operationID)
	if err != nil {
		return fmt.Errorf("store administrator publication operation: %w", err)
	}
	var auditID uuid.UUID
	err = tx.queryRowContext(ctx, `
INSERT INTO xiangwan_admin_audit_events (
    id, tenant_id, actor_id, action_code, target_type, target_id,
    request_id, details, occurred_at, created_at
) VALUES (
    $1, $2, $3, 'instance.publish', 'instance', $4,
    $5, JSONB_BUILD_OBJECT(
        'operation_id', $6::UUID,
        'identity_link_id', $7::UUID,
        'publication_event_id', $8::UUID,
        'publication_version', $9::BIGINT,
        'candidate_digest', $10::TEXT
    ), $11, $11
)
RETURNING id
`, uuid.New(), command.TenantID, command.PublishedBy,
		command.Candidate.InstanceID, command.RequestID,
		command.IdempotencyKey, command.AdminIdentityLinkID, event.ID,
		event.PublicationVersion, event.CandidateDigest, now).Scan(&auditID)
	if err != nil {
		return fmt.Errorf("store administrator publication audit: %w", err)
	}
	return nil
}

type lockedPublicationSeries struct {
	status                           activity.SeriesStatus
	successfulPublishedInstanceCount int
	isRecurring                      bool
	version                          int64
}

func lockPublicationSeries(
	ctx context.Context,
	tx publicationTransaction,
	tenantID uuid.UUID,
	seriesID uuid.UUID,
) (lockedPublicationSeries, error) {
	var series lockedPublicationSeries
	err := tx.queryRowContext(ctx, `
SELECT status, successful_published_instance_count, is_recurring, version
FROM xiangwan_activity_series
WHERE tenant_id = $1 AND id = $2
FOR UPDATE
`, tenantID, seriesID).Scan(
		&series.status,
		&series.successfulPublishedInstanceCount,
		&series.isRecurring,
		&series.version,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return lockedPublicationSeries{}, ErrSeriesNotFound
	}
	if err != nil {
		return lockedPublicationSeries{}, fmt.Errorf("lock xiangwan publication Series: %w", err)
	}
	return series, nil
}

type lockedPublicationInstance struct {
	status             activity.InstanceStatus
	publicationVersion int64
	version            int64
}

func lockPublicationInstance(
	ctx context.Context,
	tx publicationTransaction,
	tenantID uuid.UUID,
	seriesID uuid.UUID,
	instanceID uuid.UUID,
) (lockedPublicationInstance, error) {
	var instance lockedPublicationInstance
	err := tx.queryRowContext(ctx, `
SELECT status, publication_version, version
FROM xiangwan_activity_instances
WHERE tenant_id = $1 AND series_id = $2 AND id = $3
FOR UPDATE
`, tenantID, seriesID, instanceID).Scan(
		&instance.status,
		&instance.publicationVersion,
		&instance.version,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return lockedPublicationInstance{}, ErrInstanceNotFound
	}
	if err != nil {
		return lockedPublicationInstance{}, fmt.Errorf("lock xiangwan publication Instance: %w", err)
	}
	return instance, nil
}

type lockedPublicationSession struct {
	status  activity.SessionStatus
	version int64
}

func lockPublicationSessions(
	ctx context.Context,
	tx publicationTransaction,
	tenantID uuid.UUID,
	instanceID uuid.UUID,
) (map[uuid.UUID]lockedPublicationSession, error) {
	rows, err := tx.queryContext(ctx, `
SELECT id, status, version
FROM xiangwan_activity_sessions
WHERE tenant_id = $1 AND instance_id = $2
ORDER BY id
FOR UPDATE
`, tenantID, instanceID)
	if err != nil {
		return nil, fmt.Errorf("lock xiangwan publication Sessions: %w", err)
	}
	defer rows.Close()

	sessions := make(map[uuid.UUID]lockedPublicationSession)
	for rows.Next() {
		var sessionID uuid.UUID
		var session lockedPublicationSession
		if err := rows.Scan(&sessionID, &session.status, &session.version); err != nil {
			return nil, fmt.Errorf("scan locked xiangwan publication Session: %w", err)
		}
		sessions[sessionID] = session
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate locked xiangwan publication Sessions: %w", err)
	}
	return sessions, nil
}

func matchPublicationSessions(
	candidates []activity.SessionPublicationCandidate,
	stored map[uuid.UUID]lockedPublicationSession,
) ([]activity.SessionPublicationCandidate, error) {
	if len(candidates) != len(stored) {
		return nil, fmt.Errorf(
			"%w: candidate has %d Sessions, database has %d",
			ErrPublicationSessionSet,
			len(candidates),
			len(stored),
		)
	}

	ordered := append([]activity.SessionPublicationCandidate(nil), candidates...)
	sort.Slice(ordered, func(left, right int) bool {
		return ordered[left].SessionID.String() < ordered[right].SessionID.String()
	})
	for _, candidate := range ordered {
		session, exists := stored[candidate.SessionID]
		if !exists {
			return nil, fmt.Errorf(
				"%w: candidate Session is not in the Instance",
				ErrPublicationSessionSet,
			)
		}
		switch session.status {
		case activity.SessionStatusDraft, activity.SessionStatusPublished:
		default:
			return nil, fmt.Errorf(
				"%w: Session status %q",
				ErrPublicationState,
				session.status,
			)
		}
	}
	return ordered, nil
}

func updatePublishedSession(
	ctx context.Context,
	tx publicationTransaction,
	tenantID uuid.UUID,
	instanceID uuid.UUID,
	session activity.SessionPublicationCandidate,
	expectedVersion int64,
	publishedAt time.Time,
) error {
	var venueName any
	var address any
	var longitude any
	var latitude any
	var onlineMode any
	var onlineCompliant any
	switch session.DeliveryMode {
	case activity.DeliveryModeOffline:
		venueName = session.OfflineLocation.VenueName
		address = session.OfflineLocation.Address
		longitude = session.OfflineLocation.Longitude
		latitude = session.OfflineLocation.Latitude
	case activity.DeliveryModeOnline:
		onlineMode = session.OnlineParticipation.Mode
		onlineCompliant = session.OnlineParticipation.Compliant
	}

	var newVersion int64
	err := tx.queryRowContext(ctx, `
UPDATE xiangwan_activity_sessions
SET title = $4,
    registration_start_at = $5,
    registration_end_at = $6,
    session_start_at = $7,
    session_end_at = $8,
    capacity = $9,
    group_minimum = $10,
    low_stock_threshold = $11,
    price_cents = $12,
    delivery_mode = $13,
    area_code = $14,
    venue_name = $15,
    address = $16,
    longitude = $17,
    latitude = $18,
    online_participation_mode = $19,
    online_participation_compliant = $20,
    status = 'published',
    published_at = COALESCE(published_at, $21),
    updated_at = $21,
    version = version + 1
WHERE tenant_id = $1
  AND instance_id = $2
  AND id = $3
  AND version = $22
RETURNING version
`,
		tenantID,
		instanceID,
		session.SessionID,
		session.Title,
		session.RegistrationStartAt.UTC(),
		session.RegistrationEndAt.UTC(),
		session.SessionStartAt.UTC(),
		session.SessionEndAt.UTC(),
		session.Capacity,
		session.GroupMinimum,
		session.LowStockThreshold,
		session.PriceCents,
		session.DeliveryMode,
		session.Area,
		venueName,
		address,
		longitude,
		latitude,
		onlineMode,
		onlineCompliant,
		publishedAt,
		expectedVersion,
	).Scan(&newVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: Session version changed", ErrPublicationConflict)
	}
	if err != nil {
		return fmt.Errorf("publish xiangwan Session: %w", err)
	}
	return nil
}

func updatePublishedInstance(
	ctx context.Context,
	tx publicationTransaction,
	tenantID uuid.UUID,
	seriesID uuid.UUID,
	instanceID uuid.UUID,
	activityType activity.ActivityType,
	quickTagCodes []string,
	publicationVersion int64,
	expectedVersion int64,
	publishedAt time.Time,
) error {
	var newVersion int64
	err := tx.queryRowContext(ctx, `
UPDATE xiangwan_activity_instances
SET status = 'published',
    activity_type = $4,
    quick_tag_codes = $5,
    publication_version = $6,
    published_at = COALESCE(published_at, $7),
    updated_at = $7,
    version = version + 1
WHERE tenant_id = $1
  AND series_id = $2
  AND id = $3
  AND version = $8
RETURNING version
`,
		tenantID,
		seriesID,
		instanceID,
		activityType,
		quickTagCodes,
		publicationVersion,
		publishedAt,
		expectedVersion,
	).Scan(&newVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: Instance version changed", ErrPublicationConflict)
	}
	if err != nil {
		return fmt.Errorf("publish xiangwan Instance: %w", err)
	}
	return nil
}

func updateSeriesFirstPublication(
	ctx context.Context,
	tx publicationTransaction,
	tenantID uuid.UUID,
	seriesID uuid.UUID,
	instanceID uuid.UUID,
	publishedInstanceCount int,
	recurring bool,
	expectedVersion int64,
	publishedAt time.Time,
) error {
	var newVersion int64
	err := tx.queryRowContext(ctx, `
UPDATE xiangwan_activity_series
SET status = 'active',
    successful_published_instance_count = $4,
    is_recurring = $5,
    current_public_instance_id = $3,
    updated_at = $6,
    version = version + 1
WHERE tenant_id = $1
  AND id = $2
  AND version = $7
RETURNING version
`,
		tenantID,
		seriesID,
		instanceID,
		publishedInstanceCount,
		recurring,
		publishedAt,
		expectedVersion,
	).Scan(&newVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: Series version changed", ErrPublicationConflict)
	}
	if err != nil {
		return fmt.Errorf("advance xiangwan Series publication: %w", err)
	}
	return nil
}

type publicationTransaction interface {
	queryExecutor
	Commit() error
	Rollback() error
}

type publicationTransactionStarter interface {
	beginTx(context.Context, *sql.Conn, *sql.TxOptions) (publicationTransaction, error)
}

type adminPublicationOperationLocker interface {
	lock(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (*sql.Conn, func() error, error)
}

type sqlAdminPublicationOperationLocker struct {
	db *sql.DB
}

func (locker sqlAdminPublicationOperationLocker) lock(
	ctx context.Context,
	tenantID uuid.UUID,
	actorID uuid.UUID,
	operationID uuid.UUID,
) (*sql.Conn, func() error, error) {
	conn, err := locker.db.Conn(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("open administrator publication operation lock connection: %w", err)
	}
	lockKey := tenantID.String() + ":" + actorID.String() + ":" + operationID.String()
	var locked bool
	err = conn.QueryRowContext(ctx, `
SELECT TRUE
FROM (SELECT pg_advisory_lock(hashtextextended($1, 0))) AS operation_lock
`, lockKey).Scan(&locked)
	if err != nil || !locked {
		discardAdminPublicationOperationLockConnection(conn)
		if err == nil {
			err = ErrAdminPublicationOperationConflict
		}
		return nil, nil, fmt.Errorf("lock administrator publication operation: %w", err)
	}
	var once sync.Once
	var releaseErr error
	release := func() error {
		once.Do(func() {
			releaseCtx, cancelRelease := context.WithTimeout(
				context.WithoutCancel(ctx),
				adminPublicationOperationLockReleaseTimeout,
			)
			defer cancelRelease()
			var unlocked bool
			unlockErr := conn.QueryRowContext(
				releaseCtx,
				`SELECT pg_advisory_unlock(hashtextextended($1, 0))`,
				lockKey,
			).Scan(&unlocked)
			if unlockErr == nil && !unlocked {
				unlockErr = errors.New(
					"administrator publication operation lock was not held by its connection",
				)
			}
			if unlockErr != nil {
				discardAdminPublicationOperationLockConnection(conn)
				releaseErr = unlockErr
				return
			}
			releaseErr = conn.Close()
		})
		return releaseErr
	}
	return conn, release, nil
}

func discardAdminPublicationOperationLockConnection(conn *sql.Conn) {
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	_ = conn.Close()
}

type sqlPublicationTransactionStarter struct {
	db *sql.DB
}

func (starter sqlPublicationTransactionStarter) beginTx(
	ctx context.Context,
	conn *sql.Conn,
	options *sql.TxOptions,
) (publicationTransaction, error) {
	var tx *sql.Tx
	var err error
	if conn != nil {
		tx, err = conn.BeginTx(ctx, options)
	} else {
		tx, err = starter.db.BeginTx(ctx, options)
	}
	if err != nil {
		return nil, err
	}
	return &sqlPublicationTransaction{tx: tx}, nil
}

type sqlPublicationTransaction struct {
	tx *sql.Tx
}

func (tx *sqlPublicationTransaction) queryContext(
	ctx context.Context,
	query string,
	args ...any,
) (rowsScanner, error) {
	return tx.tx.QueryContext(ctx, query, args...)
}

func (tx *sqlPublicationTransaction) queryRowContext(
	ctx context.Context,
	query string,
	args ...any,
) rowScanner {
	return tx.tx.QueryRowContext(ctx, query, args...)
}

func (tx *sqlPublicationTransaction) Commit() error {
	return tx.tx.Commit()
}

func (tx *sqlPublicationTransaction) Rollback() error {
	return tx.tx.Rollback()
}
