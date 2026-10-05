package contentsecurity

import (
	"context"
	"errors"
	"strings"

	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/wzyhn/xiangwanai/internal/pkg/logx"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// LegacyFailOpenKind is the bounded outcome vocabulary emitted while callers
// still use the pre-ADR-037 allow-on-failure policy. It describes the legacy
// branch, not the future publication decision.
type LegacyFailOpenKind string

const (
	LegacyCheckerNotConfigured  LegacyFailOpenKind = "checker_not_configured"
	LegacyInvalidAppBinding     LegacyFailOpenKind = "invalid_app_binding"
	LegacyResolverNotConfigured LegacyFailOpenKind = "resolver_not_configured"
	LegacyIdentityNotBound      LegacyFailOpenKind = "identity_not_bound"
	LegacyResolverUnavailable   LegacyFailOpenKind = "resolver_unavailable"
	LegacyCheckerTransient      LegacyFailOpenKind = "checker_transient"
	LegacyReviewAllowed         LegacyFailOpenKind = "review_allowed"
	LegacyInvalidResponse       LegacyFailOpenKind = "invalid_response"
)

// Valid reports whether k is a registered legacy fail-open outcome.
func (k LegacyFailOpenKind) Valid() bool {
	switch k {
	case LegacyCheckerNotConfigured,
		LegacyInvalidAppBinding,
		LegacyResolverNotConfigured,
		LegacyIdentityNotBound,
		LegacyResolverUnavailable,
		LegacyCheckerTransient,
		LegacyReviewAllowed,
		LegacyInvalidResponse:
		return true
	default:
		return false
	}
}

// LegacyObserver is the lower-layer injection seam used by auth/content and
// legacy product modules. The composition root binds it to a fixed product and
// surface in the L2 moderation observer, so callers cannot supply metric labels.
type LegacyObserver interface {
	RecordLegacyFailOpen(ctx context.Context, kind LegacyFailOpenKind)
}

// NoopLegacyObserver preserves existing behavior in tests and non-API binaries.
type NoopLegacyObserver struct{}

func (NoopLegacyObserver) RecordLegacyFailOpen(context.Context, LegacyFailOpenKind) {}

func legacyObserverOrNoop(observer LegacyObserver) LegacyObserver {
	if observer == nil {
		return NoopLegacyObserver{}
	}
	return observer
}

// IsDisabledChecker reports whether the legacy checker cannot make a real
// provider decision. It recognizes both value and pointer DisabledChecker.
func IsDisabledChecker(checker Checker) bool {
	if checker == nil {
		return true
	}
	switch checker.(type) {
	case DisabledChecker, *DisabledChecker:
		return true
	default:
		return false
	}
}

// PrepareLegacyUserCheck validates checker/app binding and resolves the exact
// app-scoped openid required by msgSecCheck v2. Every branch that returns false
// records one bounded fail-open outcome; live allow/reject behavior is unchanged.
func PrepareLegacyUserCheck(
	ctx context.Context,
	checker Checker,
	observer LegacyObserver,
	resolver func(principalID uuid.UUID, appID string) (string, error),
	principalID uuid.UUID,
	appID string,
) (string, bool) {
	observer = legacyObserverOrNoop(observer)
	kind := LegacyFailOpenKind("")
	switch {
	case IsDisabledChecker(checker):
		kind = LegacyCheckerNotConfigured
	case strings.TrimSpace(appID) == "":
		kind = LegacyInvalidAppBinding
	case resolver == nil:
		kind = LegacyResolverNotConfigured
	}
	if kind != "" {
		observer.RecordLegacyFailOpen(ctx, kind)
		logLegacyPreflightSkip(ctx, kind)
		return "", false
	}

	openID, err := resolver(principalID, strings.TrimSpace(appID))
	if err != nil {
		kind = classifyLegacyResolverError(err)
		observer.RecordLegacyFailOpen(ctx, kind)
		logLegacyPreflightSkip(ctx, kind)
		return "", false
	}
	openID = strings.TrimSpace(openID)
	if openID == "" {
		observer.RecordLegacyFailOpen(ctx, LegacyIdentityNotBound)
		logLegacyPreflightSkip(ctx, LegacyIdentityNotBound)
		return "", false
	}
	return openID, true
}

func classifyLegacyResolverError(err error) LegacyFailOpenKind {
	var businessErr *errx.Error
	if errors.As(err, &businessErr) {
		switch businessErr.Code {
		case errx.CodeForbidden, errx.CodeNotFound:
			return LegacyIdentityNotBound
		case errx.CodeBadRequest:
			return LegacyInvalidAppBinding
		}
	}
	return LegacyResolverUnavailable
}

func logLegacyPreflightSkip(ctx context.Context, kind LegacyFailOpenKind) {
	logx.FromContext(ctx).Debug(
		"contentsecurity: legacy user check skipped",
		zap.String("reason", string(kind)),
	)
}

// EnforceUserTextLegacy performs the complete legacy user-text flow. It fails
// open exactly as before, but records why no affirmative checker pass existed.
func EnforceUserTextLegacy(
	ctx context.Context,
	checker Checker,
	observer LegacyObserver,
	resolver func(principalID uuid.UUID, appID string) (string, error),
	principalID uuid.UUID,
	caller string,
	appID string,
	text string,
	scene int,
) error {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	openID, ok := PrepareLegacyUserCheck(ctx, checker, observer, resolver, principalID, appID)
	if !ok {
		return nil
	}
	return EnforcePreparedUserTextLegacy(ctx, checker, observer, caller, appID, openID, text, scene)
}

// EnforcePreparedUserTextLegacy checks text after one successful preflight.
// Chunked callers use it to resolve the openid once and moderate every chunk.
func EnforcePreparedUserTextLegacy(
	ctx context.Context,
	checker Checker,
	observer LegacyObserver,
	caller string,
	appID string,
	openID string,
	text string,
	scene int,
) error {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	observer = legacyObserverOrNoop(observer)
	if IsDisabledChecker(checker) {
		observer.RecordLegacyFailOpen(ctx, LegacyCheckerNotConfigured)
		return nil
	}
	if strings.TrimSpace(appID) == "" {
		observer.RecordLegacyFailOpen(ctx, LegacyInvalidAppBinding)
		return nil
	}
	if strings.TrimSpace(openID) == "" {
		observer.RecordLegacyFailOpen(ctx, LegacyIdentityNotBound)
		return nil
	}
	return enforceTextObserved(ctx, checker, observer, caller, appID, openID, text, scene)
}
