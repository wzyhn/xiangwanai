package couponpostgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/checkin"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrInvalidGrantCommand = errors.New(
		`invalid xiangwan Coupon grant command`,
	)
	ErrGrantSourceNotFound = errors.New(
		`xiangwan Coupon Checkin source not found`,
	)
	ErrInitialGrantIneligible = errors.New(
		`xiangwan initial Coupon grant source is ineligible`,
	)
	ErrGrantPolicyUnavailable = errors.New(
		`xiangwan Coupon grant policy is unavailable`,
	)
	ErrManualGrantForbidden = errors.New(
		`xiangwan manual Coupon replenishment is forbidden`,
	)
	ErrGrantFactsConflict = errors.New(
		`xiangwan Coupon grant facts conflict`,
	)
	ErrGrantTransactionConflict = errors.New(
		`xiangwan Coupon grant transaction conflict`,
	)
)

type InitialGuestGrantCommand struct {
	TenantID  uuid.UUID
	CheckinID uuid.UUID
}

type ManualReplenishmentCommand struct {
	TenantID       uuid.UUID
	SourceCouponID uuid.UUID
	ActorID        uuid.UUID
	IdentityLinkID uuid.UUID
	BusinessKey    string
	Reason         string
	Context        string
}

type GrantResult struct {
	Grant     coupon.Grant
	Duplicate bool
}

type CouponAuthorizationQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type GrantPolicyProvider interface {
	CouponGrantPolicyAt(
		context.Context,
		CouponAuthorizationQuery,
		uuid.UUID,
		time.Time,
	) (coupon.GrantPolicy, error)
}

type ManualGrantAuthorizer interface {
	AuthorizeManualCouponGrant(
		context.Context,
		CouponAuthorizationQuery,
		uuid.UUID,
		uuid.UUID,
		uuid.UUID,
	) error
}

// Grantor issues immutable five-Coupon batches in serializable PostgreSQL
// transactions. Automatic callers cannot supply identity, role, face value,
// scope, or expiry facts. No volatile lock or Redis queue participates.
type Grantor struct {
	transactions grantTransactionStarter
	pending      pendingInitialGuestLister
	policies     GrantPolicyProvider
	authorizer   ManualGrantAuthorizer
	now          func() time.Time
}

func NewGrantor(
	db *sql.DB,
	policies GrantPolicyProvider,
	authorizer ManualGrantAuthorizer,
) *Grantor {
	return &Grantor{
		transactions: sqlGrantTransactionStarter{db: db},
		pending:      NewRepository(db),
		policies:     policies,
		authorizer:   authorizer,
		now:          time.Now,
	}
}

func (grantor *Grantor) ReconcilePending(
	ctx context.Context,
	tenantID uuid.UUID,
	limit int,
) ([]GrantResult, error) {
	if grantor == nil || grantor.pending == nil ||
		tenantID == uuid.Nil || limit < 1 || limit > 500 {
		return nil, ErrInvalidGrantCommand
	}
	sources, err := grantor.pending.ListPendingInitialGuestSources(
		ctx,
		tenantID,
		limit,
	)
	if err != nil {
		return nil, err
	}
	results := make([]GrantResult, 0, len(sources))
	for _, source := range sources {
		result, grantErr := grantor.GrantInitialGuest(
			ctx,
			InitialGuestGrantCommand(source),
		)
		if grantErr != nil {
			return results, grantErr
		}
		results = append(results, result)
	}
	return results, nil
}

