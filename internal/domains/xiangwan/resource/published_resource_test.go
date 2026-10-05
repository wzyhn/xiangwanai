package resource

import (
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestValidatePublishedResourceAcceptsExactContexts(t *testing.T) {
	t.Parallel()

	review := validPublishedResource()
	if err := ValidatePublishedResource(review); err != nil {
		t.Fatalf("ValidatePublishedResource(review) error = %v", err)
	}

	sessionID := uuid.New()
	sessionResource := review
	sessionResource.SessionID = &sessionID
	sessionResource.Kind = RelationKindSessionResources
	sessionResource.AccessPolicy = AccessPolicyConfirmedRegistration
	if err := ValidatePublishedResource(sessionResource); err != nil {
		t.Fatalf("ValidatePublishedResource(Session) error = %v", err)
	}
}

func TestValidatePublishedResourceFailsClosed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*PublishedResource)
	}{
		{
			name: "publication absent",
			mutate: func(value *PublishedResource) {
				value.PublicationID = uuid.Nil
			},
		},
		{
			name: "snapshot schema unknown",
			mutate: func(value *PublishedResource) {
				value.SnapshotSchema = "legacy"
			},
		},
		{
			name: "digest empty",
			mutate: func(value *PublishedResource) {
				value.SubjectDigest = Digest{}
			},
		},
		{
			name: "Instance review is restricted",
			mutate: func(value *PublishedResource) {
				value.AccessPolicy = AccessPolicyConfirmedRegistration
			},
		},
		{
			name: "Instance review carries Session",
			mutate: func(value *PublishedResource) {
				sessionID := uuid.New()
				value.SessionID = &sessionID
			},
		},
		{
			name: "publication predates Content",
			mutate: func(value *PublishedResource) {
				value.PublishedAt = value.ContentRevision.Add(-time.Second)
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			value := validPublishedResource()
			test.mutate(&value)
			if err := ValidatePublishedResource(value); !errors.Is(
				err,
				ErrInvalidPublishedResource,
			) {
				t.Fatalf("ValidatePublishedResource() error = %v", err)
			}
		})
	}
}

func validPublishedResource() PublishedResource {
	now := time.Date(2026, time.September, 13, 17, 0, 0, 0, time.UTC)
	digest := sha256.Sum256([]byte("resource snapshot"))
	return PublishedResource{
		PublicationID:         uuid.New(),
		RelationID:            uuid.New(),
		TenantID:              uuid.New(),
		SeriesID:              uuid.New(),
		InstanceID:            uuid.New(),
		Kind:                  RelationKindInstanceReview,
		ContentID:             uuid.New(),
		ContentRevision:       now.Add(-time.Minute),
		AccessPolicy:          AccessPolicyPublic,
		SortOrder:             1,
		TargetVersion:         2,
		ApprovalObservationID: uuid.New(),
		SnapshotSchema:        ContentSnapshotSchema,
		SubjectDigest:         digest,
		PublishedAt:           now,
	}
}
