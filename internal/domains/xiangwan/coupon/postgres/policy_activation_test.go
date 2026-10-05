package couponpostgres

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon"
	"github.com/google/uuid"
)

type activationProbe struct {
	inserted      bool
	queries       []string
	auditCount    int
	commitCount   int
	rollbackCount int
	readCommitted bool
	recordedAt    time.Time
	document      coupon.SignedGrantPolicy
	actorID       uuid.UUID
}

func (probe *activationProbe) Connect(context.Context) (driver.Conn, error) {
	return &activationConn{probe: probe}, nil
}
func (*activationProbe) Driver() driver.Driver { return activationDriver{} }

type activationDriver struct{}

func (activationDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("use activationProbe connector")
}

type activationConn struct{ probe *activationProbe }

func (*activationConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepared statement")
}
func (*activationConn) Close() error { return nil }
func (*activationConn) Begin() (driver.Tx, error) {
	return nil, errors.New("expected serializable transaction")
}
func (conn *activationConn) BeginTx(_ context.Context, options driver.TxOptions) (driver.Tx, error) {
	conn.probe.readCommitted = options.Isolation == driver.IsolationLevel(sql.LevelReadCommitted)
	return &activationTx{probe: conn.probe}, nil
}
func (conn *activationConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	conn.probe.queries = append(conn.probe.queries, query)
	if strings.Contains(query, "INSERT INTO xiangwan_coupon_grant_policy_versions") {
		if len(args) != 14 || !strings.Contains(query, "ON CONFLICT DO NOTHING") {
			return nil, errors.New("policy insert lost immutable or replay shape")
		}
		if conn.probe.inserted {
			return &activationRows{columns: []string{"recorded_at"}}, nil
		}
		conn.probe.inserted = true
		return &activationRows{columns: []string{"recorded_at"}, values: [][]driver.Value{{conn.probe.recordedAt}}}, nil
	}
	if strings.Contains(query, "FROM xiangwan_coupon_grant_policy_versions") {
		if len(args) != 2 || !strings.Contains(query, "FOR SHARE") {
			return nil, errors.New("policy replay lost exact lock")
		}
		value := conn.probe.document
		return &activationRows{columns: []string{
			"enabled", "face_value_cents", "validity_seconds", "scope_type", "scope_activity_type",
			"scope_series_id", "minimum_order_cents", "evidence_ref", "approved_at",
			"effective_at", "recorded_by", "recorded_at",
		}, values: [][]driver.Value{{
			value.Enabled, *value.FaceValueCents, *value.ValiditySeconds,
			string(*value.ScopeType), string(*value.ScopeActivityType), nil,
			*value.MinimumOrderCents, value.EvidenceRef,
			value.ApprovedAt, value.EffectiveAt, conn.probe.actorID.String(), conn.probe.recordedAt,
		}}}, nil
	}
	return nil, errors.New("unexpected policy query")
}
func (conn *activationConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if !strings.Contains(query, "INSERT INTO xiangwan_admin_audit_events") || len(args) != 9 {
		return nil, errors.New("missing activation audit")
	}
	conn.probe.auditCount++
	return driver.RowsAffected(1), nil
}

type activationTx struct{ probe *activationProbe }

func (tx *activationTx) Commit() error   { tx.probe.commitCount++; return nil }
func (tx *activationTx) Rollback() error { tx.probe.rollbackCount++; return nil }

type activationRows struct {
	columns []string
	values  [][]driver.Value
	index   int
}

func (rows *activationRows) Columns() []string { return rows.columns }
func (*activationRows) Close() error           { return nil }
func (rows *activationRows) Next(dest []driver.Value) error {
	if rows.index >= len(rows.values) {
		return io.EOF
	}
	copy(dest, rows.values[rows.index])
	rows.index++
	return nil
}

func TestSignedPolicyActivationAndLateReplayShareImmutableReceipt(t *testing.T) {
	seed := make([]byte, ed25519.SeedSize)
	privateKey := ed25519.NewKeyFromSeed(seed)
	publicKey := privateKey.Public().(ed25519.PublicKey)
	face, validity, minimum := int64(2000), int64(86400), int64(0)
	scope, activityType := coupon.ScopeTypeActivityType, activity.ActivityTypeAIRoundtable
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	actorID, identityID, tenantID := uuid.New(), uuid.New(), uuid.New()
	document := coupon.SignedGrantPolicy{
		TenantID: tenantID, PolicyVersion: "guest-v1", Enabled: true,
		FaceValueCents: &face, ValiditySeconds: &validity, ScopeType: &scope,
		ScopeActivityType: &activityType, MinimumOrderCents: &minimum,
		EvidenceRef: "customer-approval-001", ApprovedAt: now.Add(-time.Hour),
		EffectiveAt: now.Add(2 * time.Hour),
	}
	payload, _ := json.Marshal(document)
	command := coupon.PolicyActivationCommand{
		ActorID: actorID, IdentityLinkID: identityID,
		Payload: payload, Signature: ed25519.Sign(privateKey, payload),
	}
	probe := &activationProbe{document: document, actorID: actorID, recordedAt: now}
	db := sql.OpenDB(probe)
	defer func() { _ = db.Close() }()
	authorized := 0
	writer, err := NewPolicyActivationWriter(db, tenantID, publicKey,
		func(_ context.Context, _ *sql.Tx, actor, identity uuid.UUID) error {
			authorized++
			if actor != actorID || identity != identityID {
				return errors.New("wrong admin identity")
			}
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	writer.now = func() time.Time { return now }
	first, err := writer.Activate(context.Background(), command)
	if err != nil || first.Duplicate || first.PolicyVersion != "guest-v1" ||
		probe.auditCount != 1 || probe.commitCount != 1 || !probe.readCommitted {
		t.Fatalf("activation = %+v, %v, probe=%+v", first, err, probe)
	}
	writer.now = func() time.Time { return now.Add(3 * time.Hour) }
	replay, err := writer.Activate(context.Background(), command)
	if err != nil || !replay.Duplicate || probe.auditCount != 1 ||
		probe.commitCount != 2 || authorized != 2 {
		t.Fatalf("late replay = %+v, %v, probe=%+v", replay, err, probe)
	}
}
