package peoplepostgres

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/people"
	"github.com/google/uuid"
)

func TestProfileRepositoryPersistsAndLocksCompleteProfile(t *testing.T) {
	t.Parallel()

	current := knownProfile(t)
	reviewed, err := people.ReviewProfile(current, people.ReviewProfileCommand{
		Decision: people.ModerationStatusApproved,
		ActorID:  uuid.New(),
		At:       current.UpdatedAt.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf(`ReviewProfile() error = %v`, err)
	}
	var queries []string
	var arguments [][]any
	call := 0
	repository := &Repository{db: &fakeQueryExecutor{
		queryRow: func(query string, args ...any) rowScanner {
			queries = append(queries, query)
			arguments = append(arguments, append([]any(nil), args...))
			call++
			if call < 3 {
				return &fakeRow{values: profileScanValues(current)}
			}
			return &fakeRow{values: profileScanValues(reviewed)}
		},
	}}

	created, err := repository.CreateProfile(context.Background(), current)
	if err != nil {
		t.Fatalf(`CreateProfile() error = %v`, err)
	}
	locked, err := repository.GetProfileForUpdate(
		context.Background(),
		current.TenantID,
		current.ID,
	)
	if err != nil {
		t.Fatalf(`GetProfileForUpdate() error = %v`, err)
	}
	updated, err := repository.UpdateProfile(
		context.Background(),
		reviewed,
		current.Version,
	)
	if err != nil {
		t.Fatalf(`UpdateProfile() error = %v`, err)
	}
	if !reflect.DeepEqual(created, current) ||
		!reflect.DeepEqual(locked, current) ||
		!reflect.DeepEqual(updated, reviewed) {
		t.Fatalf(`profiles = %+v %+v %+v`, created, locked, updated)
	}
	if len(queries) != 3 ||
		!strings.Contains(queries[0], `INSERT INTO xiangwan_people_profiles`) ||
		!strings.Contains(queries[1], `tenant_id = $1 AND id = $2`) ||
		!strings.Contains(queries[1], `FOR UPDATE`) ||
		!strings.Contains(queries[2], `version = version + 1`) ||
		len(arguments[0]) != 14 ||
		len(arguments[2]) != 12 ||
		arguments[2][11] != current.Version {
		t.Fatalf(`profile queries/args = %#v %#v`, queries, arguments)
	}
}

func TestPublishedProfileReadIsTenantScopedAndModerationFenced(t *testing.T) {
	t.Parallel()

	published := knownReviewedProfile(t)
	var query string
	var arguments []any
	repository := &Repository{db: &fakeQueryExecutor{
		queryRow: func(candidate string, args ...any) rowScanner {
			query = candidate
			arguments = append([]any(nil), args...)
			return &fakeRow{values: profileScanValues(published)}
		},
	}}

	got, err := repository.GetPublishedProfile(
		context.Background(),
		published.TenantID,
		published.ID,
	)
	if err != nil || !reflect.DeepEqual(got, published) {
		t.Fatalf(`GetPublishedProfile() = %+v, %v`, got, err)
	}
	if !strings.Contains(query, `tenant_id = $1`) ||
		!strings.Contains(query, `profile_status = 'published'`) ||
		!strings.Contains(query, `moderation_status = 'approved'`) ||
		!reflect.DeepEqual(
			arguments,
			[]any{published.TenantID, published.ID},
		) {
		t.Fatalf(`published query/args = %q %#v`, query, arguments)
	}
}

func TestBindingRepositoryUsesDigestAndOptimisticTerminalUpdate(t *testing.T) {
	t.Parallel()

	current := knownBinding(t)
	revoked, err := people.RevokeBinding(current, people.RevokeBindingCommand{
		ActorID: uuid.New(),
		Reason:  `identity corrected`,
		At:      current.UpdatedAt.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf(`RevokeBinding() error = %v`, err)
	}
	var queries []string
	var arguments [][]any
	call := 0
	repository := &Repository{db: &fakeQueryExecutor{
		queryRow: func(query string, args ...any) rowScanner {
			queries = append(queries, query)
			arguments = append(arguments, append([]any(nil), args...))
			call++
			if call < 4 {
				return &fakeRow{values: bindingScanValues(current)}
			}
			return &fakeRow{values: bindingScanValues(revoked)}
		},
	}}

	created, err := repository.CreateBinding(context.Background(), current)
	if err != nil {
		t.Fatalf(`CreateBinding() error = %v`, err)
	}
	byProfile, err := repository.GetActiveBindingByProfile(
		context.Background(),
		current.TenantID,
		current.PeopleProfileID,
	)
	if err != nil {
		t.Fatalf(`GetActiveBindingByProfile() error = %v`, err)
	}
	byPrincipal, err := repository.GetActiveBindingByPrincipal(
		context.Background(),
		current.TenantID,
		current.PrincipalID,
	)
	if err != nil {
		t.Fatalf(`GetActiveBindingByPrincipal() error = %v`, err)
	}
	gotRevoked, err := repository.RevokeBinding(
		context.Background(),
		revoked,
		current.Version,
	)
	if err != nil {
		t.Fatalf(`RevokeBinding(repository) error = %v`, err)
	}
	if !reflect.DeepEqual(created, current) ||
		!reflect.DeepEqual(byProfile, current) ||
		!reflect.DeepEqual(byPrincipal, current) ||
		!reflect.DeepEqual(gotRevoked, revoked) {
		t.Fatalf(
			`bindings = %+v %+v %+v %+v`,
			created,
			byProfile,
			byPrincipal,
			gotRevoked,
		)
	}
	if len(queries) != 4 ||
		!strings.Contains(queries[0], `INSERT INTO xiangwan_people_bindings`) ||
		len(arguments[0]) != 14 ||
		!bytes.Equal(
			arguments[0][4].([]byte),
			current.EvidenceDigest[:],
		) ||
		!strings.Contains(queries[1], `people_profile_id = $2`) ||
		!strings.Contains(queries[2], `principal_id = $2`) ||
		!strings.Contains(queries[3], `version = version + 1`) ||
		arguments[3][7] != current.Version {
		t.Fatalf(`binding queries/args = %#v %#v`, queries, arguments)
	}
}

func TestPublishedProfileReadRequiresApprovedPublishedState(t *testing.T) {
	t.Parallel()

	published := knownReviewedProfile(t)
	var query string
	var arguments []any
	repository := &Repository{db: &fakeQueryExecutor{
		queryRow: func(gotQuery string, args ...any) rowScanner {
			query = gotQuery
			arguments = append([]any(nil), args...)
			return &fakeRow{values: profileScanValues(published)}
		},
	}}

	got, err := repository.GetPublishedProfile(
		context.Background(),
		published.TenantID,
		published.ID,
	)
	if err != nil || !reflect.DeepEqual(got, published) {
		t.Fatalf(`GetPublishedProfile() = %+v, %v`, got, err)
	}
	if !strings.Contains(query, `profile_status = 'published'`) ||
		!strings.Contains(query, `moderation_status = 'approved'`) ||
		len(arguments) != 2 ||
		arguments[0] != published.TenantID ||
		arguments[1] != published.ID {
		t.Fatalf(`published profile query/args = %q %#v`, query, arguments)
	}
}

func TestRoleRepositoryKeepsCanonicalRolesAndStableInstanceOrder(
	t *testing.T,
) {
	t.Parallel()

	host := knownRoleBinding(t, people.InstanceRoleHost)
	guest := knownRoleBinding(t, people.InstanceRoleInvitedGuest)
	guest.TenantID = host.TenantID
	guest.SeriesID = host.SeriesID
	guest.InstanceID = host.InstanceID
	guest.GrantedAt = host.GrantedAt.Add(time.Minute)
	guest.CreatedAt = guest.GrantedAt
	guest.UpdatedAt = guest.GrantedAt
	revoked, err := people.RevokeInstanceRoleBinding(
		host,
		people.RevokeInstanceRoleBindingCommand{
			ActorID: uuid.New(),
			Reason:  `role corrected`,
			At:      guest.GrantedAt.Add(time.Minute),
		},
	)
	if err != nil {
		t.Fatalf(`RevokeInstanceRoleBinding() error = %v`, err)
	}
	var queries []string
	var arguments [][]any
	call := 0
	rows := newFakeRows(roleBindingScanValues(host), roleBindingScanValues(guest))
	repository := &Repository{db: &fakeQueryExecutor{
		queryRow: func(query string, args ...any) rowScanner {
			queries = append(queries, query)
			arguments = append(arguments, append([]any(nil), args...))
			call++
			if call < 3 {
				return &fakeRow{values: roleBindingScanValues(host)}
			}
			return &fakeRow{values: roleBindingScanValues(revoked)}
		},
		queryRows: func(query string, args ...any) (rowsScanner, error) {
			queries = append(queries, query)
			arguments = append(arguments, append([]any(nil), args...))
			return rows, nil
		},
	}}

	created, err := repository.CreateInstanceRoleBinding(
		context.Background(),
		host,
	)
	if err != nil {
		t.Fatalf(`CreateInstanceRoleBinding() error = %v`, err)
	}
	active, err := repository.GetActiveInstanceRoleBindingForUpdate(
		context.Background(),
		host.TenantID,
		host.InstanceID,
		host.PrincipalID,
		host.RoleCode,
	)
	if err != nil {
		t.Fatalf(`GetActiveInstanceRoleBindingForUpdate() error = %v`, err)
	}
	list, err := repository.ListActiveInstanceRoleBindings(
		context.Background(),
		host.TenantID,
		host.InstanceID,
	)
	if err != nil {
		t.Fatalf(`ListActiveInstanceRoleBindings() error = %v`, err)
	}
	gotRevoked, err := repository.RevokeInstanceRoleBinding(
		context.Background(),
		revoked,
		host.Version,
	)
	if err != nil {
		t.Fatalf(`RevokeInstanceRoleBinding(repository) error = %v`, err)
	}
	if !reflect.DeepEqual(created, host) ||
		!reflect.DeepEqual(active, host) ||
		!reflect.DeepEqual(list, []people.InstanceRoleBinding{host, guest}) ||
		!reflect.DeepEqual(gotRevoked, revoked) ||
		!rows.closed {
		t.Fatalf(
			`roles = %+v %+v %+v %+v rows.closed=%v`,
			created,
			active,
			list,
			gotRevoked,
			rows.closed,
		)
	}
	if len(queries) != 4 ||
		!strings.Contains(
			queries[0],
			`INSERT INTO xiangwan_instance_role_bindings`,
		) ||
		!strings.Contains(queries[1], `role_code = $4`) ||
		!strings.Contains(queries[1], `FOR UPDATE`) ||
		!strings.Contains(queries[2], `ORDER BY granted_at ASC, id ASC`) ||
		!strings.Contains(queries[3], `version = version + 1`) ||
		arguments[3][7] != host.Version {
		t.Fatalf(`role queries/args = %#v %#v`, queries, arguments)
	}
}

func TestPeopleRepositoryMapsMissingRowsAndRejectsInvalidDigest(t *testing.T) {
	t.Parallel()

	repository := &Repository{db: &fakeQueryExecutor{
		queryRow: func(string, ...any) rowScanner {
			return &fakeRow{err: sql.ErrNoRows}
		},
	}}
	if _, err := repository.GetProfile(
		context.Background(),
		uuid.New(),
		uuid.New(),
	); !errors.Is(err, ErrProfileNotFound) {
		t.Fatalf(`GetProfile() error = %v`, err)
	}
	if _, err := repository.GetActiveBindingByPrincipal(
		context.Background(),
		uuid.New(),
		uuid.New(),
	); !errors.Is(err, ErrBindingNotFound) {
		t.Fatalf(`GetActiveBindingByPrincipal() error = %v`, err)
	}
	if _, err := repository.GetActiveInstanceRoleBinding(
		context.Background(),
		uuid.New(),
		uuid.New(),
		uuid.New(),
		people.InstanceRoleHost,
	); !errors.Is(err, ErrRoleBindingNotFound) {
		t.Fatalf(`GetActiveInstanceRoleBinding() error = %v`, err)
	}
	if _, err := repository.UpdateProfile(
		context.Background(),
		knownReviewedProfile(t),
		1,
	); !errors.Is(err, ErrProfileVersionConflict) {
		t.Fatalf(`UpdateProfile() error = %v`, err)
	}
	currentBinding := knownBinding(t)
	revokedBinding, err := people.RevokeBinding(
		currentBinding,
		people.RevokeBindingCommand{
			ActorID: uuid.New(),
			Reason:  `corrected`,
			At:      currentBinding.UpdatedAt.Add(time.Minute),
		},
	)
	if err != nil {
		t.Fatalf(`RevokeBinding() error = %v`, err)
	}
	if _, err := repository.RevokeBinding(
		context.Background(),
		revokedBinding,
		1,
	); !errors.Is(err, ErrBindingVersionConflict) {
		t.Fatalf(`RevokeBinding(repository) error = %v`, err)
	}

	values := bindingScanValues(currentBinding)
	values[4] = []byte{1, 2, 3}
	if _, err := scanBinding(&fakeRow{values: values}); !errors.Is(
		err,
		people.ErrInvalidBinding,
	) {
		t.Fatalf(`scanBinding() error = %v`, err)
	}
}

func TestPeopleRepositoryRejectsForgedLifecycleWritesBeforeSQL(t *testing.T) {
	t.Parallel()

	calls := 0
	repository := &Repository{db: &fakeQueryExecutor{
		queryRow: func(string, ...any) rowScanner {
			calls++
			return &fakeRow{err: errors.New(`unexpected SQL`)}
		},
	}}

	profile := knownProfile(t)
	reviewed := knownReviewedProfile(t)
	if _, err := repository.CreateProfile(
		context.Background(),
		reviewed,
	); !errors.Is(err, people.ErrInvalidProfile) {
		t.Fatalf(`CreateProfile(reviewed) error = %v`, err)
	}
	if _, err := repository.UpdateProfile(
		context.Background(),
		reviewed,
		profile.Version+1,
	); !errors.Is(err, people.ErrInvalidProfile) {
		t.Fatalf(`UpdateProfile(forged version) error = %v`, err)
	}

	binding := knownBinding(t)
	revokedBinding, err := people.RevokeBinding(
		binding,
		people.RevokeBindingCommand{
			ActorID: uuid.New(),
			Reason:  `corrected`,
			At:      binding.UpdatedAt.Add(time.Minute),
		},
	)
	if err != nil {
		t.Fatalf(`RevokeBinding() error = %v`, err)
	}
	if _, err := repository.CreateBinding(
		context.Background(),
		revokedBinding,
	); !errors.Is(err, people.ErrInvalidBinding) {
		t.Fatalf(`CreateBinding(revoked) error = %v`, err)
	}
	if _, err := repository.RevokeBinding(
		context.Background(),
		revokedBinding,
		binding.Version+1,
	); !errors.Is(err, people.ErrInvalidBinding) {
		t.Fatalf(`RevokeBinding(forged version) error = %v`, err)
	}

	roleBinding := knownRoleBinding(t, people.InstanceRoleHost)
	revokedRole, err := people.RevokeInstanceRoleBinding(
		roleBinding,
		people.RevokeInstanceRoleBindingCommand{
			ActorID: uuid.New(),
			Reason:  `corrected`,
			At:      roleBinding.UpdatedAt.Add(time.Minute),
		},
	)
	if err != nil {
		t.Fatalf(`RevokeInstanceRoleBinding() error = %v`, err)
	}
	if _, err := repository.CreateInstanceRoleBinding(
		context.Background(),
		revokedRole,
	); !errors.Is(err, people.ErrInvalidInstanceRoleBinding) {
		t.Fatalf(`CreateInstanceRoleBinding(revoked) error = %v`, err)
	}
	if _, err := repository.RevokeInstanceRoleBinding(
		context.Background(),
		revokedRole,
		roleBinding.Version+1,
	); !errors.Is(err, people.ErrInvalidInstanceRoleBinding) {
		t.Fatalf(`RevokeInstanceRoleBinding(forged version) error = %v`, err)
	}
	if calls != 0 {
		t.Fatalf(`SQL calls = %d, want 0`, calls)
	}
}

func knownProfile(t *testing.T) people.Profile {
	t.Helper()
	value, err := people.NewProfile(people.NewProfileCommand{
		TenantID:     uuid.New(),
		DisplayName:  `Lin`,
		Headline:     `Community host`,
		Introduction: `Builds useful gatherings.`,
		ActorID:      uuid.New(),
		At:           time.Date(2026, time.September, 13, 10, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf(`NewProfile() error = %v`, err)
	}
	return value
}

func knownReviewedProfile(t *testing.T) people.Profile {
	t.Helper()
	current := knownProfile(t)
	value, err := people.ReviewProfile(current, people.ReviewProfileCommand{
		Decision: people.ModerationStatusApproved,
		ActorID:  uuid.New(),
		At:       current.UpdatedAt.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf(`ReviewProfile() error = %v`, err)
	}
	return value
}

func knownBinding(t *testing.T) people.Binding {
	t.Helper()
	value, err := people.NewBinding(people.NewBindingCommand{
		TenantID:        uuid.New(),
		PeopleProfileID: uuid.New(),
		PrincipalID:     uuid.New(),
		EvidenceDigest:  people.EvidenceDigest{0x71, 0x42},
		ActorID:         uuid.New(),
		At:              time.Date(2026, time.September, 13, 10, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf(`NewBinding() error = %v`, err)
	}
	return value
}

func knownRoleBinding(
	t *testing.T,
	roleCode people.InstanceRoleCode,
) people.InstanceRoleBinding {
	t.Helper()
	value, err := people.NewInstanceRoleBinding(
		people.NewInstanceRoleBindingCommand{
			TenantID:    uuid.New(),
			SeriesID:    uuid.New(),
			InstanceID:  uuid.New(),
			PrincipalID: uuid.New(),
			RoleCode:    roleCode,
			GrantReason: `verified by operations`,
			ActorID:     uuid.New(),
			At: time.Date(
				2026,
				time.September,
				13,
				10,
				0,
				0,
				0,
				time.UTC,
			),
		},
	)
	if err != nil {
		t.Fatalf(`NewInstanceRoleBinding() error = %v`, err)
	}
	return value
}

func profileScanValues(value people.Profile) []any {
	return []any{
		value.ID,
		value.TenantID,
		value.DisplayName,
		nullString(value.Headline),
		value.Introduction,
		value.ProfileStatus,
		value.ModerationStatus,
		nullUUID(value.ModeratedBy),
		nullTime(value.ModeratedAt),
		value.CreatedBy,
		value.UpdatedBy,
		value.Version,
		value.CreatedAt,
		value.UpdatedAt,
	}
}

func bindingScanValues(value people.Binding) []any {
	return []any{
		value.ID,
		value.TenantID,
		value.PeopleProfileID,
		value.PrincipalID,
		append([]byte(nil), value.EvidenceDigest[:]...),
		value.BindingStatus,
		value.BoundBy,
		value.BoundAt,
		nullUUID(value.RevokedBy),
		nullTime(value.RevokedAt),
		nullString(value.RevocationReason),
		value.Version,
		value.CreatedAt,
		value.UpdatedAt,
	}
}

func roleBindingScanValues(value people.InstanceRoleBinding) []any {
	return []any{
		value.ID,
		value.TenantID,
		value.SeriesID,
		value.InstanceID,
		value.PrincipalID,
		value.RoleCode,
		value.RoleStatus,
		value.GrantReason,
		value.GrantedBy,
		value.GrantedAt,
		nullUUID(value.RevokedBy),
		nullTime(value.RevokedAt),
		nullString(value.RevocationReason),
		value.Version,
		value.CreatedAt,
		value.UpdatedAt,
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
