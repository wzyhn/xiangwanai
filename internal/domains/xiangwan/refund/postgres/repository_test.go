package refundpostgres

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/refund"
	"github.com/google/uuid"
)

func TestCreatePersistsCompleteRefundCase(t *testing.T) {
	t.Parallel()

	want := pendingRefundCase(t)
	var capturedQuery string
	var capturedArgs []any
	repository := &Repository{db: &fakeQueryExecutor{
		queryRow: func(query string, args ...any) rowScanner {
			capturedQuery = query
			capturedArgs = append([]any(nil), args...)
			return &fakeRow{values: refundCaseScanValues(want)}
		},
	}}

	got, err := repository.Create(context.Background(), want)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Create() = %+v, want %+v", got, want)
	}
	if !strings.Contains(capturedQuery, "INSERT INTO xiangwan_refund_cases") ||
		len(capturedArgs) != 23 ||
		capturedArgs[0] != want.ID ||
		capturedArgs[2] != want.OrderID ||
		capturedArgs[8] != refund.StatusPendingManual ||
		capturedArgs[11] != want.RequestedRefundCents ||
		capturedArgs[20] != int64(1) {
		t.Fatalf("Create query/args = %s %#v", capturedQuery, capturedArgs)
	}
}

func TestGetMethodsAreTenantScopedAndApplyRequestedLocks(t *testing.T) {
	t.Parallel()

	want := pendingRefundCase(t)
	tests := []struct {
		name          string
		call          func(*Repository) (refund.Case, error)
		wantFragments []string
		wantArgs      []any
	}{
		{
			name: "identity",
			call: func(repository *Repository) (refund.Case, error) {
				return repository.Get(context.Background(), want.TenantID, want.ID)
			},
			wantFragments: []string{"tenant_id = $1 AND id = $2"},
			wantArgs:      []any{want.TenantID, want.ID},
		},
		{
			name: "identity lock",
			call: func(repository *Repository) (refund.Case, error) {
				return repository.GetForUpdate(context.Background(), want.TenantID, want.ID)
			},
			wantFragments: []string{"tenant_id = $1 AND id = $2", "FOR UPDATE"},
			wantArgs:      []any{want.TenantID, want.ID},
		},
		{
			name: "Order identity",
			call: func(repository *Repository) (refund.Case, error) {
				return repository.GetByOrder(context.Background(), want.TenantID, want.OrderID)
			},
			wantFragments: []string{"tenant_id = $1 AND order_id = $2"},
			wantArgs:      []any{want.TenantID, want.OrderID},
		},
		{
			name: "Order identity lock",
			call: func(repository *Repository) (refund.Case, error) {
				return repository.GetByOrderForUpdate(context.Background(), want.TenantID, want.OrderID)
			},
			wantFragments: []string{"tenant_id = $1 AND order_id = $2", "FOR UPDATE"},
			wantArgs:      []any{want.TenantID, want.OrderID},
		},
		{
			name: "idempotency",
			call: func(repository *Repository) (refund.Case, error) {
				return repository.GetByIdempotencyKey(
					context.Background(),
					want.TenantID,
					want.IdempotencyKey,
				)
			},
			wantFragments: []string{"tenant_id = $1 AND idempotency_key = $2"},
			wantArgs:      []any{want.TenantID, want.IdempotencyKey},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var capturedQuery string
			var capturedArgs []any
			repository := &Repository{db: &fakeQueryExecutor{
				queryRow: func(query string, args ...any) rowScanner {
					capturedQuery = query
					capturedArgs = append([]any(nil), args...)
					return &fakeRow{values: refundCaseScanValues(want)}
				},
			}}
			got, err := test.call(repository)
			if err != nil {
				t.Fatalf("get error = %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("get = %+v, want %+v", got, want)
			}
			for _, fragment := range test.wantFragments {
				if !strings.Contains(capturedQuery, fragment) {
					t.Fatalf("query %q does not contain %q", capturedQuery, fragment)
				}
			}
			if !reflect.DeepEqual(capturedArgs, test.wantArgs) {
				t.Fatalf("query args = %#v, want %#v", capturedArgs, test.wantArgs)
			}
		})
	}
}

