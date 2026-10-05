package activity

import (
	"errors"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestBuildHomeCatalogAppliesSixGroupOrder(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 15, 4, 0, 0, 0, time.UTC)
	open := validHomeFact(now)
	notOpen := validHomeFact(now)
	notOpen.SessionID = uuid.New()
	notOpen.RegistrationStartAt = now.Add(time.Hour)
	notOpen.RegistrationEndAt = now.Add(2 * time.Hour)
	notOpen.SessionStartAt = now.Add(3 * time.Hour)
	notOpen.SessionEndAt = now.Add(4 * time.Hour)
	inProgress := validHomeFact(now)
	inProgress.SessionID = uuid.New()
	setHomeSessionTimes(&inProgress, now.Add(-30*time.Minute))
	inProgress.RegistrationStartAt = now.Add(-4 * time.Hour)
	inProgress.RegistrationEndAt = now.Add(-2 * time.Hour)
	gap := validHomeFact(now)
	gap.SessionID = uuid.New()
	gap.SeriesIsRecurring = true
	gap.InstanceStatus = InstanceStatusCompleted
	setHomeSessionTimes(&gap, now.Add(-3*time.Hour))
	ended := validHomeFact(now)
	ended.SessionID = uuid.New()
	setHomeSessionTimes(&ended, now.Add(-5*time.Hour))
	cancelled := validHomeFact(now)
	cancelled.SessionID = uuid.New()
	cancelled.SessionStatus = SessionStatusCancelled
	draft := HomeSessionFacts{
		SessionStatus: SessionStatusDraft,
		SessionID:     uuid.New(),
	}
	notCurrent := validHomeFact(now)
	notCurrent.SessionID = uuid.New()
	notCurrent.CurrentPublicInstance = false

	catalog, err := BuildHomeCatalog(
		[]HomeSessionFacts{cancelled, draft, gap, ended, notCurrent, inProgress, notOpen, open},
		HomeFilter{},
		availableHomeQuickTags(),
		now,
	)
	if err != nil {
		t.Fatalf("BuildHomeCatalog() error = %v", err)
	}
	wantIDs := []uuid.UUID{
		open.SessionID,
		notOpen.SessionID,
		inProgress.SessionID,
		gap.SessionID,
		ended.SessionID,
		cancelled.SessionID,
	}
	if len(catalog.Cards) != len(wantIDs) {
		t.Fatalf("card count = %d, want %d", len(catalog.Cards), len(wantIDs))
	}
	for index, wantID := range wantIDs {
		if catalog.Cards[index].SessionID != wantID {
			t.Fatalf("card[%d] Session = %s, want %s", index, catalog.Cards[index].SessionID, wantID)
		}
		if catalog.Cards[index].CTAAction != HomeCTAActionSessionDetail {
			t.Fatalf("card[%d] CTA action = %q", index, catalog.Cards[index].CTAAction)
		}
	}
	if catalog.Cards[4].CTALabel != HomeCTALabelActivityEnded {
		t.Fatalf("ended CTA label = %q", catalog.Cards[4].CTALabel)
	}
	if catalog.Cards[5].CTALabel != HomeCTALabelActivityCancelled {
		t.Fatalf("cancelled CTA label = %q", catalog.Cards[5].CTALabel)
	}
	if catalog.OpenRegistrationSessionCount != 1 {
		t.Fatalf("open count = %d, want 1", catalog.OpenRegistrationSessionCount)
	}
	if catalog.BusinessTimezone != BusinessTimezone ||
		catalog.ActiveFilter.Limit != DefaultHomeLimit ||
		!catalog.AsOf.Equal(now) {
		t.Fatalf("catalog metadata = %+v", catalog)
	}
}

