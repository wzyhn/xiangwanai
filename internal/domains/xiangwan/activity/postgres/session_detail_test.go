package activitypostgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/google/uuid"
)

func TestGetSessionDetailFactsUsesDirectTenantSessionIdentity(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 12, 4, 0, 0, 0, time.UTC)
	want := validPostgresSessionDetailFacts(now)
	want.CurrentPublicInstance = false
	tenantID := uuid.New()
	var capturedQuery string
	var capturedArgs []any
	repository := &Repository{db: &fakeQueryExecutor{
		queryRow: func(query string, args ...any) rowScanner {
			capturedQuery = query
			capturedArgs = append([]any(nil), args...)
			return &fakeRow{values: sessionDetailFactScanValues(want)}
		},
	}}

	got, err := repository.GetSessionDetailFacts(context.Background(), tenantID, want.SessionID)
	if err != nil {
		t.Fatalf("GetSessionDetailFacts() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("GetSessionDetailFacts() = %+v, want %+v", got, want)
	}
	for _, fragment := range []string{
		"activity_session.tenant_id = $1",
		"activity_session.id = $2",
		"activity_instance.tenant_id = activity_session.tenant_id",
		"activity_series.tenant_id = activity_instance.tenant_id",
		"COALESCE(activity_series.current_public_instance_id = activity_instance.id, FALSE)",
		"activity_series.successful_published_instance_count",
		"activity_series.historical_registration_count",
	} {
		if !strings.Contains(capturedQuery, fragment) {
			t.Fatalf("detail query missing %q: %s", fragment, capturedQuery)
		}
	}
	if strings.Contains(capturedQuery, "AND activity_instance.id = activity_series.current_public_instance_id") {
		t.Fatalf("detail query incorrectly falls forward to current Instance: %s", capturedQuery)
	}
	if !reflect.DeepEqual(capturedArgs, []any{tenantID, want.SessionID}) {
		t.Fatalf("detail query args = %#v", capturedArgs)
	}

	detail, err := repository.ReadSessionDetail(context.Background(), tenantID, want.SessionID, now)
	if err != nil {
		t.Fatalf("ReadSessionDetail() error = %v", err)
	}
	if detail.SessionID != want.SessionID || detail.Display.State != activity.DisplayStateOpen {
		t.Fatalf("ReadSessionDetail() = %+v", detail)
	}
}