func (grantor *Grantor) GrantInitialGuest(
	ctx context.Context,
	command InitialGuestGrantCommand,
) (GrantResult, error) {
	if grantor == nil || grantor.transactions == nil ||
		grantor.policies == nil || grantor.now == nil ||
		command.TenantID == uuid.Nil || command.CheckinID == uuid.Nil {
		return GrantResult{}, ErrInvalidGrantCommand
	}
	tx, err := grantor.begin(ctx)
	if err != nil {
		return GrantResult{}, err
	}
	committed := false
	defer rollbackGrant(tx, &committed)

	source, err := tx.lockInitialGuestSource(
		ctx,
		command.TenantID,
		command.CheckinID,
	)
	if err != nil {
		return GrantResult{}, err
	}
	if err := validateInitialGuestSource(source); err != nil {
		return GrantResult{}, err
	}
	if !source.Eligible {
		return GrantResult{}, ErrInitialGrantIneligible
	}
	if err := tx.lockPrincipal(ctx, source.Checkin.PrincipalID); err != nil {
		return GrantResult{}, err
	}

	existing, err := tx.getGrantForUpdate(
		ctx,
		command.TenantID,
		source.Checkin.PrincipalID,
		coupon.GrantKindInitialGuest,
		`initial_guest_grant`,
	)
	switch {
	case err == nil:
		return commitGrantResult(
			tx,
			GrantResult{Grant: existing, Duplicate: true},
			&committed,
		)
	case !errors.Is(err, ErrGrantNotFound):
		return GrantResult{}, err
	}

	policy, err := grantor.currentPolicy(
		ctx,
		tx.authorizationQuery(),
		command.TenantID,
		source.CheckedInEvent.OccurredAt,
	)
	if err != nil {
		return GrantResult{}, err
	}
	candidate, err := coupon.NewInitialGuestGrant(
		coupon.InitialGuestGrantCommand{
			TenantID:    command.TenantID,
			PrincipalID: source.Checkin.PrincipalID,
			Source:      source.Facts,
			Policy:      policy,
			CheckedInAt: source.CheckedInEvent.OccurredAt,
			RecordedAt: grantor.now().UTC().Truncate(
				time.Microsecond,
			),
		},
	)
	if err != nil {
		return GrantResult{}, ErrGrantFactsConflict
	}
	created, err := tx.createGrant(ctx, candidate)
	if err != nil {
		return GrantResult{}, classifyGrantWriteError(err)
	}
	return commitGrantResult(
		tx,
		GrantResult{Grant: created},
		&committed,
	)
}

func (grantor *Grantor) Replenish(
	ctx context.Context,
	command ManualReplenishmentCommand,
) (GrantResult, error) {
	if err := validateManualCommand(grantor, command); err != nil {
		return GrantResult{}, err
	}
	tx, err := grantor.begin(ctx)
	if err != nil {
		return GrantResult{}, err
	}
	committed := false
	defer rollbackGrant(tx, &committed)

	query := tx.authorizationQuery()
	if err := grantor.authorizer.AuthorizeManualCouponGrant(
		ctx,
		query,
		command.TenantID,
		command.ActorID,
		command.IdentityLinkID,
	); err != nil {
		return GrantResult{}, err
	}
	principalID, err := tx.loadCouponOwner(ctx, command.TenantID, command.SourceCouponID)
	if err != nil {
		return GrantResult{}, err
	}
	if err := tx.lockPrincipal(ctx, principalID); err != nil {
		return GrantResult{}, err
	}
	existing, err := tx.getGrantForUpdate(
		ctx,
		command.TenantID,
		principalID,
		coupon.GrantKindManualReplenishment,
		command.BusinessKey,
	)
	switch {
	case err == nil:
		if !manualGrantMatchesCommand(existing, command, principalID) {
			return GrantResult{}, ErrGrantFactsConflict
		}
		return commitGrantResult(
			tx,
			GrantResult{Grant: existing, Duplicate: true},
			&committed,
		)
	case !errors.Is(err, ErrGrantNotFound):
		return GrantResult{}, err
	}

	now := grantor.now().UTC().Truncate(time.Microsecond)
	state, err := tx.currentGrantState(
		ctx,
		command.TenantID,
		principalID,
		now,
	)
	if err != nil {
		return GrantResult{}, err
	}
	if !state.HasHistory || state.CurrentAvailable != 0 {
		return GrantResult{}, ErrManualGrantForbidden
	}
	policy, err := grantor.currentPolicy(
		ctx,
		query,
		command.TenantID,
		now,
	)
	if err != nil {
		return GrantResult{}, err
	}
	candidate, err := coupon.NewManualReplenishment(
		coupon.ManualReplenishmentCommand{
			TenantID:    command.TenantID,
			PrincipalID: principalID,
			ActorID:     command.ActorID,
			BusinessKey: command.BusinessKey,
			Reason:      command.Reason,
			Context:     command.Context,
			Policy:      policy,
			GrantedAt:   now,
			RecordedAt:  now,
		},
	)
	if err != nil {
		return GrantResult{}, ErrInvalidGrantCommand
	}
	created, err := tx.createGrant(ctx, candidate)
	if err != nil {
		return GrantResult{}, classifyGrantWriteError(err)
	}
	if err := tx.auditManualGrant(ctx, command, principalID, created); err != nil {
		return GrantResult{}, err
	}
	return commitGrantResult(
		tx,
		GrantResult{Grant: created},
		&committed,
	)
}

