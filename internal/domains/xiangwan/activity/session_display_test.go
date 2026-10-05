package activity

import (
	"errors"
	"testing"
	"time"
)

func TestDecideSessionDisplayCanonicalPriority(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*SessionDisplayFacts)
		want   DisplayState
	}{
		{
			name: "instance cancellation wins over every lower state",
			mutate: func(facts *SessionDisplayFacts) {
				facts.InstanceCancelled = true
				facts.SeriesInRecurringGap = true
				facts.Now = facts.SessionEndAt
			},
			want: DisplayStateCancelled,
		},
		{
			name: "session cancellation wins over every lower state",
			mutate: func(facts *SessionDisplayFacts) {
				facts.SessionCancelled = true
				facts.SeriesInRecurringGap = true
			},
			want: DisplayStateCancelled,
		},
		{
			name: "recurring gap wins over ended",
			mutate: func(facts *SessionDisplayFacts) {
				facts.SeriesInRecurringGap = true
				facts.Now = facts.SessionEndAt
			},
			want: DisplayStateRecurringGap,
		},
		{
			name: "ended wins over full",
			mutate: func(facts *SessionDisplayFacts) {
				facts.Now = facts.SessionEndAt
				facts.ConfirmedRegistrationCount = facts.Capacity
			},
			want: DisplayStateEnded,
		},
		{
			name: "in progress wins over full",
			mutate: func(facts *SessionDisplayFacts) {
				facts.Now = facts.SessionStartAt
				facts.ConfirmedRegistrationCount = facts.Capacity
			},
			want: DisplayStateInProgress,
		},
		{
			name: "not open wins over full",
			mutate: func(facts *SessionDisplayFacts) {
				facts.Now = facts.RegistrationStartAt.Add(-time.Nanosecond)
				facts.ConfirmedRegistrationCount = facts.Capacity
			},
			want: DisplayStateNotOpen,
		},
		{
			name: "closed wins over full",
			mutate: func(facts *SessionDisplayFacts) {
				facts.Now = facts.RegistrationEndAt
				facts.ConfirmedRegistrationCount = facts.Capacity
			},
			want: DisplayStateClosed,
		},
		{
			name: "confirmed capacity is full",
			mutate: func(facts *SessionDisplayFacts) {
				facts.ConfirmedRegistrationCount = facts.Capacity
			},
			want: DisplayStateFull,
		},
		{
			name: "holds consuming the last capacity are temporarily locked full",
			mutate: func(facts *SessionDisplayFacts) {
				facts.ConfirmedRegistrationCount = 8
				facts.ActiveHoldCount = 2
			},
			want: DisplayStateTemporarilyLockedFull,
		},
		{
			name: "low stock beats group hint",
			mutate: func(facts *SessionDisplayFacts) {
				facts.ConfirmedRegistrationCount = 1
				facts.ActiveHoldCount = 7
				facts.GroupMinimum = 5
				facts.LowStockThreshold = intPointer(2)
			},
			want: DisplayStateOpenLowStock,
		},
		{
			name: "confirmed count below group minimum needs group",
			mutate: func(facts *SessionDisplayFacts) {
				facts.ConfirmedRegistrationCount = 2
				facts.GroupMinimum = 3
			},
			want: DisplayStateOpenNeedGroup,
		},
		{
			name:   "otherwise open",
			mutate: func(*SessionDisplayFacts) {},
			want:   DisplayStateOpen,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			facts := validSessionDisplayFacts()
			test.mutate(&facts)
			decision, err := DecideSessionDisplay(facts)
			if err != nil {
				t.Fatalf("DecideSessionDisplay() error = %v", err)
			}
			if decision.State != test.want {
				t.Fatalf("DecideSessionDisplay().State = %q, want %q", decision.State, test.want)
			}
		})
	}
}

