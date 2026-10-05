package registration

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

const maximumSelfCancellationCutoff = 366 * 24 * time.Hour

var (
	ErrInvalidSelfCancellationPolicy = errors.New(
		"invalid xiangwan self-cancellation policy",
	)
	selfCancellationPolicyVersionPattern = regexp.MustCompile(
		`^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$`,
	)
)

type SelfCancellationPolicyInput struct {
	TenantID       uuid.UUID
	RegistrationID uuid.UUID
	PrincipalID    uuid.UUID
	SessionID      uuid.UUID
	SessionStartAt time.Time
	EvaluatedAt    time.Time
}

type SelfCancellationPolicyDecision struct {
	Allowed       bool
	PolicyVersion string
}

// SelfCancellationPolicy is a deterministic local policy. Callers may invoke
// it while holding PostgreSQL aggregate locks, so implementations must not
// depend on a remote service.
type SelfCancellationPolicy interface {
	EvaluateSelfCancellation(
		context.Context,
		SelfCancellationPolicyInput,
	) (SelfCancellationPolicyDecision, error)
}

type fixedSelfCancellationPolicy struct {
	version string
	cutoff  time.Duration
}

// NewSelfCancellationPolicy creates the versioned CONFIG-CANCEL-POLICY
// evaluator shared by read-side capability projection and the write-side
// transactional recheck. An empty version is valid only for the default free
// cancellation rule whose cutoff is Session start.
func NewSelfCancellationPolicy(
	version string,
	cutoff time.Duration,
) (SelfCancellationPolicy, error) {
	if !validSelfCancellationPolicyConfiguration(version, cutoff) {
		return nil, ErrInvalidSelfCancellationPolicy
	}
	return &fixedSelfCancellationPolicy{
		version: version,
		cutoff:  cutoff,
	}, nil
}

func (policy *fixedSelfCancellationPolicy) EvaluateSelfCancellation(
	ctx context.Context,
	input SelfCancellationPolicyInput,
) (SelfCancellationPolicyDecision, error) {
	if policy == nil || ctx == nil || ctx.Err() != nil {
		return SelfCancellationPolicyDecision{},
			ErrInvalidSelfCancellationPolicy
	}
	return EvaluateSelfCancellationCutoff(
		input,
		policy.version,
		policy.cutoff,
	)
}

// EvaluateSelfCancellationCutoff is the single cutoff calculation used by
// both default and configured policy adapters. The exact boundary is allowed;
// one instant after it is not.
func EvaluateSelfCancellationCutoff(
	input SelfCancellationPolicyInput,
	policyVersion string,
	cutoff time.Duration,
) (SelfCancellationPolicyDecision, error) {
	if !validSelfCancellationPolicyInput(input) ||
		!validSelfCancellationPolicyConfiguration(policyVersion, cutoff) {
		return SelfCancellationPolicyDecision{},
			ErrInvalidSelfCancellationPolicy
	}
	allowed, err := SelfCancellationWithinCutoff(
		input.SessionStartAt,
		input.EvaluatedAt,
		cutoff,
	)
	if err != nil {
		return SelfCancellationPolicyDecision{}, err
	}
	return SelfCancellationPolicyDecision{
		Allowed:       allowed,
		PolicyVersion: policyVersion,
	}, nil
}

func SelfCancellationWithinCutoff(
	sessionStartAt time.Time,
	evaluatedAt time.Time,
	cutoff time.Duration,
) (bool, error) {
	if sessionStartAt.IsZero() || evaluatedAt.IsZero() ||
		cutoff < 0 || cutoff > maximumSelfCancellationCutoff {
		return false, ErrInvalidSelfCancellationPolicy
	}
	return !sessionStartAt.Before(evaluatedAt.Add(cutoff)), nil
}

func ValidateSelfCancellationPolicyDecision(
	decision SelfCancellationPolicyDecision,
) error {
	if decision.PolicyVersion != "" &&
		(!selfCancellationPolicyVersionPattern.MatchString(decision.PolicyVersion) ||
			decision.PolicyVersion != strings.TrimSpace(decision.PolicyVersion)) {
		return ErrInvalidSelfCancellationPolicy
	}
	return nil
}

func validSelfCancellationPolicyInput(input SelfCancellationPolicyInput) bool {
	return input.TenantID != uuid.Nil &&
		input.RegistrationID != uuid.Nil &&
		input.PrincipalID != uuid.Nil &&
		input.SessionID != uuid.Nil &&
		!input.SessionStartAt.IsZero() &&
		!input.EvaluatedAt.IsZero()
}

func validSelfCancellationPolicyConfiguration(
	version string,
	cutoff time.Duration,
) bool {
	if cutoff < 0 || cutoff > maximumSelfCancellationCutoff ||
		version != strings.TrimSpace(version) {
		return false
	}
	if version == "" {
		return cutoff == 0
	}
	return selfCancellationPolicyVersionPattern.MatchString(version)
}
