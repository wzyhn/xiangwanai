package fileurl

import (
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestBuildSignedAccessPathAndValidate(t *testing.T) {
	fileID := uuid.New()
	expiresAt := time.Unix(1_800_000_000, 0).UTC()
	secret := "test-secret"

	path := BuildSignedAccessPath(fileID, expiresAt, secret)
	if !strings.HasPrefix(path, "/api/v1/files/"+fileID.String()+"/content?exp=") {
		t.Fatalf("unexpected signed path: %q", path)
	}

	expUnix := expiresAt.Unix()
	sig := strings.TrimPrefix(path, "/api/v1/files/"+fileID.String()+"/content?exp="+strconv.FormatInt(expUnix, 10)+"&sig=")
	if !ValidateSignedAccess(fileID, expUnix, sig, secret, expiresAt.Add(-time.Minute)) {
		t.Fatal("expected signed path to validate")
	}
	if ValidateSignedAccess(fileID, expUnix, sig, "wrong-secret", expiresAt.Add(-time.Minute)) {
		t.Fatal("expected wrong secret to be rejected")
	}
	if ValidateSignedAccess(fileID, expUnix, sig, secret, expiresAt.Add(time.Second)) {
		t.Fatal("expected expired signature to be rejected")
	}
}

func TestValidateSignedAccessAnySupportsBoundedRotationWindow(t *testing.T) {
	fileID := uuid.New()
	expiresAt := time.Unix(1_800_000_000, 0).UTC()
	oldPath := BuildSignedAccessPath(fileID, expiresAt, "synthetic-file-key-a")
	expUnix := expiresAt.Unix()
	sig := strings.TrimPrefix(oldPath, "/api/v1/files/"+fileID.String()+"/content?exp="+strconv.FormatInt(expUnix, 10)+"&sig=")
	now := expiresAt.Add(-time.Minute)

	if !ValidateSignedAccessAny(fileID, expUnix, sig, []string{"synthetic-file-key-b", "synthetic-file-key-a"}, now) {
		t.Fatal("old signature must remain valid during the compatibility window")
	}
	if ValidateSignedAccessAny(fileID, expUnix, sig, []string{"synthetic-file-key-b"}, now) {
		t.Fatal("old signature must fail after old-key revocation")
	}
	if ValidateSignedAccessAny(fileID, expUnix, sig, []string{"synthetic-file-key-outsider"}, now) {
		t.Fatal("unauthorized key must not validate a signature")
	}
}

func TestSourceBoundAccessCoversSourceAndPurpose(t *testing.T) {
	fileID, contentID, blockID, principalID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	expiresAt := time.Unix(1_800_000_000, 0).UTC()
	path := BuildSignedSourceBoundAccessPath(fileID, contentID, blockID, "community/post_body", &principalID, expiresAt, "source-secret")
	parsed, err := url.Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Query().Get("content_id") != contentID.String() || parsed.Query().Get("block_id") != blockID.String() || parsed.Query().Get("purpose") != "community/post_body" {
		t.Fatalf("source binding was not encoded in query: %q", parsed.RawQuery)
	}
	if !ValidateSignedSourceBoundAccess(fileID, contentID, blockID, "community/post_body", &principalID, expiresAt.Unix(), parsed.Query().Get("sig"), []string{"source-secret"}, expiresAt.Add(-time.Minute)) {
		t.Fatal("expected source-bound signature to validate")
	}
	if ValidateSignedSourceBoundAccess(fileID, contentID, uuid.New(), "community/post_body", &principalID, expiresAt.Unix(), parsed.Query().Get("sig"), []string{"source-secret"}, expiresAt.Add(-time.Minute)) {
		t.Fatal("block replacement must invalidate the source-bound signature")
	}
	if ValidateSignedSourceBoundAccess(fileID, contentID, blockID, "community/comment_body", &principalID, expiresAt.Unix(), parsed.Query().Get("sig"), []string{"source-secret"}, expiresAt.Add(-time.Minute)) {
		t.Fatal("purpose retargeting must invalidate the source-bound signature")
	}
}

func TestPurposeBoundAccessCoversRoutePurposeResourcesAndOwner(t *testing.T) {
	feedbackID, fileID, principalID := uuid.New(), uuid.New(), uuid.New()
	expiresAt := time.Unix(1_800_000_000, 0).UTC()
	path := "/api/v1/admin/feedback/" + feedbackID.String() + "/image/" + fileID.String()
	purpose := "community/admin_feedback_image"
	signed := BuildSignedPurposeAccessPath(path, purpose, []uuid.UUID{feedbackID, fileID}, &principalID, expiresAt, "feedback-secret")
	parsed, err := url.Parse(signed)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Query().Get("purpose") != purpose || parsed.Query().Get("exp") != strconv.FormatInt(expiresAt.Unix(), 10) {
		t.Fatalf("purpose grant query = %q", parsed.RawQuery)
	}
	if !ValidateSignedPurposeAccess(path, purpose, []uuid.UUID{feedbackID, fileID}, &principalID, expiresAt.Unix(), parsed.Query().Get("sig"), []string{"feedback-secret"}, expiresAt.Add(-time.Minute)) {
		t.Fatal("expected purpose-bound signature to validate")
	}
	if ValidateSignedPurposeAccess(path, purpose, []uuid.UUID{feedbackID, uuid.New()}, &principalID, expiresAt.Unix(), parsed.Query().Get("sig"), []string{"feedback-secret"}, expiresAt.Add(-time.Minute)) {
		t.Fatal("file replacement must invalidate purpose-bound signature")
	}
	if ValidateSignedPurposeAccess(path, "other-purpose", []uuid.UUID{feedbackID, fileID}, &principalID, expiresAt.Unix(), parsed.Query().Get("sig"), []string{"feedback-secret"}, expiresAt.Add(-time.Minute)) {
		t.Fatal("purpose retargeting must invalidate purpose-bound signature")
	}
	otherOwner := uuid.New()
	if ValidateSignedPurposeAccess(path, purpose, []uuid.UUID{feedbackID, fileID}, &otherOwner, expiresAt.Unix(), parsed.Query().Get("sig"), []string{"feedback-secret"}, expiresAt.Add(-time.Minute)) {
		t.Fatal("owner retargeting must invalidate purpose-bound signature")
	}
}

func TestSourceBoundAccessRejectsEveryBoundFieldMutation(t *testing.T) {
	fileID, contentID, blockID, principalID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	expiresAt := time.Unix(1_800_000_000, 0).UTC()
	secret := "source-secret"
	path := BuildSignedSourceBoundAccessPath(fileID, contentID, blockID, "community/post_body", &principalID, expiresAt, secret)
	parsed, err := url.Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	sig := parsed.Query().Get("sig")
	now := expiresAt.Add(-time.Minute)
	tests := []struct {
		name      string
		fileID    uuid.UUID
		contentID uuid.UUID
		blockID   uuid.UUID
		purpose   string
		principal *uuid.UUID
		exp       int64
	}{
		{name: "file_id", fileID: uuid.New(), contentID: contentID, blockID: blockID, purpose: "community/post_body", principal: &principalID, exp: expiresAt.Unix()},
		{name: "content_id", fileID: fileID, contentID: uuid.New(), blockID: blockID, purpose: "community/post_body", principal: &principalID, exp: expiresAt.Unix()},
		{name: "block_id", fileID: fileID, contentID: contentID, blockID: uuid.New(), purpose: "community/post_body", principal: &principalID, exp: expiresAt.Unix()},
		{name: "purpose", fileID: fileID, contentID: contentID, blockID: blockID, purpose: "community/comment_body", principal: &principalID, exp: expiresAt.Unix()},
		{name: "principal_id", fileID: fileID, contentID: contentID, blockID: blockID, purpose: "community/post_body", principal: func() *uuid.UUID { id := uuid.New(); return &id }(), exp: expiresAt.Unix()},
		{name: "exp", fileID: fileID, contentID: contentID, blockID: blockID, purpose: "community/post_body", principal: &principalID, exp: expiresAt.Add(time.Minute).Unix()},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if ValidateSignedSourceBoundAccess(tc.fileID, tc.contentID, tc.blockID, tc.purpose, tc.principal, tc.exp, sig, []string{secret}, now) {
				t.Fatalf("mutation of %s unexpectedly validated", tc.name)
			}
		})
	}
}

