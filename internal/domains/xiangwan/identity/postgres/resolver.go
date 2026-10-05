// Package identitypostgres binds Xiangwan WeChat subjects to Principals using
// serializable PostgreSQL transactions and platform identity tables.
package identitypostgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	authstandalonepg "github.com/wzyhn/xiangwanai/internal/capabilities/auth/standalonepg"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/identity"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	productXiangwan = "wq-xiangwan"
	maxResolveTries = 3
)

var policyVersionPattern = regexp.MustCompile(
	`^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$`,
)

var (
	ErrInvalidResolver             = errors.New("invalid xiangwan identity resolver")
	ErrIdentityLinkNotFound        = errors.New("xiangwan identity link not found")
	ErrPrincipalUnavailable        = errors.New("xiangwan Principal unavailable")
	ErrIdentityConflict            = errors.New("xiangwan provider identity conflict")
	ErrIdentityTransactionConflict = errors.New("xiangwan identity transaction conflict")
	ErrIdentityGenerationInactive  = errors.New("xiangwan identity generation is inactive")
)

type Resolver struct {
	tenantID             uuid.UUID
	generationID         uuid.UUID
	appID                string
	privacyPolicyVersion string
	transactions         identityTransactionStarter
}

func NewResolver(
	database *sql.DB,
	tenantID uuid.UUID,
	generationID uuid.UUID,
	appID string,
	privacyPolicyVersion string,
) (*Resolver, error) {
	if database == nil {
		return nil, ErrInvalidResolver
	}
	return newResolver(
		tenantID,
		generationID,
		appID,
		privacyPolicyVersion,
		sqlIdentityTransactionStarter{database: database},
	)
}

func newResolver(
	tenantID uuid.UUID,
	generationID uuid.UUID,
	appID string,
	privacyPolicyVersion string,
	transactions identityTransactionStarter,
) (*Resolver, error) {
	if tenantID == uuid.Nil || generationID == uuid.Nil ||
		!validIdentityAppID(appID) ||
		(privacyPolicyVersion != "" &&
			!policyVersionPattern.MatchString(privacyPolicyVersion)) ||
		transactions == nil {
		return nil, ErrInvalidResolver
	}
	return &Resolver{
		tenantID:             tenantID,
		generationID:         generationID,
		appID:                appID,
		privacyPolicyVersion: privacyPolicyVersion,
		transactions:         transactions,
	}, nil
}

func (resolver *Resolver) Resolve(
	ctx context.Context,
	providerIdentity identity.ProviderIdentity,
	occurredAt time.Time,
) (identity.ResolvedPrincipal, error) {
	if resolver == nil || resolver.tenantID == uuid.Nil ||
		resolver.generationID == uuid.Nil ||
		!validIdentityAppID(resolver.appID) || resolver.transactions == nil ||
		!policyVersionPattern.MatchString(resolver.privacyPolicyVersion) ||
		ctx == nil || occurredAt.IsZero() ||
		identity.ValidateProviderIdentity(providerIdentity) != nil {
		return identity.ResolvedPrincipal{}, ErrInvalidResolver
	}
	for attempt := 0; attempt < maxResolveTries; attempt++ {
		resolved, err := resolver.resolveOnce(
			ctx,
			providerIdentity,
			occurredAt.UTC().Truncate(time.Microsecond),
		)
		if !errors.Is(err, ErrIdentityTransactionConflict) {
			return resolved, err
		}
	}
	return identity.ResolvedPrincipal{}, ErrIdentityTransactionConflict
}

