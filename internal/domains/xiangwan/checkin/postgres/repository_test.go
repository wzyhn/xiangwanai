package checkinpostgres

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/checkin"
	"github.com/google/uuid"
)

func TestCreatePersistsCompleteCheckinFact(t *testing.T) {
	t.Parallel()

	want := checkedInFact(time.Now().UTC())
	var capturedQuery string
	var capturedArgs []any
	repository := &Repository{db: &fakeQueryExecutor{
		queryRow: func(query string, args ...any) rowScanner {
			capturedQuery = query
			capturedArgs = append([]any(nil), args...)
			return &fakeRow{values: checkinScanValues(want)}
		},
	}}

	got, err := repository.Create(context.Background(), want)
	if err != nil {
		t.Fatalf(`Create() error = %v`, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf(`Create() = %+v, want %+v`, got, want)
	}
	if !strings.Contains(capturedQuery, `INSERT INTO xiangwan_checkins`) ||
		len(capturedArgs) != 16 ||
		capturedArgs[0] != want.ID ||
		capturedArgs[2] != want.RegistrationID ||
		capturedArgs[7] != checkin.StatusCheckedIn ||
		capturedArgs[13] != int64(1) {
		t.Fatalf(`Create query/args = %s %#v`, capturedQuery, capturedArgs)
	}
}

func TestGetMethodsFenceTenantAndLockWhenRequested(t *testing.T) {
	t.Parallel()

	want := checkedInFact(time.Now().UTC())
	tests := []struct {
		name          string
		call          func(*Repository) (checkin.Checkin, error)
		wantFragments []string
		wantArgs      []any
	}{
		{
			name: `identity`,
			call: func(repository *Repository) (checkin.Checkin, error) {
				return repository.Get(context.Background(), want.TenantID, want.ID)
			},
			wantFragments: []string{`tenant_id = $1 AND id = $2`},
			wantArgs:      []any{want.TenantID, want.ID},
		},
		{
			name: `identity lock`,
			call: func(repository *Repository) (checkin.Checkin, error) {
				return repository.GetForUpdate(context.Background(), want.TenantID, want.ID)
			},
			wantFragments: []string{`tenant_id = $1 AND id = $2`, `FOR UPDATE`},
			wantArgs:      []any{want.TenantID, want.ID},
		},
		{
			name: `Registration identity`,
			call: func(repository *Repository) (checkin.Checkin, error) {
				return repository.GetByRegistration(
					context.Background(),
					want.TenantID,
					want.RegistrationID,
				)
			},
			wantFragments: []string{`tenant_id = $1 AND registration_id = $2`},
			wantArgs:      []any{want.TenantID, want.RegistrationID},
		},
		{
			name: `Registration identity lock`,
			call: func(repository *Repository) (checkin.Checkin, error) {
				return repository.GetByRegistrationForUpdate(
					context.Background(),
					want.TenantID,
					want.RegistrationID,
				)
			},
			wantFragments: []string{
				`tenant_id = $1 AND registration_id = $2`,
				`FOR UPDATE`,
			},
			wantArgs: []any{want.TenantID, want.RegistrationID},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var capturedQuery string
			var capturedArgs []any
			repository := &Repository{db: &fakeQueryExecutor{
				queryRow: func(query string, args ...any) rowScanner {
					capturedQuery = query
					capturedArgs = append([]any(nil), args...)
					return &fakeRow{values: checkinScanValues(want)}
				},
			}}
			got, err := test.call(repository)
			if err != nil {
				t.Fatalf(`get error = %v`, err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf(`get = %+v, want %+v`, got, want)
			}
			for _, fragment := range test.wantFragments {
				if !strings.Contains(capturedQuery, fragment) {
					t.Fatalf(`query %q missing %q`, capturedQuery, fragment)
				}
			}
			if !reflect.DeepEqual(capturedArgs, test.wantArgs) {
				t.Fatalf(`args = %#v, want %#v`, capturedArgs, test.wantArgs)
			}
		})
	}
}

func TestUpdatePersistsOnlyRevocationWithOptimisticVersion(t *testing.T) {
	t.Parallel()

	current := checkedInFact(time.Now().UTC())
	want := revokedFact(current)
	var capturedQuery string
	var capturedArgs []any
	repository := &Repository{db: &fakeQueryExecutor{
		queryRow: func(query string, args ...any) rowScanner {
			capturedQuery = query
			capturedArgs = append([]any(nil), args...)
			return &fakeRow{values: checkinScanValues(want)}
		},
	}}

	got, err := repository.Update(context.Background(), want, current.Version)
	if err != nil {
		t.Fatalf(`Update() error = %v`, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf(`Update() = %+v, want %+v`, got, want)
	}
	for _, fragment := range []string{
		`UPDATE xiangwan_checkins`,
		`version = version + 1`,
		`tenant_id = $1`,
		`version = $8`,
	} {
		if !strings.Contains(capturedQuery, fragment) {
			t.Fatalf(`query %q missing %q`, capturedQuery, fragment)
		}
	}
	if len(capturedArgs) != 8 ||
		capturedArgs[0] != want.TenantID ||
		capturedArgs[1] != want.ID ||
		capturedArgs[2] != checkin.StatusRevoked ||
		capturedArgs[7] != current.Version {
		t.Fatalf(`Update args = %#v`, capturedArgs)
	}
}

func TestUpdateMapsNoRowsToVersionConflict(t *testing.T) {
	t.Parallel()

	want := revokedFact(checkedInFact(time.Now().UTC()))
	repository := &Repository{db: &fakeQueryExecutor{
		queryRow: func(string, ...any) rowScanner {
			return &fakeRow{err: sql.ErrNoRows}
		},
	}}
	_, err := repository.Update(context.Background(), want, 1)
	if !errors.Is(err, ErrCheckinVersionConflict) {
		t.Fatalf(`Update() error = %v, want %v`, err, ErrCheckinVersionConflict)
	}
}

func TestGetMapsNoRowsToNotFound(t *testing.T) {
	t.Parallel()

	repository := &Repository{db: &fakeQueryExecutor{
		queryRow: func(string, ...any) rowScanner {
			return &fakeRow{err: sql.ErrNoRows}
		},
	}}
	_, err := repository.Get(context.Background(), uuid.New(), uuid.New())
	if !errors.Is(err, ErrCheckinNotFound) {
		t.Fatalf(`Get() error = %v, want %v`, err, ErrCheckinNotFound)
	}
}

func TestCreateAndReplayLookupPersistExactEvent(t *testing.T) {
	t.Parallel()

	value := checkedInFact(time.Now().UTC())
	want := checkedInEvent(value)
	var queries []string
	var arguments [][]any
	repository := &Repository{db: &fakeQueryExecutor{
		queryRow: func(query string, args ...any) rowScanner {
			queries = append(queries, query)
			arguments = append(arguments, append([]any(nil), args...))
			return &fakeRow{values: eventScanValues(want)}
		},
	}}

	created, err := repository.CreateEvent(context.Background(), want)
	if err != nil {
		t.Fatalf(`CreateEvent() error = %v`, err)
	}
	got, err := repository.GetEventByIdempotencyKey(
		context.Background(),
		want.TenantID,
		want.ActorID,
		want.IdempotencyKey,
	)
	if err != nil {
		t.Fatalf(`GetEventByIdempotencyKey() error = %v`, err)
	}
	if !reflect.DeepEqual(created, want) || !reflect.DeepEqual(got, want) {
		t.Fatalf(`events = %+v %+v, want %+v`, created, got, want)
	}
	if len(queries) != 2 ||
		!strings.Contains(queries[0], `INSERT INTO xiangwan_checkin_events`) ||
		len(arguments[0]) != 15 ||
		!strings.Contains(queries[1], `tenant_id = $1`) ||
		!strings.Contains(queries[1], `actor_id = $2`) ||
		!strings.Contains(queries[1], `idempotency_key = $3`) ||
		!reflect.DeepEqual(arguments[1], []any{
			want.TenantID,
			want.ActorID,
			want.IdempotencyKey,
		}) {
		t.Fatalf(`event queries/args = %#v %#v`, queries, arguments)
	}
}

func TestListEventsUsesTenantVersionCapAndStableOrder(t *testing.T) {
	t.Parallel()

	current := checkedInFact(time.Now().UTC())
	created := checkedInEvent(current)
	revoked := revokedFact(current)
	revocation := revokedEvent(current, revoked)
	rows := newFakeRows(eventScanValues(created), eventScanValues(revocation))
	var capturedQuery string
	var capturedArgs []any
	repository := &Repository{db: &fakeQueryExecutor{
		queryRows: func(query string, args ...any) (rowsScanner, error) {
			capturedQuery = query
			capturedArgs = append([]any(nil), args...)
			return rows, nil
		},
	}}

	got, err := repository.ListEvents(
		context.Background(),
		current.TenantID,
		current.ID,
		revoked.Version,
	)
	if err != nil {
		t.Fatalf(`ListEvents() error = %v`, err)
	}
	if !reflect.DeepEqual(got, []checkin.Event{created, revocation}) {
		t.Fatalf(`ListEvents() = %+v`, got)
	}
	for _, fragment := range []string{
		`tenant_id = $1`,
		`checkin_id = $2`,
		`resulting_checkin_version <= $3`,
		`ORDER BY event_sequence ASC`,
	} {
		if !strings.Contains(capturedQuery, fragment) {
			t.Fatalf(`query %q missing %q`, capturedQuery, fragment)
		}
	}
	if !reflect.DeepEqual(capturedArgs, []any{current.TenantID, current.ID, int64(2)}) {
		t.Fatalf(`ListEvents args = %#v`, capturedArgs)
	}
	if !rows.closed {
		t.Fatal(`ListEvents() did not close rows`)
	}
}

func checkedInFact(at time.Time) checkin.Checkin {
	return checkin.Checkin{
		ID:             uuid.New(),
		TenantID:       uuid.New(),
		RegistrationID: uuid.New(),
		SeriesID:       uuid.New(),
		InstanceID:     uuid.New(),
		SessionID:      uuid.New(),
		PrincipalID:    uuid.New(),
		CheckinStatus:  checkin.StatusCheckedIn,
		CheckedInBy:    uuid.New(),
		CheckedInAt:    at,
		Version:        1,
		CreatedAt:      at,
		UpdatedAt:      at,
	}
}

func revokedFact(current checkin.Checkin) checkin.Checkin {
	value := current
	actorID := uuid.New()
	at := current.UpdatedAt.Add(time.Minute)
	reason := `operator correction`
	value.CheckinStatus = checkin.StatusRevoked
	value.RevokedBy = &actorID
	value.RevokedAt = &at
	value.RevocationReason = &reason
	value.Version = 2
	value.UpdatedAt = at
	return value
}

func checkedInEvent(value checkin.Checkin) checkin.Event {
	return checkin.Event{
		ID:                      uuid.New(),
		TenantID:                value.TenantID,
		CheckinID:               value.ID,
		RegistrationID:          value.RegistrationID,
		SessionID:               value.SessionID,
		EventSequence:           1,
		EventType:               checkin.EventTypeCheckedIn,
		IdempotencyKey:          `scan:create:001`,
		ToStatus:                checkin.StatusCheckedIn,
		ActorID:                 value.CheckedInBy,
		OccurredAt:              value.UpdatedAt,
		ResultingCheckinVersion: 1,
		CreatedAt:               value.UpdatedAt,
	}
}

func revokedEvent(before checkin.Checkin, after checkin.Checkin) checkin.Event {
	from := checkin.StatusCheckedIn
	return checkin.Event{
		ID:                      uuid.New(),
		TenantID:                after.TenantID,
		CheckinID:               after.ID,
		RegistrationID:          after.RegistrationID,
		SessionID:               after.SessionID,
		EventSequence:           2,
		EventType:               checkin.EventTypeRevoked,
		IdempotencyKey:          `scan:revoke:001`,
		FromStatus:              &from,
		ToStatus:                checkin.StatusRevoked,
		ActorID:                 *after.RevokedBy,
		Reason:                  after.RevocationReason,
		OccurredAt:              after.UpdatedAt,
		ResultingCheckinVersion: before.Version + 1,
		CreatedAt:               after.UpdatedAt,
	}
}

func checkinScanValues(value checkin.Checkin) []any {
	return []any{
		value.ID,
		value.TenantID,
		value.RegistrationID,
		value.SeriesID,
		value.InstanceID,
		value.SessionID,
		value.PrincipalID,
		value.CheckinStatus,
		value.CheckedInBy,
		value.CheckedInAt,
		nullUUID(value.RevokedBy),
		nullTime(value.RevokedAt),
		nullString(value.RevocationReason),
		value.Version,
		value.CreatedAt,
		value.UpdatedAt,
	}
}

func eventScanValues(value checkin.Event) []any {
	var fromStatus sql.NullString
	if value.FromStatus != nil {
		fromStatus = sql.NullString{String: string(*value.FromStatus), Valid: true}
	}
	return []any{
		value.ID,
		value.TenantID,
		value.CheckinID,
		value.RegistrationID,
		value.SessionID,
		value.EventSequence,
		value.EventType,
		value.IdempotencyKey,
		fromStatus,
		value.ToStatus,
		value.ActorID,
		nullString(value.Reason),
		value.OccurredAt,
		value.ResultingCheckinVersion,
		value.CreatedAt,
	}
}

type fakeQueryExecutor struct {
	queryRow  func(string, ...any) rowScanner
	queryRows func(string, ...any) (rowsScanner, error)
}

func (executor *fakeQueryExecutor) queryRowContext(
	_ context.Context,
	query string,
	args ...any,
) rowScanner {
	return executor.queryRow(query, args...)
}

func (executor *fakeQueryExecutor) queryContext(
	_ context.Context,
	query string,
	args ...any,
) (rowsScanner, error) {
	return executor.queryRows(query, args...)
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
		return errors.New(`fake row destination count mismatch`)
	}
	for index, destination := range destinations {
		target := reflect.ValueOf(destination)
		value := reflect.ValueOf(row.values[index])
		if target.Kind() != reflect.Pointer ||
			!value.IsValid() ||
			!value.Type().AssignableTo(target.Elem().Type()) {
			return errors.New(`fake row value type mismatch`)
		}
		target.Elem().Set(value)
	}
	return nil
}

type fakeRows struct {
	values [][]any
	index  int
	err    error
	closed bool
}

func newFakeRows(values ...[]any) *fakeRows {
	return &fakeRows{values: values, index: -1}
}

func (rows *fakeRows) Next() bool {
	if rows.index+1 >= len(rows.values) {
		return false
	}
	rows.index++
	return true
}

func (rows *fakeRows) Scan(destinations ...any) error {
	if rows.index < 0 || rows.index >= len(rows.values) {
		return errors.New(`fake rows Scan called without current row`)
	}
	return (&fakeRow{values: rows.values[rows.index]}).Scan(destinations...)
}

func (rows *fakeRows) Err() error {
	return rows.err
}

func (rows *fakeRows) Close() error {
	rows.closed = true
	return nil
}

func nullUUID(value *uuid.UUID) uuid.NullUUID {
	if value == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *value, Valid: true}
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
