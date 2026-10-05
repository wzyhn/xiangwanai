package contributionpostgres

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/contribution"
	"github.com/google/uuid"
)

func TestRepositoryCreatesCompleteAppendOnlyEntry(t *testing.T) {
	t.Parallel()

	entry := repositoryEntry(t)
	var query string
	var arguments []any
	repository := &Repository{db: fakeContributionQueryExecutor{
		queryRow: func(gotQuery string, gotArguments ...any) rowScanner {
			query = gotQuery
			arguments = append([]any(nil), gotArguments...)
			return &fakeContributionRow{values: entryScanValues(entry)}
		},
	}}
	created, err := repository.Create(context.Background(), entry)
	if err != nil || !reflect.DeepEqual(created, entry) {
		t.Fatalf(`Create() = %+v, %v`, created, err)
	}
	if !strings.Contains(query, `ON CONFLICT DO NOTHING`) ||
		!strings.Contains(query, `reversal_of_entry_id`) ||
		len(arguments) != 18 || arguments[10] != entry.CheckinEventID {
		t.Fatalf(`Create query/arguments = %q %#v`, query, arguments)
	}
}

func TestRepositoryTreatsInsertConflictAsReplayCandidate(t *testing.T) {
	t.Parallel()

	repository := &Repository{db: fakeContributionQueryExecutor{
		queryRow: func(string, ...any) rowScanner {
			return &fakeContributionRow{err: sql.ErrNoRows}
		},
	}}
	if _, err := repository.Create(
		context.Background(),
		repositoryEntry(t),
	); !errors.Is(err, ErrEntryExists) {
		t.Fatalf(`Create(conflict) error = %v`, err)
	}
}