func TestDecideSessionDisplayUsesHalfOpenTimeBoundaries(t *testing.T) {
	t.Parallel()

	base := validSessionDisplayFacts()
	tests := []struct {
		name string
		now  time.Time
		want DisplayState
	}{
		{name: "immediately before registration", now: base.RegistrationStartAt.Add(-time.Nanosecond), want: DisplayStateNotOpen},
		{name: "registration start", now: base.RegistrationStartAt, want: DisplayStateOpen},
		{name: "immediately before registration end", now: base.RegistrationEndAt.Add(-time.Nanosecond), want: DisplayStateOpen},
		{name: "registration end", now: base.RegistrationEndAt, want: DisplayStateClosed},
		{name: "immediately before session start", now: base.SessionStartAt.Add(-time.Nanosecond), want: DisplayStateClosed},
		{name: "session start", now: base.SessionStartAt, want: DisplayStateInProgress},
		{name: "immediately before session end", now: base.SessionEndAt.Add(-time.Nanosecond), want: DisplayStateInProgress},
		{name: "session end", now: base.SessionEndAt, want: DisplayStateEnded},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			facts := base
			facts.Now = test.now
			decision, err := DecideSessionDisplay(facts)
			if err != nil {
				t.Fatalf("DecideSessionDisplay() error = %v", err)
			}
			if decision.State != test.want {
				t.Fatalf("DecideSessionDisplay().State = %q, want %q", decision.State, test.want)
			}
		})
	}
}

func TestDecideSessionDisplayReportsCapacitySemantics(t *testing.T) {
	t.Parallel()

	facts := validSessionDisplayFacts()
	facts.Capacity = 12
	facts.ConfirmedRegistrationCount = 4
	facts.ActiveHoldCount = 3
	facts.GroupMinimum = 7
	facts.LowStockThreshold = intPointer(5)

	decision, err := DecideSessionDisplay(facts)
	if err != nil {
		t.Fatalf("DecideSessionDisplay() error = %v", err)
	}
	if decision.State != DisplayStateOpenLowStock {
		t.Fatalf("State = %q, want %q", decision.State, DisplayStateOpenLowStock)
	}
	if decision.ConfirmedRegistrationCount != 4 {
		t.Fatalf("ConfirmedRegistrationCount = %d, want 4", decision.ConfirmedRegistrationCount)
	}
	if decision.SellableCapacity != 5 {
		t.Fatalf("SellableCapacity = %d, want 5", decision.SellableCapacity)
	}
	if decision.NeededToReachGroupMinimum != 3 {
		t.Fatalf("NeededToReachGroupMinimum = %d, want 3", decision.NeededToReachGroupMinimum)
	}
}

func TestDecideSessionDisplayComparesInstantsNotLocations(t *testing.T) {
	t.Parallel()

	utcFacts := validSessionDisplayFacts()
	chinaStandardTime := time.FixedZone("Asia/Shanghai", 8*60*60)
	localFacts := utcFacts
	localFacts.Now = utcFacts.Now.In(chinaStandardTime)
	localFacts.RegistrationStartAt = utcFacts.RegistrationStartAt.In(chinaStandardTime)
	localFacts.RegistrationEndAt = utcFacts.RegistrationEndAt.In(chinaStandardTime)
	localFacts.SessionStartAt = utcFacts.SessionStartAt.In(chinaStandardTime)
	localFacts.SessionEndAt = utcFacts.SessionEndAt.In(chinaStandardTime)

	utcDecision, err := DecideSessionDisplay(utcFacts)
	if err != nil {
		t.Fatalf("DecideSessionDisplay(UTC) error = %v", err)
	}
	localDecision, err := DecideSessionDisplay(localFacts)
	if err != nil {
		t.Fatalf("DecideSessionDisplay(Asia/Shanghai) error = %v", err)
	}
	if localDecision != utcDecision {
		t.Fatalf("same instant produced different decisions: local=%+v UTC=%+v", localDecision, utcDecision)
	}
}