func TestUpdateUsesOptimisticVersionAndPersistsLifecycle(t *testing.T) {
	t.Parallel()

	current := pendingRefundCase(t)
	operatorID := uuid.New()
	resolvedAt := current.CreatedAt.Add(time.Minute)
	updated, changed, err := refund.Complete(current, refund.CompleteCommand{
		HandledBy:         operatorID,
		ExternalRefundID:  "wechat-refund-1",
		EvidenceReference: "merchant-console/refunds/1",
		OperatorNote:      "provider accepted",
		At:                resolvedAt,
	})
	if err != nil || !changed {
		t.Fatalf("Complete() = %+v, %t, %v", updated, changed, err)
	}

	var capturedQuery string
	var capturedArgs []any
	repository := &Repository{db: &fakeQueryExecutor{
		queryRow: func(query string, args ...any) rowScanner {
			capturedQuery = query
			capturedArgs = append([]any(nil), args...)
			return &fakeRow{values: refundCaseScanValues(updated)}
		},
	}}
	got, err := repository.Update(context.Background(), updated, current.Version)
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if !reflect.DeepEqual(got, updated) {
		t.Fatalf("Update() = %+v, want %+v", got, updated)
	}
	if !strings.Contains(capturedQuery, "version = version + 1") ||
		!strings.Contains(capturedQuery, "AND version = $13") ||
		len(capturedArgs) != 13 ||
		capturedArgs[0] != updated.TenantID ||
		capturedArgs[1] != updated.ID ||
		capturedArgs[2] != refund.StatusRefunded ||
		capturedArgs[3] != updated.SuccessfulRefundCents ||
		capturedArgs[11] != updated.UpdatedAt ||
		capturedArgs[12] != current.Version {
		t.Fatalf("Update query/args = %s %#v", capturedQuery, capturedArgs)
	}
}