func TestSourceBoundAccessRejectsMissingBlockAndBindsTenant(t *testing.T) {
	fileID, contentID, blockID, principalID, tenantID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	expiresAt := time.Unix(1_800_000_000, 0).UTC()
	path := BuildSignedSourceBoundAccessPathWithTenant(fileID, contentID, blockID, "community/post_body", &principalID, &tenantID, expiresAt, "source-secret")
	parsed, err := url.Parse(path)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Query().Get("tenant_id") != tenantID.String() {
		t.Fatalf("tenant binding missing from query: %q", parsed.RawQuery)
	}
	if !ValidateSignedSourceBoundAccessWithTenant(fileID, contentID, blockID, "community/post_body", &principalID, &tenantID, expiresAt.Unix(), parsed.Query().Get("sig"), []string{"source-secret"}, expiresAt.Add(-time.Minute)) {
		t.Fatal("expected tenant-bound signature to validate")
	}
	otherTenant := uuid.New()
	if ValidateSignedSourceBoundAccessWithTenant(fileID, contentID, blockID, "community/post_body", &principalID, &otherTenant, expiresAt.Unix(), parsed.Query().Get("sig"), []string{"source-secret"}, expiresAt.Add(-time.Minute)) {
		t.Fatal("tenant replacement must invalidate the source-bound signature")
	}
	if got := BuildSignedSourceBoundAccessPath(fileID, contentID, uuid.Nil, "community/post_body", &principalID, expiresAt, "source-secret"); got != "" {
		t.Fatalf("missing block must not produce a source-bound path: %q", got)
	}
}

