package xiangwanruntime

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

type fakeAdminSeedRow struct {
	values []any
	err    error
}

func (row fakeAdminSeedRow) Scan(destinations ...any) error {
	if row.err != nil {
		return row.err
	}
	if len(destinations) != len(row.values) {
		return errors.New("admin seed row destination mismatch")
	}
	for index, destination := range destinations {
		switch target := destination.(type) {
		case *uuid.UUID:
			value, ok := row.values[index].(uuid.UUID)
			if !ok {
				return errors.New("admin seed row uuid mismatch")
			}
			*target = value
		case *string:
			value, ok := row.values[index].(string)
			if !ok {
				return errors.New("admin seed row string mismatch")
			}
			*target = value
		case *sql.NullTime:
			value, ok := row.values[index].(sql.NullTime)
			if !ok {
				return errors.New("admin seed row null time mismatch")
			}
			*target = value
		default:
			return errors.New("admin seed row unsupported destination")
		}
	}
	return nil
}

type fakeAdminSeedTransaction struct {
	queries   []string
	rows      []fakeAdminSeedRow
	committed bool
}

func (transaction *fakeAdminSeedTransaction) queryRow(
	_ context.Context, query string, _ ...any,
) adminSeedRowScanner {
	transaction.queries = append(transaction.queries, query)
	if len(transaction.rows) == 0 {
		return fakeAdminSeedRow{err: sql.ErrNoRows}
	}
	row := transaction.rows[0]
	transaction.rows = transaction.rows[1:]
	return row
}

func (transaction *fakeAdminSeedTransaction) execContext(
	_ context.Context, query string, _ ...any,
) error {
	transaction.queries = append(transaction.queries, query)
	return nil
}

func (transaction *fakeAdminSeedTransaction) Commit() error {
	transaction.committed = true
	return nil
}

func (transaction *fakeAdminSeedTransaction) Rollback() error { return nil }

type fakeAdminSeedConnection struct {
	queries     []string
	keys        []int64
	transaction *fakeAdminSeedTransaction
	closed      bool
}

func (connection *fakeAdminSeedConnection) execContext(
	_ context.Context, query string, arguments ...any,
) error {
	connection.queries = append(connection.queries, query)
	if len(arguments) > 0 {
		if key, ok := arguments[0].(int64); ok {
			connection.keys = append(connection.keys, key)
		}
	}
	return nil
}

func (connection *fakeAdminSeedConnection) beginAdminSeedTx(
	_ context.Context,
) (adminSeedTransaction, error) {
	if connection.transaction == nil {
		return nil, errors.New("no transaction stub")
	}
	return connection.transaction, nil
}

func (connection *fakeAdminSeedConnection) Close() error {
	connection.closed = true
	return nil
}

type fakeAdminSeedDatabase struct{ connection *fakeAdminSeedConnection }

func (database fakeAdminSeedDatabase) Conn(
	context.Context,
) (adminSeedConnection, error) {
	return database.connection, nil
}

// The seed must serialize concurrent runners on a session advisory lock
// acquired before the serializable transaction and released after it, on the
// same dedicated connection (codex review 2026-09-19, follow-up).
func TestAdminSeederLocksTheIdentitySessionAroundTheTransaction(t *testing.T) {
	t.Parallel()

	tenantID := uuid.New()
	linkID := uuid.New()
	principalID := uuid.New()
	grantID := uuid.New()
	transaction := &fakeAdminSeedTransaction{rows: []fakeAdminSeedRow{
		{values: []any{linkID, principalID, "active"}}, // existing active link
		{values: []any{"active", sql.NullTime{}}},
		{values: []any{grantID}}, // existing active grant
	}}
	connection := &fakeAdminSeedConnection{transaction: transaction}
	result, err := (&AdminSeeder{database: fakeAdminSeedDatabase{
		connection: connection,
	}}).Seed(context.Background(), AdminSeedCommand{
		TenantID: tenantID,
		Issuer:   "https://admin.example.com/realms/xiangwan",
		Subject:  "3f9b2c1e-0000-4000-8000-000000000001",
	})
	if err != nil {
		t.Fatalf("Seed() error = %v", err)
	}
	if !result.AlreadySeeded || result.IdentityLinkID != linkID ||
		result.PrincipalID != principalID || result.GrantID != grantID {
		t.Fatalf("Seed() = %+v", result)
	}
	if !transaction.committed {
		t.Fatal("transaction was not committed")
	}
	if len(connection.queries) != 2 ||
		!strings.Contains(connection.queries[0], "pg_advisory_lock") ||
		!strings.Contains(connection.queries[1], "pg_advisory_unlock") {
		t.Fatalf("connection queries = %#v", connection.queries)
	}
	if len(connection.keys) != 2 || connection.keys[0] != connection.keys[1] {
		t.Fatalf("lock/unlock keys = %v", connection.keys)
	}
	if !connection.closed {
		t.Fatal("dedicated connection was not closed")
	}
}

