package xiangwanapi

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/resource"
	"github.com/google/uuid"
)

func TestPublicReviewServiceComposesExactReviewAndNextRoute(t *testing.T) {
	t.Parallel()

	tenantID := apiUUID(1)
	seriesID := apiUUID(2)
	instanceID := apiUUID(3)
	sessionID := apiUUID(4)
	nextInstanceID := apiUUID(5)
	nextSessionA := apiUUID(6)
	nextSessionB := apiUUID(7)
	target := resource.PastHighlightReviewTarget{
		SeriesID:   seriesID,
		InstanceID: instanceID,
		SessionID:  &sessionID,
	}
	summary := publicReviewSummary(seriesID, instanceID)
	detail := resource.PublicReviewDetail{
		Target: target,
		Documents: []resource.PublicReviewDocument{
			{
				RelationID: apiUUID(8),
				ContentID:  apiUUID(9),
				Kind:       resource.RelationKindInstanceReview,
			},
			{
				RelationID: apiUUID(10),
				ContentID:  apiUUID(11),
				SessionID:  &sessionID,
				Kind:       resource.RelationKindSessionResources,
			},
		},
	}
	nextRoute := activity.NextInstanceRouteResolution{
		SeriesID:         seriesID,
		SourceInstanceID: instanceID,
		TargetInstanceID: &nextInstanceID,
		SessionRoute: activity.SessionRouteResolution{
			Kind: activity.SessionRouteSelectionRequired,
			CandidateSessionIDs: []uuid.UUID{
				nextSessionA,
				nextSessionB,
			},
		},
	}
	fake := &fakePublicReviewReads{
		target:  target,
		summary: summary,
		contentBlocks: []activity.DetailBlock{
			{Type: activity.DetailBlockTypeText, Title: "简介", Body: "本期内容"},
		},
		detail: detail,
		next:   nextRoute,
	}
	policy, _ := resource.NewExternalDomainPolicy([]string{"docs.example.com"})
	service, err := NewPublicReviewService(
		tenantID,
		fake,
		fake,
		fake,
		fake,
		policy,
		publicReviewAsOf,
	)
	if err != nil {
		t.Fatalf("NewPublicReviewService() error = %v", err)
	}

	page, err := service.ReadInstanceReview(
		context.Background(),
		instanceID,
		&sessionID,
	)
	if err != nil || !reflect.DeepEqual(page.Activity, summary) ||
		!reflect.DeepEqual(page.ContentBlocks, fake.contentBlocks) ||
		!reflect.DeepEqual(page.Review, detail) ||
		!reflect.DeepEqual(page.NextRoute, nextRoute) {
		t.Fatalf("ReadInstanceReview() = %+v, %v", page, err)
	}
	if fake.tenantID != tenantID || fake.instanceID != instanceID ||
		fake.sessionID == nil || *fake.sessionID != sessionID ||
		fake.summaryInstanceID != instanceID ||
		!fake.summaryAt.Equal(publicReviewAsOf()) ||
		fake.readTarget.InstanceID != instanceID ||
		fake.nextSourceID != instanceID ||
		!fake.targetAt.Equal(publicReviewAsOf()) {
		t.Fatalf("reader calls = %+v", fake)
	}
}

func TestPublicReviewServiceRejectsInvalidOrCrossContextFacts(t *testing.T) {
	t.Parallel()

	tenantID := apiUUID(20)
	instanceID := apiUUID(21)
	target := resource.PastHighlightReviewTarget{
		SeriesID:   apiUUID(22),
		InstanceID: instanceID,
	}
	fake := &fakePublicReviewReads{
		target:        target,
		summary:       publicReviewSummary(target.SeriesID, instanceID),
		contentBlocks: []activity.DetailBlock{},
		detail:        resource.PublicReviewDetail{Target: target},
		next: activity.NextInstanceRouteResolution{
			SeriesID:         target.SeriesID,
			SourceInstanceID: instanceID,
			SessionRoute: activity.SessionRouteResolution{
				Kind: activity.SessionRouteUnavailable,
			},
		},
	}
	policy, _ := resource.NewExternalDomainPolicy(nil)
	service, _ := NewPublicReviewService(
		tenantID,
		fake,
		fake,
		fake,
		fake,
		policy,
		publicReviewAsOf,
	)
	if _, err := service.ReadInstanceReview(
		context.Background(),
		uuid.Nil,
		nil,
	); !errors.Is(err, ErrInvalidPublicReviewRequest) {
		t.Fatalf("ReadInstanceReview(invalid) error = %v", err)
	}

	fake.detail.Target.SeriesID = uuid.New()
	if _, err := service.ReadInstanceReview(
		context.Background(),
		instanceID,
		nil,
	); !errors.Is(err, ErrPublicReviewResponseConflict) {
		t.Fatalf("ReadInstanceReview(cross context) error = %v", err)
	}
}

