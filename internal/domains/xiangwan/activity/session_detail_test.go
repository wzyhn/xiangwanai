package activity

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestBuildSessionDetailBindsExactlyOneSession(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 12, 4, 0, 0, 0, time.UTC)
	facts := validSessionDetailFacts(now)
	facts.CurrentPublicInstance = false

	detail, err := BuildSessionDetail(facts, now)
	if err != nil {
		t.Fatalf("BuildSessionDetail() error = %v", err)
	}
	if detail.SeriesID != facts.SeriesID ||
		detail.InstanceID != facts.InstanceID ||
		detail.SessionID != facts.SessionID ||
		detail.SuccessfulPublishedInstanceCount != facts.SuccessfulPublishedInstanceCount ||
		detail.HistoricalRegistrationCount != facts.HistoricalRegistrationCount ||
		detail.Display.State != DisplayStateOpen ||
		detail.CTA.Action != SessionDetailCTAActionStartRegistration ||
		!detail.CTA.Enabled {
		t.Fatalf("BuildSessionDetail() = %+v", detail)
	}
	if !reflect.DeepEqual(detail.QuickTagCodes, facts.InstanceQuickTagCodes) ||
		detail.Longitude == facts.Longitude ||
		detail.Latitude == facts.Latitude {
		t.Fatalf("detail did not isolate copied facts: %+v", detail)
	}
	detail.QuickTagCodes[0] = "changed"
	*detail.Longitude = 0
	if facts.InstanceQuickTagCodes[0] != "ai" || *facts.Longitude == 0 {
		t.Fatal("mutating detail changed source facts")
	}
}

func TestBuildSessionDetailUsesRecurringGapOnlyForCurrentInstance(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 12, 8, 0, 0, 0, time.UTC)
	facts := validSessionDetailFacts(now)
	facts.SeriesIsRecurring = true
	facts.InstanceStatus = InstanceStatusCompleted
	facts.RegistrationStartAt = now.Add(-4 * time.Hour)
	facts.RegistrationEndAt = now.Add(-3 * time.Hour)
	facts.SessionStartAt = now.Add(-2 * time.Hour)
	facts.SessionEndAt = now.Add(-time.Hour)

	facts.CurrentPublicInstance = true
	current, err := BuildSessionDetail(facts, now)
	if err != nil {
		t.Fatalf("BuildSessionDetail(current) error = %v", err)
	}
	if current.Display.State != DisplayStateRecurringGap || current.CTA.Label != SessionDetailCTALabelComingSoon {
		t.Fatalf("current recurring detail = %+v", current)
	}

	facts.CurrentPublicInstance = false
	historical, err := BuildSessionDetail(facts, now)
	if err != nil {
		t.Fatalf("BuildSessionDetail(historical) error = %v", err)
	}
	if historical.Display.State != DisplayStateEnded || historical.CTA.Label != SessionDetailCTALabelActivityEnded {
		t.Fatalf("historical recurring detail = %+v", historical)
	}
}

func TestBuildSessionDetailProjectsOnlineParticipationWithoutLocation(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 12, 4, 0, 0, 0, time.UTC)
	facts := validSessionDetailFacts(now)
	facts.DeliveryMode = DeliveryModeOnline
	facts.Area = AreaCodeOnline
	facts.VenueName = ""
	facts.Address = ""
	facts.Longitude = nil
	facts.Latitude = nil
	facts.OnlineParticipationMode = "wechat_group"
	facts.OnlineParticipationCompliant = true

	detail, err := BuildSessionDetail(facts, now)
	if err != nil {
		t.Fatalf("BuildSessionDetail() error = %v", err)
	}
	if detail.DeliveryMode != DeliveryModeOnline ||
		detail.Area != AreaCodeOnline ||
		detail.OnlineParticipationMode != "wechat_group" ||
		detail.Longitude != nil ||
		detail.Latitude != nil {
		t.Fatalf("online Session detail = %+v", detail)
	}
}