func TestSignAvatarURL(t *testing.T) {
	fileID := uuid.New()
	secret := "avatar-secret"

	// A bare file access path gets a fresh exp+sig that validates.
	raw := "/api/v1/files/" + fileID.String() + "/content"
	signed := SignAvatarURL(raw, secret)
	parsed, ok := ParseAccessPathFileID(signed)
	if !ok || parsed != fileID {
		t.Fatalf("signed avatar url lost the file id: %q", signed)
	}
	if !strings.Contains(signed, "exp=") || !strings.Contains(signed, "sig=") {
		t.Fatalf("expected exp+sig on signed avatar url, got %q", signed)
	}

	// A non-file URL (e.g. a wx avatar) passes through unchanged.
	wx := "https://thirdwx.qlogo.cn/abc/132"
	if got := SignAvatarURL(wx, secret); got != wx {
		t.Errorf("wx avatar url must pass through unchanged, got %q", got)
	}
	// Empty passes through.
	if got := SignAvatarURL("", secret); got != "" {
		t.Errorf("empty avatar must pass through, got %q", got)
	}
	// Empty secret → unsigned access path (no exp/sig), still loadable via content_id/owner.
	if got := SignAvatarURL(raw, ""); got != BuildAccessPath(fileID, nil) {
		t.Errorf("empty secret should yield unsigned access path, got %q", got)
	}
}

func TestParseAccessPathFileID(t *testing.T) {
	fileID := uuid.New()
	tests := []struct {
		name  string
		raw   string
		valid bool
	}{
		{name: "relative path", raw: "/api/v1/files/" + fileID.String() + "/content", valid: true},
		{name: "relative path with query", raw: "/api/v1/files/" + fileID.String() + "/content?exp=1&sig=test", valid: true},
		{name: "absolute url", raw: "https://api.example.com/api/v1/files/" + fileID.String() + "/content?exp=1&sig=test", valid: true},
		{name: "invalid path", raw: "/api/v1/media/" + fileID.String(), valid: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			parsed, ok := ParseAccessPathFileID(tc.raw)
			if ok != tc.valid {
				t.Fatalf("expected valid=%v, got %v", tc.valid, ok)
			}
			if tc.valid && parsed != fileID {
				t.Fatalf("expected file id %s, got %s", fileID, parsed)
			}
		})
	}
}