func TestBuildHomeCatalogUsesEndedDescendingAndStableTieBreak(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 15, 4, 0, 0, 0, time.UTC)
	newerEnded := validHomeFact(now)
	newerEnded.SessionID = uuid.New()
	setHomeSessionTimes(&newerEnded, now.Add(-3*time.Hour))
	olderEnded := validHomeFact(now)
	olderEnded.SessionID = uuid.New()
	setHomeSessionTimes(&olderEnded, now.Add(-6*time.Hour))

	tied := []HomeSessionFacts{validHomeFact(now), validHomeFact(now), validHomeFact(now)}
	for index := range tied {
		tied[index].SessionID = uuid.New()
		tied[index].SortOrder = 4
	}
	wantTiedIDs := []string{
		tied[0].SessionID.String(),
		tied[1].SessionID.String(),
		tied[2].SessionID.String(),
	}
	sort.Strings(wantTiedIDs)

	facts := append(tied, olderEnded, newerEnded)
	catalog, err := BuildHomeCatalog(facts, HomeFilter{}, availableHomeQuickTags(), now)
	if err != nil {
		t.Fatalf("BuildHomeCatalog() error = %v", err)
	}
	for index, wantID := range wantTiedIDs {
		if catalog.Cards[index].SessionID.String() != wantID {
			t.Fatalf("tie card[%d] = %s, want %s", index, catalog.Cards[index].SessionID, wantID)
		}
	}
	if catalog.Cards[3].SessionID != newerEnded.SessionID ||
		catalog.Cards[4].SessionID != olderEnded.SessionID {
		t.Fatalf("ended order = %s, %s", catalog.Cards[3].SessionID, catalog.Cards[4].SessionID)
	}
}

func TestBuildHomeCatalogFiltersAtSessionGranularity(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 12, 4, 0, 0, 0, time.UTC)
	offline := validHomeFact(now)
	offline.InstanceQuickTagCodes = []string{"ai"}
	online := validHomeFact(now)
	online.SessionID = uuid.New()
	online.DeliveryMode = DeliveryModeOnline
	online.Area = AreaCodeOnline
	online.VenueName = ""
	online.OnlineParticipationMode = "approved_meeting"
	online.InstanceQuickTagCodes = []string{"maker"}

	catalog, err := BuildHomeCatalog(
		[]HomeSessionFacts{offline, online},
		HomeFilter{
			ActivityType: ActivityTypeAIRoundtable,
			Area:         AreaCodeHeping,
			QuickTags:    []string{"maker", "ai", "ai"},
		},
		availableHomeQuickTags(),
		now,
	)
	if err != nil {
		t.Fatalf("BuildHomeCatalog() error = %v", err)
	}
	if len(catalog.Cards) != 1 || catalog.Cards[0].SessionID != offline.SessionID {
		t.Fatalf("filtered cards = %+v", catalog.Cards)
	}
	if !reflect.DeepEqual(catalog.ActiveFilter.QuickTags, []string{"ai", "maker"}) {
		t.Fatalf("normalized quick tags = %v", catalog.ActiveFilter.QuickTags)
	}
	if catalog.Cards[0].HeatCount !=
		offline.SeriesFavoriteCount+offline.HistoricalRegistrationCount {
		t.Fatalf("heat count = %d", catalog.Cards[0].HeatCount)
	}
	if catalog.Cards[0].CurrentFavoriteUsers != offline.SeriesFavoriteCount {
		t.Fatalf("current favorite users = %d", catalog.Cards[0].CurrentFavoriteUsers)
	}
}

func TestBuildHomeCatalogAcceptsCustomActivityType(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 12, 4, 0, 0, 0, time.UTC)
	fact := validHomeFact(now)
	fact.InstanceActivityType = ActivityTypeCustom
	catalog, err := BuildHomeCatalog(
		[]HomeSessionFacts{fact},
		HomeFilter{ActivityType: ActivityTypeCustom},
		availableHomeQuickTags(),
		now,
	)
	if err != nil {
		t.Fatalf("BuildHomeCatalog() error = %v", err)
	}
	if len(catalog.Cards) != 1 || catalog.Cards[0].ActivityType != ActivityTypeCustom {
		t.Fatalf("custom activity cards = %+v", catalog.Cards)
	}
}

