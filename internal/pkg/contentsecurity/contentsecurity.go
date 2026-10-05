// Package contentsecurity provides a platform-level UGC text moderation
// interface.  It wraps the WeChat msgSecCheck v2 API behind an interface so
// any caller can inject a mock in tests and the real implementation is only
// wired in cmd/api/main.go.
//
// Policy (enforced by callers, not this package):
//
//   - suggest=="risky"  → REJECT (caller returns 400; do NOT echo content or label)
//   - suggest=="review" → ALLOW + LOG (flag for human review if infra exists)
//   - transport error   → FAIL-OPEN (log Warn, allow) so a WeChat outage cannot
//     block all UGC submissions
//   - checker==nil      → FAIL-OPEN (skip, allow) — safe before main.go wires it
//
// Scene values follow the WeChat msgSecCheck v2 spec:
//
//	1 = 资料/profile (default when callers pass 0; e.g. nickname)
//	2 = 评论/comment
//	3 = 论坛/forum
//	4 = 社交日志/social-log
package contentsecurity

import "context"

// Suggest is the WeChat moderation verdict.
type Suggest string

const (
	// SuggestPass means content passed moderation.
	SuggestPass Suggest = "pass"
	// SuggestReview means content should be queued for human review.
	SuggestReview Suggest = "review"
	// SuggestRisky means content was flagged as policy-violating and must be rejected.
	SuggestRisky Suggest = "risky"
)

// Result is the outcome of a moderation check.
type Result struct {
	Suggest Suggest
	// Label is the WeChat violation category code (0 = normal).
	// Callers MUST NOT surface this value to end-users; it is for internal logging only.
	Label int
	// TraceID is the WeChat trace identifier for debugging.
	TraceID string
}

// Checker is the platform content-security interface.
// Callers depend only on this interface; the WeChat HTTP impl is
// internal to the wiring layer (cmd/api/main.go).
//
// appID is the WeChat mini-program AppID under which the check is performed.
// openID is the submitter's WeChat openID (required by v2 for user-context scoring).
// text is the content to check.
// scene is the WeChat scene integer (1–4; 0 treated as 1 by the impl).
//
// Semantics of the returned (Result, error):
//   - err != nil → transport/token failure: caller MUST fail-open (allow + warn)
//   - err == nil, Suggest==risky  → caller MUST reject
//   - err == nil, Suggest==review → caller SHOULD allow + flag
//   - err == nil, Suggest==pass   → allow
type Checker interface {
	CheckText(ctx context.Context, appID, openID, text string, scene int) (Result, error)
}

// DisabledChecker is a no-op Checker that always returns pass.
// Used when WeChat credentials are not configured (dev / test environments).
type DisabledChecker struct{}

func (DisabledChecker) CheckText(_ context.Context, _, _, _ string, _ int) (Result, error) {
	return Result{Suggest: SuggestPass}, nil
}
