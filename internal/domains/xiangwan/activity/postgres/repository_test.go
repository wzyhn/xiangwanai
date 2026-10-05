package activitypostgres

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/google/uuid"
)

func TestCreateMethodsApplyDomainDefaults(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 12, 2, 0, 0, 0, time.UTC)
	tenantID := uuid.New()
	seriesID := uuid.New()
	instanceID := uuid.New()

	tests := []struct {
		name       string
		row        []any
		call       func(*Repository) error
		wantStatus any
		statusArg  int
		versionArg int
	}{
		{
			name: "series",
			row: []any{
				seriesID, tenantID, "Series", activity.SeriesStatusDraft, false, true, 0,
				int64(0), int64(0), uuid.NullUUID{}, int64(1), now, now,
			},
			call: func(repository *Repository) error {
				created, err := repository.CreateSeries(context.Background(), activity.Series{
					TenantID: tenantID,
					Title:    "Series",
				})
				if err == nil && (created.ID != seriesID || created.Status != activity.SeriesStatusDraft || created.Version != 1) {
					t.Fatalf("CreateSeries() = %+v", created)
				}
				return err
			},
			wantStatus: activity.SeriesStatusDraft,
			statusArg:  3,
			versionArg: 7,
		},
		{
			name: "instance",
			row: []any{
				instanceID, tenantID, seriesID, 1, "Instance", activity.InstanceStatusDraft,
				sql.NullString{}, StringArrayJSON{}, "", []byte("[]"), int64(0), int64(1),
				sql.NullTime{}, sql.NullTime{}, sql.NullTime{}, int64(1), now, now,
			},
			call: func(repository *Repository) error {
				created, err := repository.CreateInstance(context.Background(), activity.Instance{
					TenantID: tenantID,
					SeriesID: seriesID,
					Title:    "Instance",
				})
				if err == nil && (created.ID != instanceID || created.Status != activity.InstanceStatusDraft || created.Version != 1) {
					t.Fatalf("CreateInstance() = %+v", created)
				}
				return err
			},
			wantStatus: activity.InstanceStatusDraft,
			statusArg:  5,
			versionArg: 14,
		},
		{
			name: "session",
			row: sessionScanValues(activity.Session{
				ID:         uuid.New(),
				TenantID:   tenantID,
				InstanceID: instanceID,
				Title:      "Session",
				Status:     activity.SessionStatusDraft,
				Version:    1,
				CreatedAt:  now,
				UpdatedAt:  now,
			}),
			call: func(repository *Repository) error {
				created, err := repository.CreateSession(context.Background(), activity.Session{
					TenantID:   tenantID,
					InstanceID: instanceID,
					Title:      "Session",
				})
				if err == nil && (created.ID == uuid.Nil || created.Status != activity.SessionStatusDraft || created.Version != 1) {
					t.Fatalf("CreateSession() = %+v", created)
				}
				return err
			},
			wantStatus: activity.SessionStatusDraft,
			statusArg:  4,
			versionArg: 25,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var capturedArgs []any
			executor := &fakeQueryExecutor{
				queryRow: func(_ string, args ...any) rowScanner {
					capturedArgs = append([]any(nil), args...)
					return &fakeRow{values: test.row}
				},
			}
			if err := test.call(&Repository{db: executor}); err != nil {
				t.Fatalf("create error = %v", err)
			}
			if id, ok := capturedArgs[0].(uuid.UUID); !ok || id == uuid.Nil {
				t.Fatalf("generated id argument = %#v", capturedArgs[0])
			}
			if capturedArgs[test.statusArg] != test.wantStatus {
				t.Fatalf("status argument = %#v, want %#v", capturedArgs[test.statusArg], test.wantStatus)
			}
			if capturedArgs[test.versionArg] != int64(1) {
				t.Fatalf("version argument = %#v, want 1", capturedArgs[test.versionArg])
			}
		})
	}
}