func TestRepositoryListsOnlyPrincipalScopedStableHistory(t *testing.T) {
	t.Parallel()

	earned := repositoryEntry(t)
	reversal, err := contribution.Reverse(contribution.ReverseCommand{
		Earned:         earned,
		CheckinEventID: uuid.New(),
		OccurredAt:     earned.OccurredAt.Add(time.Minute),
		RecordedAt:     earned.RecordedAt.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf(`Reverse() error = %v`, err)
	}
	var query string
	var arguments []any
	rows := &fakeContributionRows{values: [][]any{
		entryScanValues(reversal),
		entryScanValues(earned),
	}}
	repository := &Repository{db: fakeContributionQueryExecutor{
		query: func(gotQuery string, gotArguments ...any) (
			rowsScanner,
			error,
		) {
			query = gotQuery
			arguments = append([]any(nil), gotArguments...)
			return rows, nil
		},
	}}
	entries, err := repository.ListByPrincipal(
		context.Background(),
		earned.TenantID,
		earned.PrincipalID,
	)
	if err != nil || len(entries) != 2 || !rows.closed {
		t.Fatalf(`ListByPrincipal() = %+v, %v rows=%+v`, entries, err, rows)
	}
	if !strings.Contains(query, `tenant_id = $1`) ||
		!strings.Contains(query, `principal_id = $2`) ||
		!strings.Contains(query, `ORDER BY occurred_at DESC, id DESC`) ||
		!reflect.DeepEqual(
			arguments,
			[]any{earned.TenantID, earned.PrincipalID},
		) {
		t.Fatalf(`ListByPrincipal query/arguments = %q %#v`, query, arguments)
	}
}

func TestRepositoryFindsOnlyDurablyPendingCheckinEffects(t *testing.T) {
	t.Parallel()

	tenantID := uuid.New()
	checkinID := uuid.New()
	var query string
	var arguments []any
	rows := &fakeContributionRows{values: [][]any{{tenantID, checkinID}}}
	repository := &Repository{db: fakeContributionQueryExecutor{
		query: func(gotQuery string, gotArguments ...any) (
			rowsScanner,
			error,
		) {
			query = gotQuery
			arguments = append([]any(nil), gotArguments...)
			return rows, nil
		},
	}}
	sources, err := repository.ListPendingSources(
		context.Background(),
		tenantID,
		50,
	)
	if err != nil || len(sources) != 1 ||
		sources[0].CheckinID != checkinID || !rows.closed {
		t.Fatalf(`ListPendingSources() = %+v, %v`, sources, err)
	}
	for _, fragment := range []string{
		`checked_in_event.event_type = 'checked_in'`,
		`role_binding.role_code = 'host'`,
		`earned_entry.id IS NULL`,
		`reversal_entry.id IS NULL`,
		`current_checkin.tenant_id = $1`,
		`LIMIT $2`,
	} {
		if !strings.Contains(query, fragment) {
			t.Fatalf(`pending query missing %q: %s`, fragment, query)
		}
	}
	if !reflect.DeepEqual(arguments, []any{tenantID, 50}) {
		t.Fatalf(`pending arguments = %#v`, arguments)
	}
}

func repositoryEntry(t *testing.T) contribution.Entry {
	t.Helper()
	at := time.Now().UTC()
	value, err := contribution.Earn(contribution.EarnCommand{
		TenantID:         uuid.New(),
		PeopleProfileID:  uuid.New(),
		PeopleBindingID:  uuid.New(),
		PrincipalID:      uuid.New(),
		SeriesID:         uuid.New(),
		InstanceID:       uuid.New(),
		RegistrationID:   uuid.New(),
		SessionID:        uuid.New(),
		CheckinID:        uuid.New(),
		CheckinEventID:   uuid.New(),
		RoleBindingID:    uuid.New(),
		ContributionType: contribution.TypeHostCheckin,
		OccurredAt:       at,
		RecordedAt:       at.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf(`Earn() error = %v`, err)
	}
	return value
}

func entryScanValues(value contribution.Entry) []any {
	var reversalOf any
	if value.ReversalOfEntryID != nil {
		reversalOf = *value.ReversalOfEntryID
	}
	return []any{
		value.ID,
		value.TenantID,
		value.PeopleProfileID,
		value.PeopleBindingID,
		value.PrincipalID,
		value.SeriesID,
		value.InstanceID,
		value.RegistrationID,
		value.SessionID,
		value.CheckinID,
		value.CheckinEventID,
		value.RoleBindingID,
		value.ContributionType,
		value.EntryKind,
		value.Units,
		reversalOf,
		value.OccurredAt,
		value.RecordedAt,
	}
}

type fakeContributionQueryExecutor struct {
	query    func(string, ...any) (rowsScanner, error)
	queryRow func(string, ...any) rowScanner
}

func (executor fakeContributionQueryExecutor) queryContext(
	_ context.Context,
	query string,
	arguments ...any,
) (rowsScanner, error) {
	return executor.query(query, arguments...)
}

func (executor fakeContributionQueryExecutor) queryRowContext(
	_ context.Context,
	query string,
	arguments ...any,
) rowScanner {
	return executor.queryRow(query, arguments...)
}

type fakeContributionRow struct {
	values []any
	err    error
}

func (row *fakeContributionRow) Scan(destinations ...any) error {
	if row.err != nil {
		return row.err
	}
	return assignContributionValues(destinations, row.values)
}

type fakeContributionRows struct {
	values [][]any
	index  int
	err    error
	closed bool
}

func (rows *fakeContributionRows) Next() bool {
	if rows.index >= len(rows.values) {
		return false
	}
	rows.index++
	return true
}

func (rows *fakeContributionRows) Scan(destinations ...any) error {
	return assignContributionValues(
		destinations,
		rows.values[rows.index-1],
	)
}

func (rows *fakeContributionRows) Err() error {
	return rows.err
}

func (rows *fakeContributionRows) Close() error {
	rows.closed = true
	return nil
}

func assignContributionValues(destinations []any, values []any) error {
	if len(destinations) != len(values) {
		return errors.New(`fake Contribution scan arity mismatch`)
	}
	for index, destination := range destinations {
		if nullUUID, ok := destination.(*uuid.NullUUID); ok {
			if values[index] == nil {
				*nullUUID = uuid.NullUUID{}
				continue
			}
			nullUUID.UUID = values[index].(uuid.UUID)
			nullUUID.Valid = true
			continue
		}
		destinationValue := reflect.ValueOf(destination)
		if destinationValue.Kind() != reflect.Pointer ||
			destinationValue.IsNil() {
			return errors.New(`fake Contribution destination is not a pointer`)
		}
		sourceValue := reflect.ValueOf(values[index])
		if !sourceValue.IsValid() {
			return errors.New(`fake Contribution unexpected null`)
		}
		if sourceValue.Type().AssignableTo(destinationValue.Elem().Type()) {
			destinationValue.Elem().Set(sourceValue)
			continue
		}
		if sourceValue.Type().ConvertibleTo(destinationValue.Elem().Type()) {
			destinationValue.Elem().Set(
				sourceValue.Convert(destinationValue.Elem().Type()),
			)
			continue
		}
		return errors.New(`fake Contribution value type mismatch`)
	}
	return nil
}
