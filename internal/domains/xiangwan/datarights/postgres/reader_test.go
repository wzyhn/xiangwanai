package datarightspostgres

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/datarights"
	"github.com/google/uuid"
)

func TestReaderListsOnlyOwnerCasesWithCompleteImmutableHistory(t *testing.T) {
	t.Parallel()

	tenantID := uuid.New()
	principalID := uuid.New()
	now := time.Date(2026, time.September, 26, 2, 0, 0, 0, time.UTC)
	submission := datarights.Submission{
		TenantID:             tenantID,
		PrincipalID:          principalID,
		OperationKey:         uuid.New(),
		RequestType:          datarights.RequestTypeExport,
		RequestScope:         datarights.RequestScopeAll,
		PrivacyPolicyVersion: "privacy-v3",
	}
	fingerprint, err := datarights.SubmissionFingerprint(submission)
	if err != nil {
		t.Fatalf("SubmissionFingerprint() error = %v", err)
	}
	dataCase := datarights.Case{
		ID:                   uuid.New(),
		TenantID:             tenantID,
		PrincipalID:          principalID,
		OperationKey:         submission.OperationKey,
		RequestFingerprint:   fingerprint,
		RequestType:          submission.RequestType,
		RequestScope:         submission.RequestScope,
		PrivacyPolicyVersion: submission.PrivacyPolicyVersion,
		Status:               datarights.CaseStatusApproved,
		Version:              4,
		SubmittedAt:          now,
		UpdatedAt:            now.Add(3 * time.Hour),
	}
	actorID := principalID
	events := []datarights.CaseEvent{
		{
			ID:                 uuid.New(),
			TenantID:           tenantID,
			CaseID:             dataCase.ID,
			CaseVersion:        1,
			EventType:          datarights.EventTypeSubmitted,
			ResultingStatus:    datarights.CaseStatusSubmitted,
			ActorPrincipalID:   &actorID,
			PolicyBasisVersion: submission.PrivacyPolicyVersion,
			OccurredAt:         now,
		},
		{
			ID:              uuid.New(),
			TenantID:        tenantID,
			CaseID:          dataCase.ID,
			CaseVersion:     2,
			EventType:       datarights.EventTypeReviewStarted,
			ResultingStatus: datarights.CaseStatusInReview,
			OccurredAt:      now.Add(time.Hour),
		},
		{
			ID:                 uuid.New(),
			TenantID:           tenantID,
			CaseID:             dataCase.ID,
			CaseVersion:        3,
			EventType:          datarights.EventTypeApproved,
			ResultingStatus:    datarights.CaseStatusApproved,
			PolicyBasisVersion: "privacy-v3",
			OccurredAt:         now.Add(2 * time.Hour),
		},
		{
			ID:              uuid.New(),
			TenantID:        tenantID,
			CaseID:          dataCase.ID,
			CaseVersion:     4,
			EventType:       datarights.EventTypeDeliverySucceeded,
			ResultingStatus: datarights.CaseStatusApproved,
			DeliveryKind:    datarights.DeliveryKindExportArchive,
			DeliveryStatus:  datarights.DeliveryStatusDelivered,
			OccurredAt:      now.Add(3 * time.Hour),
		},
	}
	rowsData := make([][]any, 0, len(events))
	for _, event := range events {
		rowsData = append(rowsData, historyScanValues(dataCase, &event))
	}
	var capturedQuery string
	var capturedArgs []any
	reader := &Reader{db: &fakeCaseQueryExecutor{
		query: func(query string, args ...any) (rowsScanner, error) {
			capturedQuery = query
			capturedArgs = append([]any(nil), args...)
			return &fakeCaseRows{rows: rowsData}, nil
		},
	}}

	histories, err := reader.ListMine(context.Background(), tenantID, principalID)
	if err != nil {
		t.Fatalf("ListMine() error = %v", err)
	}
	if len(histories) != 1 || histories[0].Case.ID != dataCase.ID ||
		len(histories[0].Events) != 4 ||
		histories[0].Events[3].DeliveryStatus !=
			datarights.DeliveryStatusDelivered {
		t.Fatalf("ListMine() = %+v", histories)
	}
	for _, fragment := range []string{
		"data_case.tenant_id = $1",
		"data_case.principal_id = $2",
		"status = 'active'",
		"deleted_at IS NULL",
		"LIMIT $3",
		"data_event.case_version",
	} {
		if !strings.Contains(capturedQuery, fragment) {
			t.Fatalf("My Data Rights query missing %q: %s", fragment, capturedQuery)
		}
	}
	if !reflect.DeepEqual(
		capturedArgs,
		[]any{tenantID, principalID, datarights.MaxMyCases},
	) {
		t.Fatalf("ListMine() args = %#v", capturedArgs)
	}
}