func (resolver *Resolver) resolveOnce(
	ctx context.Context,
	providerIdentity identity.ProviderIdentity,
	occurredAt time.Time,
) (identity.ResolvedPrincipal, error) {
	tx, err := resolver.transactions.beginIdentityTx(
		ctx,
		&sql.TxOptions{Isolation: sql.LevelSerializable},
	)
	if err != nil {
		return identity.ResolvedPrincipal{}, classifyIdentityWriteError(err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := tx.lockActiveGeneration(
		ctx,
		resolver.tenantID,
		resolver.generationID,
	); err != nil {
		return identity.ResolvedPrincipal{}, classifyIdentityWriteError(err)
	}
	if err := tx.lockProviderIdentity(
		ctx,
		resolver.appID,
		providerIdentity,
	); err != nil {
		return identity.ResolvedPrincipal{}, classifyIdentityWriteError(err)
	}

	exact, err := tx.findExactLink(
		ctx,
		resolver.appID,
		providerIdentity.OpenID,
	)
	if err == nil {
		if err := validateActivePrincipal(exact.Principal); err != nil {
			return identity.ResolvedPrincipal{}, err
		}
		if err := reconcileUnionIdentity(
			ctx,
			tx,
			exact,
			providerIdentity.UnionID,
		); err != nil {
			return identity.ResolvedPrincipal{}, err
		}
		if err := tx.recordLogin(
			ctx,
			exact.Principal.ID,
			resolver.appID,
			occurredAt,
		); err != nil {
			return identity.ResolvedPrincipal{}, classifyIdentityWriteError(err)
		}
		if err := tx.recordCollectionBasis(
			ctx,
			resolver.tenantID,
			exact.Principal.ID,
			resolver.generationID,
			resolver.appID,
			resolver.privacyPolicyVersion,
			occurredAt,
		); err != nil {
			return identity.ResolvedPrincipal{}, classifyIdentityWriteError(err)
		}
		if err := tx.Commit(); err != nil {
			return identity.ResolvedPrincipal{}, classifyIdentityWriteError(err)
		}
		committed = true
		return identity.ResolvedPrincipal{ID: exact.Principal.ID}, nil
	}
	if !errors.Is(err, ErrIdentityLinkNotFound) {
		return identity.ResolvedPrincipal{}, classifyIdentityWriteError(err)
	}

	principal, err := resolveUnionPrincipal(
		ctx,
		tx,
		providerIdentity.UnionID,
	)
	isNew := false
	switch {
	case err == nil:
	case errors.Is(err, ErrIdentityLinkNotFound):
		principal = principalState{ID: uuid.New(), Status: "active"}
		if err := tx.createPrincipal(
			ctx,
			principal.ID,
			resolver.tenantID,
			occurredAt,
		); err != nil {
			return identity.ResolvedPrincipal{}, classifyIdentityWriteError(err)
		}
		isNew = true
	default:
		return identity.ResolvedPrincipal{}, err
	}
	if err := validateActivePrincipal(principal); err != nil {
		return identity.ResolvedPrincipal{}, err
	}
	if err := tx.createIdentityLink(
		ctx,
		principal.ID,
		resolver.appID,
		providerIdentity,
		occurredAt,
	); err != nil {
		return identity.ResolvedPrincipal{}, classifyIdentityWriteError(err)
	}
	if err := tx.recordLogin(
		ctx,
		principal.ID,
		resolver.appID,
		occurredAt,
	); err != nil {
		return identity.ResolvedPrincipal{}, classifyIdentityWriteError(err)
	}
	if err := tx.recordCollectionBasis(
		ctx,
		resolver.tenantID,
		principal.ID,
		resolver.generationID,
		resolver.appID,
		resolver.privacyPolicyVersion,
		occurredAt,
	); err != nil {
		return identity.ResolvedPrincipal{}, classifyIdentityWriteError(err)
	}
	if err := tx.Commit(); err != nil {
		return identity.ResolvedPrincipal{}, classifyIdentityWriteError(err)
	}
	committed = true
	return identity.ResolvedPrincipal{ID: principal.ID, IsNew: isNew}, nil
}

type principalState struct {
	ID      uuid.UUID
	Status  string
	Deleted bool
}

type identityLinkState struct {
	ID        uuid.UUID
	Principal principalState
	UnionID   string
}

func validateActivePrincipal(value principalState) error {
	if value.ID == uuid.Nil || value.Status != "active" || value.Deleted {
		return ErrPrincipalUnavailable
	}
	return nil
}

func reconcileUnionIdentity(
	ctx context.Context,
	tx identityTransaction,
	exact identityLinkState,
	providerUnionID string,
) error {
	if providerUnionID == "" {
		return nil
	}
	if exact.UnionID != "" && exact.UnionID != providerUnionID {
		return ErrIdentityConflict
	}
	owners, err := tx.findUnionPrincipalIDs(ctx, providerUnionID)
	if err != nil {
		return classifyIdentityWriteError(err)
	}
	for _, ownerID := range owners {
		if ownerID != exact.Principal.ID {
			return ErrIdentityConflict
		}
	}
	if exact.UnionID == "" {
		if err := tx.setLinkUnionID(ctx, exact.ID, providerUnionID); err != nil {
			return classifyIdentityWriteError(err)
		}
	}
	return nil
}

func resolveUnionPrincipal(
	ctx context.Context,
	tx identityTransaction,
	unionID string,
) (principalState, error) {
	if unionID == "" {
		return principalState{}, ErrIdentityLinkNotFound
	}
	owners, err := tx.findUnionPrincipalIDs(ctx, unionID)
	if err != nil {
		return principalState{}, classifyIdentityWriteError(err)
	}
	if len(owners) == 0 {
		return principalState{}, ErrIdentityLinkNotFound
	}
	principalID := owners[0]
	for _, ownerID := range owners[1:] {
		if ownerID != principalID {
			return principalState{}, ErrIdentityConflict
		}
	}
	principal, err := tx.lockPrincipal(ctx, principalID)
	if err != nil {
		return principalState{}, classifyIdentityWriteError(err)
	}
	return principal, nil
}

type identityTransactionStarter interface {
	beginIdentityTx(context.Context, *sql.TxOptions) (identityTransaction, error)
}

type identityTransaction interface {
	lockActiveGeneration(context.Context, uuid.UUID, uuid.UUID) error
	lockProviderIdentity(context.Context, string, identity.ProviderIdentity) error
	findExactLink(context.Context, string, string) (identityLinkState, error)
	findUnionPrincipalIDs(context.Context, string) ([]uuid.UUID, error)
	lockPrincipal(context.Context, uuid.UUID) (principalState, error)
	createPrincipal(context.Context, uuid.UUID, uuid.UUID, time.Time) error
	createIdentityLink(
		context.Context,
		uuid.UUID,
		string,
		identity.ProviderIdentity,
		time.Time,
	) error
	setLinkUnionID(context.Context, uuid.UUID, string) error
	recordLogin(context.Context, uuid.UUID, string, time.Time) error
	recordCollectionBasis(
		context.Context,
		uuid.UUID,
		uuid.UUID,
		uuid.UUID,
		string,
		string,
		time.Time,
	) error
	Commit() error
	Rollback() error
}

type sqlIdentityTransactionStarter struct {
	database *sql.DB
}

func (starter sqlIdentityTransactionStarter) beginIdentityTx(
	ctx context.Context,
	options *sql.TxOptions,
) (identityTransaction, error) {
	if starter.database == nil {
		return nil, ErrInvalidResolver
	}
	tx, err := starter.database.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	authWriter, err := authstandalonepg.NewWriter(tx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return &sqlIdentityTransaction{tx: tx, auth: authWriter}, nil
}

type sqlIdentityTransaction struct {
	tx   *sql.Tx
	auth *authstandalonepg.Writer
}

func (tx *sqlIdentityTransaction) lockActiveGeneration(
	ctx context.Context,
	tenantID uuid.UUID,
	generationID uuid.UUID,
) error {
	var writeEpoch int64
	err := tx.tx.QueryRowContext(ctx, `
SELECT runtime_generation.write_epoch
FROM xiangwan_runtime_generations AS runtime_generation
JOIN tenants AS tenant
  ON tenant.id = runtime_generation.tenant_id
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
		return ErrIdentityGenerationInactive
	}
	if err != nil {
		return fmt.Errorf("lock xiangwan identity generation: %w", err)
	}
	if writeEpoch < 1 {
		return ErrIdentityGenerationInactive
	}
	return nil
}

func (tx *sqlIdentityTransaction) lockProviderIdentity(
	ctx context.Context,
	appID string,
	providerIdentity identity.ProviderIdentity,
) error {
	return tx.auth.LockWeChatIdentity(
		ctx,
		appID,
		providerIdentity.OpenID,
		providerIdentity.UnionID,
	)
}

func (tx *sqlIdentityTransaction) findExactLink(
	ctx context.Context,
	appID string,
	openID string,
) (identityLinkState, error) {
	value, err := tx.auth.FindWeChatIdentity(ctx, appID, openID)
	if errors.Is(err, authstandalonepg.ErrIdentityNotFound) {
		return identityLinkState{}, ErrIdentityLinkNotFound
	}
	if err != nil {
		return identityLinkState{}, err
	}
	return identityLinkState{
		ID: value.ID,
		Principal: principalState{
			ID:      value.Principal.ID,
			Status:  value.Principal.Status,
			Deleted: value.Principal.Deleted,
		},
		UnionID: value.UnionID,
	}, nil
}

func (tx *sqlIdentityTransaction) findUnionPrincipalIDs(
	ctx context.Context,
	unionID string,
) ([]uuid.UUID, error) {
	return tx.auth.FindWeChatUnionPrincipalIDs(ctx, unionID)
}

func (tx *sqlIdentityTransaction) lockPrincipal(
	ctx context.Context,
	principalID uuid.UUID,
) (principalState, error) {
	value, err := tx.auth.LockPrincipal(ctx, principalID)
	if errors.Is(err, authstandalonepg.ErrPrincipalUnavailable) {
		return principalState{}, ErrPrincipalUnavailable
	}
	if err != nil {
		return principalState{}, err
	}
	return principalState{
		ID:      value.ID,
		Status:  value.Status,
		Deleted: value.Deleted,
	}, nil
}

func (tx *sqlIdentityTransaction) createPrincipal(
	ctx context.Context,
	principalID uuid.UUID,
	tenantID uuid.UUID,
	occurredAt time.Time,
) error {
	return tx.auth.CreatePrincipal(ctx, principalID, tenantID, occurredAt)
}

func (tx *sqlIdentityTransaction) createIdentityLink(
	ctx context.Context,
	principalID uuid.UUID,
	appID string,
	providerIdentity identity.ProviderIdentity,
	occurredAt time.Time,
) error {
	return tx.auth.CreateWeChatIdentity(
		ctx,
		principalID,
		appID,
		providerIdentity.OpenID,
		providerIdentity.UnionID,
		occurredAt,
	)
}

func (tx *sqlIdentityTransaction) setLinkUnionID(
	ctx context.Context,
	linkID uuid.UUID,
	unionID string,
) error {
	return tx.auth.SetWeChatUnionID(ctx, linkID, unionID)
}

func (tx *sqlIdentityTransaction) recordLogin(
	ctx context.Context,
	principalID uuid.UUID,
	appID string,
	occurredAt time.Time,
) error {
	return tx.auth.RecordProductLogin(
		ctx,
		principalID,
		productXiangwan,
		appID,
		occurredAt,
	)
}

func (tx *sqlIdentityTransaction) recordCollectionBasis(
	ctx context.Context,
	tenantID uuid.UUID,
	principalID uuid.UUID,
	generationID uuid.UUID,
	appID string,
	privacyPolicyVersion string,
	occurredAt time.Time,
) error {
	result, err := tx.tx.ExecContext(ctx, `
INSERT INTO xiangwan_identity_login_events (
    id, tenant_id, principal_id, generation_id, app_id,
    privacy_policy_version, occurred_at
) VALUES ($1, $2, $3, $4, $5, $6, $7)
`, uuid.New(), tenantID, principalID, generationID, appID,
		privacyPolicyVersion, occurredAt)
	if err != nil {
		return fmt.Errorf("record xiangwan Login collection basis: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return ErrIdentityTransactionConflict
	}
	return nil
}

func (tx *sqlIdentityTransaction) Commit() error {
	return tx.tx.Commit()
}

func (tx *sqlIdentityTransaction) Rollback() error {
	return tx.tx.Rollback()
}

func classifyIdentityWriteError(err error) error {
	if err == nil || errors.Is(err, ErrIdentityLinkNotFound) ||
		errors.Is(err, ErrPrincipalUnavailable) ||
		errors.Is(err, ErrIdentityConflict) ||
		errors.Is(err, ErrIdentityGenerationInactive) ||
		errors.Is(err, ErrIdentityTransactionConflict) {
		return err
	}
	if errors.Is(err, authstandalonepg.ErrIdentityNotFound) {
		return ErrIdentityLinkNotFound
	}
	if errors.Is(err, authstandalonepg.ErrPrincipalUnavailable) {
		return ErrPrincipalUnavailable
	}
	if errors.Is(err, authstandalonepg.ErrWriteConflict) {
		return ErrIdentityTransactionConflict
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		switch postgresError.Code {
		case "23505", "40001", "40P01":
			return fmt.Errorf("%w: %v", ErrIdentityTransactionConflict, err)
		}
	}
	return err
}
func validIdentityAppID(value string) bool {
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
