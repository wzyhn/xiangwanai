package contributionpostgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/checkin"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/contribution"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrInvalidReconcileCommand = errors.New(
		`invalid xiangwan Contribution reconciliation command`,
	)
	ErrContributionSourceNotFound = errors.New(
		`xiangwan Contribution Checkin source not found`,
	)
	ErrContributionFactsConflict = errors.New(
		`xiangwan Contribution source facts conflict`,
	)
	ErrContributionTransactionConflict = errors.New(
		`xiangwan Contribution transaction conflict`,
	)
	ErrContributionGenerationInactive = errors.New("xiangwan Contribution generation is inactive")
)

type ReconcileCommand struct {
	TenantID  uuid.UUID
	CheckinID uuid.UUID
}

type ReconcileResult struct {
	SourceEligible bool
	Earned         *contribution.Entry
	Reversal       *contribution.Entry
	Changed        bool
}

// Reconciler projects one authoritative Checkin lifecycle into the append-only
// contribution ledger. It accepts no role, identity, or amount from callers.
type Reconciler struct {
	transactions reconcileTransactionStarter
	pending      pendingSourceLister
	now          func() time.Time
	tenantID     uuid.UUID
	generationID uuid.UUID
}

func NewReconciler(db *sql.DB) *Reconciler {
	return &Reconciler{
		transactions: sqlReconcileTransactionStarter{db: db},
		pending:      NewRepository(db),
		now:          time.Now,
	}
}

// NewReconcilerWithGeneration is the production worker boundary. Legacy
// constructors remain available to isolated repository/integration callers.
func NewReconcilerWithGeneration(db *sql.DB, tenantID, generationID uuid.UUID) (*Reconciler, error) {
	if db == nil || tenantID == uuid.Nil || generationID == uuid.Nil {
		return nil, ErrInvalidReconcileCommand
	}
	value := NewReconciler(db)
	value.tenantID, value.generationID = tenantID, generationID
	return value, nil
}

func (reconciler *Reconciler) ReconcilePending(
	ctx context.Context,
	tenantID uuid.UUID,
	limit int,
) ([]ReconcileResult, error) {
	if reconciler == nil || reconciler.pending == nil ||
		tenantID == uuid.Nil || limit < 1 || limit > 500 {
		return nil, ErrInvalidReconcileCommand
	}
	sources, err := reconciler.pending.ListPendingSources(
		ctx,
		tenantID,
		limit,
	)
	if err != nil {
		return nil, err
	}
	results := make([]ReconcileResult, 0, len(sources))
	for _, source := range sources {
		result, reconcileErr := reconciler.Reconcile(
			ctx,
			ReconcileCommand(source),
		)
		if reconcileErr != nil {
			return results, reconcileErr
		}
		results = append(results, result)
	}
	return results, nil
}

