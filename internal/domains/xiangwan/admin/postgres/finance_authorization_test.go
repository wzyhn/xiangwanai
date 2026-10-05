package postgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/google/uuid"
)

type financeGrantConnector struct {
	financeAllowed  bool
	activityAllowed bool
	activityError   error
	capabilities    []string
	beginCount      int
	rollbackCount   int
	tenantID        uuid.UUID
	principalID     uuid.UUID
	identityLinkID  uuid.UUID
}

func (connector *financeGrantConnector) Connect(context.Context) (driver.Conn, error) {
	return &financeGrantConn{connector: connector}, nil
}

func (connector *financeGrantConnector) Driver() driver.Driver { return financeGrantDriver{} }

type financeGrantDriver struct{}

func (financeGrantDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("use financeGrantConnector")
}

type financeGrantConn struct{ connector *financeGrantConnector }

func (*financeGrantConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepared statement")
}

func (*financeGrantConn) Close() error { return nil }

func (*financeGrantConn) Begin() (driver.Tx, error) {
	return nil, errors.New("expected explicit transaction options")
}

func (conn *financeGrantConn) BeginTx(_ context.Context, options driver.TxOptions) (driver.Tx, error) {
	if options.Isolation != driver.IsolationLevel(sql.LevelRepeatableRead) {
		return nil, errors.New("finance read must use one repeatable-read snapshot")
	}
	conn.connector.beginCount++
	return &financeGrantTx{connector: conn.connector}, nil
}

func (conn *financeGrantConn) QueryContext(
	_ context.Context, query string, args []driver.NamedValue,
) (driver.Rows, error) {
	if !strings.Contains(query, "FOR SHARE OF principal, identity_link, admin_grant") ||
		!strings.Contains(query, "admin_grant.scope_type = 'tenant'") || len(args) != 5 {
		return nil, errors.New("grant query lost its live identity or scope lock")
	}
	capability, ok := args[3].Value.(string)
	if !ok || (capability != "activity_operator" && capability != "finance") {
		return nil, errors.New("unexpected grant capability")
	}
	if fmt.Sprint(args[0].Value) != conn.connector.tenantID.String() ||
		fmt.Sprint(args[1].Value) != conn.connector.principalID.String() ||
		fmt.Sprint(args[2].Value) != conn.connector.identityLinkID.String() ||
		args[4].Value != nil {
		return nil, errors.New("grant query must bind an exact identity and tenant scope")
	}
	conn.connector.capabilities = append(conn.connector.capabilities, capability)
	if capability == "activity_operator" && conn.connector.activityError != nil {
		return nil, conn.connector.activityError
	}
	allowed := capability == "finance" && conn.connector.financeAllowed ||
		capability == "activity_operator" && conn.connector.activityAllowed
	return &financeGrantRows{
		allowed:     allowed,
		principalID: conn.connector.principalID, identityLinkID: conn.connector.identityLinkID,
	}, nil
}

type financeGrantTx struct{ connector *financeGrantConnector }

func (*financeGrantTx) Commit() error { return nil }

func (tx *financeGrantTx) Rollback() error {
	tx.connector.rollbackCount++
	return nil
}

type financeGrantRows struct {
	allowed        bool
	read           bool
	principalID    uuid.UUID
	identityLinkID uuid.UUID
}

func (*financeGrantRows) Columns() []string {
	return []string{"principal_id", "identity_link_id", "grant_id"}
}

func (*financeGrantRows) Close() error { return nil }

func (rows *financeGrantRows) Next(dest []driver.Value) error {
	if !rows.allowed || rows.read {
		return io.EOF
	}
	rows.read = true
	dest[0] = rows.principalID.String()
	dest[1] = rows.identityLinkID.String()
	dest[2] = uuid.NewString()
	return nil
}

func financeReadCatalog(t *testing.T, fixture *financeGrantConnector) (*Catalog, xiangwanadmin.Principal) {
	t.Helper()
	tenantID := uuid.New()
	fixture.tenantID = tenantID
	fixture.principalID = uuid.New()
	fixture.identityLinkID = uuid.New()
	authorizer, err := NewGrantAuthorizer(tenantID)
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(fixture)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return &Catalog{db: db, tenantID: tenantID, authorizer: authorizer},
		xiangwanadmin.Principal{
			PrincipalID: fixture.principalID, IdentityLinkID: fixture.identityLinkID,
		}
}

func TestFinanceReadRequiresLiveFinanceGrant(t *testing.T) {
	fixture := &financeGrantConnector{financeAllowed: true}
	catalog, principal := financeReadCatalog(t, fixture)
	tx, err := catalog.beginAuthorizedFinanceRead(context.Background(), principal)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if len(fixture.capabilities) != 1 || fixture.capabilities[0] != "finance" {
		t.Fatalf("finance authorization checked %v", fixture.capabilities)
	}

	denied := &financeGrantConnector{activityAllowed: true}
	catalog, principal = financeReadCatalog(t, denied)
	_, err = catalog.beginAuthorizedFinanceRead(context.Background(), principal)
	if !errors.Is(err, xiangwanadmin.ErrScopeForbidden) ||
		denied.beginCount != 1 || denied.rollbackCount != 1 {
		t.Fatalf("activity-only refund read = %v, begins=%d rollbacks=%d", err,
			denied.beginCount, denied.rollbackCount)
	}
}

func TestOrderReadFallsBackOnlyForScopeDenial(t *testing.T) {
	operator := &financeGrantConnector{activityAllowed: true}
	catalog, principal := financeReadCatalog(t, operator)
	tx, err := catalog.beginAuthorizedOrderRead(context.Background(), principal)
	if err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback()
	if strings.Join(operator.capabilities, ",") != "activity_operator" ||
		operator.beginCount != 1 {
		t.Fatalf("activity operator Order authorization = %v, begins=%d",
			operator.capabilities, operator.beginCount)
	}

	fixture := &financeGrantConnector{financeAllowed: true}
	catalog, principal = financeReadCatalog(t, fixture)
	tx, err = catalog.beginAuthorizedOrderRead(context.Background(), principal)
	if err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback()
	if strings.Join(fixture.capabilities, ",") != "activity_operator,finance" ||
		fixture.beginCount != 2 || fixture.rollbackCount != 2 {
		t.Fatalf("finance-only Order authorization = %v, begins=%d rollbacks=%d",
			fixture.capabilities, fixture.beginCount, fixture.rollbackCount)
	}

	dbFailure := errors.New("grant database unavailable")
	broken := &financeGrantConnector{financeAllowed: true, activityError: dbFailure}
	catalog, principal = financeReadCatalog(t, broken)
	_, err = catalog.beginAuthorizedOrderRead(context.Background(), principal)
	if !errors.Is(err, dbFailure) || strings.Join(broken.capabilities, ",") != "activity_operator" ||
		broken.beginCount != 1 || broken.rollbackCount != 1 {
		t.Fatalf("database failure fallback = %v, capabilities=%v, begins=%d rollbacks=%d",
			err, broken.capabilities, broken.beginCount, broken.rollbackCount)
	}
}