func TestDisplayStateSemantics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		state               DisplayState
		wantGroup           HomeGroup
		wantKnown           bool
		registrationAllowed bool
	}{
		{state: DisplayStateOpen, wantGroup: HomeGroupOpen, wantKnown: true, registrationAllowed: true},
		{state: DisplayStateOpenLowStock, wantGroup: HomeGroupOpen, wantKnown: true, registrationAllowed: true},
		{state: DisplayStateOpenNeedGroup, wantGroup: HomeGroupOpen, wantKnown: true, registrationAllowed: true},
		{state: DisplayStateTemporarilyLockedFull, wantGroup: HomeGroupFutureUnavailable, wantKnown: true},
		{state: DisplayStateNotOpen, wantGroup: HomeGroupFutureUnavailable, wantKnown: true},
		{state: DisplayStateFull, wantGroup: HomeGroupFutureUnavailable, wantKnown: true},
		{state: DisplayStateClosed, wantGroup: HomeGroupFutureUnavailable, wantKnown: true},
		{state: DisplayStateInProgress, wantGroup: HomeGroupInProgress, wantKnown: true},
		{state: DisplayStateRecurringGap, wantGroup: HomeGroupRecurringGap, wantKnown: true},
		{state: DisplayStateEnded, wantGroup: HomeGroupEnded, wantKnown: true},
		{state: DisplayStateCancelled, wantGroup: HomeGroupCancelled, wantKnown: true},
		{state: DisplayState("unknown")},
	}

	for _, test := range tests {
		t.Run(string(test.state), func(t *testing.T) {
			t.Parallel()

			group, known := test.state.HomeGroup()
			if group != test.wantGroup || known != test.wantKnown {
				t.Fatalf("HomeGroup() = (%d, %t), want (%d, %t)", group, known, test.wantGroup, test.wantKnown)
			}
			if got := test.state.RegistrationAllowed(); got != test.registrationAllowed {
				t.Fatalf("RegistrationAllowed() = %t, want %t", got, test.registrationAllowed)
			}
		})
	}
}

