package standalonepg

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

	"github.com/google/uuid"
)

type reviewCleanupProbe struct {
	tenantID     uuid.UUID
	generationID uuid.UUID
	fileID       uuid.UUID
	lease        time.Time
	retry        int64
	missing      bool
	affected     int64
	steps        []string
}

func (probe *reviewCleanupProbe) Connect(context.Context) (driver.Conn, error) {
	return &reviewCleanupConn{probe: probe}, nil
}

func (*reviewCleanupProbe) Driver() driver.Driver { return reviewCleanupDriver{} }

type reviewCleanupDriver struct{}

func (reviewCleanupDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("use reviewCleanupProbe")
}

type reviewCleanupConn struct{ probe *reviewCleanupProbe }

func (*reviewCleanupConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepared statement")
}
func (*reviewCleanupConn) Close() error { return nil }
func (*reviewCleanupConn) Begin() (driver.Tx, error) {
	return nil, errors.New("expected explicit transaction")
}

func (conn *reviewCleanupConn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	conn.probe.steps = append(conn.probe.steps, "begin")
	return &reviewCleanupTx{probe: conn.probe}, nil
}

type reviewCleanupTx struct{ probe *reviewCleanupProbe }

func (tx *reviewCleanupTx) Commit() error {
	tx.probe.steps = append(tx.probe.steps, "commit")
	return nil
}

func (tx *reviewCleanupTx) Rollback() error {
	tx.probe.steps = append(tx.probe.steps, "rollback")
	return nil
}

func (conn *reviewCleanupConn) QueryContext(
	_ context.Context, query string, args []driver.NamedValue,
) (driver.Rows, error) {
	probe := conn.probe
	if strings.Contains(query, "FROM xiangwan_runtime_generations AS generation") {
		probe.steps = append(probe.steps, "generation")
		if len(args) != 1 || fmt.Sprint(args[0].Value) != probe.tenantID.String() ||
			!strings.Contains(query, "FOR SHARE OF generation") ||
			!strings.Contains(query, "tenant.metadata @>") {
			return nil, errors.New("review File cleanup lost trusted generation lock")
		}
		return &reviewCleanupRows{
			columns: []string{"active_generation_id"},
			values:  [][]driver.Value{{probe.generationID.String()}},
		}, nil
	}
	probe.steps = append(probe.steps, "claim")
	if len(args) != 2 || fmt.Sprint(args[0].Value) != probe.tenantID.String() ||
		args[1].Value != int64(reviewCleanupMaxRetries) ||
		!strings.Contains(query, "intent.tenant_id = $1") ||
		!strings.Contains(query, "file.file_key = 'xiangwan-review/' || file.id::text") ||
		!strings.Contains(query, "FOR UPDATE OF file SKIP LOCKED") ||
		!strings.Contains(query, "file.delete_after + file.retry_count * INTERVAL '1 hour' < clock_timestamp()") ||
		!strings.Contains(query, "file.deleting_at < clock_timestamp() - INTERVAL '10 minutes'") ||
		!strings.Contains(query, "UPDATE files AS file") ||
		!strings.Contains(query, "SET status = 'deleting', deleting_at = clock_timestamp()") {
		return nil, errors.New("review File claim lost tenant, expiration, key, or row lock")
	}
	rows := &reviewCleanupRows{}
	if !probe.missing {
		rows.values = [][]driver.Value{{probe.fileID.String(), probe.lease, probe.retry}}
	}
	return rows, nil
}

func (conn *reviewCleanupConn) ExecContext(
	_ context.Context, query string, args []driver.NamedValue,
) (driver.Result, error) {
	probe := conn.probe
	if len(args) < 3 || fmt.Sprint(args[0].Value) != probe.fileID.String() ||
		args[1].Value != probe.lease ||
		fmt.Sprint(args[2].Value) != probe.tenantID.String() ||
		!strings.Contains(query, "file.deleting_at = $2") ||
		!strings.Contains(query, "intent.tenant_id = $3") ||
		!strings.Contains(query, "file.file_key = 'xiangwan-review/' || file.id::text") {
		return nil, errors.New("review File completion lost exact tenant, key, or lease")
	}
	if strings.Contains(query, "status = 'expired'") {
		probe.steps = append(probe.steps, "expired")
	} else if len(args) == 5 && args[3].Value == int64(reviewCleanupMaxRetries) &&
		args[4].Value == "provider_delete_failed" &&
		strings.Contains(query, "THEN 'cleanup_failed' ELSE 'active'") &&
		strings.Contains(query, "retry_count = file.retry_count + 1") {
		probe.steps = append(probe.steps, "failure")
	} else {
		return nil, errors.New("unexpected review File cleanup completion")
	}
	return driver.RowsAffected(probe.affected), nil
}

