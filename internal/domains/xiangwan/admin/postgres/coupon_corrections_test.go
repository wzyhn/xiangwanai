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
	"time"

	activitypostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity/postgres"
	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/google/uuid"
)

type couponCorrectionProbe struct {
	tenantID       uuid.UUID
	principalID    uuid.UUID
	identityLinkID uuid.UUID
	allowed        bool
	auditError     error
	steps          []string
	commits        int
	rollbacks      int
	entryID        uuid.UUID
	couponID       uuid.UUID
	checkinEventID uuid.UUID
	orderID        uuid.UUID
	registrationID uuid.UUID
	recordedAt     time.Time
	cutoff         time.Time
}

func (probe *couponCorrectionProbe) Connect(context.Context) (driver.Conn, error) {
	return &couponCorrectionConn{probe: probe}, nil
}

func (*couponCorrectionProbe) Driver() driver.Driver { return couponCorrectionDriver{} }

type couponCorrectionDriver struct{}

func (couponCorrectionDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("use couponCorrectionProbe")
}

type couponCorrectionConn struct{ probe *couponCorrectionProbe }

func (*couponCorrectionConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepared statement")
}

func (*couponCorrectionConn) Close() error { return nil }

func (*couponCorrectionConn) Begin() (driver.Tx, error) {
	return nil, errors.New("expected explicit read transaction")
}

func (conn *couponCorrectionConn) BeginTx(_ context.Context, options driver.TxOptions) (driver.Tx, error) {
	if options.Isolation != driver.IsolationLevel(sql.LevelRepeatableRead) {
		return nil, errors.New("coupon correction read must use a repeatable-read snapshot")
	}
	return &couponCorrectionTx{probe: conn.probe}, nil
}

func (conn *couponCorrectionConn) QueryContext(
	_ context.Context, query string, args []driver.NamedValue,
) (driver.Rows, error) {
	probe := conn.probe
	switch {
	case strings.Contains(query, "FOR SHARE OF principal, identity_link, admin_grant"):
		probe.steps = append(probe.steps, "grant")
		if len(args) != 5 || fmt.Sprint(args[0].Value) != probe.tenantID.String() ||
			fmt.Sprint(args[1].Value) != probe.principalID.String() ||
			fmt.Sprint(args[2].Value) != probe.identityLinkID.String() ||
			args[3].Value != "super_admin" || args[4].Value != nil ||
			!strings.Contains(query, "admin_grant.scope_type = 'tenant'") {
			return nil, errors.New("coupon correction read lost exact live tenant super administrator Grant")
		}
		if !probe.allowed {
			return &couponCorrectionRows{columns: []string{"principal_id", "identity_link_id", "grant_id"}}, nil
		}
		return &couponCorrectionRows{
			columns: []string{"principal_id", "identity_link_id", "grant_id"},
			values:  [][]driver.Value{{probe.principalID.String(), probe.identityLinkID.String(), uuid.NewString()}},
		}, nil
	case strings.Contains(query, "SELECT COUNT(*)") && strings.Contains(query, "FROM xiangwan_coupon_entries"):
		probe.steps = append(probe.steps, "count")
		if len(args) != 2 || fmt.Sprint(args[0].Value) != probe.tenantID.String() ||
			args[1].Value != probe.cutoff || !strings.Contains(query, "entry_type = 'correction_required'") ||
			!strings.Contains(query, "recorded_at < $2") {
			return nil, errors.New("coupon correction count lost tenant or snapshot boundary")
		}
		return &couponCorrectionRows{columns: []string{"count"}, values: [][]driver.Value{{int64(1)}}}, nil
	case strings.Contains(query, "FROM xiangwan_coupon_entries AS correction"):
		probe.steps = append(probe.steps, "list")
		if len(args) != 4 || fmt.Sprint(args[0].Value) != probe.tenantID.String() ||
			args[1].Value != probe.cutoff || args[2].Value != int64(0) || args[3].Value != int64(50) ||
			!strings.Contains(query, "related.coupon_id = correction.coupon_id") ||
			!strings.Contains(query, "ORDER BY correction.recorded_at DESC, correction.id DESC") ||
			!strings.Contains(query, "correction.entry_type = 'correction_required'") {
			return nil, errors.New("coupon correction list lost ledger relation or bounded ordering")
		}
		return &couponCorrectionRows{
			columns: []string{"id", "coupon_id", "face_value_cents", "source_checkin_event_id", "entry_type", "order_id", "registration_id", "recorded_at", "handling_status", "handling_version", "evidence_kind", "adjustment_cents"},
			values: [][]driver.Value{{probe.entryID.String(), probe.couponID.String(), int64(500), probe.checkinEventID.String(),
				"redeemed", probe.orderID.String(), probe.registrationID.String(), probe.recordedAt,
				"pending", int64(0), "", int64(0)}},
		}, nil
	default:
		return nil, fmt.Errorf("unexpected coupon correction query: %s", query)
	}
}

