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

func TestListHomeSessionFactsUsesCurrentTenantScopedPublication(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 12, 4, 0, 0, 0, time.UTC)
	lowStockThreshold := 3
	want := activity.HomeSessionFacts{
		SeriesID:                    uuid.New(),
		SeriesStatus:                activity.SeriesStatusActive,
		SeriesIsRecurring:           true,
		SeriesHomeVisible:           true,
		CurrentPublicInstance:       true,
		InstanceID:                  uuid.New(),
		InstanceTitle:               "AI makers",
		InstanceStatus:              activity.InstanceStatusPublished,
		InstanceActivityType:        activity.ActivityTypeAIRoundtable,
		InstanceQuickTagCodes:       []string{"ai", "maker"},
		InstanceCoverImageURL:       "https://cdn.example.com/covers/ai-roundtable.jpg",
		PublicationVersion:          2,
		SessionID:                   uuid.New(),
		SessionTitle:                "Evening Session",
		SessionStatus:               activity.SessionStatusPublished,
		RegistrationStartAt:         now.Add(-time.Hour),
		RegistrationEndAt:           now.Add(time.Hour),
		SessionStartAt:              now.Add(2 * time.Hour),
		SessionEndAt:                now.Add(4 * time.Hour),
		Capacity:                    20,
		ConfirmedCount:              5,
		ActiveHoldCount:             2,
		GroupMinimum:                4,
		LowStockThreshold:           &lowStockThreshold,
		PriceCents:                  9900,
		DeliveryMode:                activity.DeliveryModeOffline,
		Area:                        activity.AreaCodeHeping,
		VenueName:                   "Maker Space",
		SortOrder:                   10,
		SeriesFavoriteCount:         7,
		HistoricalRegistrationCount: 11,
	}
	tenantID := uuid.New()
	var capturedQuery string
	var capturedArgs []any
	repository := &Repository{db: &fakeQueryExecutor{
		queryRows: func(query string, args ...any) (rowsScanner, error) {
			capturedQuery = query
			capturedArgs = append([]any(nil), args...)
			return &fakeRows{rows: [][]any{homeSessionFactScanValues(want)}}, nil
		},
	}}

	got, err := repository.ListHomeSessionFacts(context.Background(), tenantID)
	if err != nil {
		t.Fatalf("ListHomeSessionFacts() error = %v", err)
	}
	if !reflect.DeepEqual(got, []activity.HomeSessionFacts{want}) {
		t.Fatalf("ListHomeSessionFacts() = %+v, want %+v", got, want)
	}
	for _, fragment := range []string{
		"activity_instance.tenant_id = activity_series.tenant_id",
		"activity_instance.id = activity_series.current_public_instance_id",
		"activity_session.tenant_id = activity_instance.tenant_id",
		"activity_series.tenant_id = $1",
		"activity_series.home_visible",
	} {
		if !strings.Contains(capturedQuery, fragment) {
			t.Fatalf("home query missing %q: %s", fragment, capturedQuery)
		}
	}
	if !reflect.DeepEqual(capturedArgs, []any{tenantID}) {
		t.Fatalf("home query args = %#v", capturedArgs)
	}

	catalog, err := repository.ReadHomeCatalog(
		context.Background(),
		tenantID,
		activity.HomeFilter{},
		[]activity.HomeQuickTag{{Code: "ai", Label: "AI"}, {Code: "maker", Label: "Maker"}},
		now,
	)
	if err != nil {
		t.Fatalf("ReadHomeCatalog() error = %v", err)
	}
	if len(catalog.Cards) != 1 || catalog.Cards[0].HeatCount != 18 ||
		catalog.Cards[0].CurrentFavoriteUsers != 7 {
		t.Fatalf("ReadHomeCatalog() = %+v", catalog)
	}
}

