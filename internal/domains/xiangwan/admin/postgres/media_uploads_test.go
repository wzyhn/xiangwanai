package postgres

import (
	"strings"
	"testing"
	"time"

	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/google/uuid"
)

func TestReviewMediaIntentRestrictsTargetKindMIMEAndBytes(t *testing.T) {
	base := xiangwanadmin.IssueReviewMediaUploadCommand{
		OperationID: uuid.New(), InstanceID: uuid.New(),
		Kind: xiangwanadmin.ReviewMediaPhoto,
		MIME: "image/png", Size: 1024,
		SHA256: strings.Repeat("a", 64),
	}
	if !validReviewMediaCommand(base) {
		t.Fatal("valid private photo intent rejected")
	}
	tests := []struct {
		name   string
		change func(*xiangwanadmin.IssueReviewMediaUploadCommand)
	}{
		{"bad operation key", func(v *xiangwanadmin.IssueReviewMediaUploadCommand) {
			v.OperationID = uuid.NewSHA1(uuid.NameSpaceURL, []byte("file"))
		}},
		{"wrong media kind", func(v *xiangwanadmin.IssueReviewMediaUploadCommand) { v.Kind = xiangwanadmin.ReviewMediaVideo }},
		{"active content", func(v *xiangwanadmin.IssueReviewMediaUploadCommand) { v.MIME = "image/svg+xml" }},
		{"photo too large", func(v *xiangwanadmin.IssueReviewMediaUploadCommand) { v.Size = 10<<20 + 1 }},
		{"wrong checksum case", func(v *xiangwanadmin.IssueReviewMediaUploadCommand) { v.SHA256 = strings.Repeat("A", 64) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := base
			test.change(&value)
			if validReviewMediaCommand(value) {
				t.Fatalf("unsafe media intent accepted: %+v", value)
			}
		})
	}
}

func TestReviewMediaStageWindowClosesForConfirmedReplayToo(t *testing.T) {
	t.Parallel()
	expiresAt := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	if !reviewMediaStageWindowOpen(expiresAt, expiresAt) ||
		reviewMediaStageWindowOpen(expiresAt.Add(time.Nanosecond), expiresAt) ||
		reviewMediaStageWindowOpen(time.Time{}, expiresAt) {
		t.Fatal("staging window did not close at expiry")
	}
}

func TestReviewMediaFilenameIsDerivedOnlyFromFileIDAndAllowedMIME(t *testing.T) {
	t.Parallel()
	fileID := uuid.New()
	for mime, extension := range map[string]string{
		"image/jpeg": "jpg", "image/png": "png", "image/webp": "webp",
		"video/mp4": "mp4", "audio/mpeg": "mp3", "application/pdf": "pdf",
	} {
		if got := reviewMediaFilename(fileID, mime); got != "review-"+fileID.String()+"."+extension {
			t.Errorf("filename for %s = %q", mime, got)
		}
	}
	if got := reviewMediaFilename(fileID, "image/svg+xml"); got != "" {
		t.Fatalf("active-content filename = %q", got)
	}
}