func (reconciler *Reconciler) Reconcile(
	ctx context.Context,
	command ReconcileCommand,
) (ReconcileResult, error) {
	if reconciler == nil || reconciler.transactions == nil ||
		reconciler.now == nil || command.TenantID == uuid.Nil ||
		command.CheckinID == uuid.Nil {
		return ReconcileResult{}, ErrInvalidReconcileCommand
	}
	tx, err := reconciler.transactions.beginReconcileTx(
		ctx,
		&sql.TxOptions{Isolation: sql.LevelSerializable},
	)
	if err != nil {
		return ReconcileResult{}, fmt.Errorf(
			`begin xiangwan Contribution reconciliation: %w`,
			err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if reconciler.generationID != uuid.Nil {
		carrier, ok := tx.(*sqlReconcileTransaction)
		if !ok || command.TenantID != reconciler.tenantID {
			return ReconcileResult{}, ErrContributionGenerationInactive
		}
		var epoch int64
		if err := carrier.tx.QueryRowContext(ctx, `SELECT write_epoch FROM xiangwan_runtime_generations WHERE singleton_id=1 AND scope_key='wq-xiangwan' AND tenant_id=$1 AND active_generation_id=$2 AND write_epoch>0 AND bootstrap_completed_at IS NOT NULL FOR SHARE`, reconciler.tenantID, reconciler.generationID).Scan(&epoch); errors.Is(err, sql.ErrNoRows) {
			return ReconcileResult{}, ErrContributionGenerationInactive
		} else if err != nil {
			return ReconcileResult{}, err
		}
	}
	source, err := tx.lockSource(
		ctx,
		command.TenantID,
		command.CheckinID,
	)
	if err != nil {
		return ReconcileResult{}, err
	}
	if err := validateReconcileSource(source); err != nil {
		return ReconcileResult{}, err
	}
	result := ReconcileResult{SourceEligible: source.Eligible}
	earned, err := tx.getEarnedForUpdate(
		ctx,
		source.Checkin.TenantID,
		source.Checkin.PrincipalID,
		source.Checkin.InstanceID,
		contribution.TypeHostCheckin,
	)
	switch {
	case err == nil:
		if contribution.Validate(earned) != nil ||
			earned.EntryKind != contribution.EntryKindEarned ||
			earned.TenantID != source.Checkin.TenantID ||
			earned.PrincipalID != source.Checkin.PrincipalID ||
			earned.InstanceID != source.Checkin.InstanceID ||
			earned.ContributionType != contribution.TypeHostCheckin {
			return ReconcileResult{}, ErrContributionFactsConflict
		}
		result.Earned = &earned
	case errors.Is(err, ErrEntryNotFound):
		if !source.Eligible {
			return commitReconcileResult(tx, result, &committed)
		}
		candidate, earnErr := contribution.Earn(contribution.EarnCommand{
			TenantID:         source.Checkin.TenantID,
			PeopleProfileID:  source.PeopleProfileID,
			PeopleBindingID:  source.PeopleBindingID,
			PrincipalID:      source.Checkin.PrincipalID,
			SeriesID:         source.Checkin.SeriesID,
			InstanceID:       source.Checkin.InstanceID,
			RegistrationID:   source.Checkin.RegistrationID,
			SessionID:        source.Checkin.SessionID,
			CheckinID:        source.Checkin.ID,
			CheckinEventID:   source.CheckedInEvent.ID,
			RoleBindingID:    source.RoleBindingID,
			ContributionType: contribution.TypeHostCheckin,
			OccurredAt:       source.CheckedInEvent.OccurredAt,
			RecordedAt:       reconciler.now().UTC(),
		})
		if earnErr != nil {
			return ReconcileResult{}, ErrContributionFactsConflict
		}
		earned, err = tx.createEntry(ctx, candidate)
		if errors.Is(err, ErrEntryExists) {
			earned, err = tx.getEarnedForUpdate(
				ctx,
				source.Checkin.TenantID,
				source.Checkin.PrincipalID,
				source.Checkin.InstanceID,
				contribution.TypeHostCheckin,
			)
		}
		if err != nil {
			return ReconcileResult{}, classifyContributionWriteError(err)
		}
		if contribution.Validate(earned) != nil {
			return ReconcileResult{}, ErrContributionFactsConflict
		}
		result.Earned = &earned
		result.Changed = earned.ID == candidate.ID
	default:
		return ReconcileResult{}, err
	}

	if result.Earned.CheckinID != source.Checkin.ID {
		return commitReconcileResult(tx, result, &committed)
	}
	if !earnedMatchesSource(*result.Earned, source) {
		return ReconcileResult{}, ErrContributionFactsConflict
	}
	if source.Checkin.CheckinStatus != checkin.StatusRevoked {
		return commitReconcileResult(tx, result, &committed)
	}

	reversal, err := tx.getReversalForUpdate(
		ctx,
		source.Checkin.TenantID,
		result.Earned.ID,
	)
	switch {
	case err == nil:
		if !reversalMatchesSource(reversal, *result.Earned, source) {
			return ReconcileResult{}, ErrContributionFactsConflict
		}
		result.Reversal = &reversal
	case errors.Is(err, ErrEntryNotFound):
		candidate, reverseErr := contribution.Reverse(
			contribution.ReverseCommand{
				Earned:         *result.Earned,
				CheckinEventID: source.RevokedEvent.ID,
				OccurredAt:     source.RevokedEvent.OccurredAt,
				RecordedAt:     reconciler.now().UTC(),
			},
		)
		if reverseErr != nil {
			return ReconcileResult{}, ErrContributionFactsConflict
		}
		reversal, err = tx.createEntry(ctx, candidate)
		if errors.Is(err, ErrEntryExists) {
			reversal, err = tx.getReversalForUpdate(
				ctx,
				source.Checkin.TenantID,
				result.Earned.ID,
			)
		}
		if err != nil {
			return ReconcileResult{}, classifyContributionWriteError(err)
		}
		if !reversalMatchesSource(reversal, *result.Earned, source) {
			return ReconcileResult{}, ErrContributionFactsConflict
		}
		result.Reversal = &reversal
		result.Changed = result.Changed || reversal.ID == candidate.ID
	default:
		return ReconcileResult{}, err
	}
	return commitReconcileResult(tx, result, &committed)
}

func commitReconcileResult(
	tx reconcileTransaction,
	result ReconcileResult,
	committed *bool,
) (ReconcileResult, error) {
	if err := tx.Commit(); err != nil {
		return ReconcileResult{}, classifyContributionCommitError(err)
	}
	*committed = true
	return result, nil
}

type reconcileSource struct {
	Checkin         checkin.Checkin
	CheckedInEvent  checkin.Event
	RevokedEvent    *checkin.Event
	Eligible        bool
	PeopleProfileID uuid.UUID
	PeopleBindingID uuid.UUID
	RoleBindingID   uuid.UUID
}

func validateReconcileSource(source reconcileSource) error {
	if checkin.Validate(source.Checkin) != nil ||
		checkin.ValidateEvent(source.CheckedInEvent) != nil ||
		source.CheckedInEvent.TenantID != source.Checkin.TenantID ||
		source.CheckedInEvent.CheckinID != source.Checkin.ID ||
		source.CheckedInEvent.RegistrationID != source.Checkin.RegistrationID ||
		source.CheckedInEvent.SessionID != source.Checkin.SessionID ||
		source.CheckedInEvent.EventType != checkin.EventTypeCheckedIn ||
		!source.CheckedInEvent.OccurredAt.Equal(source.Checkin.CheckedInAt) {
		return ErrContributionFactsConflict
	}
	if source.Eligible &&
		(source.PeopleProfileID == uuid.Nil ||
			source.PeopleBindingID == uuid.Nil ||
			source.RoleBindingID == uuid.Nil) {
		return ErrContributionFactsConflict
	}
	if !source.Eligible &&
		(source.PeopleProfileID != uuid.Nil ||
			source.PeopleBindingID != uuid.Nil ||
			source.RoleBindingID != uuid.Nil) {
		return ErrContributionFactsConflict
	}
	switch source.Checkin.CheckinStatus {
	case checkin.StatusCheckedIn:
		if source.RevokedEvent != nil {
			return ErrContributionFactsConflict
		}
	case checkin.StatusRevoked:
		if source.RevokedEvent == nil ||
			checkin.ValidateEvent(*source.RevokedEvent) != nil ||
			source.RevokedEvent.TenantID != source.Checkin.TenantID ||
			source.RevokedEvent.CheckinID != source.Checkin.ID ||
			source.RevokedEvent.RegistrationID != source.Checkin.RegistrationID ||
			source.RevokedEvent.SessionID != source.Checkin.SessionID ||
			source.RevokedEvent.EventType != checkin.EventTypeRevoked ||
			source.RevokedEvent.ResultingCheckinVersion !=
				source.Checkin.Version ||
			source.Checkin.RevokedAt == nil ||
			!source.RevokedEvent.OccurredAt.Equal(
				*source.Checkin.RevokedAt,
			) {
			return ErrContributionFactsConflict
		}
	default:
		return ErrContributionFactsConflict
	}
	return nil
}

func earnedMatchesSource(
	entry contribution.Entry,
	source reconcileSource,
) bool {
	return contribution.Validate(entry) == nil &&
		entry.EntryKind == contribution.EntryKindEarned &&
		entry.TenantID == source.Checkin.TenantID &&
		entry.PeopleProfileID == source.PeopleProfileID &&
		entry.PeopleBindingID == source.PeopleBindingID &&
		entry.PrincipalID == source.Checkin.PrincipalID &&
		entry.SeriesID == source.Checkin.SeriesID &&
		entry.InstanceID == source.Checkin.InstanceID &&
		entry.RegistrationID == source.Checkin.RegistrationID &&
		entry.SessionID == source.Checkin.SessionID &&
		entry.CheckinID == source.Checkin.ID &&
		entry.CheckinEventID == source.CheckedInEvent.ID &&
		entry.RoleBindingID == source.RoleBindingID &&
		entry.ContributionType == contribution.TypeHostCheckin
}

func reversalMatchesSource(
	reversal contribution.Entry,
	earned contribution.Entry,
	source reconcileSource,
) bool {
	return source.RevokedEvent != nil &&
		contribution.Validate(reversal) == nil &&
		reversal.EntryKind == contribution.EntryKindReversed &&
		reversal.ReversalOfEntryID != nil &&
		*reversal.ReversalOfEntryID == earned.ID &&
		reversal.CheckinEventID == source.RevokedEvent.ID &&
		reversal.OccurredAt.Equal(source.RevokedEvent.OccurredAt) &&
		reversal.TenantID == earned.TenantID &&
		reversal.PeopleProfileID == earned.PeopleProfileID &&
		reversal.PeopleBindingID == earned.PeopleBindingID &&
		reversal.PrincipalID == earned.PrincipalID &&
		reversal.SeriesID == earned.SeriesID &&
		reversal.InstanceID == earned.InstanceID &&
		reversal.RegistrationID == earned.RegistrationID &&
		reversal.SessionID == earned.SessionID &&
		reversal.CheckinID == earned.CheckinID &&
		reversal.RoleBindingID == earned.RoleBindingID
}

type reconcileTransactionStarter interface {
	beginReconcileTx(context.Context, *sql.TxOptions) (reconcileTransaction, error)
}

type pendingSourceLister interface {
	ListPendingSources(
		context.Context,
		uuid.UUID,
		int,
	) ([]PendingSource, error)
}

type reconcileTransaction interface {
	lockSource(context.Context, uuid.UUID, uuid.UUID) (reconcileSource, error)
	getEarnedForUpdate(
		context.Context,
		uuid.UUID,
		uuid.UUID,
		uuid.UUID,
		contribution.Type,
	) (contribution.Entry, error)
	getReversalForUpdate(
		context.Context,
		uuid.UUID,
		uuid.UUID,
	) (contribution.Entry, error)
	createEntry(
		context.Context,
		contribution.Entry,
	) (contribution.Entry, error)
	Commit() error
	Rollback() error
}

type sqlReconcileTransactionStarter struct {
	db *sql.DB
}

func (starter sqlReconcileTransactionStarter) beginReconcileTx(
	ctx context.Context,
	options *sql.TxOptions,
) (reconcileTransaction, error) {
	if starter.db == nil {
		return nil, ErrInvalidReconcileCommand
	}
	tx, err := starter.db.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return &sqlReconcileTransaction{
		tx:         tx,
		repository: NewRepository(tx),
	}, nil
}

type sqlReconcileTransaction struct {
	tx         *sql.Tx
	repository *Repository
}

func (tx *sqlReconcileTransaction) lockSource(
	ctx context.Context,
	tenantID uuid.UUID,
	checkinID uuid.UUID,
) (reconcileSource, error) {
	current, err := scanCheckin(tx.tx.QueryRowContext(ctx, `
SELECT
    id, tenant_id, registration_id, series_id, instance_id, session_id,
    principal_id, checkin_status, checked_in_by, checked_in_at,
    revoked_by, revoked_at, revocation_reason, version, created_at, updated_at
FROM xiangwan_checkins
WHERE tenant_id = $1 AND id = $2
FOR UPDATE
`, tenantID, checkinID))
	if errors.Is(err, sql.ErrNoRows) {
		return reconcileSource{}, ErrContributionSourceNotFound
	}
	if err != nil {
		return reconcileSource{}, fmt.Errorf(
			`lock xiangwan Contribution Checkin source: %w`,
			err,
		)
	}
	checkedInEvent, err := tx.getCheckinEvent(
		ctx,
		tenantID,
		checkinID,
		checkin.EventTypeCheckedIn,
	)
	if err != nil {
		return reconcileSource{}, err
	}
	source := reconcileSource{
		Checkin:        current,
		CheckedInEvent: checkedInEvent,
	}
	if current.CheckinStatus == checkin.StatusRevoked {
		revokedEvent, eventErr := tx.getCheckinEvent(
			ctx,
			tenantID,
			checkinID,
			checkin.EventTypeRevoked,
		)
		if eventErr != nil {
			return reconcileSource{}, eventErr
		}
		source.RevokedEvent = &revokedEvent
	}
	identity, identityErr := tx.loadEligibility(ctx, current)
	if identityErr != nil {
		return reconcileSource{}, identityErr
	}
	if identity != nil {
		source.Eligible = true
		source.PeopleProfileID = identity.peopleProfileID
		source.PeopleBindingID = identity.peopleBindingID
		source.RoleBindingID = identity.roleBindingID
	}
	return source, nil
}

func (tx *sqlReconcileTransaction) getCheckinEvent(
	ctx context.Context,
	tenantID uuid.UUID,
	checkinID uuid.UUID,
	eventType checkin.EventType,
) (checkin.Event, error) {
	value, err := scanCheckinEvent(tx.tx.QueryRowContext(ctx, `
SELECT
    id, tenant_id, checkin_id, registration_id, session_id,
    event_sequence, event_type, idempotency_key, from_status, to_status,
    actor_id, reason, occurred_at, resulting_checkin_version, created_at
FROM xiangwan_checkin_events
WHERE tenant_id = $1 AND checkin_id = $2 AND event_type = $3
`, tenantID, checkinID, eventType))
	if errors.Is(err, sql.ErrNoRows) {
		return checkin.Event{}, ErrContributionFactsConflict
	}
	if err != nil {
		return checkin.Event{}, fmt.Errorf(
			`load xiangwan Contribution Checkin event: %w`,
			err,
		)
	}
	return value, nil
}

type eligibilityIdentity struct {
	peopleProfileID uuid.UUID
	peopleBindingID uuid.UUID
	roleBindingID   uuid.UUID
}

func (tx *sqlReconcileTransaction) loadEligibility(
	ctx context.Context,
	current checkin.Checkin,
) (*eligibilityIdentity, error) {
	rows, err := tx.tx.QueryContext(ctx, `
SELECT
    people_binding.people_profile_id,
    people_binding.id,
    role_binding.id
FROM xiangwan_people_bindings AS people_binding
JOIN xiangwan_instance_role_bindings AS role_binding
  ON role_binding.tenant_id = people_binding.tenant_id
 AND role_binding.principal_id = people_binding.principal_id
 AND role_binding.series_id = $3
 AND role_binding.instance_id = $4
 AND role_binding.role_code = 'host'
 AND role_binding.granted_at <= $5
 AND (
     role_binding.revoked_at IS NULL
     OR role_binding.revoked_at > $5
 )
WHERE people_binding.tenant_id = $1
  AND people_binding.principal_id = $2
  AND people_binding.bound_at <= $5
  AND (
      people_binding.revoked_at IS NULL
      OR people_binding.revoked_at > $5
  )
ORDER BY people_binding.id, role_binding.id
LIMIT 2
`,
		current.TenantID,
		current.PrincipalID,
		current.SeriesID,
		current.InstanceID,
		current.CheckedInAt,
	)
	if err != nil {
		return nil, fmt.Errorf(
			`load xiangwan Contribution eligibility: %w`,
			err,
		)
	}
	defer rows.Close()
	var result *eligibilityIdentity
	for rows.Next() {
		if result != nil {
			return nil, ErrContributionFactsConflict
		}
		value := &eligibilityIdentity{}
		if err := rows.Scan(
			&value.peopleProfileID,
			&value.peopleBindingID,
			&value.roleBindingID,
		); err != nil {
			return nil, fmt.Errorf(
				`scan xiangwan Contribution eligibility: %w`,
				err,
			)
		}
		result = value
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(
			`iterate xiangwan Contribution eligibility: %w`,
			err,
		)
	}
	return result, nil
}

func (tx *sqlReconcileTransaction) getEarnedForUpdate(
	ctx context.Context,
	tenantID uuid.UUID,
	principalID uuid.UUID,
	instanceID uuid.UUID,
	contributionType contribution.Type,
) (contribution.Entry, error) {
	return tx.repository.GetEarnedForUpdate(
		ctx,
		tenantID,
		principalID,
		instanceID,
		contributionType,
	)
}

func (tx *sqlReconcileTransaction) getReversalForUpdate(
	ctx context.Context,
	tenantID uuid.UUID,
	earnedEntryID uuid.UUID,
) (contribution.Entry, error) {
	return tx.repository.GetReversalForUpdate(
		ctx,
		tenantID,
		earnedEntryID,
	)
}

func (tx *sqlReconcileTransaction) createEntry(
	ctx context.Context,
	value contribution.Entry,
) (contribution.Entry, error) {
	return tx.repository.Create(ctx, value)
}

func (tx *sqlReconcileTransaction) Commit() error {
	return tx.tx.Commit()
}

func (tx *sqlReconcileTransaction) Rollback() error {
	return tx.tx.Rollback()
}

func scanCheckin(row rowScanner) (checkin.Checkin, error) {
	var value checkin.Checkin
	var revokedBy uuid.NullUUID
	var revokedAt sql.NullTime
	var revocationReason sql.NullString
	err := row.Scan(
		&value.ID,
		&value.TenantID,
		&value.RegistrationID,
		&value.SeriesID,
		&value.InstanceID,
		&value.SessionID,
		&value.PrincipalID,
		&value.CheckinStatus,
		&value.CheckedInBy,
		&value.CheckedInAt,
		&revokedBy,
		&revokedAt,
		&revocationReason,
		&value.Version,
		&value.CreatedAt,
		&value.UpdatedAt,
	)
	if err != nil {
		return checkin.Checkin{}, err
	}
	if revokedBy.Valid {
		value.RevokedBy = &revokedBy.UUID
	}
	if revokedAt.Valid {
		value.RevokedAt = &revokedAt.Time
	}
	if revocationReason.Valid {
		value.RevocationReason = &revocationReason.String
	}
	return value, nil
}

func scanCheckinEvent(row rowScanner) (checkin.Event, error) {
	var value checkin.Event
	var fromStatus sql.NullString
	var reason sql.NullString
	err := row.Scan(
		&value.ID,
		&value.TenantID,
		&value.CheckinID,
		&value.RegistrationID,
		&value.SessionID,
		&value.EventSequence,
		&value.EventType,
		&value.IdempotencyKey,
		&fromStatus,
		&value.ToStatus,
		&value.ActorID,
		&reason,
		&value.OccurredAt,
		&value.ResultingCheckinVersion,
		&value.CreatedAt,
	)
	if err != nil {
		return checkin.Event{}, err
	}
	if fromStatus.Valid {
		status := checkin.Status(fromStatus.String)
		value.FromStatus = &status
	}
	if reason.Valid {
		value.Reason = &reason.String
	}
	return value, nil
}

func classifyContributionWriteError(err error) error {
	if errors.Is(err, ErrEntryNotFound) {
		return ErrContributionFactsConflict
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		switch postgresError.Code {
		case `23503`, `23505`, `23514`, `40001`, `40P01`:
			return fmt.Errorf(
				`%w: %v`,
				ErrContributionTransactionConflict,
				err,
			)
		}
	}
	return err
}

func classifyContributionCommitError(err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) &&
		(postgresError.Code == `40001` || postgresError.Code == `40P01`) {
		return fmt.Errorf(
			`%w: %v`,
			ErrContributionTransactionConflict,
			err,
		)
	}
	return err
}