type reviewCleanupRows struct {
	columns []string
	values  [][]driver.Value
	next    int
}

func (rows *reviewCleanupRows) Columns() []string {
	if rows.columns != nil {
		return rows.columns
	}
	return []string{"id", "deleting_at", "retry_count"}
}
func (*reviewCleanupRows) Close() error { return nil }
func (rows *reviewCleanupRows) Next(dest []driver.Value) error {
	if rows.next >= len(rows.values) {
		return io.EOF
	}
	copy(dest, rows.values[rows.next])
	rows.next++
	return nil
}

func TestReviewCleanupClaimsAndFencesExactTenantFile(t *testing.T) {
	probe := &reviewCleanupProbe{
		tenantID: uuid.New(), generationID: uuid.New(), fileID: uuid.New(),
		lease:    time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC),
		affected: 1,
	}
	db := sql.OpenDB(probe)
	defer func() { _ = db.Close() }()
	repo, err := NewReviewCleanupRepository(db, probe.tenantID, probe.generationID)
	if err != nil {
		t.Fatal(err)
	}
	claim, found, err := repo.ClaimNext(context.Background())
	if err != nil || !found || claim.FileID != probe.fileID || claim.Lease != probe.lease {
		t.Fatalf("ClaimNext() = %+v, %t, %v", claim, found, err)
	}
	if err := repo.MarkExpired(context.Background(), claim); err != nil {
		t.Fatalf("MarkExpired() = %v", err)
	}
	if err := repo.RecordFailure(context.Background(), claim, "provider_delete_failed"); err != nil {
		t.Fatalf("RecordFailure() = %v", err)
	}
	if fmt.Sprint(probe.steps) != "[begin generation claim commit expired failure]" {
		t.Fatalf("cleanup steps = %v", probe.steps)
	}
	probe.affected = 0
	if err := repo.MarkExpired(context.Background(), claim); !errors.Is(err, ErrReviewCleanupLeaseLost) {
		t.Fatalf("stale lease completion = %v", err)
	}
}

func TestReviewCleanupFailsClosedForMissingAndInvalidClaims(t *testing.T) {
	if _, err := NewReviewCleanupRepository(nil, uuid.New(), uuid.New()); !errors.Is(err, ErrInvalidReviewCleanup) {
		t.Fatalf("nil database = %v", err)
	}
	probe := &reviewCleanupProbe{tenantID: uuid.New(), generationID: uuid.New(), missing: true}
	db := sql.OpenDB(probe)
	defer func() { _ = db.Close() }()
	repo, err := NewReviewCleanupRepository(db, probe.tenantID, probe.generationID)
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := repo.ClaimNext(context.Background()); err != nil || found {
		t.Fatalf("missing claim = %t, %v", found, err)
	}
	staleRepo, err := NewReviewCleanupRepository(db, probe.tenantID, uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := staleRepo.ClaimNext(context.Background()); found || !errors.Is(err, ErrReviewCleanupGenerationInactive) {
		t.Fatalf("stale generation claim = %t, %v", found, err)
	}
	if err := repo.MarkExpired(context.Background(), ReviewCleanupClaim{}); !errors.Is(err, ErrInvalidReviewCleanup) {
		t.Fatalf("empty claim = %v", err)
	}
	if err := repo.RecordFailure(context.Background(), ReviewCleanupClaim{}, "arbitrary path"); !errors.Is(err, ErrInvalidReviewCleanup) {
		t.Fatalf("unsafe failure detail = %v", err)
	}
}