// A revoked identity link must fail the seed explicitly: FinishLogin only
// accepts active links, so "already_linked" over a revoked link would report
// a usable administrator that can never log in (codex review 2026-09-19).
func TestAdminSeederRejectsRevokedIdentityLink(t *testing.T) {
	t.Parallel()

	linkID := uuid.New()
	transaction := &fakeAdminSeedTransaction{rows: []fakeAdminSeedRow{
		{values: []any{linkID, uuid.New(), "revoked"}},
	}}
	_, err := (&AdminSeeder{database: fakeAdminSeedDatabase{
		connection: &fakeAdminSeedConnection{transaction: transaction},
	}}).Seed(context.Background(), AdminSeedCommand{
		TenantID: uuid.New(),
		Issuer:   "https://admin.example.com/realms/xiangwan",
		Subject:  "3f9b2c1e-0000-4000-8000-000000000004",
	})
	if !errors.Is(err, ErrAdminSeedLinkRevoked) {
		t.Fatalf("Seed() error = %v, want ErrAdminSeedLinkRevoked", err)
	}
	if transaction.committed {
		t.Fatal("a revoked-link seed must not commit")
	}
}

// Different administrator identities must derive different advisory-lock
// keys, and the same identity must derive the same key on every run.
func TestAdminSeedLockKeyDerivesFromTheExactIdentity(t *testing.T) {
	t.Parallel()

	tenantID := uuid.New()
	issuer := "https://admin.example.com/realms/xiangwan"
	subject := "3f9b2c1e-0000-4000-8000-000000000001"
	if adminSeedLockKey(tenantID, issuer, subject) ==
		adminSeedLockKey(tenantID, issuer, "3f9b2c1e-0000-4000-8000-000000000002") {
		t.Fatal("different subjects derived the same lock key")
	}
	if adminSeedLockKey(tenantID, issuer, subject) ==
		adminSeedLockKey(uuid.New(), issuer, subject) {
		t.Fatal("different tenants derived the same lock key")
	}
	if adminSeedLockKey(tenantID, issuer, subject) ==
		adminSeedLockKey(tenantID, "https://other.example.com/realms/x", subject) {
		t.Fatal("different issuers derived the same lock key")
	}
}

// A fresh identity takes the insert path: Principal, identity link, and the
// super_admin grant are created inside the locked serializable transaction.
func TestAdminSeederProvisionsFreshAdministratorInsideTheLock(t *testing.T) {
	t.Parallel()

	transaction := &fakeAdminSeedTransaction{rows: []fakeAdminSeedRow{
		{err: sql.ErrNoRows}, // no identity link yet
		{err: sql.ErrNoRows}, // no active grant yet
	}}
	connection := &fakeAdminSeedConnection{transaction: transaction}
	result, err := (&AdminSeeder{database: fakeAdminSeedDatabase{
		connection: connection,
	}}).Seed(context.Background(), AdminSeedCommand{
		TenantID: uuid.New(),
		Issuer:   "https://admin.example.com/realms/xiangwan",
		Subject:  "3f9b2c1e-0000-4000-8000-000000000003",
	})
	if err != nil {
		t.Fatalf("Seed() error = %v", err)
	}
	if result.AlreadySeeded || result.PrincipalID == uuid.Nil ||
		result.IdentityLinkID == uuid.Nil || result.GrantID == uuid.Nil {
		t.Fatalf("Seed() = %+v", result)
	}
	joined := strings.Join(transaction.queries, "\n")
	for _, want := range []string{
		"INSERT INTO principals",
		"INSERT INTO xiangwan_admin_identity_links",
		"INSERT INTO xiangwan_admin_grants",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %s in transaction queries: %#v", want, transaction.queries)
		}
	}
	if strings.Contains(joined, "UPDATE") {
		t.Fatalf("fresh seed must not update anything: %#v", transaction.queries)
	}
	if !transaction.committed {
		t.Fatal("transaction was not committed")
	}
}
