package xiangwanapi

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/people"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/resource"
	"github.com/google/uuid"
)

var (
	ErrInvalidPublicSessionDetailService = errors.New(
		"invalid xiangwan public Session detail service",
	)
	ErrInvalidPublicSessionDetailRequest = errors.New(
		"invalid xiangwan public Session detail request",
	)
	ErrPublicSessionDetailConflict = errors.New(
		"xiangwan public Session detail conflict",
	)
)

type publicSessionDetailReader interface {
	ReadSessionDetail(
		context.Context,
		uuid.UUID,
		uuid.UUID,
		time.Time,
	) (activity.SessionDetail, error)
}

type publicSessionLeaderReader interface {
	ListInstanceSessionLeaders(
		context.Context,
		uuid.UUID,
		uuid.UUID,
	) ([]people.SessionLeaderFacts, error)
}

type publicPreviousReviewReader interface {
	ReadPreviousInstanceReview(
		context.Context,
		uuid.UUID,
		uuid.UUID,
		uuid.UUID,
	) (resource.PreviousReviewFacts, error)
}

type publicPastHighlightsReader interface {
	resource.PublicReviewReader
	ReadPastHighlightContext(context.Context, uuid.UUID, uuid.UUID) (*resource.PastHighlightContext, error)
}

type publicPastHighlightActivityReader interface {
	ReadPastActivity(context.Context, uuid.UUID, uuid.UUID, time.Time) (activity.PastActivityItem, error)
}

type PublicSessionDetailPage struct {
	BrandStatus activity.BrandLifecycleStatus
	Detail      activity.SessionDetail
	// QuickTags is the published BrandProfile vocabulary. The Session keeps
	// only tag codes in its immutable publication; carrying this read-only
	// vocabulary on the detail page lets clients render configured labels while
	// retaining quick_tag_codes for unknown/stale-code fallbacks.
	QuickTags      []activity.HomeQuickTag
	Leaders        []people.SessionLeader
	PreviousReview *resource.PreviousInstanceReview
}

type PublicSessionDetailService struct {
	tenantID             uuid.UUID
	profileReader        publicHomeProfileReader
	detailReader         publicSessionDetailReader
	leaderReader         publicSessionLeaderReader
	previousReviewReader publicPreviousReviewReader
	highlightsReader     publicPastHighlightsReader
	pastActivityReader   publicPastHighlightActivityReader
	externalDomains      resource.ExternalDomainPolicy
	now                  func() time.Time
}

// The production path selects one historical Session, then uses the same
// public review reader and target as the full review page.
func NewPublicSessionDetailServiceWithPastHighlights(
	tenantID uuid.UUID, profileReader publicHomeProfileReader,
	detailReader publicSessionDetailReader, leaderReader publicSessionLeaderReader,
	highlightsReader publicPastHighlightsReader, pastActivityReader publicPastHighlightActivityReader,
	externalDomains resource.ExternalDomainPolicy, now func() time.Time,
) (*PublicSessionDetailService, error) {
	if tenantID == uuid.Nil || profileReader == nil || detailReader == nil ||
		leaderReader == nil || highlightsReader == nil || pastActivityReader == nil || now == nil {
		return nil, ErrInvalidPublicSessionDetailService
	}
	return &PublicSessionDetailService{tenantID: tenantID, profileReader: profileReader,
		detailReader: detailReader, leaderReader: leaderReader, highlightsReader: highlightsReader,
		pastActivityReader: pastActivityReader, externalDomains: externalDomains, now: now}, nil
}

func NewPublicSessionDetailService(
	tenantID uuid.UUID,
	profileReader publicHomeProfileReader,
	detailReader publicSessionDetailReader,
	leaderReader publicSessionLeaderReader,
	previousReviewReader publicPreviousReviewReader,
	now func() time.Time,
) (*PublicSessionDetailService, error) {
	if tenantID == uuid.Nil || profileReader == nil || detailReader == nil ||
		leaderReader == nil || previousReviewReader == nil || now == nil {
		return nil, ErrInvalidPublicSessionDetailService
	}
	return &PublicSessionDetailService{
		tenantID:             tenantID,
		profileReader:        profileReader,
		detailReader:         detailReader,
		leaderReader:         leaderReader,
		previousReviewReader: previousReviewReader,
		now:                  now,
	}, nil
}

// NewPublicSessionDetailServiceWithExternalDomains enables preview images
// backed by administrator-supplied URLs only when the current policy allows
// their host. The original constructor keeps file-backed callers compatible.
func NewPublicSessionDetailServiceWithExternalDomains(
	tenantID uuid.UUID,
	profileReader publicHomeProfileReader,
	detailReader publicSessionDetailReader,
	leaderReader publicSessionLeaderReader,
	previousReviewReader publicPreviousReviewReader,
	externalDomains resource.ExternalDomainPolicy,
	now func() time.Time,
) (*PublicSessionDetailService, error) {
	service, err := NewPublicSessionDetailService(
		tenantID, profileReader, detailReader, leaderReader, previousReviewReader, now,
	)
	if err != nil {
		return nil, err
	}
	service.externalDomains = externalDomains
	return service, nil
}