func TestRepositoryTranslatesMissingRowsAndPreservesScanFailures(t *testing.T) {
	t.Parallel()

	scanFailure := errors.New("scan failed")
	tests := []struct {
		name    string
		row     rowScanner
		call    func(*Repository) error
		wantErr error
	}{
		{
			name: "get not found",
			row:  &fakeRow{err: sql.ErrNoRows},
			call: func(repository *Repository) error {
				_, err := repository.Get(context.Background(), uuid.New(), uuid.New())
				return err
			},
			wantErr: ErrRefundCaseNotFound,
		},
		{
			name: "get scan failure",
			row:  &fakeRow{err: scanFailure},
			call: func(repository *Repository) error {
				_, err := repository.GetByOrder(context.Background(), uuid.New(), uuid.New())
				return err
			},
			wantErr: scanFailure,
		},
		{
			name: "update conflict",
			row:  &fakeRow{err: sql.ErrNoRows},
			call: func(repository *Repository) error {
				_, err := repository.Update(context.Background(), pendingRefundCase(t), 1)
				return err
			},
			wantErr: ErrRefundCaseVersionConflict,
		},
		{
			name: "create scan failure",
			row:  &fakeRow{err: scanFailure},
			call: func(repository *Repository) error {
				_, err := repository.Create(context.Background(), pendingRefundCase(t))
				return err
			},
			wantErr: scanFailure,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository := &Repository{db: &fakeQueryExecutor{
				queryRow: func(string, ...any) rowScanner { return test.row },
			}}
			if err := test.call(repository); !errors.Is(err, test.wantErr) {
				t.Fatalf("repository error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestCreateEventPersistsImmutableTransitionReceipt(t *testing.T) {
	t.Parallel()

	want := completedRefundEvent(t)
	var capturedQuery string
	var capturedArgs []any
	repository := &Repository{db: &fakeQueryExecutor{
		queryRow: func(query string, args ...any) rowScanner {
			capturedQuery = query
			capturedArgs = append([]any(nil), args...)
			return &fakeRow{values: refundEventScanValues(want)}
		},
	}}
	got, err := repository.CreateEvent(context.Background(), want)
	if err != nil {
		t.Fatalf("CreateEvent() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CreateEvent() = %+v, want %+v", got, want)
	}
	if !strings.Contains(capturedQuery, "INSERT INTO xiangwan_refund_events") ||
		len(capturedArgs) != 18 ||
		capturedArgs[0] != want.ID ||
		capturedArgs[2] != want.RefundCaseID ||
		capturedArgs[4] != want.EventSequence ||
		capturedArgs[5] != refund.EventTypeRefundCompleted ||
		capturedArgs[16] != want.ResultingRefundVersion {
		t.Fatalf("CreateEvent query/args = %s %#v", capturedQuery, capturedArgs)
	}
}

func TestEventGetMethodsAreTenantScoped(t *testing.T) {
	t.Parallel()

	want := completedRefundEvent(t)
	tests := []struct {
		name          string
		call          func(*Repository) (refund.Event, error)
		wantFragments []string
		wantArgs      []any
	}{
		{
			name: "identity",
			call: func(repository *Repository) (refund.Event, error) {
				return repository.GetEvent(context.Background(), want.TenantID, want.ID)
			},
			wantFragments: []string{"tenant_id = $1 AND id = $2"},
			wantArgs:      []any{want.TenantID, want.ID},
		},
		{
			name: "idempotency",
			call: func(repository *Repository) (refund.Event, error) {
				return repository.GetEventByIdempotencyKey(
					context.Background(),
					want.TenantID,
					want.RefundCaseID,
					want.IdempotencyKey,
				)
			},
			wantFragments: []string{
				"tenant_id = $1",
				"refund_case_id = $2",
				"idempotency_key = $3",
			},
			wantArgs: []any{want.TenantID, want.RefundCaseID, want.IdempotencyKey},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var capturedQuery string
			var capturedArgs []any
			repository := &Repository{db: &fakeQueryExecutor{
				queryRow: func(query string, args ...any) rowScanner {
					capturedQuery = query
					capturedArgs = append([]any(nil), args...)
					return &fakeRow{values: refundEventScanValues(want)}
				},
			}}
			got, err := test.call(repository)
			if err != nil {
				t.Fatalf("event get error = %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("event get = %+v, want %+v", got, want)
			}
			for _, fragment := range test.wantFragments {
				if !strings.Contains(capturedQuery, fragment) {
					t.Fatalf("query %q does not contain %q", capturedQuery, fragment)
				}
			}
			if !reflect.DeepEqual(capturedArgs, test.wantArgs) {
				t.Fatalf("query args = %#v, want %#v", capturedArgs, test.wantArgs)
			}
		})
	}

	notFoundRepository := &Repository{db: &fakeQueryExecutor{
		queryRow: func(string, ...any) rowScanner { return &fakeRow{err: sql.ErrNoRows} },
	}}
	if _, err := notFoundRepository.GetEvent(
		context.Background(),
		uuid.New(),
		uuid.New(),
	); !errors.Is(err, ErrRefundEventNotFound) {
		t.Fatalf("GetEvent(not found) error = %v", err)
	}
}

type fakeQueryExecutor struct {
	queryRow  func(string, ...any) rowScanner
	queryRows func(string, ...any) (rowsScanner, error)
}

func (executor *fakeQueryExecutor) queryContext(
	_ context.Context,
	query string,
	args ...any,
) (rowsScanner, error) {
	if executor.queryRows == nil {
		return nil, errors.New("unexpected fake rows query")
	}
	return executor.queryRows(query, args...)
}

func (executor *fakeQueryExecutor) queryRowContext(
	_ context.Context,
	query string,
	args ...any,
) rowScanner {
	return executor.queryRow(query, args...)
}

type fakeRow struct {
	values []any
	err    error
}

func (row *fakeRow) Scan(destinations ...any) error {
	if row.err != nil {
		return row.err
	}
	if len(destinations) != len(row.values) {
		return errors.New("fake row destination count mismatch")
	}
	for index, destination := range destinations {
		target := reflect.ValueOf(destination)
		value := reflect.ValueOf(row.values[index])
		if target.Kind() != reflect.Pointer ||
			!value.IsValid() ||
			!value.Type().AssignableTo(target.Elem().Type()) {
			return errors.New("fake row value type mismatch")
		}
		target.Elem().Set(value)
	}
	return nil
}

func refundCaseScanValues(value refund.Case) []any {
	return []any{
		value.ID,
		value.TenantID,
		value.OrderID,
		value.RegistrationID,
		value.SeriesID,
		value.InstanceID,
		value.SessionID,
		value.PrincipalID,
		value.RefundStatus,
		value.ReasonCode,
		value.IdempotencyKey,
		value.RequestedRefundCents,
		value.SuccessfulRefundCents,
		nullTime(value.ProcessingStartedAt),
		nullTime(value.ResolvedAt),
		nullUUID(value.HandledBy),
		nullString(value.ExternalRefundID),
		nullString(value.EvidenceReference),
		nullString(value.OperatorNote),
		nullString(value.FailureReason),
		value.Version,
		value.CreatedAt,
		value.UpdatedAt,
	}
}

func refundEventScanValues(value refund.Event) []any {
	return []any{
		value.ID,
		value.TenantID,
		value.RefundCaseID,
		value.OrderID,
		value.EventSequence,
		value.EventType,
		value.IdempotencyKey,
		value.FromStatus,
		value.ToStatus,
		value.ActorID,
		value.SuccessfulRefundCents,
		nullString(value.ExternalRefundID),
		nullString(value.EvidenceReference),
		nullString(value.OperatorNote),
		nullString(value.FailureReason),
		value.OccurredAt,
		value.ResultingRefundVersion,
		value.CreatedAt,
	}
}

func completedRefundEvent(t *testing.T) refund.Event {
	t.Helper()
	current := pendingRefundCase(t)
	completed, changed, err := refund.Complete(current, refund.CompleteCommand{
		HandledBy:         uuid.New(),
		ExternalRefundID:  "wx-refund-event-1",
		EvidenceReference: "merchant-console/refunds/1",
		OperatorNote:      "verified",
		At:                current.CreatedAt.Add(time.Minute),
	})
	if err != nil || !changed {
		t.Fatalf("Complete() = %+v, %t, %v", completed, changed, err)
	}
	event, err := refund.NewEvent(
		current,
		completed,
		"refund-event:complete-1",
		completed.UpdatedAt.Add(time.Second),
	)
	if err != nil {
		t.Fatalf("NewEvent() error = %v", err)
	}
	return event
}

func pendingRefundCase(t *testing.T) refund.Case {
	t.Helper()
	now := time.Date(2026, time.September, 12, 8, 0, 0, 0, time.UTC)
	value, err := refund.NewCase(refund.NewCaseCommand{
		TenantID:             uuid.New(),
		OrderID:              uuid.New(),
		RegistrationID:       uuid.New(),
		SeriesID:             uuid.New(),
		InstanceID:           uuid.New(),
		SessionID:            uuid.New(),
		PrincipalID:          uuid.New(),
		ReasonCode:           refund.ReasonHoldExpiredAfterPayment,
		IdempotencyKey:       "refund:payment:test-1",
		ActualPaidCents:      9_000,
		RequestedRefundCents: 9_000,
		OperatorNote:         "late payment",
		Now:                  now,
	})
	if err != nil {
		t.Fatalf("NewCase() error = %v", err)
	}
	return value
}

func nullUUID(value *uuid.UUID) uuid.NullUUID {
	if value == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *value, Valid: true}
}

func nullString(value *string) sql.NullString {
	if value == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *value, Valid: true}
}

func nullTime(value *time.Time) sql.NullTime {
	if value == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *value, Valid: true}
}
