package activity

import (
	"encoding/hex"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestDigestInstancePublicationIsCanonical(t *testing.T) {
	t.Parallel()

	first := validPublicationCandidate()
	first.QuickTagCodes = []string{"local", "ai"}
	secondSession := first.Sessions[0]
	secondSession.SessionID = uuid.New()
	secondSession.Title = "Second session"
	secondSession.SessionStartAt = secondSession.SessionStartAt.Add(3 * time.Hour)
	secondSession.SessionEndAt = secondSession.SessionEndAt.Add(3 * time.Hour)
	first.Sessions = append(first.Sessions, secondSession)
	firstOrder := []uuid.UUID{first.Sessions[0].SessionID, first.Sessions[1].SessionID}

	second := first
	second.QuickTagCodes = []string{"ai", "local"}
	second.Sessions = []SessionPublicationCandidate{first.Sessions[1], first.Sessions[0]}
	offset := time.FixedZone("same instant", 8*60*60)
	for index := range second.Sessions {
		second.Sessions[index].RegistrationStartAt = second.Sessions[index].RegistrationStartAt.In(offset)
		second.Sessions[index].RegistrationEndAt = second.Sessions[index].RegistrationEndAt.In(offset)
		second.Sessions[index].SessionStartAt = second.Sessions[index].SessionStartAt.In(offset)
		second.Sessions[index].SessionEndAt = second.Sessions[index].SessionEndAt.In(offset)
	}

	firstDigest, err := DigestInstancePublication(first)
	if err != nil {
		t.Fatalf("DigestInstancePublication(first) error = %v", err)
	}
	secondDigest, err := DigestInstancePublication(second)
	if err != nil {
		t.Fatalf("DigestInstancePublication(second) error = %v", err)
	}
	if firstDigest != secondDigest {
		t.Fatalf("equivalent candidates produced %q and %q", firstDigest, secondDigest)
	}
	if len(firstDigest) != 64 {
		t.Fatalf("digest length = %d, want 64", len(firstDigest))
	}
	if _, err := hex.DecodeString(firstDigest); err != nil {
		t.Fatalf("digest is not lowercase hexadecimal: %q", firstDigest)
	}
	if first.Sessions[0].SessionID != firstOrder[0] || first.Sessions[1].SessionID != firstOrder[1] {
		t.Fatal("digest calculation mutated Session input order")
	}
}

func TestDigestInstancePublicationBindsPublicFacts(t *testing.T) {
	t.Parallel()

	original := validPublicationCandidate()
	changed := original
	changed.Sessions = append([]SessionPublicationCandidate(nil), original.Sessions...)
	changed.Sessions[0].PriceCents++

	originalDigest, err := DigestInstancePublication(original)
	if err != nil {
		t.Fatalf("DigestInstancePublication(original) error = %v", err)
	}
	changedDigest, err := DigestInstancePublication(changed)
	if err != nil {
		t.Fatalf("DigestInstancePublication(changed) error = %v", err)
	}
	if originalDigest == changedDigest {
		t.Fatalf("price change did not alter digest %q", originalDigest)
	}
}

func TestDigestInstancePublicationRejectsInvalidCandidate(t *testing.T) {
	t.Parallel()

	candidate := validPublicationCandidate()
	candidate.Sessions = nil
	if _, err := DigestInstancePublication(candidate); !errors.Is(err, ErrInvalidInstancePublication) {
		t.Fatalf("DigestInstancePublication() error = %v, want ErrInvalidInstancePublication", err)
	}
}

func TestValidateInstancePublicationRejectsNonFiniteCoordinates(t *testing.T) {
	t.Parallel()

	for _, coordinate := range []float64{math.NaN(), math.Inf(-1), math.Inf(1)} {
		candidate := validPublicationCandidate()
		candidate.Sessions[0].OfflineLocation.Longitude = floatPointer(coordinate)
		violations := ValidateInstancePublication(candidate)
		if !containsViolation(
			violations,
			"sessions[0].offline_location.longitude",
			ViolationOutOfRange,
		) {
			t.Fatalf("coordinate %v violations = %+v", coordinate, violations)
		}
	}
}
