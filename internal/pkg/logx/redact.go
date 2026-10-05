// Package logx — PII redaction helpers.
//
// docs/standards/logging-pii.md is the authoritative list of fields that MUST
// pass through these helpers before reaching a zap logger. The lint rule
// (forbidigo in .golangci.yml) blocks zap.String("openid"|"unionid"|...) in
// production code paths so the helpers cannot be silently bypassed.
//
// Determinism: the same input value always produces the same hash output, so
// log lines for the same principal can still be correlated across requests
// without exposing the cleartext identifier. Empty input maps to an empty
// string (NOT a hash of "") so absent values stay visibly absent.
package logx

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"go.uber.org/zap"
)

// redactedHashLen is how many leading hex chars of the SHA256 digest we
// emit. 8 chars (32 bits) keeps log lines short while making accidental
// collision negligible at the per-tenant scale; the suffix "..." signals
// truncation to readers.
const redactedHashLen = 8

// RedactedString returns a zap.Field whose value is the SHA256(value)[:8]
// hex prefix followed by "..." — e.g., RedactedString("openid", "o_xyz")
// emits {"openid": "ab12cd34..."}. Empty input returns zap.String(name, "")
// so missing fields stay visibly missing instead of hashing the empty
// string (which would otherwise produce a constant non-empty hash leaking
// "field present but empty" vs "field absent").
func RedactedString(name, value string) zap.Field {
	if value == "" {
		return zap.String(name, "")
	}
	sum := sha256.Sum256([]byte(value))
	return zap.String(name, hex.EncodeToString(sum[:])[:redactedHashLen]+"...")
}

// MaskPhone returns a zap.Field that keeps the leading 3 + trailing 4 chars
// of a phone number visible and masks the middle with "****", e.g.
// "13912345678" -> "139****5678". Useful for support workflows where an
// operator must visually confirm a user-reported phone without the log
// itself storing the full number. Non-empty inputs shorter than 7 chars
// fall back to RedactedString to avoid the mask leaking nearly the whole
// value.
func MaskPhone(name, value string) zap.Field {
	v := strings.TrimSpace(value)
	if v == "" {
		return zap.String(name, "")
	}
	if len(v) < 7 {
		return RedactedString(name, v)
	}
	return zap.String(name, v[:3]+"****"+v[len(v)-4:])
}

// MaskEmail keeps the first char of the local-part and the full domain
// visible, masking the rest of the local-part: "alice@x.com" -> "a***@x.com".
// Inputs without exactly one "@" fall back to RedactedString.
func MaskEmail(name, value string) zap.Field {
	v := strings.TrimSpace(value)
	if v == "" {
		return zap.String(name, "")
	}
	at := strings.IndexByte(v, '@')
	// reject 0 (no local-part), -1 (no @), or multiple @
	if at <= 0 || strings.Count(v, "@") != 1 || at == len(v)-1 {
		return RedactedString(name, v)
	}
	return zap.String(name, v[:1]+"***"+v[at:])
}
