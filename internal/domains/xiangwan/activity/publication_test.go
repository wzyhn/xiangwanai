package activity

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCheckInstancePublicationAcceptsCompleteOfflineAndOnlineSessions(t *testing.T) {
	t.Parallel()

	offline := validPublicationCandidate()
	online := validPublicationCandidate()
	online.Sessions[0].SessionID = uuid.New()
	online.Sessions[0].DeliveryMode = DeliveryModeOnline
	online.Sessions[0].Area = AreaCodeOnline
	online.Sessions[0].OfflineLocation = nil
	online.Sessions[0].OnlineParticipation = &OnlineParticipation{
		Mode:      "approved_private_meeting",
		Compliant: true,
	}
	offline.Sessions = append(offline.Sessions, online.Sessions[0])

	if err := CheckInstancePublication(offline); err != nil {
		t.Fatalf("CheckInstancePublication() error = %v", err)
	}
}

func TestCheckInstancePublicationAcceptsCustomActivityType(t *testing.T) {
	t.Parallel()

	candidate := validPublicationCandidate()
	candidate.ActivityType = ActivityTypeCustom
	if err := CheckInstancePublication(candidate); err != nil {
		t.Fatalf("CheckInstancePublication() error = %v", err)
	}
}

func TestValidateInstancePublicationRejectsEveryRequiredBoundary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		mutate    func(*InstancePublicationCandidate)
		wantField string
		wantCode  ViolationCode
	}{
		{name: "series id", mutate: func(c *InstancePublicationCandidate) { c.SeriesID = uuid.Nil }, wantField: "series_id", wantCode: ViolationRequired},
		{name: "instance id", mutate: func(c *InstancePublicationCandidate) { c.InstanceID = uuid.Nil }, wantField: "instance_id", wantCode: ViolationRequired},
		{name: "activity type", mutate: func(c *InstancePublicationCandidate) { c.ActivityType = ActivityTypeAll }, wantField: "activity_type", wantCode: ViolationInvalidChoice},
		{name: "quick tag format", mutate: func(c *InstancePublicationCandidate) { c.QuickTagCodes = []string{"Not Valid"} }, wantField: "quick_tag_codes[0]", wantCode: ViolationInvalidChoice},
		{name: "quick tag duplicate", mutate: func(c *InstancePublicationCandidate) { c.QuickTagCodes = []string{"ai", "ai"} }, wantField: "quick_tag_codes[1]", wantCode: ViolationDuplicate},
		{name: "sessions", mutate: func(c *InstancePublicationCandidate) { c.Sessions = nil }, wantField: "sessions", wantCode: ViolationRequired},
		{name: "session id", mutate: func(c *InstancePublicationCandidate) { c.Sessions[0].SessionID = uuid.Nil }, wantField: "sessions[0].session_id", wantCode: ViolationRequired},
		{
			name: "duplicate session id",
			mutate: func(c *InstancePublicationCandidate) {
				duplicate := c.Sessions[0]
				c.Sessions = append(c.Sessions, duplicate)
			},
			wantField: "sessions[1].session_id",
			wantCode:  ViolationDuplicate,
		},
		{name: "title", mutate: func(c *InstancePublicationCandidate) { c.Sessions[0].Title = "  " }, wantField: "sessions[0].title", wantCode: ViolationMustNotBeBlank},
		{name: "registration start", mutate: func(c *InstancePublicationCandidate) { c.Sessions[0].RegistrationStartAt = time.Time{} }, wantField: "sessions[0].registration_start_at", wantCode: ViolationRequired},
		{name: "registration end", mutate: func(c *InstancePublicationCandidate) {
			c.Sessions[0].RegistrationEndAt = c.Sessions[0].RegistrationStartAt
		}, wantField: "sessions[0].registration_start_at", wantCode: ViolationMustBeBefore},
		{name: "registration after session start", mutate: func(c *InstancePublicationCandidate) {
			c.Sessions[0].RegistrationEndAt = c.Sessions[0].SessionStartAt.Add(time.Nanosecond)
		}, wantField: "sessions[0].registration_end_at", wantCode: ViolationMustNotBeAfter},
		{name: "session end", mutate: func(c *InstancePublicationCandidate) { c.Sessions[0].SessionEndAt = c.Sessions[0].SessionStartAt }, wantField: "sessions[0].session_start_at", wantCode: ViolationMustBeBefore},
		{name: "capacity", mutate: func(c *InstancePublicationCandidate) { c.Sessions[0].Capacity = 0 }, wantField: "sessions[0].capacity", wantCode: ViolationMustBePositive},
		{name: "group minimum", mutate: func(c *InstancePublicationCandidate) { c.Sessions[0].GroupMinimum = 0 }, wantField: "sessions[0].group_minimum", wantCode: ViolationMustBePositive},
		{name: "group minimum exceeds capacity", mutate: func(c *InstancePublicationCandidate) { c.Sessions[0].GroupMinimum = c.Sessions[0].Capacity + 1 }, wantField: "sessions[0].group_minimum", wantCode: ViolationExceedsCapacity},
		{name: "low stock threshold", mutate: func(c *InstancePublicationCandidate) { c.Sessions[0].LowStockThreshold = intPointer(0) }, wantField: "sessions[0].low_stock_threshold", wantCode: ViolationMustBePositive},
		{name: "low stock threshold equals capacity", mutate: func(c *InstancePublicationCandidate) {
			c.Sessions[0].LowStockThreshold = intPointer(c.Sessions[0].Capacity)
		}, wantField: "sessions[0].low_stock_threshold", wantCode: ViolationMustBeLessThanCapacity},
		{name: "price", mutate: func(c *InstancePublicationCandidate) { c.Sessions[0].PriceCents = -1 }, wantField: "sessions[0].price_cents", wantCode: ViolationMustBeNonNegative},
		{name: "delivery mode", mutate: func(c *InstancePublicationCandidate) { c.Sessions[0].DeliveryMode = DeliveryMode("hybrid") }, wantField: "sessions[0].delivery_mode", wantCode: ViolationInvalidChoice},
		{name: "offline area", mutate: func(c *InstancePublicationCandidate) { c.Sessions[0].Area = AreaCodeOnline }, wantField: "sessions[0].area", wantCode: ViolationInvalidChoice},
		{name: "offline location", mutate: func(c *InstancePublicationCandidate) { c.Sessions[0].OfflineLocation = nil }, wantField: "sessions[0].offline_location", wantCode: ViolationRequired},
		{name: "venue", mutate: func(c *InstancePublicationCandidate) { c.Sessions[0].OfflineLocation.VenueName = "" }, wantField: "sessions[0].offline_location.venue_name", wantCode: ViolationMustNotBeBlank},
		{name: "address", mutate: func(c *InstancePublicationCandidate) { c.Sessions[0].OfflineLocation.Address = "" }, wantField: "sessions[0].offline_location.address", wantCode: ViolationMustNotBeBlank},
		{name: "longitude required", mutate: func(c *InstancePublicationCandidate) { c.Sessions[0].OfflineLocation.Longitude = nil }, wantField: "sessions[0].offline_location.longitude", wantCode: ViolationRequired},
		{name: "longitude range", mutate: func(c *InstancePublicationCandidate) { c.Sessions[0].OfflineLocation.Longitude = floatPointer(181) }, wantField: "sessions[0].offline_location.longitude", wantCode: ViolationOutOfRange},
		{name: "latitude required", mutate: func(c *InstancePublicationCandidate) { c.Sessions[0].OfflineLocation.Latitude = nil }, wantField: "sessions[0].offline_location.latitude", wantCode: ViolationRequired},
		{name: "latitude range", mutate: func(c *InstancePublicationCandidate) { c.Sessions[0].OfflineLocation.Latitude = floatPointer(-91) }, wantField: "sessions[0].offline_location.latitude", wantCode: ViolationOutOfRange},
		{
			name: "online participation required",
			mutate: func(c *InstancePublicationCandidate) {
				c.Sessions[0].DeliveryMode = DeliveryModeOnline
				c.Sessions[0].Area = AreaCodeOnline
				c.Sessions[0].OfflineLocation = nil
				c.Sessions[0].OnlineParticipation = nil
			},
			wantField: "sessions[0].online_participation",
			wantCode:  ViolationRequired,
		},
		{
			name: "online mode",
			mutate: func(c *InstancePublicationCandidate) {
				c.Sessions[0].DeliveryMode = DeliveryModeOnline
				c.Sessions[0].Area = AreaCodeOnline
				c.Sessions[0].OfflineLocation = nil
				c.Sessions[0].OnlineParticipation = &OnlineParticipation{Compliant: true}
			},
			wantField: "sessions[0].online_participation.mode",
			wantCode:  ViolationMustNotBeBlank,
		},
		{
			name: "online compliance",
			mutate: func(c *InstancePublicationCandidate) {
				c.Sessions[0].DeliveryMode = DeliveryModeOnline
				c.Sessions[0].Area = AreaCodeOnline
				c.Sessions[0].OfflineLocation = nil
				c.Sessions[0].OnlineParticipation = &OnlineParticipation{Mode: "meeting"}
			},
			wantField: "sessions[0].online_participation.compliant",
			wantCode:  ViolationNotReady,
		},
		{name: "questionnaire reference", mutate: func(c *InstancePublicationCandidate) { c.Sessions[0].References.QuestionnaireReady = false }, wantField: "sessions[0].references.questionnaire", wantCode: ViolationNotReady},
		{name: "people reference", mutate: func(c *InstancePublicationCandidate) { c.Sessions[0].References.PeopleReady = false }, wantField: "sessions[0].references.people", wantCode: ViolationNotReady},
		{name: "content reference", mutate: func(c *InstancePublicationCandidate) { c.Sessions[0].References.ContentReady = false }, wantField: "sessions[0].references.content", wantCode: ViolationNotReady},
		{name: "resource reference", mutate: func(c *InstancePublicationCandidate) { c.Sessions[0].References.ResourcesReady = false }, wantField: "sessions[0].references.resources", wantCode: ViolationNotReady},
		{name: "quick tags reference", mutate: func(c *InstancePublicationCandidate) { c.Sessions[0].References.QuickTagsReady = false }, wantField: "sessions[0].references.quick_tags", wantCode: ViolationNotReady},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			candidate := validPublicationCandidate()
			test.mutate(&candidate)
			violations := ValidateInstancePublication(candidate)
			if !containsViolation(violations, test.wantField, test.wantCode) {
				t.Fatalf("violations = %+v, want %s:%s", violations, test.wantField, test.wantCode)
			}
		})
	}
}