func TestBuildHomeCatalogUsesShanghaiHalfOpenDate(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 12, 4, 0, 0, 0, time.UTC)
	location, err := time.LoadLocation(BusinessTimezone)
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	localStart := time.Date(2026, time.September, 14, 0, 0, 0, 0, location)
	atStart := validHomeFact(now)
	atStart.SessionID = uuid.New()
	setHomeSessionTimes(&atStart, localStart.UTC())
	inside := validHomeFact(now)
	inside.SessionID = uuid.New()
	setHomeSessionTimes(&inside, localStart.Add(12*time.Hour).UTC())
	atEnd := validHomeFact(now)
	atEnd.SessionID = uuid.New()
	setHomeSessionTimes(&atEnd, localStart.AddDate(0, 0, 1).UTC())

	catalog, err := BuildHomeCatalog(
		[]HomeSessionFacts{atEnd, inside, atStart},
		HomeFilter{
			TimeWindow: HomeTimeWindowLocalDate,
			LocalDate:  "2026-09-14",
		},
		availableHomeQuickTags(),
		now,
	)
	if err != nil {
		t.Fatalf("BuildHomeCatalog() error = %v", err)
	}
	if len(catalog.Cards) != 2 ||
		catalog.Cards[0].SessionID != atStart.SessionID ||
		catalog.Cards[1].SessionID != inside.SessionID {
		t.Fatalf("date-filtered cards = %+v", catalog.Cards)
	}
}

func TestResolveHomeWeekRangesStartOnShanghaiMonday(t *testing.T) {
	t.Parallel()

	location, err := time.LoadLocation(BusinessTimezone)
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	localWednesday := time.Date(2026, time.September, 16, 13, 0, 0, 0, location)
	thisWeek, err := resolveHomeTimeRange(
		HomeFilter{TimeWindow: HomeTimeWindowThisWeek},
		localWednesday.UTC(),
	)
	if err != nil {
		t.Fatalf("resolve this week: %v", err)
	}
	wantThisStart := time.Date(2026, time.September, 14, 0, 0, 0, 0, location).UTC()
	if !thisWeek.start.Equal(wantThisStart) ||
		!thisWeek.end.Equal(wantThisStart.AddDate(0, 0, 7)) {
		t.Fatalf("this week = [%s, %s)", thisWeek.start, thisWeek.end)
	}

	nextWeek, err := resolveHomeTimeRange(
		HomeFilter{TimeWindow: HomeTimeWindowNextWeek},
		localWednesday.UTC(),
	)
	if err != nil {
		t.Fatalf("resolve next week: %v", err)
	}
	if !nextWeek.start.Equal(thisWeek.end) ||
		!nextWeek.end.Equal(thisWeek.end.AddDate(0, 0, 7)) {
		t.Fatalf("next week = [%s, %s)", nextWeek.start, nextWeek.end)
	}
}