func (grantor *Grantor) currentPolicy(
	ctx context.Context,
	query CouponAuthorizationQuery,
	tenantID uuid.UUID,
	at time.Time,
) (coupon.GrantPolicy, error) {
	policy, err := grantor.policies.CouponGrantPolicyAt(
		ctx,
		query,
		tenantID,
		at,
	)
	if err != nil {
		return coupon.GrantPolicy{}, fmt.Errorf(
			`load current xiangwan Coupon grant policy: %w`,
			err,
		)
	}
	if coupon.ValidateGrantPolicy(policy) != nil {
		return coupon.GrantPolicy{}, ErrGrantPolicyUnavailable
	}
	return policy, nil
}

func (grantor *Grantor) begin(
	ctx context.Context,
) (grantTransaction, error) {
	tx, err := grantor.transactions.beginGrantTx(
		ctx,
		&sql.TxOptions{Isolation: sql.LevelSerializable},
	)
	if err != nil {
		return nil, fmt.Errorf(`begin xiangwan Coupon grant: %w`, err)
	}
	return tx, nil
}

func validateManualCommand(
	grantor *Grantor,
	command ManualReplenishmentCommand,
) error {
	if grantor == nil || grantor.transactions == nil ||
		grantor.policies == nil || grantor.authorizer == nil ||
		grantor.now == nil || command.TenantID == uuid.Nil ||
		command.SourceCouponID == uuid.Nil || command.ActorID == uuid.Nil ||
		command.IdentityLinkID == uuid.Nil ||
		strings.TrimSpace(command.BusinessKey) != command.BusinessKey ||
		command.BusinessKey == `` ||
		len(command.BusinessKey) > 128 ||
		strings.TrimSpace(command.Reason) == `` ||
		len(command.Reason) > 500 ||
		strings.TrimSpace(command.Context) == `` || len(command.Context) > 128 {
		return ErrInvalidGrantCommand
	}
	return nil
}

func validateInitialGuestSource(source initialGuestSource) error {
	if checkin.Validate(source.Checkin) != nil ||
		checkin.ValidateEvent(source.CheckedInEvent) != nil ||
		source.Checkin.CheckinStatus != checkin.StatusCheckedIn ||
		source.CheckedInEvent.TenantID != source.Checkin.TenantID ||
		source.CheckedInEvent.CheckinID != source.Checkin.ID ||
		source.CheckedInEvent.RegistrationID !=
			source.Checkin.RegistrationID ||
		source.CheckedInEvent.SessionID != source.Checkin.SessionID ||
		source.CheckedInEvent.EventType != checkin.EventTypeCheckedIn ||
		source.CheckedInEvent.EventSequence != 1 ||
		!source.CheckedInEvent.OccurredAt.Equal(source.Checkin.CheckedInAt) {
		return ErrGrantFactsConflict
	}
	if source.Eligible {
		if source.Facts.PeopleProfileID == uuid.Nil ||
			source.Facts.PeopleBindingID == uuid.Nil ||
			source.Facts.RoleBindingID == uuid.Nil ||
			source.Facts.CheckinID != source.Checkin.ID ||
			source.Facts.CheckinEventID != source.CheckedInEvent.ID {
			return ErrGrantFactsConflict
		}
	} else if source.Facts != (coupon.SourceFacts{}) {
		return ErrGrantFactsConflict
	}
	return nil
}