func TestPublicationValidationIsDeterministicAndTyped(t *testing.T) {
	t.Parallel()

	candidate := validPublicationCandidate()
	candidate.SeriesID = uuid.Nil
	candidate.Sessions[0].Title = ""
	candidate.Sessions[0].References.ContentReady = false

	first := ValidateInstancePublication(candidate)
	second := ValidateInstancePublication(candidate)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("validation order changed: first=%+v second=%+v", first, second)
	}

	err := CheckInstancePublication(candidate)
	if !errors.Is(err, ErrInvalidInstancePublication) {
		t.Fatalf("CheckInstancePublication() error = %v, want typed sentinel", err)
	}
	var typed *InvalidInstancePublicationError
	if !errors.As(err, &typed) || !reflect.DeepEqual(typed.Violations, first) {
		t.Fatalf("typed error = %#v, want violations %+v", typed, first)
	}
	if !strings.Contains(err.Error(), "sessions[0].title:must_not_be_blank") {
		t.Fatalf("error has no stable violation: %v", err)
	}
}

func TestOfflineCoordinateZeroIsValid(t *testing.T) {
	t.Parallel()

	candidate := validPublicationCandidate()
	candidate.Sessions[0].OfflineLocation.Longitude = floatPointer(0)
	candidate.Sessions[0].OfflineLocation.Latitude = floatPointer(0)
	if err := CheckInstancePublication(candidate); err != nil {
		t.Fatalf("zero coordinate rejected: %v", err)
	}
}

