package xiangwanruntime

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

const (
	maxAdminSeedNicknameRunes = 64
	adminSeedGrantReason      = "first administrator provisioned by xiangwan bootstrap-admin-identity"
	adminSeedLockQuery        = `SELECT pg_advisory_lock($1)`
	adminSeedUnlockQuery      = `SELECT pg_advisory_unlock($1)`
	// adminSeedLockReleaseTimeout bounds the deferred unlock: WithoutCancel
	// alone would let a stalled network hang the one-shot container forever
	// even after a committed transaction, hiding completion as a hang. When
	// the unlock cannot be confirmed the connection is discarded and the
	// server-side session ends, releasing the lock (codex review 2026-09-19).
	adminSeedLockReleaseTimeout = 5 * time.Second
	adminSeedLinkQuery          = `SELECT id, principal_id, link_status FROM xiangwan_admin_identity_links WHERE tenant_id = $1 AND issuer = $2 AND subject = $3`
	adminSeedLinkInsert         = `INSERT INTO xiangwan_admin_identity_links (id, tenant_id, issuer, subject, principal_id, linked_at, created_at, updated_at) VALUES ($1, $2, $3, $4, $5, $6, $6, $6)`
	adminSeedGrantActiveQuery   = `SELECT id FROM xiangwan_admin_grants WHERE tenant_id = $1 AND principal_id = $2 AND domain_code = 'xiangwan' AND capability = 'super_admin' AND scope_type = 'tenant' AND scope_id IS NULL AND grant_status = 'active'`
	adminSeedGrantInsert        = `INSERT INTO xiangwan_admin_grants (id, tenant_id, principal_id, domain_code, capability, scope_type, grant_status, granted_by, grant_reason, granted_at, created_at, updated_at) VALUES ($1, $2, $3, 'xiangwan', 'super_admin', 'tenant', 'active', $3, $4, $5, $5, $5)`
	adminSeedPrincipalInsert    = `INSERT INTO principals (id, nickname, primary_tenant_id) VALUES ($1, $2, $3)`
	adminSeedPrincipalStatuses  = `SELECT status, deleted_at FROM principals WHERE id = $1`
)

var (
	ErrInvalidAdminSeedCommand = errors.New("invalid xiangwan admin identity seed command")
	ErrAdminSeedUnavailable    = errors.New("xiangwan admin identity seed is unavailable")
	ErrAdminSeedPrincipalDead  = errors.New(
		"xiangwan admin identity seed found a deleted or inactive Principal",
	)
	ErrAdminSeedLinkRevoked = errors.New(
		"xiangwan admin identity seed found a revoked identity link; choose another administrator identity or revoke the revocation deliberately",
	)
)

// adminSeedDatabase hands out the dedicated connection the seeder needs:
// the session advisory lock must live on one PostgreSQL session, and the
// serializable transaction has to run on that same session.
type adminSeedDatabase interface {
	Conn(context.Context) (adminSeedConnection, error)
}

type adminSeedRowScanner interface {
	Scan(destinations ...any) error
}

type adminSeedConnection interface {
	execContext(context.Context, string, ...any) error
	beginAdminSeedTx(context.Context) (adminSeedTransaction, error)
	Close() error
}

type adminSeedTransaction interface {
	queryRow(context.Context, string, ...any) adminSeedRowScanner
	execContext(context.Context, string, ...any) error
	Commit() error
	Rollback() error
}

type sqlAdminSeedDatabase struct{ database *sql.DB }

func (wrapper sqlAdminSeedDatabase) Conn(
	ctx context.Context,
) (adminSeedConnection, error) {
	connection, err := wrapper.database.Conn(ctx)
	if err != nil {
		return nil, err
	}
	return sqlAdminSeedConnection{conn: connection}, nil
}

type sqlAdminSeedConnection struct{ conn *sql.Conn }

func (wrapper sqlAdminSeedConnection) execContext(
	ctx context.Context, query string, arguments ...any,
) error {
	_, err := wrapper.conn.ExecContext(ctx, query, arguments...)
	return err
}

func (wrapper sqlAdminSeedConnection) beginAdminSeedTx(
	ctx context.Context,
) (adminSeedTransaction, error) {
	transaction, err := wrapper.conn.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelSerializable,
	})
	if err != nil {
		return nil, err
	}
	return sqlAdminSeedTransaction{tx: transaction}, nil
}

func (wrapper sqlAdminSeedConnection) Close() error { return wrapper.conn.Close() }

type sqlAdminSeedTransaction struct{ tx *sql.Tx }

func (wrapper sqlAdminSeedTransaction) queryRow(
	ctx context.Context, query string, arguments ...any,
) adminSeedRowScanner {
	return wrapper.tx.QueryRowContext(ctx, query, arguments...)
}