func manualGrantMatchesCommand(
	grant coupon.Grant,
	command ManualReplenishmentCommand,
	principalID uuid.UUID,
) bool {
	if coupon.ValidateGrant(grant) != nil ||
		grant.Kind != coupon.GrantKindManualReplenishment ||
		grant.BusinessKey != command.BusinessKey {
		return false
	}
	reason := strings.TrimSpace(command.Reason)
	grantContext := strings.TrimSpace(command.Context)
	for _, instrument := range grant.Coupons {
		if instrument.TenantID != command.TenantID ||
			instrument.PrincipalID != principalID ||
			instrument.GrantedBy == nil ||
			*instrument.GrantedBy != command.ActorID ||
			instrument.GrantReason == nil ||
			*instrument.GrantReason != reason ||
			instrument.GrantContext == nil ||
			*instrument.GrantContext != grantContext {
			return false
		}
	}
	return true
}

func rollbackGrant(tx grantTransaction, committed *bool) {
	if !*committed {
		_ = tx.Rollback()
	}
}

func commitGrantResult(
	tx grantTransaction,
	result GrantResult,
	committed *bool,
) (GrantResult, error) {
	if err := tx.Commit(); err != nil {
		return GrantResult{}, classifyGrantCommitError(err)
	}
	*committed = true
	return result, nil
}

type initialGuestSource struct {
	Checkin        checkin.Checkin
	CheckedInEvent checkin.Event
	Eligible       bool
	Facts          coupon.SourceFacts
}

type pendingInitialGuestLister interface {
	ListPendingInitialGuestSources(
		context.Context,
		uuid.UUID,
		int,
	) ([]PendingInitialGuestSource, error)
}

type grantTransactionStarter interface {
	beginGrantTx(context.Context, *sql.TxOptions) (grantTransaction, error)
}

type grantTransaction interface {
	authorizationQuery() CouponAuthorizationQuery
	loadCouponOwner(context.Context, uuid.UUID, uuid.UUID) (uuid.UUID, error)
	lockInitialGuestSource(
		context.Context,
		uuid.UUID,
		uuid.UUID,
	) (initialGuestSource, error)
	lockPrincipal(context.Context, uuid.UUID) error
	getGrantForUpdate(
		context.Context,
		uuid.UUID,
		uuid.UUID,
		coupon.GrantKind,
		string,
	) (coupon.Grant, error)
	currentGrantState(
		context.Context,
		uuid.UUID,
		uuid.UUID,
		time.Time,
	) (GrantState, error)
	createGrant(context.Context, coupon.Grant) (coupon.Grant, error)
	auditManualGrant(context.Context, ManualReplenishmentCommand, uuid.UUID, coupon.Grant) error
	Commit() error
	Rollback() error
}

func classifyGrantWriteError(err error) error {
	if errors.Is(err, ErrGrantExists) {
		return fmt.Errorf(`%w: %v`, ErrGrantTransactionConflict, err)
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		switch postgresError.Code {
		case `23503`, `23505`, `23514`, `40001`, `40P01`:
			return fmt.Errorf(`%w: %v`, ErrGrantTransactionConflict, err)
		}
	}
	return err
}

func classifyGrantCommitError(err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) &&
		(postgresError.Code == `40001` || postgresError.Code == `40P01`) {
		return fmt.Errorf(`%w: %v`, ErrGrantTransactionConflict, err)
	}
	return err
}
