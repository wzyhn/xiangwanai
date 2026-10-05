package registrationpostgres

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
	"github.com/google/uuid"
)

func TestCreateRegistrationPersistsCompleteParticipationFact(t *testing.T) {
	t.Parallel()

	want := confirmedRegistration(time.Date(2026, time.September, 12, 4, 0, 0, 0, time.UTC))
	var capturedQuery string
	var capturedArgs []any
	repository := &Repository{db: &fakeQueryExecutor{
		queryRow: func(query string, args ...any) rowScanner {
			capturedQuery = query
			capturedArgs = append([]any(nil), args...)
			return &fakeRow{values: registrationScanValues(want)}
		},
	}}

	got, err := repository.Create(context.Background(), want)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Create() = %+v, want %+v", got, want)
	}
	if !strings.Contains(capturedQuery, "INSERT INTO xiangwan_registrations") || len(capturedArgs) != 14 {
		t.Fatalf("Create query/args = %s %#v", capturedQuery, capturedArgs)
	}
	if capturedArgs[0] != want.ID ||
		capturedArgs[4] != want.SessionID ||
		capturedArgs[6] != registration.ParticipationStatusConfirmed ||
		capturedArgs[7] != want.IdempotencyKey ||
		capturedArgs[11] != int64(1) {
		t.Fatalf("Create args = %#v", capturedArgs)
	}
}

func TestRegistrationGetMethodsUseStableTenantScopes(t *testing.T) {
	t.Parallel()

	want := confirmedRegistration(time.Now().UTC())
	tests := []struct {
		name         string
		call         func(*Repository) (registration.Registration, error)
		wantFragment string
		wantArgs     []any
	}{
		{
			name: "identity",
			call: func(repository *Repository) (registration.Registration, error) {
				return repository.Get(context.Background(), want.TenantID, want.ID)
			},
			wantFragment: "tenant_id = $1 AND id = $2",
			wantArgs:     []any{want.TenantID, want.ID},
		},
		{
			name: "identity lock",
			call: func(repository *Repository) (registration.Registration, error) {
				return repository.GetForUpdate(context.Background(), want.TenantID, want.ID)
			},
			wantFragment: "FOR UPDATE",
			wantArgs:     []any{want.TenantID, want.ID},
		},
		{
			name: "idempotency",
			call: func(repository *Repository) (registration.Registration, error) {
				return repository.GetByIdempotencyKey(context.Background(), want.TenantID, want.IdempotencyKey)
			},
			wantFragment: "tenant_id = $1 AND idempotency_key = $2",
			wantArgs:     []any{want.TenantID, want.IdempotencyKey},
		},
		{
			name: "open principal Session",
			call: func(repository *Repository) (registration.Registration, error) {
				return repository.GetOpenByPrincipalSession(
					context.Background(),
					want.TenantID,
					want.PrincipalID,
					want.SessionID,
				)
			},
			wantFragment: "participation_status IN ('pending_payment', 'confirmed')",
			wantArgs:     []any{want.TenantID, want.PrincipalID, want.SessionID},
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
					return &fakeRow{values: registrationScanValues(want)}
				},
			}}
			got, err := test.call(repository)
			if err != nil {
				t.Fatalf("get error = %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("get = %+v, want %+v", got, want)
			}
			if !strings.Contains(capturedQuery, test.wantFragment) || !reflect.DeepEqual(capturedArgs, test.wantArgs) {
				t.Fatalf("query/args = %s %#v", capturedQuery, capturedArgs)
			}
		})
	}
}

func TestRegistrationGetMethodsTranslateOnlyNoRows(t *testing.T) {
	t.Parallel()

	scanFailure := errors.New("scan failed")
	for _, test := range []struct {
		name    string
		row     rowScanner
		wantErr error
	}{
		{name: "not found", row: &fakeRow{err: sql.ErrNoRows}, wantErr: ErrRegistrationNotFound},
		{name: "scan", row: &fakeRow{err: scanFailure}, wantErr: scanFailure},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository := &Repository{db: &fakeQueryExecutor{
				queryRow: func(string, ...any) rowScanner { return test.row },
			}}
			_, err := repository.Get(context.Background(), uuid.New(), uuid.New())
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("Get() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestUpdateParticipationUsesOptimisticVersion(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	current := confirmedRegistration(now)
	cancelledAt := now.Add(time.Minute)
	reason := "user_cancelled"
	updated := current
	updated.ParticipationStatus = registration.ParticipationStatusCancelled
	updated.CancelledAt = &cancelledAt
	updated.CancellationReason = &reason
	updated.Version = 2
	updated.UpdatedAt = cancelledAt

	var capturedQuery string
	var capturedArgs []any
	repository := &Repository{db: &fakeQueryExecutor{
		queryRow: func(query string, args ...any) rowScanner {
			capturedQuery = query
			capturedArgs = append([]any(nil), args...)
			return &fakeRow{values: registrationScanValues(updated)}
		},
	}}
	got, err := repository.UpdateParticipation(context.Background(), updated, 1)
	if err != nil {
		t.Fatalf("UpdateParticipation() error = %v", err)
	}
	if !reflect.DeepEqual(got, updated) {
		t.Fatalf("UpdateParticipation() = %+v, want %+v", got, updated)
	}
	if !strings.Contains(capturedQuery, "version = version + 1") ||
		!strings.Contains(capturedQuery, "AND version = $8") ||
		capturedArgs[0] != updated.TenantID ||
		capturedArgs[1] != updated.ID ||
		capturedArgs[6] != updated.UpdatedAt ||
		capturedArgs[7] != int64(1) {
		t.Fatalf("Update query/args = %s %#v", capturedQuery, capturedArgs)
	}

	conflictRepository := &Repository{db: &fakeQueryExecutor{
		queryRow: func(string, ...any) rowScanner { return &fakeRow{err: sql.ErrNoRows} },
	}}
	if _, err := conflictRepository.UpdateParticipation(context.Background(), updated, 1); !errors.Is(
		err,
		ErrRegistrationVersionConflict,
	) {
		t.Fatalf("UpdateParticipation(conflict) error = %v", err)
	}
}

type fakeQueryExecutor struct {
	queryRow func(string, ...any) rowScanner
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
		if target.Kind() != reflect.Pointer || !value.IsValid() || !value.Type().AssignableTo(target.Elem().Type()) {
			return errors.New("fake row value type mismatch")
		}
		target.Elem().Set(value)
	}
	return nil
}

func registrationScanValues(value registration.Registration) []any {
	return []any{
		value.ID,
		value.TenantID,
		value.SeriesID,
		value.InstanceID,
		value.SessionID,
		value.PrincipalID,
		value.ParticipationStatus,
		value.IdempotencyKey,
		nullTime(value.ConfirmedAt),
		nullTime(value.CancelledAt),
		nullString(value.CancellationReason),
		value.Version,
		value.CreatedAt,
		value.UpdatedAt,
	}
}

func confirmedRegistration(now time.Time) registration.Registration {
	confirmedAt := now
	return registration.Registration{
		ID:                  uuid.New(),
		TenantID:            uuid.New(),
		SeriesID:            uuid.New(),
		InstanceID:          uuid.New(),
		SessionID:           uuid.New(),
		PrincipalID:         uuid.New(),
		ParticipationStatus: registration.ParticipationStatusConfirmed,
		IdempotencyKey:      "registration:test:1",
		ConfirmedAt:         &confirmedAt,
		Version:             1,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
}

func nullTime(value *time.Time) sql.NullTime {
	if value == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *value, Valid: true}
}

func nullString(value *string) sql.NullString {
	if value == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *value, Valid: true}
}
