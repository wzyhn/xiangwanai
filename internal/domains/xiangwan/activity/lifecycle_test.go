package activity

import (
	"errors"
	"reflect"
	"testing"
)

func TestDecideInstanceClosure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		sessions []SessionTerminalFact
		want     InstanceClosureDecision
	}{
		{
			name:     "one non-terminal session keeps the instance open",
			sessions: []SessionTerminalFact{SessionTerminalFactNonTerminal},
			want: InstanceClosureDecision{
				Closure:      InstanceClosureNone,
				SessionCount: 1,
			},
		},
		{
			name: "a non-terminal sibling blocks completion",
			sessions: []SessionTerminalFact{
				SessionTerminalFactEnded,
				SessionTerminalFactNonTerminal,
				SessionTerminalFactCancelled,
			},
			want: InstanceClosureDecision{
				Closure:        InstanceClosureNone,
				SessionCount:   3,
				EndedCount:     1,
				CancelledCount: 1,
			},
		},
		{
			name: "all cancelled closes as cancelled",
			sessions: []SessionTerminalFact{
				SessionTerminalFactCancelled,
				SessionTerminalFactCancelled,
			},
			want: InstanceClosureDecision{
				Closure:        InstanceClosureCancelled,
				SessionCount:   2,
				CancelledCount: 2,
			},
		},
		{
			name: "all ended closes as completed",
			sessions: []SessionTerminalFact{
				SessionTerminalFactEnded,
				SessionTerminalFactEnded,
			},
			want: InstanceClosureDecision{
				Closure:      InstanceClosureCompleted,
				SessionCount: 2,
				EndedCount:   2,
			},
		},
		{
			name: "held and cancelled sessions close as completed",
			sessions: []SessionTerminalFact{
				SessionTerminalFactCancelled,
				SessionTerminalFactEnded,
				SessionTerminalFactCancelled,
			},
			want: InstanceClosureDecision{
				Closure:        InstanceClosureCompleted,
				SessionCount:   3,
				EndedCount:     1,
				CancelledCount: 2,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := DecideInstanceClosure(test.sessions)
			if err != nil {
				t.Fatalf("DecideInstanceClosure() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("DecideInstanceClosure() = %+v, want %+v", got, test.want)
			}
			if got.ShouldTransition() != (test.want.Closure != InstanceClosureNone) {
				t.Fatalf("ShouldTransition() disagrees with closure %q", got.Closure)
			}
		})
	}
}

func TestDecideInstanceClosureIsOrderIndependent(t *testing.T) {
	t.Parallel()

	permutations := [][]SessionTerminalFact{
		{SessionTerminalFactEnded, SessionTerminalFactCancelled, SessionTerminalFactCancelled},
		{SessionTerminalFactCancelled, SessionTerminalFactEnded, SessionTerminalFactCancelled},
		{SessionTerminalFactCancelled, SessionTerminalFactCancelled, SessionTerminalFactEnded},
	}

	want, err := DecideInstanceClosure(permutations[0])
	if err != nil {
		t.Fatalf("DecideInstanceClosure() error = %v", err)
	}
	for _, sessions := range permutations[1:] {
		got, decisionErr := DecideInstanceClosure(sessions)
		if decisionErr != nil {
			t.Fatalf("DecideInstanceClosure() error = %v", decisionErr)
		}
		if got != want {
			t.Fatalf("permutation changed decision: got %+v, want %+v", got, want)
		}
	}
}

func TestDecideInstanceClosureRejectsInvalidInputWithoutPartialDecision(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		sessions []SessionTerminalFact
		wantErr  error
	}{
		{
			name:    "empty aggregate",
			wantErr: ErrInstanceHasNoSessions,
		},
		{
			name: "unknown fact",
			sessions: []SessionTerminalFact{
				SessionTerminalFactEnded,
				SessionTerminalFact("archived"),
			},
			wantErr: ErrUnknownSessionTerminalFact,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := DecideInstanceClosure(test.sessions)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("DecideInstanceClosure() error = %v, want %v", err, test.wantErr)
			}
			if got != (InstanceClosureDecision{}) {
				t.Fatalf("invalid input returned partial decision %+v", got)
			}
		})
	}
}

func TestDecideInstanceClosureDoesNotMutateInput(t *testing.T) {
	t.Parallel()

	sessions := []SessionTerminalFact{
		SessionTerminalFactEnded,
		SessionTerminalFactCancelled,
	}
	wantInput := append([]SessionTerminalFact(nil), sessions...)

	if _, err := DecideInstanceClosure(sessions); err != nil {
		t.Fatalf("DecideInstanceClosure() error = %v", err)
	}
	if !reflect.DeepEqual(sessions, wantInput) {
		t.Fatalf("input mutated: got %v, want %v", sessions, wantInput)
	}
}

func TestDecideSeriesRecurringIsIrreversible(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		alreadyRecurring bool
		publishedCount   int
		want             bool
	}{
		{name: "zero publications is not recurring", publishedCount: 0},
		{name: "one publication is not recurring", publishedCount: 1},
		{name: "second publication becomes recurring", publishedCount: 2, want: true},
		{name: "later publications remain recurring", publishedCount: 9, want: true},
		{name: "stored recurring fact never reverses", alreadyRecurring: true, publishedCount: 0, want: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := DecideSeriesRecurring(test.alreadyRecurring, test.publishedCount)
			if err != nil {
				t.Fatalf("DecideSeriesRecurring() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("DecideSeriesRecurring() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestDecideSeriesRecurringRejectsNegativeCount(t *testing.T) {
	t.Parallel()

	got, err := DecideSeriesRecurring(true, -1)
	if !errors.Is(err, ErrNegativePublishedInstanceCount) {
		t.Fatalf("DecideSeriesRecurring() error = %v, want ErrNegativePublishedInstanceCount", err)
	}
	if got {
		t.Fatal("invalid count returned a recurring decision")
	}
}

func TestShouldDisplayRecurringGap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		seriesRecurring   bool
		latestClosure     InstanceClosure
		hasLaterPublished bool
		want              bool
	}{
		{
			name:            "completed recurring series with no next instance is a gap",
			seriesRecurring: true,
			latestClosure:   InstanceClosureCompleted,
			want:            true,
		},
		{
			name:          "single activity never becomes a recurring gap",
			latestClosure: InstanceClosureCompleted,
		},
		{
			name:              "new published instance closes the gap",
			seriesRecurring:   true,
			latestClosure:     InstanceClosureCompleted,
			hasLaterPublished: true,
		},
		{
			name:            "all-cancelled instance does not become a gap",
			seriesRecurring: true,
			latestClosure:   InstanceClosureCancelled,
		},
		{
			name:            "non-terminal instance does not become a gap",
			seriesRecurring: true,
			latestClosure:   InstanceClosureNone,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := ShouldDisplayRecurringGap(
				test.seriesRecurring,
				test.latestClosure,
				test.hasLaterPublished,
			)
			if got != test.want {
				t.Fatalf("ShouldDisplayRecurringGap() = %t, want %t", got, test.want)
			}
		})
	}
}