func TestReaderReturnsNonNilEmptyCollectionAndRejectsIncompleteHistory(t *testing.T) {
	t.Parallel()

	tenantID := uuid.New()
	principalID := uuid.New()
	empty := &Reader{db: &fakeCaseQueryExecutor{
		query: func(string, ...any) (rowsScanner, error) {
			return &fakeCaseRows{}, nil
		},
	}}
	items, err := empty.ListMine(context.Background(), tenantID, principalID)
	if err != nil || items == nil || len(items) != 0 {
		t.Fatalf("ListMine(empty) = %+v, %v", items, err)
	}

	submission := datarights.Submission{
		TenantID:             tenantID,
		PrincipalID:          principalID,
		OperationKey:         uuid.New(),
		RequestType:          datarights.RequestTypeAccess,
		RequestScope:         datarights.RequestScopeProfile,
		PrivacyPolicyVersion: "privacy-v3",
	}
	fingerprint, err := datarights.SubmissionFingerprint(submission)
	if err != nil {
		t.Fatalf("SubmissionFingerprint() error = %v", err)
	}
	now := time.Now().UTC()
	dataCase := datarights.Case{
		ID:                   uuid.New(),
		TenantID:             tenantID,
		PrincipalID:          principalID,
		OperationKey:         submission.OperationKey,
		RequestFingerprint:   fingerprint,
		RequestType:          submission.RequestType,
		RequestScope:         submission.RequestScope,
		PrivacyPolicyVersion: submission.PrivacyPolicyVersion,
		Status:               datarights.CaseStatusSubmitted,
		Version:              1,
		SubmittedAt:          now,
		UpdatedAt:            now,
	}
	incomplete := &Reader{db: &fakeCaseQueryExecutor{
		query: func(string, ...any) (rowsScanner, error) {
			return &fakeCaseRows{rows: [][]any{
				historyScanValues(dataCase, nil),
			}}, nil
		},
	}}
	if _, err := incomplete.ListMine(
		context.Background(),
		tenantID,
		principalID,
	); !errors.Is(err, ErrMyCasesProjection) {
		t.Fatalf("ListMine(incomplete) error = %v", err)
	}
}

func TestReaderValidatesInputAndPropagatesQueryFailure(t *testing.T) {
	t.Parallel()

	if _, err := (*Reader)(nil).ListMine(
		context.Background(),
		uuid.New(),
		uuid.New(),
	); !errors.Is(err, ErrInvalidMyCasesReader) {
		t.Fatalf("ListMine(invalid) error = %v", err)
	}
	wantErr := errors.New("query failed")
	reader := &Reader{db: &fakeCaseQueryExecutor{
		query: func(string, ...any) (rowsScanner, error) {
			return nil, wantErr
		},
	}}
	if _, err := reader.ListMine(
		context.Background(),
		uuid.New(),
		uuid.New(),
	); !errors.Is(err, wantErr) {
		t.Fatalf("ListMine(query failure) error = %v", err)
	}
}

type fakeCaseQueryExecutor struct {
	query func(string, ...any) (rowsScanner, error)
}

func (executor *fakeCaseQueryExecutor) queryContext(
	_ context.Context,
	query string,
	args ...any,
) (rowsScanner, error) {
	return executor.query(query, args...)
}

type fakeCaseRows struct {
	rows  [][]any
	index int
	err   error
}

func (rows *fakeCaseRows) Next() bool {
	if rows.index >= len(rows.rows) {
		return false
	}
	rows.index++
	return true
}

func (rows *fakeCaseRows) Scan(destinations ...any) error {
	values := rows.rows[rows.index-1]
	if len(destinations) != len(values) {
		return errors.New("fake data-rights row destination count mismatch")
	}
	for index, destination := range destinations {
		target := reflect.ValueOf(destination)
		value := reflect.ValueOf(values[index])
		if target.Kind() != reflect.Pointer || !value.IsValid() ||
			!value.Type().AssignableTo(target.Elem().Type()) {
			return errors.New("fake data-rights row value type mismatch")
		}
		target.Elem().Set(value)
	}
	return nil
}

func (rows *fakeCaseRows) Err() error {
	return rows.err
}

func (*fakeCaseRows) Close() error {
	return nil
}

func historyScanValues(
	dataCase datarights.Case,
	event *datarights.CaseEvent,
) []any {
	completedAt := sql.NullTime{}
	if dataCase.CompletedAt != nil {
		completedAt = sql.NullTime{Time: *dataCase.CompletedAt, Valid: true}
	}
	values := []any{
		dataCase.ID,
		dataCase.TenantID,
		dataCase.PrincipalID,
		dataCase.OperationKey,
		append([]byte(nil), dataCase.RequestFingerprint[:]...),
		dataCase.RequestType,
		dataCase.RequestScope,
		dataCase.PrivacyPolicyVersion,
		dataCase.Status,
		dataCase.Version,
		dataCase.SubmittedAt,
		dataCase.UpdatedAt,
		completedAt,
	}
	if event == nil {
		return append(values,
			uuid.NullUUID{},
			uuid.NullUUID{},
			uuid.NullUUID{},
			sql.NullInt64{},
			sql.NullString{},
			sql.NullString{},
			uuid.NullUUID{},
			sql.NullString{},
			sql.NullString{},
			sql.NullString{},
			[]byte(nil),
			sql.NullTime{},
		)
	}
	actorID := uuid.NullUUID{}
	if event.ActorPrincipalID != nil {
		actorID = uuid.NullUUID{UUID: *event.ActorPrincipalID, Valid: true}
	}
	return append(values,
		uuid.NullUUID{UUID: event.ID, Valid: true},
		uuid.NullUUID{UUID: event.TenantID, Valid: true},
		uuid.NullUUID{UUID: event.CaseID, Valid: true},
		sql.NullInt64{Int64: event.CaseVersion, Valid: true},
		sql.NullString{String: string(event.EventType), Valid: true},
		sql.NullString{String: string(event.ResultingStatus), Valid: true},
		actorID,
		sql.NullString{
			String: event.PolicyBasisVersion,
			Valid:  event.PolicyBasisVersion != "",
		},
		sql.NullString{String: string(event.DeliveryKind), Valid: event.DeliveryKind != ""},
		sql.NullString{String: string(event.DeliveryStatus), Valid: event.DeliveryStatus != ""},
		append([]byte(nil), event.EvidenceDigest...),
		sql.NullTime{Time: event.OccurredAt, Valid: true},
	)
}
