package couponpostgres

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

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon"
	"github.com/google/uuid"
)

type grantPolicyProbe struct {
	rows  [][]driver.Value
	err   error
	query string
	args  []driver.NamedValue
}

func (probe *grantPolicyProbe) Connect(context.Context) (driver.Conn, error) {
	return &grantPolicyConn{probe: probe}, nil
}

func (*grantPolicyProbe) Driver() driver.Driver { return grantPolicyDriver{} }

type grantPolicyDriver struct{}

func (grantPolicyDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("use grantPolicyProbe connector")
}

type grantPolicyConn struct{ probe *grantPolicyProbe }

func (*grantPolicyConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepared statement")
}

func (*grantPolicyConn) Close() error { return nil }

func (*grantPolicyConn) Begin() (driver.Tx, error) {
	return nil, errors.New("unexpected transaction")
}

func (conn *grantPolicyConn) QueryContext(
	_ context.Context, query string, args []driver.NamedValue,
) (driver.Rows, error) {
	conn.probe.query, conn.probe.args = query, args
	if conn.probe.err != nil {
		return nil, conn.probe.err
	}
	return &grantPolicyRows{values: conn.probe.rows}, nil
}

type grantPolicyRows struct {
	values [][]driver.Value
	index  int
}

func (*grantPolicyRows) Columns() []string {
	return []string{"policy_version", "enabled", "face_value_cents", "validity_seconds",
		"scope_type", "scope_activity_type", "scope_series_id", "minimum_order_cents"}
}

func (*grantPolicyRows) Close() error { return nil }

func (rows *grantPolicyRows) Next(destination []driver.Value) error {
	if rows.index >= len(rows.values) {
		return io.EOF
	}
	copy(destination, rows.values[rows.index])
	rows.index++
	return nil
}

func TestPostgresGrantPolicyProviderReadsLatestTenantPolicy(t *testing.T) {
	t.Parallel()
	tenantID := uuid.New()
	at := time.Date(2026, 10, 1, 9, 30, 0, 0, time.UTC)
	probe := &grantPolicyProbe{rows: [][]driver.Value{{
		"customer-coupon-v3", true, int64(5000), int64(86400 * 90),
		"activity_type", "ai_roundtable", nil, int64(10000),
	}}}
	database := sql.OpenDB(probe)
	defer func() { _ = database.Close() }()
	policy, err := (PostgresGrantPolicyProvider{}).CouponGrantPolicyAt(
		context.Background(), database, tenantID, at)
	if err != nil || !policy.Configured || policy.PolicyVersion != "customer-coupon-v3" ||
		policy.FaceValueCents != 5000 || policy.Validity != 90*24*time.Hour ||
		policy.ScopeType != coupon.ScopeTypeActivityType ||
		policy.ScopeActivityType == nil || *policy.ScopeActivityType != activity.ActivityTypeAIRoundtable ||
		policy.MinimumOrderCents != 10000 {
		t.Fatalf("policy = %+v, %v", policy, err)
	}
	if len(probe.args) != 2 || fmt.Sprint(probe.args[0].Value) != tenantID.String() ||
		probe.args[1].Value != at ||
		!strings.Contains(probe.query, "tenant_id = $1") ||
		!strings.Contains(probe.query, "effective_at <= $2") ||
		!strings.Contains(probe.query, "ORDER BY effective_at DESC") ||
		!strings.Contains(probe.query, "LIMIT 1") ||
		!strings.Contains(probe.query, "FOR SHARE") {
		t.Fatalf("policy read lost tenant/as-of/lock boundary: %q args=%+v", probe.query, probe.args)
	}
}

func TestPostgresGrantPolicyProviderReadsSeriesScope(t *testing.T) {
	t.Parallel()
	seriesID := uuid.New()
	probe := &grantPolicyProbe{rows: [][]driver.Value{{
		"customer-series-v1", true, int64(2500), int64(86400),
		"series", nil, seriesID.String(), int64(0),
	}}}
	database := sql.OpenDB(probe)
	defer func() { _ = database.Close() }()
	policy, err := (PostgresGrantPolicyProvider{}).CouponGrantPolicyAt(
		context.Background(), database, uuid.New(), time.Now())
	if err != nil || policy.ScopeType != coupon.ScopeTypeSeries ||
		policy.ScopeSeriesID == nil || *policy.ScopeSeriesID != seriesID ||
		policy.ScopeActivityType != nil {
		t.Fatalf("series policy = %+v, %v", policy, err)
	}
}

func TestPostgresGrantPolicyProviderFailsClosedWithoutApprovedActiveValues(t *testing.T) {
	t.Parallel()
	valid := []driver.Value{
		"customer-coupon-v3", true, int64(5000), int64(86400),
		"activity_type", "ai_roundtable", nil, int64(5000),
	}
	tests := []struct {
		name string
		rows [][]driver.Value
	}{
		{name: "no policy"},
		{name: "latest policy disables issuance", rows: [][]driver.Value{{
			"customer-coupon-off-v4", false, nil, nil, nil, nil, nil, nil,
		}}},
		{name: "invalid scope", rows: [][]driver.Value{func() []driver.Value {
			row := append([]driver.Value(nil), valid...)
			row[5] = "all"
			return row
		}()}},
		{name: "validity exceeds signed limit", rows: [][]driver.Value{func() []driver.Value {
			row := append([]driver.Value(nil), valid...)
			row[3] = int64(coupon.MaxPolicyValidity/time.Second) + 1
			return row
		}()}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			probe := &grantPolicyProbe{rows: test.rows}
			database := sql.OpenDB(probe)
			defer func() { _ = database.Close() }()
			_, err := (PostgresGrantPolicyProvider{}).CouponGrantPolicyAt(
				context.Background(), database, uuid.New(), time.Now())
			if !errors.Is(err, ErrGrantPolicyUnavailable) {
				t.Fatalf("policy error = %v", err)
			}
		})
	}
}

func TestPostgresGrantPolicyProviderSurfacesDatabaseFailure(t *testing.T) {
	t.Parallel()
	connectionLost := errors.New("connection lost")
	probe := &grantPolicyProbe{err: connectionLost}
	database := sql.OpenDB(probe)
	defer func() { _ = database.Close() }()
	_, err := (PostgresGrantPolicyProvider{}).CouponGrantPolicyAt(
		context.Background(), database, uuid.New(), time.Now())
	if !errors.Is(err, connectionLost) {
		t.Fatalf("database failure = %v", err)
	}
}