func (service *PublicSessionDetailService) ReadSessionDetail(
	ctx context.Context,
	sessionID uuid.UUID,
) (PublicSessionDetailPage, error) {
	if service == nil || service.tenantID == uuid.Nil ||
		service.profileReader == nil || service.detailReader == nil ||
		service.leaderReader == nil || (service.previousReviewReader == nil &&
		(service.highlightsReader == nil || service.pastActivityReader == nil)) ||
		service.now == nil {
		return PublicSessionDetailPage{}, ErrInvalidPublicSessionDetailService
	}
	if sessionID == uuid.Nil {
		return PublicSessionDetailPage{}, ErrInvalidPublicSessionDetailRequest
	}
	profile, err := service.profileReader.ReadPublicHomeProfile(
		ctx,
		service.tenantID,
	)
	if err != nil {
		return PublicSessionDetailPage{}, fmt.Errorf(
			"read public Session BrandProfile: %w",
			err,
		)
	}
	if err := activity.ValidatePublicHomeProfile(profile); err != nil {
		return PublicSessionDetailPage{}, err
	}
	detail, err := service.detailReader.ReadSessionDetail(
		ctx,
		service.tenantID,
		sessionID,
		service.now(),
	)
	if err != nil {
		return PublicSessionDetailPage{}, fmt.Errorf(
			"read public Session detail: %w",
			err,
		)
	}
	if detail.SeriesID == uuid.Nil || detail.InstanceID == uuid.Nil ||
		detail.SessionID != sessionID {
		return PublicSessionDetailPage{}, ErrPublicSessionDetailConflict
	}
	leaderFacts, err := service.leaderReader.ListInstanceSessionLeaders(
		ctx,
		service.tenantID,
		detail.InstanceID,
	)
	if err != nil {
		return PublicSessionDetailPage{}, fmt.Errorf(
			"read public Session leaders: %w",
			err,
		)
	}
	previousReview, err := service.readPreviousReview(ctx, detail)
	if err != nil {
		return PublicSessionDetailPage{}, fmt.Errorf(
			"read public Session previous review: %w",
			err,
		)
	}
	if profile.LifecycleStatus == activity.BrandLifecycleSuspended {
		detail.CTA = activity.SessionDetailCTA{
			Action:  activity.SessionDetailCTAActionNone,
			Label:   activity.SessionDetailCTALabelBrandSuspended,
			Enabled: false,
		}
	}
	return PublicSessionDetailPage{
		BrandStatus:    profile.LifecycleStatus,
		Detail:         detail,
		QuickTags:      append([]activity.HomeQuickTag(nil), profile.AvailableQuickTags...),
		Leaders:        people.ProjectSessionLeaders(leaderFacts),
		PreviousReview: previousReview,
	}, nil
}

func (service *PublicSessionDetailService) readPreviousReview(ctx context.Context, detail activity.SessionDetail) (*resource.PreviousInstanceReview, error) {
	if service.highlightsReader == nil {
		facts, err := service.previousReviewReader.ReadPreviousInstanceReview(ctx, service.tenantID, detail.SeriesID, detail.InstanceID)
		return resource.ProjectPreviousInstanceReviewWithPolicy(facts, service.externalDomains), err
	}
	highlight, err := service.highlightsReader.ReadPastHighlightContext(ctx, service.tenantID, detail.InstanceID)
	if errors.Is(err, resource.ErrPastHighlightAnchorUnavailable) {
		return nil, nil // non-recurring Series has no preceding period
	}
	if err != nil || highlight == nil {
		return nil, err
	}
	asOf := service.now().UTC()
	if highlight.SeriesID != detail.SeriesID || highlight.AnchorInstanceID != detail.InstanceID ||
		highlight.PreviousInstanceID == detail.InstanceID || highlight.PreviousInstanceCompletedAt.After(asOf) {
		return nil, ErrPublicSessionDetailConflict
	}
	summary, err := service.pastActivityReader.ReadPastActivity(ctx, service.tenantID, highlight.PreviousInstanceID, asOf)
	if err != nil {
		return nil, err
	}
	if activity.ValidatePastActivityItem(summary) != nil || summary.SeriesID != highlight.SeriesID ||
		summary.InstanceID != highlight.PreviousInstanceID || !summary.CompletedAt.Equal(highlight.PreviousInstanceCompletedAt) {
		return nil, ErrPublicSessionDetailConflict
	}
	target := resource.PastHighlightReviewTarget{SeriesID: highlight.SeriesID, InstanceID: highlight.PreviousInstanceID, SessionID: highlight.FeaturedSessionID}
	review, err := service.highlightsReader.ReadPublicReview(ctx, service.tenantID, target, service.externalDomains)
	if err != nil {
		return nil, err
	}
	if review.Target.SeriesID != target.SeriesID || review.Target.InstanceID != target.InstanceID ||
		(review.Target.SessionID == nil) != (target.SessionID == nil) ||
		target.SessionID != nil && *review.Target.SessionID != *target.SessionID {
		return nil, ErrPublicSessionDetailConflict
	}
	facts := resource.PreviousReviewFacts{Instance: &resource.PreviousInstanceFacts{
		InstanceID: summary.InstanceID, Title: summary.InstanceTitle, CompletedAt: summary.CompletedAt},
		HasPublishedReview: len(review.Documents) > 0}
	for index, document := range review.Documents {
		for _, block := range document.Blocks {
			if block.Type != resource.PublicReviewBlockTypeImage || block.Availability != resource.PublicReviewBlockAvailable {
				continue
			}
			image := resource.PreviousReviewImageFacts{RelationID: document.RelationID, DocumentSortOrder: index,
				BlockID: block.BlockID, BlockSortOrder: block.SortOrder, ExternalURL: block.ExternalURL}
			if block.FileID != nil {
				image.FileID = *block.FileID
			}
			facts.Images = append(facts.Images, image)
		}
	}
	result := resource.ProjectPreviousInstanceReviewWithPolicy(facts, service.externalDomains)
	if result != nil {
		result.SessionID = target.SessionID
	}
	return result, nil
}