func TestValidQuickTagCodes(t *testing.T) {
	t.Parallel()

	overLimit := make([]string, 21)
	for index := range overLimit {
		overLimit[index] = fmt.Sprintf("tag_%d", index)
	}
	for _, test := range []struct {
		name  string
		codes []string
		want  bool
	}{
		{name: "empty", codes: nil, want: true},
		{name: "valid", codes: []string{"ai", "maker_2"}, want: true},
		{name: "uppercase", codes: []string{"AI"}},
		{name: "overlong", codes: []string{"a12345678901234567890123456789012"}},
		{name: "duplicate", codes: []string{"ai", "ai"}},
		{name: "over limit", codes: overLimit},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := ValidQuickTagCodes(test.codes); got != test.want {
				t.Fatalf("ValidQuickTagCodes(%v) = %t, want %t", test.codes, got, test.want)
			}
		})
	}
}

func validPublicationCandidate() InstancePublicationCandidate {
	registrationStart := time.Date(2026, time.September, 12, 1, 0, 0, 0, time.UTC)
	registrationEnd := registrationStart.Add(24 * time.Hour)
	sessionStart := registrationEnd.Add(time.Hour)
	return InstancePublicationCandidate{
		SeriesID:      uuid.New(),
		InstanceID:    uuid.New(),
		ActivityType:  ActivityTypeAIRoundtable,
		QuickTagCodes: []string{"ai"},
		Sessions: []SessionPublicationCandidate{
			{
				SessionID:           uuid.New(),
				Title:               "Tianjin maker night",
				RegistrationStartAt: registrationStart,
				RegistrationEndAt:   registrationEnd,
				SessionStartAt:      sessionStart,
				SessionEndAt:        sessionStart.Add(2 * time.Hour),
				Capacity:            20,
				GroupMinimum:        5,
				LowStockThreshold:   intPointer(3),
				PriceCents:          9900,
				DeliveryMode:        DeliveryModeOffline,
				Area:                AreaCodeHeping,
				OfflineLocation: &OfflineLocation{
					VenueName: "Xiangwan Lab",
					Address:   "Tianjin",
					Longitude: floatPointer(117.2),
					Latitude:  floatPointer(39.1),
				},
				References: PublicationReferenceReadiness{
					QuestionnaireReady: true,
					PeopleReady:        true,
					ContentReady:       true,
					ResourcesReady:     true,
					QuickTagsReady:     true,
				},
			},
		},
	}
}

func floatPointer(value float64) *float64 {
	return &value
}