func TestValidateSessionDisplayFacts(t *testing.T) {
	t.Parallel()

	maxInt := int(^uint(0) >> 1)
	tests := []struct {
		name      string
		mutate    func(*SessionDisplayFacts)
		wantField string
		wantCode  ViolationCode
	}{
		{name: "now required", mutate: func(f *SessionDisplayFacts) { f.Now = time.Time{} }, wantField: "now", wantCode: ViolationRequired},
		{name: "registration start required", mutate: func(f *SessionDisplayFacts) { f.RegistrationStartAt = time.Time{} }, wantField: "registration_start_at", wantCode: ViolationRequired},
		{name: "registration end required", mutate: func(f *SessionDisplayFacts) { f.RegistrationEndAt = time.Time{} }, wantField: "registration_end_at", wantCode: ViolationRequired},
		{name: "session start required", mutate: func(f *SessionDisplayFacts) { f.SessionStartAt = time.Time{} }, wantField: "session_start_at", wantCode: ViolationRequired},
		{name: "session end required", mutate: func(f *SessionDisplayFacts) { f.SessionEndAt = time.Time{} }, wantField: "session_end_at", wantCode: ViolationRequired},
		{name: "registration interval must be non-empty", mutate: func(f *SessionDisplayFacts) { f.RegistrationStartAt = f.RegistrationEndAt }, wantField: "registration_start_at", wantCode: ViolationMustBeBefore},
		{name: "registration cannot end after session starts", mutate: func(f *SessionDisplayFacts) { f.RegistrationEndAt = f.SessionStartAt.Add(time.Nanosecond) }, wantField: "registration_end_at", wantCode: ViolationMustNotBeAfter},
		{name: "session interval must be non-empty", mutate: func(f *SessionDisplayFacts) { f.SessionStartAt = f.SessionEndAt }, wantField: "session_start_at", wantCode: ViolationMustBeBefore},
		{name: "capacity must be positive", mutate: func(f *SessionDisplayFacts) { f.Capacity = 0 }, wantField: "capacity", wantCode: ViolationMustBePositive},
		{name: "confirmed count cannot be negative", mutate: func(f *SessionDisplayFacts) { f.ConfirmedRegistrationCount = -1 }, wantField: "confirmed_registration_count", wantCode: ViolationMustBeNonNegative},
		{name: "hold count cannot be negative", mutate: func(f *SessionDisplayFacts) { f.ActiveHoldCount = -1 }, wantField: "active_hold_count", wantCode: ViolationMustBeNonNegative},
		{name: "confirmed count cannot exceed capacity", mutate: func(f *SessionDisplayFacts) { f.ConfirmedRegistrationCount = f.Capacity + 1 }, wantField: "confirmed_registration_count", wantCode: ViolationExceedsCapacity},
		{name: "holds cannot exceed remaining capacity", mutate: func(f *SessionDisplayFacts) { f.ActiveHoldCount = f.Capacity - f.ConfirmedRegistrationCount + 1 }, wantField: "active_hold_count", wantCode: ViolationExceedsSellableCapacity},
		{name: "group minimum must be positive", mutate: func(f *SessionDisplayFacts) { f.GroupMinimum = 0 }, wantField: "group_minimum", wantCode: ViolationMustBePositive},
		{name: "group minimum cannot exceed capacity", mutate: func(f *SessionDisplayFacts) { f.GroupMinimum = f.Capacity + 1 }, wantField: "group_minimum", wantCode: ViolationExceedsCapacity},
		{name: "low stock threshold must be positive", mutate: func(f *SessionDisplayFacts) { f.LowStockThreshold = intPointer(0) }, wantField: "low_stock_threshold", wantCode: ViolationMustBePositive},
		{name: "low stock threshold must be below capacity", mutate: func(f *SessionDisplayFacts) { f.LowStockThreshold = intPointer(f.Capacity) }, wantField: "low_stock_threshold", wantCode: ViolationMustBeLessThanCapacity},
		{
			name: "capacity arithmetic does not overflow",
			mutate: func(f *SessionDisplayFacts) {
				f.Capacity = maxInt
				f.ConfirmedRegistrationCount = maxInt
				f.ActiveHoldCount = 1
				f.GroupMinimum = 1
			},
			wantField: "active_hold_count",
			wantCode:  ViolationExceedsSellableCapacity,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			facts := validSessionDisplayFacts()
			test.mutate(&facts)
			violations := ValidateSessionDisplayFacts(facts)
			if !containsViolation(violations, test.wantField, test.wantCode) {
				t.Fatalf("violations = %+v, want %s:%s", violations, test.wantField, test.wantCode)
			}

			decision, err := DecideSessionDisplay(facts)
			if !errors.Is(err, ErrInvalidSessionDisplayFacts) {
				t.Fatalf("DecideSessionDisplay() error = %v, want ErrInvalidSessionDisplayFacts", err)
			}
			if decision != (SessionDisplayDecision{}) {
				t.Fatalf("invalid facts returned partial decision %+v", decision)
			}
		})
	}
}

func validSessionDisplayFacts() SessionDisplayFacts {
	registrationStart := time.Date(2026, time.September, 10, 1, 0, 0, 0, time.UTC)
	registrationEnd := registrationStart.Add(24 * time.Hour)
	sessionStart := registrationEnd.Add(time.Hour)
	return SessionDisplayFacts{
		Now:                        registrationStart.Add(time.Hour),
		RegistrationStartAt:        registrationStart,
		RegistrationEndAt:          registrationEnd,
		SessionStartAt:             sessionStart,
		SessionEndAt:               sessionStart.Add(2 * time.Hour),
		Capacity:                   10,
		ConfirmedRegistrationCount: 4,
		ActiveHoldCount:            0,
		GroupMinimum:               3,
	}
}

func intPointer(value int) *int {
	return &value
}

func containsViolation(violations []FactViolation, field string, code ViolationCode) bool {
	for _, violation := range violations {
		if violation.Field == field && violation.Code == code {
			return true
		}
	}
	return false
}