func TestSessionDetailCTAImplementsCanonicalMatrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		state   DisplayState
		label   SessionDetailCTALabel
		action  SessionDetailCTAAction
		enabled bool
	}{
		{DisplayStateCancelled, SessionDetailCTALabelActivityCancelled, SessionDetailCTAActionNone, false},
		{DisplayStateRecurringGap, SessionDetailCTALabelComingSoon, SessionDetailCTAActionNone, false},
		{DisplayStateEnded, SessionDetailCTALabelActivityEnded, SessionDetailCTAActionNone, false},
		{DisplayStateInProgress, SessionDetailCTALabelActivityInProgress, SessionDetailCTAActionNone, false},
		{DisplayStateNotOpen, SessionDetailCTALabelNotOpen, SessionDetailCTAActionNone, false},
		{DisplayStateClosed, SessionDetailCTALabelRegistrationClosed, SessionDetailCTAActionNone, false},
		{DisplayStateFull, SessionDetailCTALabelRegistrationClosed, SessionDetailCTAActionNone, false},
		{DisplayStateTemporarilyLockedFull, SessionDetailCTALabelTryAgainLater, SessionDetailCTAActionNone, false},
		{DisplayStateOpenLowStock, SessionDetailCTALabelRegisterNow, SessionDetailCTAActionStartRegistration, true},
		{DisplayStateOpenNeedGroup, SessionDetailCTALabelRegisterNow, SessionDetailCTAActionStartRegistration, true},
		{DisplayStateOpen, SessionDetailCTALabelRegisterNow, SessionDetailCTAActionStartRegistration, true},
	}
	for _, test := range tests {
		t.Run(string(test.state), func(t *testing.T) {
			t.Parallel()
			got := sessionDetailCTAForDisplay(test.state)
			if got.Label != test.label || got.Action != test.action || got.Enabled != test.enabled {
				t.Fatalf("sessionDetailCTAForDisplay(%q) = %+v", test.state, got)
			}
		})
	}
}

func TestBuildSessionDetailRejectsUnavailableOrMalformedFacts(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 12, 4, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		mutate  func(*SessionDetailFacts)
		wantErr error
	}{
		{
			name: "missing unique Session identity",
			mutate: func(facts *SessionDetailFacts) {
				facts.SessionID = uuid.Nil
			},
			wantErr: ErrInvalidSessionDetailFacts,
		},
		{
			name: "draft Session",
			mutate: func(facts *SessionDetailFacts) {
				facts.SessionStatus = SessionStatusDraft
			},
			wantErr: ErrSessionDetailUnavailable,
		},
		{
			name: "duplicate tag",
			mutate: func(facts *SessionDetailFacts) {
				facts.InstanceQuickTagCodes = []string{"ai", "ai"}
			},
			wantErr: ErrInvalidSessionDetailFacts,
		},
		{
			name: "negative Series history",
			mutate: func(facts *SessionDetailFacts) {
				facts.HistoricalRegistrationCount = -1
			},
			wantErr: ErrInvalidSessionDetailFacts,
		},
		{
			name: "cross-mode area",
			mutate: func(facts *SessionDetailFacts) {
				facts.Area = AreaCodeOnline
			},
			wantErr: ErrInvalidSessionDetailFacts,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			facts := validSessionDetailFacts(now)
			test.mutate(&facts)
			_, err := BuildSessionDetail(facts, now)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("BuildSessionDetail() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func validSessionDetailFacts(now time.Time) SessionDetailFacts {
	longitude := 117.2
	latitude := 39.1
	lowStockThreshold := 2
	return SessionDetailFacts{
		SeriesID:                         uuid.New(),
		SeriesStatus:                     SeriesStatusActive,
		CurrentPublicInstance:            true,
		SuccessfulPublishedInstanceCount: 3,
		HistoricalRegistrationCount:      28,
		InstanceID:                       uuid.New(),
		InstanceTitle:                    "AI makers",
		InstanceStatus:                   InstanceStatusPublished,
		InstanceActivityType:             ActivityTypeAIRoundtable,
		InstanceQuickTagCodes:            []string{"ai", "maker"},
		PublicationVersion:               1,
		SessionID:                        uuid.New(),
		SessionTitle:                     "Evening Session",
		SessionStatus:                    SessionStatusPublished,
		RegistrationStartAt:              now.Add(-time.Hour),
		RegistrationEndAt:                now.Add(time.Hour),
		SessionStartAt:                   now.Add(2 * time.Hour),
		SessionEndAt:                     now.Add(4 * time.Hour),
		Capacity:                         20,
		ConfirmedCount:                   5,
		ActiveHoldCount:                  1,
		GroupMinimum:                     4,
		LowStockThreshold:                &lowStockThreshold,
		PriceCents:                       9900,
		DeliveryMode:                     DeliveryModeOffline,
		Area:                             AreaCodeHeping,
		VenueName:                        "Maker Space",
		Address:                          "No. 1 Innovation Road",
		Longitude:                        &longitude,
		Latitude:                         &latitude,
		OnlineParticipationCompliant:     false,
	}
}
