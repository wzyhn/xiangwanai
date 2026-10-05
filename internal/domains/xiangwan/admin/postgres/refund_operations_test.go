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

type refundWriteProbe struct {
	activeGeneration  bool
	allowedCapability string
	tenantID          uuid.UUID
	generationID      uuid.UUID
	actorID           uuid.UUID
	identityLinkID    uuid.UUID
	beginCount        int
	rollbackCount     int
	caseLookups       int
	capabilities      []string
}

func (probe *refundWriteProbe) Connect(context.Context) (driver.Conn, error) {
	return &refundWriteProbeConn{probe: probe}, nil
}
func (*refundWriteProbe) Driver() driver.Driver { return refundWriteProbeDriver{} }

type refundWriteProbeDriver struct{}

func (refundWriteProbeDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("use refundWriteProbe connector")
}

type refundWriteProbeConn struct{ probe *refundWriteProbe }

func (*refundWriteProbeConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepared statement")
}
func (*refundWriteProbeConn) Close() error { return nil }
func (*refundWriteProbeConn) Begin() (driver.Tx, error) {
	return nil, errors.New("expected explicit transaction")
}
func (conn *refundWriteProbeConn) BeginTx(_ context.Context, options driver.TxOptions) (driver.Tx, error) {
	if options.Isolation != driver.IsolationLevel(sql.LevelSerializable) {
		return nil, errors.New("refund write must be serializable")
	}
	conn.probe.beginCount++
	return &refundWriteProbeTx{probe: conn.probe}, nil
}
func (conn *refundWriteProbeConn) QueryContext(
	_ context.Context, query string, args []driver.NamedValue,
) (driver.Rows, error) {
	probe := conn.probe
	switch {
	case strings.Contains(query, "FROM xiangwan_runtime_generations"):
		if len(args) != 2 || fmt.Sprint(args[0].Value) != probe.tenantID.String() ||
			fmt.Sprint(args[1].Value) != probe.generationID.String() || !strings.Contains(query, "FOR SHARE") {
			return nil, errors.New("refund generation lock lost its exact scope")
		}
		if !probe.activeGeneration {
			return &refundWriteProbeRows{}, nil
		}
		return &refundWriteProbeRows{values: []driver.Value{int64(1)}}, nil
	case strings.Contains(query, "FOR SHARE OF principal, identity_link, admin_grant"):
		if len(args) != 5 || fmt.Sprint(args[0].Value) != probe.tenantID.String() ||
			fmt.Sprint(args[1].Value) != probe.actorID.String() ||
			fmt.Sprint(args[2].Value) != probe.identityLinkID.String() || args[4].Value != nil {
			return nil, errors.New("refund grant check lost exact active identity or tenant scope")
		}
		capability, ok := args[3].Value.(string)
		if !ok {
			return nil, errors.New("refund grant capability missing")
		}
		probe.capabilities = append(probe.capabilities, capability)
		if capability != probe.allowedCapability {
			return &refundWriteProbeRows{}, nil
		}
		return &refundWriteProbeRows{values: []driver.Value{
			probe.actorID.String(), probe.identityLinkID.String(), uuid.NewString(),
		}}, nil
	case strings.Contains(query, "FROM xiangwan_refund_cases"):
		probe.caseLookups++
		return &refundWriteProbeRows{}, nil
	default:
		return nil, errors.New("unexpected refund write SQL")
	}
}

type refundWriteProbeTx struct{ probe *refundWriteProbe }

func (*refundWriteProbeTx) Commit() error { return nil }
func (tx *refundWriteProbeTx) Rollback() error {
	tx.probe.rollbackCount++
	return nil
}

type refundWriteProbeRows struct {
	values []driver.Value
	read   bool
}

func (rows *refundWriteProbeRows) Columns() []string {
	columns := make([]string, len(rows.values))
	for index := range columns {
		columns[index] = "value"
	}
	return columns
}
func (*refundWriteProbeRows) Close() error { return nil }
func (rows *refundWriteProbeRows) Next(dest []driver.Value) error {
	if rows.read || rows.values == nil {
		return io.EOF
	}
	rows.read = true
	copy(dest, rows.values)
	return nil
}

func TestRefundOperatorChecksLiveGenerationAndActionGrantBeforeCaseLookup(t *testing.T) {
	tenantID, generationID := uuid.New(), uuid.New()
	probe := &refundWriteProbe{
		activeGeneration: true, allowedCapability: "finance",
		tenantID: tenantID, generationID: generationID,
		actorID: uuid.New(), identityLinkID: uuid.New(),
	}
	db := sql.OpenDB(probe)
	db.SetMaxOpenConns(1)
	defer db.Close()
	authorizer, err := NewGrantAuthorizer(tenantID)
	if err != nil {
		t.Fatal(err)
	}
	operator, err := NewRefundOperator(db, tenantID, generationID, authorizer, nil)
	if err != nil {
		t.Fatal(err)
	}
	command := xiangwanadmin.RefundActionCommand{
		ActorID: probe.actorID, IdentityLinkID: probe.identityLinkID,
		OperationID: uuid.New(), CaseID: uuid.New(), ExpectedVersion: 1,
		Action:                xiangwanadmin.RefundActionComplete,
		SuccessfulRefundCents: 100, ExternalRefundID: "official-1",
		EvidenceReference: "merchant-refund-1",
	}
	_, err = operator.TransitionRefund(context.Background(), command)
	if !errors.Is(err, xiangwanadmin.ErrScopeForbidden) ||
		probe.caseLookups != 0 || probe.rollbackCount != 1 ||
		strings.Join(probe.capabilities, ",") != "super_admin" {
		t.Fatalf("finance-only completion: err=%v probe=%+v", err, probe)
	}
	command.Action = xiangwanadmin.RefundActionStart
	command.SuccessfulRefundCents = 0
	command.ExternalRefundID = ""
	command.EvidenceReference = ""
	_, err = operator.TransitionRefund(context.Background(), command)
	if !errors.Is(err, xiangwanadmin.ErrTargetNotFound) ||
		probe.caseLookups != 1 || probe.rollbackCount != 2 ||
		strings.Join(probe.capabilities, ",") != "super_admin,finance" {
		t.Fatalf("finance start: err=%v probe=%+v", err, probe)
	}
	probe.allowedCapability = "super_admin"
	command.Action = xiangwanadmin.RefundActionComplete
	command.SuccessfulRefundCents = 100
	command.ExternalRefundID = "official-1"
	command.EvidenceReference = "merchant-refund-1"
	_, err = operator.TransitionRefund(context.Background(), command)
	if !errors.Is(err, xiangwanadmin.ErrTargetNotFound) ||
		probe.caseLookups != 2 || probe.rollbackCount != 3 ||
		strings.Join(probe.capabilities, ",") != "super_admin,finance,super_admin" {
		t.Fatalf("super administrator completion scope: err=%v probe=%+v", err, probe)
	}
	probe.activeGeneration = false
	_, err = operator.TransitionRefund(context.Background(), command)
	if !errors.Is(err, xiangwanadmin.ErrVersionConflict) ||
		probe.caseLookups != 2 || probe.rollbackCount != 4 {
		t.Fatalf("stale generation: err=%v probe=%+v", err, probe)
	}
}