func TestBuildHomeCatalogCursorIsStableAndRevisionBound(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 12, 4, 0, 0, 0, time.UTC)
	facts := []HomeSessionFacts{validHomeFact(now), validHomeFact(now), validHomeFact(now)}
	for index := range facts {
		facts[index].SessionID = uuid.New()
	}
	filter := HomeFilter{Limit: 1}
	firstPage, err := BuildHomeCatalog(facts, filter, availableHomeQuickTags(), now)
	if err != nil {
		t.Fatalf("BuildHomeCatalog(first) error = %v", err)
	}
	if len(firstPage.Cards) != 1 || firstPage.NextCursor == "" {
		t.Fatalf("first page = %+v", firstPage)
	}

	reordered := []HomeSessionFacts{facts[2], facts[0], facts[1]}
	filter.Cursor = firstPage.NextCursor
	secondPage, err := BuildHomeCatalog(
		reordered,
		filter,
		availableHomeQuickTags(),
		now.Add(time.Minute),
	)
	if err != nil {
		t.Fatalf("BuildHomeCatalog(second) error = %v", err)
	}
	if len(secondPage.Cards) != 1 ||
		secondPage.Cards[0].SessionID == firstPage.Cards[0].SessionID ||
		!secondPage.AsOf.Equal(firstPage.AsOf) {
		t.Fatalf("second page = %+v", secondPage)
	}
	if secondPage.OpenRegistrationSessionCount != 3 {
		t.Fatalf("open count = %d, want filtered total 3", secondPage.OpenRegistrationSessionCount)
	}

	changed := append([]HomeSessionFacts(nil), facts...)
	changed[0].PublicationVersion++
	if _, err := BuildHomeCatalog(
		changed,
		filter,
		availableHomeQuickTags(),
		now.Add(time.Minute),
	); !errors.Is(err, ErrStaleHomeCursor) {
		t.Fatalf("publication change error = %v, want ErrStaleHomeCursor", err)
	}

	changed = append([]HomeSessionFacts(nil), facts...)
	changed[0].ConfirmedCount++
	if _, err := BuildHomeCatalog(
		changed,
		filter,
		availableHomeQuickTags(),
		now.Add(time.Minute),
	); !errors.Is(err, ErrStaleHomeCursor) {
		t.Fatalf("capacity change error = %v, want ErrStaleHomeCursor", err)
	}

	changedFilter := filter
	changedFilter.Area = AreaCodeHexi
	if _, err := BuildHomeCatalog(
		facts,
		changedFilter,
		availableHomeQuickTags(),
		now.Add(time.Minute),
	); !errors.Is(err, ErrStaleHomeCursor) {
		t.Fatalf("filter change error = %v, want ErrStaleHomeCursor", err)
	}

	if _, err := BuildHomeCatalog(
		facts,
		filter,
		availableHomeQuickTags(),
		now.Add(MaxHomeCursorAge+time.Second),
	); !errors.Is(err, ErrStaleHomeCursor) {
		t.Fatalf("expired cursor error = %v, want ErrStaleHomeCursor", err)
	}
}

func TestBuildHomeCatalogRejectsInvalidFilters(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		filter HomeFilter
	}{
		{name: "activity type", filter: HomeFilter{ActivityType: ActivityType("unknown")}},
		{name: "area", filter: HomeFilter{Area: AreaCode("unknown")}},
		{name: "time window", filter: HomeFilter{TimeWindow: HomeTimeWindow("unknown")}},
		{name: "missing local date", filter: HomeFilter{TimeWindow: HomeTimeWindowLocalDate}},
		{name: "unexpected local date", filter: HomeFilter{LocalDate: "2026-09-14"}},
		{name: "limit", filter: HomeFilter{Limit: MaxHomeLimit + 1}},
		{name: "quick tag count", filter: HomeFilter{QuickTags: []string{"a", "b", "c", "d", "e", "f"}}},
		{name: "unknown quick tag", filter: HomeFilter{QuickTags: []string{"unknown"}}},
		{name: "cursor", filter: HomeFilter{Cursor: "not base64"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := BuildHomeCatalog(
				[]HomeSessionFacts{validHomeFact(time.Now())},
				test.filter,
				availableHomeQuickTags(),
				time.Now(),
			)
			if !errors.Is(err, ErrInvalidHomeFilter) &&
				!errors.Is(err, ErrInvalidHomeCursor) {
				t.Fatalf("BuildHomeCatalog() error = %v", err)
			}
		})
	}
}