func TestListHomeSessionFactsPropagatesReadFailures(t *testing.T) {
	t.Parallel()

	queryFailure := errors.New("query failed")
	iterationFailure := errors.New("iteration failed")
	tests := []struct {
		name       string
		executor   *fakeQueryExecutor
		want       error
		wantPrefix string
	}{
		{
			name: "query",
			executor: &fakeQueryExecutor{queryRows: func(string, ...any) (rowsScanner, error) {
				return nil, queryFailure
			}},
			want:       queryFailure,
			wantPrefix: "list xiangwan home Session facts",
		},
		{
			name: "iterate",
			executor: &fakeQueryExecutor{queryRows: func(string, ...any) (rowsScanner, error) {
				return &fakeRows{err: iterationFailure}, nil
			}},
			want:       iterationFailure,
			wantPrefix: "iterate xiangwan home Session facts",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := (&Repository{db: test.executor}).ListHomeSessionFacts(context.Background(), uuid.New())
			if !errors.Is(err, test.want) || !strings.Contains(err.Error(), test.wantPrefix) {
				t.Fatalf("ListHomeSessionFacts() error = %v", err)
			}
		})
	}
}

func TestReadHomeCatalogPropagatesFactValidation(t *testing.T) {
	t.Parallel()

	invalid := activity.HomeSessionFacts{
		SeriesID:              uuid.New(),
		SeriesStatus:          activity.SeriesStatusActive,
		SeriesHomeVisible:     true,
		CurrentPublicInstance: true,
		InstanceID:            uuid.New(),
		InstanceStatus:        activity.InstanceStatusPublished,
		SessionID:             uuid.New(),
		SessionStatus:         activity.SessionStatusPublished,
	}
	repository := &Repository{db: &fakeQueryExecutor{
		queryRows: func(string, ...any) (rowsScanner, error) {
			return &fakeRows{rows: [][]any{homeSessionFactScanValues(invalid)}}, nil
		},
	}}

	_, err := repository.ReadHomeCatalog(
		context.Background(),
		uuid.New(),
		activity.HomeFilter{},
		nil,
		time.Now(),
	)
	if !errors.Is(err, activity.ErrInvalidHomeFacts) {
		t.Fatalf("ReadHomeCatalog() error = %v, want ErrInvalidHomeFacts", err)
	}
}

func homeSessionFactScanValues(fact activity.HomeSessionFacts) []any {
	return []any{
		fact.SeriesID,
		fact.SeriesStatus,
		fact.SeriesIsRecurring,
		fact.SeriesHomeVisible,
		fact.CurrentPublicInstance,
		fact.InstanceID,
		fact.InstanceTitle,
		fact.InstanceStatus,
		sql.NullString{String: string(fact.InstanceActivityType), Valid: fact.InstanceActivityType != ""},
		StringArrayJSON(fact.InstanceQuickTagCodes),
		fact.InstanceCoverImageURL,
		fact.PublicationVersion,
		fact.SessionID,
		fact.SessionTitle,
		fact.SessionStatus,
		sql.NullTime{Time: fact.RegistrationStartAt, Valid: !fact.RegistrationStartAt.IsZero()},
		sql.NullTime{Time: fact.RegistrationEndAt, Valid: !fact.RegistrationEndAt.IsZero()},
		sql.NullTime{Time: fact.SessionStartAt, Valid: !fact.SessionStartAt.IsZero()},
		sql.NullTime{Time: fact.SessionEndAt, Valid: !fact.SessionEndAt.IsZero()},
		sql.NullInt64{Int64: int64(fact.Capacity), Valid: fact.Capacity != 0},
		fact.ConfirmedCount,
		fact.ActiveHoldCount,
		sql.NullInt64{Int64: int64(fact.GroupMinimum), Valid: fact.GroupMinimum != 0},
		nullInt(fact.LowStockThreshold),
		sql.NullInt64{Int64: fact.PriceCents, Valid: true},
		sql.NullString{String: string(fact.DeliveryMode), Valid: fact.DeliveryMode != ""},
		sql.NullString{String: string(fact.Area), Valid: fact.Area != ""},
		sql.NullString{String: fact.VenueName, Valid: fact.VenueName != ""},
		sql.NullString{String: fact.OnlineParticipationMode, Valid: fact.OnlineParticipationMode != ""},
		fact.SortOrder,
		fact.SeriesFavoriteCount,
		fact.HistoricalRegistrationCount,
	}
}
