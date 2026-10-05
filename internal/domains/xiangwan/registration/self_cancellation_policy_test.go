package registration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSelfCancellationPolicyUsesExactVersionedCutoff(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 20, 8, 0, 0, 0, time.UTC)
	policy, err := NewSelfCancellationPolicy("cancel:v3", 24*time.Hour)
	if err != nil {
		t.Fatalf("NewSelfCancellationPolicy() error = %v", err)
	}
	input := selfCancellationPolicyInput(now, now.Add(24*time.Hour))
	decision, err := policy.EvaluateSelfCancellation(context.Background(), input)
	if err != nil || !decision.Allowed || decision.PolicyVersion != "cancel:v3" {
		t.Fatalf("EvaluateSelfCancellation(cutoff) = %+v, %v", decision, err)
	}

	input.EvaluatedAt = input.EvaluatedAt.Add(time.Nanosecond)
	decision, err = policy.EvaluateSelfCancellation(context.Background(), input)
	if err != nil || decision.Allowed || decision.PolicyVersion != "cancel:v3" {
		t.Fatalf("EvaluateSelfCancellation(after cutoff) = %+v, %v", decision, err)
	}
}

func TestDefaultSelfCancellationPolicyStopsAtSessionStart(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 20, 8, 0, 0, 0, time.UTC)
	policy, err := NewSelfCancellationPolicy("", 0)
	if err != nil {
		t.Fatalf("NewSelfCancellationPolicy(default) error = %v", err)
	}
	input := selfCancellationPolicyInput(now, now.Add(time.Hour))
	decision, err := policy.EvaluateSelfCancellation(context.Background(), input)
	if err != nil || !decision.Allowed || decision.PolicyVersion != "" {
		t.Fatalf("EvaluateSelfCancellation(before start) = %+v, %v", decision, err)
	}
	input.EvaluatedAt = input.SessionStartAt.Add(time.Nanosecond)
	decision, err = policy.EvaluateSelfCancellation(context.Background(), input)
	if err != nil || decision.Allowed {
		t.Fatalf("EvaluateSelfCancellation(after start) = %+v, %v", decision, err)
	}
}

func TestSelfCancellationPolicyRejectsInvalidConfigurationAndInput(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		version string
		cutoff  time.Duration
	}{
		{version: "", cutoff: time.Hour},
		{version: " bad", cutoff: time.Hour},
		{version: "cancel-v3", cutoff: -time.Hour},
		{version: "cancel-v3", cutoff: maximumSelfCancellationCutoff + time.Hour},
	} {
		if _, err := NewSelfCancellationPolicy(test.version, test.cutoff); !errors.Is(
			err,
			ErrInvalidSelfCancellationPolicy,
		) {
			t.Fatalf("NewSelfCancellationPolicy(%q, %s) error = %v", test.version, test.cutoff, err)
		}
	}

	policy, err := NewSelfCancellationPolicy("cancel-v3", time.Hour)
	if err != nil {
		t.Fatalf("NewSelfCancellationPolicy() error = %v", err)
	}
	if _, err = policy.EvaluateSelfCancellation(
		context.Background(),
		SelfCancellationPolicyInput{},
	); !errors.Is(err, ErrInvalidSelfCancellationPolicy) {
		t.Fatalf("EvaluateSelfCancellation(invalid) error = %v", err)
	}
	if err = ValidateSelfCancellationPolicyDecision(SelfCancellationPolicyDecision{
		Allowed:       true,
		PolicyVersion: "bad version",
	}); !errors.Is(err, ErrInvalidSelfCancellationPolicy) {
		t.Fatalf("ValidateSelfCancellationPolicyDecision() error = %v", err)
	}
}

func selfCancellationPolicyInput(
	evaluatedAt time.Time,
	sessionStartAt time.Time,
) SelfCancellationPolicyInput {
	return SelfCancellationPolicyInput{
		TenantID:       uuid.New(),
		RegistrationID: uuid.New(),
		PrincipalID:    uuid.New(),
		SessionID:      uuid.New(),
		SessionStartAt: sessionStartAt,
		EvaluatedAt:    evaluatedAt,
	}
}
