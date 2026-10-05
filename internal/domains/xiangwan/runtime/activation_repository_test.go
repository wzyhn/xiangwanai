package xiangwanruntime

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestGenerationActivatorActivatesAndCommits(t *testing.T) {
	t.Parallel()

	tenantID := uuid.New()
	generationID := uuid.New()
	now := time.Now().UTC()
	transaction := &fakeActivationTransaction{
		rows: []generationRowScanner{
			fakeActivationRow{values: []any{
				uuid.NullUUID{}, int64(0), sql.NullTime{Time: now, Valid: true},
				uuid.NullUUID{}, sql.NullTime{}, sql.NullString{}, sql.NullString{},
				"deployment_role",
			}},
			fakeActivationRow{values: []any{false}},
			fakeActivationRow{values: []any{
				tenantID, generationID, uuid.NullUUID{}, int64(1), now,
				"deployment_role", "release",
			}},
		},
	}
	activator := &GenerationActivator{database: &fakeActivationDatabase{
		transaction: transaction,
	}}
	activation, err := activator.Activate(context.Background(), ActivationCommand{
		TenantID:           tenantID,
		GenerationID:       generationID,
		ExpectedWriteEpoch: 0,
		Reason:             "release",
	})
	if err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	if activation.GenerationID != generationID || activation.WriteEpoch != 1 ||
		activation.ActivatedBy != "deployment_role" || activation.Replayed {
		t.Fatalf("Activate() = %+v", activation)
	}
	if !transaction.committed || len(transaction.queries) != 3 {
		t.Fatalf("transaction committed=%v queries=%d", transaction.committed, len(transaction.queries))
	}
	if !strings.Contains(transaction.queries[0], "FOR UPDATE") ||
		!strings.Contains(transaction.queries[0], "current_user") ||
		!strings.Contains(transaction.queries[2], "write_epoch = write_epoch + 1") {
		t.Fatalf("unexpected activation queries: %#v", transaction.queries)
	}
}

func TestGenerationActivatorReplaysExactCommittedGeneration(t *testing.T) {
	t.Parallel()

	tenantID := uuid.New()
	generationID := uuid.New()
	previousGenerationID := uuid.New()
	now := time.Now().UTC()
	transaction := &fakeActivationTransaction{rows: []generationRowScanner{
		fakeActivationRow{values: []any{
			uuid.NullUUID{UUID: generationID, Valid: true},
			int64(8),
			sql.NullTime{Time: now.Add(-time.Hour), Valid: true},
			uuid.NullUUID{UUID: previousGenerationID, Valid: true},
			sql.NullTime{Time: now, Valid: true},
			sql.NullString{String: "deployment_role", Valid: true},
			sql.NullString{String: "release", Valid: true},
			"deployment_role",
		}},
	}}
	activation, err := (&GenerationActivator{database: &fakeActivationDatabase{
		transaction: transaction,
	}}).Activate(context.Background(), ActivationCommand{
		TenantID:           tenantID,
		GenerationID:       generationID,
		ExpectedWriteEpoch: 7,
		Reason:             "release",
	})
	if err != nil || !activation.Replayed || activation.WriteEpoch != 8 {
		t.Fatalf("Activate(replay) = %+v, %v", activation, err)
	}
	if !transaction.committed || len(transaction.queries) != 1 {
		t.Fatalf("replay committed=%v queries=%d", transaction.committed, len(transaction.queries))
	}
}

func TestGenerationActivatorFailsClosedBeforeUpdate(t *testing.T) {
	t.Parallel()

	tenantID := uuid.New()
	generationID := uuid.New()
	now := time.Now().UTC()
	tests := []struct {
		name string
		row  generationRowScanner
		want error
	}{
		{name: "authority unavailable", row: fakeActivationRow{err: sql.ErrNoRows}, want: ErrGenerationAuthorityUnavailable},
		{name: "bootstrap incomplete", row: fakeActivationRow{values: []any{
			uuid.NullUUID{}, int64(0), sql.NullTime{}, uuid.NullUUID{},
			sql.NullTime{}, sql.NullString{}, sql.NullString{}, "deployment_role",
		}}, want: ErrBootstrapIncomplete},
		{name: "stale epoch", row: fakeActivationRow{values: []any{
			uuid.NullUUID{}, int64(2), sql.NullTime{Time: now, Valid: true},
			uuid.NullUUID{}, sql.NullTime{}, sql.NullString{}, sql.NullString{},
			"deployment_role",
		}}, want: ErrGenerationActivationConflict},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			transaction := &fakeActivationTransaction{rows: []generationRowScanner{test.row}}
			_, err := (&GenerationActivator{database: &fakeActivationDatabase{
				transaction: transaction,
			}}).Activate(context.Background(), ActivationCommand{
				TenantID:           tenantID,
				GenerationID:       generationID,
				ExpectedWriteEpoch: 0,
				Reason:             "release",
			})
			if !errors.Is(err, test.want) || transaction.committed {
				t.Fatalf("Activate() error=%v committed=%v", err, transaction.committed)
			}
		})
	}
}

type fakeActivationDatabase struct {
	transaction activationTransaction
	err         error
}

func (database *fakeActivationDatabase) Begin(
	context.Context,
) (activationTransaction, error) {
	return database.transaction, database.err
}

type fakeActivationTransaction struct {
	rows      []generationRowScanner
	queries   []string
	committed bool
}

func (transaction *fakeActivationTransaction) queryActivationRow(
	_ context.Context,
	query string,
	_ ...any,
) generationRowScanner {
	transaction.queries = append(transaction.queries, query)
	if len(transaction.rows) == 0 {
		return fakeActivationRow{err: errors.New("unexpected query")}
	}
	row := transaction.rows[0]
	transaction.rows = transaction.rows[1:]
	return row
}

func (transaction *fakeActivationTransaction) Commit() error {
	transaction.committed = true
	return nil
}

func (*fakeActivationTransaction) Rollback() error {
	return nil
}

type fakeActivationRow struct {
	values []any
	err    error
}

func (row fakeActivationRow) Scan(destinations ...any) error {
	if row.err != nil {
		return row.err
	}
	if len(destinations) != len(row.values) {
		return errors.New("activation row destination mismatch")
	}
	for index, destination := range destinations {
		if err := assignActivationValue(destination, row.values[index]); err != nil {
			return err
		}
	}
	return nil
}

func assignActivationValue(destination any, value any) error {
	switch target := destination.(type) {
	case *uuid.UUID:
		*target = value.(uuid.UUID)
	case *uuid.NullUUID:
		*target = value.(uuid.NullUUID)
	case *int64:
		*target = value.(int64)
	case *bool:
		*target = value.(bool)
	case *time.Time:
		*target = value.(time.Time)
	case *sql.NullTime:
		*target = value.(sql.NullTime)
	case *sql.NullString:
		*target = value.(sql.NullString)
	case *string:
		*target = value.(string)
	default:
		return errors.New("unsupported activation row destination")
	}
	return nil
}
