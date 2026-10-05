package resource

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestValidatePublicFileGrantAcceptsExactPublicMedia(t *testing.T) {
	t.Parallel()

	instanceGrant := knownPublicFileGrant()
	if err := ValidatePublicFileGrant(instanceGrant); err != nil {
		t.Fatalf("ValidatePublicFileGrant(Instance) error = %v", err)
	}
	sessionID := uuid.New()
	sessionGrant := instanceGrant
	sessionGrant.Kind = RelationKindSessionResources
	sessionGrant.SessionID = &sessionID
	sessionGrant.BlockType = PublicReviewBlockTypeAudio
	sessionGrant.MIME = "audio/mpeg"
	if err := ValidatePublicFileGrant(sessionGrant); err != nil {
		t.Fatalf("ValidatePublicFileGrant(Session) error = %v", err)
	}
}

func TestValidatePublicFileGrantRejectsMalformedOrActiveContent(t *testing.T) {
	t.Parallel()

	valid := knownPublicFileGrant()
	tests := []struct {
		name   string
		mutate func(*PublicFileGrant)
	}{
		{name: "missing tenant", mutate: func(value *PublicFileGrant) { value.TenantID = uuid.Nil }},
		{name: "missing file", mutate: func(value *PublicFileGrant) { value.FileID = uuid.Nil }},
		{name: "empty file", mutate: func(value *PublicFileGrant) { value.Size = 0 }},
		{name: "wrong media MIME", mutate: func(value *PublicFileGrant) { value.MIME = "application/pdf" }},
		{name: "active SVG", mutate: func(value *PublicFileGrant) { value.MIME = "image/svg+xml" }},
		{name: "Session on Instance review", mutate: func(value *PublicFileGrant) {
			sessionID := uuid.New()
			value.SessionID = &sessionID
		}},
		{name: "missing Session binding", mutate: func(value *PublicFileGrant) {
			value.Kind = RelationKindSessionResources
		}},
		{name: "non-media Block", mutate: func(value *PublicFileGrant) {
			value.BlockType = PublicReviewBlockTypeLink
			value.MIME = "application/pdf"
		}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			value := valid
			test.mutate(&value)
			if err := ValidatePublicFileGrant(value); !errors.Is(
				err,
				ErrInvalidPublicFileGrant,
			) {
				t.Fatalf("ValidatePublicFileGrant() error = %v", err)
			}
		})
	}
}

func knownPublicFileGrant() PublicFileGrant {
	return PublicFileGrant{
		TenantID:   uuid.New(),
		RelationID: uuid.New(),
		ContentID:  uuid.New(),
		BlockID:    uuid.New(),
		FileID:     uuid.New(),
		SeriesID:   uuid.New(),
		InstanceID: uuid.New(),
		Kind:       RelationKindInstanceReview,
		BlockType:  PublicReviewBlockTypeImage,
		MIME:       "image/webp",
		Size:       128,
	}
}
