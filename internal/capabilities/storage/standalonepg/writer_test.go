package standalonepg

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCanonicalMIMEAndVideoGate(t *testing.T) {
	if got, err := CanonicalMIME(" Video/MP4; codecs=avc1 "); err != nil || got != "video/mp4" {
		t.Fatalf("CanonicalMIME() = %q, %v", got, err)
	}
	if got, err := CanonicalVideoMIME("video/mp4"); err != nil || got != "video/mp4" {
		t.Fatalf("CanonicalVideoMIME() = %q, %v", got, err)
	}
	for _, value := range []string{"text/html", "image/svg+xml", "video/mp4\r\n"} {
		if _, err := CanonicalMIME(value); !errors.Is(err, ErrInvalidFile) {
			t.Fatalf("CanonicalMIME(%q) error = %v, want ErrInvalidFile", value, err)
		}
	}
	if _, err := CanonicalVideoMIME("image/png"); !errors.Is(err, ErrInvalidFile) {
		t.Fatalf("CanonicalVideoMIME(image/png) error = %v, want ErrInvalidFile", err)
	}
}

func TestNormalizePendingInputRejectsUnsafeOrIncompleteFacts(t *testing.T) {
	ownerID := uuid.New()
	base := PendingFileInput{
		ID: uuid.New(), PrincipalID: ownerID, Filename: "review.mp4",
		MIME: "video/mp4", Size: 128, ProviderObjectKey: "review/object-1",
		CreatedAt: time.Date(2026, 9, 28, 1, 0, 0, 0, time.UTC),
	}
	if normalized, err := normalizePendingInput(base); err != nil || normalized.DeleteAfter == nil {
		t.Fatalf("normalizePendingInput() = %+v, %v", normalized, err)
	}
	for name, mutate := range map[string]func(*PendingFileInput){
		"non-v4 id":        func(input *PendingFileInput) { input.ID = uuid.Nil },
		"missing owner":    func(input *PendingFileInput) { input.PrincipalID = uuid.Nil },
		"trimmed filename": func(input *PendingFileInput) { input.Filename = " review.mp4" },
		"traversal key":    func(input *PendingFileInput) { input.ProviderObjectKey = "review/../object-1" },
		"absolute key":     func(input *PendingFileInput) { input.ProviderObjectKey = "/review/object-1" },
		"url key":          func(input *PendingFileInput) { input.ProviderObjectKey = "https://bucket/object-1" },
		"backslash key":    func(input *PendingFileInput) { input.ProviderObjectKey = "review\\object-1" },
		"empty size":       func(input *PendingFileInput) { input.Size = 0 },
		"expired deadline": func(input *PendingFileInput) {
			deadline := base.CreatedAt
			input.DeleteAfter = &deadline
		},
	} {
		t.Run(name, func(t *testing.T) {
			input := base
			mutate(&input)
			if _, err := normalizePendingInput(input); !errors.Is(err, ErrInvalidFile) {
				t.Fatalf("normalizePendingInput() error = %v, want ErrInvalidFile", err)
			}
		})
	}
}

func TestStandaloneWriterRejectsInvalidRuntimeWithoutDatabaseAccess(t *testing.T) {
	if _, err := NewWriter(nil); !errors.Is(err, ErrInvalidWriter) {
		t.Fatalf("NewWriter(nil) error = %v, want ErrInvalidWriter", err)
	}
	var writer *Writer
	ownerID, fileID := uuid.New(), uuid.New()
	facts := ProviderObjectFacts{ProviderObjectKey: "review/object-1", MIME: "video/mp4", Size: 1}
	input := PendingFileInput{
		ID: uuid.New(), PrincipalID: ownerID, Filename: "review.mp4",
		MIME: "video/mp4", Size: 1, ProviderObjectKey: "review/object-1",
	}
	checks := []func() error{
		func() error { _, err := writer.CreatePending(context.Background(), input); return err },
		func() error { _, err := writer.Confirm(context.Background(), ownerID, fileID, facts); return err },
		func() error { _, err := writer.Pin(context.Background(), ownerID, fileID); return err },
		func() error { _, err := writer.GetConfirmed(context.Background(), ownerID, fileID); return err },
	}
	for index, check := range checks {
		if err := check(); !errors.Is(err, ErrInvalidWriter) {
			t.Fatalf("check %d error = %v, want ErrInvalidWriter", index, err)
		}
	}
}
