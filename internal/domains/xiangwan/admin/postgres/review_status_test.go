package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/resource"
	"github.com/google/uuid"
)

type reviewStatusResourceReader struct {
	instancePublished []resource.PublishedResource
	sessionPublished  map[uuid.UUID][]resource.PublishedResource
	reviews           map[uuid.UUID]resource.PublicReviewDetail
	calledTargets     []resource.PastHighlightReviewTarget
}

func (reader *reviewStatusResourceReader) ListPublishedInstanceReview(
	context.Context, uuid.UUID, uuid.UUID,
) ([]resource.PublishedResource, error) {
	return reader.instancePublished, nil
}

func (reader *reviewStatusResourceReader) ListPublishedSessionResources(
	_ context.Context, _ uuid.UUID, sessionID uuid.UUID, _ *uuid.UUID,
) ([]resource.PublishedResource, error) {
	return reader.sessionPublished[sessionID], nil
}

func (reader *reviewStatusResourceReader) ReadPublicReview(
	_ context.Context,
	_ uuid.UUID,
	target resource.PastHighlightReviewTarget,
	_ resource.ExternalDomainPolicy,
) (resource.PublicReviewDetail, error) {
	reader.calledTargets = append(reader.calledTargets, target)
	return reader.reviews[targetKey(target)], nil
}

func targetKey(target resource.PastHighlightReviewTarget) uuid.UUID {
	if target.SessionID != nil {
		return *target.SessionID
	}
	return target.InstanceID
}

func publishedAt(value time.Time) resource.PublishedResource {
	return resource.PublishedResource{PublishedAt: value}
}

func TestReadInstanceReviewStatusUsesPublicReaderEvidence(t *testing.T) {
	t.Parallel()

	tenantID := uuid.New()
	seriesID := uuid.New()
	instanceID := uuid.New()
	sessionID := uuid.New()
	instance := activity.Instance{
		TenantID: tenantID, SeriesID: seriesID, ID: instanceID,
		Status: activity.InstanceStatusCompleted,
	}
	session := activity.Session{ID: sessionID, Status: activity.SessionStatusArchived}
	published := time.Date(2026, time.September, 27, 8, 30, 0, 0, time.UTC)
	reader := &reviewStatusResourceReader{
		instancePublished: []resource.PublishedResource{publishedAt(published)},
		sessionPublished:  map[uuid.UUID][]resource.PublishedResource{sessionID: {publishedAt(published.Add(time.Hour))}},
		reviews: map[uuid.UUID]resource.PublicReviewDetail{
			instanceID: {Documents: []resource.PublicReviewDocument{{Kind: resource.RelationKindInstanceReview}}},
			sessionID: {
				Documents: []resource.PublicReviewDocument{
					{Kind: resource.RelationKindInstanceReview},
					{Kind: resource.RelationKindSessionResources},
				},
			},
		},
	}
	policy, err := resource.NewExternalDomainPolicy([]string{"video.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	status, err := readInstanceReviewStatus(
		context.Background(), tenantID, instance, []activity.Session{session}, policy, reader,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !status.PublicReviewAvailable || status.InstanceReviewDocumentCount != 1 ||
		status.PublicSessionResourceCount != 1 || len(status.Sessions) != 1 ||
		!status.Sessions[0].PublicReviewAvailable || status.Sessions[0].PublicResourceCount != 1 ||
		status.LatestPublishedAt == nil || !status.LatestPublishedAt.Equal(published.Add(time.Hour)) {
		t.Fatalf("status=%+v", status)
	}
	if len(reader.calledTargets) != 2 || reader.calledTargets[1].SessionID == nil ||
		*reader.calledTargets[1].SessionID != sessionID {
		t.Fatalf("public review targets=%+v", reader.calledTargets)
	}
}

func TestReadInstanceReviewStatusDoesNotTreatPublicationRowsAsPublicEvidence(t *testing.T) {
	t.Parallel()

	instanceID := uuid.New()
	reader := &reviewStatusResourceReader{
		instancePublished: []resource.PublishedResource{{PublishedAt: time.Now().UTC()}},
		reviews:           map[uuid.UUID]resource.PublicReviewDetail{instanceID: {}},
	}
	status, err := readInstanceReviewStatus(
		context.Background(), uuid.New(), activity.Instance{
			ID: instanceID, SeriesID: uuid.New(), Status: activity.InstanceStatusCompleted,
		}, nil, resource.ExternalDomainPolicy{}, reader,
	)
	if err != nil {
		t.Fatal(err)
	}
	if status.PublicReviewAvailable || status.InstanceReviewDocumentCount != 0 {
		t.Fatalf("publication-only status=%+v", status)
	}
}

func TestReadInstanceReviewStatusKeepsPublishedInstanceIneligible(t *testing.T) {
	t.Parallel()

	instanceID := uuid.New()
	sessionID := uuid.New()
	status, err := readInstanceReviewStatus(
		context.Background(), uuid.New(), activity.Instance{
			ID: instanceID, SeriesID: uuid.New(), Status: activity.InstanceStatusPublished,
		}, []activity.Session{{ID: sessionID, Status: activity.SessionStatusPublished}}, resource.ExternalDomainPolicy{}, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if status.PublicReviewEligible || status.PublicReviewAvailable || len(status.Sessions) != 1 ||
		status.Sessions[0].PublicReviewAvailable || status.Sessions[0].PublicResourceCount != 0 {
		t.Fatalf("published status=%+v", status)
	}
}