func TestBuildHomeCatalogRejectsInvalidPublicFactsButSkipsDrafts(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 12, 4, 0, 0, 0, time.UTC)
	valid := validHomeFact(now)
	invalid := valid
	invalid.SessionID = uuid.New()
	invalid.SeriesFavoriteCount = -1
	if _, err := BuildHomeCatalog(
		[]HomeSessionFacts{valid, invalid},
		HomeFilter{},
		availableHomeQuickTags(),
		now,
	); !errors.Is(err, ErrInvalidHomeFacts) {
		t.Fatalf("invalid public facts error = %v, want ErrInvalidHomeFacts", err)
	}

	draft := invalid
	draft.SessionStatus = SessionStatusDraft
	catalog, err := BuildHomeCatalog(
		[]HomeSessionFacts{valid, draft},
		HomeFilter{},
		availableHomeQuickTags(),
		now,
	)
	if err != nil {
		t.Fatalf("draft facts leaked into validation: %v", err)
	}
	if len(catalog.Cards) != 1 || catalog.Cards[0].SessionID != valid.SessionID {
		t.Fatalf("cards = %+v", catalog.Cards)
	}

	duplicate := valid
	if _, err := BuildHomeCatalog(
		[]HomeSessionFacts{valid, duplicate},
		HomeFilter{},
		availableHomeQuickTags(),
		now,
	); !errors.Is(err, ErrInvalidHomeFacts) {
		t.Fatalf("duplicate Session error = %v, want ErrInvalidHomeFacts", err)
	}
}

func TestBuildHomeCatalogKeepsStaleQuickTagCodesVisible(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 12, 4, 0, 0, 0, time.UTC)
	fact := validHomeFact(now)
	fact.InstanceQuickTagCodes = []string{"legacy_tag"}
	catalog, err := BuildHomeCatalog(
		[]HomeSessionFacts{fact}, HomeFilter{}, availableHomeQuickTags(), now,
	)
	if err != nil {
		t.Fatalf("BuildHomeCatalog() error = %v, want stale code to be tolerated", err)
	}
	if len(catalog.Cards) != 1 || !reflect.DeepEqual(
		catalog.Cards[0].QuickTagCodes, []string{"legacy_tag"},
	) {
		t.Fatalf("card quick tags = %+v, want opaque legacy_tag", catalog.Cards)
	}
}

func availableHomeQuickTags() []HomeQuickTag {
	return []HomeQuickTag{
		{Code: "ai", Label: "AI"},
		{Code: "local", Label: "Local"},
		{Code: "maker", Label: "Maker"},
	}
}

func validHomeFact(now time.Time) HomeSessionFacts {
	registrationStart := now.Add(-time.Hour)
	registrationEnd := now.Add(2 * time.Hour)
	sessionStart := now.Add(3 * time.Hour)
	return HomeSessionFacts{
		SeriesID:                    uuid.New(),
		SeriesStatus:                SeriesStatusActive,
		SeriesHomeVisible:           true,
		CurrentPublicInstance:       true,
		InstanceID:                  uuid.New(),
		InstanceTitle:               "Tianjin AI Roundtable",
		InstanceStatus:              InstanceStatusPublished,
		InstanceActivityType:        ActivityTypeAIRoundtable,
		InstanceQuickTagCodes:       []string{"ai", "local"},
		PublicationVersion:          1,
		SessionID:                   uuid.New(),
		SessionTitle:                "Evening Session",
		SessionStatus:               SessionStatusPublished,
		RegistrationStartAt:         registrationStart,
		RegistrationEndAt:           registrationEnd,
		SessionStartAt:              sessionStart,
		SessionEndAt:                sessionStart.Add(2 * time.Hour),
		Capacity:                    20,
		ConfirmedCount:              5,
		GroupMinimum:                3,
		LowStockThreshold:           intPointer(2),
		PriceCents:                  9900,
		DeliveryMode:                DeliveryModeOffline,
		Area:                        AreaCodeHeping,
		VenueName:                   "Xiangwan Lab",
		SortOrder:                   1,
		SeriesFavoriteCount:         7,
		HistoricalRegistrationCount: 21,
	}
}

func setHomeSessionTimes(fact *HomeSessionFacts, sessionStart time.Time) {
	fact.RegistrationStartAt = sessionStart.Add(-4 * time.Hour)
	fact.RegistrationEndAt = sessionStart.Add(-2 * time.Hour)
	fact.SessionStartAt = sessionStart
	fact.SessionEndAt = sessionStart.Add(time.Hour)
}