func TestPublicReviewServicePreservesUnavailableTarget(t *testing.T) {
	t.Parallel()

	fake := &fakePublicReviewReads{
		targetErr: resource.ErrPublicReviewTargetUnavailable,
	}
	policy, _ := resource.NewExternalDomainPolicy(nil)
	service, _ := NewPublicReviewService(
		apiUUID(30),
		fake,
		fake,
		fake,
		fake,
		policy,
		publicReviewAsOf,
	)
	_, err := service.ReadInstanceReview(
		context.Background(),
		apiUUID(31),
		nil,
	)
	if !errors.Is(err, resource.ErrPublicReviewTargetUnavailable) {
		t.Fatalf("ReadInstanceReview(unavailable) error = %v", err)
	}
}

func TestNewPublicReviewServiceRequiresAllWiring(t *testing.T) {
	t.Parallel()

	policy, _ := resource.NewExternalDomainPolicy(nil)
	if _, err := NewPublicReviewService(
		uuid.Nil,
		nil,
		nil,
		nil,
		nil,
		policy,
		publicReviewAsOf,
	); !errors.Is(err, ErrInvalidPublicReviewService) {
		t.Fatalf("NewPublicReviewService(invalid) error = %v", err)
	}
}

type fakePublicReviewReads struct {
	target        resource.PastHighlightReviewTarget
	summary       activity.PastActivityItem
	contentBlocks []activity.DetailBlock
	detail        resource.PublicReviewDetail
	next          activity.NextInstanceRouteResolution

	targetErr  error
	summaryErr error
	detailErr  error
	nextErr    error

	tenantID          uuid.UUID
	instanceID        uuid.UUID
	sessionID         *uuid.UUID
	summaryInstanceID uuid.UUID
	summaryAt         time.Time
	readTarget        resource.PastHighlightReviewTarget
	nextSourceID      uuid.UUID
	targetAt          time.Time
}

func (fake *fakePublicReviewReads) ReadPastActivity(
	_ context.Context,
	_ uuid.UUID,
	instanceID uuid.UUID,
	at time.Time,
) (activity.PastActivityItem, error) {
	fake.summaryInstanceID = instanceID
	fake.summaryAt = at
	return fake.summary, fake.summaryErr
}

func (fake *fakePublicReviewReads) ReadPastActivityDetailBlocks(
	_ context.Context,
	_ uuid.UUID,
	_ uuid.UUID,
	_ time.Time,
) ([]activity.DetailBlock, error) {
	return fake.contentBlocks, fake.summaryErr
}

func (fake *fakePublicReviewReads) ResolvePublicReviewTarget(
	_ context.Context,
	tenantID uuid.UUID,
	instanceID uuid.UUID,
	sessionID *uuid.UUID,
	at time.Time,
) (resource.PastHighlightReviewTarget, error) {
	fake.tenantID = tenantID
	fake.instanceID = instanceID
	fake.sessionID = sessionID
	fake.targetAt = at
	return fake.target, fake.targetErr
}

func (fake *fakePublicReviewReads) ReadPublicReview(
	_ context.Context,
	_ uuid.UUID,
	target resource.PastHighlightReviewTarget,
	_ resource.ExternalDomainPolicy,
) (resource.PublicReviewDetail, error) {
	fake.readTarget = target
	return fake.detail, fake.detailErr
}

func (fake *fakePublicReviewReads) ResolveNextInstanceSessionRoute(
	_ context.Context,
	_ uuid.UUID,
	sourceInstanceID uuid.UUID,
) (activity.NextInstanceRouteResolution, error) {
	fake.nextSourceID = sourceInstanceID
	return fake.next, fake.nextErr
}

func apiUUID(lastByte byte) uuid.UUID {
	var value uuid.UUID
	value[len(value)-1] = lastByte
	return value
}

func publicReviewSummary(
	seriesID uuid.UUID,
	instanceID uuid.UUID,
) activity.PastActivityItem {
	completedAt := time.Date(2026, time.September, 12, 4, 0, 0, 0, time.UTC)
	return activity.PastActivityItem{
		SeriesID:                         seriesID,
		SeriesTitle:                      "AI Community Nights",
		SuccessfulPublishedInstanceCount: 3,
		HistoricalRegistrationCount:      42,
		InstanceID:                       instanceID,
		InstanceTitle:                    "September Night",
		InstanceStatus:                   activity.InstanceStatusCompleted,
		ActivityType:                     activity.ActivityTypeAIRoundtable,
		PublicationVersion:               2,
		PublishedAt:                      completedAt.Add(-24 * time.Hour),
		CompletedAt:                      completedAt,
	}
}

func publicReviewAsOf() time.Time {
	return time.Date(2026, time.September, 13, 9, 0, 0, 0, time.UTC)
}