func TestGetMethodsTranslateOnlyNoRows(t *testing.T) {
	t.Parallel()

	tenantID := uuid.New()
	targetID := uuid.New()
	tests := []struct {
		name    string
		call    func(*Repository) error
		wantErr error
	}{
		{name: "series", call: func(r *Repository) error { _, err := r.GetSeries(context.Background(), tenantID, targetID); return err }, wantErr: ErrSeriesNotFound},
		{name: "instance", call: func(r *Repository) error {
			_, err := r.GetInstance(context.Background(), tenantID, targetID)
			return err
		}, wantErr: ErrInstanceNotFound},
		{name: "session", call: func(r *Repository) error {
			_, err := r.GetSession(context.Background(), tenantID, targetID)
			return err
		}, wantErr: ErrSessionNotFound},
		{name: "publication event", call: func(r *Repository) error {
			_, err := r.GetPublicationEvent(context.Background(), tenantID, targetID, 1)
			return err
		}, wantErr: ErrPublicationEventNotFound},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			repository := &Repository{db: &fakeQueryExecutor{
				queryRow: func(_ string, _ ...any) rowScanner { return &fakeRow{err: sql.ErrNoRows} },
			}}
			if err := test.call(repository); !errors.Is(err, test.wantErr) {
				t.Fatalf("error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestLockSessionUsesTenantScopeAndRowLock(t *testing.T) {
	t.Parallel()

	tenantID := uuid.New()
	sessionID := uuid.New()
	instanceID := uuid.New()
	now := time.Date(2026, time.September, 12, 2, 0, 0, 0, time.UTC)
	capacity := 20

	var capturedQuery string
	var capturedArgs []any
	repository := &Repository{db: &fakeQueryExecutor{
		queryRow: func(query string, args ...any) rowScanner {
			capturedQuery = query
			capturedArgs = append([]any(nil), args...)
			return &fakeRow{values: sessionScanValues(activity.Session{
				ID:         sessionID,
				TenantID:   tenantID,
				InstanceID: instanceID,
				Title:      "Session",
				Status:     activity.SessionStatusDraft,
				Capacity:   &capacity,
				Version:    1,
				CreatedAt:  now,
				UpdatedAt:  now,
			})}
		},
	}}

	got, err := repository.LockSession(context.Background(), tenantID, sessionID)
	if err != nil {
		t.Fatalf("LockSession() error = %v", err)
	}
	if got.ID != sessionID || got.Capacity == nil || *got.Capacity != capacity {
		t.Fatalf("LockSession() = %+v", got)
	}
	if !strings.Contains(capturedQuery, "tenant_id = $1 AND id = $2") || !strings.Contains(capturedQuery, "FOR UPDATE") {
		t.Fatalf("lock query is not tenant-scoped or locked: %s", capturedQuery)
	}
	if !reflect.DeepEqual(capturedArgs, []any{tenantID, sessionID}) {
		t.Fatalf("lock args = %#v", capturedArgs)
	}
}

func TestListInstanceSessionsScansNullableFacts(t *testing.T) {
	t.Parallel()

	tenantID := uuid.New()
	instanceID := uuid.New()
	now := time.Date(2026, time.September, 12, 2, 0, 0, 0, time.UTC)
	start := now.Add(time.Hour)
	end := start.Add(2 * time.Hour)
	capacity := 8
	groupMinimum := 3
	priceCents := int64(5000)
	deliveryMode := activity.DeliveryModeOffline
	area := activity.AreaCodeHeping
	venueName := "Maker Space"
	address := "Tianjin"
	longitude := 117.2
	latitude := 39.1

	want := activity.Session{
		ID:                         uuid.New(),
		TenantID:                   tenantID,
		InstanceID:                 instanceID,
		Title:                      "Session",
		Status:                     activity.SessionStatusDraft,
		SessionStartAt:             &start,
		SessionEndAt:               &end,
		Capacity:                   &capacity,
		GroupMinimum:               &groupMinimum,
		PriceCents:                 &priceCents,
		DeliveryMode:               &deliveryMode,
		Area:                       &area,
		VenueName:                  &venueName,
		Address:                    &address,
		Longitude:                  &longitude,
		Latitude:                   &latitude,
		ConfirmedRegistrationCount: 2,
		ActiveHoldCount:            1,
		SortOrder:                  4,
		Version:                    1,
		CreatedAt:                  now,
		UpdatedAt:                  now,
	}
	executor := &fakeQueryExecutor{
		queryRows: func(query string, args ...any) (rowsScanner, error) {
			if !strings.Contains(query, "ORDER BY sort_order ASC") {
				t.Fatalf("list query has no stable order: %s", query)
			}
			if !reflect.DeepEqual(args, []any{tenantID, instanceID}) {
				t.Fatalf("list args = %#v", args)
			}
			return &fakeRows{rows: [][]any{sessionScanValues(want)}}, nil
		},
	}

	got, err := (&Repository{db: executor}).ListInstanceSessions(context.Background(), tenantID, instanceID)
	if err != nil {
		t.Fatalf("ListInstanceSessions() error = %v", err)
	}
	if len(got) != 1 || !reflect.DeepEqual(got[0], want) {
		t.Fatalf("ListInstanceSessions() = %+v, want %+v", got, want)
	}
}

func TestRepositorySentinelErrorsRemainDistinct(t *testing.T) {
	t.Parallel()

	errorsToCheck := []error{
		ErrSeriesNotFound,
		ErrInstanceNotFound,
		ErrSessionNotFound,
		ErrSessionDetailNotFound,
		ErrPublicationEventNotFound,
	}
	for firstIndex, first := range errorsToCheck {
		for secondIndex, second := range errorsToCheck {
			if firstIndex != secondIndex && errors.Is(first, second) {
				t.Fatalf("%v unexpectedly matches %v", first, second)
			}
		}
	}
}

func TestCreatePublicationEventGeneratesID(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 12, 4, 0, 0, 0, time.UTC)
	event := activity.PublicationEvent{
		TenantID:           uuid.New(),
		SeriesID:           uuid.New(),
		InstanceID:         uuid.New(),
		PublicationVersion: 2,
		SessionCount:       3,
		CandidateDigest:    strings.Repeat("a", 64),
		PublishedBy:        uuid.New(),
		PublishedAt:        now,
	}

	var capturedQuery string
	var capturedArgs []any
	repository := &Repository{db: &fakeQueryExecutor{
		queryRow: func(query string, args ...any) rowScanner {
			capturedQuery = query
			capturedArgs = append([]any(nil), args...)
			created := event
			created.ID = args[0].(uuid.UUID)
			created.CreatedAt = now
			return &fakeRow{values: publicationEventScanValues(created)}
		},
	}}

	got, err := repository.CreatePublicationEvent(context.Background(), event)
	if err != nil {
		t.Fatalf("CreatePublicationEvent() error = %v", err)
	}
	if got.ID == uuid.Nil || got.TenantID != event.TenantID || got.CandidateDigest != event.CandidateDigest {
		t.Fatalf("CreatePublicationEvent() = %+v", got)
	}
	if !strings.Contains(capturedQuery, "INSERT INTO xiangwan_publication_events") {
		t.Fatalf("create query targets wrong table: %s", capturedQuery)
	}
	if len(capturedArgs) != 9 || capturedArgs[0] != got.ID || capturedArgs[4] != int64(2) {
		t.Fatalf("create args = %#v", capturedArgs)
	}
}

func TestGetPublicationEventUsesTenantInstanceAndVersion(t *testing.T) {
	t.Parallel()

	event := activity.PublicationEvent{
		ID:                 uuid.New(),
		TenantID:           uuid.New(),
		SeriesID:           uuid.New(),
		InstanceID:         uuid.New(),
		PublicationVersion: 4,
		SessionCount:       2,
		CandidateDigest:    strings.Repeat("b", 64),
		PublishedBy:        uuid.New(),
		PublishedAt:        time.Date(2026, time.September, 12, 4, 0, 0, 0, time.UTC),
		CreatedAt:          time.Date(2026, time.September, 12, 4, 0, 1, 0, time.UTC),
	}

	var capturedQuery string
	var capturedArgs []any
	repository := &Repository{db: &fakeQueryExecutor{
		queryRow: func(query string, args ...any) rowScanner {
			capturedQuery = query
			capturedArgs = append([]any(nil), args...)
			return &fakeRow{values: publicationEventScanValues(event)}
		},
	}}

	got, err := repository.GetPublicationEvent(
		context.Background(),
		event.TenantID,
		event.InstanceID,
		event.PublicationVersion,
	)
	if err != nil {
		t.Fatalf("GetPublicationEvent() error = %v", err)
	}
	if !reflect.DeepEqual(got, event) {
		t.Fatalf("GetPublicationEvent() = %+v, want %+v", got, event)
	}
	if !strings.Contains(capturedQuery, "tenant_id = $1 AND instance_id = $2 AND publication_version = $3") {
		t.Fatalf("get query is not tenant/version scoped: %s", capturedQuery)
	}
	if !reflect.DeepEqual(capturedArgs, []any{event.TenantID, event.InstanceID, event.PublicationVersion}) {
		t.Fatalf("get args = %#v", capturedArgs)
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

type fakeRows struct {
	rows  [][]any
	index int
	err   error
}

func (rows *fakeRows) Next() bool {
	if rows.index >= len(rows.rows) {
		return false
	}
	rows.index++
	return true
}

func (rows *fakeRows) Scan(destinations ...any) error {
	return (&fakeRow{values: rows.rows[rows.index-1]}).Scan(destinations...)
}

func (rows *fakeRows) Err() error {
	return rows.err
}

func (*fakeRows) Close() error {
	return nil
}

func sessionScanValues(session activity.Session) []any {
	return []any{
		session.ID,
		session.TenantID,
		session.InstanceID,
		session.Title,
		session.Status,
		nullTime(session.RegistrationStartAt),
		nullTime(session.RegistrationEndAt),
		nullTime(session.SessionStartAt),
		nullTime(session.SessionEndAt),
		nullInt(session.Capacity),
		nullInt(session.GroupMinimum),
		nullInt(session.LowStockThreshold),
		nullInt64(session.PriceCents),
		nullDeliveryMode(session.DeliveryMode),
		nullAreaCode(session.Area),
		nullString(session.VenueName),
		nullString(session.Address),
		nullFloat64(session.Longitude),
		nullFloat64(session.Latitude),
		nullString(session.OnlineParticipationMode),
		nullBool(session.OnlineParticipationCompliant),
		session.ConfirmedRegistrationCount,
		session.ActiveHoldCount,
		session.SortOrder,
		nullTime(session.PublishedAt),
		session.Version,
		session.CreatedAt,
		session.UpdatedAt,
	}
}

func nullTime(value *time.Time) sql.NullTime {
	if value == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *value, Valid: true}
}

func nullInt(value *int) sql.NullInt64 {
	if value == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*value), Valid: true}
}

func nullInt64(value *int64) sql.NullInt64 {
	if value == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *value, Valid: true}
}

func nullDeliveryMode(value *activity.DeliveryMode) sql.NullString {
	if value == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: string(*value), Valid: true}
}

func nullAreaCode(value *activity.AreaCode) sql.NullString {
	if value == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: string(*value), Valid: true}
}

func nullString(value *string) sql.NullString {
	if value == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *value, Valid: true}
}

func nullFloat64(value *float64) sql.NullFloat64 {
	if value == nil {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: *value, Valid: true}
}

func nullBool(value *bool) sql.NullBool {
	if value == nil {
		return sql.NullBool{}
	}
	return sql.NullBool{Bool: *value, Valid: true}
}

func publicationEventScanValues(event activity.PublicationEvent) []any {
	return []any{
		event.ID,
		event.TenantID,
		event.SeriesID,
		event.InstanceID,
		event.PublicationVersion,
		event.SessionCount,
		event.CandidateDigest,
		event.PublishedBy,
		event.PublishedAt,
		event.CreatedAt,
	}
}
