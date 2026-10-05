package contentsecurity

import (
	"context"

	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/wzyhn/xiangwanai/internal/pkg/logx"

	"go.uber.org/zap"
)

// EnforceText runs a text check via checker and applies the platform policy:
//
//   - checker == nil           → pass (fail-open; not yet wired)
//   - transport/token error    → pass + Warn log (fail-open; WeChat outage must not block UGC)
//   - suggest == "pass"        → pass
//   - suggest == "review"      → pass + Info log (flag for human review)
//   - suggest == "risky"       → reject with a generic 400 (no content/label echo)
//
// caller is a short string identifying the call site for logs (e.g. "compass_feedback.Submit").
// openID may be empty for system-submitted content (checker will receive "" and WeChat may
// return a lower-quality result; this is acceptable for non-user-facing writes).
// User-submitted paths in the ADR-037 migration should call EnforceUserTextLegacy
// so resolver/checker fail-open branches also reach the shared observer.
func EnforceText(ctx context.Context, checker Checker, caller, appID, openID, text string, scene int) error {
	return enforceTextObserved(ctx, checker, NoopLegacyObserver{}, caller, appID, openID, text, scene)
}

func enforceTextObserved(ctx context.Context, checker Checker, observer LegacyObserver, caller, appID, openID, text string, scene int) error {
	observer = legacyObserverOrNoop(observer)
	if IsDisabledChecker(checker) {
		observer.RecordLegacyFailOpen(ctx, LegacyCheckerNotConfigured)
		return nil // fail-open: not configured
	}

	result, err := checker.CheckText(ctx, appID, openID, text, scene)
	if err != nil {
		observer.RecordLegacyFailOpen(ctx, LegacyCheckerTransient)
		// Fail-open: log and allow. A WeChat outage must not block legitimate UGC.
		logx.FromContext(ctx).Warn(
			"contentsecurity: check failed (fail-open)",
			zap.String("caller", caller),
			zap.Error(err),
		)
		return nil
	}

	switch result.Suggest {
	case SuggestRisky:
		// Do NOT echo content, label, or trace_id in the user-facing error.
		logx.FromContext(ctx).Info(
			"contentsecurity: risky content rejected",
			zap.String("caller", caller),
			zap.Int("label", result.Label),
			zap.String("trace_id", result.TraceID),
		)
		return errx.NewBadRequest("内容含违规信息，提交已被拒绝")

	case SuggestReview:
		observer.RecordLegacyFailOpen(ctx, LegacyReviewAllowed)
		// Allow but flag. No human-review queue exists yet; log is the audit trail.
		logx.FromContext(ctx).Info(
			"contentsecurity: content flagged for review (allowed)",
			zap.String("caller", caller),
			zap.Int("label", result.Label),
			zap.String("trace_id", result.TraceID),
		)
		return nil

	case SuggestPass:
		return nil

	default: // unrecognised value → allow under the legacy policy, but observe it
		observer.RecordLegacyFailOpen(ctx, LegacyInvalidResponse)
		logx.FromContext(ctx).Warn(
			"contentsecurity: unrecognized checker response (fail-open)",
			zap.String("caller", caller),
		)
		return nil
	}
}