func TestGetSessionDetailFactsTranslatesOnlyNoRows(t *testing.T) {
	t.Parallel()

	scanFailure := errors.New("scan failed")
	tests := []struct {
		name    string
		row     rowScanner
		wantErr error
	}{
		{name: "not found", row: &fakeRow{err: sql.ErrNoRows}, wantErr: ErrSessionDetailNotFound},
		{name: "scan", row: &fakeRow{err: scanFailure}, wantErr: scanFailure},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository := &Repository{db: &fakeQueryExecutor{
				queryRow: func(string, ...any) rowScanner { return test.row },
			}}
			_, err := repository.GetSessionDetailFacts(context.Background(), uuid.New(), uuid.New())
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("GetSessionDetailFacts() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func validPostgresSessionDetailFacts(now time.Time) activity.SessionDetailFacts {
	longitude := 117.2
	latitude := 39.1
	lowStockThreshold := 3
	return activity.SessionDetailFacts{
		SeriesID:                         uuid.New(),
		SeriesStatus:                     activity.SeriesStatusActive,
		SeriesIsRecurring:                true,
		CurrentPublicInstance:            true,
		SuccessfulPublishedInstanceCount: 4,
		HistoricalRegistrationCount:      31,
		InstanceID:                       uuid.New(),
		InstanceTitle:                    "AI makers",
		InstanceStatus:                   activity.InstanceStatusPublished,
		InstanceActivityType:             activity.ActivityTypeAIRoundtable,
		InstanceQuickTagCodes:            []string{"ai", "maker"},
		InstanceCoverImageURL:            "https://cdn.example.com/covers/ai-roundtable.jpg",
		InstanceDetailBlocks: []activity.DetailBlock{
			{Type: activity.DetailBlockTypeText, Title: "活动亮点", Body: "三面环水"},
			{
				Type:    activity.DetailBlockTypeImage,
				URL:     "/api/v1/xiangwan/covers/0123456789abcdef0123456789abcdef.jpg",
				Caption: "现场",
			},
		},
		PublicationVersion:           2,
		SessionID:                    uuid.New(),
		SessionTitle:                 "Evening Session",
		SessionStatus:                activity.SessionStatusPublished,
		RegistrationStartAt:          now.Add(-time.Hour),
		RegistrationEndAt:            now.Add(time.Hour),
		SessionStartAt:               now.Add(2 * time.Hour),
		SessionEndAt:                 now.Add(4 * time.Hour),
		Capacity:                     20,
		ConfirmedCount:               5,
		ActiveHoldCount:              1,
		GroupMinimum:                 4,
		LowStockThreshold:            &lowStockThreshold,
		PriceCents:                   9900,
		DeliveryMode:                 activity.DeliveryModeOffline,
		Area:                         activity.AreaCodeHeping,
		VenueName:                    "Maker Space",
		Address:                      "No. 1 Innovation Road",
		Longitude:                    &longitude,
		Latitude:                     &latitude,
		OnlineParticipationCompliant: false,
	}
}

func sessionDetailFactScanValues(facts activity.SessionDetailFacts) []any {
	detailBlocksJSON, err := json.Marshal(facts.InstanceDetailBlocks)
	if err != nil {
		panic(err)
	}
	return []any{
		facts.SeriesID,
		facts.SeriesStatus,
		facts.SeriesIsRecurring,
		facts.CurrentPublicInstance,
		facts.SuccessfulPublishedInstanceCount,
		facts.HistoricalRegistrationCount,
		facts.InstanceID,
		facts.InstanceTitle,
		facts.InstanceStatus,
		sql.NullString{String: string(facts.InstanceActivityType), Valid: facts.InstanceActivityType != ""},
		StringArrayJSON(facts.InstanceQuickTagCodes),
		facts.InstanceCoverImageURL,
		detailBlocksJSON,
		facts.PublicationVersion,
		facts.SessionID,
		facts.SessionTitle,
		facts.SessionStatus,
		sql.NullTime{Time: facts.RegistrationStartAt, Valid: !facts.RegistrationStartAt.IsZero()},
		sql.NullTime{Time: facts.RegistrationEndAt, Valid: !facts.RegistrationEndAt.IsZero()},
		sql.NullTime{Time: facts.SessionStartAt, Valid: !facts.SessionStartAt.IsZero()},
		sql.NullTime{Time: facts.SessionEndAt, Valid: !facts.SessionEndAt.IsZero()},
		sql.NullInt64{Int64: int64(facts.Capacity), Valid: facts.Capacity != 0},
		facts.ConfirmedCount,
		facts.ActiveHoldCount,
		sql.NullInt64{Int64: int64(facts.GroupMinimum), Valid: facts.GroupMinimum != 0},
		nullInt(facts.LowStockThreshold),
		sql.NullInt64{Int64: facts.PriceCents, Valid: true},
		sql.NullString{String: string(facts.DeliveryMode), Valid: facts.DeliveryMode != ""},
		sql.NullString{String: string(facts.Area), Valid: facts.Area != ""},
		sql.NullString{String: facts.VenueName, Valid: facts.VenueName != ""},
		sql.NullString{String: facts.Address, Valid: facts.Address != ""},
		nullFloat64(facts.Longitude),
		nullFloat64(facts.Latitude),
		sql.NullString{String: facts.OnlineParticipationMode, Valid: facts.OnlineParticipationMode != ""},
		sql.NullBool{Bool: facts.OnlineParticipationCompliant, Valid: facts.DeliveryMode == activity.DeliveryModeOnline},
	}
}