func (conn *couponCorrectionConn) ExecContext(
	_ context.Context, query string, args []driver.NamedValue,
) (driver.Result, error) {
	probe := conn.probe
	probe.steps = append(probe.steps, "audit")
	if !strings.Contains(query, "'coupon.correction_list_read'") ||
		!strings.Contains(query, "INSERT INTO xiangwan_admin_audit_events") ||
		len(args) != 10 || fmt.Sprint(args[1].Value) != probe.tenantID.String() ||
		fmt.Sprint(args[2].Value) != probe.principalID.String() ||
		args[4].Value != probe.identityLinkID.String() ||
		strings.Contains(query, "reason") || strings.Contains(query, "grant_business_key") {
		return nil, errors.New("coupon correction read audit contains unsafe data or wrong actor")
	}
	if probe.auditError != nil {
		return nil, probe.auditError
	}
	return driver.RowsAffected(1), nil
}

type couponCorrectionTx struct{ probe *couponCorrectionProbe }

func (tx *couponCorrectionTx) Commit() error {
	tx.probe.steps = append(tx.probe.steps, "commit")
	tx.probe.commits++
	return nil
}

func (tx *couponCorrectionTx) Rollback() error {
	tx.probe.rollbacks++
	return nil
}

type couponCorrectionRows struct {
	columns []string
	values  [][]driver.Value
	next    int
}

func (rows *couponCorrectionRows) Columns() []string { return rows.columns }
func (*couponCorrectionRows) Close() error           { return nil }

func (rows *couponCorrectionRows) Next(dest []driver.Value) error {
	if rows.next >= len(rows.values) {
		return io.EOF
	}
	copy(dest, rows.values[rows.next])
	rows.next++
	return nil
}

func newCouponCorrectionTestCatalog(t *testing.T, allowed bool) (*Catalog, xiangwanadmin.Principal, *couponCorrectionProbe) {
	t.Helper()
	probe := &couponCorrectionProbe{
		tenantID: uuid.New(), principalID: uuid.New(), identityLinkID: uuid.New(), allowed: allowed,
		entryID: uuid.New(), couponID: uuid.New(), checkinEventID: uuid.New(),
		orderID: uuid.New(), registrationID: uuid.New(),
		recordedAt: time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC),
		cutoff:     time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC),
	}
	authorizer, err := NewGrantAuthorizer(probe.tenantID)
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(probe)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return &Catalog{
			db: db, tenantID: probe.tenantID, generationID: uuid.New(),
			authorizer: authorizer, publisher: &activitypostgres.Publisher{},
			now: func() time.Time { return probe.cutoff },
		}, xiangwanadmin.Principal{
			PrincipalID: probe.principalID, IdentityLinkID: probe.identityLinkID,
		}, probe
}

func TestCouponCorrectionListRequiresSuperAdminBeforeLedgerRead(t *testing.T) {
	catalog, principal, probe := newCouponCorrectionTestCatalog(t, false)
	_, err := catalog.ListCouponCorrections(context.Background(), principal,
		xiangwanadmin.CouponCorrectionFilter{Page: 1, PageSize: 50})
	if !errors.Is(err, xiangwanadmin.ErrScopeForbidden) ||
		strings.Join(probe.steps, ",") != "grant" || probe.commits != 0 || probe.rollbacks != 1 {
		t.Fatalf("denied correction read = %v, steps=%v, commits=%d, rollbacks=%d",
			err, probe.steps, probe.commits, probe.rollbacks)
	}
}

func TestCouponCorrectionListAuditsSnapshotBeforeReturning(t *testing.T) {
	catalog, principal, probe := newCouponCorrectionTestCatalog(t, true)
	page, err := catalog.ListCouponCorrections(context.Background(), principal,
		xiangwanadmin.CouponCorrectionFilter{Page: 1, PageSize: 50})
	if err != nil || page.Total != 1 || len(page.Items) != 1 ||
		page.Items[0].EntryID != probe.entryID || page.Items[0].CouponID != probe.couponID ||
		page.Items[0].OrderID == nil || *page.Items[0].OrderID != probe.orderID ||
		page.Items[0].RegistrationID == nil || *page.Items[0].RegistrationID != probe.registrationID ||
		!page.AsOf.Equal(probe.cutoff) || strings.Join(probe.steps, ",") != "grant,count,list,audit,commit" {
		t.Fatalf("correction page = %+v, err=%v, steps=%v", page, err, probe.steps)
	}
	if probe.commits != 1 || probe.rollbacks != 0 {
		t.Fatalf("correction transaction commits=%d rollbacks=%d", probe.commits, probe.rollbacks)
	}

	broken, actor, failedProbe := newCouponCorrectionTestCatalog(t, true)
	failedProbe.auditError = errors.New("audit unavailable")
	_, err = broken.ListCouponCorrections(context.Background(), actor,
		xiangwanadmin.CouponCorrectionFilter{Page: 1, PageSize: 50})
	if !errors.Is(err, failedProbe.auditError) || failedProbe.commits != 0 ||
		failedProbe.rollbacks != 1 || strings.Join(failedProbe.steps, ",") != "grant,count,list,audit" {
		t.Fatalf("failed correction audit = %v, steps=%v, commits=%d rollbacks=%d",
			err, failedProbe.steps, failedProbe.commits, failedProbe.rollbacks)
	}
}