func (wrapper sqlAdminSeedTransaction) execContext(
	ctx context.Context, query string, arguments ...any,
) error {
	_, err := wrapper.tx.ExecContext(ctx, query, arguments...)
	return err
}

func (wrapper sqlAdminSeedTransaction) Commit() error   { return wrapper.tx.Commit() }
func (wrapper sqlAdminSeedTransaction) Rollback() error { return wrapper.tx.Rollback() }

// adminSeedLockKey derives the advisory-lock identity from the exact
// administrator being seeded, so concurrent runners targeting the same
// (tenant, issuer, subject) serialize while different identities never wait
// on each other.
func adminSeedLockKey(tenantID uuid.UUID, issuer string, subject string) int64 {
	digest := sha256.Sum256([]byte(
		tenantID.String() + "\x00" + issuer + "\x00" + subject,
	))
	return int64(binary.BigEndian.Uint64(digest[:8]))
}

// AdminSeedConfig drives the one-shot first-administrator provisioning
// command. bootstrap-generation provisions the tenant and the generation
// authority but no Principal, identity link, or Grant, so without this seed a
// freshly migrated database rejects every OIDC callback and no operator can
// publish the first BrandProfile (codex review 2026-09-19, P1).
type AdminSeedConfig struct {
	DatabaseDSN string
	TenantID    uuid.UUID
	Issuer      string
	Subject     string
	Nickname    string
}

// AdminSeedCommand is the DSN-free form consumed by the AdminSeeder.
type AdminSeedCommand struct {
	TenantID uuid.UUID
	Issuer   string
	Subject  string
	Nickname string
}

type AdminSeedResult struct {
	TenantID       uuid.UUID
	PrincipalID    uuid.UUID
	IdentityLinkID uuid.UUID
	GrantID        uuid.UUID
	AlreadySeeded  bool
}

type AdminSeeder struct {
	database adminSeedDatabase
}

func NewAdminSeeder(database *sql.DB) *AdminSeeder {
	if database == nil {
		return &AdminSeeder{}
	}
	return &AdminSeeder{database: sqlAdminSeedDatabase{database: database}}
}

func validAdminSeedSubject(subject string) bool {
	trimmed := strings.TrimSpace(subject)
	return trimmed != "" && trimmed == subject &&
		utf8.RuneCountInString(trimmed) <= 255 &&
		!strings.ContainsAny(trimmed, "\r\n\x00")
}

func validAdminSeedIssuer(issuer string) bool {
	trimmed := strings.TrimSpace(issuer)
	return utf8.RuneCountInString(trimmed) >= 8 &&
		utf8.RuneCountInString(trimmed) <= 2048 &&
		trimmed == issuer && !strings.ContainsAny(trimmed, "\r\n\x00")
}

func validAdminSeedNickname(nickname string) bool {
	return strings.TrimSpace(nickname) == nickname &&
		utf8.RuneCountInString(nickname) <= maxAdminSeedNicknameRunes
}

// BootstrapAdminIdentity connects with the deployment DSN and delegates to
// AdminSeeder.Seed.
func BootstrapAdminIdentity(
	ctx context.Context,
	config AdminSeedConfig,
) (AdminSeedResult, error) {
	if ctx == nil || strings.TrimSpace(config.DatabaseDSN) == "" ||
		config.TenantID == uuid.Nil || !validAdminSeedIssuer(config.Issuer) ||
		!validAdminSeedSubject(config.Subject) ||
		!validAdminSeedNickname(config.Nickname) {
		return AdminSeedResult{}, ErrInvalidAdminSeedCommand
	}
	database, err := sql.Open("pgx", config.DatabaseDSN)
	if err != nil {
		return AdminSeedResult{}, ErrAdminSeedUnavailable
	}
	defer func() { _ = database.Close() }()
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)

	return NewAdminSeeder(database).Seed(ctx, AdminSeedCommand{
		TenantID: config.TenantID,
		Issuer:   config.Issuer,
		Subject:  config.Subject,
		Nickname: config.Nickname,
	})
}

