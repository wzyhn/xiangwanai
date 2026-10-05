package contentsecurity

// Image (async) moderation capability — the IMAGE sibling of the synchronous
// text Checker in this package.
//
// WeChat MANDATES content-security for UGC images (mediaCheckAsync v2). Unlike
// msgSecCheck (text, synchronous verdict), mediaCheckAsync returns immediately
// with a trace_id and the real verdict arrives later via a push callback. The
// platform contract therefore differs from EnforceText:
//
//   - text  → sync: submit blocks until verdict; risky → reject inline.
//   - image → async: submit ENQUEUES the check, persists a pending verdict, and
//     withholds the image until the callback flips it (hide-until-pass). A risky
//     callback triggers a takedown. This mirrors community's image-moderation
//     flow (internal/domains/community: hide-until-pass + reject-on-risky).
//
// Policy (enforced by callers / the callback, not this package):
//
//   - moderator == nil         → FAIL-OPEN (skip enqueue, allow) — safe before
//     main.go wires it (dev / test without WeChat creds).
//   - enqueue transport error  → FAIL-OPEN (log Warn, allow) so a WeChat outage
//     cannot block all UGC image submissions.
//   - callback suggest==pass   → release / leave for the normal review gate.
//   - callback suggest==risky  → takedown (hide / remove the image-bearing item).
//   - callback suggest==review → treat as pending → leave withheld (no auto-pass).
//
// Scene values follow the WeChat API spec (same enum as text):
//
//	1 = resource (default when callers pass 0)
//	2 = comment / profile
//	3 = forum
//	4 = social-log

import "context"

// MediaCheckResult is the immediate response from enqueuing an async image check.
// The actual verdict (pass / risky / review) arrives later via the push callback;
// this struct only carries the enqueue acknowledgement.
type MediaCheckResult struct {
	// TraceID is the WeChat trace identifier that the later callback echoes back.
	// Callers MUST persist it so the idempotent callback can correlate the verdict
	// to the withheld item. An empty TraceID means WeChat did not accept the job.
	TraceID string
}

// ImageModerator is the platform async image content-security interface.
// Callers depend only on this interface; the WeChat HTTP impl is internal to the
// wiring layer (cmd/api/main.go), mirroring the text Checker.
//
// appID is the WeChat mini-program AppID under which the check is performed.
// openID is the submitter's WeChat openID (required by v2 for user-context scoring).
// mediaURL is a publicly fetchable URL to the image bytes (WeChat fetches it).
// scene is the WeChat scene integer (1–4; 0 treated as 1 by the impl).
//
// Semantics of the returned (MediaCheckResult, error):
//   - err != nil → transport/token failure: caller MUST fail-open (allow + warn)
//   - err == nil, TraceID != "" → enqueued; persist TraceID + a pending verdict
//   - err == nil, TraceID == "" → WeChat rejected the job; treat as fail-open
type ImageModerator interface {
	CheckImageAsync(ctx context.Context, appID, openID, mediaURL string, scene int) (MediaCheckResult, error)
}

// DisabledImageModerator is a no-op ImageModerator that never enqueues a check.
// Used when WeChat message-push (callback) credentials are not configured
// (dev / test environments). Returns an empty TraceID so callers fail-open.
type DisabledImageModerator struct{}

// CheckImageAsync satisfies ImageModerator; always a no-op (empty TraceID).
func (DisabledImageModerator) CheckImageAsync(_ context.Context, _, _, _ string, _ int) (MediaCheckResult, error) {
	return MediaCheckResult{}, nil
}
