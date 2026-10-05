package xiangwanruntime

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestPostgresGenerationGateRequiresExactCustomerGeneration(t *testing.T) {
	t.Parallel()

	tenantID := uuid.New()
	generationID := uuid.New()
	var capturedQuery string
	var capturedArgs []any
	gate := &PostgresGenerationGate{db: &fakeGenerationExecutor{
		query: func(query string, args ...any) generationRowScanner {
			capturedQuery = query
			capturedArgs = append([]any(nil), args...)
			return fakeGenerationRow{active: true}
		},
	}}
	if err := gate.CheckActiveGeneration(
		context.Background(),
		tenantID,
		generationID,
	); err != nil {
		t.Fatalf("CheckActiveGeneration() error = %v", err)
	}
	if !reflect.DeepEqual(capturedArgs, []any{tenantID, generationID}) {
		t.Fatalf("CheckActiveGeneration() args = %#v", capturedArgs)
	}
	for _, fragment := range []string{
		"runtime_generation.singleton_id = 1",
		"runtime_generation.scope_key = 'wq-xiangwan'",
		"runtime_generation.tenant_id = $1",
		"runtime_generation.active_generation_id = $2",
		"runtime_generation.write_epoch > 0",
		"runtime_generation.bootstrap_completed_at IS NOT NULL",
		"tenant.type = 'business'",
		`tenant.metadata @> '{"product_code":"wq-xiangwan"}'::jsonb`,
	} {
		if !strings.Contains(capturedQuery, fragment) {
			t.Fatalf("generation query does not contain %q", fragment)
		}
	}
}

func TestPostgresGenerationGateFailsClosed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		row  fakeGenerationRow
		want error
	}{
		{name: "inactive", row: fakeGenerationRow{}, want: ErrGenerationInactive},
		{name: "database", row: fakeGenerationRow{err: errors.New("offline")}, want: errors.New("offline")},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			gate := &PostgresGenerationGate{db: &fakeGenerationExecutor{
				query: func(string, ...any) generationRowScanner {
					return test.row
				},
			}}
			err := gate.CheckActiveGeneration(
				context.Background(),
				uuid.New(),
				uuid.New(),
			)
			if test.name == "database" {
				if err == nil || !strings.Contains(err.Error(), "offline") {
					t.Fatalf("CheckActiveGeneration() error = %v", err)
				}
				return
			}
			if !errors.Is(err, test.want) {
				t.Fatalf("CheckActiveGeneration() error = %v", err)
			}
		})
	}

	if err := (&PostgresGenerationGate{}).CheckActiveGeneration(
		context.Background(),
		uuid.New(),
		uuid.New(),
	); !errors.Is(err, ErrInvalidGenerationGate) {
		t.Fatalf("CheckActiveGeneration(unwired) error = %v", err)
	}
}

type fakeGenerationExecutor struct {
	query func(string, ...any) generationRowScanner
}

func (fake *fakeGenerationExecutor) queryRowContext(
	_ context.Context,
	query string,
	args ...any,
) generationRowScanner {
	return fake.query(query, args...)
}

type fakeGenerationRow struct {
	active bool
	err    error
}

func (row fakeGenerationRow) Scan(destinations ...any) error {
	if row.err != nil {
		return row.err
	}
	if len(destinations) != 1 {
		return errors.New("generation row destination mismatch")
	}
	active, ok := destinations[0].(*bool)
	if !ok {
		return errors.New("generation row type mismatch")
	}
	*active = row.active
	return nil
}