// Seed provisions (or reconciles) exactly one administrator: an operator
// Principal, the (tenant, issuer, subject) identity link, and an active
// tenant-scoped super_admin Grant. Re-running with the same identity is an
// exact replay — it reports the stored IDs without creating duplicates.
//
// Concurrency: two deployment runners racing the same identity used to both
// read "no link yet" in Serializable isolation and then race the UNIQUE
// constraint; the loser aborted the whole seed run. The seeder now takes a
// session-scoped PostgreSQL advisory lock keyed on the exact identity before
// opening its serializable transaction, on the same dedicated connection, so
// the waiter re-reads after the winner commits and observes the stored link
// (codex review 2026-09-19, follow-up).
func (seeder *AdminSeeder) Seed(
	ctx context.Context,
	command AdminSeedCommand,
) (AdminSeedResult, error) {
	if seeder == nil || seeder.database == nil || ctx == nil ||
		command.TenantID == uuid.Nil || !validAdminSeedIssuer(command.Issuer) ||
		!validAdminSeedSubject(command.Subject) ||
		!validAdminSeedNickname(command.Nickname) {
		return AdminSeedResult{}, ErrInvalidAdminSeedCommand
	}
	connection, err := seeder.database.Conn(ctx)
	if err != nil {
		return AdminSeedResult{}, ErrAdminSeedUnavailable
	}
	defer func() { _ = connection.Close() }()
	lockKey := adminSeedLockKey(command.TenantID, command.Issuer, command.Subject)
	if err := connection.execContext(ctx, adminSeedLockQuery, lockKey); err != nil {
		return AdminSeedResult{}, ErrAdminSeedUnavailable
	}
	defer func() {
		releaseContext, cancelRelease := context.WithTimeout(
			context.WithoutCancel(ctx), adminSeedLockReleaseTimeout,
		)
		defer cancelRelease()
		_ = connection.execContext(releaseContext, adminSeedUnlockQuery, lockKey)
	}()

	transaction, err := connection.beginAdminSeedTx(ctx)
	if err != nil {
		return AdminSeedResult{}, ErrAdminSeedUnavailable
	}
	defer func() { _ = transaction.Rollback() }()

	var (
		principalID    uuid.UUID
		identityLinkID uuid.UUID
		linkStatus     string
		alreadySeeded  bool
	)
	switch err := transaction.queryRow(
		ctx, adminSeedLinkQuery, command.TenantID, command.Issuer, command.Subject,
	).Scan(&identityLinkID, &principalID, &linkStatus); {
	case errors.Is(err, sql.ErrNoRows):
		principalID = uuid.New()
		identityLinkID = uuid.New()
		nickname := command.Nickname
		if strings.TrimSpace(nickname) == "" {
			nickname = "xiangwan-admin"
		}
		if err := transaction.execContext(
			ctx, adminSeedPrincipalInsert, principalID, nickname, command.TenantID,
		); err != nil {
			return AdminSeedResult{}, fmt.Errorf(
				"%w: create administrator Principal: %v", ErrAdminSeedUnavailable, err,
			)
		}
		if err := transaction.execContext(
			ctx, adminSeedLinkInsert,
			identityLinkID, command.TenantID, command.Issuer, command.Subject,
			principalID, time.Now().UTC(),
		); err != nil {
			return AdminSeedResult{}, fmt.Errorf(
				"%w: link administrator identity: %v", ErrAdminSeedUnavailable, err,
			)
		}
	case err != nil:
		return AdminSeedResult{}, ErrAdminSeedUnavailable
	default:
		// FinishLogin only accepts link_status='active'; seeding over a
		// revoked link would report success while the identity can never
		// log in, so surface the revoked link explicitly (codex review
		// 2026-09-19).
		if linkStatus != "active" {
			return AdminSeedResult{}, ErrAdminSeedLinkRevoked
		}
		alreadySeeded = true
		var status string
		var deletedAt sql.NullTime
		if err := transaction.queryRow(
			ctx, adminSeedPrincipalStatuses, principalID,
		).Scan(&status, &deletedAt); err != nil {
			return AdminSeedResult{}, ErrAdminSeedUnavailable
		}
		if status != "active" || deletedAt.Valid {
			return AdminSeedResult{}, ErrAdminSeedPrincipalDead
		}
	}

	var grantID uuid.UUID
	switch err := transaction.queryRow(
		ctx, adminSeedGrantActiveQuery, command.TenantID, principalID,
	).Scan(&grantID); {
	case errors.Is(err, sql.ErrNoRows):
		grantID = uuid.New()
		if err := transaction.execContext(
			ctx, adminSeedGrantInsert,
			grantID, command.TenantID, principalID, adminSeedGrantReason, time.Now().UTC(),
		); err != nil {
			return AdminSeedResult{}, fmt.Errorf(
				"%w: grant administrator capability: %v", ErrAdminSeedUnavailable, err,
			)
		}
	case err != nil:
		return AdminSeedResult{}, ErrAdminSeedUnavailable
	}

	if err := transaction.Commit(); err != nil {
		return AdminSeedResult{}, ErrAdminSeedUnavailable
	}
	return AdminSeedResult{
		TenantID:       command.TenantID,
		PrincipalID:    principalID,
		IdentityLinkID: identityLinkID,
		GrantID:        grantID,
		AlreadySeeded:  alreadySeeded,
	}, nil
}
